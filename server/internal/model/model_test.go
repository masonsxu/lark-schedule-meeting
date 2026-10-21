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
		Weekdays:    map[string][]RoomRef{"1": {{ID: "omm_1", Name: "1"}}, "3": {{ID: "omm_4"}}},
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
		{func(r *Rule) { r.Weekdays = nil }, "无星期"},
		{func(r *Rule) { r.Weekdays = map[string][]RoomRef{"8": {{Name: "x"}}} }, "非法星期键"},
		{func(r *Rule) { r.Weekdays = map[string][]RoomRef{"1": {}} }, "空会议室列表"},
		{func(r *Rule) { r.StartTime = "25:00" }, "开始时间越界"},
		{func(r *Rule) { r.EndTime = "09:30" }, "结束早于开始"},
		{func(r *Rule) { r.TriggerTime = "abc" }, "触发时刻非法"},
	}
	for _, c := range bad {
		r := *good
		c.mutate(&r)
		if err := r.Validate(); err == nil {
			t.Errorf("%s: 应当报错", c.desc)
		}
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
