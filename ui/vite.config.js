import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'

// In development, /api goes to a core running on CORE_URL (default localhost:8080).
export default defineConfig({
  plugins: [svelte()],
  server: { proxy: { '/api': { target: process.env.CORE_URL || 'http://localhost:8080', changeOrigin: true } } },
  base: './', // relative asset URLs: the UI may be served under a path prefix
  build: { outDir: 'dist', sourcemap: false },
})
