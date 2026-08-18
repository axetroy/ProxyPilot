package gateway

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
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

// Gateway exposes the local proxy port and routes traffic through the node pool.
// HTTP 与 SOCKS5 始终共用同一端口（按连接首字节自动识别协议）。
type Gateway struct {
	pool     *pool.Manager
	selector *scheduler.Selector
	bus      *bus.Bus

	addr string

	// forwardTransport 是普通 HTTP 转发（非 CONNECT）复用的 http.Transport。
	// 每个转发请求都新建 Transport 会反复分配连接池结构，且立即 CloseIdleConnections
	// 丢弃全部状态；共享一个实例则只需创建一次。
	// DisableKeepAlives 保持开启：上游经由节点池按请求选路，复用连接会钉住单一出口节点。
	forwardTransport *http.Transport

	// shunt 是智能分流直连判断函数：返回 true 表示目标应直连（不经节点池）。
	// 由外部注入（rule.Manager.Shunt()），nil 表示未启用分流（全部走代理）。
	shunt func(host string) bool

	mu                sync.Mutex
	mixed             *mixedServer
	startedAt         time.Time
	limitCtx          int
	currentNode       *model.ProxyNode
	currentHTTPNode   *model.ProxyNode
	currentSOCKS5Node *model.ProxyNode

	// ipv6Cache 缓存 IPv6 字面量地址的 PTR 反查结果（空串表示反查失败），
	// 避免每次 SOCKS5 请求都阻塞在 DNS 反查上。
	ipv6Mu    sync.Mutex
	ipv6Cache map[string]ipv6CacheEntry
}

// ipv6CacheEntry 记录某个 IPv6 地址的 PTR 反查结果与缓存时间。
type ipv6CacheEntry struct {
	domain string
	at     time.Time
}

// ipv6CacheTTL 是 PTR 反查结果的缓存有效期。
const ipv6CacheTTL = 10 * time.Minute

func NewGateway(pool *pool.Manager, selector *scheduler.Selector, bus *bus.Bus, addr string) *Gateway {
	g := &Gateway{
		pool:      pool,
		selector:  selector,
		bus:       bus,
		addr:      addr,
		limitCtx:  4,
		ipv6Cache: make(map[string]ipv6CacheEntry),
	}
	g.forwardTransport = &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			// addr 是上游目标 host:port，经由节点池选路连接。
			// 显式指定 HTTP 协议，确保只从 HTTP/HTTPS 节点中挑选上游。
			return g.UpstreamWithProtocol(ctx, addr, model.ProtocolHTTP)
		},
		DisableKeepAlives: true,
	}
	return g
}

func (g *Gateway) Running() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.mixed != nil
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

// Start 在单一端口上同时提供 HTTP 与 SOCKS5 代理（混合模式）。
// 端口被占用时向后顺延，保证网关总能启动成功。
// 实际绑定的端口可通过 HTTPAddr()/SOCKSAddr() 获取（两者相同）。
func (g *Gateway) Start() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.mixed != nil {
		return nil
	}

	// 解析配置端口的 host 与起始端口号，之后仅对端口号顺延
	host, startPort, err := splitHostPort(g.addr)
	if err != nil {
		return fmt.Errorf("invalid proxy address %q: %w", g.addr, err)
	}

	for port := startPort; port < startPort+maxPortProbe; port++ {
		addr := net.JoinHostPort(host, strconv.Itoa(port))
		m := &mixedServer{addr: addr, g: g}
		if err := m.Start(); err != nil {
			continue // 端口被占用，顺延
		}

		g.mixed = m
		// 使用实际绑定的地址（配置为端口 0 时由系统分配）
		bound := m.ln.Addr().String()
		g.addr = bound
		g.startedAt = time.Now()
		if g.bus != nil {
			g.bus.Info(fmt.Sprintf("HTTP+SOCKS5 proxy listening on %s", bound))
			if port != startPort {
				g.bus.Warn(fmt.Sprintf("configured port %d occupied, mixed proxy port shifted to %s", startPort, bound))
			}
		}
		return nil
	}
	return fmt.Errorf("no free port found starting from %s (tried %d ports)", g.addr, maxPortProbe)
}

// HTTPAddr 返回当前实际绑定的代理地址（HTTP 与 SOCKS5 共用）。
// 若配置端口被占用自动顺延，这里即为顺延后的实际端口。
func (g *Gateway) HTTPAddr() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.addr
}

// SOCKSAddr 返回当前实际绑定的代理地址（与 HTTPAddr 相同，共用同一端口）。
func (g *Gateway) SOCKSAddr() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.addr
}

// SetAddr 更新配置端口（下一次 Start 时生效）。
// 网关正在运行时，先 Stop 再 Start 即可切换端口。
func (g *Gateway) SetAddr(addr string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.addr = addr
}

