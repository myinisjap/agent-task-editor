import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
export default defineConfig({
  base: '/',
  plugins: [react(), tailwindcss()],
  server: {
    port: 5174,
    proxy: {
      '/api': { target: 'http://localhost:8091', ws: true },
      '/ws': { target: 'ws://localhost:8091', ws: true, changeOrigin: true },
    },
  },
})
