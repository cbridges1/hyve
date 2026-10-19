import { writeFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { defineConfig } from 'vite';
import vue from '@vitejs/plugin-vue';
import tailwindcss from '@tailwindcss/vite';
import yaml from 'js-yaml';

const here = (p) => fileURLToPath(new URL(p, import.meta.url));

// The extension's pages (../pkg/hyve) import a few Rancher shell modules;
// each is mapped to a plain stand-in in src/shims. Packages they import
// resolve here, since ../pkg/hyve has no node_modules of its own outside
// the extension's build.
export default defineConfig({
  root:    here('.'),
  base:    '/',
  plugins: [
    vue(),
    tailwindcss(),
    {
      // go:embed needs dist to hold something before the first build;
      // keep the checked-in placeholder after emptyOutDir clears it.
      name: 'keep-placeholder',
      closeBundle() {
        writeFileSync(here('../../internal/webui/dist/PLACEHOLDER'), 'See internal/webui/webui.go.\n');
      },
    },
    {
      name: 'yaml',
      transform(src, id) {
        if (id.endsWith('.yaml')) {
          return { code: `export default ${ JSON.stringify(yaml.load(src)) };`, map: null };
        }
      },
    },
  ],
  resolve: {
    alias: [
      { find: '@pkg', replacement: here('../pkg/hyve') },
      { find: '@shell/components/Loading', replacement: here('src/shims/Loading.vue') },
      { find: '@shell/components/form/LabeledSelect', replacement: here('src/shims/LabeledSelect.vue') },
      { find: '@shell/components/form/NameNsDescription', replacement: here('src/shims/NameNsDescription.vue') },
      { find: '@shell/components/form/KeyValue', replacement: here('src/shims/KeyValue.vue') },
      { find: '@shell/components/CruResource', replacement: here('src/shims/CruResource.vue') },
      { find: '@shell/mixins/create-edit-view', replacement: here('src/shims/create-edit-view.js') },
      { find: '@shell/config/query-params', replacement: here('src/shims/query-params.js') },
      { find: '@shell/store/store-types.js', replacement: here('src/shims/store-types.js') },
      { find: '@shell/plugins/steve/steve-class', replacement: here('src/model.js') },
      { find: '@components/Banner', replacement: here('src/shims/components.js') },
      { find: '@components/Form/LabeledInput', replacement: here('src/shims/components.js') },
      { find: '@components/Form/Radio', replacement: here('src/shims/components.js') },
      { find: '@components/Form/Checkbox', replacement: here('src/shims/components.js') },
      { find: /^js-yaml$/, replacement: here('node_modules/js-yaml/dist/js-yaml.mjs') },
      { find: /^vue$/, replacement: here('node_modules/vue/dist/vue.runtime.esm-bundler.js') },
    ],
  },
  define: {
    __VUE_OPTIONS_API__:                     'true',
    __VUE_PROD_DEVTOOLS__:                   'false',
    __VUE_PROD_HYDRATION_MISMATCH_DETAILS__: 'false',
  },
  build: {
    outDir:      here('../../internal/webui/dist'),
    emptyOutDir: true,
    sourcemap:   false,
  },
  server: {
    // `npm run dev` against a host: HYVE_HOST=https://127.0.0.1:30444
    proxy: process.env.HYVE_HOST ? Object.fromEntries(['/v1', '/v1-public', '/k8s'].map((p) => [p, { target: process.env.HYVE_HOST, secure: false, changeOrigin: true }])) : undefined,
    fs:    { allow: [here('..')] },
  },
});
