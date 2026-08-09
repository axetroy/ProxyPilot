import { useEffect, useState } from 'react'
import { Plus, RefreshCw, Trash2 } from 'lucide-react'
import { Alert, Badge, Button, Card, Group, Modal, NumberInput, Stack, Table, Text, TextInput } from '@mantine/core'
import { useSubsStore } from '@/stores/subscriptions'
import type { Subscription } from '@/types'

type NoticeData = { type: 'success' | 'error'; text: string }

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
            <Button leftSection={<Plus size={16} />} loading={submitting} onClick={() => setOpen(true)}>
              {submitting ? '处理中...' : '添加订阅'}
            </Button>
            <Button variant="default" leftSection={<RefreshCw size={16} />} loading={refreshing} onClick={() => refresh()}>
              {refreshing ? '刷新中...' : '刷新'}
            </Button>
          </Group>
        </Group>
      </Card>

      <Card padding="md" radius="md" withBorder>
        <Table striped highlightOnHover withTableBorder>
          <Table.Thead>
            <Table.Tr>
              <Table.Th>ID</Table.Th>
              <Table.Th>名称</Table.Th>
              <Table.Th>URL</Table.Th>
              <Table.Th>间隔</Table.Th>
              <Table.Th>启用</Table.Th>
              <Table.Th>代理数</Table.Th>
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
                  <Table.Td>{sub.name}</Table.Td>
                  <Table.Td>
                    <Text size="xs" title={sub.url} style={{ maxWidth: 260, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                      {sub.url}
                    </Text>
                  </Table.Td>
                  <Table.Td>{sub.interval}s</Table.Td>
                  <Table.Td>
                    <Badge color={sub.enabled ? 'green' : 'gray'}>{sub.enabled ? '是' : '否'}</Badge>
                  </Table.Td>
                  <Table.Td>{sub.proxyCount ?? '-'}</Table.Td>
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
      </Card>

      {/* 编辑订阅 Modal */}
      <Modal opened={openEdit} onClose={() => setOpenEdit(false)} title="编辑订阅">
        <Stack gap="md">
          <TextInput label="名称" placeholder="proxy-source" value={form.name} onChange={(e) => setForm({ ...form, name: e.currentTarget.value })} />
          <TextInput label="URL" placeholder="https://example.com/list" value={form.url} onChange={(e) => setForm({ ...form, url: e.currentTarget.value })} />
          <NumberInput label="刷新间隔（秒）" min={60} step={300} value={form.interval} onChange={(value) => setForm({ ...form, interval: Number(value ?? 3600) })} />
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
          <TextInput label="URL" placeholder="https://example.com/list" value={form.url} onChange={(e) => setForm({ ...form, url: e.currentTarget.value })} />
          <NumberInput label="刷新间隔（秒）" min={60} step={300} value={form.interval} onChange={(value) => setForm({ ...form, interval: Number(value ?? 3600) })} />
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