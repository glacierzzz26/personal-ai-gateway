import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// 开发期把管理 API 代理到网关(同源 → 无 CORS)。部署时由 Caddy 把 dist 当站点根、
// /api /v1 反代给网关即可,前端一律走相对路径。
const gateway = process.env.GATEWAY_UPSTREAM || 'http://127.0.0.1:8787'

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      '/api': { target: gateway, changeOrigin: true },
      '/healthz': { target: gateway, changeOrigin: true },
    },
  },
  build: {
    rollupOptions: {
      output: {
        manualChunks: {
          react: ['react', 'react-dom', 'react-router-dom'],
          antd: ['antd', '@ant-design/icons'],
          echarts: ['echarts'],
          dayjs: ['dayjs'],
        },
      },
    },
  },
})
