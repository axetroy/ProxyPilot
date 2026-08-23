package pool

import (
	"context"
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/axetroy/ProxyPilot/proxy-core/bus"
	"github.com/axetroy/ProxyPilot/proxy-core/geoip"
	"github.com/axetroy/ProxyPilot/proxy-core/model"
	"github.com/axetroy/ProxyPilot/proxy-core/storage"
)

type nodeChecker interface {
	Check(node *model.ProxyNode) (model.CheckResult, error)
}

// MITMDetectFunc 中间人探测函数：返回是否检出 HTTPS 中间人及原因描述（供错误日志）。
type MITMDetectFunc func(node *model.ProxyNode) (mitm bool, detail string)

// ExcludeFilter 节点排除过滤器：返回 true 表示该节点不应被自动选用（安全路由）。
type ExcludeFilter func(n *model.ProxyNode) bool

// mitmRecheckInterval 中间人检测节流窗口：同一节点在该时间内不重复探测，
// 避免每次健康检测都额外增加一次 TLS 握手开销。
const mitmRecheckInterval = 24 * time.Hour

type Manager struct {
	store    *storage.Store
	checker  atomic.Pointer[nodeChecker]
	bus      *bus.Bus
	mx       sync.RWMutex
	nodes    map[int64]*model.ProxyNode
	concur   atomic.Int32
	checking atomic.Bool
	rrCounter atomic.Int64 // 轮询计数器

	// refreshInterval 单位纳秒，RefreshLoop 每轮读取，支持热更新
	refreshInterval atomic.Int64

	// mitmDetector 可选中的人检测函数（nil 表示未启用中间人探测）。
	mitmDetector atomic.Pointer[MITMDetectFunc]
	// excludeFilter 安全路由过滤器（nil 表示不过滤）：命中节点不会被自动选用；
	// 全部存活节点都被过滤时回退为不过滤，保证可用性优先。
	excludeFilter atomic.Pointer[ExcludeFilter]
}

func NewManager(store *storage.Store, checker nodeChecker, bus *bus.Bus, concurrency int) *Manager {
	if concurrency <= 0 {
		concurrency = 1
	}
	m := &Manager{
		store: store,
		bus:   bus,
		nodes: make(map[int64]*model.ProxyNode),
	}
	m.checker.Store(&checker)
	m.concur.Store(int32(concurrency))
	m.refreshInterval.Store(int64(15 * time.Minute))
	return m
}

// SetChecker 热更新节点检测器（target/timeout 变化时由配置更新触发）。
func (m *Manager) SetChecker(c nodeChecker) {
	m.checker.Store(&c)
}

// SetMITMDetector 注入 HTTPS 中间人探测函数（nil 关闭探测）。
// 探测在节点连通性检测通过后进行，检出时写错误日志并持久化标记。
func (m *Manager) SetMITMDetector(fn MITMDetectFunc) {
	if fn == nil {
		m.mitmDetector.Store(nil)
		return
	}
	m.mitmDetector.Store(&fn)
}

// SetExcludeFilter 注入安全路由过滤器（nil 表示不过滤）。
// 命中过滤器的节点不会被任何自动选路策略选中；
// 全部候选都被过滤时自动回退为不过滤，保证可用性优先于安全性。
func (m *Manager) SetExcludeFilter(fn ExcludeFilter) {
	if fn == nil {
		m.excludeFilter.Store(nil)
		return
	}
	m.excludeFilter.Store(&fn)
}

// excluded 报告节点是否命中当前排除过滤器。
func (m *Manager) excluded(n *model.ProxyNode) bool {
	if fn := m.excludeFilter.Load(); fn != nil {
		return (*fn)(n)
	}
	return false
}

// Excluded 供外部（如调度器粘性绑定校验）判断节点是否被排除过滤器命中。
func (m *Manager) Excluded(n *model.ProxyNode) bool {
	return m.excluded(n)
}

// SetConcurrency 热更新并发检测数。
func (m *Manager) SetConcurrency(n int) {
	if n <= 0 {
		n = 1
	}
	m.concur.Store(int32(n))
}

// SetRefreshInterval 热更新自动检测周期，RefreshLoop 下一轮生效。
func (m *Manager) SetRefreshInterval(d time.Duration) {
	if d <= 0 {
		d = 15 * time.Minute
	}
	m.refreshInterval.Store(int64(d))
}

