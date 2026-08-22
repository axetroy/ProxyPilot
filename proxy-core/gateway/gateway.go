package gateway

import (
	"context"
	"fmt"
	"math/rand"
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

// chainCacheTTL 是链列表缓存的有效期：链增删/启停后最多延迟该时长生效，
// 避免每次请求都查询数据库。
const chainCacheTTL = 30 * time.Second

// socksHandshakeTimeout 是 SOCKS5 握手的读超时：客户端建立连接后若迟迟
// 不发握手数据（慢速攻击），超时后直接关闭连接，避免占用 goroutine 与 fd。
const socksHandshakeTimeout = 10 * time.Second

// relayIdleTimeout 是双向隧道（CONNECT / SOCKS5）的空闲超时：
// 两个方向都长时间没有数据时关闭连接，避免半开连接（对端既不读写也不关闭）
// 永久悬挂。注意 relay 中两个 io.Copy 各自阻塞在一个方向，
// 仅靠对端关闭无法探测半开连接，必须主动设置 deadline。
const relayIdleTimeout = 2 * time.Minute

// maxActiveConns 是网关同时处理的最大客户端连接数（CONNECT 隧道 / SOCKS5 会话）。
// 超过上限的新连接直接关闭，防止异常或恶意客户端无界开连接耗尽 goroutine 与文件句柄。
const maxActiveConns = 512

// maxUDPAssociates 是网关同时允许的最大 SOCKS5 UDP ASSOCIATE 会话数。
// 每个会话占用 1 个本地 UDP 端口 + 多个 goroutine，无上限会被滥用耗尽资源。
const maxUDPAssociates = 256

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

	// chainsProvider 返回全部代理链路（供 chain 策略选择）。由外部注入。
	// nil 表示未注入（chain 策略无链可用）。
	chainsProvider func() ([]model.ProxyChain, error)

	// autoChainConfig 返回自动链路（auto-chain）策略的层数与每层选择策略。由外部注入。
	// 返回 hops=0 时视为未配置，自动链路策略不可用。
	autoChainConfig func() (hops int, selection scheduler.Strategy)

	// chainCache 是链列表的 TTL 缓存：链列表来自 SQLite，每次请求都查询会浪费 DB 访问。
	// 链的增删/启停通过 API 变更，最多延迟 chainCacheTTL 生效。
	chainMu       sync.Mutex
	chainCache    []model.ProxyChain
	chainCachedAt time.Time

	mu                sync.Mutex
	mixed             *mixedServer
	startedAt         time.Time
	limitCtx          int
	currentNode       *model.ProxyNode

	// ipv6Cache 缓存 IPv6 字面量地址的 PTR 反查结果（空串表示反查失败），
	// 避免每次 SOCKS5 请求都阻塞在 DNS 反查上。
	ipv6Mu    sync.Mutex
	ipv6Cache map[string]ipv6CacheEntry

	// traffic 统计网关转发的流量与连接数（本次启动累计）。
	traffic *trafficCounter

	// activeConns 追踪所有仍在处理的客户端连接（CONNECT 隧道 / SOCKS5 会话）。
	// Stop 时强制关闭，避免长连接（隧道/会话）在网关停止后悬挂。
	// 同时作为并发上限：超过 maxActiveConns 的新连接直接拒绝，
	// 防止异常/恶意客户端无界开连接耗尽 goroutine 与句柄。
	activeMu       sync.Mutex
	activeConns    map[net.Conn]struct{}
	maxActiveConns int

	// udpAssociates 限制并发 SOCKS5 UDP ASSOCIATE 会话数。
	// 每个会话占用 1 个本地 UDP 端口 + 多个 goroutine，无上限会被滥用耗尽资源。
	udpMu          sync.Mutex
	udpActive      int
	maxUDPAssociates int

	// trafficPruneAt 上次执行残留统计清理的时间：Traffic 快照限频 prune，
	// 避免高频轮询时每次都全量扫描节点池/链路表。
	trafficPruneMu sync.Mutex
	trafficPruneAt time.Time
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
		pool:              pool,
		selector:          selector,
		bus:               bus,
		addr:              addr,
		limitCtx:          4,
		ipv6Cache:         make(map[string]ipv6CacheEntry),
		traffic:           newTrafficCounter(),
		activeConns:       make(map[net.Conn]struct{}),
		maxActiveConns:    maxActiveConns,
		maxUDPAssociates:  maxUDPAssociates,
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

// SetChainsProvider 注入代理链路列表获取函数（chain 策略选择链路时使用）。
func (g *Gateway) SetChainsProvider(p func() ([]model.ProxyChain, error)) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.chainsProvider = p
}

// SetAutoChainConfig 注入自动链路（auto-chain）策略的层数与每层选择策略读取函数。
// 层数/选择策略可在运行时通过配置修改，每次建立自动链路时都会重新读取。
func (g *Gateway) SetAutoChainConfig(f func() (int, scheduler.Strategy)) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.autoChainConfig = f
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
	// 强制关闭所有仍在处理的客户端连接（CONNECT 隧道 / SOCKS5 会话），
	// 避免网关停止后这些长连接继续悬挂。
	g.closeActiveConns()
	if g.bus != nil {
		g.bus.Info("proxy gateway stopped")
	}
}

