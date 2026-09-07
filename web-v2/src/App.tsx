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
        <Route index element={<Navigate to="/models" replace />} />
        <Route
          path="dashboard"
          element={<Suspense fallback={<PageLoading />}><Dashboard /></Suspense>}
        />
        <Route path="models" element={<Suspense fallback={<PageLoading />}><Models /></Suspense>} />
        <Route path="channels" element={<Suspense fallback={<PageLoading />}><Channels /></Suspense>} />
        <Route path="routing" element={<Suspense fallback={<PageLoading />}><Routing /></Suspense>} />
        <Route path="tokens" element={<Suspense fallback={<PageLoading />}><Tokens /></Suspense>} />
        <Route path="logs" element={<Suspense fallback={<PageLoading />}><Logs /></Suspense>} />
        <Route path="settings" element={<Suspense fallback={<PageLoading />}><Settings /></Suspense>} />
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
