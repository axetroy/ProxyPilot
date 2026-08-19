package collector

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/axetroy/ProxyPilot/proxy-core/bus"
	"github.com/axetroy/ProxyPilot/proxy-core/model"
	"github.com/axetroy/ProxyPilot/proxy-core/parser"
	"github.com/axetroy/ProxyPilot/proxy-core/storage"
)

// PoolSink receives newly collected nodes and removals.
type PoolSink interface {
	AddNodes(nodes []*model.ProxyNode) int
	RemoveNodes(ids []int64)
}

type Manager struct {
	store   *storage.Store
	bus     *bus.Bus
	sink    PoolSink
	fetcher *Fetcher

	mu         sync.Mutex
	timers     map[int64]*time.Timer
	refreshing map[int64]bool // 正在抓取中的订阅，防止定时刷新与手动刷新并发执行
	cancel     context.CancelFunc
}

func NewManager(store *storage.Store, bus *bus.Bus, sink PoolSink, fetchTimeout time.Duration) *Manager {
	return &Manager{
		store:      store,
		bus:        bus,
		sink:       sink,
		fetcher:    NewFetcher(fetchTimeout),
		timers:     make(map[int64]*time.Timer),
		refreshing: make(map[int64]bool),
	}
}

// AddSubscription persists a subscription and schedules periodic refresh.
func (m *Manager) AddSubscription(name, url string, interval int64) (*model.Subscription, error) {
	sub := &model.Subscription{
		Name:     name,
		URL:      url,
		Interval: interval,
		Enabled:  true,
	}
	if err := m.store.UpsertSubscription(sub); err != nil {
		return nil, err
	}
	m.bus.Info(fmt.Sprintf("subscription added: %s", name))
	if interval > 0 {
		m.schedule(sub)
	}
	return sub, nil
}

func (m *Manager) List() ([]*model.Subscription, error) {
	return m.store.ListSubscriptions()
}

// Get 按 ID 查询单个订阅（返回 nil 表示不存在）。
func (m *Manager) Get(id int64) (*model.Subscription, error) {
	return m.store.GetSubscription(id)
}

func (m *Manager) Delete(id int64) error {
	m.mu.Lock()
	if t, ok := m.timers[id]; ok {
		t.Stop()
		delete(m.timers, id)
	}
	m.mu.Unlock()
	proxyIDs, err := m.store.DeleteSubscription(id)
	if err != nil {
		return err
	}
	if m.sink != nil {
		m.sink.RemoveNodes(proxyIDs)
	}
	m.bus.Info(fmt.Sprintf("subscription removed: %d", id))
	return nil
}

// UpdateSubscription updates subscription details and reschedules if needed.
func (m *Manager) UpdateSubscription(id int64, name, url string, interval int64, enabled bool) error {
	// 查询旧状态，判断是否发生 启用→禁用 切换（禁用时需把该订阅节点移出代理池）
	prev, err := m.Get(id)
	if err != nil {
		return err
	}
	if prev == nil {
		return fmt.Errorf("subscription %d not found", id)
	}
	prevEnabled := prev.Enabled

	sub := &model.Subscription{ID: id, Name: name, URL: url, Interval: interval, Enabled: enabled}
	if err := m.store.UpsertSubscription(sub); err != nil {
		return err
	}
	// Adjust timer
	m.mu.Lock()
	if t, ok := m.timers[id]; ok {
		t.Stop()
		delete(m.timers, id)
	}
	m.mu.Unlock()
	// 禁用订阅：把该订阅下的节点移出代理池（解除关联，无其他订阅引用的删除）
	if prevEnabled && !enabled {
		if err := m.syncStaleNodes(id, nil); err != nil {
			return err
		}
	}
	if enabled && interval > 0 {
		m.schedule(sub)
	}
	return nil
}

// Run starts periodic refresh of all enabled subscriptions.
func (m *Manager) Run(ctx context.Context) error {
	subs, err := m.store.ListSubscriptions()
	if err != nil {
		return err
	}
	childCtx, cancel := context.WithCancel(ctx)
	m.mu.Lock()
	m.cancel = cancel
	m.mu.Unlock()
	for i := range subs {
		sub := subs[i]
		if sub.Enabled {
			m.scheduleInContext(childCtx, sub)
		}
	}
	return nil
}

func (m *Manager) Stop() {
	m.mu.Lock()
	if m.cancel != nil {
		m.cancel()
	}
	for _, t := range m.timers {
		t.Stop()
	}
	m.mu.Unlock()
}

