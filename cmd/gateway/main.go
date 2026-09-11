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

// version 由构建注入:go build -ldflags "-X main.version=v0.0.1";仅回显在 /healthz 与启动日志。
var version = "dev"

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
	cfg.Version = version

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

	srv := server.New(cfg, st)

	// 明文合并面(dev/测试/容器 healthcheck 用)恒常起;TLS 双口按配置额外叠加。
	type listener struct {
		srv  *http.Server
		cert string
		key  string
	}
	insts := []listener{{
		srv: &http.Server{Addr: cfg.Listen, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second},
	}}
	if cfg.TLS.Enabled() {
		insts = append(insts,
			listener{
				srv:  &http.Server{Addr: cfg.TLS.APIListen, Handler: srv.HandlerAPI(), ReadHeaderTimeout: 10 * time.Second},
				cert: cfg.TLS.APICert, key: cfg.TLS.APIKey,
			},
			listener{
				srv:  &http.Server{Addr: cfg.TLS.AdminListen, Handler: srv.HandlerAdmin(), ReadHeaderTimeout: 10 * time.Second},
				cert: cfg.TLS.AdminCert, key: cfg.TLS.AdminKey,
			},
		)
	}

	logger.Info("gateway starting", "version", version, "listen", cfg.Listen, "db", cfg.DBPath)
	for _, in := range insts[1:] {
		logger.Info("tls listening", "addr", in.srv.Addr)
	}

	done := make(chan os.Signal, 1)
	signal.Notify(done, syscall.SIGINT, syscall.SIGTERM)

	serveErr := make(chan error, len(insts))
	for _, in := range insts {
		in := in
		go func() {
			if in.cert != "" {
				serveErr <- in.srv.ListenAndServeTLS(in.cert, in.key)
			} else {
				serveErr <- in.srv.ListenAndServe()
			}
		}()
	}

	select {
	case sig := <-done:
		logger.Info("shutting down", "signal", sig.String())
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, in := range insts {
			if err := in.srv.Shutdown(ctx); err != nil {
				logger.Error("shutdown", "addr", in.srv.Addr, "err", err)
			}
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
