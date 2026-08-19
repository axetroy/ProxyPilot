import { create } from 'zustand'
import { getErrorMessage, listSubscriptions, addSubscription, deleteSubscription, refreshSubscription, updateSubscription } from '@/api'
import { usePoolStore } from '@/stores/pool'
import type { Subscription } from '@/types'

export interface Notice {
  type: 'success' | 'error'
  text: string
}

interface SubsState {
  subs: Subscription[]
  fetchingIds: number[]
  refreshing: boolean
  submitting: boolean
  notice: Notice | null
  refresh: () => Promise<void>
  add: (name: string, url: string, interval: number) => Promise<boolean>
  update: (id: number, name: string, url: string, interval: number, enabled: boolean) => Promise<boolean>
  remove: (id: number) => Promise<boolean>
  refreshOne: (id: number) => Promise<void>
  clearNotice: () => void
}

export const useSubsStore = create<SubsState>((set, get) => ({
  subs: [],
  fetchingIds: [],
  refreshing: false,
  submitting: false,
  notice: null,
  refresh: async () => {
    set({ refreshing: true, notice: null })
    try {
      const res = await listSubscriptions()
      if (res.code === 0) {
        set({ subs: (res.data as Subscription[]) || [] })
      } else {
        set({ notice: { type: 'error', text: res.msg || '刷新订阅失败' } })
      }
    } catch (e) {
      set({ notice: { type: 'error', text: `刷新失败：${getErrorMessage(e)}` } })
    } finally {
      set({ refreshing: false })
    }
  },
  add: async (name: string, url: string, interval: number) => {
    set({ submitting: true, notice: null })
    try {
      const res = await addSubscription(name, url, interval)
      await get().refresh()
      const sub = res.data as Subscription
      if (sub?.id) {
        await get().refreshOne(sub.id)
      }
      return true
    } catch (e) {
      set({ notice: { type: 'error', text: `添加失败：${getErrorMessage(e)}` } })
      return false
    } finally {
      set({ submitting: false })
    }
  },
  update: async (id: number, name: string, url: string, interval: number, enabled: boolean) => {
    set({ submitting: true, notice: null })
    try {
      await updateSubscription(id, name, url, interval, enabled)
      await get().refresh()
      usePoolStore.getState().refresh()
      set({ notice: { type: 'success', text: `订阅#${id} 已${enabled ? '启用' : '禁用'}` } })
      return true
    } catch (e) {
      set({ notice: { type: 'error', text: `更新失败：${getErrorMessage(e)}` } })
      return false
    } finally {
      set({ submitting: false })
    }
  },
  remove: async (id: number) => {
    try {
      await deleteSubscription(id)
      set({
        subs: get().subs.filter((s) => s.id !== id),
        notice: { type: 'success', text: '订阅已删除' },
      })
      usePoolStore.getState().refresh()
      return true
    } catch (e) {
      set({ notice: { type: 'error', text: `删除失败：${getErrorMessage(e)}` } })
      return false
    }
  },
  refreshOne: async (id: number) => {
    set((s) => ({ fetchingIds: [...s.fetchingIds, id], notice: null }))
    try {
      const res = await refreshSubscription(id)
      await get().refresh()
      const summary = res.data
      // 抓取摘要反馈：确认订阅内容是否有效（total 缺失表示该订阅正在抓取中，防重入跳过）
      let notice: Notice
      if (summary && typeof summary.total === 'number' && summary.total > 0) {
        notice = { type: 'success', text: `订阅 #${id} 抓取完成：解析 ${summary.total} 个节点（新增 ${summary.added}）` }
      } else if (summary && typeof summary.total === 'number') {
        notice = { type: 'error', text: `订阅 #${id} 抓取完成，但未解析到节点（内容可能无效）` }
      } else {
        notice = { type: 'success', text: `订阅 #${id} 抓取成功` }
      }
      set((s) => ({
        fetchingIds: s.fetchingIds.filter((x) => x !== id),
        notice,
      }))
    } catch (e) {
      set((s) => ({
        fetchingIds: s.fetchingIds.filter((x) => x !== id),
        notice: { type: 'error', text: `抓取失败：${getErrorMessage(e)}` },
      }))
    }
  },
  clearNotice: () => set({ notice: null }),
}))