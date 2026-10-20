package engine

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/masonsxu/lark-schedule-meeting/server/internal/config"
	"github.com/masonsxu/lark-schedule-meeting/server/internal/lark"
	"github.com/masonsxu/lark-schedule-meeting/server/internal/model"
	"github.com/masonsxu/lark-schedule-meeting/server/internal/store"
)

// fakeLark 引擎测试替身。
type fakeLark struct {
	mu sync.Mutex

	rooms     []lark.Room // RoomFind 返回值
	findErr   error
	events    []lark.Event // SearchEvents 返回值
	createErr error
	// releaseAfter 非零时：前 N-1 次查询返回空房，第 N 次起返回 rooms。
	releaseAfter int

	findCalls   int
	createCalls []createdEvent
	deleteCalls []string
}

type createdEvent struct {
	summary string
	roomIDs []string
	start   time.Time
}

func (f *fakeLark) WhoAmI(ctx context.Context) (lark.Identity, error) {
	return lark.Identity{UserName: "tester"}, nil
}

func (f *fakeLark) RoomFind(ctx context.Context, start, end time.Time, roomNames []string) ([]lark.Room, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.findCalls++
	if f.findCalls < f.releaseAfter {
		return nil, f.findErr
	}
	return f.rooms, f.findErr
}

func (f *fakeLark) CreateEvent(ctx context.Context, summary, description string, start, end time.Time, roomIDs []string) (*lark.Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createCalls = append(f.createCalls, createdEvent{summary: summary, roomIDs: roomIDs, start: start})
	if f.createErr != nil {
		return nil, f.createErr
	}
	return &lark.Event{ID: "ev_fake", CalendarID: "primary", Summary: summary, Start: start, End: end}, nil
}

func (f *fakeLark) SearchEvents(ctx context.Context, query string, startDay, endDay time.Time, attendeeIDs []string) ([]lark.Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.events, nil
}

func (f *fakeLark) DeleteEvent(ctx context.Context, calendarID, eventID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteCalls = append(f.deleteCalls, eventID)
	return nil
}

func (f *fakeLark) stats() (int, []createdEvent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]createdEvent, len(f.createCalls))
	copy(out, f.createCalls)
	return f.findCalls, out
}

// ---- 测试基础设施 ----

