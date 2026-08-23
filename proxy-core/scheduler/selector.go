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
	// StrategyChain 代理链路：客户端按序经过多个代理节点到达目标。
	// 链的选择与连接由网关负责（链上任意节点失效则该链不可用）。
	StrategyChain Strategy = "chain"
	// StrategyAutoChain 自动链路：按配置的层数与每层选择策略，
	// 自动从存活节点中挑出 N 个互不相同的节点组成链路，无需手动指定节点。
	StrategyAutoChain Strategy = "auto-chain"
)

// ValidStrategy 判断字符串是否是合法的出口策略。
func ValidStrategy(s string) bool {
	switch Strategy(s) {
	case StrategyFixed, StrategyBest, StrategyRandom, StrategyWeighted, StrategyRoundRobin, StrategyChain, StrategyAutoChain:
		return true
	}
	return false
}

// ValidChainSelection 判断字符串是否是合法的自动链路每层选择策略。
func ValidChainSelection(s string) bool {
	switch Strategy(s) {
	case StrategyWeighted, StrategyRandom, StrategyBest:
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
		{Value: string(StrategyChain), Label: "代理链路", Desc: "客户端依次经过多个代理节点到达目标（链上节点全部存活才可用）"},
		{Value: string(StrategyAutoChain), Label: "自动链路", Desc: "按配置的层数自动从存活节点中挑选节点组成链路，无需手动指定"},
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

// stickyMaxEntries 是粘性绑定的容量上限。域名粘性只有被再次访问才惰性清理，
// 恶意随机生成海量域名可在窗口内撑爆内存，超出上限时强制淘汰最早过期的条目。
const stickyMaxEntries = 4096

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
// 策略真正变化时会清空域名粘性绑定，使新策略立即对全部分流目标生效，
// 避免旧策略遗留的 10 分钟粘性绑定继续把流量送往原节点（表现为"切换策略未立即生效"）。
func (s *Selector) SetStrategy(str Strategy) {
	if !ValidStrategy(string(str)) {
		return
	}
	if s.Strategy() == str {
		return
	}
	s.strategy.Store(str)
	s.clearSticky()
}

// clearSticky 清空全部域名粘性绑定（切换策略时调用），使新策略立即对全部分流目标生效。
func (s *Selector) clearSticky() {
	s.mu.Lock()
	s.sticky = make(map[string]stickyEntry)
	s.mu.Unlock()
}

// Pin 指定固定出口节点（id > 0），并自动把策略切换为 fixed。
// 之后无论该节点当前是否存活，所有协议流量都固定走它（以用户指定为准）；
// 节点不可达时由连接层报错，不再静默回退到其它节点，保证"指定节点立即生效"。
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
// 选路不区分协议：ConnectTCP 会按节点自身协议完成握手，任何协议流量都能复用任意节点，
// 因此这里与按策略的通用选择一致（protocol 仅作兼容参数保留，不参与筛选）。
func (s *Selector) NextForProtocol(protocol model.ProxyProtocol) *model.ProxyNode {
	return s.NextForHost(protocol, "")
}

// stickyKey 返回粘性绑定的 key：目标域名。
// 选路不区分协议后，HTTP 与 SOCKS5 流量访问同一域名时复用同一个出口节点，
// 因此粘性绑定只按域名区分，避免同一网站短时间内多个 IP 访问触发防控。
func stickyKey(host string) string {
	return host
}

// NextForHost 返回针对目标域名 host 的出口节点，按当前策略分发：
//   - fixed：固定出口节点存活时直接返回；未指定或节点失效时回退到加权策略；
//   - best：存活节点中选评分最高（叠加失败惩罚）；
//   - random：存活节点中随机挑选，同一域名在粘性窗口内保持稳定；
//   - round-robin：存活节点按 ID 顺序轮流使用（不做粘性）；
//   - chain：不在此选择单跳节点（由网关按已配置链路逐跳连接），直接返回 nil；
//   - weighted（默认）：按权重（score/latency，叠加失败惩罚）选优，
//     同一域名在窗口内复用同一出口节点，避免同一网站短时间内多个 IP 访问触发防控。
//
// protocol 参数已不参与选路：ConnectTCP 会按节点自身协议选择握手方式
// （SOCKS5 握手 / HTTP CONNECT / HTTPS TLS+CONNECT），因此 HTTP 与 SOCKS5
// 流量都能复用任意节点，无需按协议族分流，统一按当前策略从存活节点中选择。
//
// host 为空时退化为普通的按策略选择（不做粘性）。
func (s *Selector) NextForHost(protocol model.ProxyProtocol, host string) *model.ProxyNode {
	_ = protocol // 选路不区分协议：见函数注释
	switch s.Strategy() {
	case StrategyFixed:
		if n := s.pinnedNode(); n != nil {
			return n
		}
		// 未指定固定节点或节点失效：回退到加权策略继续。
	case StrategyBest:
		return s.selectBest()
	case StrategyRandom:
		return s.selectRandom(host)
	case StrategyRoundRobin:
		return s.selectRoundRobin()
	case StrategyChain:
		// 链路策略不在此选择单跳节点，由网关按已配置的链路逐跳连接。
		return nil
	case StrategyAutoChain:
		// 自动链路策略不在此选择单跳节点，由网关按配置自动挑选 N 个节点逐跳连接。
		return nil
	}

	// weighted（默认）：先尝试命中粘性绑定，否则按权重选优并记录绑定。
	key := stickyKey(host)
	if host != "" {
		if node := s.stickyNode(key); node != nil {
			return node
		}
	}
	node := s.selectBest()
	if node == nil {
		return nil
	}
	if host != "" {
		s.mu.Lock()
		s.sticky[key] = stickyEntry{
			nodeID:    node.ID,
			expiresAt: time.Now().Add(stickyWindow),
		}
		s.pruneSticky()
		s.mu.Unlock()
	}
	return node
}

// pinnedNode 返回固定策略下应使用的节点：只要指定的节点仍在池中（未被删除）即返回，
// 不论其当前存活状态——固定出口以用户指定为准，节点不可达时由连接层报错，
// 不静默回退到其它节点，从而保证"指定节点立即生效"。
func (s *Selector) pinnedNode() *model.ProxyNode {
	id := s.pinID.Load()
	if id <= 0 {
		return nil
	}
	n := s.pool.Get(id)
	if n == nil {
		// 指定的节点已被删除或淘汰，固定出口不再有意义，自动取消。
		s.pinID.Store(0)
		return nil
	}
	return n
}

// pruneSticky 清理粘性绑定：先清过期条目；仍超上限时一次性找出最接近过期的
// 条目批量淘汰（避免循环内重复全表扫描的 O(n²)），防止恶意随机域名在窗口内无限增长。
// 必须在持有 s.mu 时调用。
func (s *Selector) pruneSticky() {
	now := time.Now()
	for key, e := range s.sticky {
		if now.After(e.expiresAt) {
			delete(s.sticky, key)
		}
	}
	if len(s.sticky) <= stickyMaxEntries {
		return
	}
	// 仍超上限：一次遍历选出最早过期的条目，批量删除到上限内。
	// sticky 与 stickyMaxEntries 规模通常很小（4096），排序开销可控。
	type entry struct {
		key  string
		at   time.Time
	}
	all := make([]entry, 0, len(s.sticky))
	for key, e := range s.sticky {
		all = append(all, entry{key: key, at: e.expiresAt})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].at.Before(all[j].at) })
	excess := len(all) - stickyMaxEntries
	for i := 0; i < excess; i++ {
		delete(s.sticky, all[i].key)
	}
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
	// 安全路由命中（如检出 HTTPS 中间人）的节点视为失效：清除绑定并重新选路，
	// 避免粘性窗口内继续把流量送往已标记的不安全节点。
	n := s.pool.Get(entry.nodeID)
	if n != nil && n.Status == model.StatusAlive && !s.pool.Excluded(n) {
		return n
	}
	// 节点已失效，清除绑定，让下一次请求重新选择。
	s.mu.Lock()
	delete(s.sticky, key)
	s.mu.Unlock()
	return nil
}

