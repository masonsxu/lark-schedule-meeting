// lsm 是 lark-schedule-meeting 的服务端入口：
// 启动调度引擎与 HTTP API，并在存在 web/dist 时托管前端。
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/masonsxu/lark-schedule-meeting/server/internal/api"
	"github.com/masonsxu/lark-schedule-meeting/server/internal/config"
	"github.com/masonsxu/lark-schedule-meeting/server/internal/engine"
	"github.com/masonsxu/lark-schedule-meeting/server/internal/lark"
	"github.com/masonsxu/lark-schedule-meeting/server/internal/store"
)

var version = "dev"

func main() {
	var dataDir string
	var port int
	var addr string
	flag.StringVar(&dataDir, "data", "", "数据目录（默认 data）")
	flag.IntVar(&port, "port", 0, "HTTP 端口（覆盖配置文件）")
	flag.StringVar(&addr, "addr", "", "HTTP 监听地址（默认 0.0.0.0:port）")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	cfg := config.Load(dataDir)
	if port > 0 {
		cfg.Port = port
	}
	st, err := store.Open(cfg)
	if err != nil {
		log.Error("初始化数据目录失败", "dir", cfg.DataDir, "err", err)
		os.Exit(1)
	}

	lc := &lark.CLI{Path: cfg.LarkCliPath}
	eng := engine.New(st, lc, log)
	srv := api.New(st, eng, lc, log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go eng.Run(ctx)

	listen := fmt.Sprintf("%s:%d", listenAddr(addr), cfg.Port)
	httpServer := &http.Server{
		Addr:              listen,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		log.Info("HTTP 服务启动", "addr", listen, "version", version, "data_dir", cfg.DataDir)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("HTTP 服务异常退出", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	log.Info("正在退出")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(shutdownCtx)
	eng.StopAll()
}

func listenAddr(addr string) string {
	if addr != "" {
		return addr
	}
	return "0.0.0.0"
}
