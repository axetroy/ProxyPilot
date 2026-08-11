package gateway

import (
	"fmt"
	"net"
	"testing"
)

// 模拟 pool/selector 为空时网关也能启动监听端口（Start 不依赖它们）。
func TestStartPortAutoShift(t *testing.T) {
	// 先占用一个端口，作为"配置端口被占用"的场景
	blocker, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("blocker listen: %v", err)
	}
	defer func() { _ = blocker.Close() }()

	blockedPort := blocker.Addr().(*net.TCPAddr).Port
	baseHTTP := fmt.Sprintf("127.0.0.1:%d", blockedPort)
	baseSOCKS := fmt.Sprintf("127.0.0.1:%d", blockedPort+1)

	g := NewGateway(nil, nil, nil, baseHTTP, baseSOCKS)
	if err := g.Start(); err != nil {
		t.Fatalf("Start with blocked port: %v", err)
	}
	defer g.Stop()

	httpAddr := g.HTTPAddr()
	socksAddr := g.SOCKSAddr()
	if httpAddr == baseHTTP {
		t.Fatalf("expected port shift, HTTP still on blocked port %s", httpAddr)
	}
	// 顺延后 HTTP 端口应为 blockedPort+1，SOCKS5 为 blockedPort+2（相邻偏移保持）
	wantHTTP := fmt.Sprintf("127.0.0.1:%d", blockedPort+1)
	wantSOCKS := fmt.Sprintf("127.0.0.1:%d", blockedPort+2)
	if httpAddr != wantHTTP {
		t.Errorf("HTTPAddr = %q, want %q", httpAddr, wantHTTP)
	}
	if socksAddr != wantSOCKS {
		t.Errorf("SOCKSAddr = %q, want %q", socksAddr, wantSOCKS)
	}

	if !g.Running() {
		t.Error("Running() = false after Start")
	}
}

func TestStartIdempotent(t *testing.T) {
	// 先找一对空闲端口（系统分配后立即释放，测试中基本无竞态）
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe listen: %v", err)
	}
	base := probe.Addr().(*net.TCPAddr).Port
	_ = probe.Close()

	httpAddr := fmt.Sprintf("127.0.0.1:%d", base)
	socksAddr := fmt.Sprintf("127.0.0.1:%d", base+1)
	g := NewGateway(nil, nil, nil, httpAddr, socksAddr)

	// 验证 Start 幂等：重复调用不会重复绑定
	if err := g.Start(); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	if err := g.Start(); err != nil {
		t.Fatalf("second Start should be no-op: %v", err)
	}
	defer g.Stop()

	if got := g.HTTPAddr(); got != httpAddr {
		t.Errorf("HTTPAddr = %q, want %q", got, httpAddr)
	}
	if got := g.SOCKSAddr(); got != socksAddr {
		t.Errorf("SOCKSAddr = %q, want %q", got, socksAddr)
	}
}

func TestSplitHostPort(t *testing.T) {
	host, port, err := splitHostPort("127.0.0.1:7892")
	if err != nil {
		t.Fatalf("splitHostPort: %v", err)
	}
	if host != "127.0.0.1" || port != 7892 {
		t.Errorf("got host=%q port=%d, want 127.0.0.1 7892", host, port)
	}
}
