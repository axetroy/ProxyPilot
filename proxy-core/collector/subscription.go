package collector

import (
	"context"
	"fmt"
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

	mu     sync.Mutex
	timers map[int64]*time.Timer
	cancel context.CancelFunc
}

func NewManager(store *storage.Store, bus *bus.Bus, sink PoolSink, fetchTimeout time.Duration) *Manager {
	return &Manager{
		store:   store,
		bus:     bus,
		sink:    sink,
		fetcher: NewFetcher(fetchTimeout),
		timers:  make(map[int64]*time.Timer),
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

// FetchNow manually triggers a fetch of the given subscription.
func (m *Manager) FetchNow(ctx context.Context, sub *model.Subscription) error {
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
		if err := m.refresh(ctx, sub); err != nil {
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

func (m *Manager) refresh(ctx context.Context, sub *model.Subscription) error {
	m.bus.Info(fmt.Sprintf("fetching subscription %s from %s", sub.Name, sub.URL))
	body, err := m.fetcher.Fetch(ctx, sub.URL)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrFetch, err)
	}
	nodes := parser.ParseSubscriptionBody(body)
	if len(nodes) == 0 {
		m.bus.Warn(fmt.Sprintf("subscription %s returned no nodes", sub.Name))
	}
	for _, node := range nodes {
		node.SubscriptionID = sub.ID
	}
	if m.sink != nil {
		added := m.sink.AddNodes(nodes)
		m.bus.Info(fmt.Sprintf("subscription %s: %d nodes (new %d)", sub.Name, len(nodes), added))
	}
	if err := m.store.UpdateSubscriptionFetch(sub.ID, time.Now()); err != nil {
		return err
	}
	sub.LastFetch = time.Now()
	return nil
}
