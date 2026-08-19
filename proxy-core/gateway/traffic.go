package gateway

import (
	"net"
	"sort"
	"sync"
)

// autoChainTrafficName 是 auto-chain 策略在流量统计中的固定链路名。
// 自动链路每次请求现选节点、无固定名称，统一记到该桶下。
const autoChainTrafficName = "auto-chain"

// trafficKind 标识流量归属的出口维度。
type trafficKind int

const (
	// trafficNode 单跳节点出口（best/weighted/random/round-robin/fixed）。
	trafficNode trafficKind = iota
	// trafficChain 链路出口（chain 与 auto-chain，按链路名分桶）。
	trafficChain
	// trafficDirect 智能分流命中「直连」的目标。
	trafficDirect
)

// trafficEntry 记录某个出口累计的流量与连接数。
type trafficEntry struct {
	Upload      int64 `json:"upload"`
	Download    int64 `json:"download"`
	Connections int64 `json:"connections"`
}

// nodeTrafficItem 单个节点的流量统计。
type nodeTrafficItem struct {
	ID int64 `json:"id"`
	trafficEntry
}

// chainTrafficItem 单个链路的流量统计。
type chainTrafficItem struct {
	Name string `json:"name"`
	trafficEntry
}

// TrafficSnapshot 是当前流量统计的只读快照（本次启动累计）。
type TrafficSnapshot struct {
	Total   trafficEntry       `json:"total"`
	Direct  trafficEntry       `json:"direct"`
	ByNode  []nodeTrafficItem  `json:"byNode"`
	ByChain []chainTrafficItem `json:"byChain"`
}

// trafficCounter 统计网关转发的流量与连接数，按出口维度分桶。
// 仅内存累计（本次启动至今），进程重启后清零，不做持久化。
type trafficCounter struct {
	mu      sync.Mutex
	byNode  map[int64]*trafficEntry
	byChain map[string]*trafficEntry
	direct  trafficEntry
	total   trafficEntry
}

func newTrafficCounter() *trafficCounter {
	return &trafficCounter{
		byNode:  make(map[int64]*trafficEntry),
		byChain: make(map[string]*trafficEntry),
	}
}

// entry 返回指定出口桶的计数项（不存在时创建）。
func (c *trafficCounter) entry(kind trafficKind, nodeID int64, chainName string) *trafficEntry {
	switch kind {
	case trafficNode:
		e := c.byNode[nodeID]
		if e == nil {
			e = &trafficEntry{}
			c.byNode[nodeID] = e
		}
		return e
	case trafficChain:
		e := c.byChain[chainName]
		if e == nil {
			e = &trafficEntry{}
			c.byChain[chainName] = e
		}
		return e
	default:
		return &c.direct
	}
}

// open 记录一条新连接（连接数 +1）。
func (c *trafficCounter) open(kind trafficKind, nodeID int64, chainName string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.total.Connections++
	c.entry(kind, nodeID, chainName).Connections++
}

// add 累加一条连接的流量字节（upload/download 分别计数）。
func (c *trafficCounter) add(kind trafficKind, nodeID int64, chainName string, upload, download int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.total.Upload += upload
	c.total.Download += download
	e := c.entry(kind, nodeID, chainName)
	e.Upload += upload
	e.Download += download
}

// countedConn 包装一条出站连接：Read/Write 时把字节计入对应出口桶。
// 调用方（relay / io.Copy / http.Transport）对返回连接的读写会自动计数。
type countedConn struct {
	net.Conn
	c         *trafficCounter
	kind      trafficKind
	nodeID    int64
	chainName string
}

func (cc *countedConn) Read(p []byte) (int, error) {
	n, err := cc.Conn.Read(p)
	if n > 0 {
		cc.c.add(cc.kind, cc.nodeID, cc.chainName, 0, int64(n))
	}
	return n, err
}

func (cc *countedConn) Write(p []byte) (int, error) {
	n, err := cc.Conn.Write(p)
	if n > 0 {
		cc.c.add(cc.kind, cc.nodeID, cc.chainName, int64(n), 0)
	}
	return n, err
}

// TrackConn 把一条出站连接纳入流量统计并返回计数包装。
// 调用方后续的 Read/Write 字节会自动累加到对应出口桶，连接数在建连时 +1。
func (g *Gateway) TrackConn(conn net.Conn, kind trafficKind, nodeID int64, chainName string) net.Conn {
	if g.traffic == nil {
		return conn
	}
	g.traffic.open(kind, nodeID, chainName)
	return &countedConn{Conn: conn, c: g.traffic, kind: kind, nodeID: nodeID, chainName: chainName}
}

// prune 剔除已不存在的出口（节点已删除/链路已删除）的统计条目，避免长期残留。
// validNodeIDs / validChainNames 为当前存活集合，nil 表示保留全部。
func (c *trafficCounter) prune(validNodeIDs map[int64]struct{}, validChainNames map[string]struct{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if validNodeIDs != nil {
		for id := range c.byNode {
			if _, ok := validNodeIDs[id]; !ok {
				delete(c.byNode, id)
			}
		}
	}
	if validChainNames != nil {
		for name := range c.byChain {
			if _, ok := validChainNames[name]; !ok {
				delete(c.byChain, name)
			}
		}
	}
}

// Traffic 返回流量统计快照（本次启动累计）。
// 顺带清理已删除节点/链路的残留统计条目，避免 map 无限增长。
func (g *Gateway) Traffic() TrafficSnapshot {
	if g.traffic == nil {
		return TrafficSnapshot{}
	}
	if g.pool != nil {
		nodes := g.pool.List()
		validNodes := make(map[int64]struct{}, len(nodes))
		for _, n := range nodes {
			validNodes[n.ID] = struct{}{}
		}
		validChains := map[string]struct{}{autoChainTrafficName: {}}
		if g.chainsProvider != nil {
			if chains, err := g.chainsProvider(); err == nil {
				for _, ch := range chains {
					validChains[ch.Name] = struct{}{}
				}
			}
		}
		g.traffic.prune(validNodes, validChains)
	}
	g.traffic.mu.Lock()
	defer g.traffic.mu.Unlock()
	snap := TrafficSnapshot{
		Total:   g.traffic.total,
		Direct:  g.traffic.direct,
		ByNode:  make([]nodeTrafficItem, 0),
		ByChain: make([]chainTrafficItem, 0),
	}
	for id, e := range g.traffic.byNode {
		snap.ByNode = append(snap.ByNode, nodeTrafficItem{ID: id, trafficEntry: *e})
	}
	sort.Slice(snap.ByNode, func(i, j int) bool { return snap.ByNode[i].ID < snap.ByNode[j].ID })
	for name, e := range g.traffic.byChain {
		snap.ByChain = append(snap.ByChain, chainTrafficItem{Name: name, trafficEntry: *e})
	}
	sort.Slice(snap.ByChain, func(i, j int) bool { return snap.ByChain[i].Name < snap.ByChain[j].Name })
	return snap
}
