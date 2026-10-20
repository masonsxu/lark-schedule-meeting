// Package engine 实现周期扫描与抢座会话：到点触发、每分钟一次、窗口 10 分钟、
// 记录已预约日期并在后续循环跳过。
package engine

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/masonsxu/lark-schedule-meeting/server/internal/lark"
	"github.com/masonsxu/lark-schedule-meeting/server/internal/model"
	"github.com/masonsxu/lark-schedule-meeting/server/internal/store"
)

// 会话状态。
const (
	SessionRunning  = "running"
	SessionBooked   = "booked"
	SessionOccupied = "occupied"
	SessionFailed   = "failed"
)

// RunOptions 手动触发一次抢座时的参数覆盖。
type RunOptions struct {
	// Source 记录来源标记：auto | manual。
	Source string
	// MaxAttempts 覆盖最大尝试次数（测试用）。
	MaxAttempts int
	// IntervalSeconds 覆盖尝试间隔（测试用）。
	IntervalSeconds int
	// Force 无视已有终态记录强制开跑。
	Force bool
	// Date 指定日期（YYYY-MM-DD），空则取今天。
	Date string
}

// Session 一次抢座会话的可观测状态。
type Session struct {
	RuleID        string    `json:"ruleId"`
	RuleName      string    `json:"ruleName"`
	Date          string    `json:"date"`
	Source        string    `json:"source"`
	StartedAt     time.Time `json:"startedAt"`
	Deadline      time.Time `json:"deadline"`
	NextAttemptAt time.Time `json:"nextAttemptAt"`
	AttemptsDone  int       `json:"attemptsDone"`
	MaxAttempts   int       `json:"maxAttempts"`
	Status        string    `json:"status"`
	LastMessage   string    `json:"lastMessage,omitempty"`

	cancel context.CancelFunc
}

// Engine 调度引擎。
type Engine struct {
	store *store.Store
	lark  lark.Client
	log   *slog.Logger

	mu       sync.Mutex
	sessions map[string]*Session // key: ruleID|date

	// now / sleep 可注入以便测试。
	now   func() time.Time
	sleep func(ctx context.Context, d time.Duration) bool

	wg sync.WaitGroup
}

// New 创建引擎。
func New(st *store.Store, lc lark.Client, log *slog.Logger) *Engine {
	return &Engine{
		store:    st,
		lark:     lc,
		log:      log,
		sessions: map[string]*Session{},
		now:      time.Now,
		sleep:    sleepCtx,
	}
}

// SetClock 注入时钟与睡眠函数（仅测试使用）。
func (e *Engine) SetClock(now func() time.Time, sleep func(ctx context.Context, d time.Duration) bool) {
	e.now = now
	e.sleep = sleep
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func (e *Engine) sessionKey(ruleID, date string) string { return ruleID + "|" + date }

// Snapshot 返回全部会话（含已结束，新的在前）。
func (e *Engine) Snapshot() []Session {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]Session, 0, len(e.sessions))
	for _, s := range e.sessions {
		out = append(out, *s)
	}
	// 运行中优先，其余按开始时间倒序。
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			si, sj := out[i], out[j]
			switch {
			case si.Status == SessionRunning && sj.Status != SessionRunning:
			case si.Status != SessionRunning && sj.Status == SessionRunning:
				out[i], out[j] = out[j], out[i]
			case si.StartedAt.Before(sj.StartedAt):
				out[i], out[j] = out[j], out[i]
			default:
				continue
			}
		}
	}
	return out
}

func (e *Engine) hasSession(ruleID, date string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	s, ok := e.sessions[e.sessionKey(ruleID, date)]
	return ok && s.Status == SessionRunning
}

func (e *Engine) location() *time.Location {
	cfg := e.store.Config()
	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		return time.Local
	}
	return loc
}

// Run 启动周期扫描，直到 ctx 结束。
func (e *Engine) Run(ctx context.Context) {
	cfg := e.store.Config()
	tick := time.Duration(cfg.TickSeconds) * time.Second
	if tick <= 0 {
		tick = 15 * time.Second
	}
	e.log.Info("调度器启动", "tick", tick.String(), "timezone", cfg.Timezone)
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			e.log.Info("调度器停止")
			return
		case <-ticker.C:
			e.Scan()
		}
	}
}

