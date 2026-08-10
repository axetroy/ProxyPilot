import { contextBridge, ipcRenderer } from 'electron'

contextBridge.exposeInMainWorld('proxypilot', {
  getToken: (): Promise<string> => ipcRenderer.invoke('get-token'),
  getApiBase: (): Promise<string> => ipcRenderer.invoke('get-api-base'),
  getPlatform: (): Promise<string> => ipcRenderer.invoke('get-platform'),
  onCoreExit: (cb: () => void): void => {
    ipcRenderer.on('core:exit', () => cb())
  },
  onCoreError: (cb: (msg: string) => void): void => {
    ipcRenderer.on('core:error', (_e, msg: string) => cb(msg))
  },
})