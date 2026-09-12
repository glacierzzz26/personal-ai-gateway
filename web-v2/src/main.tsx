import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { BrowserRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { App as AntdApp, ConfigProvider } from 'antd';
import zhCN from 'antd/locale/zh_CN';
import App from './App';
import { lightTheme } from './styles/tokens';
import './styles/global.css';

const queryClient = new QueryClient({
  defaultOptions: {
    queries: { retry: 0, refetchOnWindowFocus: false, staleTime: 10_000 },
  },
});

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <ConfigProvider locale={zhCN} theme={lightTheme}>
      <AntdApp>
        <QueryClientProvider client={queryClient}>
          <BrowserRouter>
            <App />
          </BrowserRouter>
        </QueryClientProvider>
      </AntdApp>
    </ConfigProvider>
  </StrictMode>,
);
