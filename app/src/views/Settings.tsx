import { useCallback, useEffect, useMemo, useState } from 'react'
import { Copy, Download, Minimize2, Monitor, Moon, Play, RefreshCw, RotateCcw, Square, Sun, Trash2 } from 'lucide-react'
import {
  Accordion,
  Alert,
  Button,
  Card,
  Divider,
  Group,
  Progress,
  SegmentedControl,
  Select,
  Stack,
  Switch,
  Tabs,
  Text,
  TextInput,
  useMantineColorScheme,
} from '@mantine/core'
import { useStatusStore } from '@/stores/status'
import { useUpdaterStore } from '@/stores/updater'
import { useSystemProxyStore } from '@/stores/system-proxy'
import { formatBytes } from '@/lib/utils'
import { getApiBaseUrl, getErrorMessage, getPlatform, getPacConfig, getSubscriptionConfig, compactDb, getDbStatus, listSettings, syncPacRules, updatePacConfig, updateSettings, updateSubscriptionConfig, type Platform } from '@/api'
import type { AppSettings, CompactResult, DbStatus, PacConfig, SettingItem, SubscriptionExportConfig } from '@/types'

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
  const [appSettings, setAppSettings] = useState<AppSettings>({ closeBehavior: 'minimize', autoUpdate: true })
  // 订阅服务配置（独立于通用设置表单）
  const [subConfig, setSubConfig] = useState<SubscriptionExportConfig | null>(null)
  const [subListenDraft, setSubListenDraft] = useState('')
  const [subSaving, setSubSaving] = useState(false)
  const updater = useUpdaterStore()
  const systemProxy = useSystemProxyStore()
  const [spBusy, setSpBusy] = useState(false)
  // 数据库状态（数据管理）
  const [dbStatus, setDbStatus] = useState<DbStatus | null>(null)
  const [compactBusy, setCompactBusy] = useState(false)
  const [compactResult, setCompactResult] = useState<CompactResult | null>(null)
  // 智能分流配置（独立于通用设置表单）
  const [pacConfig, setPacConfig] = useState<PacConfig | null>(null)
  const [pacDraft, setPacDraft] = useState<{ mode: string; directUrls: string; proxyUrls: string; refresh: string } | null>(null)
  const [pacBusy, setPacBusy] = useState(false)
  const [pacSyncing, setPacSyncing] = useState(false)

  // 仅负责取数（重试逻辑），不直接 setState，由调用方在异步回调中写入 state
  const load = useCallback(async () => {
    const maxRetries = 10;
    for (let i = 0; i < maxRetries; i++) {
      try {
        const res = await listSettings()
        if (res.code === 0 && res.data) {
          // 订阅服务 / 智能分流配置由独立卡片管理，从通用表单中过滤掉
          const general = res.data.filter((s) => !s.key.startsWith('subscription_') && !s.key.startsWith('pac_'))
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

  // 加载智能分流配置
  useEffect(() => {
    getPacConfig()
      .then((res) => {
        if (res.code === 0 && res.data) {
          setPacConfig(res.data)
          setPacDraft({
            mode: res.data.mode,
            directUrls: res.data.directUrls,
            proxyUrls: res.data.proxyUrls,
            refresh: res.data.refresh,
          })
        }
      })
      .catch(() => {})
  }, [])

  // 更新状态 / 系统代理状态（幂等：全局已初始化时不会重复订阅）
  useEffect(() => {
    void useUpdaterStore.getState().init()
    void useSystemProxyStore.getState().init()
  }, [])

  // 加载数据库状态（大小 / 检测历史 / 可清理条数）
  useEffect(() => {
    getDbStatus()
      .then((res) => {
        if (res.code === 0 && res.data) setDbStatus(res.data)
      })
      .catch(() => {})
  }, [])

  // 手动瘦身数据库
  async function onCompact() {
    setCompactBusy(true)
    try {
      const res = await compactDb()
      if (res.code === 0 && res.data) {
        setCompactResult(res.data)
        setDbStatus((d) =>
          d
            ? { ...d, dbSize: res.data.sizeAfter, historyCount: res.data.historyCount, purgeable: 0 }
            : d,
        )
        setNotice({ type: 'success', text: '数据库已瘦身' })
      } else {
        setNotice({ type: 'error', text: res.msg || '瘦身失败' })
      }
    } catch (e) {
      setNotice({ type: 'error', text: getErrorMessage(e) })
    } finally {
      setCompactBusy(false)
    }
  }

  async function onChangeCloseBehavior(value: string) {
    const next: AppSettings = { ...appSettings, closeBehavior: value as 'minimize' | 'quit' }
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
      { label: '核心 API', value: getApiBaseUrl() },
      { label: '代理（HTTP / SOCKS5 共用，当前）', value: status.httpProxyBind || '-' },
    ],
    [status.httpProxyBind],
  )

  async function onToggleSystemProxy(enabled: boolean) {
    setSpBusy(true)
    try {
      await systemProxy.setEnabled(enabled)
      if (!useSystemProxyStore.getState().error) {
        setNotice({ type: 'success', text: enabled ? '系统代理已开启' : '系统代理已关闭' })
      }
    } catch {
      setNotice({ type: 'error', text: '切换系统代理失败' })
    } finally {
      setSpBusy(false)
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

  // ---------- 智能分流 ----------

  // 分流开关即时生效（不需要点保存）
  async function onTogglePAC(enabled: boolean) {
    setPacBusy(true)
    try {
      const res = await updatePacConfig({ enabled })
      if (res.code === 0 && res.data) {
        setPacConfig(res.data)
        setNotice({ type: 'success', text: enabled ? '智能分流已开启' : '智能分流已关闭（全部流量走代理）' })
      } else {
        setNotice({ type: 'error', text: res.msg || '更新失败' })
      }
    } catch (e) {
      setNotice({ type: 'error', text: getErrorMessage(e) })
    } finally {
      setPacBusy(false)
    }
  }

  // 模式 / 规则源 / 刷新周期保存（规则源变化后后端自动触发一次同步）
  async function onSavePacFields() {
    if (!pacConfig || !pacDraft) return
    setPacBusy(true)
    try {
      const patch: Partial<PacConfig> = {}
      if (pacDraft.mode !== pacConfig.mode) patch.mode = pacDraft.mode as PacConfig['mode']
      if (pacDraft.directUrls !== pacConfig.directUrls) patch.directUrls = pacDraft.directUrls
      if (pacDraft.proxyUrls !== pacConfig.proxyUrls) patch.proxyUrls = pacDraft.proxyUrls
      if (pacDraft.refresh !== pacConfig.refresh) patch.refresh = pacDraft.refresh
      if (Object.keys(patch).length === 0) {
        setNotice({ type: 'success', text: '配置未变化' })
        return
      }
      const res = await updatePacConfig(patch)
      if (res.code === 0 && res.data) {
        setPacConfig(res.data)
        setPacDraft({
          mode: res.data.mode,
          directUrls: res.data.directUrls,
          proxyUrls: res.data.proxyUrls,
          refresh: res.data.refresh,
        })
        setNotice({ type: 'success', text: patch.directUrls || patch.proxyUrls ? '配置已保存，规则已开始同步' : '配置已保存' })
      } else {
        setNotice({ type: 'error', text: res.msg || '保存失败' })
      }
    } catch (e) {
      setNotice({ type: 'error', text: getErrorMessage(e) })
    } finally {
      setPacBusy(false)
    }
  }

  // 手动同步规则
  async function onSyncPac() {
    setPacSyncing(true)
    try {
      const res = await syncPacRules()
      if (res.code === 0 && res.data) {
        setPacConfig(res.data)
        setNotice({ type: 'success', text: `规则已同步：直连 ${res.data.directCount} 条 / 代理 ${res.data.proxyCount} 条` })
      } else {
        setNotice({ type: 'error', text: res.msg || '同步失败' })
      }
    } catch (e) {
      setNotice({ type: 'error', text: getErrorMessage(e) })
    } finally {
      setPacSyncing(false)
    }
  }

  const pacHasChanges = !!pacConfig && !!pacDraft &&
    (pacDraft.mode !== pacConfig.mode ||
      pacDraft.directUrls !== pacConfig.directUrls ||
      pacDraft.proxyUrls !== pacConfig.proxyUrls ||
      pacDraft.refresh !== pacConfig.refresh)

  return (
    <Stack gap="md">
      {notice && (
        <Alert color={notice.type === 'success' ? 'green' : 'red'} withCloseButton onClose={() => setNotice(null)}>
          {notice.text}
        </Alert>
      )}

      <Tabs defaultValue="proxy">
        <Tabs.List>
          <Tabs.Tab value="proxy">代理</Tabs.Tab>
          <Tabs.Tab value="app">应用</Tabs.Tab>
          <Tabs.Tab value="sub">订阅</Tabs.Tab>
          <Tabs.Tab value="data">数据</Tabs.Tab>
        </Tabs.List>

        {/* ---------- 代理：系统代理 + 智能分流 + 核心配置 ---------- */}
        <Tabs.Panel value="proxy" pt="md">
          <Stack gap="md">
            <Card padding="lg" radius="md" withBorder style={{ maxWidth: 620 }}>
              <Stack gap="md">
                <div>
                  <Text fw={700}>系统代理</Text>
                  <Text size="sm" c="dimmed" mt={4}>一键将系统 HTTP / HTTPS 代理指向本机网关，浏览器等应用无需逐个手动配置</Text>
                </div>

                <Divider />

                <Group justify="space-between" wrap="wrap">
                  <div>
                    <Text fw={600}>开启系统代理</Text>
                    <Text size="sm" c="dimmed">
                      {systemProxy.enabled
                        ? `已指向 ${systemProxy.endpoint || '本机网关'}，关闭或退出应用时自动还原原有设置`
                        : '开启时自动备份原设置，关闭后完整还原'}
                    </Text>
                  </div>
                  <Switch
                    checked={systemProxy.enabled}
                    disabled={spBusy || (!systemProxy.enabled && !status.running)}
                    onChange={(e) => void onToggleSystemProxy(e.currentTarget.checked)}
                  />
                </Group>

                {!status.running && !systemProxy.enabled && (
                  <Alert color="yellow">需先启动网关，才能开启系统代理</Alert>
                )}

                {systemProxy.enabled &&
                  status.httpProxyBind &&
                  systemProxy.endpoint &&
                  systemProxy.endpoint !== status.httpProxyBind && (
                    <Alert color="yellow">网关地址已变化（当前 {status.httpProxyBind}），请关闭后重新开启系统代理</Alert>
                  )}

                {systemProxy.error && <Alert color="red">{systemProxy.error}</Alert>}
              </Stack>
            </Card>

            <Card padding="lg" radius="md" withBorder style={{ maxWidth: 620 }}>
              <Stack gap="md">
                <div>
                  <Text fw={700}>智能分流</Text>
                  <Text size="sm" c="dimmed" mt={4}>网关入口按规则判断目标直连或走节点池：大陆 / 内网直连，被墙域名走代理。关闭后全部流量走代理</Text>
                </div>

                <Divider />

                <Group justify="space-between" wrap="wrap">
                  <div>
                    <Text fw={600}>开启智能分流</Text>
                    <Text size="sm" c="dimmed">
                      {pacConfig?.enabled
                        ? '已开启：按规则匹配目标域名'
                        : '已关闭：所有流量均经节点池代理'}
                    </Text>
                  </div>
                  <Switch
                    checked={pacConfig?.enabled ?? false}
                    disabled={!pacConfig || pacBusy}
                    onChange={(e) => void onTogglePAC(e.currentTarget.checked)}
                  />
                </Group>

                {pacConfig?.enabled && (
                  <>
                    <div>
                      <Text fw={600} mb="sm">分流模式</Text>
                      <SegmentedControl
                        value={pacDraft?.mode ?? pacConfig.mode}
                        onChange={(v) => setPacDraft((d) => (d ? { ...d, mode: v } : d))}
                        data={[
                          { label: '白名单（默认走代理）', value: 'whitelist' },
                          { label: '黑名单（默认直连）', value: 'blacklist' },
                        ]}
                        fullWidth
                      />
                    </div>

                    <Accordion defaultValue="">
                      <Accordion.Item value="pac-sources">
                        <Accordion.Control>高级：规则源与刷新周期</Accordion.Control>
                        <Accordion.Panel>
                          <Stack gap="md" pt="xs">
                            <TextInput
                              label="直连规则列表 URL"
                              description="逗号分隔，按序尝试；同步失败保留上次缓存。留空表示不拉取该项"
                              value={pacDraft?.directUrls ?? ''}
                              onChange={(e) => setPacDraft((d) => (d ? { ...d, directUrls: e.currentTarget.value } : d))}
                            />
                            <TextInput
                              label="代理规则列表 URL"
                              description="逗号分隔，按序尝试；同步失败保留上次缓存。留空表示不拉取该项"
                              value={pacDraft?.proxyUrls ?? ''}
                              onChange={(e) => setPacDraft((d) => (d ? { ...d, proxyUrls: e.currentTarget.value } : d))}
                            />
                            <TextInput
                              label="自动刷新周期"
                              description="规则列表自动重新拉取的间隔，如 12h、24h（最小 1 小时）"
                              value={pacDraft?.refresh ?? ''}
                              onChange={(e) => setPacDraft((d) => (d ? { ...d, refresh: e.currentTarget.value } : d))}
                            />
                          </Stack>
                        </Accordion.Panel>
                      </Accordion.Item>
                    </Accordion>

                    <Group justify="space-between" wrap="wrap">
                      <div>
                        <Text fw={600}>规则状态</Text>
                        <Text size="sm" c="dimmed">
                          直连 {pacConfig.directCount} 条 / 代理 {pacConfig.proxyCount} 条
                          {pacConfig.syncAt ? ` · 上次同步 ${new Date(pacConfig.syncAt).toLocaleString()}` : ' · 尚未同步'}
                        </Text>
                      </div>
                      <Button
                        variant="light"
                        leftSection={<RefreshCw size={16} />}
                        loading={pacSyncing}
                        disabled={!pacConfig || pacSyncing || pacBusy}
                        onClick={onSyncPac}
                      >
                        {pacSyncing ? '同步中...' : '立即同步'}
                      </Button>
                    </Group>

                    {pacConfig.syncing && <Alert color="blue">规则正在同步中…</Alert>}
                    {pacConfig.syncError && <Alert color="yellow">上次同步失败：{pacConfig.syncError}（保留上次缓存规则）</Alert>}

                    <Group justify="space-between" wrap="wrap">
                      <Button
                        variant="light"
                        leftSection={<RotateCcw size={16} />}
                        disabled={!pacHasChanges || pacBusy}
                        onClick={() => pacConfig && setPacDraft({
                          mode: pacConfig.mode,
                          directUrls: pacConfig.directUrls,
                          proxyUrls: pacConfig.proxyUrls,
                          refresh: pacConfig.refresh,
                        })}
                      >
                        撤销
                      </Button>
                      <Button loading={pacBusy} disabled={!pacHasChanges} onClick={onSavePacFields}>
                        {pacBusy ? '保存中...' : '保存配置'}
                      </Button>
                    </Group>
                  </>
                )}
              </Stack>
            </Card>

            <Card padding="lg" radius="md" withBorder style={{ maxWidth: 620 }}>
              <Stack gap="md">
                <div>
                  <Text fw={700}>核心配置</Text>
                  <Text size="sm" c="dimmed" mt={4}>修改 proxy-core 运行参数，保存后立即生效</Text>
                </div>

                <Accordion defaultValue="">
                  <Accordion.Item value="engine-advanced">
                    <Accordion.Control>引擎高级参数</Accordion.Control>
                    <Accordion.Panel>
                      <Stack gap="md" pt="xs">
                        {settings.map((s) => (
                          <TextInput
                            key={s.key}
                            label={s.desc}
                            description={
                              s.key === 'proxy_port' ? '端口被占用会自动顺延，实际端口见「应用」页地址；HTTP 与 SOCKS5 共用此端口' : undefined
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
                      </Stack>
                    </Accordion.Panel>
                  </Accordion.Item>
                </Accordion>

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
        </Tabs.Panel>

        {/* ---------- 应用：外观 / 窗口行为 / 地址信息 / 软件更新 ---------- */}
        <Tabs.Panel value="app" pt="md">
          <Stack gap="md">
            <Card padding="lg" radius="md" withBorder style={{ maxWidth: 620 }}>
              <Stack gap="md">
                <div>
                  <Text fw={700}>应用</Text>
                  <Text size="sm" c="dimmed" mt={4}>外观与窗口行为设置</Text>
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
              </Stack>
            </Card>

            <Card padding="lg" radius="md" withBorder style={{ maxWidth: 620 }}>
              <Stack gap="md">
                <div>
                  <Text fw={700}>地址信息</Text>
                  <Text size="sm" c="dimmed" mt={4}>代理服务地址与当前版本</Text>
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
                  <Text fw={700}>软件更新</Text>
                  <Text size="sm" c="dimmed" mt={4}>从 GitHub Releases 检查并下载新版本，下载完成后重启安装</Text>
                </div>

                <Divider />

                <Group justify="space-between" wrap="wrap">
                  <div>
                    <Text fw={600}>自动检查更新</Text>
                    <Text size="sm" c="dimmed">启动后自动检查并下载新版本（默认开启）；关闭后仍可手动检查</Text>
                  </div>
                  <Switch
                    checked={updater.enabled}
                    onChange={(e) => void updater.setAutoUpdate(e.currentTarget.checked)}
                  />
                </Group>

                <Divider />

                <Group justify="space-between" wrap="wrap">
                  <Text size="sm" c="dimmed">
                    当前版本 <Text span fw={600}>{updater.currentVersion || '-'}</Text>
                    {status.version ? <>（核心 {status.version}）</> : null}
                  </Text>
                  <Button
                    leftSection={<RefreshCw size={16} />}
                    loading={updater.status === 'checking'}
                    disabled={updater.status === 'downloading'}
                    onClick={() => void updater.check()}
                  >
                    检查更新
                  </Button>
                </Group>

                {updater.status === 'available' && (
                  <Alert color="blue">发现新版本 v{updater.latestVersion}，正在自动下载…</Alert>
                )}

                {updater.status === 'downloading' && updater.progress && (
                  <div>
                    <Group justify="space-between" mb={6}>
                      <Text size="sm" c="dimmed">正在下载 v{updater.latestVersion}</Text>
                      <Text size="sm" fw={600}>{Math.round(updater.progress.percent)}%</Text>
                    </Group>
                    <Progress value={updater.progress.percent} size="sm" />
                    <Text size="xs" c="dimmed" mt={6}>
                      {formatBytes(updater.progress.transferred)} / {formatBytes(updater.progress.total)} · {formatBytes(updater.progress.bytesPerSecond)}/s
                    </Text>
                  </div>
                )}

                {updater.status === 'downloaded' && (
                  <Group justify="space-between" wrap="wrap">
                    <Alert color="green" style={{ flex: 1 }}>新版本 v{updater.latestVersion} 已下载完成，重启应用即可安装</Alert>
                    <Button color="green" leftSection={<Download size={16} />} onClick={() => void updater.install()}>
                      立即重启安装
                    </Button>
                  </Group>
                )}

                {updater.status === 'not-available' && updater.source === 'manual' && (
                  <Alert color="green">已是最新版本（当前 v{updater.currentVersion}）</Alert>
                )}

                {updater.status === 'error' && (
                  <Alert color="red">检查更新失败：{updater.error || '未知错误'}</Alert>
                )}

                {updater.status === 'dev' && (
                  <Alert color="yellow">开发模式下不支持检查更新</Alert>
                )}
              </Stack>
            </Card>
          </Stack>
        </Tabs.Panel>

        {/* ---------- 订阅：订阅服务（对外提供节点订阅） ---------- */}
        <Tabs.Panel value="sub" pt="md">
          <Stack gap="md">
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
        </Tabs.Panel>

        {/* ---------- 数据：数据库维护 ---------- */}
        <Tabs.Panel value="data" pt="md">
          <Stack gap="md">
            <Card padding="lg" radius="md" withBorder style={{ maxWidth: 620 }}>
              <Stack gap="md">
                <div>
                  <Text fw={700}>数据管理</Text>
                  <Text size="sm" c="dimmed" mt={4}>手动清理过期的检测历史并收缩数据库文件，节点 / 订阅 / 设置均不受影响</Text>
                </div>

                <Divider />

                <Group justify="space-between" wrap="wrap">
                  <div>
                    <Text fw={600}>数据库大小</Text>
                    <Text size="sm" c="dimmed">{dbStatus ? formatBytes(dbStatus.dbSize) : '-'}</Text>
                  </div>
                  <div>
                    <Text fw={600}>检测历史</Text>
                    <Text size="sm" c="dimmed">{dbStatus ? `${dbStatus.historyCount} 条` : '-'}</Text>
                  </div>
                  <div>
                    <Text fw={600}>可清理</Text>
                    <Text size="sm" c="dimmed">
                      {dbStatus ? `${dbStatus.purgeable} 条（保留 ${dbStatus.retentionDays} 天）` : '-'}
                    </Text>
                  </div>
                </Group>

                <Group justify="space-between" wrap="wrap">
                  <Text size="sm" c="dimmed">保留天数可在「代理」页的引擎高级参数中调整</Text>
                  <Button
                    leftSection={<Trash2 size={16} />}
                    color="red"
                    variant="light"
                    loading={compactBusy}
                    disabled={!dbStatus || compactBusy}
                    onClick={onCompact}
                  >
                    {compactBusy ? '瘦身中...' : '立即瘦身'}
                  </Button>
                </Group>

                {compactResult && (
                  <Alert color="green">
                    已清理 {compactResult.deleted} 条过期检测历史
                    {compactResult.sizeBefore > 0 && compactResult.sizeAfter > 0
                      ? `，释放 ${formatBytes(compactResult.sizeBefore - compactResult.sizeAfter)}，当前 ${formatBytes(compactResult.sizeAfter)}`
                      : '，已完成文件收缩'}
                  </Alert>
                )}
              </Stack>
            </Card>
          </Stack>
        </Tabs.Panel>
      </Tabs>
    </Stack>
  )
}
