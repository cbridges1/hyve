import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

// This console assumes it's served from the same origin as the hyve API
// (see src/lib/api/client.ts — every request is a relative path, no
// configurable server URL). In production that's true by deployment
// design; in local dev there's no such shared origin, so proxy the API's
// own paths to a real `hyve cluster-config api run` instance instead —
// point VITE_API_PROXY_TARGET at it (defaults to :8090, that command's
// own default bind port).
const apiProxyTarget = process.env.VITE_API_PROXY_TARGET ?? 'http://localhost:8090'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    proxy: {
      '/api': apiProxyTarget,
      '/auth': apiProxyTarget,
      '/healthz': apiProxyTarget,
      '/docs': apiProxyTarget,
      '/openapi.yaml': apiProxyTarget,
    },
  },
})
