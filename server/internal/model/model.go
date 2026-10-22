// Package model 定义预约规则、会议室引用、预约记录与尝试日志的领域模型。
package model

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// RoomRef 表示一条会议室引用。预约采用固定 room_id 直传：
// ID 为飞书会议室 ID（omm_ 前缀），名称仅用于展示。
type RoomRef struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// DisplayName 返回展示名（无名称时用 ID）。
func (r RoomRef) DisplayName() string {
	if r.Name != "" {
		return r.Name
	}
	return r.ID
}

// 循环规则类型（一次只能选择一种；空值视为 CycleOnce）。
const (
	// CycleOnce 不循环：只预约首个目标日期一次，预约完成（无论成败）规则自动关闭。
	CycleOnce = "once"
	// CycleDaily 按天循环。
	CycleDaily = "daily"
	// CycleWeekly 按周循环（同星期）。
	CycleWeekly = "weekly"
	// CycleMonthly 按月循环（同日，月末日自动归一化）。
	CycleMonthly = "monthly"
	// CycleQuarterly 按季度循环（每 3 个月同日）。
	CycleQuarterly = "quarterly"
)

// CycleNames 循环类型的展示名。
var CycleNames = map[string]string{
	CycleOnce: "不循环（只预约一次）", CycleDaily: "每天", CycleWeekly: "每周",
	CycleMonthly: "每月", CycleQuarterly: "每季度",
}

// Rule 一条循环预约规则。
type Rule struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// StartDate 首次目标会议日期（YYYY-MM-DD，本地时区）。
	StartDate string `json:"startDate"`
	// Cycle 循环规则：once | daily | weekly | monthly | quarterly；空为 once。
	Cycle string `json:"cycle"`
	// Rooms 会议室优先级列表（固定 ID 直传，依次尝试，第一个成功即结束）。
	Rooms       []RoomRef `json:"rooms"`
	StartTime   string    `json:"startTime"`   // 会议开始 HH:MM（目标日期当天）
	EndTime     string    `json:"endTime"`     // 会议结束 HH:MM
	TriggerTime string    `json:"triggerTime"` // 预约开始时刻 HH:MM（在预约发起日 = 目标日期 - LeadDays 当天生效）
	// LeadDays 提前多少天的"预约发起日"开始预约目标日期；<=0 视为默认 30
	// （公司规则：会议室最早可提前 30 天预约，窗口开放即发起预约）。
	LeadDays int `json:"leadDays,omitempty"`
	// SkipWeekends 目标日期落在周六/周日时不预约；nil 视为 true（默认跳过周六日）。
	SkipWeekends *bool `json:"skipWeekends,omitempty"`
	Enabled      bool  `json:"enabled"`
	// AllowNoRoom 为 true 时，目标会议室全部预约失败也照常创建日程（不带会议室）。
	AllowNoRoom bool `json:"allowNoRoom,omitempty"`
	// WindowMinutes 覆盖全局预约窗口（分钟）；0 表示使用全局配置。
	WindowMinutes int `json:"windowMinutes,omitempty"`
	// IntervalSeconds 覆盖全局尝试间隔（秒）；0 表示使用全局配置。
	IntervalSeconds int       `json:"intervalSeconds,omitempty"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

// CycleOrDefault 返回生效的循环类型。
func (r *Rule) CycleOrDefault() string {
	if r.Cycle == "" {
		return CycleOnce
	}
	return r.Cycle
}

// LeadDaysOr 返回生效的提前天数。
func (r *Rule) LeadDaysOr() int {
	if r.LeadDays <= 0 {
		return 30
	}
	return r.LeadDays
}

// SkipWeekendsOn 返回是否跳过周六日（默认 true）。
func (r *Rule) SkipWeekendsOn() bool {
	if r.SkipWeekends == nil {
		return true
	}
	return *r.SkipWeekends
}

// 预约记录状态。三种状态均为终态：同一目标日期同一规则不再重复发起预约。
const (
	// StatusBooked 预约成功（含检测到本规则日程已存在）。
	StatusBooked = "booked"
	// StatusOccupied 预约窗口结束仍无成功（目标会议室均被占用或预约失败）。
	StatusOccupied = "occupied"
	// StatusFailed 配置缺失或接口持续异常。
	StatusFailed = "failed"
)

// Record 一条预约记录，键为 RuleID + 目标日期。
type Record struct {
	ID         string    `json:"id"`
	RuleID     string    `json:"ruleId"`
	RuleName   string    `json:"ruleName"`
	Date       string    `json:"date"` // 目标会议日期（本地 2006-01-02）
	Start      string    `json:"start"`
	End        string    `json:"end"`
	RoomID     string    `json:"roomId,omitempty"`
	RoomName   string    `json:"roomName,omitempty"`
	Status     string    `json:"status"` // booked | occupied | failed
	Source     string    `json:"source"` // auto | manual | detected
	EventID    string    `json:"eventId,omitempty"`
	CalendarID string    `json:"calendarId,omitempty"`
	Note       string    `json:"note,omitempty"`
	Attempts   int       `json:"attempts"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

