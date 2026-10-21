// hyve's own UI: the extension's pages in a small shell, served by hyve's
// host when Rancher isn't installed (HYVE-PROPOSAL.md, "UI: on or off at
// install"). Rancher's store, components and fetch hook are replaced by
// plain stand-ins (store.js, shims/), so the pages are shared as they are.
import { createApp } from 'vue';
import App from './App.vue';
import { createHyveRouter } from './router';
import { createStore } from './store';
import { setModelContext } from './model';
import { auth, setUnauthorizedHandler } from './api';
import { t } from './i18n';
import { session, rememberCLIServer } from './session';
import './styles.css';


// Nuxt's fetch() hook, as Rancher's shell provides it: run before mount,
// with $fetchState.pending while it runs.
const fetchMixin = {
  data() {
    return typeof this.$options.fetch === 'function' ? { hyveFetch: { pending: true, error: null } } : {};
  },
  computed: {
    $fetchState() {
      return this.hyveFetch;
    },
  },
  created() {
    if (typeof this.$options.fetch !== 'function') {
      return;
    }
    this.$fetch = async() => {
      this.hyveFetch.pending = true;
      this.hyveFetch.error = null;
      try {
        await this.$options.fetch.call(this);
      } catch (e) {
        this.hyveFetch.error = e;
        session.error = e?.message || String(e);
        console.error(e); // eslint-disable-line no-console
      } finally {
        this.hyveFetch.pending = false;
      }
    };
    this.$fetch();
  },
};

async function signInFromFragment() {
  // `hyve ui` passes a session in the fragment, which never reaches a
  // server; trade it for the host's cookie and drop it from the URL.
  const m = /(?:^#|&)session=([^&]+)/.exec(window.location.hash);

  if (!m) {
    return;
  }
  const server = /(?:^#|&)server=([^&]+)/.exec(window.location.hash);
  const url = /(?:^#|&)url=([^&]+)/.exec(window.location.hash);

  if (server && url) {
    rememberCLIServer(decodeURIComponent(server[1]), decodeURIComponent(url[1]));
  }
  window.history.replaceState(null, '', window.location.pathname + window.location.search);
  try {
    await auth.redeem(decodeURIComponent(m[1]));
  } catch (e) {
    session.error = `Signing in from the CLI failed: ${ e.message }`;
  }
}

async function start() {
  // Before the router reads the URL, so the session never stays in it.
  await signInFromFragment();
  const store = createStore(t);
  const router = createHyveRouter(session, store);

  setModelContext({ store, router, t });
  setUnauthorizedHandler(() => {
    if (session.user) {
      session.user = null;
      router.push({ name: 'login', query: { redirect: router.currentRoute.value.fullPath } });
    }
  });

  try {
    const me = await auth.me();

    session.user = me;
    session.version = me.version;
    await store.load();
  } catch (e) {
    session.user = null;
  }

  const app = createApp(App);

  app.config.globalProperties.$store = store;
  app.config.globalProperties.t = t;
  app.mixin(fetchMixin);
  app.use(router);
  app.mount('#app');
}

start();
