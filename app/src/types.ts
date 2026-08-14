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
  currentHttpNode?: ProxyNode
  currentSocks5Node?: ProxyNode
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