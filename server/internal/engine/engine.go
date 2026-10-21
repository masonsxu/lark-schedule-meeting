// Package engine 实现周期扫描与抢座会话：
// 目标日期 = 抢座日 + LeadDays（默认 30，公司规则最早可提前 30 天预约）；
// 抢座日当天到达 TriggerTime 后开抢，窗口内每分钟一次，直接以固定会议室 ID 创建日程；
// 终态记录写入存储，后续循环跳过已处理日期。
package engine

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strings"
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
	// Date 指定目标会议日期（YYYY-MM-DD），空则按今天 + LeadDays 推算。
	Date string
}

// Session 一次抢座会话的可观测状态。
type Session struct {
	RuleID        string    `json:"ruleId"`
	RuleName      string    `json:"ruleName"`
	Date          string    `json:"date"` // 目标会议日期
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

// Snapshot 返回全部会话（运行中优先，其余按开始时间倒序）。
func (e *Engine) Snapshot() []Session {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]Session, 0, len(e.sessions))
	for _, s := range e.sessions {
		out = append(out, *s)
	}
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			si, sj := out[i], out[j]
			swap := false
			switch {
			case si.Status == SessionRunning && sj.Status != SessionRunning:
				swap = true
			case si.Status != SessionRunning && sj.Status == SessionRunning:
			case si.StartedAt.Before(sj.StartedAt):
				swap = true
			}
			if swap {
				out[i], out[j] = out[j], out[i]
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

// targetDateFor 返回规则在抢座日 grabDay 对应的目标会议日期（grabDay + LeadDays）。
func (e *Engine) targetDateFor(rule *model.Rule, grabDay string, loc *time.Location) (time.Time, error) {
	day, err := time.ParseInLocation("2006-01-02", grabDay, loc)
	if err != nil {
		return time.Time{}, err
	}
	return day.AddDate(0, 0, rule.LeadDaysOr()), nil
}

// Scan 执行一次扫描：今天是否为某规则目标日期的抢座日（目标日期 = 今天 + LeadDays），
// 是且到达 TriggerTime、无终态记录、无运行会话时启动会话。
func (e *Engine) Scan() {
	loc := e.location()
	now := e.now().In(loc)
	today := now.Format("2006-01-02")

	for _, rule := range e.store.Rules() {
		if !rule.Enabled {
			continue
		}
		target, err := e.targetDateFor(&rule, today, loc)
		if err != nil {
			continue
		}
		date := target.Format("2006-01-02")
		wd := model.WeekdayKey(target)
		if len(rule.Weekdays[wd]) == 0 {
			continue // 目标日期不是配置的星期
		}
		if rule.SkipWeekendsOn() && (wd == "6" || wd == "7") {
			continue // 默认跳过周六日
		}
		trigger, err := rule.TriggerAt(today, loc)
		if err != nil {
			continue
		}
		window := e.windowOf(&rule)
		if now.Before(trigger) || now.After(trigger.Add(window)) {
			continue
		}
		if e.store.HasTerminal(rule.ID, date) {
			continue // 已预约/已处理的日期跳过
		}
		if e.hasSession(rule.ID, date) {
			continue
		}
		if err := e.Start(rule.ID, RunOptions{Source: "auto"}); err != nil {
			e.log.Warn("启动抢座会话失败", "rule", rule.ID, "date", date, "err", err)
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

// Start 为规则启动一次抢座会话。date 参数为目标会议日期；opts.Date 为空时按今天 + LeadDays 推算。
func (e *Engine) Start(ruleID string, opts RunOptions) error {
	rule, ok := e.store.Rule(ruleID)
	if !ok {
		return fmt.Errorf("规则 %s 不存在", ruleID)
	}
	loc := e.location()
	date := opts.Date
	if date == "" {
		target, err := e.targetDateFor(&rule, e.now().In(loc).Format("2006-01-02"), loc)
		if err != nil {
			return err
		}
		date = target.Format("2006-01-02")
	}
	day, err := time.ParseInLocation("2006-01-02", date, loc)
	if err != nil {
		return fmt.Errorf("日期格式应为 YYYY-MM-DD，得到 %q", date)
	}
	wd := model.WeekdayKey(day)
	if len(rule.Weekdays[wd]) == 0 {
		return fmt.Errorf("目标日期 %s 是星期 %s，规则未配置该星期", date, model.WeekdayName(wd))
	}
	if rule.SkipWeekendsOn() && (wd == "6" || wd == "7") {
		return fmt.Errorf("目标日期 %s 是周末，规则已启用跳过周六日", date)
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

// updateSession 更新会话可观测状态。
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
	for _, s := range e.sessions {
		if s.Status == SessionRunning && s.cancel != nil {
			s.cancel()
		}
	}
	e.mu.Unlock()
	e.wg.Wait()
}

// overlap 判断事件与时间块是否重叠。
func overlap(ev lark.Event, start, end time.Time) bool {
	return ev.Start.Before(end) && ev.End.After(start)
}

// runSession 会话主体：预检重复 -> 逐分钟以固定会议室 ID 直传创建 -> 写终态记录。
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
		Detail: fmt.Sprintf("窗口 %s，最多尝试 %d 次，间隔 %ds，提前 %d 天抢目标日期",
			e.windowOf(&rule), s.MaxAttempts, intervalSec, rule.LeadDaysOr()),
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
	targets := rule.Weekdays[wd]
	// 仅统计携带固定 ID 的会议室（预约直传 ID）。
	withID := make([]model.RoomRef, 0, len(targets))
	for _, t := range targets {
		if strings.TrimSpace(t.ID) != "" {
			withID = append(withID, t)
		}
	}
	if len(withID) == 0 {
		note := "规则缺少带 ID 的会议室，无法抢座"
		if rule.AllowNoRoom {
			note = "规则无带 ID 会议室，按 allowNoRoom 直接创建无房会议"
		} else {
			_ = e.store.UpsertRecord(model.Record{
				RuleID: rule.ID, RuleName: rule.Name, Date: date,
				Start: start.Format(time.RFC3339), End: end.Format(time.RFC3339),
				Status: model.StatusFailed, Source: source, Note: note,
			})
			e.store.AppendAttempt(model.Attempt{
				RuleID: rule.ID, RuleName: rule.Name, Date: date,
				Kind: "session-end", OK: false, Detail: note,
			})
			e.updateSession(key, func(ss *Session) {
				ss.Status = SessionFailed
				ss.LastMessage = note
			})
			log.Error(note)
			return
		}
	}

	booked := false
	createAttempted := false
	var lastErr string

	tryCreate := func(attempt int, roomID, roomName string) *lark.Event {
		createAttempted = true
		ev, err := e.lark.CreateEvent(ctx, rule.Name, "由 lark-schedule-meeting 自动预约", start, end, roomList(roomID))
		if err != nil {
			lastErr = err.Error()
			display := roomName
			if display == "" {
				display = roomID
			}
			e.store.AppendAttempt(model.Attempt{
				RuleID: rule.ID, RuleName: rule.Name, Date: date,
				Kind: "create", OK: false,
				Detail: fmt.Sprintf("第 %d 次预定 %s 失败：%v", attempt, display, err),
			})
			e.updateSession(key, func(ss *Session) {
				ss.AttemptsDone = attempt
				ss.LastMessage = "预定 " + display + " 失败：" + lastErr
			})
			return nil
		}
		return ev
	}

	recordBooked := func(attempt int, roomID, roomName, note string, ev *lark.Event) {
		_ = e.store.UpsertRecord(model.Record{
			RuleID: rule.ID, RuleName: rule.Name, Date: date,
			Start: start.Format(time.RFC3339), End: end.Format(time.RFC3339),
			RoomID: roomID, RoomName: roomName,
			Status: model.StatusBooked, Source: source,
			EventID: ev.ID, CalendarID: ev.CalendarID,
			Note: note, Attempts: attempt,
		})
		e.store.AppendAttempt(model.Attempt{
			RuleID: rule.ID, RuleName: rule.Name, Date: date,
			Kind: "create", OK: true, Detail: note,
		})
		if roomID != "" {
			_ = e.store.UpsertRoom(roomID, roomName, 0, "record")
		}
		e.updateSession(key, func(ss *Session) {
			ss.AttemptsDone = attempt
			ss.Status = SessionBooked
			ss.LastMessage = note
		})
		log.Info(note, "room", roomName, "event_id", ev.ID)
	}

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

		// 按优先级依次以固定 ID 直传创建，第一个成功即结束本轮。
		for _, room := range withID {
			if ctx.Err() != nil {
				return
			}
			ev := tryCreate(attempt, room.ID, room.Name)
			if ev != nil {
				booked = true
				note := fmt.Sprintf("第 %d 次尝试成功，已预定 %s", attempt, room.DisplayName())
				recordBooked(attempt, room.ID, room.DisplayName(), note, ev)
				return
			}
		}

		// 所有固定 ID 均失败：allowNoRoom 时兜底创建无房会议。
		if rule.AllowNoRoom {
			ev := tryCreate(attempt, "", "")
			if ev != nil {
				booked = true
				note := fmt.Sprintf("第 %d 次尝试：目标会议室均失败，已按无会议室预约（allowNoRoom）", attempt)
				recordBooked(attempt, "", "", note, ev)
				return
			}
		}

		e.updateSession(key, func(ss *Session) { ss.AttemptsDone = attempt })

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
		note := "抢座窗口结束，目标会议室均未成功"
		if lastErr != "" {
			note += "：" + lastErr
		}
		if !createAttempted {
			status = model.StatusFailed
			note = "抢座窗口结束，未能发起任何预约请求"
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

func roomList(ids ...string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id != "" {
			out = append(out, id)
		}
	}
	return out
}

// ConfigChanged 供 API 层在配置更新后通知引擎（当前为空实现，扫描循环每次读取最新配置）。
func (e *Engine) ConfigChanged() {}
