import { useEffect, useState } from 'react'
import { FileUp, Plus, RefreshCw, Trash2 } from 'lucide-react'
import { Alert, Badge, Button, Card, Code, Group, Modal, NumberInput, Paper, Stack, Switch, Table, Text, TextInput } from '@mantine/core'
import { useSubsStore } from '@/stores/subscriptions'
import type { Subscription } from '@/types'

// 订阅格式展示元数据：当前仅 base64/明文可被解析提取节点，其余识别后暂未解析。
const FORMAT_META: Record<string, { label: string; color: string }> = {
  base64: { label: 'Base64 列表', color: 'green' },
  raw: { label: '明文列表', color: 'green' },
  clash: { label: 'Clash', color: 'yellow' },
  v2ray: { label: 'V2Ray', color: 'yellow' },
  singbox: { label: 'Sing-box', color: 'yellow' },
  ssr: { label: 'SSR', color: 'yellow' },
  ss: { label: 'SS', color: 'yellow' },
  unknown: { label: '未知', color: 'gray' },
}

function FormatCell({ sub }: { sub: Subscription }) {
  const meta = FORMAT_META[sub.format ?? 'unknown'] ?? FORMAT_META.unknown
  // 后端可直接解析的仅有 base64 / 明文列表；其余识别到但暂未解析，0 节点时提示。
  const unsupported = sub.format !== 'base64' && sub.format !== 'raw'
  return (
    <Group gap="xs" wrap="nowrap">
      <Badge variant="light" color={meta.color}>
        {meta.label}
      </Badge>
      {unsupported && (sub.proxyCount ?? 0) === 0 && (
        <Text size="xs" c="red">
          未解析
        </Text>
      )}
    </Group>
  )
}

function SubscriptionFormatHelp() {
  return (
    <Paper withBorder p="sm" radius="md" bg="var(--mantine-color-default-hover)">
      <Text size="sm" fw={500} mb={6}>
        支持的订阅格式（附示例）
      </Text>
      <Stack gap={6}>
        <div>
          <Text size="xs" c="dimmed">
            Base64 列表（最常见）
          </Text>
          <Code block>{'base64(protocol://user:pass@host:port)'}</Code>
        </div>
        <div>
          <Text size="xs" c="dimmed">
            明文协议行（每行一个节点）
          </Text>
          <Code block>{'http://user:pass@1.2.3.4:8080\nsocks5://5.6.7.8:1080'}</Code>
        </div>
        <div>
          <Text size="xs" c="dimmed">
            纯 host:port（默认按 HTTP 处理）
          </Text>
          <Code block>{'1.2.3.4:8080\n5.6.7.8:1080'}</Code>
        </div>
        <Alert color="yellow" variant="light" mt={4}>
          Clash / V2Ray / Sing-box / SSR / SS 已能识别格式，但当前版本暂未解析，节点数为 0。
        </Alert>
      </Stack>
    </Paper>
  )
}