func mustLoc(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

// nextWeekdayFrom 返回 ref 之后（含当天）第一个指定星期。
func nextWeekdayFrom(ref time.Time, wd time.Weekday) time.Time {
	d := time.Date(ref.Year(), ref.Month(), ref.Day(), 0, 0, 0, 0, ref.Location())
	for d.Weekday() != wd {
		d = d.AddDate(0, 0, 1)
	}
	return d
}

type testEnv struct {
	engine *Engine
	store  *store.Store
	fake   *fakeLark
	nowMu  sync.Mutex
	now    time.Time
}

func newEnv(t *testing.T, startAt time.Time) *testEnv {
	t.Helper()
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.GrabIntervalSeconds = 1
	cfg.TickSeconds = 1
	st, err := store.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeLark{}
	env := &testEnv{store: st, fake: fake, now: startAt}
	eng := New(st, fake, slog.New(slog.DiscardHandler))
	eng.SetClock(
		func() time.Time { env.nowMu.Lock(); defer env.nowMu.Unlock(); return env.now },
		func(ctx context.Context, d time.Duration) bool { return ctx.Err() == nil },
	)
	env.engine = eng
	return env
}

func (e *testEnv) setNow(t time.Time) {
	e.nowMu.Lock()
	defer e.nowMu.Unlock()
	e.now = t
}

// waitSessionStatus 等待指定规则的会话到达期望状态。
func (e *testEnv) waitSessionStatus(t *testing.T, ruleID, date, want string) Session {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		for _, s := range e.engine.Snapshot() {
			if s.RuleID == ruleID && s.Date == date && s.Status == want {
				return s
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("等待会话 %s/%s 状态 %s 超时，当前快照: %+v", ruleID, date, want, e.engine.Snapshot())
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// monWedRule 周一可约 A/B/C，周三可约 D/E —— 对应需求的规则形态。
func monWedRule() model.Rule {
	return model.Rule{
		ID:          "rule_test",
		Name:        "周会",
		Weekdays:    map[string][]model.RoomRef{"1": roomABC(), "3": roomDE()},
		StartTime:   "10:00",
		EndTime:     "11:00",
		TriggerTime: "09:00",
		Enabled:     true,
	}
}

func roomABC() []model.RoomRef {
	return []model.RoomRef{
		{ID: "omm_A", Name: "会议室A"},
		{ID: "omm_B", Name: "会议室B"},
		{ID: "omm_C", Name: "会议室C"},
	}
}

func roomDE() []model.RoomRef {
	return []model.RoomRef{
		{ID: "omm_D", Name: "会议室D"},
		{ID: "omm_E", Name: "会议室E"},
	}
}

// ---- 场景测试 ----

// 场景 1：周一到点触发，按优先级预定第一间可用会议室，并写终态记录；
// 同一日期重复扫描不再触发（跳过已被预约的日期）。
func TestScanBooksFirstAvailableOnMonday(t *testing.T) {
	loc := mustLoc(t)
	monday := nextWeekdayFrom(time.Date(2026, 10, 20, 0, 0, 0, 0, loc), time.Monday)
	env := newEnv(t, monday.Add(9*time.Hour+5*time.Minute)) // 09:05，处于 [09:00,09:10) 窗口内
	rule := monWedRule()
	if err := env.store.UpsertRule(rule); err != nil {
		t.Fatal(err)
	}
	// A 不可用，B、C 可用 → 应选 B。
	env.fake.rooms = []lark.Room{{ID: "omm_B", Name: "会议室B"}, {ID: "omm_C", Name: "会议室C"}}

	env.engine.Scan()
	s := env.waitSessionStatus(t, rule.ID, monday.Format("2006-01-02"), SessionBooked)
	if s.AttemptsDone != 1 {
		t.Errorf("第一次尝试即应成功，attempts=%d", s.AttemptsDone)
	}
	_, creates := env.fake.stats()
	if len(creates) != 1 {
		t.Fatalf("应恰好创建 1 个日程，得到 %d", len(creates))
	}
	if creates[0].roomIDs[0] != "omm_B" {
		t.Errorf("应预定优先级最高的可用会议室 B，得到 %v", creates[0].roomIDs)
	}
	rec, ok := env.store.RecordOf(rule.ID, monday.Format("2006-01-02"))
	if !ok || rec.Status != model.StatusBooked || rec.RoomID != "omm_B" {
		t.Fatalf("终态记录错误: %+v ok=%v", rec, ok)
	}

	// 再次扫描：已有终态记录，不得再次预约。
	env.engine.Scan()
	waitFor(t, func() bool {
		_, creates := env.fake.stats()
		return len(creates) > 1
	}, "不应重复创建", wantFalse)
	_, creates2 := env.fake.stats()
	if len(creates2) != 1 {
		t.Errorf("重复扫描不应再创建日程，创建次数=%d", len(creates2))
	}
}

// 场景 2：目标会议室全部被占 → 窗口结束写 occupied 终态；再次 Start 被拒绝。
func TestOccupiedRecordsAndSkipsNextCycle(t *testing.T) {
	loc := mustLoc(t)
	wednesday := nextWeekdayFrom(time.Date(2026, 10, 20, 0, 0, 0, 0, loc), time.Wednesday)
	env := newEnv(t, wednesday.Add(9*time.Hour))
	rule := monWedRule()
	if err := env.store.UpsertRule(rule); err != nil {
		t.Fatal(err)
	}
	env.fake.rooms = nil // 无可用会议室

	date := wednesday.Format("2006-01-02")
	if err := env.engine.Start(rule.ID, RunOptions{Source: "auto", MaxAttempts: 3, IntervalSeconds: 1}); err != nil {
		t.Fatal(err)
	}
	env.waitSessionStatus(t, rule.ID, date, SessionOccupied)

	rec, ok := env.store.RecordOf(rule.ID, date)
	if !ok || rec.Status != model.StatusOccupied {
		t.Fatalf("应写 occupied 记录: %+v ok=%v", rec, ok)
	}
	finds, creates := env.fake.stats()
	if finds != 3 {
		t.Errorf("3 次尝试应调用 3 次 room-find，得到 %d", finds)
	}
	if len(creates) != 0 {
		t.Errorf("无可用会议室时不应创建日程，得到 %d", len(creates))
	}
	// 下次循环（同日再触发）应被终态记录拦截。
	if err := env.engine.Start(rule.ID, RunOptions{Source: "auto"}); err == nil {
		t.Error("已有终态记录时再次启动应报错")
	}
}

// 场景 3：前几轮全部被占，之后释放 → 按优先级抢到。
func TestRetriesUntilRoomFreed(t *testing.T) {
	loc := mustLoc(t)
	monday := nextWeekdayFrom(time.Date(2026, 10, 20, 0, 0, 0, 0, loc), time.Monday)
	env := newEnv(t, monday.Add(9*time.Hour))
	rule := monWedRule()
	if err := env.store.UpsertRule(rule); err != nil {
		t.Fatal(err)
	}
	// 前 2 次查询无房，第 3 次 A 释放（按调用次数驱动，确定性时序）。
	env.fake.mu.Lock()
	env.fake.releaseAfter = 3
	env.fake.rooms = []lark.Room{{ID: "omm_A", Name: "会议室A"}}
	env.fake.mu.Unlock()

	date := monday.Format("2006-01-02")
	if err := env.engine.Start(rule.ID, RunOptions{Source: "auto", MaxAttempts: 5, IntervalSeconds: 1}); err != nil {
		t.Fatal(err)
	}
	s := env.waitSessionStatus(t, rule.ID, date, SessionBooked)
	if s.AttemptsDone != 3 {
		t.Errorf("第 3 次尝试应成功，attempts=%d", s.AttemptsDone)
	}
	_, creates := env.fake.stats()
	if len(creates) != 1 || creates[0].roomIDs[0] != "omm_A" {
		t.Errorf("应预定 A: %+v", creates)
	}
}

// 场景 4：预检发现本规则同名日程已覆盖时间块 → 记录 booked(detected) 且不重复创建。
func TestDuplicateDetectionSkips(t *testing.T) {
	loc := mustLoc(t)
	monday := nextWeekdayFrom(time.Date(2026, 10, 20, 0, 0, 0, 0, loc), time.Monday)
	env := newEnv(t, monday.Add(9*time.Hour))
	rule := monWedRule()
	if err := env.store.UpsertRule(rule); err != nil {
		t.Fatal(err)
	}
	env.fake.events = []lark.Event{{
		ID: "ev_existing", CalendarID: "primary", Summary: "周会",
		Start: monday.Add(10 * time.Hour), End: monday.Add(11 * time.Hour),
	}}

	date := monday.Format("2006-01-02")
	if err := env.engine.Start(rule.ID, RunOptions{Source: "manual"}); err != nil {
		t.Fatal(err)
	}
	env.waitSessionStatus(t, rule.ID, date, SessionBooked)

	rec, ok := env.store.RecordOf(rule.ID, date)
	if !ok || rec.Status != model.StatusBooked || rec.Source != "detected" || rec.EventID != "ev_existing" {
		t.Fatalf("应记录 detected/booked: %+v ok=%v", rec, ok)
	}
	if _, creates := env.fake.stats(); len(creates) != 0 {
		t.Errorf("重复场景不应创建日程，得到 %d", len(creates))
	}
}

// 场景 5：星期不匹配不触发（周二既有周一规则也有周三规则，都不该跑）。
func TestScanSkipsNonMatchingWeekday(t *testing.T) {
	loc := mustLoc(t)
	tuesday := nextWeekdayFrom(time.Date(2026, 10, 20, 0, 0, 0, 0, loc), time.Tuesday)
	env := newEnv(t, tuesday.Add(9*time.Hour+5*time.Minute))
	if err := env.store.UpsertRule(monWedRule()); err != nil {
		t.Fatal(err)
	}
	env.engine.Scan()
	time.Sleep(20 * time.Millisecond)
	for _, s := range env.engine.Snapshot() {
		t.Errorf("周二不应启动会话: %+v", s)
	}
	// 触发时刻之前也不触发（周一 08:00）。
	monday := nextWeekdayFrom(tuesday, time.Monday)
	env.setNow(monday.Add(8 * time.Hour))
	env.engine.Scan()
	time.Sleep(20 * time.Millisecond)
	for _, s := range env.engine.Snapshot() {
		t.Errorf("触发时刻之前不应启动会话: %+v", s)
	}
	// 窗口结束后同样不触发（周一 09:11，窗口 10 分钟）。
	env.setNow(monday.Add(9*time.Hour + 11*time.Minute))
	env.engine.Scan()
	time.Sleep(20 * time.Millisecond)
	for _, s := range env.engine.Snapshot() {
		t.Errorf("窗口结束后不应启动会话: %+v", s)
	}
}

// 场景 6：仅配置名称（公司内网外无法提前拿到 room_id）也能在运行时按名称解析。
func TestNameOnlyRoomResolution(t *testing.T) {
	loc := mustLoc(t)
	monday := nextWeekdayFrom(time.Date(2026, 10, 20, 0, 0, 0, 0, loc), time.Monday)
	env := newEnv(t, monday.Add(9*time.Hour))
	rule := model.Rule{
		ID: "rule_nameonly", Name: "站会",
		Weekdays:  map[string][]model.RoomRef{"1": {{Name: "3"}, {Name: "5"}}},
		StartTime: "10:00", EndTime: "10:30", TriggerTime: "09:00", Enabled: true,
	}
	if err := env.store.UpsertRule(rule); err != nil {
		t.Fatal(err)
	}
	env.fake.rooms = []lark.Room{{ID: "omm_5", Name: "5"}}

	date := monday.Format("2006-01-02")
	if err := env.engine.Start(rule.ID, RunOptions{Source: "manual"}); err != nil {
		t.Fatal(err)
	}
	env.waitSessionStatus(t, rule.ID, date, SessionBooked)
	_, creates := env.fake.stats()
	if len(creates) != 1 || creates[0].roomIDs[0] != "omm_5" {
		t.Fatalf("应通过名称解析并预定 5 号会议室: %+v", creates)
	}
}

// 场景 7：接口持续失败 → failed 终态。
func TestPersistentErrorsRecordFailed(t *testing.T) {
	loc := mustLoc(t)
	monday := nextWeekdayFrom(time.Date(2026, 10, 20, 0, 0, 0, 0, loc), time.Monday)
	env := newEnv(t, monday.Add(9*time.Hour))
	rule := monWedRule()
	if err := env.store.UpsertRule(rule); err != nil {
		t.Fatal(err)
	}
	env.fake.mu.Lock()
	env.fake.findErr = context.DeadlineExceeded
	env.fake.mu.Unlock()

	date := monday.Format("2006-01-02")
	if err := env.engine.Start(rule.ID, RunOptions{Source: "manual", MaxAttempts: 2, IntervalSeconds: 1}); err != nil {
		t.Fatal(err)
	}
	env.waitSessionStatus(t, rule.ID, date, SessionFailed)
	rec, ok := env.store.RecordOf(rule.ID, date)
	if !ok || rec.Status != model.StatusFailed {
		t.Fatalf("应写 failed 记录: %+v ok=%v", rec, ok)
	}
}

// ---- 小工具 ----

type wantBool int

const (
	wantTrue  wantBool = 0
	wantFalse wantBool = 1
)

func waitFor(t *testing.T, cond func() bool, msg string, expect wantBool) {
	t.Helper()
	deadline := time.Now().Add(50 * time.Millisecond)
	for time.Now().Before(deadline) {
		got := cond()
		if (expect == wantTrue && got) || (expect == wantFalse && !got) {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	if (expect == wantTrue && !cond()) || (expect == wantFalse && cond()) {
		t.Logf("条件最终满足: %s", msg)
		return
	}
}
