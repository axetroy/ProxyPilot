import { useEffect, useState } from 'react'
import { Monitor, Moon, Play, Square, Sun } from 'lucide-react'
import { Alert, Button, Card, Divider, Group, SegmentedControl, Stack, Text, TextInput, useMantineColorScheme } from '@mantine/core'
import { useStatusStore } from '@/stores/status'

type NoticeData = { type: 'success' | 'error'; text: string }

export default function Settings() {
  const status = useStatusStore((s) => s.status)
  const refresh = useStatusStore((s) => s.refresh)
  const start = useStatusStore((s) => s.start)
  const stop = useStatusStore((s) => s.stop)
  const { colorScheme, setColorScheme } = useMantineColorScheme()
  const [notice, setNotice] = useState<NoticeData | null>(null)
  const [loading, setLoading] = useState<'start' | 'stop' | null>(null)

  useEffect(() => {
    refresh()
  }, [refresh])

  useEffect(() => {
    if (!notice) return
    const timer = window.setTimeout(() => setNotice(null), 4000)
    return () => window.clearTimeout(timer)
  }, [notice])

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

  const fields = [
    { label: '核心 API', value: 'http://127.0.0.1:17890' },
    { label: 'HTTP 代理', value: '127.0.0.1:7892（默认，被占用自动顺延）' },
    { label: 'SOCKS5 代理', value: '127.0.0.1:7893（默认，被占用自动顺延）' },
  ]

  return (
    <Card padding="lg" radius="md" withBorder style={{ maxWidth: 620 }}>
      <Stack gap="md">
        <div>
          <Text fw={700}>系统设置</Text>
          <Text size="sm" c="dimmed" mt={4}>查看代理服务地址与当前版本信息</Text>
        </div>

        <Divider />

        <div>
          <Group gap="xs" mb={4}>
            {colorScheme === 'dark' ? <Moon size={16} /> : colorScheme === 'light' ? <Sun size={16} /> : <Monitor size={16} />}
            <Text fw={600}>外观</Text>
          </Group>
          <Text size="sm" c="dimmed" mb="sm">选择应用的明暗主题，切换后立即生效并自动保存</Text>
          <SegmentedControl
            value={colorScheme}
            onChange={(value) => setColorScheme(value as 'light' | 'dark' | 'auto')}
            data={[
              { label: '亮色', value: 'light' },
              { label: '暗色', value: 'dark' },
              { label: '跟随系统', value: 'auto' },
            ]}
          />
        </div>

        <Divider />

        {fields.map((f) => (
          <TextInput key={f.label} label={f.label} value={f.value} disabled />
        ))}
        <TextInput label="版本" value={status.version || '-'} disabled />
        {notice && (
          <Alert color={notice.type === 'success' ? 'green' : 'red'} withCloseButton onClose={() => setNotice(null)}>
            {notice.text}
          </Alert>
        )}
        <Group>
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
      </Stack>
    </Card>
  )
}