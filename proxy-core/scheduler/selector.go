package scheduler

import (
	"math"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/axetroy/ProxyPilot/proxy-core/model"
	"github.com/axetroy/ProxyPilot/proxy-core/pool"
)

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

	// pinID 用户通过界面指定的固定出口节点 ID（0 表示未指定）。
	// 指定后，节点存活时所有协议流量都固定使用该节点，不再按评分自动选择。
	pinID atomic.Int64
}

type failure struct {
	count int
	at    time.Time
}

func NewSelector(pool *pool.Manager) *Selector {
	return &Selector{
		pool:     pool,
		failures: make(map[int64]*failure),
		sticky:   make(map[string]stickyEntry),
	}
}

// Pin 指定固定出口节点（id > 0）。之后节点存活时所有流量固定走该节点；
// id <= 0 等价于 Unpin。
func (s *Selector) Pin(id int64) {
	s.pinID.Store(id)
}

// Unpin 取消固定出口指定，恢复按评分自动选择。
func (s *Selector) Unpin() {
	s.pinID.Store(0)
}

// PinnedID 返回当前指定的节点 ID（0 表示未指定）。
func (s *Selector) PinnedID() int64 {
	return s.pinID.Load()
}

// Pinned 返回当前指定的节点快照（不要求存活，供状态展示）；
// 未指定时返回 nil。
func (s *Selector) Pinned() *model.ProxyNode {
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

// NextForHost 返回指定协议下、针对目标域名 host 的推荐节点。
// 核心逻辑：
//  0. 若用户指定了固定出口且该节点存活，则直接返回它（不使用评分最高的，也不受流量协议限制）；
//  1. 否则尝试命中"域名粘性"：同一域名在窗口内复用同一出口 IP，避免同一网站短时间内多个 IP 访问触发防控；
//  2. 都没有则按权重（score / latency，并叠加失败惩罚）重新选择最优节点，并把结果绑定到该域名。
//
// 说明：指定节点跨协议使用是安全的——ConnectTCP 会按节点自身协议选择握手方式
// （SOCKS5 握手 / HTTP CONNECT），因此任何协议流量都能复用同一个指定节点。
//
// host 为空时退化为普通的按协议选择（不做粘性）。
func (s *Selector) NextForHost(protocol model.ProxyProtocol, host string) *model.ProxyNode {
	// 0. 指定固定出口：节点存活时始终使用它，满足"指定了就必须用它"的需求。
	if id := s.pinID.Load(); id > 0 {
		n := s.pool.Get(id)
		if n == nil {
			// 指定的节点已被删除或被自动淘汰，固定出口不再有意义，自动取消。
			// （dead 节点不取消，等其重新检测存活后自动恢复固定。）
			s.pinID.Store(0)
		} else if n.Status == model.StatusAlive {
			return n
		}
	}

	// 粘性绑定按"协议 + 域名"区分，避免跨协议复用节点。
	key := stickyKey(protocol, host)

	// 1. 先尝试命中粘性绑定：同一域名在窗口内复用同一出口。
	if host != "" {
		if node := s.stickyNode(key); node != nil {
			return node
		}
	}

	// 2. 没有可用粘性，走常规的最优节点选择。
	node := s.selectBest(protocol)
	if node == nil {
		return nil
	}

	// 3. 记录粘性绑定，让后续同域名的请求继续复用该节点。
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

// selectBest 在指定协议族内挑选最优节点。
// 排序优先级：存活（优先）→ 有效分数（降序）→ 延迟（升序）→ ID（升序）→ host（升序）。
// 有效分数 = 原始分数 × 失败惩罚（失败窗口内按 0.5^failures 衰减），
// 与代理池列表的排序口径保持一致：存活优先，分数优先，同分比延迟。
func (s *Selector) selectBest(protocol model.ProxyProtocol) *model.ProxyNode {
	alive := s.pool.Alive()
	if len(alive) == 0 {
		return nil
	}

	var candidates []*model.ProxyNode
	switch protocol {
	case model.ProtocolSOCKS5:
		for _, n := range alive {
			if n.Protocol == model.ProtocolSOCKS5 {
				candidates = append(candidates, n)
			}
		}
	case model.ProtocolHTTP, model.ProtocolHTTPS:
		for _, n := range alive {
			if n.Protocol == model.ProtocolHTTP || n.Protocol == model.ProtocolHTTPS {
				candidates = append(candidates, n)
			}
		}
	default:
		candidates = alive
	}
	if len(candidates) == 0 {
		candidates = alive
	}

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
	return candidates[0]
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
