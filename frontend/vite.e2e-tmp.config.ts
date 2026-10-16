import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
export default defineConfig({
  base: '/tasks/',
  plugins: [react(), tailwindcss()],
  server: { port: 5174, strictPort: true, proxy: {
    '/api': { target: 'http://localhost:8081', ws: true },
    '/ws': { target: 'ws://localhost:8081', ws: true, changeOrigin: true } } },
})
