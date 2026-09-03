import { useMemo } from 'react';
import { Outlet, useLocation, useNavigate } from 'react-router-dom';
import {
  AppstoreOutlined, ApartmentOutlined, BarChartOutlined, BulbOutlined,
  DashboardOutlined, FileSearchOutlined, KeyOutlined, MenuFoldOutlined,
  MenuUnfoldOutlined, MoonOutlined, NodeIndexOutlined, SearchOutlined,
  SettingOutlined, SunOutlined,
} from '@ant-design/icons';
import { Avatar, Dropdown, Layout, Menu, Tag, Tooltip, Typography } from 'antd';
import type { MenuProps } from 'antd';
import { useUi } from '@/stores/ui';

const { Header, Sider, Content } = Layout;

const NAV: Record<string, [group: string, label: string]> = {
  '/dashboard': ['概览', '运行总览'],
  '/channels': ['资源', '渠道管理'],
  '/models': ['资源', '模型广场'],
  '/routing': ['资源', '路由规则'],
  '/tokens': ['访问', '访问令牌'],
  '/logs': ['观测', '请求日志'],
  '/usage': ['观测', '用量统计'],
  '/settings': ['系统', '系统设置'],
};

export default function AppLayout() {
  const navigate = useNavigate();
  const { pathname } = useLocation();
  const theme = useUi(s => s.theme);
  const collapsed = useUi(s => s.collapsed);
  const toggleCollapsed = useUi(s => s.toggleCollapsed);
  const toggleTheme = useUi(s => s.toggleTheme);

  const items: MenuProps['items'] = useMemo(
    () => [
      { key: 'g1', type: 'group', label: '概览', children: [
        { key: '/dashboard', icon: <DashboardOutlined />, label: '运行总览' },
      ] },
      { key: 'g2', type: 'group', label: '资源', children: [
        { key: '/channels', icon: <ApartmentOutlined />, label: '渠道管理' },
        { key: '/models', icon: <AppstoreOutlined />, label: '模型广场' },
        { key: '/routing', icon: <NodeIndexOutlined />, label: '路由规则' },
      ] },
      { key: 'g3', type: 'group', label: '访问', children: [
        { key: '/tokens', icon: <KeyOutlined />, label: '访问令牌' },
      ] },
      { key: 'g4', type: 'group', label: '观测', children: [
        { key: '/logs', icon: <FileSearchOutlined />, label: '请求日志' },
        { key: '/usage', icon: <BarChartOutlined />, label: '用量统计' },
      ] },
      { key: 'g5', type: 'group', label: '系统', children: [
        { key: '/settings', icon: <SettingOutlined />, label: '系统设置' },
      ] },
    ],
    [],
  );

  const current = NAV[pathname];

  return (
    <Layout style={{ height: '100vh' }}>
      <Sider
        width={220}
        collapsedWidth={64}
        collapsed={collapsed}
        style={{
          background: 'var(--gw-card)',
          borderRight: '1px solid var(--gw-border)',
        }}
      >
        <div
          style={{
            height: 56, display: 'flex', alignItems: 'center', gap: 10, padding: '0 16px',
            borderBottom: '1px solid var(--gw-border-2)', overflow: 'hidden',
          }}
        >
          <div
            style={{
              width: 26, height: 26, flex: '0 0 26px', borderRadius: 7,
              background: 'var(--gw-primary)', color: '#fff',
              display: 'flex', alignItems: 'center', justifyContent: 'center',
              fontSize: 13, fontWeight: 600,
            }}
          >
            G
          </div>
          {!collapsed && (
            <Typography.Text style={{ fontWeight: 600, fontSize: 15, whiteSpace: 'nowrap' }}>
              AI Gateway
            </Typography.Text>
          )}
        </div>

        <Menu
          mode="inline"
          selectedKeys={[pathname]}
          items={items}
          onClick={({ key }) => navigate(key)}
          style={{ background: 'transparent', borderInlineEnd: 'none', paddingBottom: 24 }}
        />

        {!collapsed && (
          <div
            style={{
              padding: '12px 16px', borderTop: '1px solid var(--gw-border-2)',
              fontSize: 12, color: 'var(--gw-text-3)', whiteSpace: 'nowrap',
            }}
          >
            最后同步 20:31
          </div>
        )}
      </Sider>

      <Layout>
        <Header
          style={{
            height: 56, lineHeight: '56px', padding: '0 20px',
            background: 'var(--gw-card)', borderBottom: '1px solid var(--gw-border)',
            position: 'sticky', top: 0, zIndex: 20,
            display: 'flex', alignItems: 'center', gap: 12,
          }}
        >
          <button
            type="button"
            onClick={toggleCollapsed}
            aria-label={collapsed ? '展开侧栏' : '折叠侧栏'}
            style={{
              width: 32, height: 32, borderRadius: 8, border: 'none', cursor: 'pointer',
              background: 'transparent', color: 'var(--gw-text-2)',
              display: 'flex', alignItems: 'center', justifyContent: 'center',
            }}
          >
            {collapsed ? <MenuUnfoldOutlined /> : <MenuFoldOutlined />}
          </button>

          <span style={{ fontSize: 13, color: 'var(--gw-text-3)' }}>
            {current ? `${current[0]} / ` : ''}
            <b style={{ color: 'var(--gw-text)', fontWeight: 500 }}>{current?.[1]}</b>
          </span>

          <div style={{ marginLeft: 'auto', display: 'flex', alignItems: 'center', gap: 12 }}>
            <button
              type="button"
              style={{
                height: 32, padding: '0 10px', borderRadius: 8, cursor: 'pointer',
                border: '1px solid var(--gw-border)', background: 'var(--gw-card)',
                color: 'var(--gw-text-3)', fontSize: 13,
                display: 'flex', alignItems: 'center', gap: 8,
              }}
            >
              <SearchOutlined />
              搜索
              <kbd
                style={{
                  fontSize: 11, border: '1px solid var(--gw-border)', borderRadius: 4,
                  padding: '1px 5px', background: 'var(--gw-fill)', fontFamily: 'inherit',
                }}
              >
                ⌘K
              </kbd>
            </button>

            <Tooltip title={theme === 'dark' ? '切换到亮色' : '切换到暗色'}>
              <button
                type="button"
                onClick={toggleTheme}
                aria-label="切换主题"
                style={{
                  width: 32, height: 32, borderRadius: 8, border: 'none', cursor: 'pointer',
                  background: 'transparent', color: 'var(--gw-text-2)',
                  display: 'flex', alignItems: 'center', justifyContent: 'center',
                }}
              >
                {theme === 'dark' ? <SunOutlined /> : <BulbOutlined />}
              </button>
            </Tooltip>

            <Tag style={{ marginInlineEnd: 0 }}>Dev</Tag>

            <Dropdown
              menu={{
                items: [
                  { key: 'theme', icon: theme === 'dark' ? <MoonOutlined /> : <BulbOutlined />, label: '切换主题', onClick: toggleTheme },
                  { key: 'settings', icon: <SettingOutlined />, label: '系统设置', onClick: () => navigate('/settings') },
                ],
              }}
            >
              <Avatar style={{ background: 'var(--gw-fill)', color: 'var(--gw-text-2)', cursor: 'pointer' }}>
                U
              </Avatar>
            </Dropdown>
          </div>
        </Header>

        <Content style={{ overflowY: 'auto', padding: 24 }}>
          <Outlet />
        </Content>
      </Layout>
    </Layout>
  );
}
