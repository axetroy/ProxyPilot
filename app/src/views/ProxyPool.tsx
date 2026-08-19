import { useDeferredValue, useEffect, useMemo, useRef, useState } from 'react'
import { Check, Copy, Info, MoreHorizontal, Pin, PinOff, RefreshCw, Search, Trash2, Zap } from 'lucide-react'
import { Alert, Badge, Box, Button, Card, Checkbox, Code, Grid, Group, Menu, Modal, Progress, Stack, Tabs, Text, TextInput, Tooltip } from '@mantine/core'
import { usePoolStore } from '@/stores/pool'
import { useStatusStore } from '@/stores/status'
import { useSubsStore } from '@/stores/subscriptions'
import { getPlatform, getProxyHistory, type Platform } from '@/api'
import { buildCommands, proxyUrl, type ProxyCommandSet } from '@/lib/proxy-commands'
import type { CheckHistory, ProxyNode, Subscription } from '@/types'

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
  const removeMany = usePoolStore((s) => s.removeMany)
  const check = usePoolStore((s) => s.check)
  const checkMany = usePoolStore((s) => s.checkMany)
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
  // 订阅分组过滤：'all' 全部 / 'none' 未分组（手动添加的节点）/ String(订阅 id)
  const [subFilter, setSubFilter] = useState<string>('all')
  const [scrollTop, setScrollTop] = useState(0)
  const [pending, setPending] = useState<ProxyNode | null>(null)
  const [copyTarget, setCopyTarget] = useState<ProxyNode | null>(null)
  const [scoreTarget, setScoreTarget] = useState<ProxyNode | null>(null)
  const [detailTarget, setDetailTarget] = useState<ProxyNode | null>(null)
  // 批量操作：勾选的节点 id 集合
  const [selected, setSelected] = useState<Set<number>>(new Set())
  const [batchDeleteOpen, setBatchDeleteOpen] = useState(false)
  const [batchDeleting, setBatchDeleting] = useState(false)
  const [platform, setPlatform] = useState<Platform>('linux')
  const [activeTab, setActiveTab] = useState<string | null>('darwin')
  const [copiedKey, setCopiedKey] = useState<string | null>(null)
  const [localNotice, setLocalNotice] = useState<NoticeData | null>(null)
  const viewportRef = useRef<HTMLDivElement | null>(null)
  const ROW_HEIGHT = 56
  const OVERSCAN = 8
  // 列表视口实际高度（由 flex 布局决定，ResizeObserver 实时测量），用于虚拟滚动计算
  const [viewportHeight, setViewportHeight] = useState(0)
  // 列表 grid 列模板：表头与每行必须使用同一模板，避免拉大窗口时列错位。
  // 固定列 + 主机列最小宽度 + gap + padding 之和为内容最小宽度（确保操作列无需横向滚动即可见）：
  // 40+60+180+72+70+64+68+62+92 = 708，+8×8 gap +24 padding = 788。
  const GRID_COLUMNS = '40px 60px minmax(180px, 2.2fr) 72px 70px 64px 68px 62px 92px'
  const GRID_MIN_WIDTH = 788

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

  // 监听列表视口尺寸：窗口大小变化 / 顶部元素高度变化时实时更新虚拟滚动高度，
  // 使列表始终撑满剩余页面高度，页面本身不再出现外部滚动条。
  useEffect(() => {
    const el = viewportRef.current
    if (!el) return
    const ro = new ResizeObserver((entries) => {
      for (const entry of entries) {
        setViewportHeight(entry.contentRect.height)
      }
    })
    ro.observe(el)
    return () => ro.disconnect()
  }, [])

  // filter / subFilter 变化时重置滚动位置：
  // scrollTop 用「渲染期间调整 state」模式（React 官方推荐，避免 effect 内同步 setState），
  // DOM 滚动保留在 effect（外部系统同步，合法用法）
  const [prevFilter, setPrevFilter] = useState(deferredFilter)
  if (prevFilter !== deferredFilter) {
    setPrevFilter(deferredFilter)
    setScrollTop(0)
  }
  const [prevSubFilter, setPrevSubFilter] = useState(subFilter)
  if (prevSubFilter !== subFilter) {
    setPrevSubFilter(subFilter)
    setScrollTop(0)
  }

  useEffect(() => {
    viewportRef.current?.scrollTo({ top: 0 })
  }, [deferredFilter, subFilter])

  // 订阅分组计数：每个订阅的节点数 + 未分组节点数，用于分组 Tabs 显示
  const subCounts = useMemo(() => {
    const counts = new Map<string, number>()
    let none = 0
    for (const n of nodes) {
      if (n.subscriptionId == null) {
        none++
      } else {
        const key = String(n.subscriptionId)
        counts.set(key, (counts.get(key) ?? 0) + 1)
      }
    }
    return { counts, none }
  }, [nodes])

  const list = useMemo(() => {
    // 排序由 proxy-core 完成（分数 → 延迟 → ID → host），前端只做过滤
    const normalizedFilter = deferredFilter.trim().toLowerCase()
    return nodes.filter((n) => {
      if (subFilter === 'none') {
        if (n.subscriptionId != null) return false
      } else if (subFilter !== 'all' && n.subscriptionId !== Number(subFilter)) {
        return false
      }
      if (!normalizedFilter) return true
      const subName = subscriptionName(subs, n.subscriptionId) ?? ''
      return `${n.host}:${n.port} ${n.protocol} ${geoText(n)} ${subName}`.toLowerCase().includes(normalizedFilter)
    })
  }, [nodes, deferredFilter, subs, subFilter])

  const totalHeight = list.length * ROW_HEIGHT
  const startIndex = Math.max(0, Math.floor(scrollTop / ROW_HEIGHT) - OVERSCAN)
  const endIndex = Math.min(list.length, Math.ceil((scrollTop + (viewportHeight || 520)) / ROW_HEIGHT) + OVERSCAN)
  const visibleRows = useMemo(() => list.slice(startIndex, endIndex), [list, startIndex, endIndex])

  async function onCheck(n: ProxyNode) {
    await check(n.id)
    window.setTimeout(() => refresh(), 1500)
  }

  async function onCheckAll() {
    await check()
    window.setTimeout(() => refresh(), 1500)
  }

  // 当前过滤结果是否全部被选中（用于表头全选/半选状态）
  const allVisibleSelected = list.length > 0 && list.every((n) => selected.has(n.id))
  const checkingSelected = [...selected].some((id) => checkingIds.includes(id))

  function toggleAll() {
    setSelected((prev) => {
      const next = new Set(prev)
      if (allVisibleSelected) {
        list.forEach((n) => next.delete(n.id))
      } else {
        list.forEach((n) => next.add(n.id))
      }
      return next
    })
  }

  function toggleOne(id: number) {
    setSelected((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  function clearSelection() {
    setSelected(new Set())
  }

  async function onCheckSelected() {
    await checkMany([...selected])
    window.setTimeout(() => refresh(), 1500)
  }

  async function onDeleteSelected() {
    setBatchDeleting(true)
    const ok = await removeMany([...selected])
    setBatchDeleting(false)
    setBatchDeleteOpen(false)
    if (ok) clearSelection()
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
    <Stack gap="md" style={{ height: '100%', overflow: 'hidden' }}>
      <Card padding="lg" radius="md" withBorder style={{ flexShrink: 0 }}>
        <Group justify="space-between" align="flex-start" wrap="wrap">
          <div>
            <Text fw={700}>代理池管理</Text>
            <Text size="sm" c="dimmed" mt={4}>按评分、状态、主机名和地区快速筛选和管理节点</Text>
          </div>
          <Group gap="sm" wrap="wrap">
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
        <Group gap="sm" mt="sm" wrap="nowrap" style={{ alignItems: 'center' }}>
          <TextInput
            leftSection={<Search size={16} />}
            placeholder="筛选主机 / 地区 / 订阅"
            value={filter}
            onChange={(e) => setFilter(e.currentTarget.value)}
            style={{ width: 260, flexShrink: 0 }}
          />
          {/* 订阅分组视图：按订阅分组过滤，每个 Tab 显示该订阅节点数 */}
          <Tabs value={subFilter} onChange={(v) => setSubFilter(v ?? 'all')} style={{ flex: 1, minWidth: 0 }}>
            <Tabs.List style={{ flexWrap: 'nowrap', overflowX: 'auto' }}>
              <Tabs.Tab value="all">
                全部 <Badge size="xs" variant="light" ml={4}>{nodes.length}</Badge>
              </Tabs.Tab>
              {subs.map((s) => (
                <Tabs.Tab key={s.id} value={String(s.id)}>
                  {s.name} <Badge size="xs" variant="light" ml={4}>{subCounts.counts.get(String(s.id)) ?? 0}</Badge>
                </Tabs.Tab>
              ))}
              {subCounts.none > 0 && (
                <Tabs.Tab value="none">
                  未分组 <Badge size="xs" variant="light" ml={4}>{subCounts.none}</Badge>
                </Tabs.Tab>
              )}
            </Tabs.List>
          </Tabs>
        </Group>
      </Card>

      {(notice || localNotice) && (
        <Alert color={notice?.type === 'success' || localNotice?.type === 'success' ? 'green' : 'red'} withCloseButton onClose={() => { clearNotice(); setLocalNotice(null) }} style={{ flexShrink: 0 }}>
          {(notice || localNotice)?.text}
        </Alert>
      )}

      {pinnedNode && (
        <Alert color="blue" variant="light" withCloseButton onClose={onUnpin} style={{ flexShrink: 0 }}>
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
      )}

      <Card padding="md" radius="md" withBorder style={{ flex: 1, minHeight: 0, display: 'flex', flexDirection: 'column' }}>
        <Group justify="space-between" mb="sm">
          {selected.size > 0 ? (
            <Group gap="sm">
              <Text size="sm" c="dimmed">已选 <Text span fw={700}>{selected.size}</Text> 个节点</Text>
              <Button size="xs" variant="light" leftSection={<Zap size={14} />} disabled={checkingSelected} onClick={onCheckSelected}>
                {checkingSelected ? '检测中...' : '批量检测'}
              </Button>
              <Button size="xs" color="red" variant="light" leftSection={<Trash2 size={14} />} onClick={() => setBatchDeleteOpen(true)}>
                批量删除
              </Button>
              <Button size="xs" variant="subtle" onClick={clearSelection}>
                清除选择
              </Button>
            </Group>
          ) : (
            <Text size="sm" c="dimmed">共 {list.length} 个节点 · 当前仅渲染可见区域</Text>
          )}
        </Group>
        <Box
          ref={viewportRef}
          onScroll={(event) => setScrollTop(event.currentTarget.scrollTop)}
          style={{ flex: 1, minHeight: 0, overflowY: 'auto', overflowX: 'auto', borderRadius: 12, border: '1px solid var(--mantine-color-default-border)', background: 'var(--mantine-color-body)' }}
        >
          <Box style={{ height: Math.max(totalHeight, 1), minHeight: '100%', position: 'relative' }}>
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
              <Checkbox
                size="xs"
                checked={allVisibleSelected}
                indeterminate={selected.size > 0 && !allVisibleSelected}
                onChange={toggleAll}
                aria-label="全选当前列表"
              />
              <Text size="xs">ID</Text>
              <Text size="xs">主机 · 地区</Text>
              <Text size="xs">协议</Text>
              <Text size="xs">延迟</Text>
              <Text size="xs">评分</Text>
              <Text size="xs">匿名</Text>
              <Text size="xs">状态</Text>
              <Text size="xs" style={{ textAlign: 'right' }}>操作</Text>
            </Box>

            {list.length === 0 ? (
              <Box style={{ height: '100%', minHeight: ROW_HEIGHT, display: 'flex', alignItems: 'center', justifyContent: 'center', color: 'var(--mantine-color-dimmed)' }}>
                {loading ? '加载中...' : '暂无代理'}
              </Box>
            ) : (
              <Box style={{ transform: `translateY(${startIndex * ROW_HEIGHT}px)` }}>
                {visibleRows.map((n) => (
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
                    <Checkbox
                      size="xs"
                      checked={selected.has(n.id)}
                      onChange={() => toggleOne(n.id)}
                      aria-label={`选择节点 ${n.id}`}
                    />
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
                ))}
                </Box>
              )}
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

      <Modal opened={batchDeleteOpen} onClose={() => setBatchDeleteOpen(false)} title="批量删除节点">
        <Stack gap="md">
          <Text>确定删除选中的 {selected.size} 个节点？此操作不可撤销。</Text>
          {selected.size > 0 && (
            <Text size="sm" c="dimmed">
              {[...selected].slice(0, 5).map((id) => nodes.find((n) => n.id === id)).filter(Boolean).map((n) => `${n!.host}:${n!.port}`).join('、')}
              {selected.size > 5 ? ` 等 ${selected.size} 个` : ''}
            </Text>
          )}
          <Group justify="flex-end">
            <Button variant="default" disabled={batchDeleting} onClick={() => setBatchDeleteOpen(false)}>
              取消
            </Button>
            <Button color="red" loading={batchDeleting} onClick={onDeleteSelected}>
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

// LatencySparkline 用纯 SVG 绘制最近 N 次检测的延迟趋势：成功点连线，
// 检测失败（success=false 或 latency<=0）点不连线，在底部显示红点。
function LatencySparkline({ history }: { history: CheckHistory[] }) {
  const W = 480
  const H = 80
  const PAD = 5

  const valid = history.filter((h) => h.success && h.latency > 0)
  const maxLat = valid.length ? Math.max(...valid.map((h) => h.latency)) : 1
  const avgLat = valid.length ? Math.round(valid.reduce((sum, h) => sum + h.latency, 0) / valid.length) : 0
  const last = history[history.length - 1]

  const x = (i: number) => PAD + (i * (W - PAD * 2)) / Math.max(history.length - 1, 1)
  const y = (h: CheckHistory) =>
    h.success && h.latency > 0 ? H - PAD - (h.latency / maxLat) * (H - PAD * 2) : H - PAD

  // 失败点打断路径，避免在底部拉出误导性的竖线
  let d = ''
  let penDown = false
  history.forEach((h, i) => {
    if (h.success && h.latency > 0) {
      d += `${penDown ? 'L' : 'M'}${x(i).toFixed(1)},${y(h).toFixed(1)} `
      penDown = true
    } else {
      penDown = false
    }
  })

  return (
    <Box p="sm" style={{ borderRadius: 8, background: 'var(--mantine-color-default-hover)' }}>
      <svg viewBox={`0 0 ${W} ${H}`} style={{ width: '100%', height: 'auto', display: 'block' }}>
        {d && (
          <path
            d={d}
            fill="none"
            stroke="var(--mantine-color-blue-6)"
            strokeWidth={1.5}
            strokeLinejoin="round"
            strokeLinecap="round"
          />
        )}
        {history.map((h, i) =>
          !h.success || h.latency <= 0 ? (
            <circle key={i} cx={x(i)} cy={y(h)} r={2.5} fill="var(--mantine-color-red-6)" />
          ) : null,
        )}
        {last.success && last.latency > 0 && (
          <circle
            cx={x(history.length - 1)}
            cy={y(last)}
            r={3}
            fill="var(--mantine-color-blue-6)"
            stroke="var(--mantine-color-body)"
            strokeWidth={1.5}
          />
        )}
      </svg>
      <Group justify="space-between" mt={4}>
        <Text size="xs" c="dimmed">红点 = 检测失败</Text>
        <Text size="xs" c="dimmed">平均 {avgLat}ms · 峰值 {maxLat}ms</Text>
      </Group>
    </Box>
  )
}

function NodeDetailModal({ node, subs }: { node: ProxyNode; subs: Subscription[] }) {
  const subName = subscriptionName(subs, node.subscriptionId)
  const geo = geoText(node)
  const [history, setHistory] = useState<CheckHistory[]>([])
  const [historyLoading, setHistoryLoading] = useState(true)

  useEffect(() => {
    let cancelled = false
    getProxyHistory(node.id, 60)
      .then((res) => {
        if (!cancelled && res.code === 0) setHistory((res.data as CheckHistory[]) ?? [])
      })
      .catch(() => {})
      .finally(() => {
        if (!cancelled) setHistoryLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [node.id])

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

      <Text size="xs" c="dimmed">延迟趋势（最近 {history.length} 次检测）</Text>
      {historyLoading ? (
        <Text size="xs" c="dimmed">加载中...</Text>
      ) : history.length === 0 ? (
        <Text size="xs" c="dimmed">暂无检测历史</Text>
      ) : (
        <LatencySparkline history={history} />
      )}

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