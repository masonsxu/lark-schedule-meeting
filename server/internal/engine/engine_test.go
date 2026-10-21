package engine

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/masonsxu/lark-schedule-meeting/server/internal/config"
	"github.com/masonsxu/lark-schedule-meeting/server/internal/lark"
	"github.com/masonsxu/lark-schedule-meeting/server/internal/model"
	"github.com/masonsxu/lark-schedule-meeting/server/internal/store"
)

// fakeLark 引擎测试替身。CreateEvent 按 roomID 决定成败，模拟房间被占。
type fakeLark struct {
	mu sync.Mutex

	createErrFor map[string]error // roomID -> 失败原因
	createAllErr error
	events       []lark.Event // SearchEvents 返回值

	findCalls   int
	createCalls []createdEvent
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
	return nil, nil
}

func (f *fakeLark) CreateEvent(ctx context.Context, summary, description string, start, end time.Time, roomIDs []string) (*lark.Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createCalls = append(f.createCalls, createdEvent{summary: summary, roomIDs: roomIDs, start: start})
	if f.createAllErr != nil {
		return nil, f.createAllErr
	}
	for _, id := range roomIDs {
		if err, ok := f.createErrFor[id]; ok {
			return nil, err
		}
	}
	return &lark.Event{ID: "ev_fake", CalendarID: "primary", Summary: summary, Start: start, End: end}, nil
}

func (f *fakeLark) SearchEvents(ctx context.Context, query string, startDay, endDay time.Time, attendeeIDs []string) ([]lark.Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.events, nil
}