export default function Subscriptions() {
  const subs = useSubsStore((s) => s.subs)
  const fetchingIds = useSubsStore((s) => s.fetchingIds)
  const refreshing = useSubsStore((s) => s.refreshing)
  const submitting = useSubsStore((s) => s.submitting)
  const notice = useSubsStore((s) => s.notice)
  const refresh = useSubsStore((s) => s.refresh)
  const add = useSubsStore((s) => s.add)
  const remove = useSubsStore((s) => s.remove)
  const refreshOne = useSubsStore((s) => s.refreshOne)
  const clearNotice = useSubsStore((s) => s.clearNotice)
  const update = useSubsStore((s) => s.update)

  const [open, setOpen] = useState(false)
  const [openEdit, setOpenEdit] = useState(false)
  const [toDelete, setToDelete] = useState<Subscription | null>(null)
  const [editSub, setEditSub] = useState<Subscription | null>(null)
  const [form, setForm] = useState({ name: '', url: '', interval: 3600 })

  useEffect(() => {
    refresh()
  }, [refresh])

  useEffect(() => {
    if (!notice) return
    const timer = window.setTimeout(clearNotice, 5000)
    return () => window.clearTimeout(timer)
  }, [notice, clearNotice])

  function resetForm() {
    setForm({ name: '', url: '', interval: 3600 })
  }

  // 从本地文件选择订阅源：file:// URL 由主进程（dialog）生成，
  // 未选文件或主进程不支持时不改动现有输入。
  async function pickLocalFile() {
    const fileUrl = await window.proxypilot?.pickSubscriptionFile?.()
    if (fileUrl) {
      setForm((f) => ({ ...f, url: fileUrl }))
    }
  }

  async function onSubmit() {
    if (!form.name || !form.url) return
    const ok = await add(form.name, form.url, form.interval)
    if (!ok) return
    setOpen(false)
    resetForm()
  }

  return (
    <Stack gap="md">
      {notice && (
        <Alert color={notice.type === 'success' ? 'green' : 'red'} withCloseButton onClose={clearNotice}>
          {notice.text}
        </Alert>
      )}

      <Card padding="lg" radius="md" withBorder>
        <Group justify="space-between" align="flex-start" wrap="wrap">
          <div>
            <Text fw={700}>订阅源管理</Text>
            <Text size="sm" c="dimmed" mt={4}>集中管理代理订阅源和抓取任务</Text>
          </div>
          <Group gap="sm" wrap="wrap">
            <Button leftSection={<Plus size={16} />} loading={submitting} onClick={() => { resetForm(); setOpen(true); }}>
              {submitting ? '处理中...' : '添加订阅'}
            </Button>
            <Button variant="default" leftSection={<RefreshCw size={16} />} loading={refreshing} onClick={() => refresh()}>
              {refreshing ? '刷新中...' : '刷新'}
            </Button>
          </Group>
        </Group>
      </Card>

      <Card padding="md" radius="md" withBorder>
        <Table.ScrollContainer minWidth={850}>
          <Table striped highlightOnHover withTableBorder>
          <Table.Thead>
            <Table.Tr>
              <Table.Th>ID</Table.Th>
              <Table.Th>名称</Table.Th>
              <Table.Th>间隔</Table.Th>
              <Table.Th>启用</Table.Th>
              <Table.Th>代理数</Table.Th>
              <Table.Th>格式</Table.Th>
              <Table.Th>上次抓取</Table.Th>
              <Table.Th style={{ textAlign: 'right' }}>操作</Table.Th>
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {subs.length === 0 ? (
                <Table.Tr>
                  <Table.Td colSpan={8} style={{ textAlign: 'center', color: 'var(--mantine-color-dimmed)' }}>
                    暂无订阅
                  </Table.Td>
                </Table.Tr>
            ) : (
              subs.map((sub) => (
                <Table.Tr key={sub.id}>
                  <Table.Td>{sub.id}</Table.Td>
                  <Table.Td>
                    <Text size="sm" title={`${sub.name}\n${sub.url}`} style={{ maxWidth: 260, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                      {sub.name}
                    </Text>
                  </Table.Td>
                  <Table.Td>{sub.interval}s</Table.Td>
                  <Table.Td>
                    <Switch
                      checked={sub.enabled}
                      disabled={submitting}
                      onChange={(e) => void update(sub.id, sub.name, sub.url, sub.interval, e.currentTarget.checked)}
                    />
                  </Table.Td>
                  <Table.Td>{sub.proxyCount ?? '-'}</Table.Td>
                  <Table.Td>
                    <FormatCell sub={sub} />
                  </Table.Td>
                  <Table.Td>{sub.lastFetch ? new Date(sub.lastFetch).toLocaleString() : '-'}</Table.Td>
                  <Table.Td style={{ textAlign: 'right' }}>
                    <Group justify="flex-end" gap="xs">
                      <Button size="xs" variant="light" loading={fetchingIds.includes(sub.id)} onClick={() => refreshOne(sub.id)}>
                        抓取
                      </Button>
                      <Button size="xs" variant="light" onClick={() => { setEditSub(sub); setForm({ name: sub.name, url: sub.url, interval: sub.interval }); setOpenEdit(true); }}>
                        编辑
                      </Button>
                      <Button size="xs" color="red" variant="subtle" onClick={() => setToDelete(sub)}>
                        <Trash2 size={14} />
                      </Button>
                    </Group>
                  </Table.Td>
                </Table.Tr>
              ))
            )}
          </Table.Tbody>
          </Table>
        </Table.ScrollContainer>
      </Card>

      {/* 编辑订阅 Modal */}
      <Modal opened={openEdit} onClose={() => setOpenEdit(false)} title="编辑订阅">
        <Stack gap="md">
          <TextInput label="名称" placeholder="proxy-source" value={form.name} onChange={(e) => setForm({ ...form, name: e.currentTarget.value })} />
          <TextInput label="URL" placeholder="https://example.com/list 或 file:///C:/path/list.txt" value={form.url} onChange={(e) => setForm({ ...form, url: e.currentTarget.value })} />
          <Button size="xs" variant="light" leftSection={<FileUp size={14} />} onClick={() => void pickLocalFile()}>
            选择本地文件
          </Button>
          <NumberInput label="刷新间隔（秒）" min={60} step={300} value={form.interval} onChange={(value) => setForm({ ...form, interval: Number(value ?? 3600) })} />
          <SubscriptionFormatHelp />
          <Group justify="flex-end">
            <Button variant="default" onClick={() => setOpenEdit(false)}>
              取消
            </Button>
            <Button onClick={async () => {
              if (!editSub) return;
              const ok = await update(editSub.id, form.name, form.url, form.interval, editSub.enabled);
              if (ok) { setOpenEdit(false); resetForm(); }
            }} loading={submitting}>
              保存
            </Button>
          </Group>
        </Stack>
      </Modal>

      <Modal opened={open} onClose={() => setOpen(false)} title="添加订阅">
        <Stack gap="md">
          <TextInput label="名称" placeholder="proxy-source" value={form.name} onChange={(e) => setForm({ ...form, name: e.currentTarget.value })} />
          <TextInput label="URL" placeholder="https://example.com/list 或 file:///C:/path/list.txt" value={form.url} onChange={(e) => setForm({ ...form, url: e.currentTarget.value })} />
          <Button size="xs" variant="light" leftSection={<FileUp size={14} />} onClick={() => void pickLocalFile()}>
            选择本地文件
          </Button>
          <NumberInput label="刷新间隔（秒）" min={60} step={300} value={form.interval} onChange={(value) => setForm({ ...form, interval: Number(value ?? 3600) })} />
          <SubscriptionFormatHelp />
          <Group justify="flex-end">
            <Button variant="default" onClick={() => setOpen(false)}>
              取消
            </Button>
            <Button onClick={onSubmit} loading={submitting}>
              添加
            </Button>
          </Group>
        </Stack>
      </Modal>

      <Modal opened={toDelete !== null} onClose={() => setToDelete(null)} title="删除订阅">
        <Stack gap="md">
          <Text>确定删除订阅 “{toDelete?.name}”？此操作不可撤销。</Text>
          <Group justify="flex-end">
            <Button variant="default" onClick={() => setToDelete(null)}>
              取消
            </Button>
            <Button color="red" onClick={async () => { if (toDelete) await remove(toDelete.id); setToDelete(null) }}>
              删除
            </Button>
          </Group>
        </Stack>
      </Modal>
    </Stack>
  )
}