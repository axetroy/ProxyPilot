import axios, { type AxiosInstance } from 'axios'
import type { ApiResponse, AppSettings, CheckResult, CompactResult, DbStatus, EgressConfig, EgressStrategy, LogEvent, PacConfig, ProxyNode, SettingItem, Subscription, SubscriptionExportConfig, SystemStatus, SystemProxyState, UpdateSettingsResult, UpdaterState } from '@/types'

// 默认 API 地址；Electron 环境由主进程告知实际地址（API 端口可能顺延）。
let API_BASE = 'http://127.0.0.1:17890'

export function getApiBaseUrl(): string {
  return API_BASE
}

let token = ''
let apiReadyPromise: Promise<void> | null = null
let resolveApiReady: (() => void) | null = null

declare global {
  interface Window {
    proxypilot?: {
      getToken: () => Promise<string>
      getApiBase: () => Promise<string>
      getPlatform: () => Promise<string>
      getAppSettings: () => Promise<AppSettings>
      setAppSettings: (settings: AppSettings) => Promise<AppSettings>
      // ---- 更新机制 ----
      getUpdaterState: () => Promise<UpdaterState>
      checkForUpdates: () => Promise<UpdaterState>
      setAutoUpdate: (enabled: boolean) => Promise<UpdaterState>
      installUpdate: () => Promise<void>
      onUpdaterEvent: (cb: (state: UpdaterState) => void) => () => void
      // ---- 系统代理 ----
      getSystemProxyState: () => Promise<SystemProxyState>
      setSystemProxy: (enabled: boolean) => Promise<SystemProxyState>
      onSystemProxyEvent: (cb: (state: SystemProxyState) => void) => () => void
      onCoreExit: (cb: () => void) => void
      onCoreError: (cb: (msg: string) => void) => void
    }
  }
}

export async function initApi(): Promise<void> {
  if (!apiReadyPromise) {
    apiReadyPromise = new Promise<void>((resolve) => {
      resolveApiReady = resolve
    })
  }
  if (window.proxypilot) {
    token = await window.proxypilot.getToken()
    const base = await window.proxypilot.getApiBase()
    if (base) {
      API_BASE = base
      // axios 实例创建时的 baseURL 是默认值，这里按实际地址更新
      http.defaults.baseURL = base
    }
  }
  resolveApiReady?.()
  resolveApiReady = null
}

async function refreshToken(): Promise<void> {
  if (window.proxypilot) {
    token = await window.proxypilot.getToken()
  }
}

export async function ensureApiReady(): Promise<void> {
  if (!apiReadyPromise) {
    await initApi()
    return
  }
  await apiReadyPromise
  await refreshToken()
}

export function getToken(): string {
  return token
}

export type Platform = 'win32' | 'darwin' | 'linux'

export async function getPlatform(): Promise<Platform> {
  if (window.proxypilot) {
    const p = await window.proxypilot.getPlatform()
    if (p === 'win32' || p === 'darwin' || p === 'linux') return p
  }
  // 浏览器环境兜底：根据 UA 推断
  const ua = navigator.userAgent
  if (/Windows/i.test(ua)) return 'win32'
  if (/Macintosh|Mac OS X/i.test(ua)) return 'darwin'
  return 'linux'
}

export function getErrorMessage(error: unknown): string {
  if (axios.isAxiosError(error)) {
    const data = error.response?.data as { msg?: string; error?: string } | string | undefined
    if (typeof data === 'string' && data) return data
    if (data && typeof data === 'object') {
      if (typeof data.msg === 'string' && data.msg) return data.msg
      if (typeof data.error === 'string' && data.error) return data.error
    }
    if (error.message) return error.message
  }

  if (error && typeof error === 'object') {
    const maybe = error as { message?: string; response?: { data?: { msg?: string; error?: string } } }
    if (typeof maybe.message === 'string' && maybe.message) return maybe.message
    if (maybe.response?.data && typeof maybe.response.data === 'object') {
      if (typeof maybe.response.data.msg === 'string' && maybe.response.data.msg) return maybe.response.data.msg
      if (typeof maybe.response.data.error === 'string' && maybe.response.data.error) return maybe.response.data.error
    }
  }

  return '请求失败'
}

