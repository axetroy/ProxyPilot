import { create } from 'zustand'
import { getErrorMessage, listProxies, deleteProxy, deleteProxies, checkProxy, checkProxies, speedTestProxy, speedTestProxies, speedTestAllAlive as speedTestAllAliveApi, pinProxy, unpinProxy } from '@/api'
import { useStatusStore } from '@/stores/status'
import type { ProxyNode } from '@/types'

type NoticeData = { type: 'success' | 'error' | 'info'; text: string }

// formatSpeed 将字节/秒速率格式化为易读速率；0 表示尚未测速。
function formatSpeed(bps?: number): string {
  const b = bps ?? 0
  if (b <= 0) return '未测速'
  const units = ['B/s', 'KB/s', 'MB/s', 'GB/s', 'TB/s']
  let v = b
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(v >= 100 || i === 0 ? 0 : 1)} ${units[i]}`
}

interface PoolState {
  nodes: ProxyNode[]
  loading: boolean
  checkingIds: number[]
  checkingAll: boolean
  speedTestIds: number[]
  notice: NoticeData | null
  refresh: (status?: string, silent?: boolean) => Promise<void>
  remove: (id: number) => Promise<boolean>
  removeMany: (ids: number[]) => Promise<boolean>
  check: (id?: number) => Promise<void>
  checkMany: (ids: number[]) => Promise<void>
  speedTest: (id: number) => Promise<void>
  speedTestMany: (ids: number[]) => Promise<void>
  speedTestAllAlive: () => Promise<void>
  /** 指定某个节点为固定出口 */
  pin: (id: number) => Promise<boolean>
  /** 取消固定出口指定 */
  unpin: () => Promise<boolean>
  clearNotice: () => void
}

export const usePoolStore = create<PoolState>((set, get) => ({
  nodes: [],
  loading: false,
  checkingIds: [],
  checkingAll: false,
  speedTestIds: [],
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
      // 若删除的正是固定出口节点，后端会自动取消指定，这里同步刷新状态
      await useStatusStore.getState().refresh()
      return true
    } catch (e) {
      set({ notice: { type: 'error', text: `删除失败：${getErrorMessage(e)}` } })
      return false
    }
  },
  removeMany: async (ids: number[]) => {
    if (ids.length === 0) return true
    try {
      const res = await deleteProxies(ids)
      if (res.code !== 0) {
        set({ notice: { type: 'error', text: res.msg || '批量删除失败' } })
        return false
      }
      const removed = new Set(ids)
      set({
        nodes: get().nodes.filter((n) => !removed.has(n.id)),
        notice: { type: 'success', text: `已删除 ${res.data?.deleted ?? ids.length} 个节点` },
      })
      // 若删除的含固定出口节点，后端会自动取消指定，这里同步刷新状态
      await useStatusStore.getState().refresh()
      return true
    } catch (e) {
      set({ notice: { type: 'error', text: `批量删除失败：${getErrorMessage(e)}` } })
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
  checkMany: async (ids: number[]) => {
    if (ids.length === 0) return
    set((s) => ({ checkingIds: [...s.checkingIds, ...ids], notice: null }))
    try {
      const res = await checkProxies(ids)
      if (res.code === 0) {
        set({ notice: { type: 'success', text: `已发起 ${ids.length} 个节点检测` } })
      } else {
        set({ notice: { type: 'error', text: res.msg || '批量检测失败' } })
      }
    } catch (e) {
      set({ notice: { type: 'error', text: `批量检测失败：${getErrorMessage(e)}` } })
    } finally {
      const idSet = new Set(ids)
      set((s) => ({ checkingIds: s.checkingIds.filter((x) => !idSet.has(x)) }))
    }
  },
  clearNotice: () => set({ notice: null }),
  speedTest: async (id: number) => {
    set((s) => ({ speedTestIds: [...s.speedTestIds, id], notice: null }))
    try {
      const res = await speedTestProxy(id)
      if (res.code === 0 && res.data?.ok) {
        set((s) => ({
          nodes: s.nodes.map((n) => (n.id === id ? { ...n, speed: res.data.speed, speedAt: new Date().toISOString() } : n)),
          notice: { type: 'success', text: `测速完成：${formatSpeed(res.data.speed)}` },
        }))
      } else {
        set({ notice: { type: 'error', text: res.data?.error || res.msg || '测速失败' } })
      }
    } catch (e) {
      set({ notice: { type: 'error', text: `测速失败：${getErrorMessage(e)}` } })
    } finally {
      set((s) => ({ speedTestIds: s.speedTestIds.filter((x) => x !== id) }))
      window.setTimeout(() => get().refresh(), 1500)
    }
  },
  speedTestMany: async (ids: number[]) => {
    if (ids.length === 0) return
    set((s) => ({ speedTestIds: [...s.speedTestIds, ...ids], notice: null }))
    try {
      const res = await speedTestProxies(ids)
      if (res.code === 0) {
        set({ notice: { type: 'success', text: `已发起 ${ids.length} 个节点测速` } })
      } else {
        set({ notice: { type: 'error', text: res.msg || '批量测速失败' } })
      }
    } catch (e) {
      set({ notice: { type: 'error', text: `批量测速失败：${getErrorMessage(e)}` } })
    } finally {
      const idSet = new Set(ids)
      set((s) => ({ speedTestIds: s.speedTestIds.filter((x) => !idSet.has(x)) }))
      window.setTimeout(() => get().refresh(), 2000)
    }
  },
  speedTestAllAlive: async () => {
    const aliveIds = get().nodes.filter((n) => n.status === 'alive').map((n) => n.id)
    set((s) => ({ notice: null }))
    if (aliveIds.length === 0) {
      set({ notice: { type: 'info', text: '没有存活节点可测速' } })
      return
    }
    set((s) => ({ speedTestIds: [...s.speedTestIds, ...aliveIds] }))
    try {
      const res = await speedTestAllAliveApi()
      if (res.code === 0) {
        if (res.data?.started) {
          set({ notice: { type: 'success', text: `已发起全部存活节点测速（共 ${res.data.total ?? aliveIds.length} 个）` } })
        } else {
          set({ notice: { type: 'info', text: res.data?.message || '没有存活节点可测速' } })
        }
      } else {
        set({ notice: { type: 'error', text: res.msg || '测速全部存活节点失败' } })
      }
    } catch (e) {
      set({ notice: { type: 'error', text: `测速全部存活节点失败：${getErrorMessage(e)}` } })
    } finally {
      const idSet = new Set(aliveIds)
      set((s) => ({ speedTestIds: s.speedTestIds.filter((x) => !idSet.has(x)) }))
      window.setTimeout(() => get().refresh(), 3000)
    }
  },
  pin: async (id: number) => {
    try {
      const res = await pinProxy(id)
      if (res.code === 0) {
        set({ notice: { type: 'success', text: '已把该节点指定为固定出口' } })
      } else {
        set({ notice: { type: 'error', text: res.msg || '指定固定出口失败' } })
        return false
      }
      await useStatusStore.getState().refresh()
      return true
    } catch (e) {
      set({ notice: { type: 'error', text: `指定固定出口失败：${getErrorMessage(e)}` } })
      return false
    }
  },
  unpin: async () => {
    try {
      const res = await unpinProxy()
      if (res.code === 0) {
        set({ notice: { type: 'success', text: '已取消固定出口，恢复按评分自动选择' } })
      } else {
        set({ notice: { type: 'error', text: res.msg || '取消固定出口失败' } })
        return false
      }
      await useStatusStore.getState().refresh()
      return true
    } catch (e) {
      set({ notice: { type: 'error', text: `取消固定出口失败：${getErrorMessage(e)}` } })
      return false
    }
  },
}))