// Scan 执行一次扫描：找出今天到触发时刻且尚未处理的规则并启动会话。
func (e *Engine) Scan() {
	loc := e.location()
	now := e.now().In(loc)
	today := now.Format("2006-01-02")
	wd := model.WeekdayKey(now)

	for _, rule := range e.store.Rules() {
		if !rule.Enabled {
			continue
		}
		if len(rule.Weekdays[wd]) == 0 {
			continue
		}
		trigger, err := rule.TriggerAt(today, loc)
		if err != nil {
			continue
		}
		window := e.windowOf(&rule)
		if now.Before(trigger) || now.After(trigger.Add(window)) {
			continue
		}
		if e.store.HasTerminal(rule.ID, today) {
			continue
		}
		if e.hasSession(rule.ID, today) {
			continue
		}
		opts := RunOptions{Source: "auto"}
		if err := e.Start(rule.ID, opts); err != nil {
			e.log.Warn("启动抢座会话失败", "rule", rule.ID, "date", today, "err", err)
		}
	}
}

func (e *Engine) windowOf(rule *model.Rule) time.Duration {
	cfg := e.store.Config()
	mins := cfg.GrabWindowMinutes
	if rule.WindowMinutes > 0 {
		mins = rule.WindowMinutes
	}
	return time.Duration(mins) * time.Minute
}

func (e *Engine) intervalOf(rule *model.Rule, opts RunOptions) int {
	if opts.IntervalSeconds > 0 {
		return opts.IntervalSeconds
	}
	if rule.IntervalSeconds > 0 {
		return rule.IntervalSeconds
	}
	return e.store.Config().GrabIntervalSeconds
}

// Start 为规则启动一次抢座会话。返回错误当规则不存在、日期非法或已有终态记录。
func (e *Engine) Start(ruleID string, opts RunOptions) error {
	rule, ok := e.store.Rule(ruleID)
	if !ok {
		return fmt.Errorf("规则 %s 不存在", ruleID)
	}
	loc := e.location()
	date := opts.Date
	if date == "" {
		date = e.now().In(loc).Format("2006-01-02")
	}
	day, err := time.ParseInLocation("2006-01-02", date, loc)
	if err != nil {
		return fmt.Errorf("日期格式应为 YYYY-MM-DD，得到 %q", date)
	}
	wd := model.WeekdayKey(day)
	if len(rule.Weekdays[wd]) == 0 {
		return fmt.Errorf("规则在星期 %s 没有配置会议室", model.WeekdayName(wd))
	}
	start, end, err := rule.SlotFor(date, loc)
	if err != nil {
		return err
	}
	if !opts.Force && e.store.HasTerminal(rule.ID, date) {
		rec, _ := e.store.RecordOf(rule.ID, date)
		return fmt.Errorf("日期 %s 已有终态记录（%s），跳过；可强制重跑", date, rec.Status)
	}

	window := e.windowOf(&rule)
	interval := e.intervalOf(&rule, opts)
	maxAttempts := int(math.Ceil(float64(window) / float64(time.Duration(interval)*time.Second)))
	if opts.MaxAttempts > 0 {
		maxAttempts = opts.MaxAttempts
	}
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	if interval < 1 {
		interval = 1
	}
	source := opts.Source
	if source == "" {
		source = "manual"
	}

	key := e.sessionKey(rule.ID, date)
	ctx, cancel := context.WithCancel(context.Background())
	s := &Session{
		RuleID:      rule.ID,
		RuleName:    rule.Name,
		Date:        date,
		Source:      source,
		StartedAt:   e.now(),
		Deadline:    e.now().Add(window),
		MaxAttempts: maxAttempts,
		Status:      SessionRunning,
		LastMessage: "会话启动",
		cancel:      cancel,
	}
	e.mu.Lock()
	if old, ok := e.sessions[key]; ok && old.Status == SessionRunning {
		e.mu.Unlock()
		cancel()
		return fmt.Errorf("规则在 %s 已有运行中的会话", date)
	}
	// 只保留最近 20 条已结束会话。
	e.pruneLocked()
	e.sessions[key] = s
	e.mu.Unlock()

	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		e.runSession(ctx, s, rule, date, start, end, interval, source)
	}()
	return nil
}

// pruneLocked 清理已结束会话，最多保留 20 条（调用方需持锁）。
func (e *Engine) pruneLocked() {
	finished := make([]*Session, 0)
	for _, s := range e.sessions {
		if s.Status != SessionRunning {
			finished = append(finished, s)
		}
	}
	if len(finished) <= 20 {
		return
	}
	for i := 0; i < len(finished); i++ {
		for j := i + 1; j < len(finished); j++ {
			if finished[j].StartedAt.Before(finished[i].StartedAt) {
				finished[i], finished[j] = finished[j], finished[i]
			}
		}
	}
	for _, s := range finished[:len(finished)-20] {
		delete(e.sessions, e.sessionKey(s.RuleID, s.Date))
	}
}