export const http: AxiosInstance = axios.create({
  baseURL: API_BASE,
  timeout: 15000,
})

http.interceptors.request.use(async (config) => {
  await ensureApiReady()
  config.headers['X-Token'] = token
  return config
})

export async function getStatus(): Promise<ApiResponse<SystemStatus>> {
  await ensureApiReady()
  const { data } = await http.get<ApiResponse<SystemStatus>>('/api/status')
  return data
}

export async function listProxies(status?: string): Promise<ApiResponse<ProxyNode[]>> {
  await ensureApiReady()
  const { data } = await http.get<ApiResponse<ProxyNode[]>>('/api/proxies', {
    params: status ? { status } : {},
  })
  return data
}

export async function deleteProxy(id: number): Promise<ApiResponse<void>> {
  await ensureApiReady()
  const { data } = await http.delete<ApiResponse<void>>(`/api/proxy/${id}`)
  return data
}

/** 指定某个节点为固定出口（不再按评分自动选择） */
export async function pinProxy(id: number): Promise<ApiResponse<ProxyNode>> {
  await ensureApiReady()
  const { data } = await http.put<ApiResponse<ProxyNode>>('/api/proxy/pin', { id })
  return data
}

/** 取消固定出口指定，恢复按评分自动选择 */
export async function unpinProxy(): Promise<ApiResponse<void>> {
  await ensureApiReady()
  const { data } = await http.delete<ApiResponse<void>>('/api/proxy/pin')
  return data
}

export async function checkProxy(id?: number): Promise<ApiResponse<CheckResult | { started: boolean }>> {
  await ensureApiReady()
  const { data } = await http.post<ApiResponse<CheckResult | { started: boolean }>>(
    '/api/proxy/check',
    id ? { id } : {},
  )
  return data
}

export async function listSubscriptions(): Promise<ApiResponse<Subscription[]>> {
  await ensureApiReady()
  const { data } = await http.get<ApiResponse<Subscription[]>>('/api/subscriptions')
  return data
}

export async function addSubscription(name: string, url: string, interval: number): Promise<ApiResponse<Subscription>> {
  await ensureApiReady()
  const { data } = await http.post<ApiResponse<Subscription>>('/api/subscription', { name, url, interval })
  return data
}

export async function deleteSubscription(id: number): Promise<ApiResponse<void>> {
  await ensureApiReady()
  const { data } = await http.delete<ApiResponse<void>>(`/api/subscription/${id}`)
  return data
}

export async function refreshSubscription(id: number): Promise<ApiResponse<Subscription>> {
  await ensureApiReady()
  const { data } = await http.post<ApiResponse<Subscription>>(`/api/subscription/${id}/refresh`)
  return data
}

export async function updateSubscription(id: number, name: string, url: string, interval: number, enabled: boolean): Promise<ApiResponse<void>> {
  await ensureApiReady()
  const { data } = await http.put<ApiResponse<void>>(`/api/subscription/${id}`, { name, url, interval, enabled })
  return data
}

export async function startGateway(): Promise<ApiResponse<{ http: string; socks5: string }>> {
  await ensureApiReady()
  const { data } = await http.post<ApiResponse<{ http: string; socks5: string }>>('/api/gateway/start')
  return data
}

export async function stopGateway(): Promise<ApiResponse<void>> {
  await ensureApiReady()
  const { data } = await http.post<ApiResponse<void>>('/api/gateway/stop')
  return data
}

export async function listSettings(): Promise<ApiResponse<SettingItem[]>> {
  await ensureApiReady()
  const { data } = await http.get<ApiResponse<SettingItem[]>>('/api/settings')
  return data
}

export async function updateSettings(updates: Record<string, string>): Promise<ApiResponse<UpdateSettingsResult>> {
  await ensureApiReady()
  const { data } = await http.put<ApiResponse<UpdateSettingsResult>>('/api/settings', updates)
  return data
}

export async function getSubscriptionConfig(): Promise<ApiResponse<SubscriptionExportConfig>> {
  await ensureApiReady()
  const { data } = await http.get<ApiResponse<SubscriptionExportConfig>>('/api/subscription')
  return data
}

