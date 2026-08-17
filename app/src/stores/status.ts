import { create } from 'zustand'
import { getErrorMessage, getStatus, startGateway, stopGateway } from '@/api'
import type { SystemStatus } from '@/types'

interface StatusState {
  status: SystemStatus
  refresh: () => Promise<void>
  start: () => Promise<void>
  stop: () => Promise<void>
}

const emptyStatus: SystemStatus = {
  running: false,
  proxyCount: 0,
  aliveCount: 0,
  currentIP: '',
  currentHttpNode: undefined,
  currentSocks5Node: undefined,
  httpProxyBind: '',
  socks5ProxyBind: '',
  version: '',
}

export const useStatusStore = create<StatusState>((set, get) => ({
  status: emptyStatus,
  refresh: async () => {
    const res = await getStatus()
    if (res.code === 0) {
      set((state) => ({
        status: {
          ...state.status,
          ...res.data,
          // 后端这些指针字段带 omitempty，未命中时 JSON 中会缺席，
          // 直接展开会残留旧值（如取消固定出口后 pinnedNode 不消失）。
          // 这里显式同步，让 nil/null/undefined 也能清除旧状态。
          currentNode: res.data.currentNode,
          currentHttpNode: res.data.currentHttpNode,
          currentSocks5Node: res.data.currentSocks5Node,
          pinnedNode: res.data.pinnedNode,
        },
      }))
    }
  },
  start: async () => {
    try {
      const res = await startGateway()
      if (res.code === 0) {
        set((state) => ({
          status: {
            ...state.status,
            running: true,
            httpProxyBind: res.data?.http || state.status.httpProxyBind,
            socks5ProxyBind: res.data?.socks5 || state.status.socks5ProxyBind,
          },
        }))
      }
      await get().refresh()
    } catch (e) {
      throw new Error(getErrorMessage(e))
    }
  },
  stop: async () => {
    try {
      await stopGateway()
      set((state) => ({
        status: {
          ...state.status,
          running: false,
          httpProxyBind: '',
          socks5ProxyBind: '',
        },
      }))
      await get().refresh()
    } catch (e) {
      throw new Error(getErrorMessage(e))
    }
  },
}))