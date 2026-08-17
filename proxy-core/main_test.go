package main

import (
	"net"
	"strconv"
	"testing"
)

// 正常场景：端口空闲时使用原端口。
func TestResolveAPIBindFreePort(t *testing.T) {
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe listen: %v", err)
	}
	host, portStr, err := net.SplitHostPort(probe.Addr().String())
	if err != nil {
		t.Fatalf("split addr: %v", err)
	}
	// 关闭探测监听，释放该端口供 resolveAPIBind 使用
	_ = probe.Close()

	bind := net.JoinHostPort(host, portStr)
	resolved, ln, err := resolveAPIBind(bind)
	if err != nil {
		t.Fatalf("resolveAPIBind(%s): %v", bind, err)
	}
	defer func() { _ = ln.Close() }()

	if resolved != bind {
		t.Fatalf("expected original bind %s, got %s", bind, resolved)
	}
}

// 端口被占用时向后顺延，绑定到下一个可用端口。
func TestResolveAPIBindShiftWhenOccupied(t *testing.T) {
	// 占用一个随机端口，模拟端口冲突
	blocker, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("blocker listen: %v", err)
	}
	defer func() { _ = blocker.Close() }()

	host, portStr, err := net.SplitHostPort(blocker.Addr().String())
	if err != nil {
		t.Fatalf("split blocker addr: %v", err)
	}
	occupierPort, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse blocker port: %v", err)
	}

	bind := net.JoinHostPort(host, strconv.Itoa(occupierPort))
	resolved, ln, err := resolveAPIBind(bind)
	if err != nil {
		t.Fatalf("resolveAPIBind(%s): %v", bind, err)
	}
	defer func() { _ = ln.Close() }()

	_, resolvedPortStr, err := net.SplitHostPort(resolved)
	if err != nil {
		t.Fatalf("split resolved addr: %v", err)
	}
	resolvedPort, err := strconv.Atoi(resolvedPortStr)
	if err != nil {
		t.Fatalf("parse resolved port: %v", err)
	}
	if resolvedPort <= occupierPort {
		t.Fatalf("expected shifted port > %d, got %d", occupierPort, resolvedPort)
	}
}

// 非法 bind 地址应返回错误。
func TestResolveAPIBindInvalidBind(t *testing.T) {
	if _, ln, err := resolveAPIBind("invalid"); err == nil || ln != nil {
		t.Fatalf("expected error for invalid bind, got ln=%v err=%v", ln, err)
	}
}