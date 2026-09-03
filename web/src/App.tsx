import { useCallback, useEffect, useMemo, useState } from 'react'
import { Navigate, Route, Routes, useLocation, useNavigate } from 'react-router-dom'
import {
  AlertOutlined,
  ApiOutlined,
  DashboardOutlined,
  KeyOutlined,
  LogoutOutlined,
  MenuFoldOutlined,
  MenuUnfoldOutlined,
  MoonOutlined,
  SafetyCertificateOutlined,
  SunOutlined,
  TableOutlined,
} from '@ant-design/icons'
import { App as AntdApp, Badge, Button, ConfigProvider, Flex, Layout, Menu, Switch, Tag, Tooltip, theme } from 'antd'
import zhCN from 'antd/locale/zh_CN'
import { clearKey, getKey, setKey, setUnauthorizedHandler } from './api/client'
import { listQuota, validateKey } from './api/quota'
import { analyzeQuota } from './components/QuotaAlertCards'
import { AuthContext, useAuth } from './auth/AuthContext'
import { persistThemeMode, ThemeContext, themeConfig, initialThemeMode, useTheme, type ThemeMode } from './theme'
import KeyGate from './KeyGate'
import Overview from './pages/Overview'
import UsageLogs from './pages/UsageLogs'
import UpstreamsPage from './pages/UpstreamsPage'
import KeysPage from './pages/KeysPage'
import QuotaStatusPage from './pages/QuotaStatusPage'

const { Header, Sider, Content } = Layout
const { useToken } = theme

const MENU = [
  { key: '/overview', icon: <DashboardOutlined />, label: '概览' },
  { key: '/usage', icon: <TableOutlined />, label: '用量明细' },
  { key: '/upstreams', icon: <ApiOutlined />, label: '订阅源' },
  { key: '/keys', icon: <KeyOutlined />, label: 'API Keys' },
]
const TITLES: Record<string, string> = {
  '/overview': '概览',
  '/usage': '用量明细',
  '/upstreams': '订阅源',
  '/keys': 'API Keys',
  '/quota': '配额与告警',
}

export default function App() {
  const [mode, setMode] = useState<ThemeMode>(initialThemeMode)
  const dark = mode === 'dark'
  const toggle = useCallback(() => {
    setMode((m) => {
      const next = m === 'dark' ? 'light' : 'dark'
      persistThemeMode(next)
      return next
    })
  }, [])
  const themeValue = useMemo(() => ({ mode, dark, toggle }), [mode, dark, toggle])

  return (
    <ConfigProvider locale={zhCN} theme={themeConfig(mode)}>
      <AntdApp>
        <ThemeContext.Provider value={themeValue}>
          <Inner />
        </ThemeContext.Provider>
      </AntdApp>
    </ConfigProvider>
  )
}

function Inner() {
  const { message } = AntdApp.useApp()
  const [authed, setAuthed] = useState<boolean>(() => !!getKey())

  const login = useCallback(async (key: string) => {
    await validateKey(key) // 校验失败会抛 ApiError
    setKey(key)
    setAuthed(true)
  }, [])
  const logout = useCallback(() => {
    clearKey()
    setAuthed(false)
  }, [])
  const auth = useMemo(() => ({ login, logout }), [login, logout])

  useEffect(() => {
    setUnauthorizedHandler(() => {
      clearKey()
      setAuthed(false)
      message.error('网关密钥无效或已失效,请重新输入')
    })
    return () => setUnauthorizedHandler(null)
  }, [message])

  return <AuthContext.Provider value={auth}>{authed ? <Shell /> : <KeyGate />}</AuthContext.Provider>
}

function Shell() {
  const { dark, toggle } = useTheme()
  const { token } = useToken()
  const nav = useNavigate()
  const loc = useLocation()
  const { modal } = AntdApp.useApp()
  const { logout } = useAuth()
  const [collapsed, setCollapsed] = useState(false)
  const [hardCount, setHardCount] = useState(0)

  // 每 30s 静默刷一次配额告警数,喂给菜单角标;失败不打扰。
  useEffect(() => {
    let alive = true
    const tick = async () => {
      try {
        const rows = await listQuota()
        if (alive) setHardCount(analyzeQuota(rows).bad)
      } catch {
        /* ignore */
      }
    }
    void tick()
    const id = setInterval(() => void tick(), 30000)
    return () => {
      alive = false
      clearInterval(id)
    }
  }, [])

  const items = [
    ...MENU,
    {
      key: '/quota',
      icon: <AlertOutlined />,
      label: (
        <Flex align="center" gap={8}>
          <span>配额与告警</span>
          {hardCount > 0 && <Badge count={hardCount} size="small" />}
        </Flex>
      ),
    },
  ]

  const pageTitle = TITLES[loc.pathname] ?? '管理台'

  const confirmLogout = () => {
    modal.confirm({
      title: '退出管理台?',
      content: '将清除当前浏览器会话保存的网关密钥,下次进入需重新输入。',
      okText: '退出',
      okButtonProps: { danger: true },
      cancelText: '取消',
      onOk: logout,
    })
  }

  return (
    <Layout style={{ minHeight: '100vh' }}>
      <Sider
        width={216}
        collapsible
        collapsed={collapsed}
        trigger={null}
        theme="light"
        className="app-sider"
        style={{ borderRight: `1px solid ${token.colorBorderSecondary}` }}
      >
        <div className="app-logo">
          <div className="app-logo-mark">⇌</div>
          {!collapsed && <div className="app-logo-name">AI 网关</div>}
        </div>
        <Menu
          mode="inline"
          selectedKeys={[loc.pathname]}
          items={items}
          onClick={({ key }) => nav(key)}
          style={{ borderInlineEnd: 'none' }}
        />
      </Sider>
      <Layout>
        <Header
          className="app-header"
          style={{ background: token.colorBgContainer, borderBottom: `1px solid ${token.colorBorderSecondary}` }}
        >
          <Flex align="center" gap={12}>
            <Button
              type="text"
              icon={collapsed ? <MenuUnfoldOutlined /> : <MenuFoldOutlined />}
              onClick={() => setCollapsed((c) => !c)}
            />
            <span className="page-title">{pageTitle}</span>
          </Flex>
          <Flex align="center" gap={12}>
            <Tooltip title="密钥仅在本会话内有效,用于访问网关 /api 管理接口">
              <Tag icon={<SafetyCertificateOutlined />} color="green" style={{ marginInlineEnd: 0 }}>
                已鉴权
              </Tag>
            </Tooltip>
            <Switch
              checked={dark}
              onChange={toggle}
              checkedChildren={<MoonOutlined />}
              unCheckedChildren={<SunOutlined />}
              size="small"
            />
            <Button type="text" danger icon={<LogoutOutlined />} onClick={confirmLogout}>
              退出
            </Button>
          </Flex>
        </Header>
        <Content>
          <div className="page">
            <Routes>
              <Route path="/" element={<Navigate to="/overview" replace />} />
              <Route path="/overview" element={<Overview />} />
              <Route path="/usage" element={<UsageLogs />} />
              <Route path="/upstreams" element={<UpstreamsPage />} />
              <Route path="/keys" element={<KeysPage />} />
              <Route path="/quota" element={<QuotaStatusPage />} />
            </Routes>
          </div>
        </Content>
      </Layout>
    </Layout>
  )
}
