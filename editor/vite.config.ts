import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import path from 'node:path';

// Production build emits directly into the orchestrator's embed source.
// `go build ./...` in the orchestrator picks up the result via //go:embed.
const ORCH_DIST = path.resolve(__dirname, '../orchestrator/internal/static/dist');

// Proxy editor → http://localhost:8080 (orchestrator dev) so `npm run dev`
// can hit the real /signup, /login, /me, /ws endpoints during development.
const DEV_TARGET = 'http://localhost:8080';

export default defineConfig({
  plugins: [svelte()],
  build: {
    outDir: ORCH_DIST,
    emptyOutDir: true,
    sourcemap: false
  },
  server: {
    port: 5180,
    proxy: {
      '/signup':  DEV_TARGET,
      '/login':   DEV_TARGET,
      '/logout':  DEV_TARGET,
      '/me':      DEV_TARGET,
      '/ws':      { target: DEV_TARGET, ws: true }
    }
  }
});
