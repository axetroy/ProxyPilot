/* eslint-disable react-hooks/set-state-in-effect */
import { useCallback, useEffect, useMemo, useState } from 'react'
import { Copy, RefreshCw, Search } from 'lucide-react'
import {
  Accordion,
  ActionIcon,
  Alert,
  Badge,
  Box,
  Button,
  Card,
  Code,
  Collapse,
  Divider,
  Group,
  Paper,
  ScrollArea,
  SimpleGrid,
  Stack,
  Switch,
  Text,
  TextInput,
  ThemeIcon,
  Title,
  Tooltip,
} from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { getApiBaseUrl, getToken } from '@/api'
import { useStatusStore } from '@/stores/status'
import {
  CATEGORY_META,
  CATEGORY_ORDER,
  categorize,
  extractOverview,
  formatBytes,
  formatDuration,
  formatLabels,
  formatNumber,
  parsePrometheusText,
  type MetricCategoryKey,
  type ParsedMetric,
} from '@/lib/prometheus-parser'
import { getHistory, recordSample, type MetricSample } from '@/lib/metrics-history'

const AUTO_REFRESH_KEY = 'metrics-auto-refresh'
const REFRESH_INTERVAL = 10_000

async function copyText(text: string) {
  try {
    await navigator.clipboard.writeText(text)
    notifications.show({ message: '已复制', color: 'green', autoClose: 800 })
  } catch {
    notifications.show({ message: '复制失败，请手动选择复制', color: 'red', autoClose: 2000 })
  }
}

/** 概览统计条中的一项 */
function StatItem({ label, value, unit, color }: { label: string; value: string; unit?: string; color?: string }) {
  return (
    <Box miw={104}>
      <Text size="xs" c="dimmed" mb={2}>{label}</Text>
      <Group gap={4} align="baseline" wrap="nowrap">
        <Text size="lg" fw={700} style={{ lineHeight: 1.2 }} c={color}>{value}</Text>
        {unit && <Text size="xs" c="dimmed">{unit}</Text>}
      </Group>
    </Box>
  )
}

/** 单条指标行：名称+标签（截断）| 值 | 复制原始行 */
function MetricRow({ metric }: { metric: ParsedMetric }) {
  const labels = formatLabels(metric.labels)
  const full = `${metric.name}${labels}`
  return (
    <Group justify="space-between" gap="md" wrap="nowrap">
      <Tooltip
        label={
          <Box maw={480}>
            {metric.help && <Text size="xs" c="dimmed" mb={4}>{metric.help}</Text>}
            {/* 等宽 Text 而非 Code：Code 固定暗色背景，不随亮色主题切换 */}
            <Text size="xs" ff="monospace" style={{ whiteSpace: 'normal', wordBreak: 'break-all' }}>
              {full}
            </Text>
          </Box>
        }
        openDelay={400}
        multiline
        disabled={full.length < 56}
      >
        <Box style={{ minWidth: 0, flex: 1 }}>
          <Text component="div" size="sm" ff="monospace" truncate>
            {metric.name}
            {labels && (
              <Text span c="dimmed" size="xs" inherit>
                {labels}
              </Text>
            )}
          </Text>
        </Box>
      </Tooltip>
      <Group gap={2} wrap="nowrap">
        <Text size="sm" fw={600} ff="monospace" ta="right">
          {formatNumber(metric.value)}
        </Text>
        <ActionIcon
          size="sm"
          variant="subtle"
          color="gray"
          aria-label="复制原始行"
          onClick={() => void copyText(metric.rawLine)}
        >
          <Copy size={13} />
        </ActionIcon>
      </Group>
    </Group>
  )
}

