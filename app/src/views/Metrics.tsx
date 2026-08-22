/* eslint-disable react-hooks/set-state-in-effect */
import { useCallback, useEffect, useState } from 'react'
import { Code, Copy, ExternalLink, RefreshCw, AlertTriangle, Check } from 'lucide-react'
import {
  Alert,
  Box,
  Button,
  Card,
  Group,
  Stack,
  Text,
  Title,
} from '@mantine/core'
import { useStatusStore } from '@/stores/status'
import { notifications } from '@mantine/notifications'

const METRICS_ENDPOINT = '/metrics'

// 打开 Grafana：优先尝试本地 3000 端口，用户可自行配置反向代理或修改端口
// 也可在设置页配置 Grafana 地址（后续扩展）
function openGrafana() {
  // 尝试本地 3000 端口（Grafana 默认端口），路径留空让用户自行跳转到 Dashboards 列表
  const base = window.location.origin.replace(/:\d+$/, ':3000')
  window.open(base, '_blank')
}

export default function Metrics() {
  const running = useStatusStore((s) => s.status.running)
  const [raw, setRaw] = useState<string>('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [copied, setCopied] = useState(false)

  const fetchMetrics = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const res = await fetch(METRICS_ENDPOINT)
      if (!res.ok) throw new Error(`HTTP ${res.status}`)
      const text = await res.text()
      setRaw(text)
    } catch (e) {
      setError(e instanceof Error ? e.message : '获取指标失败')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    if (running) fetchMetrics()
  }, [running, fetchMetrics])

  const copyToClipboard = useCallback(async () => {
    try {
      await navigator.clipboard.writeText(raw)
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    } catch {
      notifications.show({ title: '复制失败', color: 'red', message: '请手动复制' })
    }
  }, [raw])

  return (
    <Stack gap="md" style={{ height: '100%', overflow: 'hidden' }}>
      <Card padding="lg" radius="md" withBorder style={{ flexShrink: 0 }}>
        <Group justify="space-between" wrap="wrap">
          <div>
            <Title order={3}>Prometheus 指标</Title>
            <Text size="sm" c="dimmed" mt={4}>
              实时抓取 proxy-core /metrics 端点，供 Prometheus / Grafana 采集。
            </Text>
            <Alert color="blue" variant="light" mt="sm" icon={<Code size={14} />} style={{ maxWidth: 600 }}>
              <Text size="sm">
                <strong>此页面用途：</strong> 展示 proxy-core 暴露的 Prometheus 格式监控指标（/metrics 端点）。
                这些指标可被 Prometheus 定期抓取，配合 Grafana 仪表盘实现：
                <ul style={{ margin: '8px 0 0 20px', padding: 0 }}>
                  <li>代理池存活率、评分分布、延迟趋势</li>
                  <li>网关流量统计（上传/下载/连接数，按节点/链路分桶）</li>
                  <li>链路健康检测状态与延迟</li>
                  <li>订阅抓取成功率、新增/移除节点数</li>
                  <li>选择器当前策略、节点失败计数</li>
                  <li>系统资源：Goroutine 数、内存占用、运行时长</li>
                </ul>
                <Text size="xs" c="dimmed" mt={4}>
                  配置 Prometheus 抓取目标为 <code>{'http://<proxy-core-host>:17890/metrics'}</code>，
                  在 Grafana 中导入或自建仪表盘即可可视化。
                </Text>
              </Text>
            </Alert>
          </div>
          <Group gap="sm">
            <Button
              leftSection={loading ? <RefreshCw size={16} className="pp-spin" /> : <RefreshCw size={16} />}
              disabled={loading || !running}
              onClick={fetchMetrics}
            >
              {loading ? '刷新中...' : '刷新'}
            </Button>
            <Button
              leftSection={<Copy size={16} />}
              variant={copied ? 'filled' : 'light'}
              color={copied ? 'green' : 'gray'}
              onClick={copyToClipboard}
              disabled={!raw}
            >
              {copied ? '已复制' : '复制全部'}
            </Button>
            <Button
              leftSection={<ExternalLink size={16} />}
              variant="light"
              onClick={openGrafana}
            >
              Grafana 面板
            </Button>
          </Group>
        </Group>
        {!running && (
          <Alert color="yellow" variant="light" mt="sm" icon={<AlertTriangle size={16} />}>
            网关未运行，无法获取指标。请先在「出口路由」页启动网关。
          </Alert>
        )}
        {error && (
          <Alert color="red" variant="light" mt="sm" icon={<AlertTriangle size={16} />}>
            获取失败：{error}
          </Alert>
        )}
        {running && !error && !raw && !loading && (
          <Alert color="blue" variant="light" mt="sm" icon={<Check size={16} />}>
            网关运行中，点击「刷新」获取最新指标。
          </Alert>
        )}
      </Card>

      {raw && (
        <Card padding="md" radius="md" withBorder style={{ flex: 1, minHeight: 0, display: 'flex', flexDirection: 'column' }}>
          <Box style={{ flex: 1, minHeight: 0, overflow: 'auto' }}>
            <pre style={{
              margin: 0,
              padding: '12px',
              fontSize: 12,
              lineHeight: 1.5,
              fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace',
              whiteSpace: 'pre-wrap',
              wordBreak: 'break-word',
              background: 'var(--mantine-color-dark-9)',
              color: 'var(--mantine-color-dark-0)',
              borderRadius: 4,
            }}>
              <code>{raw}</code>
            </pre>
          </Box>
        </Card>
      )}
    </Stack>
  )
}