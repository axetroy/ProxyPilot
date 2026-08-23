/** Prometheus 文本格式解析与展示辅助 */

export interface ParsedMetric {
  /** 指标名，如 proxypilot_pool_nodes_total */
  name: string
  /** 标签集合，如 { status="alive" } */
  labels: Record<string, string>
  value: number
  /** 原始行（复制用） */
  rawLine: string
  /** 该指标族的 HELP 描述 */
  help?: string
}

export type MetricCategoryKey =
  | 'pool'
  | 'check'
  | 'gateway'
  | 'chain'
  | 'subscription'
  | 'selector'
  | 'runtime'
  | 'other'

export const CATEGORY_ORDER: MetricCategoryKey[] = [
  'pool',
  'gateway',
  'check',
  'chain',
  'subscription',
  'selector',
  'runtime',
  'other',
]

export const CATEGORY_META: Record<MetricCategoryKey, { label: string; desc: string }> = {
  pool: { label: '代理池', desc: '节点数量、评分、延迟' },
  gateway: { label: '网关', desc: '流量、连接数、并发会话' },
  check: { label: '检测', desc: '检测次数与耗时分布' },
  chain: { label: '链路健康', desc: '链路探测结果与失败计数' },
  subscription: { label: '订阅', desc: '抓取次数与节点增删' },
  selector: { label: '出口策略', desc: '当前策略与失败惩罚' },
  runtime: { label: '运行时', desc: 'Goroutine、内存等进程指标' },
  other: { label: '其他', desc: '未分类指标' },
}

/** 解析 Prometheus exposition 文本（# HELP / # TYPE / name{labels} value） */
export function parsePrometheusText(text: string): ParsedMetric[] {
  const out: ParsedMetric[] = []
  let help = ''
  for (const line of text.split('\n')) {
    const t = line.trim()
    if (!t) continue
    if (t.startsWith('# HELP ')) {
      help = t.slice(7).trim()
      continue
    }
    if (t.startsWith('#')) continue
    const sp = t.indexOf(' ')
    if (sp <= 0) continue
    const ident = t.slice(0, sp)
    const value = Number.parseFloat(t.slice(sp + 1))
    if (!Number.isFinite(value)) continue

    let name = ident
    const labels: Record<string, string> = {}
    const brace = ident.indexOf('{')
    if (brace >= 0 && ident.endsWith('}')) {
      name = ident.slice(0, brace)
      const inner = ident.slice(brace + 1, -1)
      const re = /([a-zA-Z_][a-zA-Z0-9_]*)="((?:[^"\\]|\\.)*)"/g
      for (const m of inner.matchAll(re)) {
        labels[m[1]] = m[2].replaceAll('\\"', '"').replaceAll('\\\\', '\\')
      }
    }
    out.push({ name, labels, value, rawLine: line, help })
  }
  return out
}

/** 按指标名前缀归类 */
export function categorize(name: string): MetricCategoryKey {
  if (name.startsWith('proxypilot_pool_')) return 'pool'
  if (name.startsWith('proxypilot_gateway_')) return 'gateway'
  if (name.startsWith('proxypilot_check_')) return 'check'
  if (name.startsWith('proxypilot_chain_')) return 'chain'
  if (name.startsWith('proxypilot_subscription_')) return 'subscription'
  if (name.startsWith('proxypilot_selector_')) return 'selector'
  if (name.startsWith('go_') || name.startsWith('process_') || name.startsWith('proxypilot_system_')) return 'runtime'
  return 'other'
}

// ---------- 数值格式化 ----------

export function formatNumber(v: number): string {
  if (!Number.isFinite(v)) return '-'
  if (Number.isInteger(v)) return v.toLocaleString('en-US')
  return v.toLocaleString('en-US', { maximumFractionDigits: 3 })
}

export function formatBytes(v: number): string {
  if (!Number.isFinite(v)) return '-'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let n = v
  let i = 0
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024
    i++
  }
  const digits = i === 0 ? 0 : n >= 100 ? 1 : 2
  return `${n.toFixed(digits)} ${units[i]}`
}

export function formatDuration(seconds: number): string {
  if (!Number.isFinite(seconds)) return '-'
  if (seconds < 60) return `${Math.round(seconds)}s`
  const d = Math.floor(seconds / 86400)
  const h = Math.floor((seconds % 86400) / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  if (d > 0) return `${d}d ${h}h`
  if (h > 0) return `${h}h ${m}m`
  return `${m}m ${Math.round(seconds % 60)}s`
}

/** 渲染标签为 { k="v", ... }；无标签返回空串 */
export function formatLabels(labels: Record<string, string>): string {
  const keys = Object.keys(labels)
  if (keys.length === 0) return ''
  return '{' + keys.sort().map((k) => `${k}="${labels[k]}"`).join(',') + '}'
}

// ---------- 概览统计 ----------

export interface OverviewStats {
  poolTotal: number
  poolAlive: number
  uploadBytes: number
  downloadBytes: number
  activeConns: number
  strategy: string
  goroutines: number
  memAllocBytes: number
  uptimeSeconds: number
  chainOk: number
  chainBad: number
}

function find(metrics: ParsedMetric[], name: string, match?: (m: ParsedMetric) => boolean): ParsedMetric | undefined {
  return metrics.find((m) => m.name === name && (!match || match(m)))
}

function sum(metrics: ParsedMetric[], name: string): number {
  return metrics.reduce((acc, m) => (m.name === name ? acc + m.value : acc), 0)
}

export function extractOverview(metrics: ParsedMetric[]): OverviewStats {
  const byStatus = (status: string) =>
    find(metrics, 'proxypilot_pool_nodes_total', (m) => m.labels.status === status)?.value ?? 0

  const strategyEntry = find(metrics, 'proxypilot_selector_strategy', (m) => m.value === 1)

  return {
    poolTotal: sum(metrics, 'proxypilot_pool_nodes_total'),
    poolAlive: byStatus('alive'),
    uploadBytes: sum(metrics, 'proxypilot_gateway_traffic_upload_bytes'),
    downloadBytes: sum(metrics, 'proxypilot_gateway_traffic_download_bytes'),
    activeConns: find(metrics, 'proxypilot_gateway_active_connections')?.value ?? 0,
    strategy: strategyEntry?.labels.strategy ?? '-',
    goroutines: find(metrics, 'proxypilot_system_goroutines')?.value ?? 0,
    memAllocBytes: find(metrics, 'proxypilot_system_memory_alloc_bytes')?.value ?? 0,
    uptimeSeconds: find(metrics, 'proxypilot_system_uptime_seconds')?.value ?? 0,
    // 后端仅对检测过的链路输出序列（success=1 / failure=0），未检测不输出
    chainOk: metrics.filter((m) => m.name === 'proxypilot_chain_health_result' && m.value === 1).length,
    chainBad: metrics.filter((m) => m.name === 'proxypilot_chain_health_result' && m.value === 0).length,
  }
}
