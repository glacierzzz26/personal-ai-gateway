import { Suspense, lazy, useEffect, useState } from 'react';
import { Navigate, Route, Routes } from 'react-router-dom';
import { Skeleton } from 'antd';
import AppLayout from '@/layout/AppLayout';
import Login from '@/pages/Login';
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

/** 仅管理员可达;普通用户重定向到访问令牌页。 */
function AdminOnly({ children }: { children: React.ReactNode }) {
  const admin = useSession(s => s.admin);
  if (admin?.role !== 'admin') return <Navigate to="/tokens" replace />;
  return <>{children}</>;
}

/** 首页按角色分流:管理员到模型广场,普通用户到访问令牌。 */
function Home() {
  const admin = useSession(s => s.admin);
  return <Navigate to={admin?.role === 'admin' ? '/models' : '/tokens'} replace />;
}

function PageLoading() {
  return (
    <div className="gw-page" style={{ padding: 24 }}>
      <Skeleton active paragraph={{ rows: 8 }} />
    </div>
  );
}

function Boot() {
  return (
    <div
      style={{
        height: '100vh', display: 'flex', flexDirection: 'column', gap: 14,
        alignItems: 'center', justifyContent: 'center', background: 'var(--gw-fill)',
      }}
    >
      <div
        style={{
          width: 44, height: 44, borderRadius: 11, background: 'var(--gw-primary)', color: '#fff',
          fontSize: 22, fontWeight: 700, display: 'flex', alignItems: 'center', justifyContent: 'center',
        }}
      >
        G
      </div>
      <Skeleton active title={false} paragraph={{ rows: 1, width: 160 }} />
    </div>
  );
}

/** 会话守卫:启动拉 /auth/me 恢复会话;未登录渲染 Login(其内部含首启「创建管理员」态)。 */
export default function App() {
  const admin = useSession(s => s.admin);
  const setAdmin = useSession(s => s.setAdmin);
  const [booting, setBooting] = useState(true);

  useEffect(() => {
    setUnauthorizedHandler(() => setAdmin(null)); // 任一请求 401 → 回到登录态
    let live = true;
    api.me()
      .then(a => { if (live) setAdmin(a); })
      .catch(() => { if (live) setAdmin(null); })
      .finally(() => { if (live) setBooting(false); });
    return () => { live = false; setUnauthorizedHandler(null); };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  if (booting) return <Boot />;
  if (!admin) return <Login />;

  return (
    <Routes>
      <Route path="/" element={<AppLayout />}>
        <Route index element={<Home />} />
        <Route
          path="dashboard"
          element={<AdminOnly><Suspense fallback={<PageLoading />}><Dashboard /></Suspense></AdminOnly>}
        />
        <Route path="models" element={<AdminOnly><Suspense fallback={<PageLoading />}><Models /></Suspense></AdminOnly>} />
        <Route path="channels" element={<AdminOnly><Suspense fallback={<PageLoading />}><Channels /></Suspense></AdminOnly>} />
        <Route path="routing" element={<AdminOnly><Suspense fallback={<PageLoading />}><Routing /></Suspense></AdminOnly>} />
        <Route path="tokens" element={<Suspense fallback={<PageLoading />}><Tokens /></Suspense>} />
        <Route path="logs" element={<AdminOnly><Suspense fallback={<PageLoading />}><Logs /></Suspense></AdminOnly>} />
        <Route path="settings" element={<AdminOnly><Suspense fallback={<PageLoading />}><Settings /></Suspense></AdminOnly>} />
        <Route path="users" element={<AdminOnly><Suspense fallback={<PageLoading />}><Users /></Suspense></AdminOnly>} />
        <Route
          path="*"
          element={
            <div className="gw-page" style={{ padding: 48, textAlign: 'center', color: 'var(--gw-text-3)' }}>
              页面不存在
            </div>
          }
        />
      </Route>
    </Routes>
  );
}
