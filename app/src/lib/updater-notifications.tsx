import { notifications } from '@mantine/notifications'
import { Button, Progress, Text } from '@mantine/core'
import type { UpdaterState } from '@/types'
import { formatBytes } from '@/lib/utils'

/** 主进程更新事件 → 全局通知（任意页面可见，含下载进度与重启安装入口） */
export function handleUpdaterEvent(state: UpdaterState): void {
  switch (state.status) {
    case 'available': {
      notifications.show({
        id: 'update-available',
        title: '发现新版本',
        message: `v${state.latestVersion} 可用，正在自动下载…`,
        color: 'blue',
        autoClose: 5000,
      })
      break
    }
    case 'downloading': {
      const p = state.progress
      if (!p) break
      notifications.show({
        id: 'update-progress',
        title: `正在下载更新 v${state.latestVersion}`,
        message: (
          <div>
            <Progress value={p.percent} size="xs" />
            <Text size="xs" c="dimmed" mt={4}>
              {Math.round(p.percent)}% · {formatBytes(p.transferred)} / {formatBytes(p.total)} · {formatBytes(p.bytesPerSecond)}/s
            </Text>
          </div>
        ),
        color: 'blue',
        autoClose: false,
      })
      break
    }
    case 'downloaded': {
      notifications.hide('update-progress')
      notifications.show({
        id: 'update-downloaded',
        title: '更新已就绪',
        message: (
          <div>
            <Text size="sm">v{state.latestVersion} 已下载完成，重启应用即可安装</Text>
            <Button
              size="xs"
              variant="filled"
              mt={8}
              onClick={() => {
                notifications.hide('update-downloaded')
                void window.proxypilot?.installUpdate()
              }}
            >
              立即重启
            </Button>
          </div>
        ),
        color: 'green',
        autoClose: false,
      })
      break
    }
    case 'not-available': {
      // 仅手动检查时提示「已是最新」，避免自动检查频繁打扰
      if (state.source === 'manual') {
        notifications.show({
          id: 'update-not-available',
          title: '已是最新版本',
          message: `当前 v${state.currentVersion} 已是最新`,
          color: 'green',
          autoClose: 3000,
        })
      }
      break
    }
    case 'error': {
      notifications.hide('update-progress')
      notifications.show({
        id: 'update-error',
        title: '更新失败',
        message: state.error || '检查更新时发生错误',
        color: 'red',
        autoClose: 6000,
      })
      break
    }
    default:
      break
  }
}
