import { create } from 'zustand'
import { connectLogStream } from '@/api'
import type { LogEvent } from '@/types'

interface LogState {
  events: LogEvent[]
  max: number
  connected: boolean
  connect: () => () => void
  push: (e: LogEvent) => void
  clear: () => void
}

// seqCounter 单调递增的日志序号（push 时分配），
// 避免每次 push 依赖数组末元素计算 seq。
let seqCounter = 0

export const useLogStore = create<LogState>((set, get) => ({
  events: [],
  max: 500,
  connected: false,
  connect: () => {
    const close = connectLogStream((e) => get().push(e))
    set({ connected: true })
    return close
  },
  push: (e: LogEvent) => {
    seqCounter += 1
    const { events, max } = get()
    const next = [...events, { ...e, receivedAt: Date.now(), seq: seqCounter }]
    if (next.length > max) {
      next.splice(0, next.length - max)
    }
    set({ events: next })
  },
  clear: () => set({ events: [] }),
}))