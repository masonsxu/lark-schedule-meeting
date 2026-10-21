// Package store 提供基于 SQLite 的规则、记录、日志与会议室名录持久化。
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"github.com/masonsxu/lark-schedule-meeting/server/internal/config"
	"github.com/masonsxu/lark-schedule-meeting/server/internal/model"
)

// Store 并发安全的 SQLite 存储。所有数据保存在数据目录的 lsm.db。
type Store struct {
	mu  sync.Mutex
	db  *sql.DB
	cfg config.Config
}

// Open 打开（或初始化）数据目录中的数据库，并迁移旧版 JSON 数据（若存在）。
func Open(cfg config.Config) (*Store, error) {
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return nil, err
	}
	dsn := filepath.Join(cfg.DataDir, "lsm.db") + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // SQLite 单写连接，避免锁竞争
	s := &Store{db: db, cfg: cfg}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	if err := s.migrateJSON(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS rules (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			weekdays TEXT NOT NULL,
			start_time TEXT NOT NULL,
			end_time TEXT NOT NULL,
			trigger_time TEXT NOT NULL,
			lead_days INTEGER NOT NULL DEFAULT 30,
			skip_weekends INTEGER,
			enabled INTEGER NOT NULL DEFAULT 0,
			allow_no_room INTEGER NOT NULL DEFAULT 0,
			window_minutes INTEGER NOT NULL DEFAULT 0,
			interval_seconds INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS records (
			id TEXT PRIMARY KEY,
			rule_id TEXT NOT NULL,
			rule_name TEXT NOT NULL,
			date TEXT NOT NULL,
			start TEXT NOT NULL,
			end TEXT NOT NULL,
			room_id TEXT NOT NULL DEFAULT '',
			room_name TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL,
			source TEXT NOT NULL,
			event_id TEXT NOT NULL DEFAULT '',
			calendar_id TEXT NOT NULL DEFAULT '',
			note TEXT NOT NULL DEFAULT '',
			attempts INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			UNIQUE(rule_id, date)
		)`,
		`CREATE TABLE IF NOT EXISTS attempts (
			id TEXT PRIMARY KEY,
			at TEXT NOT NULL,
			rule_id TEXT NOT NULL,
			rule_name TEXT NOT NULL DEFAULT '',
			date TEXT NOT NULL DEFAULT '',
			kind TEXT NOT NULL,
			ok INTEGER NOT NULL DEFAULT 0,
			detail TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_attempts_at ON attempts(at DESC)`,
		`CREATE TABLE IF NOT EXISTS rooms (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL DEFAULT '',
			capacity INTEGER NOT NULL DEFAULT 0,
			source TEXT NOT NULL DEFAULT 'manual',
			updated_at TEXT NOT NULL
		)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("初始化表结构失败: %w", err)
		}
	}
	return nil
}

// migrateJSON 一次性导入旧版 JSON 数据文件（导入后重命名为 *.migrated）。
func (s *Store) migrateJSON() error {
	imports := []struct {
		name string
		run  func() error
	}{
		{"rules.json", func() error {
			var rules map[string]model.Rule
			if err := readJSON(s.cfg.DataDir, "rules.json", &rules); err != nil || len(rules) == 0 {
				return err
			}
			for _, r := range rules {
				cp := r
				if err := s.UpsertRule(cp); err != nil {
					return err
				}
			}
			return nil
		}},
		{"records.json", func() error {
			// 旧版 records.json 为 map[ruleID|date]Record。
			var recs map[string]model.Record
			if err := readJSON(s.cfg.DataDir, "records.json", &recs); err != nil || len(recs) == 0 {
				return err
			}
			for _, r := range recs {
				cp := r
				if err := s.UpsertRecord(cp); err != nil {
					return err
				}
			}
			return nil
		}},
		{"attempts.json", func() error {
			var atts []model.Attempt
			if err := readJSON(s.cfg.DataDir, "attempts.json", &atts); err != nil || len(atts) == 0 {
				return err
			}
			for _, a := range atts {
				s.AppendAttempt(a)
			}
			return nil
		}},
	}
	for _, imp := range imports {
		path := filepath.Join(s.cfg.DataDir, imp.name)
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err := imp.run(); err != nil {
			return fmt.Errorf("迁移 %s 失败: %w", imp.name, err)
		}
		_ = os.Rename(path, path+".migrated")
	}
	return nil
}

