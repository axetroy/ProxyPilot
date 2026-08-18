package scheduler

import (
	"math"
	"math/rand"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/axetroy/ProxyPilot/proxy-core/model"
	"github.com/axetroy/ProxyPilot/proxy-core/pool"
)

// Strategy 是网关出口节点的集中选择策略。
// 策略定义在「出口路由」设置中，可随时切换，切换即时生效并持久化。
type Strategy string

const (
	// StrategyFixed 固定出口：使用用户指定的节点，节点存活时全部流量走它。
	StrategyFixed Strategy = "fixed"
	// StrategyBest 最高评分：每次从存活节点中选评分最高者。
	StrategyBest Strategy = "best"
	// StrategyRandom 随机可用：从存活节点中随机挑选（同一域名窗口内保持稳定）。
	StrategyRandom Strategy = "random"
	// StrategyWeighted 智能加权（默认）：评分/延迟加权 + 失败惩罚 + 域名粘性。
	StrategyWeighted Strategy = "weighted"
	// StrategyRoundRobin 轮询：存活节点按顺序轮流使用，均衡负载。
	StrategyRoundRobin Strategy = "round-robin"
)

// ValidStrategy 判断字符串是否是合法的出口策略。
func ValidStrategy(s string) bool {
	switch Strategy(s) {
	case StrategyFixed, StrategyBest, StrategyRandom, StrategyWeighted, StrategyRoundRobin:
		return true
	}
	return false
}

// StrategyMeta 描述一个出口策略（供前端展示与选择）。
type StrategyMeta struct {
	Value string `json:"value"`
	Label string `json:"label"`
	Desc  string `json:"desc"`
}

// Strategies 返回全部支持的出口策略列表（顺序即展示顺序）。
func Strategies() []StrategyMeta {
	return []StrategyMeta{
		{Value: string(StrategyFixed), Label: "固定出口", Desc: "使用指定的节点，节点存活时全部流量走它"},
		{Value: string(StrategyBest), Label: "最高评分", Desc: "从存活节点中选择评分最高者"},
		{Value: string(StrategyRandom), Label: "随机可用", Desc: "从存活节点中随机挑选，同一网站短时间窗口内保持稳定"},
		{Value: string(StrategyWeighted), Label: "智能加权", Desc: "评分/延迟加权，叠加失败惩罚与域名粘性（默认，推荐）"},
		{Value: string(StrategyRoundRobin), Label: "轮询", Desc: "存活节点按顺序轮流使用，均衡负载"},
	}
}

// failurePenalty is the weight multiplier applied per consecutive failure.
const failurePenalty = 0.5

// failureWindow is how long a recorded failure stays effective before decaying.
const failureWindow = 30 * time.Second

// stickyWindow 是域名粘性绑定的有效期。
// 在窗口内，同一个目标域名会一直复用同一个出口节点（同一个 IP），
// 避免短时间内多个 IP 访问同一网站而触发目标站点的安全防控机制。
const stickyWindow = 10 * time.Minute

// stickyEntry 记录某个目标域名当前绑定的出口节点。
type stickyEntry struct {
	nodeID    int64     // 绑定的节点 ID
	expiresAt time.Time // 绑定过期时间，过期后允许重新选择
}

// Selector picks the best exit node for the gateway.
type Selector struct {
	pool *pool.Manager

	mu       sync.Mutex
	failures map[int64]*failure
	sticky   map[string]stickyEntry // 目标域名 -> 绑定的出口节点

	// strategy 当前出口策略（默认加权）。使用 atomic.Value 存放 Strategy。
	strategy atomic.Value
	// rrCounter 轮询策略的游标，每次取节点时自增。
	rrCounter atomic.Uint64

	// pinID 用户通过界面指定的固定出口节点 ID（0 表示未指定）。
	// 仅策略为 fixed 时生效；节点存活时所有协议流量都固定使用该节点。
	pinID atomic.Int64
}

type failure struct {
	count int
	at    time.Time
}

func NewSelector(pool *pool.Manager) *Selector {
	s := &Selector{
		pool:     pool,
		failures: make(map[int64]*failure),
		sticky:   make(map[string]stickyEntry),
	}
	s.strategy.Store(StrategyWeighted)
	return s
}

