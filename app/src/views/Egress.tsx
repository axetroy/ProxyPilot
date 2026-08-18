import { useEffect, useState } from 'react'
import { GitBranch, MapPin, Pencil, Pin, PinOff, Plus, RadioTower, Trash2 } from 'lucide-react'
import { ActionIcon, Alert, Badge, Button, Card, Divider, Group, Modal, MultiSelect, Radio, Select, Stack, Switch, Text, TextInput, type ComboboxItem, type ComboboxParsedItem } from '@mantine/core'
import { usePoolStore } from '@/stores/pool'
import { useStatusStore } from '@/stores/status'
import { createChain, deleteChain, getEgressConfig, getErrorMessage, listChains, updateChain, updateEgressConfig } from '@/api'
import type { EgressConfig, ProxyChain, ProxyNode } from '@/types'

type NoticeData = { type: 'success' | 'error'; text: string }

/** 固定节点下拉的选项：在 ComboboxItem 基础上附带地区/协议/评分，用于富文本展示与搜索 */
interface NodeSelectItem extends ComboboxItem {
  protocol: string
  score: number
  country?: string
  city?: string
}

// geoLabel 拼接节点地区文案（国家 · 城市，连续重复段自动合并，未知段忽略）。
function geoLabel(n: NodeSelectItem): string {
  return [n.country, n.city]
    .filter((v): v is string => !!v)
    .filter((v, i, a) => a.indexOf(v) === i)
    .join(' · ')
}

// egressOptionFilter 支持按 host:port、协议、国家/城市搜索下拉选项。
function egressOptionFilter({ options, search, limit }: { options: ComboboxParsedItem[]; search: string; limit: number }): ComboboxParsedItem[] {
  const q = search.trim().toLowerCase()
  const filtered = q
    ? options.filter((o): o is ComboboxParsedItem => {
        if (!('label' in o)) return false
        const n = o as unknown as NodeSelectItem
        return (
          o.label.toLowerCase().includes(q) ||
          n.protocol.toLowerCase().includes(q) ||
          (n.country ?? '').toLowerCase().includes(q) ||
          (n.city ?? '').toLowerCase().includes(q)
        )
      })
    : options
  return filtered.slice(0, limit)
}

// chainOptions 构建链路编辑器用的节点下拉选项：
// 存活节点可选，链中出现但已失效的节点并入并禁用（保证编辑回显正确）。
function buildChainOptions(nodes: ProxyNode[], chains: ProxyChain[]): NodeSelectItem[] {
  const options: NodeSelectItem[] = nodes
    .filter((n) => n.status === 'alive')
    .map((n) => ({
      value: String(n.id),
      label: `${n.host}:${n.port}`,
      protocol: n.protocol,
      score: n.score,
      country: n.country,
      city: n.city,
    }))
  const inChains = new Set<number>()
  chains.forEach((c) => c.nodeIds.forEach((id) => inChains.add(id)))
  nodes
    .filter((n) => n.status !== 'alive' && inChains.has(n.id))
    .forEach((n) => {
      if (options.some((o) => o.value === String(n.id))) return
      options.push({
        value: String(n.id),
        label: `${n.host}:${n.port}`,
        protocol: n.protocol,
        score: n.score,
        country: n.country,
        city: n.city,
        disabled: true,
      })
    })
  return options
}