// SetShunt 注入智能分流直连判断函数（rule.Manager.Shunt()）。
// nil 表示关闭分流（全部走代理）；分流内部已包含开关判断。
func (g *Gateway) SetShunt(shunt func(host string) bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.shunt = shunt
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
	if g.mixed != nil {
		g.mixed.Stop()
		g.mixed = nil
	}
	g.currentNode = nil
	g.currentHTTPNode = nil
	g.currentSOCKS5Node = nil
	if g.bus != nil {
		g.bus.Info("proxy gateway stopped")
	}
}

// dialDirect 智能分流命中「直连」时直接建立 TCP 连接（不经节点池）。
func (g *Gateway) dialDirect(ctx context.Context, target string) (net.Conn, error) {
	if g.bus != nil {
		g.bus.Debug(fmt.Sprintf("shunt direct target=%s", target))
	}
	dialer := net.Dialer{Timeout: 10 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", target)
	if err != nil {
		return nil, fmt.Errorf("direct dial failed for %s: %w", target, err)
	}
	return conn, nil
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

	// 智能分流：命中直连判断的目标直接建立连接，不经节点池。
	// 直连语义下由本机 DNS 解析（命中直连的均为大陆/内网/未墙域名，DNS 干净）；
	// 走代理的域名原样交给上游节点解析，避开本地 DNS 污染。
	if g.shunt != nil && g.shunt(host) {
		return g.dialDirect(ctx, target)
	}

	// 如果目标是 IPv6 字面量，尝试 PTR 反查域名，用域名连接。
	// 部分上游节点只支持 IPv4 目标地址，客户端本地解析出 IPv6 后
	// 直接连接会失败；反查域名后让节点重新解析（通常得到 IPv4）。
	// 反查失败时保持原样，按 IPv6 目标直连。结果带缓存避免重复阻塞。
	if ip := net.ParseIP(host); ip != nil && ip.To4() == nil {
		if domain := g.ipv6Domain(ip.String()); domain != "" {
			if _, port, err := net.SplitHostPort(target); err == nil {
				host = domain
				target = net.JoinHostPort(domain, port)
				if g.bus != nil {
					g.bus.Debug(fmt.Sprintf("ipv6 target %s resolved to domain %s via PTR", ip, domain))
				}
			}
		}
	}

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
			if g.bus != nil {
				g.bus.Debug(fmt.Sprintf("upstream dial via %s failed: %v", node.Key(), err))
			}
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

// NewUDPRelay 为本地 SOCKS5 UDP 中继选择 SOCKS5 上游节点并建立 UDP 通道。
// 只有池中存在存活的 SOCKS5 节点才能成功：HTTP/HTTPS 节点只支持 CONNECT 隧道，
// 无法承载 UDP 流量，因此不做跨协议回退。
func (g *Gateway) NewUDPRelay() (udpBackend, error) {
	node := g.selector.NextStrict(model.ProtocolSOCKS5)
	if node == nil {
		return nil, errNoSOCKS5UDP
	}
	sess, err := validator.UDPAssociate(node, 10*time.Second)
	if err != nil {
		g.selector.FailOn(node.ID)
		return nil, err
	}
	g.selector.Success(node.ID)

	g.mu.Lock()
	g.currentNode = node
	g.currentSOCKS5Node = node
	g.mu.Unlock()
	if g.bus != nil {
		g.bus.Debug(fmt.Sprintf("udp relay established via node=%s", node.Key()))
	}
	return &upstreamSOCKS5UDP{sess: sess}, nil
}

// targetHost 从 target（形如 "example.com:443" 或 "1.2.3.4:8080"）中提取纯域名/IP。
// 如果 target 不带端口，则原样返回。提取出的 host 用于域名粘性选择。
func targetHost(target string) string {
	if host, _, err := net.SplitHostPort(target); err == nil {
		return host
	}
	return target
}

// ipv6Domain 对 IPv6 字面量地址做 PTR 反查，返回对应的域名（去掉末尾点）。
// 结果缓存 ipv6CacheTTL 时长，反查失败缓存空串，避免重复阻塞在 DNS 上。
func (g *Gateway) ipv6Domain(ip string) string {
	g.ipv6Mu.Lock()
	if e, ok := g.ipv6Cache[ip]; ok && time.Since(e.at) < ipv6CacheTTL {
		g.ipv6Mu.Unlock()
		return e.domain
	}
	g.ipv6Mu.Unlock()

	lookupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	names, err := net.DefaultResolver.LookupAddr(lookupCtx, ip)
	cancel()

	domain := ""
	if err == nil && len(names) > 0 {
		domain = strings.TrimSuffix(names[0], ".")
	}

	g.ipv6Mu.Lock()
	g.ipv6Cache[ip] = ipv6CacheEntry{domain: domain, at: time.Now()}
	g.ipv6Mu.Unlock()
	return domain
}
