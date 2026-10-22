<script>
import { session } from './session';
import { auth } from './api';
import { CAPI_CLUSTER, HYVE, PRODUCT } from '@pkg/config/types';
import Logo from './components/Logo.vue';
import Icon from './components/Icon.vue';
import ThemeToggle from './components/ThemeToggle.vue';
import ChangePassword from './components/ChangePassword.vue';
import { download, kubeconfig } from './util';

const USER = 'hyve.io.user';

// The console's shell: a full-width header (logo, theme, account menu)
// over a grouped sidebar and the page.
export default {
  name:       'HyveApp',
  components: {
    Logo, Icon, ThemeToggle, ChangePassword
  },

  data() {
    return {
      session, mobileOpen: false, menuOpen: false, changingPassword: false
    };
  },

  computed: {
    signedIn() {
      return !!this.session.user && !this.$route.meta.public;
    },
    groups() {
      const page = (id, label, icon) => ({
        label, icon, to: { name: `${ PRODUCT }-c-cluster-${ id }`, params: { cluster: '_' } }, page: `${ PRODUCT }-c-cluster-${ id }`
      });
      const list = (resource, label, icon) => ({
        label, icon, to: { name: `${ PRODUCT }-c-cluster-resource`, params: { cluster: '_', resource } }, resource
      });
      const served = (i) => !i.resource || this.$store.getters['management/schemaFor'](i.resource);

      return [
        {
          label: 'Clusters',
          items: [
            page(HYVE.OVERVIEW, 'Overview', 'overview'),
            list(CAPI_CLUSTER, 'Clusters', 'clusters'),
            list(HYVE.TEMPLATE, 'Templates', 'templates'),
          ],
        },
        {
          label: 'Configuration',
          items: [
            list(HYVE.RESOURCE, 'Resources', 'resources'),
            page(HYVE.SECRETS, 'Secrets', 'secrets'),
            page(HYVE.REPOS, 'Repos', 'repos'),
            page(HYVE.PROVIDERS, 'Providers', 'providers'),
          ],
        },
        {
          label: 'Administration',
          items: [
            list(USER, 'Users', 'users'),
            { label: 'Environments', icon: 'environments', to: { name: 'hyve-environments' }, page: 'hyve-environments' },
            { label: 'Access', icon: 'access', to: { name: 'hyve-access' }, page: 'hyve-access' },
          ],
        },
      ].map((g) => ({ ...g, items: g.items.filter(served) })).filter((g) => g.items.length);
    },
    // A page's tabs (a cluster's Resources) are one view: switching them
    // doesn't load it again.
    viewKey() {
      const r = this.$route;

      return r.params.tab ? r.fullPath.replace(`/${ r.params.tab }`, '') : r.fullPath;
    },
    initial() {
      return (this.session.user?.username || '?').slice(0, 1).toUpperCase();
    },
  },

  watch: {
    $route() {
      this.session.error = '';
      this.mobileOpen = false;
      this.menuOpen = false;
    },
  },

  methods: {
    async downloadKubeconfig() {
      this.menuOpen = false;
      try {
        download('hyve-local.kubeconfig', await kubeconfig('local'));
      } catch (e) {
        this.session.error = e.message;
      }
    },
    async signOut() {
      try {
        await auth.logout();
      } catch (e) {}
      this.session.user = null;
      this.$router.push({ name: 'login' });
    },
    active(item) {
      const r = this.$route;

      if (item.resource) {
        return r.params.resource === item.resource;
      }

      return r.name === item.page || (item.page === `${ PRODUCT }-c-cluster-${ HYVE.CREATE_CLUSTER }` && r.name === item.page);
    },
    linkClass(item) {
      return [
        'flex items-center gap-2.5 rounded-lg px-3 py-2 text-sm font-medium no-underline transition-colors hover:no-underline',
        this.active(item) ? 'bg-neutral-900 text-white dark:bg-white dark:text-neutral-900' : 'text-neutral-600 hover:bg-neutral-100 dark:text-neutral-400 dark:hover:bg-neutral-800/70',
      ];
    },
  },
};
</script>

