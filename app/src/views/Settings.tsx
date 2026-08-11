import { useCallback, useEffect, useMemo, useState } from 'react'
import { Monitor, Moon, Play, RotateCcw, Square, Sun } from 'lucide-react'
import {
  Alert,
  Button,
  Card,
  Divider,
  Group,
  SegmentedControl,
  Stack,
  Text,
  TextInput,
  useMantineColorScheme,
} from '@mantine/core'
import { useStatusStore } from '@/stores/status'
import { getPlatform, listSettings, updateSettings, type Platform } from '@/api'
import type { SettingItem } from '@/types'

type NoticeData = { type: 'success' | 'error'; text: string }

export default function Settings() {
  const status = useStatusStore((s) => s.status)
  const refresh = useStatusStore((s) => s.refresh)
  const start = useStatusStore((s) => s.start)
  const stop = useStatusStore((s) => s.stop)
  const { colorScheme, setColorScheme } = useMantineColorScheme()
  const [notice, setNotice] = useState<NoticeData | null>(null)
  const [loading, setLoading] = useState<'start' | 'stop' | 'save' | null>(null)
  const [settings, setSettings] = useState<SettingItem[]>([])
  const [draft, setDraft] = useState<Record<string, string>>({})
  const [saving, setSaving] = useState(false)
  const [platform, setPlatform] = useState<Platform>('linux')

  const load = useCallback(async () => {
    const maxRetries = 10;
    for (let i = 0; i < maxRetries; i++) {
      try {
        const res = await listSettings()
        if (res.code === 0 && res.data) {
          setSettings(res.data)
          setDraft(Object.fromEntries(res.data.map((s) => [s.key, s.value])))
          return
        }
      } catch {
        // ignore, retry
      }
      // wait a bit before retry
      await new Promise(resolve => setTimeout(resolve, 500))
    }
    setNotice({ type: 'error', text: '加载配置失败' })
  }, [])

  useEffect(() => {
    refresh()
    load()
  }, [refresh, load])

  useEffect(() => {
    getPlatform().then(setPlatform).catch(() => setPlatform('linux'))
  }, [])

  useEffect(() => {
    if (!notice) return
    const timer = window.setTimeout(() => setNotice(null), 4000)
    return () => window.clearTimeout(timer)
  }, [notice])

  const fields = useMemo(
    () => [
      { label: '核心 API', value: 'http://127.0.0.1:17890' },
      { label: '代理（HTTP / SOCKS5 共用，当前）', value: status.httpProxyBind || '-' },
    ],
    [status.httpProxyBind],
  )

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

  // 重置为默认值
  function onReset(key: string, def: string) {
    setDraft((d) => ({ ...d, [key]: def }))
  }

  // 计算是否有未保存的变更
  const hasChanges = settings.some((s) => draft[s.key] !== s.value)

  async function onSave() {
    const changed: Record<string, string> = {}
    for (const s of settings) {
      if (draft[s.key] !== s.value) {
        changed[s.key] = draft[s.key]
      }
    }
    if (Object.keys(changed).length === 0) return
    setSaving(true)
    try {
      const res = await updateSettings(changed)
      if (res.code === 0 && res.data) {
        setSettings(res.data.settings)
        setDraft(Object.fromEntries(res.data.settings.map((s) => [s.key, s.value])))
        setNotice({ type: 'success', text: res.data.changed ? '配置已保存并生效' : '配置已保存' })
        // 端口可能变化，刷新状态以展示新的实际绑定地址
        window.setTimeout(() => refresh(), 800)
      } else {
        setNotice({ type: 'error', text: res.msg || '保存失败' })
      }
    } catch (e) {
      const err = e as { response?: { data?: { msg?: string } } }
      setNotice({ type: 'error', text: err.response?.data?.msg || '保存失败' })
    } finally {
      setSaving(false)
    }
  }

  return (
    <Stack gap="md">
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
        </Stack>
      </Card>

      <Card padding="lg" radius="md" withBorder style={{ maxWidth: 620 }}>
        <Stack gap="md">
          <div>
            <Text fw={700}>核心配置</Text>
            <Text size="sm" c="dimmed" mt={4}>修改 proxy-core 运行参数，保存后立即生效</Text>
          </div>

          {settings.map((s) => (
            <TextInput
              key={s.key}
              label={s.desc}
              description={
                s.key === 'proxy_port' ? '端口被占用会自动顺延，实际端口见上方「当前」地址；HTTP 与 SOCKS5 共用此端口' : undefined
              }
              value={draft[s.key] ?? ''}
              onChange={(e) => {
                // 必须在事件处理器内同步读取 value：
                // setDraft 的函数更新器会在渲染阶段才执行，届时 e.currentTarget 已被 React 置为 null
                const value = e.currentTarget.value
                setDraft((d) => ({ ...d, [s.key]: value }))
              }}
              rightSection={
                draft[s.key] !== s.default ? (
                  <Button size="xs" variant="subtle" onClick={() => onReset(s.key, s.default)}>
                    重置
                  </Button>
                ) : undefined
              }
            />
          ))}

          {notice && (
            <Alert color={notice.type === 'success' ? 'green' : 'red'} withCloseButton onClose={() => setNotice(null)}>
              {notice.text}
            </Alert>
          )}

          <Group justify="space-between">
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
            <Group>
              <Button
                variant="light"
                leftSection={<RotateCcw size={16} />}
                disabled={!hasChanges || saving}
                onClick={() => setDraft(Object.fromEntries(settings.map((s) => [s.key, s.value])))}
              >
                撤销
              </Button>
              <Button loading={saving} disabled={!hasChanges} onClick={onSave}>
                {saving ? '保存中...' : '保存配置'}
              </Button>
            </Group>
          </Group>
        </Stack>
      </Card>
    </Stack>
  )
}