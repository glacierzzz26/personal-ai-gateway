import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import { fileURLToPath, URL } from 'node:url';

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) },
  },
  server: {
    port: 5178,
    host: true,
    proxy: {
      '/api': { target: 'http://localhost:8787', changeOrigin: true },
      '/v1': { target: 'http://localhost:8787', changeOrigin: true },
      // 顶栏全局状态 chip 探 /healthz；不代理的话开发态会恒显示「网关不可达」
      '/healthz': { target: 'http://localhost:8787', changeOrigin: true },
    },
  },
});