func readJSON(dir, name string, v any) error {
	raw, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, v)
}

func fmtTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// Config 返回当前配置。
func (s *Store) Config() config.Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg
}

// SetConfig 更新并持久化配置。
func (s *Store) SetConfig(cfg config.Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg.Normalize()
	s.cfg = cfg
	return cfg.Save()
}

// ---- 规则 ----

const ruleCols = `id, name, description, weekdays, start_time, end_time, trigger_time,
	lead_days, skip_weekends, enabled, allow_no_room, window_minutes, interval_seconds, created_at, updated_at`

func scanRule(scan func(dest ...any) error) (model.Rule, error) {
	var r model.Rule
	var weekdays string
	var skipWeekends sql.NullInt64
	var createdAt, updatedAt string
	if err := scan(&r.ID, &r.Name, &r.Description, &weekdays, &r.StartTime, &r.EndTime, &r.TriggerTime,
		&r.LeadDays, &skipWeekends, &r.Enabled, &r.AllowNoRoom, &r.WindowMinutes, &r.IntervalSeconds,
		&createdAt, &updatedAt); err != nil {
		return r, err
	}
	_ = json.Unmarshal([]byte(weekdays), &r.Weekdays)
	if skipWeekends.Valid {
		v := skipWeekends.Int64 == 1
		r.SkipWeekends = &v
	}
	r.CreatedAt = parseTime(createdAt)
	r.UpdatedAt = parseTime(updatedAt)
	return r, nil
}

