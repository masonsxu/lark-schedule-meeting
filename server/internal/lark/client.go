// Package lark 封装 lark-cli 子进程调用，提供飞书日历与会议室能力。
// 所有命令的返回均为 {ok, identity, data|error} JSON 信封。
package lark

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Room 一间会议室。
type Room struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Capacity int    `json:"capacity,omitempty"`
}

// Event 一个日历日程。
type Event struct {
	ID         string    `json:"id"`
	CalendarID string    `json:"calendarId,omitempty"`
	Summary    string    `json:"summary"`
	Start      time.Time `json:"start"`
	End        time.Time `json:"end"`
}

// Identity 当前授权身份。
type Identity struct {
	UserName string `json:"userName"`
	OpenID   string `json:"openId"`
	AppID    string `json:"appId"`
	Brand    string `json:"brand"`
}

// Client 飞书能力接口（可被测试替身替换）。
type Client interface {
	WhoAmI(ctx context.Context) (Identity, error)
	// RoomFind 查询时间块内可用的会议室。roomNames 为空表示不过滤名称。
	RoomFind(ctx context.Context, start, end time.Time, roomNames []string) ([]Room, error)
	// CreateEvent 创建日程，roomIDs 中的会议室将作为资源型参会人一并预定。
	CreateEvent(ctx context.Context, summary, description string, start, end time.Time, roomIDs []string) (*Event, error)
	// SearchEvents 在指定日期范围内按关键词（可为空）搜索日程，可按参会人过滤。
	SearchEvents(ctx context.Context, query string, startDay, endDay time.Time, attendeeIDs []string) ([]Event, error)
	// DeleteEvent 删除日程。
	DeleteEvent(ctx context.Context, calendarID, eventID string) error
}

// CLI 基于 lark-cli 子进程的实现。
type CLI struct {
	Path string
	// ExecCommand 可替换以做子进程级测试；为空时使用 exec.CommandContext。
	ExecCommand func(ctx context.Context, name string, args ...string) *exec.Cmd
}

// commandTimeout 单次子进程调用的超时。
const commandTimeout = 90 * time.Second

func (c *CLI) run(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	var cmd *exec.Cmd
	if c.ExecCommand != nil {
		cmd = c.ExecCommand(ctx, c.Path, args...)
	} else {
		cmd = exec.CommandContext(ctx, c.Path, args...)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("lark-cli %s 执行失败: %s", args[0], compactError(stderr.String(), err))
	}
	return stdout.Bytes(), nil
}

// compactError 从 stderr 提取可读错误：优先解析 JSON 信封中的 error 字段，避免原始 JSON 刷屏。
func compactError(stderr string, cause error) string {
	msg := strings.TrimSpace(stderr)
	if msg == "" {
		return cause.Error()
	}
	var env struct {
		Error *cliError `json:"error"`
	}
	if err := json.Unmarshal([]byte(msg), &env); err == nil && env.Error != nil {
		return truncate(env.Error.Error(), 300)
	}
	return truncate(msg, 300)
}