/** 纯 SVG 迷你趋势线（零依赖），颜色传 CSS 变量以适配明暗主题 */
function Sparkline({ values, color, height = 44 }: { values: number[]; color: string; height?: number }) {
  if (values.length < 2) {
    return <Box h={height} />
  }
  const W = 100
  const max = Math.max(...values)
  const min = Math.min(...values)
  const span = max - min || 1
  const pts = values.map((v, i) => {
    const x = (i / (values.length - 1)) * W
    const y = height - 3 - ((v - min) / span) * (height - 6)
    return `${x.toFixed(2)},${y.toFixed(2)}`
  })
  return (
    <svg
      viewBox={`0 0 ${W} ${height}`}
      preserveAspectRatio="none"
      style={{ width: '100%', height, display: 'block' }}
      aria-hidden
    >
      <polygon
        points={`0,${height} ${pts.join(' ')} ${W},${height}`}
        fill={color}
        opacity={0.12}
      />
      <polyline
        points={pts.join(' ')}
        fill="none"
        stroke={color}
        strokeWidth={1.5}
        strokeLinejoin="round"
        strokeLinecap="round"
        vectorEffect="non-scaling-stroke"
      />
    </svg>
  )
}

/** 趋势卡片：当前值 + 峰值 + 迷你趋势线 */
function TrendCard({
  label,
  values,
  format,
  color,
}: {
  label: string
  values: number[]
  format: (v: number) => string
  color: string
}) {
  const cur = values.length > 0 ? values[values.length - 1] : 0
  const peak = values.length > 0 ? Math.max(...values) : 0
  return (
    <Paper withBorder radius="md" p="md">
      <Group justify="space-between" wrap="nowrap" gap="xs">
        <Text size="xs" c="dimmed">{label}</Text>
        {peak > 0 && (
          <Text size="xs" c="dimmed" ff="monospace">峰 {format(peak)}</Text>
        )}
      </Group>
      <Text size="lg" fw={700} mb={4}>{format(cur)}</Text>
      <Sparkline values={values} color={color} />
    </Paper>
  )
}

