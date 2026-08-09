import { create } from 'zustand'
import { getErrorMessage, listProxies, deleteProxy, checkProxy } from '@/api'
import type { ProxyNode } from '@/types'

type NoticeData = { type: 'success' | 'error'; text: string }

interface PoolState {
  nodes: ProxyNode[]
  loading: boolean
  checkingIds: number[]
  checkingAll: boolean
  notice: NoticeData | null
  refresh: (status?: string, silent?: boolean) => Promise<void>
  remove: (id: number) => Promise<boolean>
  check: (id?: number) => Promise<void>
  clearNotice: () => void
}

export const usePoolStore = create<PoolState>((set, get) => ({
  nodes: [],
  loading: false,
  checkingIds: [],
  checkingAll: false,
  notice: null,
  refresh: async (status?: string, silent?: boolean) => {
    // silent 为 true 时（定时自动刷新）不触发 loading，避免刷新按钮一直转圈
    if (!silent) set({ loading: true, notice: null })
    try {
      const res = await listProxies(status)
      if (res.code === 0) {
        set({ nodes: (res.data as ProxyNode[]) || [] })
      } else {
        set({ notice: { type: 'error', text: res.msg || '刷新代理池失败' } })
      }
    } catch (e) {
      set({ notice: { type: 'error', text: `刷新失败：${getErrorMessage(e)}` } })
    } finally {
      if (!silent) set({ loading: false })
    }
  },
  remove: async (id: number) => {
    try {
      await deleteProxy(id)
      set({
        nodes: get().nodes.filter((n) => n.id !== id),
        notice: { type: 'success', text: '代理已删除' },
      })
      return true
    } catch (e) {
      set({ notice: { type: 'error', text: `删除失败：${getErrorMessage(e)}` } })
      return false
    }
  },
  check: async (id?: number) => {
    if (id) {
      set((s) => ({ checkingIds: [...s.checkingIds, id], notice: null }))
    } else {
      set({ checkingAll: true, notice: null })
    }
    try {
      const res = await checkProxy(id)
      if (res.code === 0) {
        if (id) {
          set({ notice: { type: 'success', text: `代理 #${id} 检测已发起` } })
        } else {
          set({ notice: { type: 'success', text: '检测任务已启动' } })
        }
      } else {
        set({ notice: { type: 'error', text: res.msg || '检测失败' } })
      }
    } catch (e) {
      set({ notice: { type: 'error', text: `检测失败：${getErrorMessage(e)}` } })
    } finally {
      if (id) {
        set((s) => ({ checkingIds: s.checkingIds.filter((x) => x !== id) }))
      } else {
        set({ checkingAll: false })
      }
    }
  },
  clearNotice: () => set({ notice: null }),
}))