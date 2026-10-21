// Package api 提供 HTTP 接口与前端静态资源托管。
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/masonsxu/lark-schedule-meeting/server/internal/engine"
	"github.com/masonsxu/lark-schedule-meeting/server/internal/lark"
	"github.com/masonsxu/lark-schedule-meeting/server/internal/model"
	"github.com/masonsxu/lark-schedule-meeting/server/internal/store"
)

// Server HTTP 服务。
type Server struct {
	store  *store.Store
	engine *engine.Engine
	lark   lark.Client
	log    *slog.Logger
}

// New 创建 HTTP 服务。
func New(st *store.Store, eng *engine.Engine, lc lark.Client, log *slog.Logger) *Server {
	return &Server{store: st, engine: eng, lark: lc, log: log}
}

// Handler 组装路由。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/config", s.handleGetConfig)
	mux.HandleFunc("PUT /api/config", s.handlePutConfig)

	mux.HandleFunc("GET /api/rules", s.handleListRules)
	mux.HandleFunc("POST /api/rules", s.handleCreateRule)
	mux.HandleFunc("GET /api/rules/{id}", s.handleGetRule)
	mux.HandleFunc("PUT /api/rules/{id}", s.handlePutRule)
	mux.HandleFunc("DELETE /api/rules/{id}", s.handleDeleteRule)
	mux.HandleFunc("POST /api/rules/{id}/run", s.handleRunRule)
	mux.HandleFunc("GET /api/rules/{id}/records", s.handleRuleRecords)

	mux.HandleFunc("GET /api/records", s.handleRecords)
	mux.HandleFunc("DELETE /api/records/{id}", s.handleDeleteRecord)
	mux.HandleFunc("GET /api/attempts", s.handleAttempts)
	mux.HandleFunc("GET /api/sessions", s.handleSessions)

	mux.HandleFunc("POST /api/rooms/available", s.handleRoomsAvailable)
	mux.HandleFunc("GET /api/rooms", s.handleListRooms)
	mux.HandleFunc("POST /api/rooms", s.handleAddRoom)
	mux.HandleFunc("DELETE /api/rooms/{id}", s.handleDeleteRoom)

	// 前端静态资源（存在 web/dist 时）。
	mux.HandleFunc("/", s.handleStatic)

	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]any{"ok": false, "error": err.Error()})
}

func decodeBody(r *http.Request, v any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("请求体不是合法 JSON: %w", err)
	}
	return nil
}

// ---- health / config ----

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := contextWithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	id, err := s.lark.WhoAmI(ctx)
	larkOK := err == nil
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"lark": map[string]any{"ok": larkOK, "identity": id, "error": msg},
		"now":  time.Now().Format(time.RFC3339),
	})
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.store.Config())
}

func (s *Server) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	var cfg configPayload
	if err := decodeBody(r, &cfg); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	next := s.store.Config()
	if cfg.Port != nil {
		next.Port = *cfg.Port
	}
	if cfg.LarkCliPath != nil {
		next.LarkCliPath = *cfg.LarkCliPath
	}
	if cfg.Timezone != nil {
		next.Timezone = *cfg.Timezone
	}
	if cfg.TickSeconds != nil {
		next.TickSeconds = *cfg.TickSeconds
	}
	if cfg.GrabWindowMinutes != nil {
		next.GrabWindowMinutes = *cfg.GrabWindowMinutes
	}
	if cfg.GrabIntervalSeconds != nil {
		next.GrabIntervalSeconds = *cfg.GrabIntervalSeconds
	}
	if cfg.AttemptLogCap != nil {
		next.AttemptLogCap = *cfg.AttemptLogCap
	}
	if err := s.store.SetConfig(next); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.engine.ConfigChanged()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "config": s.store.Config()})
}

type configPayload struct {
	Port                *int    `json:"port"`
	LarkCliPath         *string `json:"larkCliPath"`
	Timezone            *string `json:"timezone"`
	TickSeconds         *int    `json:"tickSeconds"`
	GrabWindowMinutes   *int    `json:"grabWindowMinutes"`
	GrabIntervalSeconds *int    `json:"grabIntervalSeconds"`
	AttemptLogCap       *int    `json:"attemptLogCap"`
}

func contextWithTimeout(parent context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, d)
}

// ---- rules ----

func (s *Server) handleListRules(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.store.Rules())
}

func (s *Server) handleCreateRule(w http.ResponseWriter, r *http.Request) {
	var rule model.Rule
	if err := decodeBody(r, &rule); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	rule.ID = ""
	if err := rule.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	rule.ID = newRuleID()
	if err := s.store.UpsertRule(rule); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	saved, _ := s.store.Rule(rule.ID)
	writeJSON(w, http.StatusCreated, saved)
}

func (s *Server) handleGetRule(w http.ResponseWriter, r *http.Request) {
	rule, ok := s.store.Rule(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, errors.New("规则不存在"))
		return
	}
	writeJSON(w, http.StatusOK, rule)
}