// Terminal 报告该记录是否为终态。
func (r *Record) Terminal() bool {
	return r != nil && r.Status != ""
}

// Attempt 一条引擎动作日志。
type Attempt struct {
	ID       string    `json:"id"`
	At       time.Time `json:"at"`
	RuleID   string    `json:"ruleId"`
	RuleName string    `json:"ruleName"`
	Date     string    `json:"date"`
	Kind     string    `json:"kind"` // session-start | create | skip | session-end
	OK       bool      `json:"ok"`
	Detail   string    `json:"detail"`
}

// WeekdayKey 返回 ISO 星期键：周一 "1" ... 周日 "7"。
func WeekdayKey(t time.Time) string {
	d := int(t.Weekday()) // Go: Sunday=0, Monday=1, ..., Saturday=6
	if d == 0 {
		d = 7
	}
	return fmt.Sprintf("%d", d)
}

// ParseHM 解析 "HH:MM"。
func ParseHM(s string) (int, int, error) {
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("时间格式应为 HH:MM，得到 %q", s)
	}
	var h, m int
	if _, err := fmt.Sscanf(s, "%d:%d", &h, &m); err != nil {
		return 0, 0, fmt.Errorf("时间格式应为 HH:MM，得到 %q", s)
	}
	if h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, 0, fmt.Errorf("时间超出范围 %q", s)
	}
	return h, m, nil
}

// Validate 校验规则字段。会议室必须携带固定 ID（预约直传 ID）。
func (r *Rule) Validate() error {
	r.Name = strings.TrimSpace(r.Name)
	if r.Name == "" {
		return errors.New("会议主题不能为空")
	}
	if _, err := time.ParseInLocation("2006-01-02", r.StartDate, time.Local); err != nil {
		return fmt.Errorf("首次目标日期应为 YYYY-MM-DD，得到 %q", r.StartDate)
	}
	switch r.Cycle {
	case "", CycleOnce, CycleDaily, CycleWeekly, CycleMonthly, CycleQuarterly:
	default:
		return fmt.Errorf("循环规则应为 once/daily/weekly/monthly/quarterly，得到 %q", r.Cycle)
	}
	if len(r.Rooms) == 0 {
		return errors.New("至少需要配置一间会议室（固定 ID）")
	}
	for i, room := range r.Rooms {
		if strings.TrimSpace(room.ID) == "" {
			return fmt.Errorf("第 %d 间会议室 %q 缺少固定 ID（omm_ 开头）", i+1, room.DisplayName())
		}
	}
	sh, sm, err := ParseHM(r.StartTime)
	if err != nil {
		return fmt.Errorf("会议开始时间：%w", err)
	}
	eh, em, err := ParseHM(r.EndTime)
	if err != nil {
		return fmt.Errorf("会议结束时间：%w", err)
	}
	if eh*60+em <= sh*60+sm {
		return errors.New("会议结束时间必须晚于开始时间")
	}
	if _, _, err := ParseHM(r.TriggerTime); err != nil {
		return fmt.Errorf("预约开始时刻：%w", err)
	}
	if r.LeadDays < 0 || r.LeadDays > 365 {
		return errors.New("提前天数应在 0-365 之间")
	}
	if r.WindowMinutes < 0 || r.WindowMinutes > 24*60 {
		return errors.New("预约窗口分钟数超出范围")
	}
	if r.IntervalSeconds < 0 {
		return errors.New("尝试间隔不能为负")
	}
	return nil
}