// ChainManager 代理链路管理：列表（名称/节点/启停/编辑/删除）+ 新建/编辑弹窗。
function ChainManager({ nodes }: { nodes: ProxyNode[] }) {
  const [chains, setChains] = useState<ProxyChain[]>([])
  const [loading, setLoading] = useState(true)
  const [modalOpen, setModalOpen] = useState(false)
  const [editing, setEditing] = useState<ProxyChain | null>(null)
  const [name, setName] = useState('')
  const [selected, setSelected] = useState<string[]>([])
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function refresh() {
    const res = await listChains()
    if (res.code === 0 && res.data) setChains(res.data)
  }

  useEffect(() => {
    let active = true
    listChains()
      .then((res) => {
        if (active && res.code === 0 && res.data) setChains(res.data)
      })
      .finally(() => {
        if (active) setLoading(false)
      })
    return () => {
      active = false
    }
  }, [])

  const options = buildChainOptions(nodes, chains)
  // 当前选中但不在节点列表里的值（如节点已从池中删除）也并入选项并禁用，
  // 避免编辑弹窗里回显裸 ID。
  selected.forEach((v) => {
    if (!options.some((o) => o.value === v)) {
      options.push({ value: v, label: `节点#${v}`, protocol: '', score: 0, disabled: true })
    }
  })

  function openCreate() {
    setEditing(null)
    setName('')
    setSelected([])
    setError(null)
    setModalOpen(true)
  }

  function openEdit(c: ProxyChain) {
    setEditing(c)
    setName(c.name)
    setSelected(c.nodeIds.map(String))
    setError(null)
    setModalOpen(true)
  }

  async function save() {
    const ids = selected.map(Number)
    const trimmed = name.trim()
    if (!trimmed) {
      setError('请输入链路名称')
      return
    }
    if (ids.length === 0) {
      setError('请至少选择一个节点')
      return
    }
    setSubmitting(true)
    try {
      const res = editing
        ? await updateChain(editing.id, { name: trimmed, nodeIds: ids })
        : await createChain({ name: trimmed, nodeIds: ids })
      if (res.code === 0) {
        setModalOpen(false)
        refresh()
      } else {
        setError(res.msg || '保存失败')
      }
    } catch (e) {
      setError(getErrorMessage(e))
    } finally {
      setSubmitting(false)
    }
  }

  async function toggle(c: ProxyChain) {
    const res = await updateChain(c.id, { name: c.name, nodeIds: c.nodeIds, enabled: !c.enabled })
    if (res.code === 0) refresh()
  }

  async function remove(c: ProxyChain) {
    if (!window.confirm(`确定删除链路「${c.name}」？`)) return
    const res = await deleteChain(c.id)
    if (res.code === 0) refresh()
  }

  return (
    <Card padding="lg" radius="md" withBorder style={{ maxWidth: 620 }}>
      <Stack gap="md">
        <Group justify="space-between" wrap="wrap">
          <div>
            <Text fw={700}>代理链路</Text>
            <Text size="sm" c="dimmed" mt={4}>
              配置客户端依次经过多个节点到达目标的链路。链上所有节点存活时该链才可用，多条启用的链会随机分担流量
            </Text>
          </div>
          <Button size="xs" leftSection={<Plus size={14} />} onClick={openCreate}>
            新建链路
          </Button>
        </Group>

        <Divider />

        {loading ? (
          <Text size="sm" c="dimmed">加载中…</Text>
        ) : chains.length === 0 ? (
          <Text size="sm" c="dimmed">还没有配置链路，点击「新建链路」创建第一条</Text>
        ) : (
          <Stack gap="sm">
            {chains.map((c) => {
              const labels = c.nodeIds
                .map((id) => {
                  const n = nodes.find((x) => x.id === id)
                  return n ? `${n.host}:${n.port}` : `节点#${id}`
                })
                .join(' → ')
              return (
                <Group key={c.id} justify="space-between" wrap="wrap">
                  <div style={{ flex: 1, minWidth: 220 }}>
                    <Group gap="xs">
                      <Text fw={600} size="sm">{c.name}</Text>
                      <Badge size="xs" variant="light" color={c.enabled ? 'green' : 'gray'}>
                        {c.enabled ? '启用' : '停用'}
                      </Badge>
                    </Group>
                    <Text size="xs" c="dimmed" mt={2} truncate style={{ maxWidth: 380 }}>
                      {labels}
                    </Text>
                  </div>
                  <Group gap="xs">
                    <Switch size="sm" checked={c.enabled} onChange={() => toggle(c)} aria-label={`启用链路 ${c.name}`} />
                    <ActionIcon variant="subtle" onClick={() => openEdit(c)} aria-label={`编辑链路 ${c.name}`}>
                      <Pencil size={14} />
                    </ActionIcon>
                    <ActionIcon variant="subtle" color="red" onClick={() => remove(c)} aria-label={`删除链路 ${c.name}`}>
                      <Trash2 size={14} />
                    </ActionIcon>
                  </Group>
                </Group>
              )
            })}
          </Stack>
        )}
      </Stack>

      <Modal opened={modalOpen} onClose={() => setModalOpen(false)} title={editing ? '编辑链路' : '新建链路'} size="lg" centered>
        <Stack gap="md">
          <TextInput
            label="链路名称"
            placeholder="如：双层出口"
            value={name}
            onChange={(e) => setName(e.currentTarget.value)}
          />
          <MultiSelect
            label="链路节点（按选择顺序依次经过）"
            placeholder={options.length ? '搜索并选择节点' : '没有存活节点可选'}
            data={options}
            value={selected}
            onChange={setSelected}
            searchable
            disabled={!options.length}
            filter={egressOptionFilter}
            renderOption={({ option }) => {
              const n = option as unknown as NodeSelectItem
              const geo = geoLabel(n)
              return (
                <Group gap="xs" align="center" wrap="nowrap" style={{ width: '100%' }}>
                  <div style={{ flex: 1, minWidth: 0 }}>
                    <Group gap="xs">
                      <Text size="sm" fw={600} truncate>{option.label}</Text>
                      <Badge size="xs" variant="light">{n.protocol}</Badge>
                    </Group>
                    <Group gap={4} align="center" mt={2}>
                      {geo && (
                        <>
                          <MapPin size={11} />
                          <Text size="xs" c="dimmed" truncate>{geo}</Text>
                        </>
                      )}
                      <Text size="xs" c="dimmed">评分 {n.score}</Text>
                    </Group>
                  </div>
                </Group>
              )
            }}
          />
          {error && (
            <Text size="sm" c="red">{error}</Text>
          )}
          <Group justify="flex-end">
            <Button variant="default" onClick={() => setModalOpen(false)}>取消</Button>
            <Button loading={submitting} onClick={save}>保存</Button>
          </Group>
        </Stack>
      </Modal>
    </Card>
  )
}