// selectBest 在全部存活节点中挑选最优节点。
// 排序优先级：存活（优先）→ 有效分数（降序）→ 延迟（升序）→ ID（升序）→ host（升序）。
// 有效分数 = 原始分数 × 失败惩罚（失败窗口内按 0.5^failures 衰减），
// 与代理池列表的排序口径保持一致：存活优先，分数优先，同分比延迟。
func (s *Selector) selectBest() *model.ProxyNode {
	alive := s.pool.Alive()
	if len(alive) == 0 {
		return nil
	}
	s.sortCandidates(alive)
	return alive[0]
}

// selectRandom 从全部存活节点中随机挑选一个。
// 同一目标域名在粘性窗口内保持稳定，避免同一网站短时间内频繁更换出口 IP。
func (s *Selector) selectRandom(host string) *model.ProxyNode {
	key := stickyKey(host)
	if host != "" {
		if node := s.stickyNode(key); node != nil {
			return node
		}
	}

	node := s.pool.PickRandom()
	if node == nil {
		return nil
	}

	if host != "" {
		s.mu.Lock()
		s.sticky[key] = stickyEntry{
			nodeID:    node.ID,
			expiresAt: time.Now().Add(stickyWindow),
		}
		s.pruneSticky()
		s.mu.Unlock()
	}
	return node
}

