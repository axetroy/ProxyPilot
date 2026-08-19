import { useEffect, useState } from 'react'
import { RefreshCw, RotateCcw, X } from 'lucide-react'
import {
  Accordion,
  ActionIcon,
  Alert,
  Button,
  Card,
  Divider,
  Group,
  SegmentedControl,
  Stack,
  Switch,
  Text,
  TextInput,
} from '@mantine/core'
import { getErrorMessage, getPacConfig, syncPacRules, updatePacConfig } from '@/api'
import type { PacConfig } from '@/types'

type NoticeData = { type: 'success' | 'error'; text: string }

// 智能分流：网关入口按规则判断目标直连或走节点池。
// 独立页面承载：分流属运行时路由决策，与系统/偏好设置在设置页分开管理。
export default function Rules() {
  const [notice, setNotice] = useState<NoticeData | null>(null)
  const [pacConfig, setPacConfig] = useState<PacConfig | null>(null)
  const [pacDraft, setPacDraft] = useState<{ mode: string; directUrls: string; proxyUrls: string; refresh: string } | null>(null)
  const [pacBusy, setPacBusy] = useState(false)
  const [pacSyncing, setPacSyncing] = useState(false)
  // 手动规则名单（代理/直连各一个输入框 + 保存状态）
  const [pacCustomProxyInput, setPacCustomProxyInput] = useState('')
  const [pacCustomDirectInput, setPacCustomDirectInput] = useState('')
  const [pacCustomBusy, setPacCustomBusy] = useState(false)

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

  // 前端域名校验（与后端一致：小写字母/数字/-/.，≤255，不以点或连字符开头/结尾）
  function validDomain(d: string): boolean {
    return /^[a-z0-9-.]{1,255}$/.test(d) && !d.startsWith('.') && !d.endsWith('.') && !d.startsWith('-') && !d.endsWith('-')
  }

  // 整表覆盖提交手动名单（代理/直连共用）
  async function putCustom(kind: 'proxy' | 'direct', list: string[]) {
    setPacCustomBusy(true)
    try {
      const res = await updatePacConfig(kind === 'proxy' ? { customProxy: list } : { customDirect: list })
      if (res.code === 0 && res.data) {
        setPacConfig(res.data)
      } else {
        setNotice({ type: 'error', text: res.msg || '保存失败' })
      }
    } catch (e) {
      setNotice({ type: 'error', text: getErrorMessage(e) })
    } finally {
      setPacCustomBusy(false)
    }
  }

  async function onAddCustom(kind: 'proxy' | 'direct', raw: string) {
    const d = raw.trim().toLowerCase()
    if (!pacConfig) return
    if (!d) return
    if (!validDomain(d)) {
      setNotice({ type: 'error', text: `非法域名：${d}` })
      return
    }
    const cur = kind === 'proxy' ? (pacConfig.customProxy ?? []) : (pacConfig.customDirect ?? [])
    if (cur.includes(d)) {
      setNotice({ type: 'error', text: `${d} 已在手动名单中` })
      return
    }
    await putCustom(kind, [...cur, d])
    if (kind === 'proxy') {
      setPacCustomProxyInput('')
    } else {
      setPacCustomDirectInput('')
    }
  }

  function onRemoveCustom(kind: 'proxy' | 'direct', d: string) {
    if (!pacConfig) return
    const cur = kind === 'proxy' ? pacConfig.customProxy : pacConfig.customDirect
    void putCustom(kind, cur.filter((x) => x !== d))
  }

  return (
    <Stack gap="md">
      {notice && (
        <Alert color={notice.type === 'success' ? 'green' : 'red'} withCloseButton onClose={() => setNotice(null)}>
          {notice.text}
        </Alert>
      )}

      <Card padding="lg" radius="md" withBorder style={{ maxWidth: 720 }}>
        <Stack gap="md">
          <div>
            <Text fw={700}>智能分流</Text>
            <Text size="sm" c="dimmed" mt={4}>网关入口按规则判断目标直连或走节点池：大陆 / 内网直连，需代理域名走代理。关闭后全部流量走代理</Text>
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

              <div>
                <Text fw={600} mb="xs">手动规则</Text>
                <Text size="sm" c="dimmed" mb="md">
                  手动指定域名强制走代理或直连，优先级高于自动同步名单；子域名自动匹配父域条目
                </Text>
                <Group align="flex-start" grow wrap="wrap">
                  <Stack gap="xs">
                    <Text fw={500} size="sm">手动代理名单（强制走代理）</Text>
                    <Group gap="xs" wrap="nowrap">
                      <TextInput
                        placeholder="如 google.com"
                        value={pacCustomProxyInput}
                        onChange={(e) => setPacCustomProxyInput(e.currentTarget.value)}
                        onKeyDown={(e) => {
                          if (e.key === 'Enter') void onAddCustom('proxy', pacCustomProxyInput)
                        }}
                        disabled={pacCustomBusy}
                        style={{ flex: 1 }}
                      />
                      <Button
                        size="sm"
                        variant="light"
                        disabled={pacCustomBusy || !pacCustomProxyInput.trim()}
                        onClick={() => void onAddCustom('proxy', pacCustomProxyInput)}
                      >
                        添加
                      </Button>
                    </Group>
                    <Group gap={6}>
                      {(pacConfig.customProxy ?? []).map((d) => (
                        <Group
                          key={d}
                          gap={2}
                          py={2}
                          px="sm"
                          style={{ borderRadius: 6, background: 'var(--mantine-color-default-hover)' }}
                        >
                          <Text size="xs">{d}</Text>
                          <ActionIcon
                            size={16}
                            variant="subtle"
                            color="gray"
                            disabled={pacCustomBusy}
                            aria-label={`移除 ${d}`}
                            onClick={() => onRemoveCustom('proxy', d)}
                          >
                            <X size={12} />
                          </ActionIcon>
                        </Group>
                      ))}
                      {(pacConfig.customProxy ?? []).length === 0 && (
                        <Text size="sm" c="dimmed">无手动代理规则</Text>
                      )}
                    </Group>
                  </Stack>

                  <Stack gap="xs">
                    <Text fw={500} size="sm">手动直连名单（强制直连）</Text>
                    <Group gap="xs" wrap="nowrap">
                      <TextInput
                        placeholder="如 baidu.com"
                        value={pacCustomDirectInput}
                        onChange={(e) => setPacCustomDirectInput(e.currentTarget.value)}
                        onKeyDown={(e) => {
                          if (e.key === 'Enter') void onAddCustom('direct', pacCustomDirectInput)
                        }}
                        disabled={pacCustomBusy}
                        style={{ flex: 1 }}
                      />
                      <Button
                        size="sm"
                        variant="light"
                        disabled={pacCustomBusy || !pacCustomDirectInput.trim()}
                        onClick={() => void onAddCustom('direct', pacCustomDirectInput)}
                      >
                        添加
                      </Button>
                    </Group>
                    <Group gap={6}>
                      {(pacConfig.customDirect ?? []).map((d) => (
                        <Group
                          key={d}
                          gap={2}
                          py={2}
                          px="sm"
                          style={{ borderRadius: 6, background: 'var(--mantine-color-default-hover)' }}
                        >
                          <Text size="xs">{d}</Text>
                          <ActionIcon
                            size={16}
                            variant="subtle"
                            color="gray"
                            disabled={pacCustomBusy}
                            aria-label={`移除 ${d}`}
                            onClick={() => onRemoveCustom('direct', d)}
                          >
                            <X size={12} />
                          </ActionIcon>
                        </Group>
                      ))}
                      {(pacConfig.customDirect ?? []).length === 0 && (
                        <Text size="sm" c="dimmed">无手动直连规则</Text>
                      )}
                    </Group>
                  </Stack>
                </Group>
              </div>

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
    </Stack>
  )
}