import { useMemo, useState } from 'react';
import { NavLink, Outlet, useLocation, useNavigate } from 'react-router-dom';
import { App, Tooltip } from 'antd';
import type { MenuProps } from 'antd';
import { useQuery } from '@tanstack/react-query';
import ChangePasswordModal from '@/components/ChangePasswordModal';
import CmdK from '@/layout/CmdK';
import { IconFold } from '@/components/icons';
import { NAV_GROUPS, crumbOf, visibleNav } from '@/layout/nav';
import { useUi } from '@/stores/ui';
import { useSession } from '@/stores/session';
import { api } from '@/services/api';

const SIDER_W = 248;
const SIDER_COLLAPSED_W = 64;
const TOPBAR_H = 62;

/** 全局状态 chip 三态 —— 由后端健康检查驱动，不是装饰。 */
type GateStatus = 'ok' | 'warn' | 'err' | 'loading';

const GATE_TEXT: Record<GateStatus, string> = {
  ok: '运行正常',
  warn: '降级运行',
  err: '网关不可达',
  loading: '状态读取中',
};

function Brand({ collapsed }: { collapsed: boolean }) {
  return (
    <div
      style={{
        height: TOPBAR_H,
        flex: `0 0 ${TOPBAR_H}px`,
        display: 'flex',
        alignItems: 'center',
        gap: 10,
        padding: '0 16px',
        borderBottom: '1px solid var(--gw-border)',
        overflow: 'hidden',
      }}
    >
      <span className="gw-brand-mark" aria-hidden="true">
        G
      </span>
      {!collapsed && (
        <span style={{ fontSize: 16, fontWeight: 600, color: 'var(--gw-text)', whiteSpace: 'nowrap' }}>
          AI Gateway
        </span>
      )}
    </div>
  );
}