// Strategy 返回当前出口策略。
func (s *Selector) Strategy() Strategy {
	if v, ok := s.strategy.Load().(Strategy); ok && ValidStrategy(string(v)) {
		return v
	}
	return StrategyWeighted
}

// SetStrategy 设置出口策略。非法策略会被忽略（保持当前策略）。
func (s *Selector) SetStrategy(str Strategy) {
	if ValidStrategy(string(str)) {
		s.strategy.Store(str)
	}
}

// Pin 指定固定出口节点（id > 0），并自动把策略切换为 fixed。
// 之后节点存活时所有协议流量固定走该节点；id <= 0 等价于 Unpin。
func (s *Selector) Pin(id int64) {
	s.pinID.Store(id)
	if id > 0 {
		s.SetStrategy(StrategyFixed)
	}
}

// Unpin 取消固定出口指定，并恢复默认的智能加权策略。
func (s *Selector) Unpin() {
	s.pinID.Store(0)
	s.SetStrategy(StrategyWeighted)
}

// PinnedID 返回当前指定的节点 ID（0 表示未指定）。
// 固定节点在非 fixed 策略下不参与选择，但保留指定以便切回 fixed 时恢复使用。
func (s *Selector) PinnedID() int64 {
	return s.pinID.Load()
}

// Pinned 返回当前「有效」的固定节点快照（不要求存活，供状态展示）：
// 仅当策略为 fixed 时返回指定节点；未指定或策略非 fixed 时返回 nil，
// 避免切换策略后界面仍显示"固定出口已指定"造成误导。
func (s *Selector) Pinned() *model.ProxyNode {
	if s.Strategy() != StrategyFixed {
		return nil
	}
	id := s.pinID.Load()
	if id <= 0 {
		return nil
	}
	return s.pool.Get(id)
}

// Next returns the recommended node: highest weight = score / latency,
// penalized by recent consecutive failures so alternatives are preferred.
func (s *Selector) Next() *model.ProxyNode {
	return s.NextForHost("", "")
}

// NextForProtocol returns the recommended node for the given protocol.
// 选择时会优先在相同协议族内比较：
// - SOCKS5 流量只从 SOCKS5 代理中挑选；
// - HTTP/HTTPS 流量只从 HTTP/HTTPS 代理中挑选；
// 这样不会把不同协议类型的代理混在一起比较。
func (s *Selector) NextForProtocol(protocol model.ProxyProtocol) *model.ProxyNode {
	return s.NextForHost(protocol, "")
}

// stickyKey 返回粘性绑定的 key：协议 + 目标域名。
// 不同协议（HTTP/SOCKS5）访问同一域名时使用独立的粘性绑定，
// 避免 HTTP 流量复用到 SOCKS5 节点（或反之），保证协议语义正确。
func stickyKey(protocol model.ProxyProtocol, host string) string {
	return string(protocol) + "|" + host
}

// NextForHost 返回指定协议下、针对目标域名 host 的出口节点，按当前策略分发：
//   - fixed：固定出口节点存活时直接返回；未指定或节点失效时回退到加权策略；
//   - best：存活节点中选评分最高（叠加失败惩罚）；
//   - random：存活节点中随机挑选，同一域名在粘性窗口内保持稳定；
//   - round-robin：存活节点按 ID 顺序轮流使用（不做粘性）；
//   - weighted（默认）：按权重（score/latency，叠加失败惩罚）选优，
//     同一域名在窗口内复用同一出口节点，避免同一网站短时间内多个 IP 访问触发防控。
//
// 说明：指定节点跨协议使用是安全的——ConnectTCP 会按节点自身协议选择握手方式
// （SOCKS5 握手 / HTTP CONNECT），因此任何协议流量都能复用同一个指定节点。
//
// host 为空时退化为普通的按协议选择（不做粘性）。
func (s *Selector) NextForHost(protocol model.ProxyProtocol, host string) *model.ProxyNode {
	switch s.Strategy() {
	case StrategyFixed:
		if n := s.pinnedNode(); n != nil {
			return n
		}
		// 未指定固定节点或节点失效：回退到加权策略继续。
	case StrategyBest:
		return s.selectBest(protocol)
	case StrategyRandom:
		return s.selectRandom(protocol, host)
	case StrategyRoundRobin:
		return s.selectRoundRobin(protocol)
	}

	// weighted（默认）：先尝试命中粘性绑定，否则按权重选优并记录绑定。
	key := stickyKey(protocol, host)
	if host != "" {
		if node := s.stickyNode(key); node != nil {
			return node
		}
	}
	node := s.selectBest(protocol)
	if node == nil {
		return nil
	}
	if host != "" {
		s.mu.Lock()
		s.sticky[key] = stickyEntry{
			nodeID:    node.ID,
			expiresAt: time.Now().Add(stickyWindow),
		}
		s.mu.Unlock()
	}
	return node
}

