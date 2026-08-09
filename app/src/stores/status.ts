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