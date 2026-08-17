import { useDeferredValue, useEffect, useMemo, useRef, useState } from 'react'
import { Check, Copy, Info, MoreHorizontal, Pin, PinOff, RefreshCw, Search, Trash2, Zap } from 'lucide-react'
import { Alert, Badge, Box, Button, Card, Code, Grid, Group, Menu, Modal, Progress, Stack, Tabs, Text, TextInput, Tooltip } from '@mantine/core'
import { usePoolStore } from '@/stores/pool'
import { useStatusStore } from '@/stores/status'
import { useSubsStore } from '@/stores/subscriptions'
import { getPlatform, type Platform } from '@/api'
import { buildCommands, proxyUrl, type ProxyCommandSet } from '@/lib/proxy-commands'
import type { ProxyNode, Subscription } from '@/types'

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

// geoText 拼接节点地区文案：国家 · 省份 · 城市，
// 连续重复段（直辖市省=市同名）自动合并，未知段忽略。
function geoText(n: ProxyNode): string {
  const parts: string[] = []
  for (const v of [n.country, n.province, n.city]) {
    if (v && v !== parts[parts.length - 1]) {
      parts.push(v)
    }
  }
  return parts.join(' · ')
}

// subscriptionName 通过 subscriptionId 关联订阅列表取名称；无订阅或未找到返回 undefined。
function subscriptionName(subs: Subscription[], id?: number): string | undefined {
  if (!id) return undefined
  return subs.find((s) => s.id === id)?.name
}

// formatTime 格式化时间字符串为本地时间；空值返回占位符。
function formatTime(v?: string): string {
  if (!v) return '—'
  const d = new Date(v)
  if (Number.isNaN(d.getTime())) return '—'
  return d.toLocaleString()
}

