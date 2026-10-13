/* ============================================================
   应用外壳 —— 移植自原型（.app / .sidebar / .topbar / .content）。
   保留原行为：/healthz 30s 探活、版本展示、登出、改密弹窗、
   公告自动弹窗、Ctrl+K 面板；新增：顶栏明暗主题切换。
   ============================================================ */
import { useMemo } from 'react';
import { NavLink, Outlet, useLocation, useNavigate } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { useState } from 'react';
import ChangePasswordModal from '@/components/ChangePasswordModal';
import AnnouncementModal from '@/components/AnnouncementModal';
import CmdK from '@/layout/CmdK';
import { NAV_GROUPS, crumbOf, visibleNav } from '@/layout/nav';
import { Icon } from '@/ui/icons';
import { Dropdown } from '@/ui/overlay';
import { toast } from '@/ui/toast';
import { useTheme } from '@/ui/theme';
import { useUi } from '@/stores/ui';
import { useSession } from '@/stores/session';
import { api } from '@/services/api';
import { queryClient } from '@/main';

/** /healthz 未回版本（本地 go run 未注入、或网关不可达）时的兜底展示，绝不显示 undefined。 */
const VERSION_FALLBACK = 'v0.0.0';

/** 从发布版本串里取短 hash：`v0.0.0-4de1cde`→`4de1cde`（脏工作区后缀 `-dirty` 一并跳过）。 */
function shortHash(version: string): string {
  return version.match(/[0-9a-f]{7,}/)?.[0] ?? version;
}

/** 全局状态 chip 三态 —— 由后端健康检查驱动，不是装饰。 */
type GateStatus = 'ok' | 'warn' | 'err' | 'loading';
const GATE_DOT: Record<GateStatus, string> = { ok: 'g', warn: 'a', err: 'r', loading: 's' };

export default function AppLayout() {
  const navigate = useNavigate();
  const { pathname } = useLocation();
  const collapsed = useUi(s => s.collapsed);
  const toggleCollapsed = useUi(s => s.toggleCollapsed);
  const setCmdkOpen = useUi(s => s.setCmdkOpen);
  const admin = useSession(s => s.admin);
  const setAdmin = useSession(s => s.setAdmin);
  const isAdmin = admin?.role === 'admin';
  const mode = useTheme(s => s.mode);
  const toggleTheme = useTheme(s => s.toggle);
  const [pwOpen, setPwOpen] = useState(false);

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

  /** 发布版本：取 /healthz 的 version，缺失时回退。 */
  const version = health?.version || VERSION_FALLBACK;

  const items = useMemo(() => visibleNav(isAdmin), [isAdmin]);
  const crumb = crumbOf(pathname, isAdmin);
  const username = admin?.username ?? '';
  const avatarLetter = username ? username[0].toUpperCase() : 'A';

  const logout = async () => {
    try {
      await api.logout();
    } catch {
      /* 会话可能已失效，照样回登录页 */
    }
    // 清空查询缓存:部分响应按角色收敛(如 /models),留着会被下一个登录的账号读到(见 App.tsx 同名处理)。
    queryClient.clear();
    setAdmin(null);
    toast.ok('已退出登录');
  };

  return (
    <div className="app">
      {/* ============ 侧栏 ============ */}
      <aside className={`sidebar${collapsed ? ' collapsed' : ''}`}>
        <div className="brand">
          <div className="brand-mark" aria-hidden="true">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.8} strokeLinecap="round" strokeLinejoin="round">
              <circle cx="6" cy="19" r="2.4" /><circle cx="18" cy="5" r="2.4" />
              <path d="M8.4 19h7.9a3.4 3.4 0 0 0 0-6.8H7.7a3.4 3.4 0 0 1 0-6.8H15" />
            </svg>
          </div>
          <div className="brand-text">
            <div className="brand-name">AI Gateway</div>
            <div className="brand-sub">site · {shortHash(version)}</div>
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
                    className={({ isActive }) => `nav-item${isActive ? ' active' : ''}`}
                    aria-current={pathname === it.key ? 'page' : undefined}
                    title={collapsed ? it.label : undefined}
                  >
                    {it.icon}
                    <span className="nav-label">{it.label}</span>
                  </NavLink>
                ))}
              </div>
            );
          })}
        </nav>

        <div className="side-foot">
          <div
            className="gw-chip"
            title={gate === 'err' ? '管理面 /healthz 不可达' : gate === 'warn' ? '数据面降级运行' : '网关运行正常'}
          >
            <span className={`dot ${GATE_DOT[gate]}`} />
            <span className="gtext">
              {collapsed ? shortHash(version) : `${gate === 'err' ? 'gateway · 不可达' : gate === 'warn' ? 'gateway · 降级' : 'gateway · 正常'} · ${version}`}
            </span>
          </div>
        </div>
      </aside>

      {/* ============ 主区 ============ */}
      <div className="main">
        <header className="topbar">
          <button
            type="button"
            className="icon-btn"
            onClick={toggleCollapsed}
            aria-label={collapsed ? '展开侧栏' : '折叠侧栏'}
            title={collapsed ? '展开侧栏' : '折叠侧栏'}
          >
            <Icon n="panelLeft" />
          </button>

          <nav className="crumbs" aria-label="面包屑">
            {crumb.group && (
              <>
                <span>{crumb.group}</span>
                <Icon n="chevR" />
              </>
            )}
            <span className="cur">{crumb.label}</span>
          </nav>

          <div className="cmd-trigger" onClick={() => setCmdkOpen(true)}>
            <Icon n="search" />
            <span className="t">搜索页面 / 跳转</span>
            <span className="kbd">Ctrl K</span>
          </div>

          <div className="top-right">
            <button className="icon-btn" onClick={toggleTheme} title={mode === 'dark' ? '切换到浅色' : '切换到深色'}>
              <Icon n={mode === 'dark' ? 'sun' : 'moon'} />
            </button>
            <div className="sep" />
            <Dropdown
              menu={[
                { key: 'password', label: '修改密码', icon: 'key', onClick: () => setPwOpen(true) },
                ...(isAdmin ? [{ key: 'settings', label: '系统设置', icon: 'sliders', onClick: () => navigate('/settings') }] : []),
                { key: 'logout', label: '退出登录', icon: 'logout', danger: true, onClick: logout },
              ]}
            >
              {() => <div className="avatar" aria-haspopup="menu">{avatarLetter}</div>}
            </Dropdown>
          </div>
        </header>

        <div className="content">
          <div className="inner page-in">
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