export default function AppLayout() {
  const { message } = App.useApp();
  const navigate = useNavigate();
  const { pathname } = useLocation();
  const collapsed = useUi(s => s.collapsed);
  const toggleCollapsed = useUi(s => s.toggleCollapsed);
  const setCmdkOpen = useUi(s => s.setCmdkOpen);
  const admin = useSession(s => s.admin);
  const setAdmin = useSession(s => s.setAdmin);
  const isAdmin = admin?.role === 'admin';
  const [pwOpen, setPwOpen] = useState(false);
  const [userMenuOpen, setUserMenuOpen] = useState(false);
  const [userMenuPos, setUserMenuPos] = useState({ top: 0, left: 0 });

  /**
   * 全局状态：探 /healthz。管理面自身可达 = 网关在跑；
   * 数据面不可达会由 /healthz 的 store 字段反映为 warn。
   */
  const { data: health, isError } = useQuery({
    queryKey: ['healthz'],
    queryFn: async () => {
      const r = await fetch('/healthz', { credentials: 'include' });
      if (!r.ok) throw new Error(String(r.status));
      return (await r.json()) as { ok?: boolean; store?: string; version?: string };
    },
    refetchInterval: 30_000,
    retry: 0,
  });

  const gate: GateStatus = isError
    ? 'err'
    : !health
      ? 'loading'
      : health.ok === false || health.store === 'down'
        ? 'warn'
        : 'ok';
  const gateTone = gate === 'ok' ? 'ok' : gate === 'warn' ? 'warn' : gate === 'err' ? 'err' : 'aux';

  const items = useMemo(() => visibleNav(isAdmin), [isAdmin]);
  const crumb = crumbOf(pathname, isAdmin);
  const username = admin?.username ?? '';
  const avatarLetter = username ? username[0].toUpperCase() : 'A';

  const logout = async () => {
    setUserMenuOpen(false);
    try {
      await api.logout();
    } catch {
      /* 会话可能已失效，照样回登录页 */
    }
    setAdmin(null);
    message.success('已退出登录');
  };

  const userItems: MenuProps['items'] = [
    { key: 'password', label: '修改密码', onClick: () => setPwOpen(true) },
    ...(isAdmin
      ? [{ key: 'settings', label: '系统设置', onClick: () => navigate('/settings') }]
      : []),
    { type: 'divider' as const },
    { key: 'logout', label: '退出登录', danger: true, onClick: logout },
  ];

  return (
    <div style={{ display: 'flex', minHeight: '100vh' }}>
      {/* ============ 侧栏 ============ */}
      <aside
        className="gw-sider"
        style={{
          width: collapsed ? SIDER_COLLAPSED_W : SIDER_W,
          flex: `0 0 ${collapsed ? SIDER_COLLAPSED_W : SIDER_W}px`,
          background: 'var(--gw-card)',
          borderRight: '1px solid var(--gw-border)',
          display: 'flex',
          flexDirection: 'column',
          position: 'sticky',
          top: 0,
          height: '100vh',
          transition: 'width .15s ease, flex-basis .15s ease',
        }}
      >
        <Brand collapsed={collapsed} />

        <nav style={{ flex: 1, overflowY: 'auto', padding: '12px 0 18px' }} aria-label="主导航">
          {NAV_GROUPS.map(g => {
            const inGroup = items.filter(it => it.group === g);
            if (inGroup.length === 0) return null;
            return (
              <div key={g}>
                {!collapsed && (
                  <div
                    style={{
                      fontSize: 12,
                      color: 'var(--gw-text-3)',
                      padding: '16px 22px 7px',
                      letterSpacing: '.04em',
                    }}
                  >
                    {g}
                  </div>
                )}
                {inGroup.map(it => (
                  <NavLink
                    key={it.key}
                    to={it.key}
                    aria-current={pathname === it.key ? 'page' : undefined}
                    title={collapsed ? it.label : undefined}
                    style={({ isActive }) => ({
                      position: 'relative',
                      display: 'flex',
                      alignItems: 'center',
                      justifyContent: collapsed ? 'center' : 'flex-start',
                      gap: 11,
                      width: `calc(100% - 18px)`,
                      margin: '2px 9px',
                      height: 42,
                      padding: collapsed ? 0 : '0 14px',
                      border: 0,
                      background: isActive ? 'var(--gw-primary-50)' : 'transparent',
                      borderRadius: 'var(--gw-r-btn)',
                      fontSize: 14.5,
                      fontWeight: isActive ? 500 : 400,
                      color: isActive ? 'var(--gw-primary)' : 'var(--gw-text-2)',
                      cursor: 'pointer',
                      textAlign: 'left',
                      textDecoration: 'none',
                    })}
                  >
                    {({ isActive }) => (
                      <>
                        {/* 激活项 = 3px 主色竖条 */}
                        {isActive && (
                          <span
                            aria-hidden="true"
                            style={{
                              position: 'absolute',
                              left: 0,
                              top: 6,
                              bottom: 6,
                              width: 3,
                              background: 'var(--gw-primary)',
                            }}
                          />
                        )}
                        <span style={{ flex: '0 0 16px', display: 'inline-flex' }}>{it.icon}</span>
                        {!collapsed && <span>{it.label}</span>}
                      </>
                    )}
                  </NavLink>
                ))}
              </div>
            );
          })}
        </nav>

        {!collapsed && (
          <div
            style={{
              borderTop: '1px solid var(--gw-border)',
              padding: '12px 18px',
              fontSize: 12.5,
              color: 'var(--gw-text-3)',
            }}
          >
            个人网关 · v2
          </div>
        )}
      </aside>

      {/* ============ 主区 ============ */}
      <div style={{ flex: 1, minWidth: 0, display: 'flex', flexDirection: 'column' }}>
        <header
          style={{
            height: TOPBAR_H,
            flex: `0 0 ${TOPBAR_H}px`,
            background: 'var(--gw-card)',
            borderBottom: '1px solid var(--gw-border)',
            display: 'flex',
            alignItems: 'center',
            gap: 12,
            padding: '0 20px',
            position: 'sticky',
            top: 0,
            zIndex: 20,
          }}
        >
          <button
            type="button"
            onClick={toggleCollapsed}
            aria-label={collapsed ? '展开侧栏' : '折叠侧栏'}
            title={collapsed ? '展开侧栏' : '折叠侧栏'}
            style={{
              width: 34,
              height: 34,
              flex: '0 0 34px',
              display: 'inline-flex',
              alignItems: 'center',
              justifyContent: 'center',
              border: 0,
              background: 'transparent',
              borderRadius: 'var(--gw-r-btn)',
              color: 'var(--gw-text-2)',
              cursor: 'pointer',
            }}
          >
            <IconFold />
          </button>

          <nav
            aria-label="面包屑"
            style={{ fontSize: 14, color: 'var(--gw-text-3)', display: 'flex', alignItems: 'center', gap: 7 }}
          >
            {crumb.group && (
              <>
                <span>{crumb.group}</span>
                <span style={{ color: 'var(--gw-border)' }}>/</span>
              </>
            )}
            <b style={{ color: 'var(--gw-text)', fontWeight: 500 }}>{crumb.label}</b>
          </nav>

          <div style={{ marginLeft: 'auto', display: 'flex', alignItems: 'center', gap: 10 }}>
            <button
              type="button"
              className="gw-kbtn"
              onClick={() => setCmdkOpen(true)}
              style={{
                display: 'inline-flex',
                alignItems: 'center',
                gap: 9,
                height: 34,
                padding: '0 9px 0 12px',
                border: '1px solid var(--gw-border)',
                borderRadius: 'var(--gw-r-btn)',
                background: 'var(--gw-card)',
                color: 'var(--gw-text-3)',
                fontSize: 14,
                cursor: 'pointer',
              }}
            >
              搜索 <kbd style={kbdStyle}>Ctrl K</kbd>
            </button>

            <Tooltip title={gate === 'err' ? '管理面 /healthz 不可达' : undefined}>
              <span
                role="status"
                aria-live="polite"
                style={{
                  display: 'inline-flex',
                  alignItems: 'center',
                  gap: 7,
                  height: 34,
                  padding: '0 12px',
                  border: '1px solid var(--gw-border)',
                  borderRadius: 'var(--gw-r-badge)',
                  fontSize: 14,
                  color: 'var(--gw-text-2)',
                  background: 'var(--gw-card)',
                }}
              >
                <i className={`gw-dot ${gateTone}`} />
                {GATE_TEXT[gate]}
              </span>
            </Tooltip>

            <span aria-hidden="true" style={{ width: 1, height: 22, background: 'var(--gw-border)' }} />

            <button
              type="button"
              onClick={e => {
                const r = (e.currentTarget as HTMLElement).getBoundingClientRect();
                setUserMenuPos({ top: r.bottom + 8, left: Math.max(8, r.right - 210) });
                setUserMenuOpen(true);
              }}
              aria-haspopup="menu"
              aria-expanded={userMenuOpen}
              style={{
                display: 'inline-flex',
                alignItems: 'center',
                gap: 9,
                height: 38,
                padding: '0 9px',
                border: 0,
                background: 'transparent',
                borderRadius: 'var(--gw-r-btn)',
                cursor: 'pointer',
                color: 'var(--gw-text-2)',
                fontSize: 14.5,
              }}
            >
              <span
                aria-hidden="true"
                style={{
                  width: 30,
                  height: 30,
                  borderRadius: '50%',
                  background: 'var(--gw-primary)',
                  color: 'var(--gw-card)',
                  display: 'inline-flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                  fontSize: 14,
                  fontWeight: 600,
                }}
              >
                {avatarLetter}
              </span>
              <span>{username}</span>
            </button>
          </div>
        </header>

        <main style={{ flex: 1, padding: '26px 30px 36px', minWidth: 0 }}>
          <Outlet />
        </main>
      </div>

      {/* 用户菜单：自己定位，避免 antd Dropdown 的默认阴影与圆角 */}
      {userMenuOpen && (
        <>
          <div
            style={{ position: 'fixed', inset: 0, zIndex: 49 }}
            onMouseDown={() => setUserMenuOpen(false)}
            aria-hidden="true"
          />
          <div
            role="menu"
            style={{
              position: 'fixed',
              top: userMenuPos.top,
              left: userMenuPos.left,
              width: 210,
              background: 'var(--gw-card)',
              border: '1px solid var(--gw-border)',
              borderRadius: 'var(--gw-r-popup)',
              padding: 6,
              zIndex: 50,
            }}
          >
            {userItems.map((it, i) =>
              it && 'type' in it && it.type === 'divider' ? (
                <div key={`d${i}`} style={{ height: 1, background: 'var(--gw-border)', margin: '6px 0' }} />
              ) : (
                <button
                  key={(it as { key: string }).key}
                  type="button"
                  role="menuitem"
                  onClick={() => {
                    setUserMenuOpen(false);
                    (it as { onClick?: () => void }).onClick?.();
                  }}
                  style={{
                    width: '100%',
                    display: 'flex',
                    alignItems: 'center',
                    height: 38,
                    padding: '0 11px',
                    border: 0,
                    background: 'transparent',
                    borderRadius: 'var(--gw-r-btn)',
                    fontSize: 14.5,
                    color: (it as { danger?: boolean }).danger ? 'var(--gw-err)' : 'var(--gw-text-2)',
                    cursor: 'pointer',
                    textAlign: 'left',
                  }}
                >
                  {(it as { label: React.ReactNode }).label}
                </button>
              ),
            )}
          </div>
        </>
      )}

      <CmdK />
      <ChangePasswordModal open={pwOpen} onClose={() => setPwOpen(false)} />
    </div>
  );
}

const kbdStyle: React.CSSProperties = {
  fontFamily: 'var(--gw-mono)',
  fontSize: 12,
  color: 'var(--gw-text-3)',
  border: '1px solid var(--gw-border)',
  borderRadius: 'var(--gw-r-badge)',
  padding: '2px 6px',
};