// trackConn 登记一条正在处理的客户端连接；超出并发上限时返回 false 并关闭该连接。
// 调用方应在登记失败时立即终止本次连接处理（不再继续握手/建上游）。
// 未绑定网关（如独立构造的 socksServer）时不限制并发，返回 true。
func (g *Gateway) trackConn(conn net.Conn) bool {
	if g == nil {
		return true
	}
	g.activeMu.Lock()
	if len(g.activeConns) >= g.maxActiveConns {
		g.activeMu.Unlock()
		_ = conn.Close()
		if g.bus != nil {
			g.bus.Debug(fmt.Sprintf("connection rejected: active connections exceed %d", g.maxActiveConns))
		}
		return false
	}
	g.activeConns[conn] = struct{}{}
	g.activeMu.Unlock()
	return true
}

// untrackConn 注销一条已结束的客户端连接。
func (g *Gateway) untrackConn(conn net.Conn) {
	if g == nil {
		return
	}
	g.activeMu.Lock()
	delete(g.activeConns, conn)
	g.activeMu.Unlock()
}

// closeActiveConns 关闭所有已登记的活动连接并清空集合。
func (g *Gateway) closeActiveConns() {
	g.activeMu.Lock()
	for conn := range g.activeConns {
		_ = conn.Close()
	}
	g.activeConns = make(map[net.Conn]struct{})
	g.activeMu.Unlock()
}

// trackUDP 登记一个 UDP ASSOCIATE 会话；超出并发上限时返回 false。
// UDP 会话不关联 net.Conn（使用 UDPConn），仅计数限流。
func (g *Gateway) trackUDP() bool {
	if g == nil {
		return true
	}
	g.udpMu.Lock()
	if g.udpActive >= g.maxUDPAssociates {
		g.udpMu.Unlock()
		if g.bus != nil {
			g.bus.Debug(fmt.Sprintf("UDP associate rejected: active sessions exceed %d", g.maxUDPAssociates))
		}
		return false
	}
	g.udpActive++
	g.udpMu.Unlock()
	return true
}

// untrackUDP 注销一个 UDP ASSOCIATE 会话。
func (g *Gateway) untrackUDP() {
	if g == nil {
		return
	}
	g.udpMu.Lock()
	if g.udpActive > 0 {
		g.udpActive--
	}
	g.udpMu.Unlock()
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
	return g.TrackConn(conn, trafficDirect, 0, ""), nil
}

// Upstream dials `target` through the best live node, retrying alternatives
// and penalizing failed nodes so the next attempt prefers a healthy one.
func (g *Gateway) Upstream(ctx context.Context, target string) (net.Conn, error) {
	return g.UpstreamWithProtocol(ctx, target, "")
}

