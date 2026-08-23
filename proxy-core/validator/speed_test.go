package validator

import (
	"testing"
	"time"

	"github.com/axetroy/ProxyPilot/proxy-core/model"
)

// 代理不可达时 SpeedTest 应返回错误，且不 panic（无需真实网络）。
func TestSpeedTestUnreachableProxy(t *testing.T) {
	n := &model.ProxyNode{
		Host:     "127.0.0.1",
		Port:     1, // 无监听，连接必失败
		Protocol: model.ProtocolHTTP,
	}
	_, err := SpeedTest(n, "https://speed.cloudflare.com/__down?bytes=1048576", 5*time.Second, 1<<20)
	if err == nil {
		t.Fatal("expected error for unreachable proxy, got nil")
	}
}