// Rules 返回全部规则（按创建时间升序）。
func (s *Store) Rules() []model.Rule {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT ` + ruleCols + ` FROM rules ORDER BY created_at, id`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := []model.Rule{}
	for rows.Next() {
		if r, err := scanRule(rows.Scan); err == nil {
			out = append(out, r)
		}
	}
	return out
}

// Rule 按 ID 查询规则。
func (s *Store) Rule(id string) (model.Rule, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ruleLocked(id)
}

func (s *Store) ruleLocked(id string) (model.Rule, bool) {
	row := s.db.QueryRow(`SELECT `+ruleCols+` FROM rules WHERE id = ?`, id)
	r, err := scanRule(row.Scan)
	if err != nil {
		return model.Rule{}, false
	}
	return r, true
}

// UpsertRule 新建或更新规则。
func (s *Store) UpsertRule(r model.Rule) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	r.UpdatedAt = now
	if old, ok := s.ruleLocked(r.ID); ok {
		r.CreatedAt = old.CreatedAt
	} else {
		r.CreatedAt = now
	}
	weekdays, err := json.Marshal(r.Weekdays)
	if err != nil {
		return err
	}
	var skipWeekends any
	if r.SkipWeekends != nil {
		skipWeekends = boolToInt(*r.SkipWeekends)
	}
	_, err = s.db.Exec(`INSERT INTO rules
		(id, name, description, weekdays, start_time, end_time, trigger_time, lead_days, skip_weekends,
		 enabled, allow_no_room, window_minutes, interval_seconds, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			name=excluded.name, description=excluded.description, weekdays=excluded.weekdays,
			start_time=excluded.start_time, end_time=excluded.end_time, trigger_time=excluded.trigger_time,
			lead_days=excluded.lead_days, skip_weekends=excluded.skip_weekends, enabled=excluded.enabled,
			allow_no_room=excluded.allow_no_room, window_minutes=excluded.window_minutes,
			interval_seconds=excluded.interval_seconds, updated_at=excluded.updated_at`,
		r.ID, r.Name, r.Description, string(weekdays), r.StartTime, r.EndTime, r.TriggerTime,
		r.LeadDaysOr(), skipWeekends, r.Enabled, r.AllowNoRoom, r.WindowMinutes, r.IntervalSeconds,
		fmtTime(r.CreatedAt), fmtTime(r.UpdatedAt))
	return err
}

// DeleteRule 删除规则。
func (s *Store) DeleteRule(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec(`DELETE FROM rules WHERE id = ?`, id)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n > 0
}

// ---- 记录 ----

const recordCols = `id, rule_id, rule_name, date, start, end, room_id, room_name,
	status, source, event_id, calendar_id, note, attempts, created_at, updated_at`

func scanRecord(scan func(dest ...any) error) (model.Record, error) {
	var r model.Record
	var createdAt, updatedAt string
	if err := scan(&r.ID, &r.RuleID, &r.RuleName, &r.Date, &r.Start, &r.End, &r.RoomID, &r.RoomName,
		&r.Status, &r.Source, &r.EventID, &r.CalendarID, &r.Note, &r.Attempts, &createdAt, &updatedAt); err != nil {
		return r, err
	}
	r.CreatedAt = parseTime(createdAt)
	r.UpdatedAt = parseTime(updatedAt)
	return r, nil
}

// Records 返回全部预约记录（按日期与更新时间倒序）。
func (s *Store) Records() []model.Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT ` + recordCols + ` FROM records ORDER BY date DESC, updated_at DESC`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := []model.Record{}
	for rows.Next() {
		if r, err := scanRecord(rows.Scan); err == nil {
			out = append(out, r)
		}
	}
	return out
}

// UpsertRecord 按 ruleID+date 写入记录（保留首次创建时间）。
func (s *Store) UpsertRecord(rec model.Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	rec.UpdatedAt = now
	var createdAt string
	err := s.db.QueryRow(`SELECT created_at FROM records WHERE rule_id = ? AND date = ?`, rec.RuleID, rec.Date).Scan(&createdAt)
	if err == nil {
		rec.CreatedAt = parseTime(createdAt)
	} else {
		rec.CreatedAt = now
	}
	if rec.ID == "" {
		rec.ID = newID("rec")
	}
	_, err = s.db.Exec(`INSERT INTO records
		(id, rule_id, rule_name, date, start, end, room_id, room_name, status, source, event_id,
		 calendar_id, note, attempts, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(rule_id, date) DO UPDATE SET
			id=excluded.id, start=excluded.start, end=excluded.end,
			room_id=excluded.room_id, room_name=excluded.room_name,
			status=excluded.status, source=excluded.source, event_id=excluded.event_id,
			calendar_id=excluded.calendar_id, note=excluded.note, attempts=excluded.attempts,
			updated_at=excluded.updated_at`,
		rec.ID, rec.RuleID, rec.RuleName, rec.Date, rec.Start, rec.End, rec.RoomID, rec.RoomName,
		rec.Status, rec.Source, rec.EventID, rec.CalendarID, rec.Note, rec.Attempts,
		fmtTime(rec.CreatedAt), fmtTime(rec.UpdatedAt))
	return err
}

// DeleteRecord 删除一条记录。
func (s *Store) DeleteRecord(id string) (model.Record, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row := s.db.QueryRow(`SELECT `+recordCols+` FROM records WHERE id = ?`, id)
	r, err := scanRecord(row.Scan)
	if err != nil {
		return model.Record{}, false
	}
	if _, err := s.db.Exec(`DELETE FROM records WHERE id = ?`, id); err != nil {
		return model.Record{}, false
	}
	return r, true
}

// RecordOf 查询规则在指定日期的记录。
func (s *Store) RecordOf(ruleID, date string) (model.Record, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row := s.db.QueryRow(`SELECT `+recordCols+` FROM records WHERE rule_id = ? AND date = ?`, ruleID, date)
	r, err := scanRecord(row.Scan)
	if err != nil {
		return model.Record{}, false
	}
	return r, true
}

// HasTerminal 判断规则在指定日期是否已有终态记录（下次循环跳过依据）。
func (s *Store) HasTerminal(ruleID, date string) bool {
	rec, ok := s.RecordOf(ruleID, date)
	return ok && rec.Terminal()
}

// ---- 尝试日志 ----

// AppendAttempt 追加一条尝试日志并裁剪到容量上限。
func (s *Store) AppendAttempt(a model.Attempt) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if a.ID == "" {
		a.ID = newID("att")
	}
	a.At = time.Now()
	if _, err := s.db.Exec(`INSERT INTO attempts (id, at, rule_id, rule_name, date, kind, ok, detail)
		VALUES (?,?,?,?,?,?,?,?)`,
		a.ID, fmtTime(a.At), a.RuleID, a.RuleName, a.Date, a.Kind, boolToInt(a.OK), a.Detail); err != nil {
		return
	}
	if cap := s.cfg.AttemptLogCap; cap > 0 {
		_, _ = s.db.Exec(`DELETE FROM attempts WHERE id NOT IN (
			SELECT id FROM attempts ORDER BY at DESC, id DESC LIMIT ?)`, cap)
	}
}

