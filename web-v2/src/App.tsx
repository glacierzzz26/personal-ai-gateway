import { Suspense, lazy, useEffect, useState } from 'react';
import { Navigate, Route, Routes } from 'react-router-dom';
import AppLayout from '@/layout/AppLayout';
import Login from '@/pages/Login';
import { SkLines } from '@/components/States';
import { Blocks } from '@/components/Block';
import { setUnauthorizedHandler } from '@/services/http';
import { api } from '@/services/api';
import { useSession } from '@/stores/session';

const Dashboard = lazy(() => import('@/pages/Dashboard'));
const Models = lazy(() => import('@/pages/Models'));
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
    <Routes>
      <Route path="/" element={<AppLayout />}>
        <Route index element={<Home />} />
        <Route path="dashboard" element={wrap(<Dashboard />)} />
        <Route path="models" element={wrap(<Models />)} />
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
  );
}
