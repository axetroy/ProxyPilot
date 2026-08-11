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
	base := fmt.Sprintf("127.0.0.1:%d", blockedPort)

	g := NewGateway(nil, nil, nil, base)
	if err := g.Start(); err != nil {
		t.Fatalf("Start with blocked port: %v", err)
	}
	defer g.Stop()

	if g.HTTPAddr() == base {
		t.Fatalf("expected port shift, still on blocked port %s", g.HTTPAddr())
	}
	// HTTP 与 SOCKS5 共用同一端口
	if g.HTTPAddr() != g.SOCKSAddr() {
		t.Errorf("HTTPAddr = %q, SOCKSAddr = %q, want same shared port", g.HTTPAddr(), g.SOCKSAddr())
	}
	if !g.Running() {
		t.Error("Running() = false after Start")
	}
}

func TestStartIdempotent(t *testing.T) {
	// 先找一个空闲端口（系统分配后立即释放，测试中基本无竞态）
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe listen: %v", err)
	}
	base := probe.Addr().(*net.TCPAddr).Port
	_ = probe.Close()

	addr := fmt.Sprintf("127.0.0.1:%d", base)
	g := NewGateway(nil, nil, nil, addr)

	// 验证 Start 幂等：重复调用不会重复绑定
	if err := g.Start(); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	if err := g.Start(); err != nil {
		t.Fatalf("second Start should be no-op: %v", err)
	}
	defer g.Stop()

	if got := g.HTTPAddr(); got != addr {
		t.Errorf("HTTPAddr = %q, want %q", got, addr)
	}
	if got := g.SOCKSAddr(); got != addr {
		t.Errorf("SOCKSAddr = %q, want %q", got, addr)
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
