import { useDeferredValue, useEffect, useMemo, useRef, useState } from 'react'
import { Check, Copy, RefreshCw, Search, Trash2, Zap } from 'lucide-react'
import { Alert, Badge, Box, Button, Card, Code, Group, Modal, Progress, Stack, Tabs, Text, TextInput, Tooltip } from '@mantine/core'
import { usePoolStore } from '@/stores/pool'
import { getPlatform, type Platform } from '@/api'
import { buildCommands, proxyUrl, type ProxyCommandSet } from '@/lib/proxy-commands'
import type { ProxyNode } from '@/types'

type NoticeData = { type: 'success' | 'error'; text: string }

function statusColor(n: ProxyNode) {
  switch (n.status) {
    case 'alive':
      return 'green'
    case 'checking':
      return 'yellow'
    case 'dead':
      return 'red'
    default:
      return 'gray'
  }
}

function statusLabel(n: ProxyNode) {
  switch (n.status) {
    case 'alive':
      return '存活'
    case 'checking':
      return '检测中'
    case 'dead':
      return '失效'
    default:
      return '新节点'
  }
}

export default function ProxyPool() {
  const nodes = usePoolStore((s) => s.nodes)
  const loading = usePoolStore((s) => s.loading)
  const checkingIds = usePoolStore((s) => s.checkingIds)
  const checkingAll = usePoolStore((s) => s.checkingAll)
  const notice = usePoolStore((s) => s.notice)
  const refresh = usePoolStore((s) => s.refresh)
  const remove = usePoolStore((s) => s.remove)
  const check = usePoolStore((s) => s.check)
  const clearNotice = usePoolStore((s) => s.clearNotice)

  const [filter, setFilter] = useState('')
  const deferredFilter = useDeferredValue(filter)
  const [scrollTop, setScrollTop] = useState(0)
  const [pending, setPending] = useState<ProxyNode | null>(null)
  const [copyTarget, setCopyTarget] = useState<ProxyNode | null>(null)
  const [scoreTarget, setScoreTarget] = useState<ProxyNode | null>(null)
  const [platform, setPlatform] = useState<Platform>('linux')
  const [activeTab, setActiveTab] = useState<string | null>('darwin')
  const [copiedKey, setCopiedKey] = useState<string | null>(null)
  const [localNotice, setLocalNotice] = useState<NoticeData | null>(null)
  const viewportRef = useRef<HTMLDivElement | null>(null)
  const ROW_HEIGHT = 56
  const VIEWPORT_HEIGHT = 520
  const OVERSCAN = 8

  useEffect(() => {
    // 首次加载显示 loading，之后定时自动刷新使用静默模式，不触发按钮 loading
    refresh()
    const timer = window.setInterval(() => refresh(undefined, true), 5000)
    return () => window.clearInterval(timer)
  }, [refresh])

  useEffect(() => {
    getPlatform().then(setPlatform).catch(() => setPlatform('linux'))
  }, [])

  useEffect(() => {
    if (!notice) return
    const timer = window.setTimeout(clearNotice, 4000)
    return () => window.clearTimeout(timer)
  }, [notice, clearNotice])

  // filter 变化时重置滚动位置：
  // scrollTop 用「渲染期间调整 state」模式（React 官方推荐，避免 effect 内同步 setState），
  // DOM 滚动保留在 effect（外部系统同步，合法用法）
  const [prevFilter, setPrevFilter] = useState(deferredFilter)
  if (prevFilter !== deferredFilter) {
    setPrevFilter(deferredFilter)
    setScrollTop(0)
  }

  useEffect(() => {
    viewportRef.current?.scrollTo({ top: 0 })
  }, [deferredFilter])

  const list = useMemo(() => {
    // 排序由 proxy-core 完成（分数 → 延迟 → ID → host），前端只做过滤
    const normalizedFilter = deferredFilter.trim().toLowerCase()
    return normalizedFilter ? nodes.filter((n) => n.host.toLowerCase().includes(normalizedFilter)) : nodes
  }, [nodes, deferredFilter])

  const totalHeight = list.length * ROW_HEIGHT
  const startIndex = Math.max(0, Math.floor(scrollTop / ROW_HEIGHT) - OVERSCAN)
  const endIndex = Math.min(list.length, Math.ceil((scrollTop + VIEWPORT_HEIGHT) / ROW_HEIGHT) + OVERSCAN)
  const visibleRows = useMemo(() => list.slice(startIndex, endIndex), [list, startIndex, endIndex])

  async function onCheck(n: ProxyNode) {
    await check(n.id)
    window.setTimeout(() => refresh(), 1500)
  }

  async function onCheckAll() {
    await check()
    window.setTimeout(() => refresh(), 1500)
  }

  async function copyCommand(key: string, text: string) {
    try {
      await navigator.clipboard.writeText(text)
      setCopiedKey(key)
      window.setTimeout(() => setCopiedKey((k) => (k === key ? null : k)), 1500)
    } catch {
      setLocalNotice({ type: 'error', text: '复制失败，请手动复制' })
    }
  }

  const commandSets = useMemo(() => (copyTarget ? buildCommands(copyTarget) : []), [copyTarget])
  const defaultTab = platform === 'win32' ? 'win32-powershell' : 'darwin'

  return (
    <Stack gap="md">
      <Card padding="lg" radius="md" withBorder>
        <Group justify="space-between" align="flex-start" wrap="wrap">
          <div>
            <Text fw={700}>代理池管理</Text>
            <Text size="sm" c="dimmed" mt={4}>按评分、状态和主机名快速筛选和管理节点</Text>
          </div>
          <Group gap="sm" wrap="wrap">
            <TextInput
              leftSection={<Search size={16} />}
              placeholder="筛选主机"
              value={filter}
              onChange={(e) => setFilter(e.currentTarget.value)}
              style={{ width: 260 }}
            />
            <Button leftSection={<Zap size={16} />} variant="light" loading={checkingAll} onClick={onCheckAll}>
              {checkingAll ? '检测中...' : '检测新节点'}
            </Button>
            <Button
              leftSection={<RefreshCw size={16} className={loading ? 'pp-spin' : undefined} />}
              variant="default"
              disabled={loading}
              onClick={() => refresh()}
            >
              刷新
            </Button>
          </Group>
        </Group>
      </Card>

      {(notice || localNotice) && (
        <Alert color={notice?.type === 'success' || localNotice?.type === 'success' ? 'green' : 'red'} withCloseButton onClose={() => { clearNotice(); setLocalNotice(null) }}>
          {(notice || localNotice)?.text}
        </Alert>
      )}

      <Card padding="md" radius="md" withBorder>
        <Group justify="space-between" mb="sm">
          <Text size="sm" c="dimmed">共 {list.length} 个节点 · 当前仅渲染可见区域</Text>
        </Group>
        <Box
          ref={viewportRef}
          onScroll={(event) => setScrollTop(event.currentTarget.scrollTop)}
          style={{ maxHeight: VIEWPORT_HEIGHT, overflowY: 'auto', borderRadius: 12, border: '1px solid var(--mantine-color-default-border)', background: 'var(--mantine-color-body)' }}
        >
          <Box style={{ height: totalHeight, minHeight: VIEWPORT_HEIGHT, position: 'relative' }}>
            <Box
              style={{
                position: 'sticky',
                top: 0,
                zIndex: 2,
                display: 'grid',
                gridTemplateColumns: '70px minmax(180px, 2.2fr) 90px 90px 70px 110px 180px',
                alignItems: 'center',
                gap: 8,
                padding: '0 12px',
                height: ROW_HEIGHT,
                fontSize: 12,
                fontWeight: 700,
                color: 'var(--mantine-color-dimmed)',
                textTransform: 'uppercase',
                letterSpacing: '0.08em',
                background: 'var(--mantine-color-default-hover)',
                borderBottom: '1px solid var(--mantine-color-default-border)',
              }}
            >
              <Text size="xs">ID</Text>
              <Text size="xs">主机</Text>
              <Text size="xs">协议</Text>
              <Text size="xs">延迟</Text>
              <Text size="xs">评分</Text>
              <Text size="xs">状态</Text>
              <Text size="xs" style={{ textAlign: 'right' }}>操作</Text>
            </Box>

            <Box style={{ transform: `translateY(${startIndex * ROW_HEIGHT}px)` }}>
              {list.length === 0 ? (
                <Box style={{ height: ROW_HEIGHT, display: 'flex', alignItems: 'center', justifyContent: 'center', color: 'var(--mantine-color-dimmed)' }}>
                  {loading ? '加载中...' : '暂无代理'}
                </Box>
              ) : (
                visibleRows.map((n) => (
                  <Box
                    key={n.id}
                    style={{
                      display: 'grid',
                      gridTemplateColumns: '70px minmax(180px, 2.2fr) 90px 90px 70px 110px 180px',
                      alignItems: 'center',
                      gap: 8,
                      padding: '0 12px',
                      height: ROW_HEIGHT,
                      borderBottom: '1px solid var(--mantine-color-default-border)',
                    }}
                  >
                    <Text size="sm">{n.id}</Text>
                    <Text size="sm" style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{`${n.host}:${n.port}`}</Text>
                    <Badge variant="light">{n.protocol}</Badge>
                    <Text size="sm">{n.latency}ms</Text>
                    <Tooltip label="查看评分明细">
                      <Button size="xs" variant="subtle" px={4} onClick={() => setScoreTarget(n)}>
                        {n.score}
                      </Button>
                    </Tooltip>
                    <Badge color={statusColor(n)}>{statusLabel(n)}</Badge>
                    <Group justify="flex-end" gap="xs">
                      <Tooltip label="复制代理命令">
                        <Button size="xs" variant="subtle" onClick={() => { setCopyTarget(n); setActiveTab(defaultTab) }}>
                          <Copy size={14} />
                        </Button>
                      </Tooltip>
                      <Button size="xs" variant="light" loading={checkingIds.includes(n.id)} onClick={() => onCheck(n)}>
                        检测
                      </Button>
                      <Button size="xs" color="red" variant="subtle" onClick={() => setPending(n)}>
                        <Trash2 size={14} />
                      </Button>
                    </Group>
                  </Box>
                ))
              )}
            </Box>
          </Box>
        </Box>
      </Card>

      <Modal opened={pending !== null} onClose={() => setPending(null)} title="删除代理">
        <Stack gap="md">
          <Text>确定删除 {pending?.host}:{pending?.port}？此操作不可撤销。</Text>
          <Group justify="flex-end">
            <Button variant="default" onClick={() => setPending(null)}>
              取消
            </Button>
            <Button color="red" onClick={async () => { if (pending) await remove(pending.id); setPending(null) }}>
              删除
            </Button>
          </Group>
        </Stack>
      </Modal>

      <Modal opened={scoreTarget !== null} onClose={() => setScoreTarget(null)} title="评分明细" size="md">
        {scoreTarget && (
          <ScoreBreakdownModal node={scoreTarget} />
        )}
      </Modal>

      <Modal opened={copyTarget !== null} onClose={() => setCopyTarget(null)} title="复制代理命令" size="lg">
        {copyTarget && (
          <Stack gap="md">
            <Group gap="xs">
              <Badge variant="light">{copyTarget.protocol}</Badge>
              <Text size="sm" fw={600} style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                {proxyUrl(copyTarget)}
              </Text>
            </Group>
            <Tabs value={activeTab} onChange={setActiveTab}>
              <Tabs.List>
                <Tabs.Tab value="win32-powershell">Windows PowerShell</Tabs.Tab>
                <Tabs.Tab value="win32-cmd">Windows CMD</Tabs.Tab>
                <Tabs.Tab value="darwin">macOS / Linux</Tabs.Tab>
              </Tabs.List>
              {commandSets.map((cs) => {
                const tabValue = cs.platform === 'win32' ? (cs.label === 'Windows CMD' ? 'win32-cmd' : 'win32-powershell') : 'darwin'
                return (
                  <Tabs.Panel key={tabValue} value={tabValue} pt="md">
                    <Stack gap="md">
                      <div>
                        <Group justify="space-between" mb={4}>
                          <Text size="sm" fw={600}>设置环境变量</Text>
                          <Button
                            size="xs"
                            variant="light"
                            leftSection={copiedKey === `${tabValue}-env` ? <Check size={14} /> : <Copy size={14} />}
                            onClick={() => copyCommand(`${tabValue}-env`, cs.env)}
                          >
                            {copiedKey === `${tabValue}-env` ? '已复制' : '复制'}
                          </Button>
                        </Group>
                        <Code block style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-all' }}>{cs.env}</Code>
                      </div>
                    </Stack>
                  </Tabs.Panel>
                )
              })}
            </Tabs>
          </Stack>
        )}
      </Modal>
    </Stack>
  )
}