// FetchResult 单次订阅抓取的结果摘要，供「立即抓取」反馈解析结果。
type FetchResult struct {
	Total int `json:"total"` // 本次解析出的节点总数
	Added int `json:"added"` // 相对池中已有的新增节点数
}

// FetchNow manually triggers a fetch of the given subscription.
func (m *Manager) FetchNow(ctx context.Context, sub *model.Subscription) (*FetchResult, error) {
	return m.refresh(ctx, sub)
}

func (m *Manager) schedule(sub *model.Subscription) {
	ctx := context.Background()
	m.scheduleInContext(ctx, sub)
}

func (m *Manager) scheduleInContext(ctx context.Context, sub *model.Subscription) {
	interval := time.Duration(sub.Interval) * time.Second
	if interval <= 0 {
		interval = time.Hour
	}
	timer := time.AfterFunc(interval, func() {
		if _, err := m.refresh(ctx, sub); err != nil {
			m.bus.Warn(fmt.Sprintf("refresh %s failed: %v", sub.Name, err))
		}
		select {
		case <-ctx.Done():
			return
		default:
			m.scheduleInContext(ctx, sub)
		}
	})
	m.mu.Lock()
	m.timers[sub.ID] = timer
	m.mu.Unlock()
}

func (m *Manager) refresh(ctx context.Context, sub *model.Subscription) (*FetchResult, error) {
	// 防重入：同一订阅的抓取未结束时，后续触发（定时器重叠、手动刷新）直接跳过，
	// 避免慢订阅在抓取期间被并发重复拉取。
	m.mu.Lock()
	if m.refreshing[sub.ID] {
		m.mu.Unlock()
		m.bus.Debug(fmt.Sprintf("subscription %s already refreshing, skip", sub.Name))
		return nil, nil
	}
	m.refreshing[sub.ID] = true
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.refreshing, sub.ID)
		m.mu.Unlock()
	}()

	m.bus.Info(fmt.Sprintf("fetching subscription %s", sub.Name))
	body, err := m.fetcher.Fetch(ctx, sub.URL)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrFetch, err)
	}
	nodes := parser.ParseSubscriptionBody(body)
	if len(nodes) == 0 {
		m.bus.Warn(fmt.Sprintf("subscription %s returned no nodes", sub.Name))
	}
	for _, node := range nodes {
		node.SubscriptionID = sub.ID
	}
	added := 0
	if m.sink != nil {
		added = m.sink.AddNodes(nodes)
		m.bus.Info(fmt.Sprintf("subscription %s: %d nodes (new %d)", sub.Name, len(nodes), added))
		// 同步清理：移除该订阅下已不在本次抓取结果中的旧节点，
		// 避免订阅节点减少或变化时代理数只增不减
		if err := m.syncStaleNodes(sub.ID, nodes); err != nil {
			m.bus.Warn(fmt.Sprintf("subscription %s cleanup failed: %v", sub.Name, err))
		}
	}
	if err := m.store.UpdateSubscriptionFetch(sub.ID, time.Now()); err != nil {
		return nil, err
	}
	sub.LastFetch = time.Now()
	return &FetchResult{Total: len(nodes), Added: added}, nil
}

// syncStaleNodes 找出该订阅下已不在本次抓取结果中的旧节点并清理。
// 节点可能被多个订阅共享：先解除与当前订阅的关联，
// 若不再被任何订阅引用则从池中删除。
func (m *Manager) syncStaleNodes(subID int64, nodes []*model.ProxyNode) error {
	existing, err := m.store.ListNodesBySubscription(subID)
	if err != nil {
		return err
	}
	if len(existing) == 0 {
		return nil
	}
	keep := make(map[string]struct{}, len(nodes))
	for _, n := range nodes {
		keep[strings.ToLower(n.Key())] = struct{}{}
	}
	var removed []int64
	for _, n := range existing {
		if _, ok := keep[strings.ToLower(n.Key())]; ok {
			continue
		}
		// 解除与当前订阅的关联
		if err := m.store.DetachNodeFromSubscription(n.ID, subID); err != nil {
			m.bus.Debug(fmt.Sprintf("detach node %d failed: %v", n.ID, err))
			continue
		}
		// 若不再被其他订阅引用，则从池中删除
		refs, err := m.store.CountSubscriptionRefs(n.ID)
		if err != nil {
			m.bus.Debug(fmt.Sprintf("count refs failed: %v", err))
			continue
		}
		if refs == 0 {
			removed = append(removed, n.ID)
		}
	}
	if len(removed) > 0 {
		m.sink.RemoveNodes(removed)
		m.bus.Info(fmt.Sprintf("subscription %d: removed %d stale nodes", subID, len(removed)))
	}
	return nil
}
