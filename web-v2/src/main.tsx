import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { BrowserRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import App from './App';
import { Toaster } from './ui/toast';
import { OverlayHost } from './ui/overlay';
import { initTheme } from './ui/theme';
import './styles/prototype.css';

export const queryClient = new QueryClient({
  defaultOptions: {
    queries: { retry: 0, refetchOnWindowFocus: false, staleTime: 10_000 },
  },
});

// 首帧主题对齐（index.html 内联脚本已先行设过，这里是 React 侧兜底）。
initTheme();

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <App />
        <Toaster />
        <OverlayHost />
      </BrowserRouter>
    </QueryClientProvider>
  </StrictMode>,
);
