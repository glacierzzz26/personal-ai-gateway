// gateway 是 personal-ai-gateway 的入口:组装 config → store → router → proxy → server。
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"personal-ai-gateway/internal/config"
	"personal-ai-gateway/internal/pricing"
	"personal-ai-gateway/internal/proxy"
	"personal-ai-gateway/internal/quota"
	"personal-ai-gateway/internal/router"
	"personal-ai-gateway/internal/server"
	"personal-ai-gateway/internal/store"
)

func main() {
	cfgPath := flag.String("config", "config.yaml", "path to YAML config")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		logger.Error("config", "err", err)
		os.Exit(1)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		logger.Error("store", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	// 订阅源以 DB upstreams 表为唯一权威(管理 API 增删改的落点),
	// config.yaml 不再承载上游(见 DESIGN 决策 #13)。表空 = 空上游的合法启动态:
	// 网关照常服务管理面与 /healthz,模型请求无源可路由 → 404(not_found_error)。
	ups, err := st.LoadUpstreams()
	if err != nil {
		logger.Error("store", "err", err)
		os.Exit(1)
	}
	if len(ups) == 0 {
		logger.Info("no upstreams configured",
			"msg", "model requests will 404 until an upstream is added via /api/v1/upstreams")
	}
	// raw → resolved:展开 ${ENV} 并补默认值;router/quota/proxy 只用这份。
	resolved := config.ResolveUpstreams(ups)

	rt := router.New(resolved)
	gw := proxy.New(rt, st, pricing.New(cfg.Pricing))
	gw.Logger = logger

	// 配额感知选路:P3。启用了 quota 的上游由管理器后台轮询,
	// 快照实时推给 router(超过 hard 阈值的上游自动降级到备选)。
	qm := quota.NewManager(resolved, &quota.HTTPFetcher{}, logger)
	qm.SetUpdater(rt.SetQuota)
	qctx, qcancel := context.WithCancel(context.Background())
	defer qcancel()
	go qm.Run(qctx)
	quotaEnabled := 0
	for _, u := range resolved {
		if u.Quota != nil && u.Quota.Enabled {
			quotaEnabled++
		}
	}
	if quotaEnabled > 0 {
		logger.Info("quota manager", "enabled_upstreams", quotaEnabled)
	}

	srv := server.New(&cfg, gw, logger)
	srv.SetQuotaManager(qm)

	httpSrv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	logger.Info("gateway starting",
		"listen", cfg.Listen,
		"db", cfg.DBPath,
		"keys", len(cfg.Keys),
	)
	for _, u := range resolved {
		logger.Info("upstream",
			"name", u.Name, "type", u.Type, "base", u.BaseURL,
			"priority", u.Priority, "models", u.Models)
	}

	// 优雅退出:关停 http server
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