export default function Metrics() {
  const running = useStatusStore((s) => s.status.running)
  const [text, setText] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [searchTerm, setSearchTerm] = useState('')
  // 默认开启自动刷新；用户显式关闭过（存了 'false'）则尊重其选择
  const [autoRefresh, setAutoRefresh] = useState(() => localStorage.getItem(AUTO_REFRESH_KEY) !== 'false')
  const [rawOpen, setRawOpen] = useState(false)
  // 采样历史（模块级缓冲，切页不丢；应用重启清零）
  const [samples, setSamples] = useState<MetricSample[]>(() => getHistory())

  const scrapeUrl = useMemo(() => `${getApiBaseUrl()}/metrics`, [])

  const fetchMetrics = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      // /metrics 受 session token 保护，仅供本应用消费
      const res = await fetch(scrapeUrl, { headers: { 'X-Token': getToken() } })
      if (res.status === 401) throw new Error('会话已失效，请重启应用')
      if (!res.ok) throw new Error(`HTTP ${res.status}`)
      const raw = await res.text()
      const parsed = parsePrometheusText(raw)
      setText(raw)
      // 记录采样点供趋势图使用
      setSamples(recordSample(extractOverview(parsed)))
    } catch (e) {
      setError(e instanceof Error ? e.message : '获取失败')
    } finally {
      setLoading(false)
    }
  }, [scrapeUrl])

  useEffect(() => {
    if (!running) return
    void fetchMetrics()
    if (!autoRefresh) return
    const timer = window.setInterval(() => void fetchMetrics(), REFRESH_INTERVAL)
    return () => window.clearInterval(timer)
  }, [running, autoRefresh, fetchMetrics])

  const metrics = useMemo(() => parsePrometheusText(text), [text])
  const overview = useMemo(() => extractOverview(metrics), [metrics])

  // 按分类分组（应用搜索过滤），组内按名称排序保证稳定展示
  const groups = useMemo(() => {
    const kw = searchTerm.trim().toLowerCase()
    const hit = (m: ParsedMetric) =>
      !kw ||
      m.name.toLowerCase().includes(kw) ||
      Object.values(m.labels).some((v) => v.toLowerCase().includes(kw)) ||
      String(m.value).includes(kw)
    const map = new Map<MetricCategoryKey, ParsedMetric[]>()
    for (const m of metrics) {
      if (!hit(m)) continue
      const key = categorize(m.name)
      const list = map.get(key) ?? []
      list.push(m)
      map.set(key, list)
    }
    for (const list of map.values()) {
      list.sort(
        (a, b) =>
          a.name.localeCompare(b.name) ||
          formatLabels(a.labels).localeCompare(formatLabels(b.labels)),
      )
    }
    return CATEGORY_ORDER.filter((k) => map.has(k)).map((k) => [k, map.get(k)!] as const)
  }, [metrics, searchTerm])

  const aliveRate = overview.poolTotal > 0 ? Math.round((overview.poolAlive / overview.poolTotal) * 100) : null

  return (
    <Stack gap="md" style={{ height: '100%', overflow: 'auto' }}>
      {/* 头部：标题 + 操作 */}
      <Card padding="lg" radius="md" withBorder style={{ flexShrink: 0 }}>
        <Group justify="space-between" wrap="wrap" gap="md">
          <Box>
            <Title order={3}>运行指标</Title>
            <Text size="sm" c="dimmed" mt={4}>
              proxy-core 运行状态快照与趋势曲线，数据本地采样，仅本应用可见。
            </Text>
          </Box>
          <Group gap="sm" wrap="nowrap" align="center">
            <Button
              leftSection={<RefreshCw size={16} className={loading ? 'pp-spin' : undefined} />}
              disabled={!running || loading}
              onClick={() => void fetchMetrics()}
            >
              刷新
            </Button>
            {/* 与按钮等高的容器，保证开关与按钮中线对齐 */}
            <Box h={36} style={{ display: 'flex', alignItems: 'center' }}>
              <Switch
                label="自动刷新 (10s)"
                checked={autoRefresh}
                disabled={!running}
                onChange={(e) => {
                  setAutoRefresh(e.currentTarget.checked)
                  localStorage.setItem(AUTO_REFRESH_KEY, String(e.currentTarget.checked))
                }}
              />
            </Box>
          </Group>
        </Group>

        {!running && (
          <Alert color="yellow" variant="light" mt="md">
            网关未运行。启动网关后即可查看实时指标。
          </Alert>
        )}
        {error && (
          <Alert color="red" variant="light" mt="md">
            指标获取失败：{error}
            <Button size="compact-xs" variant="light" ml="sm" onClick={() => void fetchMetrics()}>
              重试
            </Button>
          </Alert>
        )}
      </Card>

      {/* 概览统计条 */}
      {text && (
        <Card padding="lg" radius="md" withBorder style={{ flexShrink: 0 }}>
          <Group gap="xl" wrap="wrap">
            <StatItem
              label="节点存活"
              value={`${overview.poolAlive}/${overview.poolTotal}`}
              color={aliveRate == null ? undefined : aliveRate >= 80 ? 'green' : aliveRate >= 50 ? 'yellow' : 'red'}
            />
            <StatItem label="存活率" value={aliveRate == null ? '-' : `${aliveRate}`} unit="%" />
            <StatItem label="上行流量" value={formatBytes(overview.uploadBytes)} />
            <StatItem label="下行流量" value={formatBytes(overview.downloadBytes)} />
            <StatItem label="活跃连接" value={formatNumber(overview.activeConns)} />
            <StatItem label="链路健康" value={overview.chainOk + overview.chainBad === 0 ? '-' : `${overview.chainOk}/${overview.chainOk + overview.chainBad}`} />
            <StatItem label="出口策略" value={overview.strategy} />
            <StatItem label="运行时长" value={formatDuration(overview.uptimeSeconds)} />
            <StatItem label="Goroutine" value={formatNumber(overview.goroutines)} />
            <StatItem label="内存" value={formatBytes(overview.memAllocBytes)} />
          </Group>
        </Card>
      )}

      {/* 趋势图（前端采样，无需外部监控系统） */}
      {text && samples.length >= 2 && (
        <Card padding="lg" radius="md" withBorder style={{ flexShrink: 0 }}>
          <Group justify="space-between" mb="sm">
            <Title order={5}>趋势</Title>
            <Text size="xs" c="dimmed">
              最近 {samples.length} 次采样{autoRefresh ? '（自动刷新中）' : ''}
            </Text>
          </Group>
          <SimpleGrid cols={{ base: 1, sm: 2, xl: 4 }} spacing="md">
            <TrendCard
              label="存活节点"
              values={samples.map((s) => s.poolAlive)}
              format={formatNumber}
              color="var(--mantine-color-green-6)"
            />
            <TrendCard
              label="下行速率"
              values={samples.map((s) => s.downRate)}
              format={(v) => `${formatBytes(v)}/s`}
              color="var(--mantine-color-blue-6)"
            />
            <TrendCard
              label="上行速率"
              values={samples.map((s) => s.upRate)}
              format={(v) => `${formatBytes(v)}/s`}
              color="var(--mantine-color-teal-6)"
            />
            <TrendCard
              label="内存占用"
              values={samples.map((s) => s.memAllocBytes)}
              format={formatBytes}
              color="var(--mantine-color-grape-6)"
            />
          </SimpleGrid>
        </Card>
      )}
      {text && samples.length === 1 && (
        <Alert color="blue" variant="light" style={{ flexShrink: 0 }}>
          已记录 1 次采样。开启「自动刷新」或稍后再刷新，即可看到流量、内存等指标的趋势曲线。
        </Alert>
      )}

      {/* 分类明细 */}
      {text && (
        <Card padding="lg" radius="md" withBorder style={{ flexShrink: 0 }}>
          <Group justify="space-between" mb="sm" wrap="wrap">
            <Title order={5}>指标明细</Title>
            <TextInput
              leftSection={<Search size={14} />}
              placeholder="搜索指标名 / 标签 / 值"
              value={searchTerm}
              onChange={(e) => setSearchTerm(e.currentTarget.value)}
              w={280}
              size="xs"
            />
          </Group>
          {groups.length === 0 ? (
            <Text size="sm" c="dimmed" ta="center" py="lg">
              没有匹配「{searchTerm}」的指标
            </Text>
          ) : (
            <Accordion multiple variant="separated" defaultValue={[groups[0][0]]}>
              {groups.map(([key, items]) => (
                <Accordion.Item key={key} value={key}>
                  <Accordion.Control>
                    <Group gap="sm" wrap="nowrap">
                      <Text fw={500}>{CATEGORY_META[key].label}</Text>
                      <Badge size="sm" variant="light" color="gray">
                        {items.length}
                      </Badge>
                      <Text size="xs" c="dimmed" visibleFrom="sm">
                        {CATEGORY_META[key].desc}
                      </Text>
                    </Group>
                  </Accordion.Control>
                  <Accordion.Panel>
                    <ScrollArea.Autosize mah={380} type="scroll" scrollbarSize={6}>
                      <Stack gap="xs" pr="sm">
                        {items.map((m) => (
                          <MetricRow key={m.rawLine} metric={m} />
                        ))}
                      </Stack>
                    </ScrollArea.Autosize>
                  </Accordion.Panel>
                </Accordion.Item>
              ))}
            </Accordion>
          )}
        </Card>
      )}

      {/* 原始输出（默认收起） */}
      {text && (
        <Card padding="lg" radius="md" withBorder style={{ flexShrink: 0 }}>
          <Group justify="space-between" wrap="wrap">
            <Group gap="sm">
              <ThemeIcon variant="light" color="gray" size="sm">
                <Copy size={13} />
              </ThemeIcon>
              <Text fw={500}>原始输出</Text>
              <Text size="xs" c="dimmed">{metrics.length} 个采样点</Text>
            </Group>
            <Group gap="sm">
              <Switch size="sm" label="显示" checked={rawOpen} onChange={(e) => setRawOpen(e.currentTarget.checked)} />
              <ActionIcon variant="default" aria-label="复制全部" onClick={() => void copyText(text)}>
                <Copy size={14} />
              </ActionIcon>
            </Group>
          </Group>
          <Collapse expanded={rawOpen}>
            <Divider my="sm" />
            <ScrollArea.Autosize mah={420} type="scroll">
              <Code block style={{ fontSize: 11 }}>{text}</Code>
            </ScrollArea.Autosize>
          </Collapse>
        </Card>
      )}
    </Stack>
  )
}