// AnonymityBadge 在列表中标注节点的匿名性：优先使用真实探测明细分数，
// 回退到评分明细中的匿名性分数（启发式）；非存活或未评分节点显示占位符。
function AnonymityBadge({ node }: { node: ProxyNode }) {
  const d = node.anonymityDetail
  const score = d?.score ?? node.scoreBreakdown?.anonymity
  if (node.status !== 'alive' || score === undefined) {
    return <Text size="xs" c="dimmed">—</Text>
  }
  const probed = d != null
  const info =
    score >= 80
      ? { label: '匿名', color: 'green' }
      : score >= 60
        ? { label: '半匿名', color: 'yellow' }
        : { label: '不匿名', color: 'red' }
  return (
    <Tooltip label={`匿名性 ${score} 分（${probed ? '真实探测' : '启发式估算'}）`}>
      <Badge color={info.color} variant="light">{info.label}</Badge>
    </Tooltip>
  )
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
  const pin = usePoolStore((s) => s.pin)
  const unpin = usePoolStore((s) => s.unpin)
  // 当前指定的固定出口（由 App 每秒轮询 /api/status 更新）
  const pinnedNode = useStatusStore((s) => s.status.pinnedNode)
  // 订阅列表：节点详情展示「来自哪个订阅」时用于关联 subscriptionId → 名称
  const subs = useSubsStore((s) => s.subs)
  const refreshSubs = useSubsStore((s) => s.refresh)

  const [filter, setFilter] = useState('')
  const deferredFilter = useDeferredValue(filter)
  const [scrollTop, setScrollTop] = useState(0)
  const [pending, setPending] = useState<ProxyNode | null>(null)
  const [copyTarget, setCopyTarget] = useState<ProxyNode | null>(null)
  const [scoreTarget, setScoreTarget] = useState<ProxyNode | null>(null)
  const [detailTarget, setDetailTarget] = useState<ProxyNode | null>(null)
  const [platform, setPlatform] = useState<Platform>('linux')
  const [activeTab, setActiveTab] = useState<string | null>('darwin')
  const [copiedKey, setCopiedKey] = useState<string | null>(null)
  const [localNotice, setLocalNotice] = useState<NoticeData | null>(null)
  const viewportRef = useRef<HTMLDivElement | null>(null)
  const ROW_HEIGHT = 56
  const VIEWPORT_HEIGHT = 520
  const OVERSCAN = 8
  // 列表 grid 列模板：表头与每行必须使用同一模板，避免拉大窗口时列错位。
  // 固定列 + 主机列最小宽度 + gap + padding 之和为内容最小宽度（确保操作列无需横向滚动即可见）：
  // 60+180+72+70+64+68+62+92 = 668，+7×8 gap +24 padding = 748。
  const GRID_COLUMNS = '60px minmax(180px, 2.2fr) 72px 70px 64px 68px 62px 92px'
  const GRID_MIN_WIDTH = 748

  useEffect(() => {
    // 首次加载显示 loading，之后定时自动刷新使用静默模式，不触发按钮 loading
    refresh()
    const timer = window.setInterval(() => refresh(undefined, true), 5000)
    return () => window.clearInterval(timer)
  }, [refresh])

  useEffect(() => {
    getPlatform().then(setPlatform).catch(() => setPlatform('linux'))
  }, [])

  // 代理池页也需要订阅列表（节点详情显示订阅来源）。订阅页加载过就直接用，否则拉一次。
  useEffect(() => {
    if (subs.length === 0) {
      refreshSubs()
    }
  }, [subs.length, refreshSubs])

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
    if (!normalizedFilter) return nodes
    return nodes.filter((n) => {
      const subName = subscriptionName(subs, n.subscriptionId) ?? ''
      return `${n.host}:${n.port} ${n.protocol} ${geoText(n)} ${subName}`.toLowerCase().includes(normalizedFilter)
    })
  }, [nodes, deferredFilter, subs])

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

  async function onPin(n: ProxyNode) {
    await pin(n.id)
  }

  async function onUnpin() {
    await unpin()
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
            <Text size="sm" c="dimmed" mt={4}>按评分、状态、主机名和地区快速筛选和管理节点</Text>
          </div>
          <Group gap="sm" wrap="wrap">
            <TextInput
              leftSection={<Search size={16} />}
              placeholder="筛选主机 / 地区"
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

      {pinnedNode ? (
        <Alert color="blue" variant="light" withCloseButton onClose={onUnpin}>
          <Group gap="xs" wrap="nowrap">
            <Pin size={16} />
            <Box>
              <Text size="sm">
                固定出口已指定：<Badge variant="light">{pinnedNode.protocol}</Badge>{' '}
                <Text span fw={600} inherit>{`${pinnedNode.host}:${pinnedNode.port}`}</Text>
              </Text>
              {pinnedNode.status === 'alive' ? (
                <Text size="sm" mt={2}>流量固定走该节点（评分 {pinnedNode.score}），不再按评分自动选择；点击关闭可取消指定</Text>
              ) : (
                <Text size="sm" c="red" mt={2}>
                  节点当前不可用（{statusLabel(pinnedNode)}），流量暂回退自动选择，存活后自动恢复固定
                </Text>
              )}
            </Box>
          </Group>
        </Alert>
      ) : (
        <Alert color="gray" variant="light">
          <Group gap="xs" wrap="nowrap">
            <PinOff size={16} />
            <Text size="sm">未指定固定出口，自动按评分选择最优节点；可在任意节点行点击「指定」固定使用</Text>
          </Group>
        </Alert>
      )}

      <Card padding="md" radius="md" withBorder>
        <Group justify="space-between" mb="sm">
          <Text size="sm" c="dimmed">共 {list.length} 个节点 · 当前仅渲染可见区域</Text>
        </Group>
        <Box
          ref={viewportRef}
          onScroll={(event) => setScrollTop(event.currentTarget.scrollTop)}
          style={{ maxHeight: VIEWPORT_HEIGHT, overflowY: 'auto', overflowX: 'auto', borderRadius: 12, border: '1px solid var(--mantine-color-default-border)', background: 'var(--mantine-color-body)' }}
        >
          <Box style={{ height: totalHeight, minHeight: VIEWPORT_HEIGHT, position: 'relative' }}>
            <Box
              style={{
                position: 'sticky',
                top: 0,
                zIndex: 2,
                display: 'grid',
                gridTemplateColumns: GRID_COLUMNS,
                minWidth: GRID_MIN_WIDTH,
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
              <Text size="xs">主机 · 地区</Text>
              <Text size="xs">协议</Text>
              <Text size="xs">延迟</Text>
              <Text size="xs">评分</Text>
              <Text size="xs">匿名</Text>
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
                      gridTemplateColumns: GRID_COLUMNS,
                      minWidth: GRID_MIN_WIDTH,
                      alignItems: 'center',
                      gap: 8,
                      padding: '0 12px',
                      height: ROW_HEIGHT,
                      borderBottom: '1px solid var(--mantine-color-default-border)',
                    }}
                  >
                    <Text size="sm">{n.id}</Text>
                    <Box
                      style={{ cursor: 'pointer', minWidth: 0 }}
                      onClick={() => setDetailTarget(n)}
                    >
                      <Tooltip label="查看节点详情" position="top-start">
                        <Text size="sm" style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{`${n.host}:${n.port}`}</Text>
                      </Tooltip>
                      <Text size="xs" c="dimmed" style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                        {geoText(n) || '—'}
                      </Text>
                    </Box>
                    <Badge variant="light">{n.protocol}</Badge>
                    <Text size="sm">{n.latency}ms</Text>
                    <Tooltip label="查看评分明细">
                      <Button size="xs" variant="subtle" px={4} onClick={() => setScoreTarget(n)}>
                        {n.score}
                      </Button>
                    </Tooltip>
                    <AnonymityBadge node={n} />
                    <Badge color={statusColor(n)}>{statusLabel(n)}</Badge>
                    <Group justify="flex-end" gap={6} wrap="nowrap">
<Button size="xs" variant="light" loading={checkingIds.includes(n.id)} onClick={() => onCheck(n)} px={6}>
                    检测
                  </Button>
                      <Menu position="bottom-end" shadow="md" width={200} withinPortal>
                        <Menu.Target>
                          <Tooltip label="更多操作">
                            <Button size="xs" variant="subtle" px={6} aria-label="更多操作">
                              <MoreHorizontal size={16} />
                            </Button>
                          </Tooltip>
                        </Menu.Target>
                        <Menu.Dropdown>
                          {pinnedNode?.id === n.id ? (
                            <Menu.Item color="green" leftSection={<PinOff size={14} />} onClick={onUnpin}>
                              取消指定
                            </Menu.Item>
                          ) : (
                            <Menu.Item leftSection={<Pin size={14} />} onClick={() => onPin(n)}>
                              指定为固定出口
                            </Menu.Item>
                          )}
                          <Menu.Item leftSection={<Info size={14} />} onClick={() => setDetailTarget(n)}>
                            查看详情
                          </Menu.Item>
                          <Menu.Item leftSection={<Copy size={14} />} onClick={() => { setCopyTarget(n); setActiveTab(defaultTab) }}>
                            复制代理命令
                          </Menu.Item>
                          <Menu.Divider />
                          <Menu.Item color="red" leftSection={<Trash2 size={14} />} onClick={() => setPending(n)}>
                            删除代理
                          </Menu.Item>
                        </Menu.Dropdown>
                      </Menu>
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

      <Modal opened={detailTarget !== null} onClose={() => setDetailTarget(null)} title="节点详情" size="md">
        {detailTarget && (
          <NodeDetailModal node={detailTarget} subs={subs} />
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

function anonymityHint(node: ProxyNode, value: number): string {
  const d = node.anonymityDetail
  if (!d) {
    // 无探测明细：回退说明
    if (value === 0) return '死亡节点，匿名性记 0 分'
    return node.protocol === 'socks5' ? 'SOCKS5 默认 95 分（未探测到回显数据）' : node.username ? '带认证，匿名性较低（未探测到回显数据）' : 'HTTP/HTTPS 默认 80 分（未探测到回显数据）'
  }
  const parts: string[] = []
  if (d.sourceIpHidden === undefined) {
    parts.push('源 IP 无法对比')
  } else if (d.sourceIpHidden) {
    parts.push('源 IP 已隐藏')
  } else {
    parts.push('源 IP 未隐藏（透明）')
  }
  parts.push(`头泄漏 ${d.headerLeaks?.length ?? 0} 项`)
  parts.push(`代理特征 ${d.proxyMarkers?.length ?? 0} 项`)
  if (d.rotatingIp) {
    parts.push('出口 IP 轮换')
  }
  if ((d.reqIssues?.length ?? 0) > 0) {
    parts.push(`连接信息问题 ${d.reqIssues?.length} 项`)
  }
  return parts.join(' · ')
}

function NodeDetailModal({ node, subs }: { node: ProxyNode; subs: Subscription[] }) {
  const subName = subscriptionName(subs, node.subscriptionId)
  const geo = geoText(node)
  return (
    <Stack gap="md">
      <Group justify="space-between" align="flex-start" wrap="wrap">
        <div>
          <Group gap="xs">
            <Badge variant="light">{node.protocol}</Badge>
            <Text size="sm" fw={600}>{`${node.host}:${node.port}`}</Text>
          </Group>
          <Text size="xs" c="dimmed" mt={4}>地区：{geo || '—'} · 延迟 {node.latency}ms</Text>
        </div>
        <div style={{ textAlign: 'right' }}>
          <Text size="xl" fw={700} c={node.score >= 60 ? 'green' : node.score >= 40 ? 'yellow' : 'red'}>{node.score}</Text>
          <Text size="xs" c="dimmed">综合评分</Text>
        </div>
      </Group>

      <Box p="sm" style={{ borderRadius: 8, background: 'var(--mantine-color-default-hover)' }}>
        <Grid>
          <Grid.Col span={6}>
            <Text size="xs" c="dimmed">订阅来源</Text>
            <Text size="sm">{subName ?? '—'}</Text>
          </Grid.Col>
          <Grid.Col span={6}>
            <Text size="xs" c="dimmed">状态</Text>
            <Badge color={statusColor(node)}>{statusLabel(node)}</Badge>
          </Grid.Col>
          <Grid.Col span={6}>
            <Text size="xs" c="dimmed">认证</Text>
            <Text size="sm">{node.username ? `${node.username}${node.password ? '（有密码）' : ''}` : '无'}</Text>
          </Grid.Col>
          <Grid.Col span={6}>
            <Text size="xs" c="dimmed">成功率</Text>
            <Text size="sm">{node.successCount} 次成功 / {node.failCount} 次失败</Text>
          </Grid.Col>
          <Grid.Col span={6}>
            <Text size="xs" c="dimmed">最近检测</Text>
            <Text size="sm">{formatTime(node.lastCheck)}</Text>
          </Grid.Col>
          <Grid.Col span={6}>
            <Text size="xs" c="dimmed">创建时间</Text>
            <Text size="sm">{formatTime(node.createdAt)}</Text>
          </Grid.Col>
        </Grid>
      </Box>

      <Text size="xs" c="dimmed">评分明细</Text>
      <ScoreBreakdownModal node={node} />
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
  const d = node.anonymityDetail
  const rows = [
    { label: '成功率', weight: b.weightSuccess, value: b.successRate, hint: '历史成功次数占比，权重最高' },
    { label: '延迟', weight: b.weightLatency, value: b.latencyScore, hint: `${node.latency}ms 映射为 ${b.latencyScore} 分` },
    { label: '稳定性', weight: b.weightStability, value: b.stability, hint: '失败次数越少越稳定' },
    { label: '匿名性', weight: b.weightAnonymity, value: b.anonymity, hint: anonymityHint(node, b.anonymity) },
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

      {d && (d.headerLeaks?.length || d.proxyMarkers?.length || d.sourceIpHidden !== undefined || d.rotatingIp || d.reqIssues?.length) && (
        <Box p="sm" style={{ borderRadius: 8, background: 'var(--mantine-color-default-hover)' }}>
          <Text size="xs" fw={600} mb={4}>匿名性探测明细</Text>
          {d.sourceIpHidden !== undefined && (
            <Text size="xs" c="dimmed">
              源 IP 隐藏：{d.sourceIpHidden ? '是（代理出口 ≠ 直连出口）' : '否（透明代理，出口 IP 相同）'}
            </Text>
          )}
          {d.rotatingIp && (
            <Text size="xs" c="dimmed">出口 IP 轮换：是（两次采样出口不同，难以关联同一用户）</Text>
          )}
          {d.headerLeaks && d.headerLeaks.length > 0 && (
            <Text size="xs" c="dimmed">头泄漏：</Text>
          )}
          {d.headerLeaks?.map((h) => (
            <Code key={h} block style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-all' }}>{h}</Code>
          ))}
          {d.proxyMarkers && d.proxyMarkers.length > 0 && (
            <Text size="xs" c="dimmed" mt={4}>代理特征：</Text>
          )}
          {d.proxyMarkers?.map((h) => (
            <Code key={h} block style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-all' }}>{h}</Code>
          ))}
          {d.reqIssues && d.reqIssues.length > 0 && (
            <Text size="xs" c="dimmed" mt={4}>连接信息问题（请求被改写）：</Text>
          )}
          {d.reqIssues?.map((h) => (
            <Code key={h} block style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-all' }}>{h}</Code>
          ))}
        </Box>
      )}

      <Box p="sm" style={{ borderRadius: 8, background: 'var(--mantine-color-default-hover)' }}>
        <Text size="xs" c="dimmed" mb={4}>计算公式（加权求和，死亡节点总分减半）</Text>
        <Code block style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-all' }}>
          {`${b.weightSuccess}×${b.successRate} + ${b.weightLatency}×${b.latencyScore} + ${b.weightStability}×${b.stability} + ${b.weightAnonymity}×${b.anonymity} = ${b.score}`}
        </Code>
      </Box>
    </Stack>
  )
}