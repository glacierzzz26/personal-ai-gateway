import { Suspense, lazy, useEffect, useState } from 'react';
import { Navigate, Route, Routes } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import AppLayout from '@/layout/AppLayout';
import Login from '@/pages/Login';
import { SkLines } from '@/components/States';
import { Blocks } from '@/components/Block';
import { setUnauthorizedHandler } from '@/services/http';
import { api } from '@/services/api';
import { useSession } from '@/stores/session';
import { useCurrency } from '@/stores/currency';

const Dashboard = lazy(() => import('@/pages/Dashboard'));
const Models = lazy(() => import('@/pages/Models'));
const OfficialPricing = lazy(() => import('@/pages/OfficialPricing'));
const Channels = lazy(() => import('@/pages/Channels'));
const Routing = lazy(() => import('@/pages/Routing'));
const Tokens = lazy(() => import('@/pages/Tokens'));
const Logs = lazy(() => import('@/pages/Logs'));
const Settings = lazy(() => import('@/pages/Settings'));
const Users = lazy(() => import('@/pages/Users'));

/** 仅管理员可达；普通用户重定向到访问令牌页。 */
function AdminOnly({ children }: { children: React.ReactNode }) {
  const admin = useSession(s => s.admin);
  if (admin?.role !== 'admin') return <Navigate to="/tokens" replace />;
  return <>{children}</>;
}

/** 首页按角色分流：管理员到运行总览，普通用户到访问令牌。 */
function Home() {
  const admin = useSession(s => s.admin);
  return <Navigate to={admin?.role === 'admin' ? '/dashboard' : '/tokens'} replace />;
}

/** 路由级加载态：与各页区块的骨架同款，避免跳页时白屏。 */
function PageLoading() {
  return (
    <Blocks>
      <div style={{ padding: '18px 0' }}>
        <SkLines rows={['w40', 'w80', 'w60', 'w80', 'w60']} />
      </div>
    </Blocks>
  );
}

/**
 * 水合计价币种：全站价格的展示单位来自 settings.displayCurrency。
 * 仅管理员会话可读 /settings —— 普通用户请求会 401，被 setUnauthorizedHandler 直接登出。
 * 与「系统设置」页共用 ['settings'] 缓存，故在那里改币种会即时反映到全站。
 */
function CurrencySync() {
  const isAdmin = useSession(s => s.admin?.role === 'admin');
  const setCurrency = useCurrency(s => s.setCurrency);
  const { data } = useQuery({
    queryKey: ['settings'],
    queryFn: api.getSettings,
    enabled: isAdmin,
    retry: 0,
    staleTime: 300_000,
  });
  useEffect(() => {
    if (data) setCurrency(data.displayCurrency ?? 'CNY');
  }, [data, setCurrency]);
  return null;
}

function Boot() {
  return (
    <div
      style={{
        height: '100vh',
        display: 'flex',
        flexDirection: 'column',
        gap: 14,
        alignItems: 'center',
        justifyContent: 'center',
        background: 'var(--gw-bg)',
      }}
    >
      <span className="gw-brand-mark lg" aria-hidden="true">
        G
      </span>
      <div style={{ width: 160 }}>
        <SkLines rows={['']} />
      </div>
    </div>
  );
}

/** 会话守卫：启动拉 /auth/me 恢复会话；未登录渲染 Login（含首启「创建管理员」态）。 */
export default function App() {
  const admin = useSession(s => s.admin);
  const setAdmin = useSession(s => s.setAdmin);
  const [booting, setBooting] = useState(true);
  // 订阅计价币种：fmt.price/fmt.usd 以非 React 方式读取它，靠 App 重渲染带动全树刷新。
  useCurrency(s => s.currency);

  useEffect(() => {
    setUnauthorizedHandler(() => setAdmin(null)); // 任一请求 401 → 回到登录态
    let live = true;
    api
      .me()
      .then(a => {
        if (live) setAdmin(a);
      })
      .catch(() => {
        if (live) setAdmin(null);
      })
      .finally(() => {
        if (live) setBooting(false);
      });
    return () => {
      live = false;
      setUnauthorizedHandler(null);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  if (booting) return <Boot />;
  if (!admin) return <Login />;

  const wrap = (el: React.ReactNode) => <AdminOnly><Suspense fallback={<PageLoading />}>{el}</Suspense></AdminOnly>;

  return (
    <>
      <CurrencySync />
      <Routes>
        <Route path="/" element={<AppLayout />}>
          <Route index element={<Home />} />
          <Route path="dashboard" element={wrap(<Dashboard />)} />
          <Route path="models" element={wrap(<Models />)} />
          <Route path="pricing" element={wrap(<OfficialPricing />)} />
          <Route path="channels" element={wrap(<Channels />)} />
          <Route path="routing" element={wrap(<Routing />)} />
          <Route path="tokens" element={<Suspense fallback={<PageLoading />}><Tokens /></Suspense>} />
          <Route path="logs" element={wrap(<Logs />)} />
          <Route path="settings" element={wrap(<Settings />)} />
          <Route path="users" element={wrap(<Users />)} />
          <Route
            path="*"
            element={
              <Blocks>
                <div style={{ padding: 48, textAlign: 'center', color: 'var(--gw-text-3)' }}>页面不存在</div>
              </Blocks>
            }
          />
        </Route>
      </Routes>
    </>
  );
}