// Load populates the in-memory pool from the store.
func (m *Manager) Load() error {
	nodes, err := m.store.ListNode()
	if err != nil {
		return err
	}
	m.mx.Lock()
	for _, n := range nodes {
		m.nodes[n.ID] = n
	}
	m.mx.Unlock()
	m.bus.Info(fmt.Sprintf("loaded %d nodes from storage", len(nodes)))
	return nil
}

// AddNodes persists and merges new nodes. Returns count of newly added nodes.
func (m *Manager) AddNodes(nodes []*model.ProxyNode) int {
	added := 0
	updated := 0
	for _, n := range nodes {
		isNew, err := m.store.SaveNode(n)
		if err != nil {
			m.bus.Debug(fmt.Sprintf("save node failed: %v", err))
			continue
		}
		if isNew {
			added++
			m.mx.Lock()
			m.nodes[n.ID] = n
			m.mx.Unlock()
		} else {
			updated++
			// 节点已存在：凭据可能在 SaveNode 中被更新，刷新内存池避免网关继续用旧凭据。
			if n.ID > 0 {
				m.mx.Lock()
				if cur, ok := m.nodes[n.ID]; ok {
					cur.Username = n.Username
					cur.Password = n.Password
					cur.UpdatedAt = n.UpdatedAt
				}
				m.mx.Unlock()
			}
		}
	}
	if added > 0 && updated > 0 {
		m.bus.Info(fmt.Sprintf("pool delta: +%d new, %d existing", added, updated))
	} else if added > 0 {
		m.bus.Info(fmt.Sprintf("pool grows: +%d nodes", added))
	}
	return added
}

// List returns a snapshot of all nodes.
// 返回结果按 分数（降序）→ 延迟（升序）→ ID（升序）→ host（升序）排序，
// 保证 API 返回的节点列表顺序稳定且与选择阶段口径一致。
func (m *Manager) List() []*model.ProxyNode {
	m.mx.RLock()
	defer m.mx.RUnlock()
	out := make([]*model.ProxyNode, 0, len(m.nodes))
	for _, n := range m.nodes {
		out = append(out, cloneNode(n))
	}
	sortNodes(out)
	return out
}

// StatusRank 返回节点状态的排序优先级：alive(0) < checking(1) < new(2) < dead(3)。
// 所有状态都有明确顺序，保证排序比较器是严格全序；
// 否则（如只区分 alive/非 alive）两个非 alive 节点互相比较都返回 false，
// sort.Slice 不稳定排序会导致它们的相对顺序每次随机。
func StatusRank(s model.ProxyStatus) int {
	switch s {
	case model.StatusAlive:
		return 0
	case model.StatusChecking:
		return 1
	case model.StatusNew:
		return 2
	case model.StatusDead:
		return 3
	default:
		return 4
	}
}

