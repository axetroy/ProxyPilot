package gateway

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/axetroy/ProxyPilot/proxy-core/bus"
	"github.com/axetroy/ProxyPilot/proxy-core/model"
	"github.com/axetroy/ProxyPilot/proxy-core/pool"
	"github.com/axetroy/ProxyPilot/proxy-core/scheduler"
	"github.com/axetroy/ProxyPilot/proxy-core/validator"
)

// Gateway exposes local proxy ports and routes traffic through the node pool.
type Gateway struct {
	pool     *pool.Manager
	selector *scheduler.Selector
	bus      *bus.Bus

	httpAddr  string
	socksAddr string

	mu                sync.Mutex
	http              *httpServer
	socks             *socksServer
	startedAt         time.Time
	limitCtx          int
	currentNode       *model.ProxyNode
	currentHTTPNode   *model.ProxyNode
	currentSOCKS5Node *model.ProxyNode
}

func NewGateway(pool *pool.Manager, selector *scheduler.Selector, bus *bus.Bus, httpAddr, socks5Addr string) *Gateway {
	return &Gateway{
		pool:      pool,
		selector:  selector,
		bus:       bus,
		httpAddr:  httpAddr,
		socksAddr: socks5Addr,
		limitCtx:  4,
	}
}

func (g *Gateway) Running() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.http != nil || g.socks != nil
}

func (g *Gateway) CurrentNode() *model.ProxyNode {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.currentNode == nil {
		return nil
	}
	copyNode := *g.currentNode
	return &copyNode
}

func (g *Gateway) CurrentHTTPNode() *model.ProxyNode {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.currentHTTPNode == nil {
		return nil
	}
	copyNode := *g.currentHTTPNode
	return &copyNode
}

func (g *Gateway) CurrentSOCKS5Node() *model.ProxyNode {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.currentSOCKS5Node == nil {
		return nil
	}
	copyNode := *g.currentSOCKS5Node
	return &copyNode
}

// Start binds HTTP and SOCKS5 proxy listeners.
func (g *Gateway) Start() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.http == nil {
		h := &httpServer{addr: g.httpAddr, g: g}
		if err := h.Start(); err != nil {
			return fmt.Errorf("http proxy startup: %w", err)
		}
		g.http = h
		g.bus.Info(fmt.Sprintf("HTTP proxy listening on %s", g.httpAddr))
	}
	if g.socks == nil {
		s := &socksServer{addr: g.socksAddr, g: g}
		if err := s.Start(); err != nil {
			if g.http != nil {
				g.http.Stop()
				g.http = nil
			}
			return fmt.Errorf("socks5 startup: %w", err)
		}
		g.socks = s
		g.bus.Info(fmt.Sprintf("SOCKS5 proxy listening on %s", g.socksAddr))
	}
	g.startedAt = time.Now()
	return nil
}

// Stop releases all proxy listeners.
func (g *Gateway) Stop() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.http != nil {
		g.http.Stop()
		g.http = nil
	}
	if g.socks != nil {
		g.socks.Stop()
		g.socks = nil
	}
	g.currentNode = nil
	g.currentHTTPNode = nil
	g.currentSOCKS5Node = nil
	g.bus.Info("proxy gateway stopped")
}

// Upstream dials `target` through the best live node, retrying alternatives
// and penalizing failed nodes so the next attempt prefers a healthy one.
func (g *Gateway) Upstream(ctx context.Context, target string) (net.Conn, error) {
	return g.UpstreamWithProtocol(ctx, target, "")
}

// UpstreamWithDial dials `t` through the best live node for the given
// protocol. SOCKS5 traffic prefers SOCKS5 upstreams to keep routing semantics
// aligned with the local SOCKS5 listener.
// 目标域名会传给选择器做\"域名粘性\"：同一域名在窗口内复用同一出口 IP，
// 避免短时间内多个 IP 访问同一网站触发目标站点的安全防控。
func (g *Gateway) UpstreamWithProtocol(ctx context.Context, target string, protocol model.ProxyProtocol) (net.Conn, error) {
	if g.bus != nil {
		g.bus.Debug(fmt.Sprintf("proxy upstream selection protocol=%s target=%s", protocol, target))
	}

	// 从 target（host:port）中提取纯域名，用于域名粘性选择。
	host := targetHost(target)

	var lastErr error
	for attempt := 0; attempt < g.limitCtx; attempt++ {
		node := g.selector.NextForHost(protocol, host)
		if node == nil {
			lastErr = fmt.Errorf("no usable proxy in pool")
			break
		}
		g.mu.Lock()
		g.currentNode = node
		switch protocol {
		case model.ProtocolSOCKS5:
			g.currentSOCKS5Node = node
		default:
			g.currentHTTPNode = node
		}
		g.mu.Unlock()

		if g.bus != nil {
			g.bus.Debug(fmt.Sprintf("proxy upstream selected node=%s protocol=%s target=%s", node.Key(), node.Protocol, target))
		}

		conn, err := validator.ConnectTCP(node, target, 10*time.Second)
		if err != nil {
			g.bus.Debug(fmt.Sprintf("upstream dial via %s failed: %v", node.Key(), err))
			g.selector.FailOn(node.ID)
			lastErr = err
			continue
		}
		g.selector.Success(node.ID)
		return conn, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no usable proxy in pool")
	}
	return nil, fmt.Errorf("all upstream attempts failed for %s: %w", target, lastErr)
}

// targetHost 从 target（形如 "example.com:443" 或 "1.2.3.4:8080"）中提取纯域名/IP。
// 如果 target 不带端口，则原样返回。提取出的 host 用于域名粘性选择。
func targetHost(target string) string {
	if host, _, err := net.SplitHostPort(target); err == nil {
		return host
	}
	return target
}