function ScoreBreakdownModal({ node }: { node: ProxyNode }) {
  const b = node.scoreBreakdown
  if (!b) {
    return (
      <Stack gap="sm">
        <Text size="sm" c="dimmed">暂无评分明细（可能为旧版本数据，请刷新后重试）</Text>
        <Text size="sm">总分：{node.score}</Text>
      </Stack>
    )
  }
  const rows = [
    { label: '成功率', weight: b.weightSuccess, value: b.successRate, hint: '历史成功次数占比，权重最高' },
    { label: '延迟', weight: b.weightLatency, value: b.latencyScore, hint: `${node.latency}ms 映射为 ${b.latencyScore} 分` },
    { label: '稳定性', weight: b.weightStability, value: b.stability, hint: '失败次数越少越稳定' },
    { label: '匿名性', weight: b.weightAnonymity, value: b.anonymity, hint: node.protocol === 'socks5' ? 'SOCKS5 默认 95 分' : node.username ? '带认证，匿名性较低' : 'HTTP/HTTPS 默认 80 分' },
  ]
  return (
    <Stack gap="md">
      <Group justify="space-between" align="flex-start" wrap="wrap">
        <div>
          <Group gap="xs">
            <Badge variant="light">{node.protocol}</Badge>
            <Text size="sm" fw={600}>{`${node.host}:${node.port}`}</Text>
          </Group>
          <Text size="xs" c="dimmed" mt={4}>状态：{statusLabel(node)} · 延迟 {node.latency}ms</Text>
        </div>
        <div style={{ textAlign: 'right' }}>
          <Text size="xl" fw={700} c={node.score >= 60 ? 'green' : node.score >= 40 ? 'yellow' : 'red'}>{node.score}</Text>
          <Text size="xs" c="dimmed">综合评分</Text>
        </div>
      </Group>

      {rows.map((r) => (
        <div key={r.label}>
          <Group justify="space-between" mb={4}>
            <Text size="sm" fw={600}>{r.label}（{Math.round(r.weight * 100)}%）</Text>
            <Text size="sm">{r.value}</Text>
          </Group>
          <Progress value={r.value} color={r.value >= 60 ? 'green' : r.value >= 40 ? 'yellow' : 'red'} size="md" />
          <Text size="xs" c="dimmed" mt={2}>{r.hint}</Text>
        </div>
      ))}

      <Box p="sm" style={{ borderRadius: 8, background: 'var(--mantine-color-default-hover)' }}>
        <Text size="xs" c="dimmed" mb={4}>计算公式（加权求和，死亡节点总分减半）</Text>
        <Code block style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-all' }}>
          {`${b.weightSuccess}×${b.successRate} + ${b.weightLatency}×${b.latencyScore} + ${b.weightStability}×${b.stability} + ${b.weightAnonymity}×${b.anonymity} = ${b.score}`}
        </Code>
      </Box>
    </Stack>
  )
}