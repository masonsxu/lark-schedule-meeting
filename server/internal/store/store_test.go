package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/masonsxu/lark-schedule-meeting/server/internal/config"
	"github.com/masonsxu/lark-schedule-meeting/server/internal/model"
)

// 旧版 JSON 数据（rules: map、records: map[ruleID|date]）应被完整迁移进 SQLite。
func TestMigrateLegacyJSON(t *testing.T) {
	dir := t.TempDir()
	legacyRules := `{
  "rule_old": {
    "id": "rule_old", "name": "旧规则",
    "weekdays": {"1": [{"id": "omm_1", "name": "1"}]},
    "startTime": "10:00", "endTime": "11:00", "triggerTime": "09:00",
    "enabled": true, "createdAt": "2026-10-01T00:00:00Z", "updatedAt": "2026-10-01T00:00:00Z"
  }
}`
	legacyRecords := `{
  "rule_old|2026-11-02": {
    "id": "rec_old", "ruleId": "rule_old", "ruleName": "旧规则", "date": "2026-11-02",
    "start": "2026-11-02T10:00:00+08:00", "end": "2026-11-02T11:00:00+08:00",
    "status": "booked", "source": "auto", "roomId": "omm_1", "roomName": "1",
    "eventId": "ev_old", "attempts": 1,
    "createdAt": "2026-10-03T00:00:00Z", "updatedAt": "2026-10-03T00:00:00Z"
  }
}`
	if err := os.WriteFile(filepath.Join(dir, "rules.json"), []byte(legacyRules), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "records.json"), []byte(legacyRecords), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.DataDir = dir
	st, err := Open(cfg)
	if err != nil {
		t.Fatal(err)
	}

	rules := st.Rules()
	if len(rules) != 1 || rules[0].ID != "rule_old" {
		t.Fatalf("规则迁移失败: %+v", rules)
	}
	if rules[0].Cycle != "weekly" {
		t.Errorf("旧 weekdays 规则应回填为 weekly 循环，得到 %q", rules[0].Cycle)
	}
	if len(rules[0].Rooms) != 1 || rules[0].Rooms[0].ID != "omm_1" {
		t.Errorf("应取编号最小星期（周一）的会议室列表: %+v", rules[0].Rooms)
	}
	if rules[0].StartDate == "" {
		t.Error("回填后首次目标日期不应为空")
	}
	rec, ok := st.RecordOf("rule_old", "2026-11-02")
	if !ok || rec.Status != model.StatusBooked || rec.RoomID != "omm_1" {
		t.Fatalf("记录迁移失败: %+v ok=%v", rec, ok)
	}
	for _, name := range []string{"rules.json", "records.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("%s 迁移后应被改名", name)
		}
	}
}
