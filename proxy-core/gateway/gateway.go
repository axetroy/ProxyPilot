package gateway

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/axetroy/ProxyPilot/proxy-core/bus"
	"github.com/axetroy/ProxyPilot/proxy-core/model"
	"github.com/axetroy/ProxyPilot/proxy-core/pool"
	"github.com/axetroy/ProxyPilot/proxy-core/scheduler"
	"github.com/axetroy/ProxyPilot/proxy-core/validator"
)

// maxPortProbe 端口被占用时向后顺延的最大尝试次数。
const maxPortProbe = 100

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
// 若配置的端口被占用，会向后顺延寻找一对空闲端口（HTTP 与 SOCKS5 端口保持相邻），
// 保证网关总能启动成功。实际绑定的端口可通过 HTTPAddr()/SOCKSAddr() 获取。
func (g *Gateway) Start() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.http != nil || g.socks != nil {
		return nil
	}

	// 解析配置端口的 host 与起始端口号，之后仅对端口号顺延
	host, startPort, err := splitHostPort(g.httpAddr)
	if err != nil {
		return fmt.Errorf("invalid http proxy address %q: %w", g.httpAddr, err)
	}

	// 从配置端口开始向后探测：HTTP 端口为 port，SOCKS5 端口为 port+1（保持相邻偏移）
	for port := startPort; port < startPort+maxPortProbe; port++ {
		httpAddr := net.JoinHostPort(host, strconv.Itoa(port))
		socksAddr := net.JoinHostPort(host, strconv.Itoa(port+1))

		h := &httpServer{addr: httpAddr, g: g}
		if err := h.Start(); err != nil {
			continue // HTTP 端口被占用，整体后移
		}
		s := &socksServer{addr: socksAddr, g: g}
		if err := s.Start(); err != nil {
			h.Stop() // SOCKS5 端口被占用，回滚后整体后移
			continue
		}

		g.http = h
		g.socks = s
		g.httpAddr = httpAddr
		g.socksAddr = socksAddr
		g.startedAt = time.Now()
		if g.bus != nil {
			g.bus.Info(fmt.Sprintf("HTTP proxy listening on %s", g.httpAddr))
			g.bus.Info(fmt.Sprintf("SOCKS5 proxy listening on %s", g.socksAddr))
			if port != startPort {
				g.bus.Warn(fmt.Sprintf("configured port %d occupied, proxy ports shifted to %s / %s", startPort, g.httpAddr, g.socksAddr))
			}
		}
		return nil
	}
	return fmt.Errorf("no free port pair found starting from %s (tried %d ports)", g.httpAddr, maxPortProbe)
}

// HTTPAddr 返回当前实际绑定的 HTTP 代理地址。
// 若配置端口被占用自动顺延，这里即为顺延后的实际端口。
func (g *Gateway) HTTPAddr() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.httpAddr
}

// SOCKSAddr 返回当前实际绑定的 SOCKS5 代理地址。
// 若配置端口被占用自动顺延，这里即为顺延后的实际端口。
func (g *Gateway) SOCKSAddr() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.socksAddr
}

// SetAddrs 更新配置端口（下一次 Start 时生效）。
// 网关正在运行时，先 Stop 再 Start 即可切换端口。
func (g *Gateway) SetAddrs(httpAddr, socksAddr string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.httpAddr = httpAddr
	g.socksAddr = socksAddr
}

// splitHostPort 将 host:port 拆分为 host 与端口号。
func splitHostPort(addr string) (host string, port int, err error) {
	h, p, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0, err
	}
	n, err := strconv.Atoi(p)
	if err != nil {
		return "", 0, fmt.Errorf("invalid port %q: %w", p, err)
	}
	return h, n, nil
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
	if g.bus != nil {
		g.bus.Info("proxy gateway stopped")
	}
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
