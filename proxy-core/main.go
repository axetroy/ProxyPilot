package main

import (
	"context"
	"fmt"
	"net"
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
	"github.com/axetroy/ProxyPilot/proxy-core/rule"
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

	// 智能分流规则管理器：加载上次缓存（无缓存用内置兜底列表），
	// 并注入网关作为直连判断函数（开关关闭时恒走代理）。
	ruleMgr := rule.NewManager(cfg, busc, cfg.DBPath)
	if err := ruleMgr.LoadCache(); err != nil {
		busc.Debug(fmt.Sprintf("load pac rules cache: %v", err))
	}
	gw.SetShunt(ruleMgr.Shunt())

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
		Rule:      ruleMgr,
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
	go ruleMgr.Start(ctx)

	// API 监听端口：被占用时向后顺延（与网关端口顺延一致），实际端口通过
	// stdout 的 PROXYPILOT_API 告诉 Electron，由前端按实际地址访问。
	// 只有连续 maxAPIPortProbe 个端口都不可用时才视为致命错误退出。
	resolvedAPIBind, ln, err := resolveAPIBind(cfg.APIBind)
	if err != nil {
		fmt.Fprintf(os.Stderr, "api listen: %v\n", err)
		os.Exit(1)
	}
	srv := &http.Server{Handler: router}
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			busc.Error(fmt.Sprintf("api server: %v", err))
		}
	}()
	busc.Info(fmt.Sprintf("ProxyPilot core %s listening on %s", config.Version, resolvedAPIBind))

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
	fmt.Printf("PROXYPILOT_API=http://%s\n", resolvedAPIBind)

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

// maxAPIPortProbe API 端口被占用时向后顺延的最大尝试次数（与网关端口顺延一致）。
const maxAPIPortProbe = 100

// resolveAPIBind 尝试绑定 API 监听端口；被占用时向后顺延（17890 → 17891 → ...），
// 返回实际绑定的地址与 listener。顺延是为了在端口冲突时 core 仍能启动，实际地址
// 通过 stdout 的 PROXYPILOT_API 通知 Electron，前端按该地址访问（端口不写死）。
func resolveAPIBind(bind string) (string, net.Listener, error) {
	host, portStr, err := net.SplitHostPort(bind)
	if err != nil {
		return "", nil, fmt.Errorf("invalid api bind %q: %w", bind, err)
	}
	startPort, err := strconv.Atoi(portStr)
	if err != nil {
		return "", nil, fmt.Errorf("invalid api port %q: %w", portStr, err)
	}
	for port := startPort; port < startPort+maxAPIPortProbe; port++ {
		addr := net.JoinHostPort(host, strconv.Itoa(port))
		ln, err := net.Listen("tcp", addr)
		if err == nil {
			return addr, ln, nil
		}
	}
	return "", nil, fmt.Errorf("no free port found starting from %s (tried %d ports)", bind, maxAPIPortProbe)
}
