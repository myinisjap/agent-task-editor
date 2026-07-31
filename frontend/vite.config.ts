import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

export default defineConfig(({ command }) => ({
  base: command === 'build' ? '/tasks/' : '/',
  plugins: [react(), tailwindcss()],
  server: {
    port: 5173,
    proxy: {
      '/api': 'http://localhost:8080',
      '/ws': { target: 'ws://localhost:8080', ws: true, changeOrigin: true },
    },
  },
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['./src/test/setup.ts'],
    css: true,
    exclude: ['**/node_modules/**', 'e2e/**'],
    coverage: {
      // 'json-summary' emits coverage/coverage-summary.json with a
      // machine-readable "total" block (statements/branches/functions/lines
      // pct), which CI parses to gate the coverage floor. 'text' keeps the
      // existing console table, 'html'/'json' keep the existing artifacts.
      reporter: ['text', 'html', 'json', 'json-summary'],
    },
  },
}))
