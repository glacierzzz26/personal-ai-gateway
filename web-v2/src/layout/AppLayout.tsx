import { useEffect, useMemo, useRef, useState } from 'react';
import { NavLink, Outlet, useLocation, useNavigate } from 'react-router-dom';
import clsx from 'clsx';
import { useQuery } from '@tanstack/react-query';
import ChangePasswordModal from '@/components/ChangePasswordModal';
import AnnouncementModal from '@/components/AnnouncementModal';
import CmdK from '@/layout/CmdK';
import { Icon } from '@/ui/icons';
import { Menu } from '@/ui/overlay';
import type { MenuItem } from '@/ui/overlay';
import { confirm, closeAllOverlays } from '@/ui/overlay';
import { toast } from '@/ui/toast';
import { NAV_GROUPS, crumbOf, visibleNav } from '@/layout/nav';
import { useUi } from '@/stores/ui';
import { useSession } from '@/stores/session';
import { useTheme } from '@/ui/theme';
import { api } from '@/services/api';
import { queryClient } from '@/main';

/** 全局状态 chip 三态 —— 由后端健康检查驱动，不是装饰。 */
type GateStatus = 'ok' | 'warn' | 'err' | 'loading';

const GATE_DOT: Record<GateStatus, string> = { ok: 'g', warn: 'a', err: 'r', loading: 's' };
const GATE_TITLE: Record<GateStatus, string> = {
  ok: '网关运行正常',
  warn: '网关降级运行',
  err: '网关不可达（/healthz 探活失败）',
  loading: '正在读取网关状态',
};

/** 侧栏 brand 的路线图标记（原型内联 SVG，非 P 图标集）。 */
function BrandMark() {
  return (
    <div className="brand-mark">
      <svg
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth={1.8}
        strokeLinecap="round"
        strokeLinejoin="round"
      >
        <circle cx="6" cy="19" r="2.4" />
        <circle cx="18" cy="5" r="2.4" />
        <path d="M8.4 19h7.9a3.4 3.4 0 0 0 0-6.8H7.7a3.4 3.4 0 0 1 0-6.8H15" />
      </svg>
    </div>
  );
}

