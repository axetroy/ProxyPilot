import { useEffect, useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { Check, Clipboard, Copy, GitBranch, Play, RefreshCw, Square } from 'lucide-react'
import { Alert, Badge, Button, Card, Code, Divider, Group, Modal, SimpleGrid, Stack, Tabs, Text, TextInput } from '@mantine/core'
import './dashboard.css'
import { useStatusStore } from '@/stores/status'
import { usePoolStore } from '@/stores/pool'
import { getEgressConfig, getPlatform, getTraffic, type Platform } from '@/api'
import { buildGatewayCommands, type GatewayCommandSet } from '@/lib/proxy-commands'
import type { EgressConfig, ProxyNode, TrafficSnapshot } from '@/types'

type NoticeData = { type: 'success' | 'error'; text: string }

export default function Dashboard() {
  const status = useStatusStore((s) => s.status)
  const start = useStatusStore((s) => s.start)
  const stop = useStatusStore((s) => s.stop)
  const checkAll = usePoolStore((s) => s.check)
  const refreshPool = usePoolStore((s) => s.refresh)
  const nodes = usePoolStore((s) => s.nodes)
  const [notice, setNotice] = useState<NoticeData | null>(null)
  const [loading, setLoading] = useState<'check' | 'start' | 'stop' | null>(null)
  const [egress, setEgress] = useState<EgressConfig | null>(null)
  const [traffic, setTraffic] = useState<TrafficSnapshot | null>(null)
  const [copiedKey, setCopiedKey] = useState<'http' | 'socks5' | null>(null)
  const [gatewayModalOpen, setGatewayModalOpen] = useState(false)
  const [platform, setPlatform] = useState<Platform>('linux')
  const [activeTab, setActiveTab] = useState<string | null>('darwin')
  const [copiedCmdKey, setCopiedCmdKey] = useState<string | null>(null)

  const aliveRate = useMemo(() => {
    if (!status.proxyCount) return '-'
    return `${Math.round((status.aliveCount / status.proxyCount) * 100)}%`
  }, [status.aliveCount, status.proxyCount])

  const currentNode = status.currentNode

  function formatNodeAddress(node?: ProxyNode, fallback = '等待首次请求') {
    return node ? `${node.host}:${node.port}` : fallback
  }

  function formatNodeStatus(node?: ProxyNode) {
    if (!node?.status) return '等待'
    if (node.status === 'alive') return '在线'
    if (node.status === 'dead') return '失效'
    if (node.status === 'checking') return '检测中'
    return '待检测'
  }

  function formatNodeProtocol(node?: ProxyNode) {
    return node?.protocol?.toUpperCase() ?? '—'
  }

  function formatNodeLatency(node?: ProxyNode) {
    return node?.latency ? `${node.latency}ms` : '—'
  }

  // 字节数格式化为可读单位（B/KB/MB/GB/TB）。
  function formatBytes(v?: number) {
    if (!v || v <= 0) return '0 B'
    const units = ['B', 'KB', 'MB', 'GB', 'TB']
    const i = Math.min(Math.floor(Math.log(v) / Math.log(1024)), units.length - 1)
    const n = v / 1024 ** i
    return `${n >= 100 || i === 0 ? Math.round(n) : n.toFixed(1)} ${units[i]}`
  }

  // 按节点流量降序取 Top5，观察谁在扛主要流量。
  const topNodes = useMemo(() => {
    if (!traffic) return []
    return [...(traffic.byNode ?? [])]
      .sort((a, b) => b.download + b.upload - (a.download + a.upload))
      .slice(0, 5)
  }, [traffic])

  function nodeLabel(id: number) {
    const n = nodes.find((x) => x.id === id)
    return n ? `${n.host}:${n.port}` : `节点#${id}`
  }

  const stats = [
    { label: '节点总数', value: status.proxyCount },
    { label: '存活节点', value: status.aliveCount },
    { label: '存活率', value: aliveRate },
    { label: '版本', value: status.version },
  ]

  useEffect(() => {
    if (!notice) return
    const timer = window.setTimeout(() => setNotice(null), 4000)
    return () => window.clearTimeout(timer)
  }, [notice])

  useEffect(() => {
    getPlatform()
      .then(setPlatform)
      .catch(() => setPlatform('linux'))
  }, [])

  // 加载并轮询出口策略，保持与「出口路由」页展示一致（策略可能从其他入口变更）。
  useEffect(() => {
    let active = true
    const load = () =>
      getEgressConfig()
        .then((res) => {
          if (active && res.code === 0 && res.data) setEgress(res.data)
        })
        .catch(() => {})
    load()
    const timer = window.setInterval(load, 5000)
    return () => {
      active = false
      window.clearInterval(timer)
    }
  }, [])

  // 轮询网关流量统计（本次启动累计）。
  useEffect(() => {
    let active = true
    const load = () =>
      getTraffic()
        .then((res) => {
          if (active && res.code === 0 && res.data) setTraffic(res.data)
        })
        .catch(() => {})
    load()
    const timer = window.setInterval(load, 5000)
    return () => {
      active = false
      window.clearInterval(timer)
    }
  }, [])

  const gatewaySets = useMemo<GatewayCommandSet[]>(() => {
    const http = formatProxyValue(status.httpProxyBind, 'http')
    const socks5 = formatProxyValue(status.socks5ProxyBind, 'socks5')
    return buildGatewayCommands(http === '未启动' ? 'http://127.0.0.1:7892' : http, socks5 === '未启动' ? 'socks5h://127.0.0.1:7892' : socks5)
  }, [status.httpProxyBind, status.socks5ProxyBind])

  const defaultTab = platform === 'win32' ? 'win32-powershell' : 'darwin'

  async function copyCommand(key: string, text: string) {
    try {
      await navigator.clipboard.writeText(text)
      setCopiedCmdKey(key)
      window.setTimeout(() => setCopiedCmdKey(null), 1500)
    } catch {
      setNotice({ type: 'error', text: '复制失败，请手动复制' })
    }
  }

  async function onCheckAll() {
    try {
      setLoading('check')
      await checkAll()
      setNotice({ type: 'success', text: '检测任务已发起' })
      // 检测结果出来后刷新代理池数据，而不是整页 reload（避免丢状态、闪白屏）
      window.setTimeout(() => refreshPool(undefined, true), 1500)
    } catch {
      setNotice({ type: 'error', text: '检测失败，请稍后重试' })
    } finally {
      setLoading(null)
    }
  }

  async function onStart() {
    try {
      setLoading('start')
      await start()
      setNotice({ type: 'success', text: '网关已启动' })
    } catch {
      setNotice({ type: 'error', text: '启动网关失败' })
    } finally {
      setLoading(null)
    }
  }

  async function onStop() {
    try {
      setLoading('stop')
      await stop()
      setNotice({ type: 'success', text: '网关已停止' })
    } catch {
      setNotice({ type: 'error', text: '停止网关失败' })
    } finally {
      setLoading(null)
    }
  }

  function formatProxyValue(value: string, key: 'http' | 'socks5') {
    if (!value || value === '未启动') return '未启动'
    const trimmed = value.trim()
    if (key === 'http') {
      return trimmed.includes('://') ? trimmed : `http://${trimmed}`
    }
    // SOCKS5 统一使用 socks5h 协议：让代理解析目标域名，
    // 避免客户端本地解析出 IPv6 地址导致上游节点连接失败
    if (trimmed.includes('://')) {
      return trimmed.replace(/^socks5:\/\//, 'socks5h://')
    }
    return `socks5h://${trimmed}`
  }

  async function onCopy(value: string, key: 'http' | 'socks5') {
    const formatted = formatProxyValue(value, key)
    if (!formatted || formatted === '未启动') return
    try {
      await navigator.clipboard.writeText(formatted)
      setCopiedKey(key)
      window.setTimeout(() => setCopiedKey(null), 1500)
      setNotice({ type: 'success', text: '地址已复制到剪贴板' })
    } catch {
      setNotice({ type: 'error', text: '复制失败，请手动复制' })
    }
  }

  return (
    <Stack gap="lg">
      <Card padding="lg" radius="md" withBorder style={{ background: 'linear-gradient(135deg, var(--mantine-primary-color-light), var(--mantine-color-body))' }}>
        <Group justify="space-between" align="flex-start" wrap="wrap">
          <div>
            <Text fw={700} size="lg">运行概览</Text>
            <Text size="sm" c="dimmed" mt={4}>实时查看代理节点健康状态与网关运行情况</Text>
          </div>
          <Badge color={status.running ? 'green' : 'gray'} variant="light">
            {status.running ? '网关在线' : '网关已停机'}
          </Badge>
        </Group>
      </Card>

      <SimpleGrid cols={{ base: 1, md: 4 }} spacing="md">
        {stats.map((s) => (
          <Card key={s.label} padding="lg" radius="md" withBorder>
            <Text size="sm" c="dimmed">{s.label}</Text>
            <Text fw={700} size="xl" mt="xs">{s.value}</Text>
          </Card>
        ))}
      </SimpleGrid>

      {notice && (
        <Alert color={notice.type === 'success' ? 'green' : 'red'} title={notice.type === 'success' ? '成功' : '失败'} withCloseButton onClose={() => setNotice(null)}>
          {notice.text}
        </Alert>
      )}

      <Card padding="lg" radius="md" withBorder>
        <Group justify="space-between" align="flex-start" wrap="wrap">
          <Group gap="sm" align="flex-start" wrap="nowrap">
            <div>
              <Text fw={600}>出口策略</Text>
              <Text size="sm" c="dimmed" mt={2}>
                {egress?.strategies.find((s) => s.value === egress.strategy)?.label ?? '—'}
              </Text>
              <Text size="sm" c="dimmed" mt={2} style={{ maxWidth: 520 }}>
                {egress?.strategies.find((s) => s.value === egress.strategy)?.desc}
              </Text>
              {egress?.strategy === 'fixed' && egress.pinnedNode && (
                <Group gap="xs" mt={6} wrap="nowrap">
                  <Badge variant="light" color="blue">固定出口</Badge>
                  <Text size="sm">
                    {`${egress.pinnedNode.host}:${egress.pinnedNode.port}`}
                    <Text span c="dimmed" size="sm">
                      {`（评分 ${egress.pinnedNode.score}）`}
                    </Text>
                  </Text>
                  {egress.pinnedNode.status !== 'alive' && (
                    <Text size="sm" c="red">当前不可用，流量临时回退智能加权</Text>
                  )}
                </Group>
              )}
            </div>
          </Group>
          <Button component={Link} to="/egress" size="sm" variant="light" leftSection={<GitBranch size={14} />}>
            管理出口路由
          </Button>
        </Group>
      </Card>

      <Card padding="lg" radius="md" withBorder>
        <Group justify="space-between" align="center" wrap="wrap">
          <div>
            <Text fw={600}>操作中心</Text>
            <Text size="sm" c="dimmed">一键检测节点、启动或停止网关</Text>
          </div>
          <Group gap="sm" wrap="wrap">
            <Button leftSection={<RefreshCw size={16} />} loading={loading === 'check'} onClick={onCheckAll}>
              {loading === 'check' ? '检测中...' : '检测全部代理'}
            </Button>
            {status.running ? (
              <Button color="red" leftSection={<Square size={16} />} loading={loading === 'stop'} onClick={onStop}>
                {loading === 'stop' ? '停止中...' : '停止网关'}
              </Button>
            ) : (
              <Button leftSection={<Play size={16} />} loading={loading === 'start'} onClick={onStart}>
                {loading === 'start' ? '启动中...' : '启动网关'}
              </Button>
            )}
          </Group>
        </Group>
      </Card>

      <Card padding="lg" radius="md" withBorder>
        <Group justify="space-between" align="flex-start" wrap="wrap">
          <div>
            <Text fw={600}>对外访问入口</Text>
            <Text size="sm" c="dimmed" mt={4}>{status.running ? '网关已经启动，可以直接把下面地址配置到你的客户端或浏览器代理中。' : '启动网关后，下面的入口地址会自动显示出来，方便你直接使用。'}</Text>
          </div>
          <Badge color={status.running ? 'green' : 'gray'} variant="light">
            {status.running ? '可直接使用' : '等待启动'}
          </Badge>
        </Group>

        <SimpleGrid cols={{ base: 1, md: 1 }} spacing="md" mt="md">
          <Card padding="md" radius="md" withBorder>
            <Group justify="space-between" align="flex-start" wrap="wrap">
              <div>
                <Text size="sm" c="dimmed">当前出口节点</Text>
                <Text fw={600} size="lg" mt="xs">{formatNodeAddress(currentNode, '等待首次请求')}</Text>
              </div>
              <Badge color={currentNode ? 'green' : 'gray'} variant="light">
                {currentNode ? '已命中' : '等待命中'}
              </Badge>
            </Group>
            <SimpleGrid cols={{ base: 1, sm: 4 }} spacing="xs" mt="md">
              <div>
                <Text size="xs" c="dimmed">协议</Text>
                <Text size="sm" mt={4}>{formatNodeProtocol(currentNode)}</Text>
              </div>
              <div>
                <Text size="xs" c="dimmed">评分</Text>
                <Text size="sm" mt={4}>{currentNode?.score ?? '—'}</Text>
              </div>
              <div>
                <Text size="xs" c="dimmed">状态</Text>
                <Text size="sm" mt={4}>{formatNodeStatus(currentNode)}</Text>
              </div>
              <div>
                <Text size="xs" c="dimmed">延迟</Text>
                <Text size="sm" mt={4}>{formatNodeLatency(currentNode)}</Text>
              </div>
            </SimpleGrid>
          </Card>
        </SimpleGrid>

        <SimpleGrid cols={{ base: 1, md: 1 }} spacing="md" mt="md">
          <Card padding="md" radius="md" withBorder>
            <Text size="sm" c="dimmed">HTTP / SOCKS5 代理（共用同一端口）</Text>
            <TextInput readOnly value={formatProxyValue(status.httpProxyBind, 'http')} mt="xs" />
            <Group gap="xs" mt="xs">
              <Button
                size="xs"
                variant="light"
                color={copiedKey === 'http' ? 'green' : 'blue'}
                className={copiedKey === 'http' ? 'dashboard-copy-btn success' : 'dashboard-copy-btn'}
                leftSection={<Clipboard size={14} />}
                onClick={() => onCopy(status.httpProxyBind, 'http')}
              >
                {copiedKey === 'http' ? '已复制' : '复制 HTTP 地址'}
              </Button>
              <Button
                size="xs"
                variant="light"
                color={copiedKey === 'socks5' ? 'green' : 'blue'}
                className={copiedKey === 'socks5' ? 'dashboard-copy-btn success' : 'dashboard-copy-btn'}
                leftSection={<Clipboard size={14} />}
                onClick={() => onCopy(status.socks5ProxyBind, 'socks5')}
              >
                {copiedKey === 'socks5' ? '已复制' : '复制 SOCKS5 地址'}
              </Button>
              <Button
                size="xs"
                variant="light"
                disabled={!status.running}
                leftSection={<Copy size={14} />}
                onClick={() => {
                  setActiveTab(defaultTab)
                  setGatewayModalOpen(true)
                }}
              >
                复制命令
              </Button>
              <Text size="xs" c="dimmed">默认 7892，被占用时自动顺延</Text>
            </Group>
            <Text size="xs" c="dimmed" mt="xs">HTTP 与 SOCKS5 共用同一端口，按连接首字节自动识别分流；浏览器/系统代理用 HTTP，Clash、Telegram 等用 SOCKS5</Text>
          </Card>
        </SimpleGrid>
      </Card>

      <Card padding="lg" radius="md" withBorder>
        <Group justify="space-between" align="flex-start" wrap="wrap">
          <div>
            <Text fw={600}>流量统计</Text>
            <Text size="sm" c="dimmed" mt={4}>本次启动累计的代理转发流量（UDP 中继暂不统计）</Text>
          </div>
          <Badge variant="light" color={traffic && traffic.total.connections > 0 ? 'green' : 'gray'}>
            {traffic && traffic.total.connections > 0 ? '累计中' : '暂无流量'}
          </Badge>
        </Group>

        <SimpleGrid cols={{ base: 1, sm: 3 }} spacing="md" mt="md">
          <Card padding="md" radius="md" withBorder>
            <Text size="sm" c="dimmed">上传</Text>
            <Text fw={700} size="lg" mt={4}>{formatBytes(traffic?.total.upload)}</Text>
          </Card>
          <Card padding="md" radius="md" withBorder>
            <Text size="sm" c="dimmed">下载</Text>
            <Text fw={700} size="lg" mt={4}>{formatBytes(traffic?.total.download)}</Text>
          </Card>
          <Card padding="md" radius="md" withBorder>
            <Text size="sm" c="dimmed">连接数</Text>
            <Text fw={700} size="lg" mt={4}>{traffic?.total.connections ?? 0}</Text>
          </Card>
        </SimpleGrid>

        {topNodes.length > 0 || (traffic && (traffic.byChain ?? []).length > 0) ? (
          <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="md" mt="md">
            {topNodes.length > 0 && (
              <Card padding="md" radius="md" withBorder>
                <Text size="sm" c="dimmed" mb="xs">节点流量 Top {topNodes.length}</Text>
                <Stack gap="xs">
                  {topNodes.map((n) => (
                    <Group key={n.id} justify="space-between" wrap="nowrap">
                      <Text size="sm" truncate style={{ flex: 1, minWidth: 0 }}>{nodeLabel(n.id)}</Text>
                      <Group gap="xs" wrap="nowrap">
                        <Text size="xs" c="dimmed">↑{formatBytes(n.upload)}</Text>
                        <Text size="xs" c="dimmed">↓{formatBytes(n.download)}</Text>
                        <Text size="xs" c="dimmed">{n.connections} 连接</Text>
                      </Group>
                    </Group>
                  ))}
                </Stack>
              </Card>
            )}
            {traffic && (traffic.byChain ?? []).length > 0 && (
              <Card padding="md" radius="md" withBorder>
                <Text size="sm" c="dimmed" mb="xs">链路流量</Text>
                <Stack gap="xs">
                  {(traffic.byChain ?? []).map((c) => (
                    <Group key={c.name} justify="space-between" wrap="nowrap">
                      <Text size="sm" truncate style={{ flex: 1, minWidth: 0 }}>
                        {c.name === 'auto-chain' ? '自动链路' : c.name}
                      </Text>
                      <Group gap="xs" wrap="nowrap">
                        <Text size="xs" c="dimmed">↑{formatBytes(c.upload)}</Text>
                        <Text size="xs" c="dimmed">↓{formatBytes(c.download)}</Text>
                        <Text size="xs" c="dimmed">{c.connections} 连接</Text>
                      </Group>
                    </Group>
                  ))}
                </Stack>
              </Card>
            )}
          </SimpleGrid>
        ) : null}
      </Card>

      <Modal
        opened={gatewayModalOpen}
        onClose={() => setGatewayModalOpen(false)}
        title="复制网关命令"
        size="lg"
      >
        <Text size="sm" c="dimmed" mb="md">
          网关同时提供 HTTP 与 SOCKS5 两个出口，选择你的平台复制对应命令即可；SOCKS5 命令同样能处理 HTTP 请求。
        </Text>
        <Tabs value={activeTab} onChange={setActiveTab}>
          <Tabs.List>
            <Tabs.Tab value="win32-powershell">Windows PowerShell</Tabs.Tab>
            <Tabs.Tab value="win32-cmd">Windows CMD</Tabs.Tab>
            <Tabs.Tab value="darwin">macOS / Linux</Tabs.Tab>
          </Tabs.List>

          {gatewaySets.map((set) => {
            const tabKey = set.platform === 'win32' ? (set.label.includes('CMD') ? 'win32-cmd' : 'win32-powershell') : 'darwin'
            return (
              <Tabs.Panel key={tabKey} value={tabKey} pt="md">
                <Stack gap="md">
                  <div>
                    <Group justify="space-between" align="center" mb={4}>
                      <Text size="sm" fw={600}>HTTP 代理命令</Text>
                      <Badge color="blue" variant="light" size="xs">HTTP_PROXY / HTTPS_PROXY / ALL_PROXY</Badge>
                    </Group>
                    <Code block style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-all' }}>
                      {set.httpEnv}
                    </Code>
                    <Group justify="flex-end" mt={4}>
                      <Button
                        size="xs"
                        variant="light"
                        color={copiedCmdKey === `${tabKey}-http` ? 'green' : 'blue'}
                        leftSection={copiedCmdKey === `${tabKey}-http` ? <Check size={14} /> : <Copy size={14} />}
                        onClick={() => copyCommand(`${tabKey}-http`, set.httpEnv)}
                      >
                        {copiedCmdKey === `${tabKey}-http` ? '已复制' : '复制 HTTP 命令'}
                      </Button>
                    </Group>
                  </div>
                  <Divider />
                  <div>
                    <Group justify="space-between" align="center" mb={4}>
                      <Text size="sm" fw={600}>SOCKS5 代理命令</Text>
                      <Badge color="grape" variant="light" size="xs">HTTP_PROXY / HTTPS_PROXY / ALL_PROXY</Badge>
                    </Group>
                    <Code block style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-all' }}>
                      {set.socks5Env}
                    </Code>
                    <Group justify="flex-end" mt={4}>
                      <Button
                        size="xs"
                        variant="light"
                        color={copiedCmdKey === `${tabKey}-socks5` ? 'green' : 'blue'}
                        leftSection={copiedCmdKey === `${tabKey}-socks5` ? <Check size={14} /> : <Copy size={14} />}
                        onClick={() => copyCommand(`${tabKey}-socks5`, set.socks5Env)}
                      >
                        {copiedCmdKey === `${tabKey}-socks5` ? '已复制' : '复制 SOCKS5 命令'}
                      </Button>
                    </Group>
                  </div>
                </Stack>
              </Tabs.Panel>
            )
          })}
        </Tabs>
      </Modal>
    </Stack>
  )
}