// pinnedNode 返回固定策略下应使用的节点：指定了固定节点且存活时返回该节点。
// 指定节点已被删除或淘汰时自动取消固定（dead 节点不取消，等其恢复后自动生效）。
func (s *Selector) pinnedNode() *model.ProxyNode {
	id := s.pinID.Load()
	if id <= 0 {
		return nil
	}
	n := s.pool.Get(id)
	if n == nil {
		// 指定的节点已被删除或被自动淘汰，固定出口不再有意义，自动取消。
		s.pinID.Store(0)
		return nil
	}
	if n.Status == model.StatusAlive {
		return n
	}
	return nil
}

// stickyNode 查找 key（协议 + 域名）的粘性绑定：
//   - 绑定存在且未过期，且节点仍存活 -> 返回该节点；
//   - 绑定已过期 -> 清除绑定，返回 nil；
//   - 绑定的节点已不在池中或已死亡 -> 清除绑定，返回 nil。
func (s *Selector) stickyNode(key string) *model.ProxyNode {
	s.mu.Lock()
	entry, ok := s.sticky[key]
	if !ok {
		s.mu.Unlock()
		return nil
	}
	if time.Now().After(entry.expiresAt) {
		delete(s.sticky, key)
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	// 校验绑定的节点是否仍然可用。
	// 按 ID 直接查找（O(1)），避免每次请求都对整个存活池做克隆+排序。
	n := s.pool.Get(entry.nodeID)
	if n != nil && n.Status == model.StatusAlive {
		return n
	}
	// 节点已失效，清除绑定，让下一次请求重新选择。
	s.mu.Lock()
	delete(s.sticky, key)
	s.mu.Unlock()
	return nil
}

// selectBest 在指定协议族内挑选最优节点；协议族内没有候选时回退到全部存活节点
// （软限制：SOCKS5 流量没有 SOCKS5 节点时也可以复用 HTTP 节点）。
// 排序优先级：存活（优先）→ 有效分数（降序）→ 延迟（升序）→ ID（升序）→ host（升序）。
// 有效分数 = 原始分数 × 失败惩罚（失败窗口内按 0.5^failures 衰减），
// 与代理池列表的排序口径保持一致：存活优先，分数优先，同分比延迟。
func (s *Selector) selectBest(protocol model.ProxyProtocol) *model.ProxyNode {
	alive := s.pool.Alive()
	if len(alive) == 0 {
		return nil
	}
	candidates := filterByProtocol(alive, protocol)
	if len(candidates) == 0 {
		candidates = alive
	}
	s.sortCandidates(candidates)
	return candidates[0]
}

// selectRandom 在指定协议族内随机挑选一个存活节点；协议族内没有候选时回退到全部存活节点。
// 同一目标域名在粘性窗口内保持稳定，避免同一网站短时间内频繁更换出口 IP。
func (s *Selector) selectRandom(protocol model.ProxyProtocol, host string) *model.ProxyNode {
	key := stickyKey(protocol, host)
	if host != "" {
		if node := s.stickyNode(key); node != nil {
			return node
		}
	}

	alive := s.pool.Alive()
	if len(alive) == 0 {
		return nil
	}
	candidates := filterByProtocol(alive, protocol)
	if len(candidates) == 0 {
		candidates = alive
	}
	node := candidates[rand.Intn(len(candidates))]

	if host != "" {
		s.mu.Lock()
		s.sticky[key] = stickyEntry{
			nodeID:    node.ID,
			expiresAt: time.Now().Add(stickyWindow),
		}
		s.mu.Unlock()
	}
	return node
}

// selectRoundRobin 在指定协议族内按 ID 顺序轮流返回存活节点（协议族内为空时回退到全部存活节点）。
// 游标为原子计数器，多 goroutine 并发调用时也能保证不重复。
func (s *Selector) selectRoundRobin(protocol model.ProxyProtocol) *model.ProxyNode {
	alive := s.pool.Alive()
	if len(alive) == 0 {
		return nil
	}
	candidates := filterByProtocol(alive, protocol)
	if len(candidates) == 0 {
		candidates = alive
	}
	// 按 ID 稳定排序，保证轮转顺序确定（不依赖池内顺序）。
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].ID < candidates[j].ID })
	i := s.rrCounter.Add(1) - 1
	return candidates[i%uint64(len(candidates))]
}