export default function Egress() {
  const nodes = usePoolStore((s) => s.nodes)
  const refreshPool = usePoolStore((s) => s.refresh)
  const pinnedNode = useStatusStore((s) => s.status.pinnedNode)
  const refreshStatus = useStatusStore((s) => s.refresh)

  const [config, setConfig] = useState<EgressConfig | null>(null)
  const [busy, setBusy] = useState(false)
  const [notice, setNotice] = useState<NoticeData | null>(null)

  useEffect(() => {
    refreshPool()
    getEgressConfig()
      .then((res) => {
        if (res.code === 0 && res.data) setConfig(res.data)
      })
      .catch(() => {})
  }, [refreshPool])

  // 轻量轮询同步：策略可能从代理池的「指定为固定出口」等入口变更，
  // 保持本页面的策略/固定节点展示与后端一致。
  useEffect(() => {
    const timer = window.setInterval(() => {
      getEgressConfig()
        .then((res) => {
          if (res.code === 0 && res.data) {
            setConfig((prev) => ({ ...prev, ...res.data }))
          }
        })
        .catch(() => {})
    }, 5000)
    return () => window.clearInterval(timer)
  }, [])

  useEffect(() => {
    if (!notice) return
    const timer = window.setTimeout(() => setNotice(null), 4000)
    return () => window.clearTimeout(timer)
  }, [notice])

  // 存活节点下拉数据：带协议/评分/地区，供下拉富文本展示与搜索
  const aliveOptions: NodeSelectItem[] = nodes
    .filter((n) => n.status === 'alive')
    .map((n) => ({
      value: String(n.id),
      label: `${n.host}:${n.port}`,
      protocol: n.protocol,
      score: n.score,
      country: n.country,
      city: n.city,
    }))

  // 当前固定节点可能不在存活列表（如节点已失效）。
  // 若不回显进下拉，Select 会显示裸 ID；这里把固定节点（含失效的）并入选项并禁用，保证回显正确。
  const pinnedNodeId = pinnedNode?.id ?? config?.pinnedNode?.id
  if (pinnedNodeId && !aliveOptions.some((o) => o.value === String(pinnedNodeId))) {
    const n = nodes.find((x) => x.id === pinnedNodeId)
    if (n) {
      aliveOptions.push({
        value: String(n.id),
        label: `${n.host}:${n.port}`,
        protocol: n.protocol,
        score: n.score,
        country: n.country,
        city: n.city,
        disabled: true,
      })
    }
  }

  async function selectStrategy(value: string) {
    setBusy(true)
    try {
      const res = await updateEgressConfig({ strategy: value as EgressConfig['strategy'] })
      if (res.code === 0 && res.data) {
        setConfig(res.data)
        setNotice({ type: 'success', text: `已切换到「${res.data.strategies.find((s) => s.value === value)?.label ?? value}」策略` })
      } else {
        setNotice({ type: 'error', text: res.msg || '切换策略失败' })
      }
    } catch (e) {
      setNotice({ type: 'error', text: getErrorMessage(e) })
    } finally {
      setBusy(false)
    }
  }

  async function selectPinnedNode(id: string | null) {
    if (!id) return
    setBusy(true)
    try {
      const res = await updateEgressConfig({ strategy: 'fixed', pinId: Number(id) })
      if (res.code === 0 && res.data) {
        setConfig(res.data)
        refreshStatus()
        setNotice({ type: 'success', text: '固定出口节点已更新' })
      } else {
        setNotice({ type: 'error', text: res.msg || '设置固定节点失败' })
      }
    } catch (e) {
      setNotice({ type: 'error', text: getErrorMessage(e) })
    } finally {
      setBusy(false)
    }
  }

  async function unpin() {
    setBusy(true)
    try {
      // 取消固定并回到默认的智能加权策略
      const res = await updateEgressConfig({ strategy: 'weighted' })
      if (res.code === 0 && res.data) {
        setConfig(res.data)
        refreshStatus()
        setNotice({ type: 'success', text: '已取消固定出口，恢复智能加权策略' })
      } else {
        setNotice({ type: 'error', text: res.msg || '取消固定失败' })
      }
    } catch (e) {
      setNotice({ type: 'error', text: getErrorMessage(e) })
    } finally {
      setBusy(false)
    }
  }

  const pinnedSelectValue = pinnedNode ? String(pinnedNode.id) : config?.pinnedNode ? String(config.pinnedNode.id) : null

  return (
    <Stack gap="md">
      {notice && (
        <Alert color={notice.type === 'success' ? 'green' : 'red'} withCloseButton onClose={() => setNotice(null)}>
          {notice.text}
        </Alert>
      )}

      <Card padding="lg" radius="md" withBorder style={{ maxWidth: 620 }}>
        <Group gap="sm" align="flex-start">
          <GitBranch size={22} />
          <div>
            <Text fw={700}>出口路由</Text>
            <Text size="sm" c="dimmed" mt={4}>
              集中管理网关出口节点的选择策略。切换后即时生效并自动保存；若当前没有存活节点，所有策略都暂无法出口
            </Text>
          </div>
        </Group>
      </Card>

      <Card padding="lg" radius="md" withBorder style={{ maxWidth: 620 }}>
        <Stack gap="md">
          <Group justify="space-between" wrap="wrap">
            <div>
              <Text fw={700}>出口策略</Text>
              <Text size="sm" c="dimmed" mt={4}>选择网关为每个连接挑选出口节点的方式</Text>
            </div>
            <Group gap="xs">
              <Badge variant="light">存活节点 {config?.aliveCount ?? 0}</Badge>
            </Group>
          </Group>

          <Divider />

          <Radio.Group value={config?.strategy ?? 'weighted'} onChange={selectStrategy} disabled={busy || !config}>
            <Stack gap="sm">
              {(config?.strategies ?? []).map((s) => (
                <Radio.Card key={s.value} value={s.value} radius="md" p="md">
                  <Group gap="sm" align="flex-start" wrap="wrap">
                    <Radio.Indicator mt={2} />
                    <div style={{ flex: 1, minWidth: 220 }}>
                      <Group gap="xs">
                        <Text fw={600} size="sm">{s.label}</Text>
                        {config?.strategy === s.value && (
                          <Badge size="xs" variant="light" color="blue">当前</Badge>
                        )}
                      </Group>
                      <Text size="xs" c="dimmed" mt={2} lh={1.6}>{s.desc}</Text>
                    </div>
                  </Group>
                </Radio.Card>
              ))}
            </Stack>
          </Radio.Group>
        </Stack>
      </Card>

      {config?.strategy === 'fixed' && (
        <Card padding="lg" radius="md" withBorder style={{ maxWidth: 620 }}>
          <Stack gap="md">
            <div>
              <Text fw={700}>固定出口节点</Text>
              <Text size="sm" c="dimmed" mt={4}>
                指定后，该节点存活时全部流量固定走它；节点失效时自动回退智能加权，存活后自动恢复固定
              </Text>
            </div>

            <Divider />

            <Select
              label="选择固定节点"
              placeholder={aliveOptions.length ? '搜索或选择要固定的节点' : '没有存活节点可选'}
              data={aliveOptions}
              value={pinnedSelectValue}
              onChange={selectPinnedNode}
              searchable
              allowDeselect={false}
              disabled={busy || !aliveOptions.length}
              filter={egressOptionFilter}
              renderOption={({ option }) => {
                const n = option as unknown as NodeSelectItem
                const geo = geoLabel(n)
                return (
                  <Group gap="xs" align="center" wrap="nowrap" style={{ width: '100%' }}>
                    <div style={{ flex: 1, minWidth: 0 }}>
                      <Group gap="xs">
                        <Text size="sm" fw={600} truncate>{option.label}</Text>
                        <Badge size="xs" variant="light">{n.protocol}</Badge>
                      </Group>
                      <Group gap={4} align="center" mt={2}>
                        {geo && (
                          <>
                            <MapPin size={11} />
                            <Text size="xs" c="dimmed" truncate>{geo}</Text>
                          </>
                        )}
                        <Text size="xs" c="dimmed">评分 {n.score}</Text>
                      </Group>
                    </div>
                  </Group>
                )
              }}
            />

            {pinnedNode && (
              <Alert color="green" variant="light" withCloseButton onClose={unpin}>
                <Group gap="xs">
                  <Pin size={16} />
                  <Text span inherit>
                    当前固定出口：<Badge variant="light">{pinnedNode.protocol}</Badge>{' '}
                    <Text span fw={600} inherit>{`${pinnedNode.host}:${pinnedNode.port}`}</Text>{' '}
                    <Text span size="sm" inherit>（评分 {pinnedNode.score}）</Text>
                  </Text>
                </Group>
              </Alert>
            )}

            {pinnedNode?.status !== 'alive' && (
              <Alert color="yellow">
                固定节点当前不可用，流量已临时回退智能加权策略；存活后自动恢复固定出口
              </Alert>
            )}

            {!pinnedNode && config.pinnedNode && (
              <Alert color="yellow">
                固定节点（{config.pinnedNode.host}:{config.pinnedNode.port}）已失效，请重新选择或更换策略
              </Alert>
            )}

            <Group justify="space-between" wrap="wrap">
              <Text size="sm" c="dimmed">也可以在代理池的节点行菜单中点击「指定为固定出口」</Text>
              {pinnedNode && (
                <Button variant="light" color="red" leftSection={<PinOff size={16} />} loading={busy} onClick={unpin}>
                  取消固定
                </Button>
              )}
            </Group>
          </Stack>
        </Card>
      )}

      {config?.strategy === 'chain' && <ChainManager nodes={nodes} />}

      {config?.strategy !== 'fixed' && config?.strategy !== 'chain' && (
        <Card padding="lg" radius="md" withBorder style={{ maxWidth: 620 }}>
          <Group gap="sm">
            <RadioTower size={18} />
            <Text size="sm" c="dimmed">
              {config?.strategy === 'best' && '每次请求都会从存活节点中选择评分最高者作为出口。'}
              {config?.strategy === 'random' && '每次请求会随机挑选一个存活节点；同一网站短时间窗口内保持同一出口，避免频繁更换 IP。'}
              {config?.strategy === 'round-robin' && '存活节点会按顺序轮流作为出口，均衡所有节点的负载。'}
              {config?.strategy === 'weighted' && '按 评分/延迟 加权选择出口，叠加失败惩罚（连续失败自动降权）与域名粘性（同一网站短时间窗口内保持同一出口），是最均衡的默认策略。'}
            </Text>
          </Group>
        </Card>
      )}
    </Stack>
  )
}
