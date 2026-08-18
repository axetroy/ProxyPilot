package gateway

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/axetroy/ProxyPilot/proxy-core/bus"
	"github.com/axetroy/ProxyPilot/proxy-core/model"
	"github.com/axetroy/ProxyPilot/proxy-core/pool"
	"github.com/axetroy/ProxyPilot/proxy-core/scheduler"
	"github.com/axetroy/ProxyPilot/proxy-core/storage"
)

// connectProxyServer 返回一个简单的 HTTP CONNECT 转发代理（用于构造链路）。
func connectProxyServer(forwardTo string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "CONNECT required", http.StatusMethodNotAllowed)
			return
		}
		up, err := net.Dial("tcp", forwardTo)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		hj, ok := w.(http.Hijacker)
		if !ok {
			_ = up.Close()
			http.Error(w, "hijack unsupported", http.StatusInternalServerError)
			return
		}
		down, _, err := hj.Hijack()
		if err != nil {
			_ = up.Close()
			return
		}
		if _, err := down.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
			_ = up.Close()
			_ = down.Close()
			return
		}
		go func() { _, _ = io.Copy(up, down); _ = up.Close() }()
		go func() { _, _ = io.Copy(down, up); _ = down.Close() }()
	})
}

func addAliveNode(t *testing.T, m *pool.Manager, srv *httptest.Server) *model.ProxyNode {
	t.Helper()
	host, portStr, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatalf("split addr: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}
	n := &model.ProxyNode{Host: host, Port: port, Protocol: model.ProtocolHTTP, Status: model.StatusAlive, Score: 100, Latency: 1}
	if m.AddNodes([]*model.ProxyNode{n}) != 1 {
		t.Fatalf("failed to add node %s", srv.Listener.Addr())
	}
	return n
}

// TestGatewayChainStrategy 验证 chain 策略下网关按链路 A→B→target 建立隧道。
func TestGatewayChainStrategy(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "gateway-chain-ok")
	}))
	defer target.Close()
	hopB := httptest.NewServer(connectProxyServer(target.Listener.Addr().String()))
	defer hopB.Close()
	hopA := httptest.NewServer(connectProxyServer(hopB.Listener.Addr().String()))
	defer hopA.Close()

	st, err := storage.New(":memory:")
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	poolMgr := pool.NewManager(st, nil, bus.New(), 4)
	nodeA := addAliveNode(t, poolMgr, hopA)
	nodeB := addAliveNode(t, poolMgr, hopB)

	sel := scheduler.NewSelector(poolMgr)
	sel.SetStrategy(scheduler.StrategyChain)

	g := NewGateway(poolMgr, sel, nil, "127.0.0.1:0")
	g.SetChainsProvider(func() ([]model.ProxyChain, error) {
		return []model.ProxyChain{{
			ID:      1,
			Name:    "test-chain",
			Enabled: true,
			NodeIDs: []int64{nodeA.ID, nodeB.ID},
		}}, nil
	})

	conn, err := g.Upstream(context.Background(), target.Listener.Addr().String())
	if err != nil {
		t.Fatalf("Upstream via chain: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// 通过隧道发送明文 HTTP，验证到达 target。
	req, _ := http.NewRequest(http.MethodGet, "http://"+target.Listener.Addr().String()+"/", nil)
	if err := req.Write(conn); err != nil {
		t.Fatalf("write request: %v", err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(body) != "gateway-chain-ok" {
		t.Fatalf("response = %d %q, want 200 gateway-chain-ok", resp.StatusCode, body)
	}
}

// TestGatewayChainNoUsableChain 没有启用的链时返回明确错误。
func TestGatewayChainNoUsableChain(t *testing.T) {
	st, err := storage.New(":memory:")
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	poolMgr := pool.NewManager(st, nil, bus.New(), 4)

	sel := scheduler.NewSelector(poolMgr)
	sel.SetStrategy(scheduler.StrategyChain)

	g := NewGateway(poolMgr, sel, nil, "127.0.0.1:0")
	g.SetChainsProvider(func() ([]model.ProxyChain, error) {
		// 只有停用的链
		return []model.ProxyChain{{ID: 1, Name: "disabled", Enabled: false, NodeIDs: []int64{1}}}, nil
	})

	_, err = g.Upstream(context.Background(), "127.0.0.1:1")
	if err == nil {
		t.Fatal("expected error when no enabled chain")
	}
}

// TestGatewayChainUnavailable 链上节点死亡/缺失时跳过，尝试下一条可用链。
func TestGatewayChainUnavailable(t *testing.T) {
	hopB := httptest.NewServer(connectProxyServer("127.0.0.1:1"))
	defer hopB.Close()

	st, err := storage.New(":memory:")
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	poolMgr := pool.NewManager(st, nil, bus.New(), 4)
	nodeB := addAliveNode(t, poolMgr, hopB)

	sel := scheduler.NewSelector(poolMgr)
	sel.SetStrategy(scheduler.StrategyChain)
	g := NewGateway(poolMgr, sel, nil, "127.0.0.1:0")

	// 两条启用链：第一条节点 ID 不存在（不可用），第二条只有 hopB（连到死目标）。
	// 均失败时应返回错误。
	g.SetChainsProvider(func() ([]model.ProxyChain, error) {
		return []model.ProxyChain{
			{ID: 1, Name: "bad", Enabled: true, NodeIDs: []int64{99999}},
			{ID: 2, Name: "dead-target", Enabled: true, NodeIDs: []int64{nodeB.ID}},
		}, nil
	})

	_, err = g.Upstream(context.Background(), "127.0.0.1:1")
	if err == nil {
		t.Fatal("expected error when all chains fail")
	}
}
