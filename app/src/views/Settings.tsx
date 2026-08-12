import { useCallback, useEffect, useMemo, useState } from 'react'
import { Copy, Minimize2, Monitor, Moon, Play, RotateCcw, Square, Sun } from 'lucide-react'
import {
  Alert,
  Button,
  Card,
  Divider,
  Group,
  SegmentedControl,
  Select,
  Stack,
  Switch,
  Text,
  TextInput,
  useMantineColorScheme,
} from '@mantine/core'
import { useStatusStore } from '@/stores/status'
import { getErrorMessage, getPlatform, getSubscriptionConfig, listSettings, updateSettings, updateSubscriptionConfig, type Platform } from '@/api'
import type { AppSettings, SettingItem, SubscriptionExportConfig } from '@/types'

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
  const [appSettings, setAppSettings] = useState<AppSettings>({ closeBehavior: 'minimize' })
  // 订阅服务配置（独立于通用设置表单）
  const [subConfig, setSubConfig] = useState<SubscriptionExportConfig | null>(null)
  const [subListenDraft, setSubListenDraft] = useState('')
  const [subSaving, setSubSaving] = useState(false)

    // 仅负责取数（重试逻辑），不直接 setState，由调用方在异步回调中写入 state
  const load = useCallback(async () => {
    const maxRetries = 10;
    for (let i = 0; i < maxRetries; i++) {
      try {
        const res = await listSettings()
        if (res.code === 0 && res.data) {
          // 订阅服务配置由独立卡片管理，从通用表单中过滤掉
          const general = res.data.filter((s) => !s.key.startsWith('subscription_'))
          return {
            settings: general,
            draft: Object.fromEntries(general.map((s) => [s.key, s.value])),
          }
        }
      } catch {
        // ignore, retry
      }
      // wait a bit before retry
      await new Promise(resolve => setTimeout(resolve, 500))
    }
    return null
  }, [])

  useEffect(() => {
    refresh()
    load().then((data) => {
      if (data) {
        setSettings(data.settings)
        setDraft(data.draft)
      } else {
        setNotice({ type: 'error', text: '加载配置失败' })
      }
    })
  }, [refresh, load])

  useEffect(() => {
    getPlatform().then(setPlatform).catch(() => setPlatform('linux'))
  }, [])

  useEffect(() => {
    window.proxypilot?.getAppSettings().then(setAppSettings).catch(() => {})
  }, [])

  // 加载订阅服务配置
  useEffect(() => {
    getSubscriptionConfig()
      .then((res) => {
        if (res.code === 0 && res.data) {
          setSubConfig(res.data)
          setSubListenDraft(res.data.listen)
        }
      })
      .catch(() => {})
  }, [])

  async function onChangeCloseBehavior(value: string) {
    const next: AppSettings = { closeBehavior: value as 'minimize' | 'quit' }
    setAppSettings(next)
    try {
      await window.proxypilot?.setAppSettings(next)
      setNotice({ type: 'success', text: '窗口行为设置已保存' })
    } catch {
      setNotice({ type: 'error', text: '保存窗口行为设置失败' })
    }
  }

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

  // ---------- 订阅服务 ----------

  async function onToggleSub(enabled: boolean) {
    if (!subConfig) return
    setSubSaving(true)
    try {
      const res = await updateSubscriptionConfig({ enabled })
      if (res.code === 0 && res.data) {
        setSubConfig(res.data)
        setNotice({ type: 'success', text: enabled ? '订阅服务已开启' : '订阅服务已关闭' })
      } else {
        setNotice({ type: 'error', text: res.msg || '更新失败' })
      }
    } catch (e) {
      setNotice({ type: 'error', text: getErrorMessage(e) })
    } finally {
      setSubSaving(false)
    }
  }

  async function onSaveSubListen() {
    if (!subConfig) return
    setSubSaving(true)
    try {
      const res = await updateSubscriptionConfig({ listen: subListenDraft })
      if (res.code === 0 && res.data) {
        setSubConfig(res.data)
        setSubListenDraft(res.data.listen)
        setNotice({ type: 'success', text: '监听地址已保存，重启应用后生效' })
      } else {
        setNotice({ type: 'error', text: res.msg || '保存失败' })
      }
    } catch (e) {
      setNotice({ type: 'error', text: getErrorMessage(e) })
    } finally {
      setSubSaving(false)
    }
  }

  async function onSaveSubHost(host: string) {
    if (!subConfig) return
    setSubSaving(true)
    try {
      const res = await updateSubscriptionConfig({ host })
      if (res.code === 0 && res.data) {
        setSubConfig(res.data)
        setNotice({ type: 'success', text: host ? '对外 IP 已更新' : '已清除对外 IP（回退 127.0.0.1）' })
      } else {
        setNotice({ type: 'error', text: res.msg || '保存失败' })
      }
    } catch (e) {
      setNotice({ type: 'error', text: getErrorMessage(e) })
    } finally {
      setSubSaving(false)
    }
  }

  async function onResetSubToken() {
    if (!subConfig) return
    setSubSaving(true)
    try {
      const res = await updateSubscriptionConfig({ resetToken: true })
      if (res.code === 0 && res.data) {
        setSubConfig(res.data)
        setNotice({ type: 'success', text: '订阅密钥已重置，请更新客户端中的订阅地址' })
      } else {
        setNotice({ type: 'error', text: res.msg || '重置失败' })
      }
    } catch (e) {
      setNotice({ type: 'error', text: getErrorMessage(e) })
    } finally {
      setSubSaving(false)
    }
  }

  async function copySubUrl() {
    if (!subConfig) return
    try {
      await navigator.clipboard.writeText(subConfig.url)
      setNotice({ type: 'success', text: '订阅地址已复制' })
    } catch {
      setNotice({ type: 'error', text: '复制失败，请手动复制' })
    }
  }

  async function copySubPlainUrl() {
    if (!subConfig) return
    try {
      await navigator.clipboard.writeText(`${subConfig.url}?format=plain`)
      setNotice({ type: 'success', text: '明文订阅地址已复制' })
    } catch {
      setNotice({ type: 'error', text: '复制失败，请手动复制' })
    }
  }

  // 监听地址是否为通配（0.0.0.0/::）：通配时用户需从本机局域网 IP 中选择对外地址
  function isWildcardListen(listen: string): boolean {
    const host = listen.split(':')[0]
    return host === '' || host === '0.0.0.0' || host === '::' || host === '[::]'
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

          <div>
            <Group gap="xs" mb={4}>
              <Minimize2 size={16} />
              <Text fw={600}>窗口行为</Text>
            </Group>
            <Text size="sm" c="dimmed" mb="sm">关闭主窗口时最小化到系统托盘，或直接退出程序</Text>
            <SegmentedControl
              value={appSettings.closeBehavior}
              onChange={onChangeCloseBehavior}
              data={[
                { label: '最小化到托盘', value: 'minimize' },
                { label: '退出程序', value: 'quit' },
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
              rightSectionWidth={64}
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

      <Card padding="lg" radius="md" withBorder style={{ maxWidth: 620 }}>
        <Stack gap="md">
          <div>
            <Text fw={700}>订阅服务</Text>
            <Text size="sm" c="dimmed" mt={4}>将代理池中存活的节点作为订阅源对外提供，其他设备 / 客户端（Clash、v2ray 等）可通过订阅 URL 拉取节点列表</Text>
          </div>

          <Divider />

          <Group justify="space-between" wrap="wrap">
            <div>
              <Text fw={600}>开启订阅服务</Text>
              <Text size="sm" c="dimmed">关闭后订阅 URL 将返回 404</Text>
            </div>
            <Switch
              checked={subConfig?.enabled ?? false}
              disabled={!subConfig || subSaving}
              onChange={(e) => onToggleSub(e.currentTarget.checked)}
            />
          </Group>

          <TextInput
            label="监听地址"
            description="默认仅本机可访问；如需局域网设备订阅，改为 0.0.0.0:17891。修改后需重启应用生效"
            value={subListenDraft}
            onChange={(e) => setSubListenDraft(e.currentTarget.value)}
            rightSection={
              <Button
                size="xs"
                variant="light"
                loading={subSaving}
                disabled={!subConfig || subListenDraft === subConfig.listen}
                onClick={onSaveSubListen}
              >
                保存
              </Button>
            }
            rightSectionWidth={72}
          />

          {subConfig && isWildcardListen(subConfig.listen) && (
            <Select
              label="对外 IP"
              description="监听 0.0.0.0 时，局域网设备通过此 IP 访问订阅服务；订阅 URL 将随所选 IP 更新"
              data={subConfig.lanIPs}
              value={subConfig.host || null}
              placeholder={subConfig.lanIPs.length ? '请选择局域网 IP' : '未检测到局域网 IP'}
              disabled={!subConfig.lanIPs.length || subSaving}
              onChange={(v) => onSaveSubHost(v ?? '')}
              searchable
              clearable
              allowDeselect
            />
          )}

          <TextInput
            label="订阅 URL"
            description="Base64 编码（默认），兼容 v2rayN 等客户端；如客户端支持明文可直接用下方明文地址"
            value={subConfig?.url ?? ''}
            readOnly
            rightSection={
              <Button size="xs" variant="light" leftSection={<Copy size={14} />} onClick={copySubUrl}>
                复制
              </Button>
            }
            rightSectionWidth={80}
          />

          <TextInput
            label="明文订阅地址"
            description="每行一个节点（protocol://user:pass@host:port），适合 Clash 等支持明文嗅探的客户端"
            value={subConfig ? `${subConfig.url}?format=plain` : ''}
            readOnly
            rightSection={
              <Button size="xs" variant="light" leftSection={<Copy size={14} />} onClick={copySubPlainUrl}>
                复制
              </Button>
            }
            rightSectionWidth={80}
          />

          <Group justify="space-between" wrap="wrap">
            <Text size="sm" c="dimmed">订阅密钥用于保护订阅地址，泄露后可重置</Text>
            <Button variant="light" color="red" loading={subSaving} disabled={!subConfig} onClick={onResetSubToken}>
              重置密钥
            </Button>
          </Group>
        </Stack>
      </Card>
    </Stack>
  )
}