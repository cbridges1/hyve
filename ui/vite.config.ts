import { writeFileSync } from 'node:fs'
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
