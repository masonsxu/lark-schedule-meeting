package model

import (
	"testing"
	"time"
)

func shanghai() *time.Location {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		panic(err)
	}
	return loc
}

func day(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02", s, shanghai())
	if err != nil {
		panic(err)
	}
	return t
}

func TestWeekdayKey(t *testing.T) {
	cases := []struct {
		in   time.Time
		want string
		name string
	}{
		{time.Date(2026, 10, 19, 0, 0, 0, 0, shanghai()), "1", "周一"},
		{time.Date(2026, 10, 21, 0, 0, 0, 0, shanghai()), "3", "周三"},
		{time.Date(2026, 10, 25, 0, 0, 0, 0, shanghai()), "7", "周日"},
	}
	for _, c := range cases {
		if got := WeekdayKey(c.in); got != c.want {
			t.Errorf("%s: WeekdayKey=%s want %s", c.name, got, c.want)
		}
	}
}

func TestSlotFor(t *testing.T) {
	r := &Rule{StartTime: "10:00", EndTime: "11:30"}
	start, end, err := r.SlotFor("2026-10-19", shanghai())
	if err != nil {
		t.Fatal(err)
	}
	if want := "2026-10-19T10:00:00+08:00"; start.Format(time.RFC3339) != want {
		t.Errorf("start=%s want %s", start.Format(time.RFC3339), want)
	}
	if want := "2026-10-19T11:30:00+08:00"; end.Format(time.RFC3339) != want {
		t.Errorf("end=%s want %s", end.Format(time.RFC3339), want)
	}
}

func TestTriggerAt(t *testing.T) {
	r := &Rule{TriggerTime: "09:00"}
	at, err := r.TriggerAt("2026-10-21", shanghai())
	if err != nil {
		t.Fatal(err)
	}
	if want := "2026-10-21T09:00:00+08:00"; at.Format(time.RFC3339) != want {
		t.Errorf("trigger=%s want %s", at.Format(time.RFC3339), want)
	}
}

func TestValidate(t *testing.T) {
	good := &Rule{
		Name:        "站会",
		StartDate:   "2026-11-02",
		Cycle:       CycleWeekly,
		Rooms:       []RoomRef{{ID: "omm_1", Name: "1"}, {ID: "omm_2"}},
		StartTime:   "10:00",
		EndTime:     "10:30",
		TriggerTime: "09:00",
		Enabled:     true,
	}
	if err := good.Validate(); err != nil {
		t.Fatalf("合法规则不应报错: %v", err)
	}

	bad := []struct {
		mutate func(*Rule)
		desc   string
	}{
		{func(r *Rule) { r.Name = " " }, "空主题"},
		{func(r *Rule) { r.StartDate = "2026/11/02" }, "日期格式错误"},
		{func(r *Rule) { r.Cycle = "biweekly" }, "非法循环类型"},
		{func(r *Rule) { r.Rooms = nil }, "无会议室"},
		{func(r *Rule) { r.Rooms = []RoomRef{{Name: "1"}} }, "会议室缺固定 ID"},
		{func(r *Rule) { r.StartTime = "25:00" }, "开始时间越界"},
		{func(r *Rule) { r.EndTime = "09:30" }, "结束早于开始"},
		{func(r *Rule) { r.TriggerTime = "abc" }, "触发时刻非法"},
		{func(r *Rule) { r.LeadDays = 400 }, "提前天数越界"},
	}
	for _, c := range bad {
		r := *good
		c.mutate(&r)
		if err := r.Validate(); err == nil {
			t.Errorf("%s: 应当报错", c.desc)
		}
	}
}