// envelope lark-cli 统一输出信封。
type envelope struct {
	OK    bool            `json:"ok"`
	Error *cliError       `json:"error,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
}

type cliError struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
	Message string `json:"message"`
	Code    int64  `json:"code"`
}

func (e *cliError) Error() string {
	if e.Code != 0 {
		return fmt.Sprintf("%s/%s (code %d): %s", e.Type, e.Subtype, e.Code, e.Message)
	}
	return fmt.Sprintf("%s/%s: %s", e.Type, e.Subtype, e.Message)
}

func decode(out []byte, data any) error {
	var env envelope
	if err := json.Unmarshal(out, &env); err != nil {
		return fmt.Errorf("lark-cli 输出不是合法 JSON: %s", truncate(string(out), 300))
	}
	if !env.OK {
		if env.Error != nil {
			return env.Error
		}
		return fmt.Errorf("lark-cli 调用未成功: %s", truncate(string(out), 300))
	}
	if data == nil || len(env.Data) == 0 {
		return nil
	}
	if err := json.Unmarshal(env.Data, data); err != nil {
		return fmt.Errorf("解析 lark-cli data 失败: %w", err)
	}
	return nil
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// isoTime 输出带时区偏移的 ISO 8601 时间（lark-cli 要求必须带偏移）。
func isoTime(t time.Time) string {
	return t.Format("2006-01-02T15:04:05-07:00")
}

// WhoAmI 查询当前授权身份。whoami 输出为顶层 JSON（无 ok 信封），单独解析。
func (c *CLI) WhoAmI(ctx context.Context) (Identity, error) {
	out, err := c.run(ctx, "whoami")
	if err != nil {
		return Identity{}, err
	}
	var data struct {
		OK         *bool     `json:"ok"`
		Error      *cliError `json:"error"`
		AppID      string    `json:"appId"`
		Brand      string    `json:"brand"`
		OnBehalfOf *struct {
			UserName string `json:"userName"`
			OpenID   string `json:"openId"`
		} `json:"onBehalfOf"`
	}
	if err := json.Unmarshal(out, &data); err != nil {
		return Identity{}, fmt.Errorf("解析 whoami 输出失败: %w", err)
	}
	if data.Error != nil {
		return Identity{}, data.Error
	}
	if data.OK != nil && !*data.OK {
		return Identity{}, fmt.Errorf("whoami 返回未成功: %s", truncate(string(out), 300))
	}
	id := Identity{AppID: data.AppID, Brand: data.Brand}
	if data.OnBehalfOf != nil {
		id.UserName = data.OnBehalfOf.UserName
		id.OpenID = data.OnBehalfOf.OpenID
	}
	return id, nil
}

// room-find 响应结构。
type roomFindData struct {
	TimeSlots []struct {
		Start        string    `json:"start"`
		End          string    `json:"end"`
		MeetingRooms []rawRoom `json:"meeting_rooms"`
		Hint         string    `json:"hint"`
	} `json:"time_slots"`
}

// rawRoom 兼容不同字段形态（容量可能为数字或字符串）。
type rawRoom struct {
	RoomID       string      `json:"room_id"`
	RoomName     string      `json:"room_name"`
	FallbackName string      `json:"name"`
	Capacity     json.Number `json:"capacity"`
}

func (r rawRoom) id() string { return r.RoomID }
func (r rawRoom) name() string {
	if r.RoomName != "" {
		return r.RoomName
	}
	return r.FallbackName
}
func (r rawRoom) capacity() int {
	if r.Capacity != "" {
		if n, err := r.Capacity.Int64(); err == nil {
			return int(n)
		}
	}
	return 0
}

// RoomFind 调用 calendar +room-find 查询时间块内的可用会议室。
func (c *CLI) RoomFind(ctx context.Context, start, end time.Time, roomNames []string) ([]Room, error) {
	args := []string{"calendar", "+room-find",
		"--slot", isoTime(start) + "~" + isoTime(end),
	}
	if len(roomNames) > 0 {
		args = append(args, "--room-name", strings.Join(roomNames, ","))
	}
	out, err := c.run(ctx, args...)
	if err != nil {
		return nil, err
	}
	var data roomFindData
	if err := decode(out, &data); err != nil {
		return nil, err
	}
	rooms := make([]Room, 0)
	seen := map[string]bool{}
	for _, slot := range data.TimeSlots {
		for _, r := range slot.MeetingRooms {
			if r.id() == "" || seen[r.id()] {
				continue
			}
			seen[r.id()] = true
			rooms = append(rooms, Room{ID: r.id(), Name: r.name(), Capacity: r.capacity()})
		}
	}
	return rooms, nil
}

// +create 响应结构。
type createData struct {
	EventID string `json:"event_id"`
	Summary string `json:"summary"`
	Start   string `json:"start"`
	End     string `json:"end"`
}

// CreateEvent 调用 calendar +create 创建日程；roomIDs 非空时追加为参会人。
func (c *CLI) CreateEvent(ctx context.Context, summary, description string, start, end time.Time, roomIDs []string) (*Event, error) {
	args := []string{"calendar", "+create",
		"--summary", summary,
		"--start", isoTime(start),
		"--end", isoTime(end),
	}
	if description != "" {
		args = append(args, "--description", description)
	}
	if len(roomIDs) > 0 {
		args = append(args, "--attendee-ids", strings.Join(roomIDs, ","))
	}
	out, err := c.run(ctx, args...)
	if err != nil {
		return nil, err
	}
	var data createData
	if err := decode(out, &data); err != nil {
		return nil, err
	}
	ev := &Event{ID: data.EventID, CalendarID: "primary", Summary: data.Summary}
	// 解析返回时间（失败不阻断，保留零值）。
	if t, err := time.Parse(time.RFC3339, data.Start); err == nil {
		ev.Start = t
	}
	if t, err := time.Parse(time.RFC3339, data.End); err == nil {
		ev.End = t
	}
	return ev, nil
}

// search-event 响应结构。
type searchData struct {
	Items []struct {
		EventID string `json:"event_id"`
		Summary string `json:"summary"`
		Start   struct {
			DateTime string `json:"date_time"`
		} `json:"start"`
		End struct {
			DateTime string `json:"date_time"`
		} `json:"end"`
	} `json:"items"`
}

// SearchEvents 调用 calendar +search-event 搜索日程（主日历视角）。
func (c *CLI) SearchEvents(ctx context.Context, query string, startDay, endDay time.Time, attendeeIDs []string) ([]Event, error) {
	args := []string{"calendar", "+search-event",
		"--start", startDay.Format("2006-01-02"),
		"--end", endDay.Format("2006-01-02"),
	}
	if query != "" {
		args = append(args, "--query", query)
	}
	if len(attendeeIDs) > 0 {
		args = append(args, "--attendee-ids", strings.Join(attendeeIDs, ","))
	}
	out, err := c.run(ctx, args...)
	if err != nil {
		return nil, err
	}
	var data searchData
	if err := decode(out, &data); err != nil {
		return nil, err
	}
	events := make([]Event, 0, len(data.Items))
	for _, it := range data.Items {
		ev := Event{ID: it.EventID, CalendarID: "primary", Summary: it.Summary}
		if t, err := time.Parse(time.RFC3339, it.Start.DateTime); err == nil {
			ev.Start = t
		}
		if t, err := time.Parse(time.RFC3339, it.End.DateTime); err == nil {
			ev.End = t
		}
		events = append(events, ev)
	}
	return events, nil
}

// DeleteEvent 删除日程（lark-cli 将删除标记为高风险写操作，需要 --yes）。
func (c *CLI) DeleteEvent(ctx context.Context, calendarID, eventID string) error {
	args := []string{"calendar", "+delete", "--event-id", eventID, "--yes"}
	if calendarID != "" {
		args = append(args, "--calendar-id", calendarID)
	}
	out, err := c.run(ctx, args...)
	if err != nil {
		return err
	}
	return decode(out, nil)
}
