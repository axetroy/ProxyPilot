package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/axetroy/ProxyPilot/proxy-core/api"
	"github.com/axetroy/ProxyPilot/proxy-core/bus"
	"github.com/axetroy/ProxyPilot/proxy-core/collector"
	"github.com/axetroy/ProxyPilot/proxy-core/config"
	"github.com/axetroy/ProxyPilot/proxy-core/gateway"
	"github.com/axetroy/ProxyPilot/proxy-core/pool"
	"github.com/axetroy/ProxyPilot/proxy-core/scheduler"
	"github.com/axetroy/ProxyPilot/proxy-core/storage"
	"github.com/axetroy/ProxyPilot/proxy-core/validator"
)

func main() {
	cfg := config.New()
	busc := bus.New()

	st, err := storage.New(cfg.DBPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "storage: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	poolMgr := pool.NewManager(st, validator.NewChecker(cfg.CheckTarget, cfg.CheckTimeout), busc, cfg.CheckConcurrency)
	if err := poolMgr.Load(); err != nil {
		fmt.Fprintf(os.Stderr, "pool load: %v\n", err)
		os.Exit(1)
	}

	col := collector.NewManager(st, busc, poolMgr, cfg.CheckTimeout)
	sel := scheduler.NewSelector(poolMgr)
	gw := gateway.NewGateway(poolMgr, sel, busc, cfg.HTTPProxyBind, cfg.SOCKSProxyBind)

	services := &api.Services{
		Cfg:       cfg,
		Store:     st,
		Pool:      poolMgr,
		Collector: col,
		Gateway:   gw,
		Bus:       busc,
	}
	router := api.NewRouter(services)

	// 启动时自动启动网关，让客户端打开即可直接使用代理，
	// 无需手动点击"启动网关"。若端口被占用等导致失败，仅记录错误，
	// 不阻塞 API 服务，用户仍可在界面中手动重试。
	if err := gw.Start(); err != nil {
		busc.Error(fmt.Sprintf("auto start gateway failed: %v", err))
	} else {
		busc.Info("gateway auto-started")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = col.Run(ctx) }()
	go poolMgr.RefreshLoop(ctx, cfg.RefreshInterval)

	srv := &http.Server{Addr: cfg.APIBind, Handler: router}
	go func() {
		busc.Info(fmt.Sprintf("ProxyPilot core %s listening on %s", config.Version, cfg.APIBind))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			busc.Error(fmt.Sprintf("api server: %v", err))
		}
	}()

	// Print the session token for the Electron wrapper to pick up.
	fmt.Printf("PROXYPILOT_TOKEN=%s\n", cfg.SessionToken)
	fmt.Printf("PROXYPILOT_API=http://%s\n", cfg.APIBind)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig

	fmt.Println("shutting down...")
	busc.Info("shutting down")
	col.Stop()
	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer shutdownCancel()
	_ = srv.Shutdown(shutdownCtx)
	gw.Stop()
}