func TestOccursOn(t *testing.T) {
	base := func(cycle string, start string, skipWE bool) *Rule {
		return &Rule{StartDate: start, Cycle: cycle, SkipWeekends: &skipWE}
	}

	cases := []struct {
		desc string
		rule *Rule
		date string
		want bool
	}{
		{"once 命中", base(CycleOnce, "2026-11-02", false), "2026-11-02", true},
		{"once 其他日", base(CycleOnce, "2026-11-02", false), "2026-11-09", false},
		{"daily 命中", base(CycleDaily, "2026-11-02", false), "2026-11-20", true},
		{"daily 早于起始", base(CycleDaily, "2026-11-02", false), "2026-11-01", false},
		{"weekly 整周", base(CycleWeekly, "2026-11-02", false), "2026-11-16", true},
		{"weekly 非整周", base(CycleWeekly, "2026-11-02", false), "2026-11-17", false},
		{"monthly 同日", base(CycleMonthly, "2026-10-21", false), "2026-12-21", true},
		{"monthly 非同日", base(CycleMonthly, "2026-10-21", false), "2026-11-28", false},
		{"quarterly 季度同日", base(CycleQuarterly, "2026-10-21", false), "2027-01-21", true},
		{"quarterly 月度同日不算", base(CycleQuarterly, "2026-10-21", false), "2026-11-21", false},
		{"跳过周末-周六", base(CycleDaily, "2026-11-02", true), "2026-11-21", false},
		{"跳过周末-周日", base(CycleDaily, "2026-11-02", true), "2026-11-22", false},
		{"默认跳过周末", &Rule{StartDate: "2026-11-02", Cycle: CycleDaily}, "2026-11-21", false},
	}
	for _, c := range cases {
		if got := c.rule.OccursOn(day(c.date)); got != c.want {
			t.Errorf("%s: OccursOn=%v want %v", c.desc, got, c.want)
		}
	}
}

func TestMonthlyEndOfMonthNormalization(t *testing.T) {
	// 1月31日按月循环：2月没有31日，AddDate 归一化为 3月3日。
	r := &Rule{StartDate: "2026-01-31", Cycle: CycleMonthly}
	if !r.occursOnBase(day("2026-03-03")) {
		t.Error("1月31日 + 1月 应归一化命中 3月3日")
	}
	if r.occursOnBase(day("2026-02-28")) {
		t.Error("不应命中 2月28日")
	}
}

func TestNextOccurrence(t *testing.T) {
	skip := true
	r := &Rule{StartDate: "2026-11-02", Cycle: CycleDaily, SkipWeekends: &skip}
	// 从周六 2026-11-21 起找下一次：跳过周六日，命中周一 2026-11-23。
	got, ok := r.NextOccurrence(day("2026-11-21"), 14)
	if !ok || got.Format("2006-01-02") != "2026-11-23" {
		t.Errorf("下一个目标应为 2026-11-23，得到 %s ok=%v", got.Format("2006-01-02"), ok)
	}
	// 周五本身有效（含当天）。
	if got, ok := r.NextOccurrence(day("2026-11-20"), 7); !ok || got.Format("2006-01-02") != "2026-11-20" {
		t.Errorf("从周五找应命中当天，得到 %s ok=%v", got.Format("2006-01-02"), ok)
	}
	// 只看周六一天且跳过周末：无结果。
	if _, ok := r.NextOccurrence(day("2026-11-21"), 1); ok {
		t.Error("周六且跳过周末应返回 false")
	}
}

func TestCycleOrDefaultAndLeadDays(t *testing.T) {
	r := &Rule{}
	if r.CycleOrDefault() != CycleOnce {
		t.Error("空循环应默认 once")
	}
	if r.LeadDaysOr() != 30 {
		t.Error("空提前天数应默认 30")
	}
	if !r.SkipWeekendsOn() {
		t.Error("nil 应默认跳过周末")
	}
}

func TestTerminal(t *testing.T) {
	if !(&Record{Status: StatusBooked}).Terminal() {
		t.Error("booked 应为终态")
	}
	if (&Record{}).Terminal() {
		t.Error("空状态不应为终态")
	}
}