func (s *Server) handlePutRule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.store.Rule(id); !ok {
		writeErr(w, http.StatusNotFound, errors.New("规则不存在"))
		return
	}
	var rule model.Rule
	if err := decodeBody(r, &rule); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	rule.ID = id
	if err := rule.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.store.UpsertRule(rule); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	saved, _ := s.store.Rule(id)
	writeJSON(w, http.StatusOK, saved)
}

func (s *Server) handleDeleteRule(w http.ResponseWriter, r *http.Request) {
	if !s.store.DeleteRule(r.PathValue("id")) {
		writeErr(w, http.StatusNotFound, errors.New("规则不存在"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleRunRule(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Date            string `json:"date"`
		MaxAttempts     int    `json:"maxAttempts"`
		IntervalSeconds int    `json:"intervalSeconds"`
		Force           bool   `json:"force"`
	}
	if r.Body != nil {
		if err := decodeBody(r, &body); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
	}
	if body.MaxAttempts > 100 {
		writeErr(w, http.StatusBadRequest, errors.New("maxAttempts 上限 100"))
		return
	}
	opts := engine.RunOptions{
		Source:          "manual",
		Date:            body.Date,
		MaxAttempts:     body.MaxAttempts,
		IntervalSeconds: body.IntervalSeconds,
		Force:           body.Force,
	}
	if err := s.engine.Start(r.PathValue("id"), opts); err != nil {
		writeErr(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true})
}

func (s *Server) handleRuleRecords(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.store.Rule(id); !ok {
		writeErr(w, http.StatusNotFound, errors.New("规则不存在"))
		return
	}
	all := s.store.Records()
	out := make([]model.Record, 0)
	for _, rec := range all {
		if rec.RuleID == id {
			out = append(out, rec)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// ---- records / attempts / sessions ----

func (s *Server) handleRecords(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.store.Records())
}

func (s *Server) handleDeleteRecord(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.store.DeleteRecord(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, errors.New("记录不存在"))
		return
	}
	if r.URL.Query().Get("deleteEvent") == "true" && rec.EventID != "" {
		ctx, cancel := contextWithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		if err := s.lark.DeleteEvent(ctx, rec.CalendarID, rec.EventID); err != nil {
			writeErr(w, http.StatusBadGateway, fmt.Errorf("记录已删除，但飞书日程删除失败: %w", err))
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "deleted": rec})
}

func (s *Server) handleAttempts(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		if _, err := fmt.Sscanf(v, "%d", &limit); err != nil || limit <= 0 || limit > 1000 {
			writeErr(w, http.StatusBadRequest, errors.New("limit 应为 1-1000"))
			return
		}
	}
	writeJSON(w, http.StatusOK, s.store.Attempts(limit))
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.engine.Snapshot())
}

// ---- rooms ----

func (s *Server) handleRoomsAvailable(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Start     string   `json:"start"` // ISO 8601 带时区
		End       string   `json:"end"`
		RoomNames []string `json:"roomNames"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	start, err := time.Parse(time.RFC3339, body.Start)
	if err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("start 需为 ISO 8601 带时区时间: %w", err))
		return
	}
	end, err := time.Parse(time.RFC3339, body.End)
	if err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("end 需为 ISO 8601 带时区时间: %w", err))
		return
	}
	if !end.After(start) {
		writeErr(w, http.StatusBadRequest, errors.New("end 必须晚于 start"))
		return
	}
	ctx, cancel := contextWithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	rooms, err := s.lark.RoomFind(ctx, start, end, body.RoomNames)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	// 检索结果自动进入本地名录（ID 稳定，可长期复用）。
	for _, room := range rooms {
		_ = s.store.UpsertRoom(room.ID, room.Name, room.Capacity, "room-find")
	}
	writeJSON(w, http.StatusOK, map[string]any{"start": body.Start, "end": body.End, "rooms": rooms})
}

// ---- rooms directory ----

func (s *Server) handleListRooms(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.store.Rooms())
}

func (s *Server) handleAddRoom(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(body.ID) == "" {
		writeErr(w, http.StatusBadRequest, errors.New("会议室 ID（omm_ 开头）不能为空"))
		return
	}
	if err := s.store.UpsertRoom(body.ID, body.Name, 0, "manual"); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true})
}

func (s *Server) handleDeleteRoom(w http.ResponseWriter, r *http.Request) {
	if !s.store.DeleteRoom(r.PathValue("id")) {
		writeErr(w, http.StatusNotFound, errors.New("名录中不存在该会议室"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---- static ----

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeErr(w, http.StatusNotFound, errors.New("接口不存在"))
		return
	}
	root := "web/dist"
	if _, err := os.Stat(filepath.Join(root, "index.html")); err != nil {
		http.Error(w, "前端未构建（web/dist 不存在）；开发模式请使用 pnpm dev:web", http.StatusNotFound)
		return
	}
	path := filepath.Join(root, filepath.Clean("/"+r.URL.Path))
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		http.ServeFile(w, r, path)
		return
	}
	http.ServeFile(w, r, filepath.Join(root, "index.html"))
}

func newRuleID() string {
	return fmt.Sprintf("rule_%x", time.Now().UnixNano())
}
