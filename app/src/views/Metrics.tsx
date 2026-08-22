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

  const openGrafana = () => {
    const base = window.location.origin.replace(/:\d+$/, ':3000')
    window.open(`${base}/d/proxypilot/proxypilot-overview`, '_blank')
  }

  return (
    <Stack gap="md" style={{ height: '100%', overflow: 'hidden' }}>
      <Card padding="lg" radius="md" withBorder style={{ flexShrink: 0 }}>
        <Group justify="space-between" wrap="wrap">
          <div>
            <Title order={3}>Prometheus 指标</Title>
            <Text size="sm" c="dimmed" mt={4}>
              实时抓取 proxy-core /metrics 端点，供 Prometheus / Grafana 采集。
            </Text>
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