// NextStrict 返回指定协议族内的最优存活节点，且不做跨协议回退。
// 用于必须匹配特定协议的场景：UDP 中继只能经 SOCKS5 节点承载，
// HTTP/HTTPS 节点无法转发 UDP，因此协议族内为空时必须返回 nil 而不是回退。
func (s *Selector) NextStrict(protocol model.ProxyProtocol) *model.ProxyNode {
	alive := s.pool.Alive()
	if len(alive) == 0 {
		return nil
	}
	candidates := filterByProtocol(alive, protocol)
	if len(candidates) == 0 {
		return nil
	}
	s.sortCandidates(candidates)
	return candidates[0]
}

// filterByProtocol 返回 nodes 中属于指定协议族的存活节点。
// SOCKS5 只匹配 SOCKS5 节点；HTTP/HTTPS 匹配两者；protocol 为空时不筛选。
func filterByProtocol(nodes []*model.ProxyNode, protocol model.ProxyProtocol) []*model.ProxyNode {
	switch protocol {
	case model.ProtocolSOCKS5:
		var out []*model.ProxyNode
		for _, n := range nodes {
			if n.Protocol == model.ProtocolSOCKS5 {
				out = append(out, n)
			}
		}
		return out
	case model.ProtocolHTTP, model.ProtocolHTTPS:
		var out []*model.ProxyNode
		for _, n := range nodes {
			if n.Protocol == model.ProtocolHTTP || n.Protocol == model.ProtocolHTTPS {
				out = append(out, n)
			}
		}
		return out
	default:
		return nodes
	}
}

// sortCandidates 对候选节点按 存活 → 有效分数 → 延迟 → ID → host 排序，
// 同时清理过期的失败记录。传入切片会被原地排序（上游克隆，安全）。
func (s *Selector) sortCandidates(candidates []*model.ProxyNode) {
	s.mu.Lock()
	penalties := make(map[int64]int, len(candidates))
	for _, n := range candidates {
		if f, ok := s.failures[n.ID]; ok {
			if time.Since(f.at) > failureWindow {
				delete(s.failures, n.ID)
			} else {
				penalties[n.ID] = f.count
			}
		}
	}
	s.mu.Unlock()

	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if ra, rb := pool.StatusRank(a.Status), pool.StatusRank(b.Status); ra != rb {
			return ra < rb
		}
		sa := effectiveScore(a, penalties[a.ID])
		sb := effectiveScore(b, penalties[b.ID])
		if sa != sb {
			return sa > sb
		}
		if a.Latency != b.Latency {
			return a.Latency < b.Latency
		}
		if a.ID != b.ID {
			return a.ID < b.ID
		}
		return a.Host < b.Host
	})
}

// FailOn records a consecutive failure for the node so its weight drops and
// an alternative is preferred on the next attempt.
// 同时会清除所有绑定到该节点的粘性记录：节点已经不可用，
// 依赖它的域名需要重新选择出口，避免继续复用坏节点。
func (s *Selector) FailOn(id int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.failures[id]
	if f == nil {
		f = &failure{}
		s.failures[id] = f
	}
	f.count++
	f.at = time.Now()

	// 清除该节点上的所有粘性绑定。
	for host, entry := range s.sticky {
		if entry.nodeID == id {
			delete(s.sticky, host)
		}
	}
}

// Success clears the failure record for a node after a successful dial.
func (s *Selector) Success(id int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.failures, id)
}

// effectiveScore 返回节点在选择阶段使用的有效分数：
// 失败窗口内按 0.5^failures 衰减，窗口外恢复原始分数。
func effectiveScore(n *model.ProxyNode, failures int) float64 {
	if n.Score <= 0 {
		return 0
	}
	s := float64(n.Score)
	if failures > 0 {
		s *= math.Pow(failurePenalty, float64(failures))
	}
	return s
}
