import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'

// Served by the Go binary under /admin/ — base makes asset URLs resolve there.
export default defineConfig({
  base: '/admin/',
  plugins: [svelte()],
  build: { outDir: 'dist', emptyOutDir: true },
  server: {
    proxy: { '/api': 'http://localhost:4747' },
  },
})
