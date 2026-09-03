import { Suspense, lazy } from 'react';
import { Navigate, Route, Routes } from 'react-router-dom';
import { Skeleton } from 'antd';
import AppLayout from '@/layout/AppLayout';

const Dashboard = lazy(() => import('@/pages/Dashboard'));
const Models = lazy(() => import('@/pages/Models'));
const Channels = lazy(() => import('@/pages/Channels'));
const Routing = lazy(() => import('@/pages/Routing'));
const Tokens = lazy(() => import('@/pages/Tokens'));
const Logs = lazy(() => import('@/pages/Logs'));
const Usage = lazy(() => import('@/pages/Usage'));
const Settings = lazy(() => import('@/pages/Settings'));

function PageLoading() {
  return (
    <div className="gw-page" style={{ padding: 24 }}>
      <Skeleton active paragraph={{ rows: 8 }} />
    </div>
  );
}

export default function App() {
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
        <Route path="usage" element={<Suspense fallback={<PageLoading />}><Usage /></Suspense>} />
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
