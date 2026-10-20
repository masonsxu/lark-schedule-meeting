// Package store 提供基于 JSON 文件的规则、记录与日志持久化。
package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/masonsxu/lark-schedule-meeting/server/internal/config"
	"github.com/masonsxu/lark-schedule-meeting/server/internal/model"
)

// Store 并发安全的数据存储。所有数据保存在数据目录下的 JSON 文件。
type Store struct {
	mu       sync.Mutex
	dir      string
	cfg      config.Config
	rules    map[string]*model.Rule
	records  map[string]*model.Record // key: ruleID|date
	attempts []model.Attempt
}

// Open 加载（或初始化）数据目录中的存储。
func Open(cfg config.Config) (*Store, error) {
	s := &Store{
		dir:      cfg.DataDir,
		cfg:      cfg,
		rules:    map[string]*model.Rule{},
		records:  map[string]*model.Record{},
		attempts: []model.Attempt{},
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return nil, err
	}
	if err := s.load("rules.json", &s.rules); err != nil {
		return nil, err
	}
	if err := s.load("records.json", &s.records); err != nil {
		return nil, err
	}
	if err := s.load("attempts.json", &s.attempts); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) load(name string, v any) error {
	raw, err := os.ReadFile(filepath.Join(s.dir, name))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, v)
}

func (s *Store) save(name string, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.dir, name), raw, 0o644)
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
	return s.save("config.json", s.cfg)
}

// Rules 返回全部规则（按创建时间升序）。
func (s *Store) Rules() []model.Rule {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]model.Rule, 0, len(s.rules))
	for _, r := range s.rules {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

// Rule 按 ID 查询规则。
func (s *Store) Rule(id string) (model.Rule, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rules[id]
	if !ok {
		return model.Rule{}, false
	}
	return *r, true
}

// UpsertRule 新建或更新规则。
func (s *Store) UpsertRule(r model.Rule) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	r.UpdatedAt = now
	if old, ok := s.rules[r.ID]; ok {
		r.CreatedAt = old.CreatedAt
	} else {
		r.CreatedAt = now
	}
	cp := r
	s.rules[r.ID] = &cp
	return s.save("rules.json", s.rules)
}

// DeleteRule 删除规则。
func (s *Store) DeleteRule(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.rules[id]; !ok {
		return false
	}
	delete(s.rules, id)
	_ = s.save("rules.json", s.rules)
	return true
}

// Records 返回全部预约记录（按更新时间倒序）。
func (s *Store) Records() []model.Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]model.Record, 0, len(s.records))
	for _, r := range s.records {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Date != out[j].Date {
			return out[i].Date > out[j].Date
		}
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out
}

func recordKey(ruleID, date string) string { return ruleID + "|" + date }

// UpsertRecord 按 ruleID+date 写入记录（保留首次创建时间）。
func (s *Store) UpsertRecord(rec model.Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := recordKey(rec.RuleID, rec.Date)
	now := time.Now()
	rec.UpdatedAt = now
	if old, ok := s.records[key]; ok {
		rec.ID = old.ID
		rec.CreatedAt = old.CreatedAt
	} else {
		rec.ID = newID("rec")
		rec.CreatedAt = now
	}
	cp := rec
	s.records[key] = &cp
	return s.save("records.json", s.records)
}

// DeleteRecord 删除一条记录。
func (s *Store) DeleteRecord(id string) (model.Record, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, r := range s.records {
		if r.ID == id {
			delete(s.records, key)
			_ = s.save("records.json", s.records)
			return *r, true
		}
	}
	return model.Record{}, false
}

// RecordOf 查询规则在指定日期的记录。
func (s *Store) RecordOf(ruleID, date string) (model.Record, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.records[recordKey(ruleID, date)]
	if !ok {
		return model.Record{}, false
	}
	return *r, true
}

// HasTerminal 判断规则在指定日期是否已有终态记录（下次循环跳过依据）。
func (s *Store) HasTerminal(ruleID, date string) bool {
	rec, ok := s.RecordOf(ruleID, date)
	return ok && rec.Terminal()
}

// AppendAttempt 追加一条尝试日志并裁剪到容量上限。
func (s *Store) AppendAttempt(a model.Attempt) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a.ID = newID("att")
	a.At = time.Now()
	s.attempts = append(s.attempts, a)
	if cap := s.cfg.AttemptLogCap; cap > 0 && len(s.attempts) > cap {
		s.attempts = s.attempts[len(s.attempts)-cap:]
	}
	_ = s.save("attempts.json", s.attempts)
}

// Attempts 返回最近 limit 条日志（新的在前）。
func (s *Store) Attempts(limit int) []model.Attempt {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(s.attempts)
	if limit <= 0 || limit > n {
		limit = n
	}
	out := make([]model.Attempt, limit)
	for i := 0; i < limit; i++ {
		out[i] = s.attempts[n-1-i]
	}
	return out
}

func newID(prefix string) string {
	return prefix + "_" + strings.ReplaceAll(time.Now().UTC().Format("20060102T150405.000000000"), ".", "")
}
