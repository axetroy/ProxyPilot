package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
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
	defer func() { _ = st.Close() }()

	// 加载用户在前端持久化的配置，覆盖默认值/环境变量
	cfg.LoadOverrides(st)

	poolMgr := pool.NewManager(st, validator.NewCheckerWithAnonymity(cfg.CheckTarget, cfg.CheckAnonymityTarget, cfg.CheckTimeout), busc, cfg.CheckConcurrency)
	if err := poolMgr.Load(); err != nil {
		fmt.Fprintf(os.Stderr, "pool load: %v\n", err)
		os.Exit(1)
	}
	poolMgr.SetRefreshInterval(cfg.RefreshInterval)

	col := collector.NewManager(st, busc, poolMgr, cfg.CheckTimeout)
	sel := scheduler.NewSelector(poolMgr)
	gw := gateway.NewGateway(poolMgr, sel, busc, cfg.ProxyAddr())

	// 恢复用户上次指定的固定出口节点（存在且合法时）。
	if v, err := st.GetSetting(config.KeyPinnedProxy); err == nil && v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil && id > 0 {
			if poolMgr.Get(id) != nil {
				sel.Pin(id)
				busc.Info(fmt.Sprintf("restored pinned exit node id=%d", id))
			} else {
				// 节点已不存在（被删除/自动淘汰），清除悬空指定，避免下次启动再次恢复。
				_ = st.SetSetting(config.KeyPinnedProxy, "")
				busc.Info("cleared stale pinned exit node (node no longer exists)")
			}
		}
	}

	services := &api.Services{
		Cfg:       cfg,
		Store:     st,
		Pool:      poolMgr,
		Collector: col,
		Gateway:   gw,
		Selector:  sel,
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
	go poolMgr.RefreshLoop(ctx)

	srv := &http.Server{Addr: cfg.APIBind, Handler: router}
	go func() {
		busc.Info(fmt.Sprintf("ProxyPilot core %s listening on %s", config.Version, cfg.APIBind))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			busc.Error(fmt.Sprintf("api server: %v", err))
		}
	}()

	// 订阅服务：独立端口，仅暴露订阅端点。默认仅监听本机；
	// 如需局域网设备拉取订阅，用户需在设置中显式把监听地址改为 0.0.0.0:17891。
	var subSrv *http.Server
	if cfg.SubEnabled {
		subSrv = &http.Server{Addr: cfg.SubListen, Handler: api.NewSubscriptionRouter(services)}
		go func() {
			busc.Info(fmt.Sprintf("subscription service listening on %s", cfg.SubListen))
			if err := subSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				busc.Error(fmt.Sprintf("subscription server: %v", err))
			}
		}()
	} else {
		busc.Info("subscription service disabled")
	}

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
	if subSrv != nil {
		_ = subSrv.Shutdown(shutdownCtx)
	}
	gw.Stop()
}
