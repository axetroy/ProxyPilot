import React from 'react'
import ReactDOM from 'react-dom/client'
import { HashRouter } from 'react-router-dom'
import { MantineProvider, createTheme, Alert, Button, Stack, Text } from '@mantine/core'
import { Notifications } from '@mantine/notifications'
import '@mantine/core/styles.css'
import '@mantine/notifications/styles.css'
import App from './App'
import { initApi } from './api'
import './index.css'

const theme = createTheme({
  primaryColor: 'blue',
  primaryShade: 6,
  defaultRadius: 'md',
  fontFamily: 'Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif',
})

// initApi 失败（core 未启动/无 token）时渲染错误页而非永久空白：
// 提供重试按钮，core 就绪（如手动重启应用或自动重启成功）后可恢复。
function renderRoot(ui: React.ReactNode): void {
  ReactDOM.createRoot(document.getElementById('root') as HTMLElement).render(
    <React.StrictMode>
      <MantineProvider theme={theme} defaultColorScheme="auto">
        <Notifications position="top-right" zIndex={300} />
        {ui}
      </MantineProvider>
    </React.StrictMode>,
  )
}

async function bootstrap(): Promise<void> {
  try {
    await initApi()
  } catch (err) {
    const msg = err instanceof Error ? err.message : String(err)
    console.error('[api] init failed:', msg)
    renderRoot(
      <Stack
        align="center"
        justify="center"
        style={{ height: '100vh', background: 'var(--mantine-color-body)' }}
      >
        <Alert color="red" title="核心引擎初始化失败" style={{ maxWidth: 480 }}>
          <Text size="sm">{msg}</Text>
          <Button
            mt="md"
            variant="light"
            color="red"
            onClick={() => window.location.reload()}
          >
            重试
          </Button>
        </Alert>
      </Stack>,
    )
    return
  }
  renderRoot(<HashRouter><App /></HashRouter>)
}

void bootstrap()