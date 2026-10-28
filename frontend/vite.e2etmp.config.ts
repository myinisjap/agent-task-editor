import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
export default defineConfig({
  base: '/tasks/',
  plugins: [react(), tailwindcss()],
  server: { port: 5199, strictPort: true, proxy: {
    '/api': { target: 'http://localhost:8091', ws: true },
    '/ws': { target: 'ws://localhost:8091', ws: true, changeOrigin: true },
  } },
})
