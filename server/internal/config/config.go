// Package config 管理服务运行配置，持久化在数据目录的 config.json。
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Config 服务配置。字段含义见 JSON 标签。
type Config struct {
	Port    int    `json:"port"`
	DataDir string `json:"dataDir"`
	// LarkCliPath lark-cli 可执行文件路径；留空则从 PATH 查找。
	LarkCliPath string `json:"larkCliPath"`
	// Timezone 规则日期解释所用时区（IANA 名称）。
	Timezone string `json:"timezone"`
	// TickSeconds 调度器扫描间隔。
	TickSeconds int `json:"tickSeconds"`
	// GrabWindowMinutes 每次预约窗口总时长（分钟）。
	GrabWindowMinutes int `json:"grabWindowMinutes"`
	// GrabIntervalSeconds 窗口内两次尝试的间隔（秒）。
	GrabIntervalSeconds int `json:"grabIntervalSeconds"`
	// AttemptLogCap 尝试日志保留条数。
	AttemptLogCap int `json:"attemptLogCap"`
}

// Default 返回默认配置。
func Default() Config {
	return Config{
		Port:                8787,
		DataDir:             "data",
		LarkCliPath:         "lark-cli",
		Timezone:            "Asia/Shanghai",
		TickSeconds:         15,
		GrabWindowMinutes:   10,
		GrabIntervalSeconds: 60,
		AttemptLogCap:       500,
	}
}

// Load 从数据目录读取配置，不存在或字段缺失时回落默认值。
func Load(dataDir string) Config {
	cfg := Default()
	if dataDir != "" {
		cfg.DataDir = dataDir
	}
	raw, err := os.ReadFile(filepath.Join(cfg.DataDir, "config.json"))
	if err != nil {
		return cfg
	}
	merged := cfg
	if err := json.Unmarshal(raw, &merged); err != nil {
		return cfg
	}
	merged.Normalize()
	return merged
}

func (c *Config) Normalize() {
	d := Default()
	if c.Port <= 0 {
		c.Port = d.Port
	}
	if c.DataDir == "" {
		c.DataDir = d.DataDir
	}
	if c.LarkCliPath == "" {
		c.LarkCliPath = d.LarkCliPath
	}
	if c.Timezone == "" {
		c.Timezone = d.Timezone
	}
	if c.TickSeconds <= 0 {
		c.TickSeconds = d.TickSeconds
	}
	if c.GrabWindowMinutes <= 0 {
		c.GrabWindowMinutes = d.GrabWindowMinutes
	}
	if c.GrabIntervalSeconds <= 0 {
		c.GrabIntervalSeconds = d.GrabIntervalSeconds
	}
	if c.AttemptLogCap <= 0 {
		c.AttemptLogCap = d.AttemptLogCap
	}
}

// Save 将配置写入数据目录。
func (c *Config) Save() error {
	if err := os.MkdirAll(c.DataDir, 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(c.DataDir, "config.json"), raw, 0o644)
}
