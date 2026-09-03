// gateway 是 personal-ai-gateway 的入口:组装 config → 主密钥 → store(新库+迁移)→ server。
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"personal-ai-gateway/internal/config"
	"personal-ai-gateway/internal/secret"
	"personal-ai-gateway/internal/server"
	"personal-ai-gateway/internal/store"
)

func main() {
	cfgPath := flag.String("config", "config.yaml", "path to YAML config")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// 配置文件缺失时用默认(空 config)→ listen :8787 / gateway-v2.db
	cfg := config.Config{}
	if _, err := os.Stat(*cfgPath); err == nil {
		cfg, err = config.Load(*cfgPath)
		if err != nil {
			logger.Error("config", "err", err)
			os.Exit(1)
		}
	} else {
		cfg.Listen = ":8787"
		cfg.DBPath = "gateway-v2.db"
		logger.Info("no config file, using defaults", "listen", cfg.Listen, "db", cfg.DBPath)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		logger.Error("store", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	// 主密钥引导(渠道 api_key 加密用):GW_MASTER_KEY 优先,否则 DB 同目录自动生成。
	if _, err := secret.BootstrapKey(filepath.Dir(abs(cfg.DBPath))); err != nil {
		logger.Error("secret", "err", err)
		os.Exit(1)
	}

	if err := st.PruneExpiredSessions(); err != nil {
		logger.Warn("prune sessions", "err", err)
	}

	srv := server.New(cfg, st)

	httpSrv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	logger.Info("gateway starting",
		"listen", cfg.Listen,
		"db", cfg.DBPath,
	)

	done := make(chan os.Signal, 1)
	signal.Notify(done, syscall.SIGINT, syscall.SIGTERM)

	serveErr := make(chan error, 1)
	go func() { serveErr <- httpSrv.ListenAndServe() }()

	select {
	case sig := <-done:
		logger.Info("shutting down", "signal", sig.String())
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpSrv.Shutdown(ctx); err != nil {
			logger.Error("shutdown", "err", err)
		}
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server", "err", err)
			os.Exit(1)
		}
	}
}

func abs(p string) string {
	a, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return a
}
