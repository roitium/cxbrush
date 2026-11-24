import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'

export default defineConfig({
  plugins: [svelte()],
  // pnpm dev 时把 API 转发给本地 cxbrush serve
  server: { proxy: { '/api': 'http://127.0.0.1:8080' } },
  build: {
    outDir: '../internal/server/web/dist',
    emptyOutDir: true,
  },
})
