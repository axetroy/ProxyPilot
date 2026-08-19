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
    const events = [...get().events, { ...e, receivedAt: Date.now() }]
    if (events.length > get().max) {
      events.splice(0, events.length - get().max)
    }
    set({ events })
  },
  clear: () => set({ events: [] }),
}))