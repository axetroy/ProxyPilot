import { create } from 'zustand'
import type { SystemProxyState } from '@/types'

interface SystemProxyStore extends SystemProxyState {
  /** 拉取一次主进程状态并订阅后续事件推送（可重复调用，幂等） */
  init: () => Promise<void>
  /** 开启 / 关闭系统代理（失败时 error 字段携带原因） */
  setEnabled: (enabled: boolean) => Promise<void>
}

let initialized = false

export const useSystemProxyStore = create<SystemProxyStore>((set) => ({
  enabled: false,

  init: async () => {
    if (initialized) return
    initialized = true
    try {
      const state = await window.proxypilot?.getSystemProxyState()
      if (state) set(state)
    } catch {
      // 主进程未就绪时忽略，事件推送到达后会自动补全
    }
    window.proxypilot?.onSystemProxyEvent((s) => set(s))
  },

  setEnabled: async (enabled) => {
    const state = await window.proxypilot?.setSystemProxy(enabled)
    if (state) set(state)
  },
}))
