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

// monWedRule 周一可约 A/B/C，周三可约 D/E；提前 30 天抢。
func monWedRule() model.Rule {
	return model.Rule{
		ID:          "rule_test",
		Name:        "周会",
		Weekdays:    map[string][]model.RoomRef{"1": roomABC(), "3": roomDE()},
		StartTime:   "10:00",
		EndTime:     "11:00",
		TriggerTime: "09:00",
		LeadDays:    30,
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

// grabDayFor 返回“目标日期 - 30 天”的抢座日。
func grabDayFor(target time.Time) time.Time {
	return target.AddDate(0, 0, -30)
}

// ---- 场景测试 ----

// 场景 1：抢座日到点触发，按优先级以固定 ID 直传创建；
// A 被占则落到 B；目标日期 = 抢座日 + 30；终态记录后重复扫描不再触发。
func TestScanBooksByFixedRoomID(t *testing.T) {
	loc := mustLoc(t)
	target := nextWeekdayFrom(time.Date(2026, 10, 20, 0, 0, 0, 0, loc), time.Monday) // 目标会议日：周一
	grabDay := grabDayFor(target)
	env := newEnv(t, grabDay.Add(9*time.Hour+5*time.Minute)) // 09:05 处于抢座窗口
	rule := monWedRule()
	if err := env.store.UpsertRule(rule); err != nil {
		t.Fatal(err)
	}
	env.fake.mu.Lock()
	env.fake.createErrFor = map[string]error{"omm_A": errors.New("会议室已被占用")}
	env.fake.mu.Unlock()

	env.engine.Scan()
	date := target.Format("2006-01-02")
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
	// 成功预约的会议室应自动进入名录。
	rooms := env.store.Rooms()
	if len(rooms) != 1 || rooms[0].ID != "omm_B" {
		t.Errorf("成功会议室应回写名录: %+v", rooms)
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
	target := nextWeekdayFrom(time.Date(2026, 10, 20, 0, 0, 0, 0, loc), time.Wednesday)
	env := newEnv(t, target.Add(9*time.Hour))
	rule := monWedRule()
	if err := env.store.UpsertRule(rule); err != nil {
		t.Fatal(err)
	}
	env.fake.mu.Lock()
	env.fake.createAllErr = errors.New("会议室时间冲突")
	env.fake.mu.Unlock()

	date := target.Format("2006-01-02")
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

// 场景 3：仅配置名称（无固定 ID）时 failed；allowNoRoom 则直接创建无房会议。
func TestNoIDRoomsBehavior(t *testing.T) {
	loc := mustLoc(t)
	target := nextWeekdayFrom(time.Date(2026, 10, 20, 0, 0, 0, 0, loc), time.Monday)
	date := target.Format("2006-01-02")

	t.Run("无ID且未开allowNoRoom", func(t *testing.T) {
		env := newEnv(t, target.Add(9*time.Hour))
		rule := model.Rule{
			ID: "rule_noID", Name: "周会",
			Weekdays:  map[string][]model.RoomRef{"1": {{Name: "1"}}},
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
			Weekdays:  map[string][]model.RoomRef{"1": {{Name: "1"}}},
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

// 场景 4：目标日期落在周六：默认跳过；显式关闭 skipWeekends 才允许。
func TestSkipWeekends(t *testing.T) {
	loc := mustLoc(t)
	saturday := nextWeekdayFrom(time.Date(2026, 10, 20, 0, 0, 0, 0, loc), time.Saturday)
	grabDay := grabDayFor(saturday)

	// 默认（nil = true）：扫描不触发。
	env := newEnv(t, grabDay.Add(9*time.Hour+5*time.Minute))
	rule := model.Rule{
		ID: "rule_sat", Name: "周六会",
		Weekdays:  map[string][]model.RoomRef{"6": {{ID: "omm_S", Name: "S"}}},
		StartTime: "10:00", EndTime: "11:00", TriggerTime: "09:00", LeadDays: 30, Enabled: true,
	}
	if err := env.store.UpsertRule(rule); err != nil {
		t.Fatal(err)
	}
	env.engine.Scan()
	waitQuiet(t)
	if sessions := env.engine.Snapshot(); len(sessions) != 0 {
		t.Errorf("默认应跳过周六目标日期: %+v", sessions)
	}
	// 手动触发周六也应被拒。
	if err := env.engine.Start(rule.ID, RunOptions{Date: saturday.Format("2006-01-02")}); err == nil {
		t.Error("手动触发周末目标日期应报错")
	}

	// 显式关闭 skipWeekends：允许触发。
	env2 := newEnv(t, grabDay.Add(9*time.Hour+5*time.Minute))
	rule2 := rule
	rule2.ID = "rule_sat2"
	rule2.SkipWeekends = boolPtr(false)
	if err := env2.store.UpsertRule(rule2); err != nil {
		t.Fatal(err)
	}
	env2.engine.Scan()
	env2.waitSessionStatus(t, rule2.ID, saturday.Format("2006-01-02"), SessionBooked)
}

// 场景 5：目标日期星期与规则不匹配时不触发。
func TestScanSkipsNonMatchingTargetWeekday(t *testing.T) {
	loc := mustLoc(t)
	tuesday := nextWeekdayFrom(time.Date(2026, 10, 20, 0, 0, 0, 0, loc), time.Tuesday)
	grabDay := grabDayFor(tuesday)
	env := newEnv(t, grabDay.Add(9*time.Hour+5*time.Minute))
	if err := env.store.UpsertRule(monWedRule()); err != nil {
		t.Fatal(err)
	}
	env.engine.Scan()
	waitQuiet(t)
	if sessions := env.engine.Snapshot(); len(sessions) != 0 {
		t.Errorf("目标日期周二不应启动会话: %+v", sessions)
	}
	// 触发时刻之前同样不触发（目标为周一时，抢座日 08:00）。
	monday := nextWeekdayFrom(tuesday, time.Monday)
	env.setNow(grabDayFor(monday).Add(8 * time.Hour))
	env.engine.Scan()
	waitQuiet(t)
	for _, s := range env.engine.Snapshot() {
		t.Errorf("触发时刻之前不应启动会话: %+v", s)
	}
}

// 场景 6：预检发现本规则同名日程已覆盖时间块 -> 记录 booked(detected) 且不重复创建。
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

// 场景 7：手动触发未指定日期时，按今天 + LeadDays 推算目标日期。
func TestManualRunDefaultDate(t *testing.T) {
	loc := mustLoc(t)
	now := time.Date(2026, 10, 20, 21, 0, 0, 0, loc) // 周二
	// 今天 + 30 天后的星期配进规则。
	target := now.AddDate(0, 0, 30)
	env := newEnv(t, now)
	rule := model.Rule{
		ID: "rule_manual", Name: "手动会",
		Weekdays:  map[string][]model.RoomRef{model.WeekdayKey(target): {{ID: "omm_T", Name: "T"}}},
		StartTime: "10:00", EndTime: "11:00", TriggerTime: "09:00", LeadDays: 30, Enabled: true,
	}
	if err := env.store.UpsertRule(rule); err != nil {
		t.Fatal(err)
	}
	if err := env.engine.Start(rule.ID, RunOptions{Source: "manual"}); err != nil {
		t.Fatal(err)
	}
	env.waitSessionStatus(t, rule.ID, target.Format("2006-01-02"), SessionBooked)
}

// 场景 8：SQLite 持久化——重开数据库后规则、记录、名录仍在。
func TestSqlitePersistence(t *testing.T) {
	loc := mustLoc(t)
	monday := nextWeekdayFrom(time.Date(2026, 10, 20, 0, 0, 0, 0, loc), time.Monday)
	env := newEnv(t, monday.Add(9*time.Hour))
	rule := monWedRule()
	if err := env.store.UpsertRule(rule); err != nil {
		t.Fatal(err)
	}
	date := monday.Format("2006-01-02")
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

	// 重开同一数据目录。
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
	if got := rules[0].LeadDaysOr(); got != 30 {
		t.Errorf("leadDays 未持久化: %d", got)
	}
	if rec, ok := st2.RecordOf(rule.ID, date); !ok || rec.Status != model.StatusBooked {
		t.Fatalf("记录未持久化: %+v ok=%v", rec, ok)
	}
	if !st2.HasTerminal(rule.ID, date) {
		t.Error("终态判断应持久生效")
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
