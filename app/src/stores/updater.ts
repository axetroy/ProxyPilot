import { create } from 'zustand'
import type { UpdaterState } from '@/types'

interface UpdaterStore extends UpdaterState {
  /** 拉取一次主进程状态并订阅后续事件推送（可重复调用，幂等） */
  init: () => Promise<void>
  /** 手动检查更新 */
  check: () => Promise<void>
  /** 开关自动更新 */
  setAutoUpdate: (enabled: boolean) => Promise<void>
  /** 重启并安装已下载的更新 */
  install: () => Promise<void>
}

let initialized = false

export const useUpdaterStore = create<UpdaterStore>((set) => ({
  enabled: true,
  status: 'idle',
  source: 'auto',
  currentVersion: '',

  init: async () => {
    if (initialized) return
    initialized = true
    try {
      const state = await window.proxypilot?.getUpdaterState()
      if (state) set(state)
    } catch {
      // 主进程未就绪时忽略，事件推送到达后会自动补全
    }
    window.proxypilot?.onUpdaterEvent((s) => set(s))
  },

  check: async () => {
    const state = await window.proxypilot?.checkForUpdates()
    if (state) set(state)
  },

  setAutoUpdate: async (enabled) => {
    const state = await window.proxypilot?.setAutoUpdate(enabled)
    if (state) set(state)
  },

  install: async () => {
    await window.proxypilot?.installUpdate()
  },
}))
