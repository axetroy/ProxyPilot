// app/electron/main/app-settings.ts
/**
 * 应用级设置（Electron 主进程持久化到 userData/settings.json）。
 * 仅存储桌面端自身的行为偏好，与 proxy-core 的 /api/settings 无关。
 */
import { app } from 'electron'
import { readFileSync, writeFileSync } from 'node:fs'
import * as path from 'node:path'

export interface AppSettings {
  closeBehavior: 'minimize' | 'quit'
  /** 自动检查并下载更新，默认开启 */
  autoUpdate: boolean
  /** 开机自动启动，默认关闭 */
  autoLaunch: boolean
  /** 系统代理开关与备份（关闭/退出时按备份还原原设置） */
  systemProxy?: {
    enabled: boolean
    endpoint?: string
    backup?: unknown
  }
}

function settingsFilePath(): string {
  return path.join(app.getPath('userData'), 'settings.json')
}

export function loadAppSettings(): AppSettings {
  try {
    const raw = JSON.parse(readFileSync(settingsFilePath(), 'utf-8')) as Partial<AppSettings>
    return {
      closeBehavior: raw.closeBehavior === 'quit' ? 'quit' : 'minimize',
      // 缺省开启自动更新（旧版本没有该字段）
      autoUpdate: raw.autoUpdate !== false,
      // 缺省关闭开机自启（旧版本没有该字段）
      autoLaunch: raw.autoLaunch === true,
      // 系统代理设置原样透传（旧版本没有该字段）
      ...(raw.systemProxy ? { systemProxy: raw.systemProxy } : {}),
    }
  } catch {
    return { closeBehavior: 'minimize', autoUpdate: true, autoLaunch: false }
  }
}

export function saveAppSettings(settings: AppSettings): void {
  try {
    writeFileSync(settingsFilePath(), JSON.stringify(settings, null, 2), 'utf-8')
  } catch (e) {
    console.error(`[app] 保存设置失败: ${e instanceof Error ? e.message : String(e)}`)
  }
}
