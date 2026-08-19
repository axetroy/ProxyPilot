import { contextBridge, ipcRenderer } from 'electron'
import type { AppSettings } from './app-settings'
import type { UpdaterState } from './updater'
import type { SystemProxyState } from './system-proxy'

contextBridge.exposeInMainWorld('proxypilot', {
  getToken: (): Promise<string> => ipcRenderer.invoke('get-token'),
  getApiBase: (): Promise<string> => ipcRenderer.invoke('get-api-base'),
  getPlatform: (): Promise<string> => ipcRenderer.invoke('get-platform'),
  pickSubscriptionFile: (): Promise<string | null> => ipcRenderer.invoke('pick-subscription-file'),
  getAppSettings: (): Promise<AppSettings> => ipcRenderer.invoke('get-app-settings'),
  setAppSettings: (settings: AppSettings): Promise<AppSettings> => ipcRenderer.invoke('set-app-settings', settings),

  // ---- 更新机制 ----
  getUpdaterState: (): Promise<UpdaterState> => ipcRenderer.invoke('updater:get-state'),
  checkForUpdates: (): Promise<UpdaterState> => ipcRenderer.invoke('updater:check'),
  setAutoUpdate: (enabled: boolean): Promise<UpdaterState> => ipcRenderer.invoke('updater:set-auto-update', enabled),
  installUpdate: (): Promise<void> => ipcRenderer.invoke('updater:install'),
  onUpdaterEvent: (cb: (state: UpdaterState) => void): (() => void) => {
    const listener = (_e: Electron.IpcRendererEvent, state: UpdaterState) => cb(state)
    ipcRenderer.on('updater:event', listener)
    return () => {
      ipcRenderer.removeListener('updater:event', listener)
    }
  },

  // ---- 系统代理 ----
  getSystemProxyState: (): Promise<SystemProxyState> => ipcRenderer.invoke('system-proxy:get-state'),
  setSystemProxy: (enabled: boolean): Promise<SystemProxyState> =>
    ipcRenderer.invoke('system-proxy:set', enabled),
  onSystemProxyEvent: (cb: (state: SystemProxyState) => void): (() => void) => {
    const listener = (_e: Electron.IpcRendererEvent, state: SystemProxyState) => cb(state)
    ipcRenderer.on('system-proxy:event', listener)
    return () => {
      ipcRenderer.removeListener('system-proxy:event', listener)
    }
  },

  onCoreExit: (cb: () => void): (() => void) => {
    const listener = () => cb()
    ipcRenderer.on('core:exit', listener)
    return () => {
      ipcRenderer.removeListener('core:exit', listener)
    }
  },
  onCoreError: (cb: (msg: string) => void): (() => void) => {
    const listener = (_e: Electron.IpcRendererEvent, msg: string) => cb(msg)
    ipcRenderer.on('core:error', listener)
    return () => {
      ipcRenderer.removeListener('core:error', listener)
    }
  },
  onCoreRestarted: (cb: () => void): (() => void) => {
    const listener = () => cb()
    ipcRenderer.on('core:restarted', listener)
    return () => {
      ipcRenderer.removeListener('core:restarted', listener)
    }
  },
})
