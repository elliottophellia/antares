import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// The dev server listens on all interfaces so the dashboard is reachable over
// Tailscale from another machine; API calls proxy to the Go backend.
export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: { '@': new URL('./src', import.meta.url).pathname },
  },
  server: {
    host: '0.0.0.0',
    port: 5173,
    strictPort: true,
    allowedHosts: true,
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:8787',
        changeOrigin: true,
        ws: true,
      },
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
})
