import { useEffect, useRef, useState } from 'react'
import { ArrowDown, Trash2 } from 'lucide-react'
import { ActionIcon, Badge, Box, Button, Card, Group, ScrollArea, Stack, Text } from '@mantine/core'
import { useLogStore } from '@/stores/logs'

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

  // 只渲染最近 200 条，避免日志过多时全量渲染导致界面卡顿。
  const visibleEvents = events.slice(-200)

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
            <Button variant="default" leftSection={<Trash2 size={16} />} onClick={clear}>
              清空
            </Button>
          </Group>
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
                  <Group key={i} align="flex-start" gap="sm">
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