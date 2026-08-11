package pool

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"github.com/axetroy/ProxyPilot/proxy-core/bus"
	"github.com/axetroy/ProxyPilot/proxy-core/model"
	"github.com/axetroy/ProxyPilot/proxy-core/storage"
)

type nodeChecker interface {
	Check(node *model.ProxyNode) (model.CheckResult, error)
}

type Manager struct {
	store    *storage.Store
	checker  atomic.Pointer[nodeChecker]
	bus      *bus.Bus
	mx       sync.RWMutex
	nodes    map[int64]*model.ProxyNode
	concur   atomic.Int32
	checking atomic.Bool

	// refreshInterval 单位纳秒，RefreshLoop 每轮读取，支持热更新
	refreshInterval atomic.Int64
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
func (m *Manager) List() []*model.ProxyNode {
	m.mx.RLock()
	defer m.mx.RUnlock()
	out := make([]*model.ProxyNode, 0, len(m.nodes))
	for _, n := range m.nodes {
		out = append(out, cloneNode(n))
	}
	return out
}

// Alive returns nodes currently marked alive.
func (m *Manager) Alive() []*model.ProxyNode {
	m.mx.RLock()
	defer m.mx.RUnlock()
	var out []*model.ProxyNode
	for _, n := range m.nodes {
		if n.Status == model.StatusAlive {
			out = append(out, cloneNode(n))
		}
	}
	return out
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

// PickRandom returns an arbitrary live node.
func (m *Manager) PickRandom() *model.ProxyNode {
	alive := m.Alive()
	if len(alive) == 0 {
		return nil
	}
	return alive[rand.Intn(len(alive))]
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

	nodes := m.List()
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
	go func() {
		for range progressTicker.C {
			doneCount := atomic.LoadInt64(&done)
			m.bus.Progress(int(doneCount), len(nodes))
		}
	}()

	for _, n := range nodes {
		select {
		case <-checkCtx.Done():
			return checkCtx.Err()
		case sem <- struct{}{}:
		}
		wg.Add(1)
		go func(node *model.ProxyNode) {
			defer wg.Done()
			defer func() { <-sem }()
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

	result, _ := (*m.checker.Load()).Check(fresh)
	score := CalculateScore(fresh, result)

	status := model.StatusAlive
	if !result.OK {
		status = model.StatusDead
	}
	fresh.Status = status
	fresh.Latency = result.Latency
	fresh.Score = score.Score
	fresh.LastCheck = time.Now()
	if result.OK {
		fresh.SuccessCount++
	} else {
		fresh.FailCount++
	}

	if err := m.store.UpdateNodeCheck(node.ID, status, result.Latency, int64(score.Score), result.OK); err != nil {
		m.bus.Debug(fmt.Sprintf("persist check failed: %v", err))
	}
	if err := m.store.AddCheckHistory(model.CheckHistory{
		ProxyID: node.ID,
		Success: result.OK,
		Latency: result.Latency,
		Error:   result.Error,
	}); err != nil {
		m.bus.Debug(fmt.Sprintf("history write failed: %v", err))
	}

	m.mx.Lock()
	if live, ok := m.nodes[node.ID]; ok {
		live.Status = fresh.Status
		live.Latency = fresh.Latency
		live.Score = fresh.Score
		live.SuccessCount = fresh.SuccessCount
		live.FailCount = fresh.FailCount
		live.LastCheck = fresh.LastCheck
	}
	m.mx.Unlock()

	if !result.OK {
		m.bus.Debug(fmt.Sprintf("node %s marked %s: %s",
			node.Key(), status, result.Error))
	}
	return result
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

func cloneNode(n *model.ProxyNode) *model.ProxyNode {
	c := *n
	return &c
}