// UpstreamWithDial dials `t` through the best live node for the given
// protocol. 选路不区分协议：ConnectTCP 会按节点自身协议完成握手，HTTP 与 SOCKS5
// 流量统一按当前策略从存活节点中选择（protocol 仅作兼容参数保留，不影响选路）。
// 目标域名会传给选择器做\"域名粘性\"：同一域名在窗口内复用同一出口 IP，
// 避免短时间内多个 IP 访问同一网站触发目标站点的安全防控。
func (g *Gateway) UpstreamWithProtocol(ctx context.Context, target string, protocol model.ProxyProtocol) (net.Conn, error) {
	if g.bus != nil {
		g.bus.Debug(fmt.Sprintf("proxy upstream selection protocol=%s target=%s", protocol, target))
	}

	// 从 target（host:port）中提取纯域名，用于域名粘性选择。
	host := targetHost(target)

	// 智能分流：命中直连判断的目标直接建立连接，不经节点池。
	// 直连语义下由本机 DNS 解析（命中直连的均为大陆/内网/可直连域名，DNS 干净）；
	// 走代理的域名原样交给上游节点解析，避开本地 DNS 解析异常。
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
		// 链路类策略：不选单跳节点，按链路建立隧道。
		// chain 从已启用的手动链路挑选；auto-chain 按配置自动挑选 N 个节点。
		strategy := g.selector.Strategy()
		if strategy == scheduler.StrategyChain || strategy == scheduler.StrategyAutoChain {
			var conn net.Conn
			var err error
			if strategy == scheduler.StrategyChain {
				conn, err = g.upstreamViaChain(ctx, target)
			} else {
				conn, err = g.upstreamViaAutoChain(ctx, target)
			}
			if err != nil {
				lastErr = err
				break // 链路建立失败不重试单跳，直接返回错误
			}
			return conn, nil
		}

		node := g.selector.NextForHost(protocol, host)
		if node == nil {
			lastErr = fmt.Errorf("no usable proxy in pool")
			break
		}
		g.mu.Lock()
		g.currentNode = node
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
		return g.TrackConn(conn, trafficNode, node.ID, ""), nil
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
	// 链路不支持承载 UDP：chain 策略下 UDP 流量回退到单跳 SOCKS5 节点。
	if g.selector.Strategy() == scheduler.StrategyChain && g.bus != nil {
		g.bus.Debug("chain strategy active: UDP traffic falls back to single-hop SOCKS5")
	}
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

// upstreamViaChain 从已启用的链路中随机挑一条「全部节点存活」的链，
// 逐跳连接建立直达 target 的隧道。某条链不可用或连接失败时尝试下一条。
func (g *Gateway) upstreamViaChain(ctx context.Context, target string) (net.Conn, error) {
	g.mu.Lock()
	provider := g.chainsProvider
	g.mu.Unlock()
	if provider == nil {
		return nil, fmt.Errorf("chain strategy enabled but no chain provider configured")
	}
	chains, err := g.cachedChains(provider)
	if err != nil {
		return nil, fmt.Errorf("load proxy chains: %w", err)
	}
	enabled := make([]model.ProxyChain, 0, len(chains))
	for _, c := range chains {
		if c.Enabled {
			enabled = append(enabled, c)
		}
	}
	if len(enabled) == 0 {
		return nil, fmt.Errorf("chain strategy enabled but no enabled chain")
	}

	// 随机打乱启用链的顺序，让多条链均衡承担流量。
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	r.Shuffle(len(enabled), func(i, j int) { enabled[i], enabled[j] = enabled[j], enabled[i] })

	var lastErr error
	for _, chain := range enabled {
		// 客户端已断开或请求上下文取消时快速失败，不再尝试下一条链。
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// 链上任一节点缺失或非存活则该链不可用，换下一条。
		nodes := make([]*model.ProxyNode, 0, len(chain.NodeIDs))
		usable := true
		for _, id := range chain.NodeIDs {
			n := g.pool.Get(id)
			if n == nil || n.Status != model.StatusAlive {
				usable = false
				break
			}
			nodes = append(nodes, n)
		}
		if !usable || len(nodes) == 0 {
			continue
		}

		conn, err := validator.ConnectChain(nodes, target, 10*time.Second)
		if err != nil {
			if g.bus != nil {
				g.bus.Debug(fmt.Sprintf("chain %q unavailable: %v", chain.Name, err))
			}
			lastErr = err
			continue
		}
		g.mu.Lock()
		g.currentNode = nodes[len(nodes)-1]
		g.mu.Unlock()
		if g.bus != nil {
			names := make([]string, 0, len(nodes))
			for _, n := range nodes {
				names = append(names, n.Key())
			}
			g.bus.Debug(fmt.Sprintf("chain %q established via [%s] -> %s", chain.Name, strings.Join(names, " -> "), target))
		}
		return g.TrackConn(conn, trafficChain, 0, chain.Name), nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no usable chain (all chains have dead nodes)")
	}
	return nil, fmt.Errorf("all chains failed for %s: %w", target, lastErr)
}

// upstreamViaAutoChain 按自动链路配置（层数 + 每层选择策略）从存活节点中
// 自动挑选 N 个互不相同的节点，逐跳建立直达 target 的隧道。
func (g *Gateway) upstreamViaAutoChain(ctx context.Context, target string) (net.Conn, error) {
	g.mu.Lock()
	configFn := g.autoChainConfig
	g.mu.Unlock()
	if configFn == nil {
		return nil, fmt.Errorf("auto-chain strategy enabled but no config provider configured")
	}
	hops, selection := configFn()
	if hops <= 0 {
		return nil, fmt.Errorf("auto-chain strategy enabled but hops not configured")
	}

	// 每次请求现选节点，节点池变化即时生效（无需缓存）；
	// 链路建立失败时由调用方整体报错，下一次请求会重新挑选。
	nodes := g.selector.SelectChain(hops, selection)
	if len(nodes) == 0 {
		return nil, fmt.Errorf("auto-chain strategy enabled but no live node in pool")
	}

	conn, err := validator.ConnectChain(nodes, target, 10*time.Second)
	if err != nil {
		return nil, err
	}
	g.mu.Lock()
	g.currentNode = nodes[len(nodes)-1]
	g.mu.Unlock()
	if g.bus != nil {
		names := make([]string, 0, len(nodes))
		for _, n := range nodes {
			names = append(names, n.Key())
		}
		g.bus.Debug(fmt.Sprintf("auto-chain established via [%s] -> %s", strings.Join(names, " -> "), target))
	}
	return g.TrackConn(conn, trafficChain, 0, autoChainTrafficName), nil
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

// cachedChains 返回链列表：未过期时命中缓存，过期后重新从 provider 加载。
func (g *Gateway) cachedChains(provider func() ([]model.ProxyChain, error)) ([]model.ProxyChain, error) {
	g.chainMu.Lock()
	defer g.chainMu.Unlock()
	if g.chainCache != nil && time.Since(g.chainCachedAt) < chainCacheTTL {
		return g.chainCache, nil
	}
	chains, err := provider()
	if err != nil {
		return nil, err
	}
	g.chainCache = chains
	g.chainCachedAt = time.Now()
	return chains, nil
}