func (f *fakeLark) DeleteEvent(ctx context.Context, calendarID, eventID string) error {
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

func fmtDate(d time.Time) string { return d.Format("2006-01-02") }

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

func boolPtr(b bool) *bool { return &b }

// baseRule 每周循环，会议室优先级 A/B/C；firstTarget 为首次目标日期（周一）。
func baseRule(firstTarget time.Time) model.Rule {
	return model.Rule{
		ID:        "rule_test",
		Name:      "周会",
		StartDate: fmtDate(firstTarget),
		Cycle:     model.CycleWeekly,
		Rooms: []model.RoomRef{
			{ID: "omm_A", Name: "会议室A"},
			{ID: "omm_B", Name: "会议室B"},
			{ID: "omm_C", Name: "会议室C"},
		},
		StartTime:   "10:00",
		EndTime:     "11:00",
		TriggerTime: "09:00",
		LeadDays:    30,
		Enabled:     true,
	}
}

// grabDayFor 返回“目标日期 - 30 天”的抢座日。
func grabDayFor(target time.Time) time.Time {
	return target.AddDate(0, 0, -30)
}

// ---- 场景测试 ----

// 场景 1：抢座日到点触发，按优先级以固定 ID 直传创建；
// A 被占则落到 B；目标日期 = 抢座日 + 30；终态记录后重复扫描不再触发。
func TestScanBooksByFixedRoomID(t *testing.T) {
	loc := mustLoc(t)
	target := nextWeekdayFrom(time.Date(2026, 10, 20, 0, 0, 0, 0, loc), time.Monday)
	grabDay := grabDayFor(target)
	env := newEnv(t, grabDay.Add(9*time.Hour+5*time.Minute)) // 09:05 处于抢座窗口
	rule := baseRule(target)
	if err := env.store.UpsertRule(rule); err != nil {
		t.Fatal(err)
	}
	env.fake.mu.Lock()
	env.fake.createErrFor = map[string]error{"omm_A": errors.New("会议室已被占用")}
	env.fake.mu.Unlock()

	env.engine.Scan()
	date := fmtDate(target)
	s := env.waitSessionStatus(t, rule.ID, date, SessionBooked)

	finds, creates := env.fake.stats()
	if finds != 0 {
		t.Errorf("抢座路径不应调用 room-find，调用了 %d 次", finds)
	}
	if len(creates) != 2 {
		t.Fatalf("应先试 A（失败）再试 B（成功），共 2 次创建，得到 %d", len(creates))
	}
	if creates[0].roomIDs[0] != "omm_A" || creates[1].roomIDs[0] != "omm_B" {
		t.Errorf("优先级顺序错误: %+v", creates)
	}
	if s.AttemptsDone != 1 {
		t.Errorf("第一轮尝试即应成功，attempts=%d", s.AttemptsDone)
	}
	rec, ok := env.store.RecordOf(rule.ID, date)
	if !ok || rec.Status != model.StatusBooked || rec.RoomID != "omm_B" {
		t.Fatalf("终态记录错误: %+v ok=%v", rec, ok)
	}
	// 每周规则不应被自动关闭。
	if latest, _ := env.store.Rule(rule.ID); !latest.Enabled {
		t.Error("每周规则抢座成功后不应自动关闭")
	}

	// 再次扫描：已有终态记录，不得重复预约。
	env.engine.Scan()
	waitQuiet(t)
	_, creates2 := env.fake.stats()
	if len(creates2) != 2 {
		t.Errorf("重复扫描不应再创建日程，创建次数=%d", len(creates2))
	}
}

// 场景 2：所有固定 ID 均失败 -> 窗口结束写 occupied 终态；再次 Start 被拒绝。
func TestOccupiedWhenAllRoomsFail(t *testing.T) {
	loc := mustLoc(t)
	target := nextWeekdayFrom(time.Date(2026, 10, 20, 0, 0, 0, 0, loc), time.Monday)
	env := newEnv(t, grabDayFor(target).Add(9*time.Hour))
	rule := baseRule(target)
	rule.Rooms = rule.Rooms[:2] // A、B
	if err := env.store.UpsertRule(rule); err != nil {
		t.Fatal(err)
	}
	env.fake.mu.Lock()
	env.fake.createAllErr = errors.New("会议室时间冲突")
	env.fake.mu.Unlock()

	date := fmtDate(target)
	if err := env.engine.Start(rule.ID, RunOptions{Source: "auto", MaxAttempts: 2, IntervalSeconds: 1, Date: date}); err != nil {
		t.Fatal(err)
	}
	env.waitSessionStatus(t, rule.ID, date, SessionOccupied)

	rec, ok := env.store.RecordOf(rule.ID, date)
	if !ok || rec.Status != model.StatusOccupied {
		t.Fatalf("应写 occupied 记录: %+v ok=%v", rec, ok)
	}
	_, creates := env.fake.stats()
	// 2 轮尝试 x 2 间会议室 = 4 次创建。
	if len(creates) != 4 {
		t.Errorf("2 轮 x 2 间会议室应创建 4 次，得到 %d", len(creates))
	}
	if err := env.engine.Start(rule.ID, RunOptions{Source: "auto", Date: date}); err == nil {
		t.Error("已有终态记录时再次启动应报错")
	}
}

// 场景 3：单次模式（once）无论成败，到达终态后规则自动关闭。
func TestOnceRuleAutoCloses(t *testing.T) {
	loc := mustLoc(t)
	target := nextWeekdayFrom(time.Date(2026, 10, 20, 0, 0, 0, 0, loc), time.Monday)
	date := fmtDate(target)

	t.Run("成功后关闭", func(t *testing.T) {
		env := newEnv(t, grabDayFor(target).Add(9*time.Hour))
		rule := baseRule(target)
		rule.ID = "rule_once_ok"
		rule.Cycle = model.CycleOnce
		if err := env.store.UpsertRule(rule); err != nil {
			t.Fatal(err)
		}
		if err := env.engine.Start(rule.ID, RunOptions{Date: date}); err != nil {
			t.Fatal(err)
		}
		env.waitSessionStatus(t, rule.ID, date, SessionBooked)
		latest, _ := env.store.Rule(rule.ID)
		if latest.Enabled {
			t.Error("单次规则成功后应自动关闭")
		}
	})

	t.Run("失败后也关闭", func(t *testing.T) {
		env := newEnv(t, grabDayFor(target).Add(9*time.Hour))
		rule := baseRule(target)
		rule.ID = "rule_once_fail"
		rule.Cycle = model.CycleOnce
		if err := env.store.UpsertRule(rule); err != nil {
			t.Fatal(err)
		}
		env.fake.mu.Lock()
		env.fake.createAllErr = errors.New("会议室时间冲突")
		env.fake.mu.Unlock()
		if err := env.engine.Start(rule.ID, RunOptions{Date: date, MaxAttempts: 1}); err != nil {
			t.Fatal(err)
		}
		env.waitSessionStatus(t, rule.ID, date, SessionOccupied)
		latest, _ := env.store.Rule(rule.ID)
		if latest.Enabled {
			t.Error("单次规则失败后也应自动关闭")
		}
		// 关闭后扫描不再触发（历史会话仍在快照中，只检查无新增运行中会话）。
		env.setNow(grabDayFor(target).Add(9*time.Hour + 5*time.Minute))
		env.engine.Scan()
		waitQuiet(t)
		running := 0
		for _, s := range env.engine.Snapshot() {
			if s.Status == SessionRunning {
				running++
			}
		}
		if running != 0 {
			t.Errorf("关闭后的规则不应再被扫描触发，运行中会话 %d 个", running)
		}
	})
}

// 场景 4：按天循环 + 跳过周六日：周六目标不触发，下周一目标触发。
func TestDailyCycleSkipsWeekendTargets(t *testing.T) {
	loc := mustLoc(t)
	saturday := nextWeekdayFrom(time.Date(2026, 10, 20, 0, 0, 0, 0, loc), time.Saturday)
	monday := nextWeekdayFrom(saturday, time.Monday)

	env := newEnv(t, grabDayFor(saturday).Add(9*time.Hour+5*time.Minute))
	rule := model.Rule{
		ID: "rule_daily", Name: "晨会",
		StartDate: saturday.AddDate(0, 0, -7).Format("2006-01-02"), // 起始日早于周六
		Cycle:     model.CycleDaily,
		Rooms:     []model.RoomRef{{ID: "omm_D", Name: "会议室D"}},
		StartTime: "10:00", EndTime: "11:00", TriggerTime: "09:00", LeadDays: 30, Enabled: true,
	}
	if err := env.store.UpsertRule(rule); err != nil {
		t.Fatal(err)
	}
	// 周六目标：默认跳过。
	env.engine.Scan()
	waitQuiet(t)
	if sessions := env.engine.Snapshot(); len(sessions) != 0 {
		t.Errorf("周六目标应被跳过: %+v", sessions)
	}
	// 周一目标：正常触发。
	env.setNow(grabDayFor(monday).Add(9*time.Hour + 5*time.Minute))
	env.engine.Scan()
	env.waitSessionStatus(t, rule.ID, fmtDate(monday), SessionBooked)
}

// 场景 5：每月循环：起始日 + 整月为有效目标，中间日期不是。
func TestMonthlyCycleOccurrences(t *testing.T) {
	loc := mustLoc(t)
	start := time.Date(2026, 10, 19, 0, 0, 0, 0, loc) // 周一
	next := start.AddDate(0, 1, 0)                    // 2026-11-19 周四

	env := newEnv(t, grabDayFor(next).Add(9*time.Hour+5*time.Minute))
	rule := model.Rule{
		ID: "rule_monthly", Name: "月会",
		StartDate: fmtDate(start), Cycle: model.CycleMonthly,
		Rooms:     []model.RoomRef{{ID: "omm_M", Name: "会议室M"}},
		StartTime: "10:00", EndTime: "11:00", TriggerTime: "09:00", LeadDays: 30, Enabled: true,
	}
	if err := env.store.UpsertRule(rule); err != nil {
		t.Fatal(err)
	}
	// 非目标日（起始日 + 7 天对应的抢座日）不触发。
	env.setNow(grabDayFor(start.AddDate(0, 0, 7)).Add(9*time.Hour + 5*time.Minute))
	env.engine.Scan()
	waitQuiet(t)
	if sessions := env.engine.Snapshot(); len(sessions) != 0 {
		t.Errorf("非整月日期不应触发: %+v", sessions)
	}
	// 目标日（下月同日）触发。
	env.setNow(grabDayFor(next).Add(9*time.Hour + 5*time.Minute))
	env.engine.Scan()
	env.waitSessionStatus(t, rule.ID, fmtDate(next), SessionBooked)
}

// 场景 6：非循环目标日（每周规则的非整周日期）扫描不触发。
func TestScanSkipsNonOccurrenceDay(t *testing.T) {
	loc := mustLoc(t)
	monday := nextWeekdayFrom(time.Date(2026, 10, 20, 0, 0, 0, 0, loc), time.Monday)
	tuesday := monday.AddDate(0, 0, 1)
	env := newEnv(t, grabDayFor(tuesday).Add(9*time.Hour+5*time.Minute))
	if err := env.store.UpsertRule(baseRule(monday)); err != nil {
		t.Fatal(err)
	}
	env.engine.Scan()
	waitQuiet(t)
	if sessions := env.engine.Snapshot(); len(sessions) != 0 {
		t.Errorf("非循环目标日不应启动会话: %+v", sessions)
	}
	// 触发时刻之前同样不触发。
	env.setNow(grabDayFor(monday).Add(8 * time.Hour))
	env.engine.Scan()
	waitQuiet(t)
	for _, s := range env.engine.Snapshot() {
		t.Errorf("触发时刻之前不应启动会话: %+v", s)
	}
}

// 场景 7：预检发现本规则同名日程已覆盖时间块 -> 记录 booked(detected) 且不重复创建。
func TestDuplicateDetectionSkips(t *testing.T) {
	loc := mustLoc(t)
	monday := nextWeekdayFrom(time.Date(2026, 10, 20, 0, 0, 0, 0, loc), time.Monday)
	env := newEnv(t, monday.Add(9*time.Hour))
	rule := baseRule(monday)
	if err := env.store.UpsertRule(rule); err != nil {
		t.Fatal(err)
	}
	env.fake.events = []lark.Event{{
		ID: "ev_existing", CalendarID: "primary", Summary: "周会",
		Start: monday.Add(10 * time.Hour), End: monday.Add(11 * time.Hour),
	}}

	date := fmtDate(monday)
	if err := env.engine.Start(rule.ID, RunOptions{Source: "manual", Date: date}); err != nil {
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

// 场景 8：手动触发未指定日期时，取下一个可抢的目标日期（抢座日不早于今天）。
func TestManualRunDefaultDate(t *testing.T) {
	loc := mustLoc(t)
	now := time.Date(2026, 10, 21, 21, 0, 0, 0, loc) // 周三
	lead := 30
	env := newEnv(t, now)
	// 首个目标 = 今天 + 30 天 + 3 天（跳过中间非目标日期）。
	target := now.AddDate(0, 0, lead+3)
	rule := model.Rule{
		ID: "rule_manual", Name: "手动会",
		StartDate: fmtDate(target), Cycle: model.CycleOnce,
		Rooms:     []model.RoomRef{{ID: "omm_T", Name: "T"}},
		StartTime: "10:00", EndTime: "11:00", TriggerTime: "09:00", LeadDays: lead, Enabled: true,
	}
	if err := env.store.UpsertRule(rule); err != nil {
		t.Fatal(err)
	}
	if err := env.engine.Start(rule.ID, RunOptions{Source: "manual"}); err != nil {
		t.Fatal(err)
	}
	env.waitSessionStatus(t, rule.ID, fmtDate(target), SessionBooked)
}

// 场景 9：仅配置名称（无固定 ID）时 failed；allowNoRoom 则直接创建无房会议。
func TestNoIDRoomsBehavior(t *testing.T) {
	loc := mustLoc(t)
	target := nextWeekdayFrom(time.Date(2026, 10, 20, 0, 0, 0, 0, loc), time.Monday)
	date := fmtDate(target)

	t.Run("无ID且未开allowNoRoom", func(t *testing.T) {
		env := newEnv(t, target.Add(9*time.Hour))
		rule := model.Rule{
			ID: "rule_noID", Name: "周会",
			StartDate: date, Cycle: model.CycleWeekly,
			Rooms:     []model.RoomRef{{Name: "1"}},
			StartTime: "10:00", EndTime: "11:00", TriggerTime: "09:00", LeadDays: 30, Enabled: true,
		}
		if err := env.store.UpsertRule(rule); err != nil {
			t.Fatal(err)
		}
		if err := env.engine.Start(rule.ID, RunOptions{Date: date}); err != nil {
			t.Fatal(err)
		}
		env.waitSessionStatus(t, rule.ID, date, SessionFailed)
		rec, _ := env.store.RecordOf(rule.ID, date)
		if rec.Status != model.StatusFailed {
			t.Fatalf("应写 failed: %+v", rec)
		}
		if _, creates := env.fake.stats(); len(creates) != 0 {
			t.Errorf("不应发起创建，得到 %d 次", len(creates))
		}
	})

	t.Run("无ID但allowNoRoom", func(t *testing.T) {
		env := newEnv(t, target.Add(9*time.Hour))
		rule := model.Rule{
			ID: "rule_noID2", Name: "站会",
			StartDate: date, Cycle: model.CycleWeekly,
			Rooms:     []model.RoomRef{{Name: "1"}},
			StartTime: "10:00", EndTime: "11:00", TriggerTime: "09:00", LeadDays: 30, Enabled: true,
			AllowNoRoom: true,
		}
		if err := env.store.UpsertRule(rule); err != nil {
			t.Fatal(err)
		}
		if err := env.engine.Start(rule.ID, RunOptions{Date: date}); err != nil {
			t.Fatal(err)
		}
		env.waitSessionStatus(t, rule.ID, date, SessionBooked)
		_, creates := env.fake.stats()
		if len(creates) != 1 || len(creates[0].roomIDs) != 0 {
			t.Errorf("应创建一次无房日程: %+v", creates)
		}
	})
}

// 场景 10：SQLite 持久化——重开数据库后规则与记录仍在。
func TestSqlitePersistence(t *testing.T) {
	loc := mustLoc(t)
	monday := nextWeekdayFrom(time.Date(2026, 10, 20, 0, 0, 0, 0, loc), time.Monday)
	env := newEnv(t, monday.Add(9*time.Hour))
	rule := baseRule(monday)
	if err := env.store.UpsertRule(rule); err != nil {
		t.Fatal(err)
	}
	date := fmtDate(monday)
	if err := env.store.UpsertRecord(model.Record{
		RuleID: rule.ID, RuleName: rule.Name, Date: date,
		Start: "2026-11-16T10:00:00+08:00", End: "2026-11-16T11:00:00+08:00",
		Status: model.StatusBooked, Source: "auto", RoomID: "omm_A", RoomName: "会议室A",
	}); err != nil {
		t.Fatal(err)
	}
	if err := env.store.UpsertRoom("omm_A", "会议室A", 7, "manual"); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.DataDir = env.store.Config().DataDir
	st2, err := store.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	rules := st2.Rules()
	if len(rules) != 1 || rules[0].ID != rule.ID {
		t.Fatalf("规则未持久化: %+v", rules)
	}
	if rules[0].Cycle != model.CycleWeekly || rules[0].StartDate != fmtDate(monday) {
		t.Errorf("循环/起始日未持久化: %+v", rules[0])
	}
	if len(rules[0].Rooms) != 3 || rules[0].Rooms[0].ID != "omm_A" {
		t.Errorf("房间列表未持久化: %+v", rules[0].Rooms)
	}
	if got := rules[0].LeadDaysOr(); got != 30 {
		t.Errorf("leadDays 未持久化: %d", got)
	}
	if rec, ok := st2.RecordOf(rule.ID, date); !ok || rec.Status != model.StatusBooked {
		t.Fatalf("记录未持久化: %+v ok=%v", rec, ok)
	}
	if rooms := st2.Rooms(); len(rooms) != 1 || rooms[0].ID != "omm_A" {
		t.Fatalf("名录未持久化: %+v", rooms)
	}
}

// waitQuiet 等待一小段时间确认无异步会话启动。
func waitQuiet(t *testing.T) {
	t.Helper()
	time.Sleep(30 * time.Millisecond)
}