/** 查询数据库状态（大小 / 检测历史 / 可清理条数） */
export async function getDbStatus(): Promise<ApiResponse<DbStatus>> {
  await ensureApiReady()
  const { data } = await http.get<ApiResponse<DbStatus>>('/api/db/status')
  return data
}

/** 手动瘦身数据库：清理过期检测历史并 VACUUM 收缩文件 */
export async function compactDb(): Promise<ApiResponse<CompactResult>> {
  await ensureApiReady()
  const { data } = await http.post<ApiResponse<CompactResult>>('/api/db/compact')
  return data
}

export async function updateSubscriptionConfig(patch: {
  enabled?: boolean
  listen?: string
  host?: string
  resetToken?: boolean
}): Promise<ApiResponse<SubscriptionExportConfig>> {
  await ensureApiReady()
  const { data } = await http.put<ApiResponse<SubscriptionExportConfig>>('/api/subscription', patch)
  return data
}

/** 获取智能分流配置与规则同步状态 */
export async function getPacConfig(): Promise<ApiResponse<PacConfig>> {
  await ensureApiReady()
  const { data } = await http.get<ApiResponse<PacConfig>>('/api/pac-config')
  return data
}

/** 更新智能分流配置（仅传需要修改的字段） */
export async function updatePacConfig(patch: Partial<Omit<PacConfig, 'directCount' | 'proxyCount' | 'syncAt' | 'syncError' | 'syncing'>>): Promise<ApiResponse<PacConfig>> {
  await ensureApiReady()
  const { data } = await http.put<ApiResponse<PacConfig>>('/api/pac-config', patch)
  return data
}

/** 手动触发分流规则同步 */
export async function syncPacRules(): Promise<ApiResponse<PacConfig>> {
  await ensureApiReady()
  const { data } = await http.post<ApiResponse<PacConfig>>('/api/pac/sync')
  return data
}

/** 获取出口路由配置（当前策略 / 固定节点 / 存活数 / 可选策略） */
export async function getEgressConfig(): Promise<ApiResponse<EgressConfig>> {
  await ensureApiReady()
  const { data } = await http.get<ApiResponse<EgressConfig>>('/api/egress')
  return data
}

/** 切换出口策略；固定策略可同时指定节点（pinId） */
export async function updateEgressConfig(patch: { strategy: EgressStrategy; pinId?: number }): Promise<ApiResponse<EgressConfig>> {
  await ensureApiReady()
  const { data } = await http.put<ApiResponse<EgressConfig>>('/api/egress', patch)
  return data
}

/** 导出存活节点：format 为 json（默认）/ base64 / plain，文本格式直接返回内容 */
export async function exportProxies(format: 'json' | 'base64' | 'plain' = 'json'): Promise<ApiResponse<unknown>> {
  await ensureApiReady()
  if (format === 'json') {
    const { data } = await http.get<ApiResponse<unknown>>('/api/export')
    return data
  }
  const { data } = await http.get<{ code: number; msg: string; data?: unknown }>(`/api/export?format=${format}`, {
    responseType: 'text',
  })
  // 文本格式的响应体即订阅内容，这里包一层以便调用方统一处理
  return { code: data.code ?? 0, msg: data.msg ?? 'ok', data: typeof data === 'string' ? data : (data as unknown as { data?: unknown })?.data }
}

export function connectLogStream(onEvent: (e: LogEvent) => void): () => void {
  let ws: WebSocket | null = null
  let closed = false
  let retryTimer: number | null = null
  let retryDelay = 1000

  const connect = () => {
    if (closed) return
    const wsBase = API_BASE.replace(/^http/, 'ws')
    ws = new WebSocket(`${wsBase}/ws?token=${encodeURIComponent(token)}`)
    ws.onmessage = (msg) => {
      try {
        onEvent(JSON.parse(msg.data as string) as LogEvent)
      } catch {
        /* ignore malformed */
      }
    }
    // 连接断开后自动重连，避免长时间运行后日志流静默中断。
    ws.onclose = () => {
      if (closed) return
      retryTimer = window.setTimeout(connect, retryDelay)
      retryDelay = Math.min(retryDelay * 2, 15000)
    }
    ws.onerror = () => {
      ws?.close()
    }
  }

  connect()

  return () => {
    closed = true
    if (retryTimer !== null) window.clearTimeout(retryTimer)
    ws?.close()
  }
}