import { useEffect, useMemo, useState } from 'react'
import { Check, Clipboard, Copy, Play, RefreshCw, Square } from 'lucide-react'
import { Alert, Badge, Button, Card, Code, Group, Modal, SimpleGrid, Stack, Tabs, Text, TextInput } from '@mantine/core'
import './dashboard.css'
import { useStatusStore } from '@/stores/status'
import { usePoolStore } from '@/stores/pool'
import { getPlatform, type Platform } from '@/api'
import { buildGatewayCommands, type GatewayCommandSet } from '@/lib/proxy-commands'
import type { ProxyNode } from '@/types'

type NoticeData = { type: 'success' | 'error'; text: string }

export default function Dashboard() {
  const status = useStatusStore((s) => s.status)
  const start = useStatusStore((s) => s.start)
  const stop = useStatusStore((s) => s.stop)
  const checkAll = usePoolStore((s) => s.check)
  const [notice, setNotice] = useState<NoticeData | null>(null)
  const [loading, setLoading] = useState<'check' | 'start' | 'stop' | null>(null)
  const [copiedKey, setCopiedKey] = useState<'http' | 'socks5' | null>(null)
  const [gatewayModalOpen, setGatewayModalOpen] = useState(false)
  const [platform, setPlatform] = useState<Platform>('linux')
  const [activeTab, setActiveTab] = useState<string | null>('darwin')
  const [copiedCmdKey, setCopiedCmdKey] = useState<string | null>(null)

  const aliveRate = useMemo(() => {
    if (!status.proxyCount) return '-'
    return `${Math.round((status.aliveCount / status.proxyCount) * 100)}%`
  }, [status.aliveCount, status.proxyCount])

  const currentHttpNode = status.currentHttpNode
  const currentSocks5Node = status.currentSocks5Node

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

  const gatewaySets = useMemo<GatewayCommandSet[]>(() => {
    const http = formatProxyValue(status.httpProxyBind, 'http')
    const socks5 = formatProxyValue(status.socks5ProxyBind, 'socks5')
    return buildGatewayCommands(http === '未启动' ? 'http://127.0.0.1:7890' : http, socks5 === '未启动' ? 'socks5://127.0.0.1:7891' : socks5)
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
      window.setTimeout(() => window.location.reload(), 1500)
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
    return trimmed.includes('://') ? trimmed : `socks5://${trimmed}`
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

        <SimpleGrid cols={{ base: 1, md: 2 }} spacing="md" mt="md">
          <Card padding="md" radius="md" withBorder>
            <Group justify="space-between" align="flex-start" wrap="wrap">
              <div>
                <Text size="sm" c="dimmed">当前 HTTP 上游代理</Text>
                <Text fw={600} size="lg" mt="xs">{formatNodeAddress(currentHttpNode, '等待首次 HTTP 请求')}</Text>
              </div>
              <Badge color={currentHttpNode ? 'green' : 'gray'} variant="light">
                {currentHttpNode ? '已命中' : '等待命中'}
              </Badge>
            </Group>
            <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="xs" mt="md">
              <div>
                <Text size="xs" c="dimmed">协议</Text>
                <Text size="sm" mt={4}>{formatNodeProtocol(currentHttpNode)}</Text>
              </div>
              <div>
                <Text size="xs" c="dimmed">评分</Text>
                <Text size="sm" mt={4}>{currentHttpNode?.score ?? '—'}</Text>
              </div>
            </SimpleGrid>
            <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="xs" mt="md">
              <div>
                <Text size="xs" c="dimmed">状态</Text>
                <Text size="sm" mt={4}>{formatNodeStatus(currentHttpNode)}</Text>
              </div>
              <div>
                <Text size="xs" c="dimmed">延迟</Text>
                <Text size="sm" mt={4}>{formatNodeLatency(currentHttpNode)}</Text>
              </div>
            </SimpleGrid>
          </Card>

          <Card padding="md" radius="md" withBorder>
            <Group justify="space-between" align="flex-start" wrap="wrap">
              <div>
                <Text size="sm" c="dimmed">当前 SOCKS5 上游代理</Text>
                <Text fw={600} size="lg" mt="xs">{formatNodeAddress(currentSocks5Node, '等待首次 SOCKS5 请求')}</Text>
              </div>
              <Badge color={currentSocks5Node ? 'green' : 'gray'} variant="light">
                {currentSocks5Node ? '已命中' : '等待命中'}
              </Badge>
            </Group>
            <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="xs" mt="md">
              <div>
                <Text size="xs" c="dimmed">协议</Text>
                <Text size="sm" mt={4}>{formatNodeProtocol(currentSocks5Node)}</Text>
              </div>
              <div>
                <Text size="xs" c="dimmed">评分</Text>
                <Text size="sm" mt={4}>{currentSocks5Node?.score ?? '—'}</Text>
              </div>
            </SimpleGrid>
            <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="xs" mt="md">
              <div>
                <Text size="xs" c="dimmed">状态</Text>
                <Text size="sm" mt={4}>{formatNodeStatus(currentSocks5Node)}</Text>
              </div>
              <div>
                <Text size="xs" c="dimmed">延迟</Text>
                <Text size="sm" mt={4}>{formatNodeLatency(currentSocks5Node)}</Text>
              </div>
            </SimpleGrid>
          </Card>
        </SimpleGrid>

        <SimpleGrid cols={{ base: 1, md: 2 }} spacing="md" mt="md">
          <Card padding="md" radius="md" withBorder>
            <Text size="sm" c="dimmed">HTTP 代理</Text>
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
                {copiedKey === 'http' ? '已复制' : '复制地址'}
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
              <Text size="xs" c="dimmed">示例：http://127.0.0.1:7890</Text>
            </Group>
            <Text size="xs" c="dimmed" mt="xs">适合系统代理、浏览器代理、HTTP 客户端</Text>
          </Card>
          <Card padding="md" radius="md" withBorder>
            <Text size="sm" c="dimmed">SOCKS5 代理</Text>
            <TextInput readOnly value={formatProxyValue(status.socks5ProxyBind, 'socks5')} mt="xs" />
            <Group gap="xs" mt="xs">
              <Button
                size="xs"
                variant="light"
                color={copiedKey === 'socks5' ? 'green' : 'blue'}
                className={copiedKey === 'socks5' ? 'dashboard-copy-btn success' : 'dashboard-copy-btn'}
                leftSection={<Clipboard size={14} />}
                onClick={() => onCopy(status.socks5ProxyBind, 'socks5')}
              >
                {copiedKey === 'socks5' ? '已复制' : '复制地址'}
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
              <Text size="xs" c="dimmed">示例：socks5://127.0.0.1:7891</Text>
            </Group>
            <Text size="xs" c="dimmed" mt="xs">适合 Clash、Telegram、浏览器扩展等 SOCKS5 场景</Text>
          </Card>
        </SimpleGrid>
      </Card>

      <Modal
        opened={gatewayModalOpen}
        onClose={() => setGatewayModalOpen(false)}
        title="复制网关命令"
        size="lg"
      >
        <Text size="sm" c="dimmed" mb="md">
          网关同时提供 HTTP 与 SOCKS5 两个出口：HTTP_PROXY/HTTPS_PROXY 走 HTTP 出口，ALL_PROXY 走 SOCKS5 出口。选择你的平台复制对应命令。
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
                <Text size="sm" fw={600} mb={4}>设置环境变量</Text>
                <Code block style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-all' }}>
                  {set.env}
                </Code>
                <Group justify="flex-end" mt={4}>
                  <Button
                    size="xs"
                    variant="light"
                    color={copiedCmdKey === `${tabKey}-env` ? 'green' : 'blue'}
                    leftSection={copiedCmdKey === `${tabKey}-env` ? <Check size={14} /> : <Copy size={14} />}
                    onClick={() => copyCommand(`${tabKey}-env`, set.env)}
                  >
                    {copiedCmdKey === `${tabKey}-env` ? '已复制' : '复制'}
                  </Button>
                </Group>
              </Tabs.Panel>
            )
          })}
        </Tabs>
      </Modal>
    </Stack>
  )
}