// selectRoundRobin 按 ID 顺序轮流返回全部存活节点。
// 使用 pool.PickRoundRobin() 单次遍历避免全量克隆+排序。
func (s *Selector) selectRoundRobin() *model.ProxyNode {
	return s.pool.PickRoundRobin()
}

// NextStrict 返回指定协议族内的最优存活节点，且不做跨协议回退。
// 用于必须匹配特定协议的场景：UDP 中继只能经 SOCKS5 节点承载，
// HTTP/HTTPS 节点无法转发 UDP，因此协议族内为空时必须返回 nil 而不是回退。
// 使用 pool.PickByProtocol() 单次遍历避免 Alive()+filterByProtocol 双重遍历。
func (s *Selector) NextStrict(protocol model.ProxyProtocol) *model.ProxyNode {
	return s.pool.PickByProtocol(protocol)
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

// SelectChain 为自动链路策略挑选 n 个互不相同的存活节点，
// 每层按 selection 策略选择（weighted / random / best）。
// 存活节点不足 n 时返回实际可用数量；无存活节点时返回空切片。
// 返回的节点顺序即链路的跳顺序（客户端 → nodes[0] → … → target）。
func (s *Selector) SelectChain(n int, selection Strategy) []*model.ProxyNode {
	if n <= 0 {
		return nil
	}
	aliveCount := s.pool.CountAlive()
	if aliveCount == 0 {
		return nil
	}
	if n > aliveCount {
		n = aliveCount
	}
	// 逐层挑选，每层排除已选节点，保证同一链路内节点不重复。
	// StrategyBest 需应用失败惩罚，使用 selector 自身方法；
	// Random/Weighted 使用 pool 单遍历方法避免全量克隆。
	picked := make(map[int64]bool, n)
	nodes := make([]*model.ProxyNode, 0, n)
	for len(nodes) < n {
		var node *model.ProxyNode
		switch selection {
		case StrategyBest:
			// 需要完整候选列表以应用惩罚，获取一次 Alive（仅 Best 需要）
			alive := s.pool.Alive()
			node = s.pickBestNotIn(alive, picked)
		case StrategyRandom:
			node = s.pool.PickRandomNotIn(picked)
		default: // weighted（默认）：加权挑选
			node = s.pool.PickWeightedNotIn(picked)
		}
		if node == nil {
			break
		}
		picked[node.ID] = true
		nodes = append(nodes, node)
	}
	return nodes
}

// pickBestNotIn 从 candidates 中选评分最高且未被 picked 的节点。
func (s *Selector) pickBestNotIn(candidates []*model.ProxyNode, picked map[int64]bool) *model.ProxyNode {
	best := (*model.ProxyNode)(nil)
	var bestWeight float64
	for _, n := range candidates {
		if picked[n.ID] {
			continue
		}
		penalty := s.failureCount(n.ID)
		w := effectiveScore(n, penalty)
		// 同分数比延迟：延迟越低越优
		if best == nil || w > bestWeight || (w == bestWeight && n.Latency < best.Latency) {
			best = n
			bestWeight = w
		}
	}
	return best
}

// failureCount 返回节点当前生效的连续失败次数（窗口外为 0）。
func (s *Selector) failureCount(id int64) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.failures[id]
	if !ok {
		return 0
	}
	if time.Since(f.at) > failureWindow {
		delete(s.failures, id)
		return 0
	}
	return f.count
}
