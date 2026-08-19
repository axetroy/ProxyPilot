export interface ProxyNode {
  id: number
  host: string
  port: number
  protocol: 'http' | 'https' | 'socks5'
  username?: string
  password?: string
  latency: number
  score: number
  status: 'new' | 'checking' | 'alive' | 'dead'
  country?: string
  province?: string
  city?: string
  successCount: number
  failCount: number
  lastCheck: string
  createdAt: string
  updatedAt: string
  subscriptionId?: number
  scoreBreakdown?: ScoreBreakdown
  anonymityDetail?: AnonymityDetail
}

/** 匿名性检测明细，与后端 model.AnonymityDetail 对应 */
export interface AnonymityDetail {
  /** 源 IP 是否隐藏：true=代理出口与直连不同；false=透明；缺省=无法对比 */
  sourceIpHidden?: boolean
  /** 泄漏的真实客户端信息头（如 X-Forwarded-For） */
  headerLeaks?: string[]
  /** 暴露代理身份的特征头（如 Via / Proxy-Agent） */
  proxyMarkers?: string[]
  /** 出口 IP 是否轮换（两次经代理采样结果不同，轮换代理匿名性更好） */
  rotatingIp?: boolean
  /** 连接信息问题（请求被代理改写，如回显 URL/Host 与目标不一致） */
  reqIssues?: string[]
  /** 匿名性子评分（0-100） */
  score: number
}

/** 评分明细，与后端 pool.Breakdown 返回结构对应 */
export interface ScoreBreakdown {
  successRate: number
  latencyScore: number
  stability: number
  anonymity: number
  weightSuccess: number
  weightLatency: number
  weightStability: number
  weightAnonymity: number
  score: number
}

export interface Subscription {
  id: number
  name: string
  url: string
  interval: number
  enabled: boolean
  lastFetch: string
  createdAt: string
  proxyCount?: number
}

export interface SystemStatus {
  running: boolean
  proxyCount: number
  aliveCount: number
  currentIP: string
  currentNode?: ProxyNode
  /** 用户指定的固定出口节点（未指定或节点已删除时为 undefined） */
  pinnedNode?: ProxyNode
  httpProxyBind: string
  socks5ProxyBind: string
  version: string
}

/** proxy-core 可在前端配置的项 */
export interface SettingItem {
  key: string
  value: string
  default: string
  desc: string
}

export interface UpdateSettingsResult {
  changed: boolean
  settings: SettingItem[]
}

/** 订阅导出配置（GET/PUT /api/subscription） */
export interface SubscriptionExportConfig {
  enabled: boolean
  listen: string
  host: string
  lanIPs: string[]
  token: string
  url: string
}

/** 数据库状态（GET /api/db/status） */
export interface DbStatus {
  dbSize: number
  historyCount: number
  purgeable: number
  retentionDays: number
}

/** 手动瘦身数据库的结果（POST /api/db/compact） */
export interface CompactResult {
  deleted: number
  sizeBefore: number
  sizeAfter: number
  historyCount: number
}

/** 智能分流配置与规则同步状态（GET/PUT /api/pac-config） */
export interface PacConfig {
  enabled: boolean
  mode: 'whitelist' | 'blacklist'
  directUrls: string
  proxyUrls: string
  refresh: string
  directCount: number
  proxyCount: number
  syncAt?: string
  syncError?: string
  syncing: boolean
}

/** 网关出口策略 */
export type EgressStrategy = 'fixed' | 'best' | 'random' | 'weighted' | 'round-robin' | 'chain' | 'auto-chain'

/** 自动链路每层可用的选择策略 */
export type ChainSelection = 'weighted' | 'random' | 'best'

/** 出口策略描述（后端 scheduler.StrategyMeta） */
export interface EgressStrategyMeta {
  value: EgressStrategy
  label: string
  desc: string
}

/** 出口路由配置（GET/PUT /api/egress） */
export interface EgressConfig {
  strategy: EgressStrategy
  pinnedNode?: ProxyNode
  aliveCount: number
  strategies: EgressStrategyMeta[]
  /** 自动链路（auto-chain）层数与每层选择策略 */
  chainHops: number
  chainSelection: ChainSelection
}

/** 某个出口（节点/链路/直连/总量）的累计流量统计（本次启动至今） */
export interface TrafficEntry {
  upload: number
  download: number
  connections: number
}

/** 按节点分桶的流量统计 */
export interface NodeTrafficItem extends TrafficEntry {
  id: number
}

/** 按链路分桶的流量统计（auto-chain 统一记为该名） */
export interface ChainTrafficItem extends TrafficEntry {
  name: string
}

/** 网关流量统计快照（GET /api/traffic） */
export interface TrafficSnapshot {
  total: TrafficEntry
  direct: TrafficEntry
  byNode: NodeTrafficItem[]
  byChain: ChainTrafficItem[]
}

/** 代理链路（客户端按序经过多个节点到达目标） */
export interface ProxyChain {
  id: number
  name: string
  nodeIds: number[]
  enabled: boolean
  createdAt: string
  updatedAt: string
}

/** 链路测试中某一跳（节点）的测试结果 */
export interface ChainHopResult {
  hop: number
  nodeId: number
  key: string
  protocol: string
  ok: boolean
  latency: number
  error?: string
}

/** 一次链路测试的整体结果 */
export interface ChainTestResult {
  ok: boolean
  totalLatency: number
  hops: ChainHopResult[]
}

export interface CheckResult {
  ok: boolean
  latency: number
  error?: string
}

/** 应用级设置（Electron 主进程持久化到 userData/settings.json） */
export interface AppSettings {
  closeBehavior: 'minimize' | 'quit'
  /** 自动检查并下载更新（默认开启） */
  autoUpdate: boolean
  /** 开机自动启动（默认关闭） */
  autoLaunch: boolean
  /** 系统代理开关与备份（关闭或退出时按备份还原原设置） */
  systemProxy?: {
    enabled: boolean
    endpoint?: string
    backup?: unknown
  }
}

/** 系统代理开关状态（与主进程 system-proxy.ts 对应） */
export interface SystemProxyState {
  enabled: boolean
  /** 系统代理目标地址（网关 HTTP 入口），如 127.0.0.1:7892 */
  endpoint?: string
  error?: string
  changedAt?: number
}

/** 更新流程状态（与主进程 updater.ts 的 UpdaterStatus 对应） */
export type UpdaterStatus =
  | 'idle'
  | 'checking'
  | 'available'
  | 'not-available'
  | 'downloading'
  | 'downloaded'
  | 'error'
  | 'dev'

export interface UpdaterProgress {
  percent: number
  transferred: number
  total: number
  bytesPerSecond: number
}

export interface UpdaterState {
  /** 自动更新开关（默认开启） */
  enabled: boolean
  status: UpdaterStatus
  /** 触发来源：auto=启动/重新开启自动更新，manual=设置页手动检查 */
  source: 'auto' | 'manual'
  currentVersion: string
  latestVersion?: string
  progress?: UpdaterProgress
  error?: string
  checkedAt?: number
}

export interface ApiResponse<T = unknown> {
  code: number
  msg: string
  data: T
}

export interface LogEvent {
  type: 'log' | 'progress'
  level?: 'debug' | 'info' | 'warn' | 'error'
  message?: string
  current?: number
  total?: number
}