import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'

// Build output goes to dist/, which the Go server embeds via go:embed.
// In dev, API calls proxy to the running Go server.
const browsers = ['chrome111', 'edge111', 'firefox113', 'safari16.2', 'ios16.2']

export default defineConfig({
  plugins: [svelte()],
  // The oldest browsers supported, as the README states: color-mix() sets the
  // floor. esbuild lowers or prefixes what it can for these and fails the
  // build on syntax they cannot run.
  build: { outDir: 'dist', emptyOutDir: true, target: browsers, cssTarget: browsers },
  server: {
    proxy: { '/api': 'http://localhost:4747' },
  },
})
