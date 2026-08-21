import { notifications } from '@mantine/notifications'
import { resetApiReady, reconnectLogStream } from '@/api'
import { useStatusStore } from '@/stores/status'

let exitingNotified = false

/**
 * 消费主进程的 core 生命周期事件（core:exit / core:error / core:restarted）：
 * - core 意外退出：提示并交由主进程自动重启；
 * - core 重启成功：重置 API 就绪状态（token/端口可能变化）并刷新状态；
 * - 启动/重启失败：展示错误，方便用户发现后手动处理。
 */
export function handleCoreError(msg: string): void {
  // core 意外退出时主进程会先发 core:exit（已提示"正在自动重启"），
  // 随后补发一条"已退出，正在自动重启…"的 error；此时忽略它，
  // 避免与 exit 提示叠加成两个 toast。其他错误（重启失败/超过重启次数）照常展示。
  if (exitingNotified && msg.includes('正在自动重启')) {
    return
  }
  notifications.show({
    id: 'core-error',
    title: '核心引擎异常',
    message: msg,
    color: 'red',
    autoClose: 6000,
  })
  exitingNotified = false
}

export function handleCoreExit(): void {
  if (exitingNotified) return
  exitingNotified = true
  resetApiReady()
  notifications.show({
    id: 'core-exit',
    title: '核心引擎已退出',
    message: '正在自动重启 proxy-core…',
    color: 'orange',
    autoClose: 4000,
  })
}

export function handleCoreRestarted(): void {
  exitingNotified = false
  resetApiReady()
  // 立即重连日志流，重置退避延迟，避免 core 重启后日志流长时间（最长 15s）不恢复。
  reconnectLogStream()
  // 立即刷新状态与网关运行信息，让 UI 尽快恢复实时数据。
  void useStatusStore.getState().refresh()
  notifications.show({
    id: 'core-restarted',
    title: '核心引擎已恢复',
    message: 'proxy-core 已重启完成',
    color: 'green',
    autoClose: 3000,
  })
}