<template>
  <router-view v-if="!signedIn" />
  <div
    v-else
    class="flex min-h-screen flex-col bg-neutral-50 dark:bg-neutral-950"
  >
    <header class="sticky top-0 z-30 flex h-14 shrink-0 items-center justify-between border-b border-neutral-200 bg-white px-4 sm:px-6 dark:border-neutral-800 dark:bg-neutral-900">
      <div class="flex items-center gap-3">
        <button
          type="button"
          aria-label="Open menu"
          class="-ml-1.5 rounded-lg p-1.5 text-neutral-600 hover:bg-neutral-100 md:hidden dark:text-neutral-400 dark:hover:bg-neutral-800"
          @click="mobileOpen = true"
        >
          <Icon
            name="menu"
            :size="22"
          />
        </button>
        <router-link
          to="/"
          class="hidden md:block"
        >
          <Logo />
        </router-link>
      </div>
      <div class="flex items-center gap-4">
        <div class="hidden md:block">
          <ThemeToggle />
        </div>
        <div class="hidden h-6 w-px bg-neutral-200 md:block dark:bg-neutral-800" />
        <div class="relative">
          <button
            type="button"
            :aria-expanded="menuOpen"
            aria-label="Account menu"
            class="flex items-center gap-2 rounded-full py-1 pr-2 pl-1 transition-colors hover:bg-neutral-100 dark:hover:bg-neutral-800"
            @click="menuOpen = !menuOpen"
          >
            <span class="flex h-8 w-8 items-center justify-center rounded-full bg-neutral-900 text-xs font-semibold text-white dark:bg-white dark:text-neutral-900">{{ initial }}</span>
            <Icon
              name="chevronDown"
              :size="14"
              class="text-neutral-400"
            />
          </button>
          <template v-if="menuOpen">
            <button
              type="button"
              aria-label="Close menu"
              class="fixed inset-0 z-40"
              @click="menuOpen = false"
            />
            <div class="absolute right-0 z-50 mt-2 w-56 rounded-xl border border-neutral-200 bg-white p-3 shadow-lg ring-1 ring-black/5 dark:border-neutral-700 dark:bg-neutral-800 dark:ring-white/10">
              <div class="flex items-center justify-between">
                <span class="truncate text-sm font-medium text-neutral-900 dark:text-neutral-100">{{ session.user.username }}</span>
                <span
                  v-if="session.user.groups.length"
                  class="shrink-0 rounded-full bg-neutral-100 px-2 py-0.5 text-xs font-medium text-neutral-600 dark:bg-neutral-700 dark:text-neutral-300"
                >{{ session.user.groups[0] }}</span>
              </div>
              <span class="mt-0.5 block truncate text-xs text-neutral-500">hyve {{ session.version }}</span>
              <div class="mt-3 border-t border-neutral-100 pt-3 md:hidden dark:border-neutral-700">
                <span class="mb-1.5 block text-xs font-medium text-neutral-500">Theme</span>
                <ThemeToggle />
              </div>
              <!-- Local mode signs the browser in by itself; its admin's
                   password is the CLI's to keep. -->
              <button
                v-if="!session.user?.autoSignIn"
                type="button"
                class="mt-3 w-full rounded-lg px-2.5 py-1.5 text-left text-sm font-medium text-neutral-500 transition-colors hover:bg-neutral-100 hover:text-neutral-900 dark:hover:bg-neutral-700/70 dark:hover:text-neutral-100"
                @click="menuOpen = false; changingPassword = true"
              >
                Change password
              </button>
              <button
                type="button"
                class="w-full rounded-lg px-2.5 py-1.5 text-left text-sm font-medium text-neutral-500 transition-colors hover:bg-neutral-100 hover:text-neutral-900 dark:hover:bg-neutral-700/70 dark:hover:text-neutral-100"
                :class="{ 'mt-3': session.user?.autoSignIn }"
                title="A kubeconfig for the management cluster that runs 'hyve token'; it holds no secret"
                @click="downloadKubeconfig"
              >
                Download kubeconfig
              </button>
              <button
                v-if="!session.user?.autoSignIn"
                type="button"
                class="w-full rounded-lg px-2.5 py-1.5 text-left text-sm font-medium text-neutral-500 transition-colors hover:bg-neutral-100 hover:text-neutral-900 dark:hover:bg-neutral-700/70 dark:hover:text-neutral-100"
                @click="signOut"
              >
                Sign out
              </button>
            </div>
          </template>
        </div>
      </div>
    </header>

    <div
      v-if="mobileOpen"
      class="fixed inset-0 z-40 md:hidden"
    >
      <button
        type="button"
        aria-label="Close menu"
        class="absolute inset-0 bg-black/40"
        @click="mobileOpen = false"
      />
      <div class="absolute inset-y-0 left-0 w-72 max-w-[85vw] overflow-y-auto border-r border-neutral-200 bg-white shadow-xl dark:border-neutral-800 dark:bg-neutral-900">
        <div class="flex items-center justify-between p-2 pl-3">
          <Logo />
          <button
            type="button"
            aria-label="Close menu"
            class="rounded-lg p-1.5 text-neutral-500 hover:bg-neutral-100 dark:hover:bg-neutral-800"
            @click="mobileOpen = false"
          >
            <Icon name="close" />
          </button>
        </div>
        <nav class="space-y-3 px-2.5 pb-3">
          <div
            v-for="g in groups"
            :key="g.label"
          >
            <div class="px-3 pt-4 pb-1 text-[11px] font-semibold tracking-wider text-neutral-400 uppercase dark:text-neutral-600">
              {{ g.label }}
            </div>
            <div class="space-y-0.5">
              <router-link
                v-for="item in g.items"
                :key="item.label"
                :to="item.to"
                :class="linkClass(item)"
              >
                <Icon :name="item.icon" />
                {{ item.label }}
              </router-link>
            </div>
          </div>
        </nav>
      </div>
    </div>

    <div class="flex min-h-0 flex-1">
      <aside class="sticky top-14 hidden h-[calc(100vh-3.5rem)] w-60 shrink-0 border-r border-neutral-200 bg-white md:flex dark:border-neutral-800 dark:bg-neutral-900">
        <nav class="w-full flex-1 space-y-3 overflow-y-auto px-2.5 pb-3">
          <div
            v-for="g in groups"
            :key="g.label"
          >
            <div class="px-3 pt-4 pb-1 text-[11px] font-semibold tracking-wider text-neutral-400 uppercase dark:text-neutral-600">
              {{ g.label }}
            </div>
            <div class="space-y-0.5">
              <router-link
                v-for="item in g.items"
                :key="item.label"
                :to="item.to"
                :class="linkClass(item)"
              >
                <Icon :name="item.icon" />
                {{ item.label }}
              </router-link>
            </div>
          </div>
        </nav>
      </aside>
      <main class="min-w-0 flex-1 px-4 py-5 sm:px-6 sm:py-6 lg:px-8">
        <div class="mx-auto max-w-6xl">
          <p
            v-if="session.error"
            class="mb-4 rounded-lg bg-red-50 px-3 py-2 text-sm text-red-700 dark:bg-red-950/60 dark:text-red-300"
          >
            {{ session.error }}
          </p>
          <router-view :key="viewKey" />
        </div>
      </main>
    </div>

    <ChangePassword
      v-if="changingPassword"
      :username="session.user.username"
      @close="changingPassword = false"
    />
  </div>
</template>
