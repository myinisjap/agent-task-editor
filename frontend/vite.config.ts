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
      // v8 only instruments modules a test actually imports, so a brand-new
      // untested file would be absent from the report rather than counted as
      // 0% — meaning adding untested code could never lower the percentage
      // and the CI floor could not detect the regression it exists to
      // detect. all + include forces every source file into the report.
      all: true,
      include: ['src/**/*.{ts,tsx}'],
      exclude: [
        'src/**/*.test.*', // test files themselves
        'src/api/types.ts', // generated from ../openapi.yaml by openapi-typescript
        'src/main.tsx', // app bootstrap; nothing to assert
        'src/test/**', // test setup/helpers (setup.ts, mockApi.ts)
      ],
      // 'json-summary' emits coverage/coverage-summary.json with a
      // machine-readable "total" block (statements/branches/functions/lines
      // pct), which CI parses to gate the coverage floor. 'text' keeps the
      // existing console table, 'html'/'json' keep the existing artifacts.
      reporter: ['text', 'html', 'json', 'json-summary'],
    },
  },
}))
