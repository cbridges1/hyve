import { createHash } from 'node:crypto'
import { readFileSync, writeFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

const here = (p: string) => fileURLToPath(new URL(p, import.meta.url))

// The build goes into internal/webui/dist, which the hyve binary embeds.
// `npm run dev` proxies the host's paths to a running host:
// HYVE_HOST=https://127.0.0.1:30444 npm run dev
const host = process.env.HYVE_HOST

export default defineConfig({
  plugins: [
    react(),
    tailwindcss(),
    {
      // go:embed needs dist to hold something before the first build;
      // keep the checked-in placeholder after emptyOutDir clears it.
      name: 'keep-placeholder',
      closeBundle() {
        writeFileSync(here('../internal/webui/dist/PLACEHOLDER'), 'See internal/webui/webui.go.\n')
      },
    },
    {
      // The service worker (public/sw.js) names its cache after the build
      // (index.html names every hashed entry file, so its hash changes
      // with any of them) and stores the entry's files on install.
      name: 'service-worker',
      apply: 'build',
      closeBundle() {
        const html = readFileSync(here('../internal/webui/dist/index.html'), 'utf8')
        const build = createHash('sha256').update(html).digest('hex').slice(0, 12)
        const assets = [...new Set([...html.matchAll(/(?:src|href)="(\/assets\/[^"]+)"/g)].map((m) => m[1]))]
        const sw = here('../internal/webui/dist/sw.js')
        writeFileSync(
          sw,
          readFileSync(sw, 'utf8')
            .replace('__HYVE_BUILD__', build)
            .replace('/* __HYVE_PRECACHE__ */ []', JSON.stringify(assets)),
        )
      },
    },
  ],
  build: {
    outDir: here('../internal/webui/dist'),
    emptyOutDir: true,
    sourcemap: false,
  },
  server: {
    proxy: host
      ? Object.fromEntries(['/v1', '/v1-public', '/v3', '/k8s'].map((p) => [p, { target: host, secure: false, changeOrigin: true, ws: true, headers: { Origin: host } }]))
      : undefined,
  },
})
