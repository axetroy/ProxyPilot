import { useEffect, useMemo, useRef, useState } from 'react'
import { ArrowDown, Download, Search, Trash2 } from 'lucide-react'
import {
  ActionIcon,
  Badge,
  Box,
  Button,
  Card,
  Group,
  MultiSelect,
  ScrollArea,
  Stack,
  Switch,
  Text,
  TextInput,
} from '@mantine/core'
import { useLogStore } from '@/stores/logs'
import type { LogEvent } from '@/types'

function levelColor(level?: string) {
  switch (level) {
    case 'error':
      return 'red'
    case 'warn':
      return 'yellow'
    case 'debug':
      return 'blue'
    default:
      return 'gray'
  }
}

function levelLabel(level?: string) {
  switch (level) {
    case 'error':
      return '错误'
    case 'warn':
      return '警告'
    case 'debug':
      return '调试'
    case 'info':
      return '信息'
    default:
      return level ?? '日志'
  }
}

export default function Logs() {
  const events = useLogStore((s) => s.events)
  const clear = useLogStore((s) => s.clear)
  const bodyRef = useRef<HTMLDivElement>(null)
  // 是否跟随滚动到底部：初始为 true；用户向上滚动后置为 false，新日志不再强制滚动
  const stickToBottomRef = useRef(true)
  // 用户不在底部且来了新日志时，显示"回到最新"悬浮按钮
  const [showJumpToBottom, setShowJumpToBottom] = useState(false)
  // 过滤：按级别（多选）与关键词搜索；是否显示进度事件
  const [levels, setLevels] = useState<string[]>(['error', 'warn', 'info', 'debug'])
  const [keyword, setKeyword] = useState('')
  const [showProgress, setShowProgress] = useState(true)

  // 先按过滤条件筛选（完整结果，供渲染与导出共用）；
  // 无关键词时只截取最近 200 条渲染，避免日志过多时全量渲染导致界面卡顿，
  // 搜索时显示全部匹配结果（避免 slice 截断把命中的日志藏掉）。
  const filteredEvents = useMemo(() => {
    const kw = keyword.trim().toLowerCase()
    return events.filter((e) => {
      if (e.type === 'progress') {
        // 进度事件无级别，仅受「进度」开关与关键词共同控制
        if (!showProgress) return false
        if (kw && !(e.message ?? '').toLowerCase().includes(kw)) return false
        return true
      }
      if (!levels.includes(e.level ?? 'info')) return false
      if (kw && !(e.message ?? '').toLowerCase().includes(kw)) return false
      return true
    })
  }, [events, levels, keyword, showProgress])

  const visibleEvents = useMemo(() => {
    return keyword.trim() ? filteredEvents : filteredEvents.slice(-200)
  }, [filteredEvents, keyword])

  // 导出当前过滤结果（不截断）为文本文件
  function downloadLogs(evs: LogEvent[]) {
    const lines = evs.map((e) => {
      const ts = e.receivedAt ? new Date(e.receivedAt).toLocaleString() : '—'
      if (e.type === 'progress') {
        return `[${ts}] [进度] ${e.current}/${e.total}`
      }
      return `[${ts}] [${levelLabel(e.level)}] ${e.message ?? ''}`
    })
    const blob = new Blob([lines.join('\n')], { type: 'text/plain;charset=utf-8' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `proxypilot-logs-${new Date().toISOString().slice(0, 19).replace(/[:T]/g, '-')}.log`
    a.click()
    URL.revokeObjectURL(url)
  }

  // 新日志到来时：若用户停留在底部则自动跟随滚动；否则停留在当前位置，并显示"回到最新"悬浮按钮
  useEffect(() => {
    const el = bodyRef.current
    if (!el) return
    if (stickToBottomRef.current) {
      el.scrollTop = el.scrollHeight
      setShowJumpToBottom(false)
    } else {
      // 用户不在底部且来了新日志 → 提示可跳回最新
      setShowJumpToBottom(true)
    }
  }, [events])

  // 滚动事件：判断用户是否在底部，决定是否继续自动跟随新日志
  function handleScroll() {
    const el = bodyRef.current
    if (!el) return
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 50
    stickToBottomRef.current = atBottom
    // 用户手动滚回底部时隐藏悬浮按钮
    if (atBottom) setShowJumpToBottom(false)
  }

  // 点击"回到最新"：滚动到底部并恢复自动跟随
  function jumpToBottom() {
    const el = bodyRef.current
    if (el) {
      el.scrollTop = el.scrollHeight
      stickToBottomRef.current = true
      setShowJumpToBottom(false)
    }
  }

  return (
    <Stack gap="md" style={{ height: '100%' }}>
      <Card padding="lg" radius="md" withBorder style={{ flex: 1 }}>
        <Group justify="space-between" mb="sm">
          <div>
            <Text fw={600}>实时日志</Text>
            <Text size="sm" c="dimmed" mt={4}>查看抓取、检测和网关运行过程中的实时输出</Text>
          </div>
          <Group gap="sm">
            <Badge color="green" variant="light">在线</Badge>
            <Button variant="default" leftSection={<Download size={16} />} disabled={filteredEvents.length === 0} onClick={() => downloadLogs(filteredEvents)}>
              下载日志
            </Button>
            <Button variant="default" leftSection={<Trash2 size={16} />} onClick={clear}>
              清空
            </Button>
          </Group>
        </Group>
        <Group gap="sm" mb="sm" wrap="wrap">
          <TextInput
            leftSection={<Search size={16} />}
            placeholder="搜索日志内容..."
            value={keyword}
            onChange={(e) => setKeyword(e.currentTarget.value)}
            style={{ flex: 1, minWidth: 200 }}
            aria-label="搜索日志内容"
          />
          <MultiSelect
            data={[
              { value: 'error', label: '错误' },
              { value: 'warn', label: '警告' },
              { value: 'info', label: '信息' },
              { value: 'debug', label: '调试' },
            ]}
            value={levels}
            onChange={(v) => setLevels(v as string[])}
            placeholder="日志级别"
            aria-label="按日志级别过滤"
            style={{ minWidth: 180 }}
            clearable
          />
          <Switch
            checked={showProgress}
            onChange={(e) => setShowProgress(e.currentTarget.checked)}
            label="进度"
          />
        </Group>
        <Box style={{ position: 'relative' }}>
          <ScrollArea
            viewportRef={bodyRef}
            viewportProps={{ onScroll: handleScroll }}
            style={{ height: 'calc(100vh - 240px)', background: 'var(--mantine-color-body)', borderRadius: 12, border: '1px solid var(--mantine-color-default-border)' }}
          >
            <Stack gap={4} p="sm">
              {visibleEvents.length === 0 ? (
                <Text size="sm" c="dimmed">等待事件...</Text>
              ) : (
                visibleEvents.map((e, i) => (
                  <Group key={e.seq ?? i} align="flex-start" gap="sm">
                    {e.type === 'progress' ? (
                      <Badge color="blue" variant="light" size="sm">
                        {e.current}/{e.total}
                      </Badge>
                    ) : (
                      <Badge color={levelColor(e.level)} variant="light" size="sm">
                        {levelLabel(e.level)}
                      </Badge>
                    )}
                    <Text size="xs" style={{ wordBreak: 'break-all' }}>
                      {e.message || e.type}
                    </Text>
                  </Group>
                ))
              )}
            </Stack>
          </ScrollArea>
          {showJumpToBottom && (
            <ActionIcon
              variant="filled"
              color="blue"
              size="lg"
              radius="xl"
              onClick={jumpToBottom}
              aria-label="回到最新日志"
              style={{ position: 'absolute', right: 16, bottom: 16, boxShadow: '0 4px 12px rgba(0,0,0,0.2)' }}
            >
              <ArrowDown size={18} />
            </ActionIcon>
          )}
        </Box>
      </Card>
    </Stack>
  )
}