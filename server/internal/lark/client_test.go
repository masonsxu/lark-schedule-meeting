package lark

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// newFixtureCLI 构造用 echo 回放固定 JSON 的 CLI 客户端。
func newFixtureCLI(fixture func(args []string) string) *CLI {
	c := &CLI{Path: "fake-lark-cli"}
	c.ExecCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "echo", fixture(args))
	}
	return c
}

const roomFindJSON = `{"ok":true,"identity":"user","data":{"time_slots":[
  {"start":"2026-10-21T10:00:00+08:00","end":"2026-10-21T11:00:00+08:00",
   "meeting_rooms":[
     {"room_id":"omm_1","room_name":"学清嘉创大厦B座-F2-02","capacity":7},
     {"room_id":"omm_2","room_name":"学清嘉创大厦B座-F3-05","capacity":"11"}
   ]}
]},"meta":{"count":1}}`

const createJSON = `{"ok":true,"identity":"user","data":{
  "end":"2026-10-21T11:00:00+08:00","event_id":"ev_123",
  "start":"2026-10-21T10:00:00+08:00","summary":"站会"}}`

const searchJSON = `{"ok":true,"identity":"user","data":{"calendar_id":"primary","items":[
  {"event_id":"ev_123","summary":"站会",
   "start":{"date_time":"2026-10-21T10:00:00+08:00","timezone":"Asia/Shanghai"},
   "end":{"date_time":"2026-10-21T11:00:00+08:00","timezone":"Asia/Shanghai"}}
],"has_more":false},"meta":{"count":1}}`

const whoamiJSON = `{"profile":"cli_x","appId":"cli_x","brand":"feishu","identity":"user",
 "available":true,"tokenStatus":"ready",
 "onBehalfOf":{"userName":"masonxu","openId":"ou_abc"}}`

const errJSON = `{"ok":false,"identity":"user","error":{
  "type":"authorization","subtype":"missing_scope","code":99991679,"message":"no scope"}}`

func TestWhoAmI(t *testing.T) {
	c := newFixtureCLI(func(args []string) string { return whoamiJSON })
	id, err := c.WhoAmI(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if id.UserName != "masonxu" || id.OpenID != "ou_abc" || id.Brand != "feishu" {
		t.Errorf("解析结果不符: %+v", id)
	}
}

func TestRoomFindParsesRoomsAndFlags(t *testing.T) {
	var gotArgs []string
	c := newFixtureCLI(func(args []string) string {
		gotArgs = args
		return roomFindJSON
	})
	loc, _ := time.LoadLocation("Asia/Shanghai")
	start := time.Date(2026, 10, 21, 10, 0, 0, 0, loc)
	end := start.Add(time.Hour)
	rooms, err := c.RoomFind(context.Background(), start, end, []string{"02", "05"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rooms) != 2 {
		t.Fatalf("应解析到 2 间会议室，得到 %d", len(rooms))
	}
	if rooms[0].ID != "omm_1" || rooms[0].Name != "学清嘉创大厦B座-F2-02" || rooms[0].Capacity != 7 {
		t.Errorf("第一间解析错误: %+v", rooms[0])
	}
	if rooms[1].Capacity != 11 {
		t.Errorf("字符串容量应解析为数字 11，得到 %d", rooms[1].Capacity)
	}
	joined := strings.Join(gotArgs, " ")
	if !strings.Contains(joined, "+room-find") || !strings.Contains(joined, "--room-name 02,05") {
		t.Errorf("命令参数不符: %v", gotArgs)
	}
	if !strings.Contains(joined, "--slot 2026-10-21T10:00:00+08:00~2026-10-21T11:00:00+08:00") {
		t.Errorf("slot 参数不符: %v", gotArgs)
	}
}

func TestCreateEventFlagsAndParse(t *testing.T) {
	var gotArgs []string
	c := newFixtureCLI(func(args []string) string {
		gotArgs = args
		return createJSON
	})
	loc, _ := time.LoadLocation("Asia/Shanghai")
	start := time.Date(2026, 10, 21, 10, 0, 0, 0, loc)
	ev, err := c.CreateEvent(context.Background(), "站会", "备注", start, start.Add(time.Hour), []string{"omm_1"})
	if err != nil {
		t.Fatal(err)
	}
	if ev.ID != "ev_123" || ev.CalendarID != "primary" {
		t.Errorf("事件解析错误: %+v", ev)
	}
	joined := strings.Join(gotArgs, " ")
	for _, want := range []string{"+create", "--summary 站会", "--attendee-ids omm_1", "--start 2026-10-21T10:00:00+08:00"} {
		if !strings.Contains(joined, want) {
			t.Errorf("缺少参数 %q: %v", want, gotArgs)
		}
	}
}

func TestSearchEventsParsesItems(t *testing.T) {
	c := newFixtureCLI(func(args []string) string { return searchJSON })
	loc, _ := time.LoadLocation("Asia/Shanghai")
	day := time.Date(2026, 10, 21, 0, 0, 0, 0, loc)
	events, err := c.SearchEvents(context.Background(), "站会", day, day, []string{"omm_1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("应解析到 1 条日程，得到 %d", len(events))
	}
	if events[0].Summary != "站会" || events[0].ID != "ev_123" {
		t.Errorf("日程解析错误: %+v", events[0])
	}
	if !events[0].Start.Equal(time.Date(2026, 10, 21, 10, 0, 0, 0, loc)) {
		t.Errorf("开始时间解析错误: %v", events[0].Start)
	}
}

func TestErrorEnvelope(t *testing.T) {
	c := newFixtureCLI(func(args []string) string { return errJSON })
	_, err := c.RoomFind(context.Background(), time.Now(), time.Now().Add(time.Hour), nil)
	if err == nil {
		t.Fatal("应当返回错误")
	}
	if !strings.Contains(err.Error(), "99991679") {
		t.Errorf("错误应包含 code 99991679: %v", err)
	}
}

func TestNonJSONOutput(t *testing.T) {
	c := &CLI{Path: "fake-lark-cli"}
	c.ExecCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "echo", "not json at all")
	}
	_, err := c.WhoAmI(context.Background())
	if err == nil || !strings.Contains(err.Error(), "解析 whoami 输出失败") {
		t.Fatalf("应报 JSON 解析错误，得到: %v", err)
	}
}