// startDay 解析首次目标日期（返回零值与错误）。
func (r *Rule) startDay(loc *time.Location) (time.Time, error) {
	return time.ParseInLocation("2006-01-02", r.StartDate, loc)
}

// occursOnBase 判断 date 是否为一次目标日期（不考虑周末跳过；date 为本地零点）。
func (r *Rule) occursOnBase(date time.Time) bool {
	start, err := r.startDay(date.Location())
	if err != nil || date.Before(start) {
		return false
	}
	switch r.CycleOrDefault() {
	case CycleOnce:
		return date.Equal(start)
	case CycleDaily:
		return true // start 起每天
	case CycleWeekly:
		for d := start; !d.After(date); d = d.AddDate(0, 0, 7) {
			if d.Equal(date) {
				return true
			}
		}
		return false
	case CycleMonthly, CycleQuarterly:
		step := 1
		if r.CycleOrDefault() == CycleQuarterly {
			step = 3
		}
		for m := 0; ; m += step {
			d := start.AddDate(0, m, 0)
			if d.After(date) {
				return false
			}
			if d.Equal(date) {
				return true
			}
		}
	default:
		return false
	}
}

// OccursOn 判断 date 是否为一次有效的目标日期（含周末跳过）。
func (r *Rule) OccursOn(date time.Time) bool {
	if !r.occursOnBase(date) {
		return false
	}
	if r.SkipWeekendsOn() {
		if wd := WeekdayKey(date); wd == "6" || wd == "7" {
			return false
		}
	}
	return true
}

// NextOccurrence 返回 from 起（含当天）第一个有效目标日期；maxDays 为搜索天数上限。
func (r *Rule) NextOccurrence(from time.Time, maxDays int) (time.Time, bool) {
	d := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, from.Location())
	for i := 0; i < maxDays; i++ {
		if r.OccursOn(d) {
			return d, true
		}
		d = d.AddDate(0, 0, 1)
	}
	return time.Time{}, false
}

// SlotFor 计算规则在指定目标日期（2006-01-02）的会议起止时间。
func (r *Rule) SlotFor(date string, loc *time.Location) (time.Time, time.Time, error) {
	day, err := time.ParseInLocation("2006-01-02", date, loc)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("日期格式应为 YYYY-MM-DD，得到 %q", date)
	}
	sh, sm, err := ParseHM(r.StartTime)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	eh, em, err := ParseHM(r.EndTime)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	start := day.Add(time.Duration(sh)*time.Hour + time.Duration(sm)*time.Minute)
	end := day.Add(time.Duration(eh)*time.Hour + time.Duration(em)*time.Minute)
	return start, end, nil
}

// TriggerAt 计算预约发起日（目标日期 - LeadDays）当天的预约开始时刻。
func (r *Rule) TriggerAt(grabDay string, loc *time.Location) (time.Time, error) {
	day, err := time.ParseInLocation("2006-01-02", grabDay, loc)
	if err != nil {
		return time.Time{}, fmt.Errorf("日期格式应为 YYYY-MM-DD，得到 %q", grabDay)
	}
	h, m, err := ParseHM(r.TriggerTime)
	if err != nil {
		return time.Time{}, err
	}
	return day.Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute), nil
}

// WeekdayName 返回星期键的中文描述。
func WeekdayName(key string) string {
	names := map[string]string{"1": "周一", "2": "周二", "3": "周三", "4": "周四", "5": "周五", "6": "周六", "7": "周日"}
	if n, ok := names[key]; ok {
		return n
	}
	return key
}