// Attempts 返回最近 limit 条日志（新的在前）。
func (s *Store) Attempts(limit int) []model.Attempt {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(`SELECT id, at, rule_id, rule_name, date, kind, ok, detail
		FROM attempts ORDER BY at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := []model.Attempt{}
	for rows.Next() {
		var a model.Attempt
		var at string
		var ok int
		if err := rows.Scan(&a.ID, &at, &a.RuleID, &a.RuleName, &a.Date, &a.Kind, &ok, &a.Detail); err != nil {
			continue
		}
		a.At = parseTime(at)
		a.OK = ok == 1
		out = append(out, a)
	}
	return out
}

// ---- 会议室名录 ----

// Room 名录条目。
type Room struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Capacity  int       `json:"capacity,omitempty"`
	Source    string    `json:"source"` // manual | room-find | record
	UpdatedAt time.Time `json:"updatedAt"`
}

// UpsertRoom 写入（或更新）名录中的会议室。
func (s *Store) UpsertRoom(id, name string, capacity int, source string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("会议室 ID 不能为空")
	}
	_, err := s.db.Exec(`INSERT INTO rooms (id, name, capacity, source, updated_at) VALUES (?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			name=CASE WHEN excluded.name != '' THEN excluded.name ELSE rooms.name END,
			capacity=CASE WHEN excluded.capacity > 0 THEN excluded.capacity ELSE rooms.capacity END,
			source=excluded.source, updated_at=excluded.updated_at`,
		id, strings.TrimSpace(name), capacity, source, fmtTime(time.Now()))
	return err
}

// Rooms 返回全部名录会议室（按名称、ID 排序）。
func (s *Store) Rooms() []Room {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT id, name, capacity, source, updated_at FROM rooms
		ORDER BY CASE WHEN name != '' THEN name ELSE id END, id`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := []Room{}
	for rows.Next() {
		var r Room
		var updatedAt string
		if err := rows.Scan(&r.ID, &r.Name, &r.Capacity, &r.Source, &updatedAt); err != nil {
			continue
		}
		r.UpdatedAt = parseTime(updatedAt)
		out = append(out, r)
	}
	return out
}

// DeleteRoom 从名录删除一间会议室。
func (s *Store) DeleteRoom(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec(`DELETE FROM rooms WHERE id = ?`, id)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n > 0
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func newID(prefix string) string {
	return prefix + "_" + strings.ReplaceAll(time.Now().UTC().Format("20060102T150405.000000000"), ".", "")
}
