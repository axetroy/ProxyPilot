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

export interface CheckResult {
  ok: boolean
  latency: number
  error?: string
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