// UpdateSession 更新会话可观测状态。
func (e *Engine) updateSession(key string, mut func(*Session)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if s, ok := e.sessions[key]; ok {
		mut(s)
	}
}

// StopAll 取消所有运行中的会话（进程退出前调用）。
func (e *Engine) StopAll() {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, s := range e.sessions {
		if s.Status == SessionRunning && s.cancel != nil {
			s.cancel()
		}
	}
	e.wg.Wait()
}

// resolveRoom 在可用列表中按优先级解析目标会议室：
// 先按 ID 精确匹配，再按名称精确匹配；返回解析出的真实房间（含 room_id）。
func resolveRoom(targets []model.RoomRef, avail []lark.Room) (lark.Room, bool) {
	for _, t := range targets {
		for _, a := range avail {
			if t.ID != "" && a.ID == t.ID {
				return a, true
			}
		}
	}
	for _, t := range targets {
		for _, a := range avail {
			if t.Name != "" && a.Name == t.Name {
				return a, true
			}
		}
	}
	return lark.Room{}, false
}

// overlap 判断事件与时间块是否重叠。
func overlap(ev lark.Event, start, end time.Time) bool {
	return ev.Start.Before(end) && ev.End.After(start)
}

// runSession 会话主体：预检重复 -> 逐分钟尝试 -> 写终态记录。
func (e *Engine) runSession(ctx context.Context, s *Session, rule model.Rule, date string, start, end time.Time, intervalSec int, source string) {
	key := e.sessionKey(rule.ID, date)
	log := e.log.With("rule", rule.Name, "date", date)
	defer func() {
		if s.Status == SessionRunning {
			s.Status = SessionFailed
		}
	}()

	e.store.AppendAttempt(model.Attempt{
		RuleID: rule.ID, RuleName: rule.Name, Date: date,
		Kind: "session-start", OK: true,
		Detail: fmt.Sprintf("窗口 %s，最多尝试 %d 次，间隔 %ds", e.windowOf(&rule), s.MaxAttempts, intervalSec),
	})

	// 预检：主日历中若已有本规则同名日程覆盖该时间块，视为已预约，
	// 记录并跳过（防止服务重启或手动创建后的重复预约）。
	if events, err := e.lark.SearchEvents(ctx, rule.Name, start, end, nil); err == nil {
		for _, ev := range events {
			if ev.Summary == rule.Name && overlap(ev, start, end) {
				note := "检测到同名日程已存在，记录并跳过该日期"
				_ = e.store.UpsertRecord(model.Record{
					RuleID: rule.ID, RuleName: rule.Name, Date: date,
					Start: start.Format(time.RFC3339), End: end.Format(time.RFC3339),
					Status: model.StatusBooked, Source: "detected",
					EventID: ev.ID, CalendarID: ev.CalendarID, Note: note,
				})
				e.store.AppendAttempt(model.Attempt{
					RuleID: rule.ID, RuleName: rule.Name, Date: date,
					Kind: "skip", OK: true, Detail: note,
				})
				e.updateSession(key, func(ss *Session) {
					ss.Status = SessionBooked
					ss.LastMessage = note
				})
				log.Info(note, "event_id", ev.ID)
				return
			}
		}
	} else {
		log.Warn("预检搜索失败，继续尝试", "err", err)
	}

	wd := model.WeekdayKey(start)
	booked := false
	findOK := false
	var lastErr string

	for attempt := 1; attempt <= s.MaxAttempts && !booked; attempt++ {
		if ctx.Err() != nil {
			e.updateSession(key, func(ss *Session) { ss.LastMessage = "会话被取消" })
			return
		}
		// 其他途径（手动）已写入终态则退出。
		if e.store.HasTerminal(rule.ID, date) {
			rec, _ := e.store.RecordOf(rule.ID, date)
			e.updateSession(key, func(ss *Session) {
				ss.Status = map[string]string{
					model.StatusBooked: SessionBooked, model.StatusOccupied: SessionOccupied,
					model.StatusFailed: SessionFailed,
				}[rec.Status]
				ss.LastMessage = "检测到终态记录，会话结束：" + rec.Status
			})
			return
		}

		targets := rule.Weekdays[wd]
		names := make([]string, 0, len(targets))
		for _, t := range targets {
			if t.Name != "" {
				names = append(names, t.Name)
			}
		}
		avail, err := e.lark.RoomFind(ctx, start, end, names)
		if err != nil {
			lastErr = err.Error()
			e.store.AppendAttempt(model.Attempt{
				RuleID: rule.ID, RuleName: rule.Name, Date: date,
				Kind: "find", OK: false, Detail: fmt.Sprintf("第 %d 次：%v", attempt, err),
			})
			e.updateSession(key, func(ss *Session) {
				ss.AttemptsDone = attempt
				ss.LastMessage = "查询可用会议室失败：" + lastErr
			})
		} else {
			findOK = true
			room, ok := resolveRoom(targets, avail)
			if !ok {
				busy := make([]string, 0, len(targets))
				for _, t := range targets {
					busy = append(busy, t.Name)
				}
				detail := fmt.Sprintf("第 %d 次：目标会议室均不可用（%s）", attempt, joinNames(busy))
				e.store.AppendAttempt(model.Attempt{
					RuleID: rule.ID, RuleName: rule.Name, Date: date,
					Kind: "find", OK: true, Detail: detail,
				})
				e.updateSession(key, func(ss *Session) {
					ss.AttemptsDone = attempt
					ss.LastMessage = detail
				})
				if attempt == s.MaxAttempts {
					break
				}
			} else {
				ev, err := e.lark.CreateEvent(ctx, rule.Name, "由 lark-schedule-meeting 自动预约", start, end, []string{room.ID})
				if err != nil {
					lastErr = err.Error()
					e.store.AppendAttempt(model.Attempt{
						RuleID: rule.ID, RuleName: rule.Name, Date: date,
						Kind: "create", OK: false,
						Detail: fmt.Sprintf("第 %d 次预定 %s 失败：%v", attempt, room.Name, err),
					})
					e.updateSession(key, func(ss *Session) {
						ss.AttemptsDone = attempt
						ss.LastMessage = "预定 " + room.Name + " 失败：" + lastErr
					})
				} else {
					booked = true
					note := fmt.Sprintf("第 %d 次尝试成功，已预定 %s", attempt, room.Name)
					_ = e.store.UpsertRecord(model.Record{
						RuleID: rule.ID, RuleName: rule.Name, Date: date,
						Start: start.Format(time.RFC3339), End: end.Format(time.RFC3339),
						RoomID: room.ID, RoomName: room.Name,
						Status: model.StatusBooked, Source: source,
						EventID: ev.ID, CalendarID: ev.CalendarID,
						Note: note, Attempts: attempt,
					})
					e.store.AppendAttempt(model.Attempt{
						RuleID: rule.ID, RuleName: rule.Name, Date: date,
						Kind: "create", OK: true, Detail: note,
					})
					e.updateSession(key, func(ss *Session) {
						ss.AttemptsDone = attempt
						ss.Status = SessionBooked
						ss.LastMessage = note
					})
					log.Info(note, "room", room.Name, "event_id", ev.ID)
					return
				}
			}
		}

		// 未成功：按间隔等待后进行下一次尝试。
		if attempt < s.MaxAttempts {
			next := time.Duration(intervalSec) * time.Second
			e.updateSession(key, func(ss *Session) { ss.NextAttemptAt = e.now().Add(next) })
			if !e.sleep(ctx, next) {
				e.updateSession(key, func(ss *Session) { ss.LastMessage = "会话被取消" })
				return
			}
		}
	}

	if !booked {
		status := model.StatusOccupied
		note := "抢座窗口结束，目标会议室均不可用"
		if !findOK {
			status = model.StatusFailed
			note = "抢座窗口结束，接口调用持续失败"
			if lastErr != "" {
				note += "：" + lastErr
			}
		}
		_ = e.store.UpsertRecord(model.Record{
			RuleID: rule.ID, RuleName: rule.Name, Date: date,
			Start: start.Format(time.RFC3339), End: end.Format(time.RFC3339),
			Status: status, Source: source, Note: note, Attempts: s.MaxAttempts,
		})
		e.store.AppendAttempt(model.Attempt{
			RuleID: rule.ID, RuleName: rule.Name, Date: date,
			Kind: "session-end", OK: false, Detail: note,
		})
		e.updateSession(key, func(ss *Session) {
			ss.Status = map[string]string{
				model.StatusOccupied: SessionOccupied, model.StatusFailed: SessionFailed,
			}[status]
			ss.LastMessage = note
		})
		log.Warn(note)
	}
}

func joinNames(names []string) string {
	out := ""
	for i, n := range names {
		if i > 0 {
			out += "、"
		}
		out += n
	}
	return out
}

// ConfigChanged 供 API 层在配置更新后通知引擎（当前为空实现，扫描循环每次读取最新配置）。
func (e *Engine) ConfigChanged() {}
