/** 指标采样历史：前端内存环形缓冲，用于绘制趋势图（应用重启即清零，不持久化） */

import type { OverviewStats } from './prometheus-parser'

export interface MetricSample {
  /** 采样时间戳（毫秒） */
  t: number
  poolAlive: number
  poolTotal: number
  activeConns: number
  goroutines: number
  memAllocBytes: number
  /** 流量累计值（字节），用于与上次采样做差计算速率 */
  upTotal: number
  downTotal: number
  /** 流量速率（B/s），由相邻两次采样差值计算；首个样本为 0 */
  upRate: number
  downRate: number
  /** core 运行时长（秒），用于检测 core 重启 */
  uptime: number
}

/** 保留最近 180 个样本（10s 自动刷新 ≈ 30 分钟趋势） */
const MAX_SAMPLES = 180

let history: MetricSample[] = []

/**
 * 记录一次采样，返回更新后的完整历史副本。
 * - 自动计算上下行速率；
 * - 检测到 core 重启（uptime 回退）时清空历史，避免速率出现负值/跳变。
 */
export function recordSample(o: OverviewStats, now = Date.now()): MetricSample[] {
  const last = history.length > 0 ? history[history.length - 1] : null

  if (last && o.uptimeSeconds + 1 < last.uptime) {
    history = []
  }

  let upRate = 0
  let downRate = 0
  if (last && now > last.t) {
    const dt = (now - last.t) / 1000
    // 累计值回退（core 刚重启）时不算速率
    if (o.uploadBytes >= last.upTotal && o.downloadBytes >= last.downTotal) {
      upRate = (o.uploadBytes - last.upTotal) / dt
      downRate = (o.downloadBytes - last.downTotal) / dt
    }
  }

  history.push({
    t: now,
    poolAlive: o.poolAlive,
    poolTotal: o.poolTotal,
    activeConns: o.activeConns,
    goroutines: o.goroutines,
    memAllocBytes: o.memAllocBytes,
    upTotal: o.uploadBytes,
    downTotal: o.downloadBytes,
    upRate,
    downRate,
    uptime: o.uptimeSeconds,
  })
  if (history.length > MAX_SAMPLES) {
    history.splice(0, history.length - MAX_SAMPLES)
  }
  return [...history]
}

/** 当前历史快照（不触发采样） */
export function getHistory(): MetricSample[] {
  return [...history]
}