// sortNodes 按 存活（优先）→ 分数（降序）→ 延迟（升序）→ ID（升序）→ host（升序）排序。
func sortNodes(nodes []*model.ProxyNode) {
	sort.Slice(nodes, func(i, j int) bool {
		a, b := nodes[i], nodes[j]
		if ra, rb := StatusRank(a.Status), StatusRank(b.Status); ra != rb {
			return ra < rb
		}
		if a.Score != b.Score {
			return a.Score > b.Score
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

// Alive returns nodes currently marked alive.
// 返回结果同样按 分数 → 延迟 → ID → host 排序，与 List 口径一致。
// 安全路由开启时自动跳过被排除的节点；全部存活节点都被排除时回退为不过滤（可用性优先）。
func (m *Manager) Alive() []*model.ProxyNode {
	out := m.alive(true)
	if len(out) == 0 {
		return m.alive(false)
	}
	return out
}

func (m *Manager) alive(avoidUnsafe bool) []*model.ProxyNode {
	m.mx.RLock()
	defer m.mx.RUnlock()
	var out []*model.ProxyNode
	for _, n := range m.nodes {
		if n.Status != model.StatusAlive {
			continue
		}
		if avoidUnsafe && m.excluded(n) {
			continue
		}
		out = append(out, cloneNode(n))
	}
	sortNodes(out)
	return out
}

// PickBest 返回评分最高（按原始分数、延迟、ID、host 排序）的存活节点。
// 单次遍历 O(n)，不克隆也不全量排序，适合热路径（如网关选路）。
// 安全路由开启时跳过被排除节点；全部被排除时回退为不过滤。返回副本。
func (m *Manager) PickBest() *model.ProxyNode {
	if best := m.pickBest(true); best != nil {
		return best
	}
	return m.pickBest(false)
}

func (m *Manager) pickBest(avoidUnsafe bool) *model.ProxyNode {
	m.mx.RLock()
	defer m.mx.RUnlock()
	var best *model.ProxyNode
	for _, n := range m.nodes {
		if n.Status != model.StatusAlive {
			continue
		}
		if avoidUnsafe && m.excluded(n) {
			continue
		}
		if best == nil {
			best = n
			continue
		}
		// 排序规则：分数降序 → 延迟升序 → ID 升序 → host 升序
		if n.Score > best.Score {
			best = n
			continue
		}
		if n.Score == best.Score {
			if n.Latency < best.Latency {
				best = n
				continue
			}
			if n.Latency == best.Latency {
				if n.ID < best.ID {
					best = n
					continue
				}
				if n.ID == best.ID && n.Host < best.Host {
					best = n
				}
			}
		}
	}
	if best == nil {
		return nil
	}
	return cloneNode(best)
}

// PickRandom 随机返回一个存活节点。
// 使用水塘采样，单次遍历 O(n)，无需构建完整切片。
// 安全路由开启时跳过被排除节点；全部被排除时回退为不过滤。
func (m *Manager) PickRandom() *model.ProxyNode {
	if n := m.pickRandom(true); n != nil {
		return n
	}
	return m.pickRandom(false)
}

func (m *Manager) pickRandom(avoidUnsafe bool) *model.ProxyNode {
	m.mx.RLock()
	defer m.mx.RUnlock()
	var chosen *model.ProxyNode
	count := 0
	for _, n := range m.nodes {
		if n.Status != model.StatusAlive {
			continue
		}
		if avoidUnsafe && m.excluded(n) {
			continue
		}
		count++
		// 水塘采样：第 k 个元素以 1/k 概率被选中
		if rand.Intn(count) == 0 {
			chosen = n
		}
	}
	if chosen == nil {
		return nil
	}
	return cloneNode(chosen)
}

// PickRoundRobin 按 ID 顺序轮询返回存活节点。
// 使用原子计数器保证并发安全，单次遍历 O(n) 收集存活节点 ID，
// 再按 ID 排序取模，避免全量克隆+排序。
// 安全路由开启时跳过被排除节点；全部被排除时回退为不过滤。
func (m *Manager) PickRoundRobin() *model.ProxyNode {
	if n := m.pickRoundRobin(true); n != nil {
		return n
	}
	return m.pickRoundRobin(false)
}

func (m *Manager) pickRoundRobin(avoidUnsafe bool) *model.ProxyNode {
	m.mx.RLock()
	var aliveIDs []int64
	for id, n := range m.nodes {
		if n.Status != model.StatusAlive {
			continue
		}
		if avoidUnsafe && m.excluded(n) {
			continue
		}
		aliveIDs = append(aliveIDs, id)
	}
	m.mx.RUnlock()
	if len(aliveIDs) == 0 {
		return nil
	}
	// 按 ID 排序保证顺序确定
	sort.Slice(aliveIDs, func(i, j int) bool { return aliveIDs[i] < aliveIDs[j] })
	// 原子计数器选下一个（atomic.Int64 用 Load/Add 方法）
	idx := int(m.rrCounter.Add(1)-1) % len(aliveIDs)
	return m.Get(aliveIDs[idx])
}

// PickByProtocol 返回指定协议族中评分最高的存活节点（单次遍历）。
// protocol: SOCKS5 只匹配 SOCKS5；HTTP/HTTPS 匹配两者；空=不筛选。
// 供 NextStrict 等热路径使用，避免 Alive()+filterByProtocol 双重遍历。
// 安全路由开启时跳过被排除节点；全部被排除时回退为不过滤（UDP 场景同样优先保证可用）。
func (m *Manager) PickByProtocol(protocol model.ProxyProtocol) *model.ProxyNode {
	if n := m.pickByProtocol(protocol, true); n != nil {
		return n
	}
	return m.pickByProtocol(protocol, false)
}

func (m *Manager) pickByProtocol(protocol model.ProxyProtocol, avoidUnsafe bool) *model.ProxyNode {
	m.mx.RLock()
	defer m.mx.RUnlock()
	var best *model.ProxyNode
	for _, n := range m.nodes {
		if n.Status != model.StatusAlive {
			continue
		}
		if avoidUnsafe && m.excluded(n) {
			continue
		}
		// 协议筛选
		switch protocol {
		case model.ProtocolSOCKS5:
			if n.Protocol != model.ProtocolSOCKS5 {
				continue
			}
		case model.ProtocolHTTP, model.ProtocolHTTPS:
			if n.Protocol != model.ProtocolHTTP && n.Protocol != model.ProtocolHTTPS {
				continue
			}
		}
		if best == nil {
			best = n
			continue
		}
		if n.Score > best.Score {
			best = n
			continue
		}
		if n.Score == best.Score {
			if n.Latency < best.Latency {
				best = n
				continue
			}
			if n.Latency == best.Latency {
				if n.ID < best.ID {
					best = n
					continue
				}
				if n.ID == best.ID && n.Host < best.Host {
					best = n
				}
			}
		}
	}
	if best == nil {
		return nil
	}
	return cloneNode(best)
}

// PickWeightedNotIn 从存活节点中按 加权随机 选一个未在 picked 中的节点。
// 权重 = 有效分数 / 延迟（有效分数 = 原始分数，不含 selector 的失败惩罚；
// 失败惩罚由 selector 层在 sortCandidates 中应用，此处仅做基础加权）。
// 单次遍历 O(n)，用于 SelectChain weighted 策略。
// 安全路由开启时跳过被排除节点；全部被排除时回退为不过滤。
func (m *Manager) PickWeightedNotIn(picked map[int64]bool) *model.ProxyNode {
	if n := m.pickWeightedNotIn(picked, true); n != nil {
		return n
	}
	return m.pickWeightedNotIn(picked, false)
}

func (m *Manager) pickWeightedNotIn(picked map[int64]bool, avoidUnsafe bool) *model.ProxyNode {
	m.mx.RLock()
	defer m.mx.RUnlock()
	type candidate struct {
		node   *model.ProxyNode
		weight float64
	}
	var candidates []candidate
	totalWeight := 0.0
	for _, n := range m.nodes {
		if n.Status != model.StatusAlive {
			continue
		}
		if picked[n.ID] {
			continue
		}
		if avoidUnsafe && m.excluded(n) {
			continue
		}
		// 基础权重：分数/延迟，避免除零
		lat := n.Latency
		if lat <= 0 {
			lat = 1
		}
		w := float64(n.Score) / float64(lat)
		candidates = append(candidates, candidate{node: n, weight: w})
		totalWeight += w
	}
	if len(candidates) == 0 {
		return nil
	}
	// 加权随机
	r := rand.Float64() * totalWeight
	for _, c := range candidates {
		r -= c.weight
		if r <= 0 {
			return cloneNode(c.node)
		}
	}
	// 浮点误差兜底：返回最后一个
	return cloneNode(candidates[len(candidates)-1].node)
}

// AliveIDs 返回所有存活节点的 ID 列表（不克隆节点、不排序）。
// 供 traffic.go 等仅需 ID 集合的场景使用，O(n) 单次遍历。
func (m *Manager) AliveIDs() []int64 {
	m.mx.RLock()
	defer m.mx.RUnlock()
	var ids []int64
	for id, n := range m.nodes {
		if n.Status == model.StatusAlive {
			ids = append(ids, id)
		}
	}
	return ids
}

// PickBestNotIn 返回评分最高且未在 picked 中的存活节点（单次遍历）。
func (m *Manager) PickBestNotIn(picked map[int64]bool) *model.ProxyNode {
	if n := m.pickBestNotIn(picked, true); n != nil {
		return n
	}
	return m.pickBestNotIn(picked, false)
}

func (m *Manager) pickBestNotIn(picked map[int64]bool, avoidUnsafe bool) *model.ProxyNode {
	m.mx.RLock()
	defer m.mx.RUnlock()
	var best *model.ProxyNode
	for _, n := range m.nodes {
		if n.Status != model.StatusAlive {
			continue
		}
		if picked[n.ID] {
			continue
		}
		if avoidUnsafe && m.excluded(n) {
			continue
		}
		if best == nil {
			best = n
			continue
		}
		if n.Score > best.Score {
			best = n
			continue
		}
		if n.Score == best.Score {
			if n.Latency < best.Latency {
				best = n
				continue
			}
			if n.Latency == best.Latency {
				if n.ID < best.ID {
					best = n
					continue
				}
				if n.ID == best.ID && n.Host < best.Host {
					best = n
				}
			}
		}
	}
	if best == nil {
		return nil
	}
	return cloneNode(best)
}

// PickRandomNotIn 随机返回一个未在 picked 中的存活节点（水塘采样）。
// 安全路由开启时跳过被排除节点；全部被排除时回退为不过滤。
func (m *Manager) PickRandomNotIn(picked map[int64]bool) *model.ProxyNode {
	if n := m.pickRandomNotIn(picked, true); n != nil {
		return n
	}
	return m.pickRandomNotIn(picked, false)
}

func (m *Manager) pickRandomNotIn(picked map[int64]bool, avoidUnsafe bool) *model.ProxyNode {
	m.mx.RLock()
	defer m.mx.RUnlock()
	var chosen *model.ProxyNode
	count := 0
	for _, n := range m.nodes {
		if n.Status != model.StatusAlive {
			continue
		}
		if picked[n.ID] {
			continue
		}
		if avoidUnsafe && m.excluded(n) {
			continue
		}
		count++
		if rand.Intn(count) == 0 {
			chosen = n
		}
	}
	if chosen == nil {
		return nil
	}
	return cloneNode(chosen)
}
// 用于选择器等热路径做 O(1) 按 ID 查找，避免整池克隆+排序。
func (m *Manager) Get(id int64) *model.ProxyNode {
	m.mx.RLock()
	defer m.mx.RUnlock()
	if n, ok := m.nodes[id]; ok {
		return cloneNode(n)
	}
	return nil
}

func (m *Manager) Count() int {
	m.mx.RLock()
	defer m.mx.RUnlock()
	return len(m.nodes)
}

func (m *Manager) CountAlive() int {
	m.mx.RLock()
	defer m.mx.RUnlock()
	c := 0
	for _, n := range m.nodes {
		if n.Status == model.StatusAlive {
			c++
		}
	}
	return c
}

// Remove deletes a node by id.
func (m *Manager) Remove(id int64) error {
	m.mx.Lock()
	delete(m.nodes, id)
	m.mx.Unlock()
	return m.store.DeleteNode(id)
}

// RemoveNodes removes nodes from the in-memory pool and storage.
func (m *Manager) RemoveNodes(ids []int64) {
	if len(ids) == 0 {
		return
	}
	m.mx.Lock()
	for _, id := range ids {
		delete(m.nodes, id)
	}
	m.mx.Unlock()
	for _, id := range ids {
		_ = m.store.DeleteNode(id)
	}
}

// Eliminate removes nodes whose consecutive failures exceed maxFailures.
// Returns the number of eliminated nodes.
func (m *Manager) Eliminate(maxFailures int) int {
	if maxFailures <= 0 {
		maxFailures = 3
	}
	m.mx.Lock()
	var victims []int64
	for id, n := range m.nodes {
		if n.FailCount >= maxFailures {
			victims = append(victims, id)
		}
	}
	for _, id := range victims {
		delete(m.nodes, id)
	}
	m.mx.Unlock()

	for _, id := range victims {
		if err := m.store.DeleteNode(id); err != nil {
			m.bus.Debug(fmt.Sprintf("eliminate node %d failed: %v", id, err))
		}
	}
	if len(victims) > 0 {
		m.bus.Info(fmt.Sprintf("eliminated %d dead nodes (failCount >= %d)", len(victims), maxFailures))
	}
	return len(victims)
}

// Pick roulette-selects a live node weighted by score/latency.
func (m *Manager) Pick() *model.ProxyNode {
	m.mx.RLock()
	alive := make([]*model.ProxyNode, 0, 4)
	for _, n := range m.nodes {
		if n.Status == model.StatusAlive && n.Score > 0 {
			alive = append(alive, cloneNode(n))
		}
	}
	m.mx.RUnlock()
	if len(alive) == 0 {
		return nil
	}
	var weights []float64
	for _, n := range alive {
		lat := n.Latency
		if lat <= 0 {
			lat = 1
		}
		w := float64(n.Score) / float64(lat)
		if w <= 0 {
			w = 0.01
		}
		weights = append(weights, w)
	}
	total := 0.0
	for _, w := range weights {
		total += w
	}
	roll := rand.Float64() * total
	for i, w := range weights {
		roll -= w
		if roll <= 0 {
			return alive[i]
		}
	}
	return alive[len(alive)-1]
}

// CheckNow runs a full validation pass with progress broadcasting.
func (m *Manager) CheckNow(ctx context.Context) error {
	if m.checking.Swap(true) {
		m.bus.Debug("check already running, skip")
		return nil
	}
	defer m.checking.Store(false)

	checkCtx := ctx
	if checkCtx == nil {
		checkCtx = context.Background()
	} else if err := checkCtx.Err(); err != nil {
		m.bus.Debug("check context already canceled, continuing with background context")
		checkCtx = context.Background()
	}

	return m.runChecks(checkCtx, m.List())
}

// CheckNodes 并发检测选中的节点子集（批量检测场景），支持进度广播；
// 与 CheckNow 共用 checking 互斥标志，避免同时运行两轮检测。
// ids 中不存在的节点会被跳过。
func (m *Manager) CheckNodes(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	if m.checking.Swap(true) {
		m.bus.Debug("check already running, skip")
		return nil
	}
	defer m.checking.Store(false)

	checkCtx := ctx
	if checkCtx == nil {
		checkCtx = context.Background()
	} else if err := checkCtx.Err(); err != nil {
		m.bus.Debug("check context already canceled, continuing with background context")
		checkCtx = context.Background()
	}

	// 从存储取节点副本，避免长时间持有池锁；不存在的 id 跳过。
	nodes := make([]*model.ProxyNode, 0, len(ids))
	for _, id := range ids {
		n, err := m.store.GetNode(id)
		if err != nil {
			continue
		}
		nodes = append(nodes, n)
	}
	return m.runChecks(checkCtx, nodes)
}

// runChecks 并发检测节点列表：sem 限流 + 进度广播 + 失败收敛。
func (m *Manager) runChecks(ctx context.Context, nodes []*model.ProxyNode) error {
	if len(nodes) == 0 {
		m.bus.Info("no nodes to check")
		return nil
	}
	m.bus.Info(fmt.Sprintf("starting check of %d nodes", len(nodes)))

	sem := make(chan struct{}, m.concur.Load())
	var done int64
	var wg sync.WaitGroup
	progressTicker := time.NewTicker(500 * time.Millisecond)
	defer progressTicker.Stop()
	progressDone := make(chan struct{})
	defer close(progressDone)
	go func() {
		for {
			select {
			case <-progressDone:
				return
			case <-progressTicker.C:
				doneCount := atomic.LoadInt64(&done)
				m.bus.Progress(int(doneCount), len(nodes))
			}
		}
	}()

	for _, n := range nodes {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case sem <- struct{}{}:
		}
		wg.Add(1)
		go func(node *model.ProxyNode) {
			defer wg.Done()
			defer func() { <-sem }()
			// 取消后已排队未启动的检查直接跳过，避免产生"幽灵检测"。
			select {
			case <-ctx.Done():
				return
			default:
			}
			m.evalOne(node)
			atomic.AddInt64(&done, 1)
		}(n)
	}
	wg.Wait()
	m.bus.Progress(len(nodes), len(nodes))
	alive := m.CountAlive()
	m.bus.Info(fmt.Sprintf("check finished: %d/%d alive", alive, len(nodes)))
	return nil
}

// CheckNode validates a single node immediately and updates state.
func (m *Manager) CheckNode(proxy *model.ProxyNode) model.CheckResult {
	return m.evalOne(proxy)
}

func (m *Manager) evalOne(node *model.ProxyNode) model.CheckResult {
	fresh := cloneNode(node)
	fresh.Status = model.StatusChecking
	m.mx.Lock()
	if live, ok := m.nodes[node.ID]; ok {
		live.Status = model.StatusChecking
	}
	m.mx.Unlock()

	result, err := (*m.checker.Load()).Check(fresh)
	if err != nil {
		// 检测器内部错误（非节点不可达）：保留结果并记录，避免静默吞掉诊断信息。
		m.bus.Debug(fmt.Sprintf("check node %s failed with error: %v", node.Key(), err))
		if result.Error == "" {
			result.Error = err.Error()
		}
	}
	// 连通性通过后做中间人探测（内部按节点节流），检出时写错误日志并持久化标记；
	// 必须在 CalculateScore 之前执行，保证本轮评分的安全分清零立即生效。
	if result.OK && m.mitmDetector.Load() != nil {
		m.probeMitm(fresh)
	}
	score := CalculateScore(fresh, result)

	status := model.StatusAlive
	if !result.OK {
		status = model.StatusDead
	}
	fresh.Status = status
	fresh.Latency = result.Latency
	fresh.Score = score.Score
	fresh.SafetyDetail = score.SafetyDetail
	fresh.LastCheck = time.Now()
	// 离线 GeoIP 解析节点地区（优先用连接安全探测到的代理出口 IP）。
	fresh.Country, fresh.Province, fresh.City = resolveGeo(fresh, result)
	if result.OK {
		fresh.SuccessCount++
	} else {
		fresh.FailCount++
	}

	if err := m.store.RecordCheckResult(
		node.ID, status, result.Latency, int64(score.Score), result.OK,
		fresh.Country, fresh.Province, fresh.City, result.Error); err != nil {
		m.bus.Debug(fmt.Sprintf("persist check failed: %v", err))
	}

	m.mx.Lock()
	if live, ok := m.nodes[node.ID]; ok {
		live.Status = fresh.Status
		live.Latency = fresh.Latency
		live.Score = fresh.Score
		live.SuccessCount = fresh.SuccessCount
		live.FailCount = fresh.FailCount
		live.LastCheck = fresh.LastCheck
		live.SafetyDetail = fresh.SafetyDetail
		live.Country = fresh.Country
		live.Province = fresh.Province
		live.City = fresh.City
		live.MitmDetected = fresh.MitmDetected
		live.MitmAt = fresh.MitmAt
	}
	m.mx.Unlock()

	if !result.OK {
		m.bus.Debug(fmt.Sprintf("node %s marked %s: %s",
			node.Key(), status, result.Error))
	}
	return result
}

// probeMitm 对节点执行一次 HTTPS 中间人探测并落库标记。
// 按节点节流（mitmRecheckInterval 内不重复探测）；检出时输出错误日志。
// 调用方保证仅在节点连通性检测通过后调用；n 为池内节点的副本，标记会写回副本。
func (m *Manager) probeMitm(n *model.ProxyNode) {
	if !n.MitmAt.IsZero() && time.Since(n.MitmAt) < mitmRecheckInterval {
		return
	}
	fn := m.mitmDetector.Load()
	if fn == nil {
		return
	}
	detected, detail := (*fn)(n)
	now := time.Now()
	n.MitmAt = now
	if detected && !n.MitmDetected {
		n.MitmDetected = true
		m.bus.Error(fmt.Sprintf("检测到 HTTPS 中间人：节点 %s 在访问目标站点时返回伪造证书（%s），"+
			"已标记为不安全节点，安全路由开启期间不再自动选用", n.Key(), detail))
	}
	if err := m.store.SetNodeMitm(n.ID, n.MitmDetected, now); err != nil {
		m.bus.Debug(fmt.Sprintf("persist mitm mark failed: %v", err))
	}
}

// RefreshLoop periodically validates nodes until ctx is cancelled.
// 周期通过 SetRefreshInterval 热更新，下一轮生效。
func (m *Manager) RefreshLoop(ctx context.Context) {
	for {
		interval := time.Duration(m.refreshInterval.Load())
		if interval <= 0 {
			interval = 15 * time.Minute
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			if err := m.CheckNow(ctx); err != nil {
				m.bus.Debug(fmt.Sprintf("periodic check interrupted: %v", err))
			}
			// auto-eliminate nodes that keep failing (Phase 3: 自动淘汰)
			m.Eliminate(3)
		}
	}
}

// resolveGeo 离线解析节点的代理地区（国家/省份/城市）。
// 优先级：
//  1. 连接安全探测成功的节点出口 IP（ProxiedIP）——最准确，代表真实代理所在地区；
//  2. 节点 host（若为 IP 直接查询，若为域名则本地 DNS 解析后查询）。
// 未命中（如 IPv6 节点、保留地址之外的查找失败）时返回三位空串。
func resolveGeo(node *model.ProxyNode, result model.CheckResult) (country, province, city string) {
	if a := result.Safety; a != nil && a.ProxiedIP != "" {
		if loc, ok := geoip.Lookup(a.ProxiedIP); ok {
			return loc.Country, loc.Province, loc.City
		}
	}
	if node.Host != "" {
		if loc, ok := geoip.LookupHost(node.Host); ok {
			return loc.Country, loc.Province, loc.City
		}
	}
	return "", "", ""
}

func cloneNode(n *model.ProxyNode) *model.ProxyNode {
	c := *n
	return &c
}