export default function AppLayout() {
  const navigate = useNavigate();
  const { pathname } = useLocation();
  const collapsed = useUi(s => s.collapsed);
  const toggleCollapsed = useUi(s => s.toggleCollapsed);
  const setCmdkOpen = useUi(s => s.setCmdkOpen);
  const admin = useSession(s => s.admin);
  const setAdmin = useSession(s => s.setAdmin);
  const themeMode = useTheme(s => s.mode);
  const toggleTheme = useTheme(s => s.toggle);
  const isAdmin = admin?.role === 'admin';
  const [pwOpen, setPwOpen] = useState(false);
  const [menuOpen, setMenuOpen] = useState(false);
  const contentRef = useRef<HTMLDivElement>(null);
  /** /healthz 往返耗时（顶栏与侧栏 chip 展示）。 */
  const [lat, setLat] = useState<number | null>(null);

  /**
   * 全局状态：探 /healthz。管理面自身可达 = 网关在跑；
   * 数据面不可达会由 /healthz 的 store 字段反映为 warn。
   */
  const { data: health, isError } = useQuery({
    queryKey: ['healthz'],
    queryFn: async () => {
      const t0 = performance.now();
      const r = await fetch('/healthz', { credentials: 'include' });
      if (!r.ok) throw new Error(String(r.status));
      setLat(Math.round(performance.now() - t0));
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

  const items = useMemo(() => visibleNav(isAdmin), [isAdmin]);
  const crumb = crumbOf(pathname, isAdmin);
  const username = admin?.username ?? '';
  const avatarText = username ? username.slice(0, 2).toUpperCase() : 'GW';

  // 切页：内容区回顶 + 关闭所有弹层（对齐原型 go()）。
  useEffect(() => {
    if (contentRef.current) contentRef.current.scrollTop = 0;
    closeAllOverlays();
  }, [pathname]);

  const doLogout = async () => {
    const ok = await confirm({
      title: '退出登录？',
      content: (
        <p style={{ fontSize: 12, color: 'var(--t2)', lineHeight: 1.6 }}>
          会话失效后返回登录页（会话滑动续期：空闲 1h 失效，7 天绝对上限）。
        </p>
      ),
      okText: '退出',
      danger: true,
    });
    if (!ok) return;
    try {
      await api.logout();
    } catch {
      /* 会话可能已失效，照样回登录页 */
    }
    // 清空查询缓存:部分响应按角色收敛(如 /models),留着会被下一个登录的账号读到(见 App.tsx 同名处理)。
    queryClient.clear();
    setAdmin(null);
    toast('已退出登录', 'info');
  };

  const menuItems: MenuItem[] = [
    { label: '修改密码', icon: 'key', onClick: () => setPwOpen(true) },
    ...(isAdmin ? [{ label: '系统设置', icon: 'sliders' as const, onClick: () => navigate('/settings') }] : []),
    { label: '切换深色 / 浅色', icon: 'sun', onClick: toggleTheme },
    { label: '刷新当前页', icon: 'refresh', onClick: () => navigate(0) },
    { sep: true },
    { label: '退出登录', icon: 'x', danger: true, onClick: doLogout },
  ];

  return (
    <div className="app">
      {/* ============ 侧栏 ============ */}
      <aside className={clsx('sidebar', collapsed && 'collapsed')}>
        <div className="brand">
          <BrandMark />
          <div className="brand-text">
            <div className="brand-name">AI Gateway</div>
            <div className="brand-sub">site · web-v2 · dev</div>
          </div>
        </div>

        <nav className="nav" aria-label="主导航">
          {NAV_GROUPS.map(g => {
            const inGroup = items.filter(it => it.group === g);
            if (inGroup.length === 0) return null;
            return (
              <div key={g}>
                <div className="nav-group">{g}</div>
                {inGroup.map(it => (
                  <NavLink
                    key={it.key}
                    to={it.key}
                    className={({ isActive }) => clsx('nav-item', isActive && 'active')}
                    title={collapsed ? it.label : undefined}
                    aria-current={pathname === it.key ? 'page' : undefined}
                  >
                    <Icon name={it.icon} size={15} />
                    <span className="nav-label">{it.label}</span>
                  </NavLink>
                ))}
              </div>
            );
          })}
        </nav>

        <div className="side-foot">
          <div className="gw-chip" title={GATE_TITLE[gate]}>
            <span className={clsx('dot', GATE_DOT[gate])} />
            <span className="gtext">
              {location.host} · {lat != null ? `${lat}ms` : '…'}
            </span>
          </div>
        </div>
      </aside>

      {/* ============ 主区 ============ */}
      <div className="main">
        <header className="topbar">
          <button
            className="icon-btn"
            onClick={toggleCollapsed}
            aria-label={collapsed ? '展开侧边栏' : '折叠侧边栏'}
            title="折叠侧边栏"
          >
            <svg
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth={1.5}
              strokeLinecap="round"
              strokeLinejoin="round"
            >
              <rect x="3" y="3" width="18" height="18" rx="1" />
              <path d="M9 3v18" />
            </svg>
          </button>

          <div className="crumbs" aria-label="面包屑">
            <span>site</span>
            <Icon name="right" size={12} />
            {crumb.group ? (
              <>
                <span>{crumb.group}</span>
                <Icon name="right" size={12} />
              </>
            ) : null}
            <span className="cur">{crumb.label}</span>
          </div>

          <div className="cmd-trigger" onClick={() => setCmdkOpen(true)} role="button" tabIndex={0}>
            <Icon name="search" size={14} />
            <span className="t">搜索模型 / 上游 / 请求 ID</span>
            <span className="kbd">Ctrl K</span>
          </div>

          <div className="top-right">
            <div className="health-chip" title={GATE_TITLE[gate]}>
              <span className={clsx('dot', GATE_DOT[gate])} />
              gateway
              <span className="lat">·{lat != null ? `${lat}ms` : '…'}</span>
            </div>

            <button
              className="icon-btn"
              onClick={() => {
                toggleTheme();
                toast(themeMode === 'dark' ? '已切换为浅色模式' : '已切换为深色模式', 'info');
              }}
              aria-label="切换主题"
              title="切换深色 / 浅色（暗色为核心场景）"
            >
              <Icon name={themeMode === 'dark' ? 'moon' : 'sun'} />
            </button>

            <button className="icon-btn bell" aria-label="告警" title="告警">
              <Icon name="bell" />
              {(gate === 'warn' || gate === 'err') && <span className="dot" />}
            </button>

            <div className="sep" />

            <div className="rel">
              <div
                className="avatar"
                onClick={() => setMenuOpen(o => !o)}
                role="button"
                tabIndex={0}
                aria-haspopup="menu"
                aria-expanded={menuOpen}
                title={username}
              >
                {avatarText}
              </div>
              {menuOpen && <Menu items={menuItems} onClose={() => setMenuOpen(false)} />}
            </div>
          </div>
        </header>

        <div className="content" ref={contentRef}>
          <div className="inner page-in" key={pathname}>
            <Outlet />
          </div>
        </div>
      </div>

      {/* 全站公告:登录后自动拉取未读公告并弹窗(见 AnnouncementModal) */}
      <AnnouncementModal />

      <CmdK />
      <ChangePasswordModal open={pwOpen} onClose={() => setPwOpen(false)} />
    </div>
  );
}
