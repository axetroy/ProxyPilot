import { useEffect, useMemo } from 'react'
import { NavLink, Navigate, Routes, Route, useLocation } from 'react-router-dom'
import { GitBranch, LayoutDashboard, Network, Rss, ScrollText, Settings, Workflow } from 'lucide-react'
import { Badge, Box, Group, Stack, Text, ThemeIcon } from '@mantine/core'
import { useStatusStore } from '@/stores/status'
import { useLogStore } from '@/stores/logs'
import { useUpdaterStore } from '@/stores/updater'
import { useSystemProxyStore } from '@/stores/system-proxy'
import { handleUpdaterEvent } from '@/lib/updater-notifications'
import Dashboard from '@/views/Dashboard'
import ProxyPool from '@/views/ProxyPool'
import Egress from '@/views/Egress'
import Subscriptions from '@/views/Subscriptions'
import Logs from '@/views/Logs'
import Rules from '@/views/Rules'
import SettingsView from '@/views/Settings'

const navItems = [
  { to: '/dashboard', label: '仪表盘', icon: LayoutDashboard },
  { to: '/proxies', label: '代理池', icon: Network },
  { to: '/egress', label: '出口路由', icon: GitBranch },
  { to: '/rules', label: '智能分流', icon: Workflow },
  { to: '/subscriptions', label: '订阅', icon: Rss },
  { to: '/logs', label: '日志', icon: ScrollText },
  { to: '/settings', label: '设置', icon: Settings },
]

const titles: Record<string, string> = {
  '/dashboard': '仪表盘',
  '/proxies': '代理池',
  '/egress': '出口路由',
  '/rules': '智能分流',
  '/subscriptions': '订阅',
  '/logs': '日志',
  '/settings': '设置',
}

function App() {
  const location = useLocation()
  const status = useStatusStore((s) => s.status)
  const refreshStatus = useStatusStore((s) => s.refresh)
  const connect = useLogStore((s) => s.connect)
  // 页面标题完全由路由派生，无需 state
  const pageTitle = titles[location.pathname] ?? '仪表盘'

  useEffect(() => {
    refreshStatus()
    const closeLog = connect()
    const timer = window.setInterval(() => refreshStatus(), 1000)
    return () => {
      closeLog()
      window.clearInterval(timer)
    }
  }, [refreshStatus, connect])

  // 更新机制 / 系统代理：拉取初始状态、订阅主进程事件，并在任意页面弹出更新提醒
  useEffect(() => {
    void useUpdaterStore.getState().init()
    void useSystemProxyStore.getState().init()
    const off = window.proxypilot?.onUpdaterEvent(handleUpdaterEvent)
    return () => {
      off?.()
    }
  }, [])

  const aliveRate = useMemo(() => {
    if (!status.proxyCount) return '-'
    return `${Math.round((status.aliveCount / status.proxyCount) * 100)}%`
  }, [status.aliveCount, status.proxyCount])

  return (
    <Box style={{ display: 'flex', height: '100vh', background: 'var(--mantine-color-body)' }}>
      <Box style={{ width: 220, borderRight: '1px solid var(--mantine-color-default-border)', background: 'var(--mantine-color-body)' }}>
        <Box style={{ height: 72, display: 'flex', alignItems: 'center', justifyContent: 'center', borderBottom: '1px solid var(--mantine-color-default-border)' }}>
          <Group gap="xs" wrap="nowrap">
            <img src="./icon.png" alt="ProxyPilot" width={28} height={28} style={{ borderRadius: 6 }} />
            <Text fw={700} size="lg">ProxyPilot</Text>
          </Group>
        </Box>
        <Stack gap={6} p="sm">
          {navItems.map((item) => (
            <NavLink key={item.to} to={item.to} style={{ textDecoration: 'none' }}>
              {({ isActive }) => (
                <Group
                  gap="sm"
                  px="sm"
                  py="xs"
                  style={{
                    borderRadius: 10,
                    background: isActive ? 'var(--mantine-primary-color-light)' : 'transparent',
                    color: isActive ? 'var(--mantine-color-text)' : 'var(--mantine-color-dimmed)',
                    transition: 'all 160ms ease',
                  }}
                >
                  <ThemeIcon size="sm" variant={isActive ? 'filled' : 'subtle'} color={isActive ? 'blue' : 'gray'}>
                    <item.icon size={16} />
                  </ThemeIcon>
                  <Text size="sm" fw={isActive ? 600 : 500}>{item.label}</Text>
                </Group>
              )}
            </NavLink>
          ))}
        </Stack>
      </Box>

      <Box style={{ flex: 1, display: 'flex', flexDirection: 'column', minWidth: 0 }}>
        <Box style={{ height: 72, display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '0 24px', borderBottom: '1px solid var(--mantine-color-default-border)', background: 'var(--mantine-color-body)' }}>
          <Text fw={600}>{pageTitle}</Text>
          <Badge color={status.running ? 'green' : 'gray'} variant="light">{status.running ? '网关运行中' : '网关已停止'}</Badge>
        </Box>
        <Box p="lg" style={{ flex: 1, overflow: 'auto' }}>
          <Routes>
            {/* 根路径重定向到仪表盘，保证导航高亮与页面一致 */}
            <Route path="/" element={<Navigate to="/dashboard" replace />} />
            <Route path="/dashboard" element={<Dashboard />} />
            <Route path="/proxies" element={<ProxyPool />} />
            <Route path="/egress" element={<Egress />} />
            <Route path="/rules" element={<Rules />} />
            <Route path="/subscriptions" element={<Subscriptions />} />
            <Route path="/logs" element={<Logs />} />
            <Route path="/settings" element={<SettingsView />} />
          </Routes>
        </Box>
      </Box>
    </Box>
  )
}

export default App