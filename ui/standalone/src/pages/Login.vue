<script>
import { auth } from '../api';
import { session } from '../session';
import Logo from '../components/Logo.vue';
import ThemeToggle from '../components/ThemeToggle.vue';

const inputClass = 'w-full rounded-lg border border-neutral-300 bg-white px-3 py-2 text-sm text-neutral-900 outline-none transition-colors placeholder:text-neutral-400 focus:border-neutral-500 focus:ring-2 focus:ring-neutral-900/10 dark:border-neutral-700 dark:bg-neutral-800 dark:text-neutral-100 dark:focus:border-neutral-500 dark:focus:ring-white/10';

// Username and password against the host's Users, as `hyve login` does.
// A User that must change its password does it here before signing in.
export default {
  name:       'HyveLogin',
  components: { Logo, ThemeToggle },

  data() {
    return {
      username: '', password: '', newPassword: '', confirm: '', mustChange: false, error: '', busy: false, inputClass
    };
  },

  methods: {
    async submit() {
      this.error = '';
      if (this.mustChange && this.newPassword !== this.confirm) {
        this.error = "The new passwords don't match";

        return;
      }
      this.busy = true;
      try {
        await auth.login(this.username, this.password, this.mustChange ? this.newPassword : '');
        const me = await auth.me();

        session.user = me;
        session.version = me.version;
        await this.$store.load();
        this.$router.replace(this.$route.query.redirect || '/');
      } catch (e) {
        if (e.body?.code === 'MustChangePassword') {
          this.mustChange = true;
        } else {
          this.error = e.status === 401 ? 'Invalid username or password' : e.message;
        }
      } finally {
        this.busy = false;
      }
    },
  },
};
</script>

<template>
  <div class="relative flex min-h-screen items-center justify-center bg-neutral-50 px-4 dark:bg-neutral-950">
    <div class="absolute top-4 right-4">
      <ThemeToggle />
    </div>
    <form
      class="w-full max-w-sm rounded-2xl border border-neutral-200 bg-white p-7 shadow-sm dark:border-neutral-800 dark:bg-neutral-900"
      @submit.prevent="submit"
    >
      <div class="mb-7 flex justify-center">
        <Logo size="h-6" />
      </div>
      <label class="mb-3.5 block text-sm">
        <span class="mb-1.5 block font-medium text-neutral-600 dark:text-neutral-400">Username</span>
        <input
          v-model="username"
          type="text"
          required
          autofocus
          autocomplete="username"
          :disabled="mustChange"
          :class="inputClass"
        >
      </label>
      <label class="mb-3.5 block text-sm">
        <span class="mb-1.5 block font-medium text-neutral-600 dark:text-neutral-400">Password</span>
        <input
          v-model="password"
          type="password"
          required
          autocomplete="current-password"
          :disabled="mustChange"
          :class="inputClass"
        >
      </label>
      <template v-if="mustChange">
        <p class="mb-3.5 rounded-lg bg-sky-50 px-3 py-2 text-sm text-sky-800 dark:bg-sky-950/60 dark:text-sky-300">
          Choose a new password to finish signing in.
        </p>
        <label class="mb-3.5 block text-sm">
          <span class="mb-1.5 block font-medium text-neutral-600 dark:text-neutral-400">New password</span>
          <input
            v-model="newPassword"
            type="password"
            required
            autocomplete="new-password"
            :class="inputClass"
          >
        </label>
        <label class="mb-3.5 block text-sm">
          <span class="mb-1.5 block font-medium text-neutral-600 dark:text-neutral-400">New password again</span>
          <input
            v-model="confirm"
            type="password"
            required
            autocomplete="new-password"
            :class="inputClass"
          >
        </label>
      </template>
      <p
        v-if="error"
        class="mb-5 rounded-lg bg-red-50 px-3 py-2 text-sm text-red-700 dark:bg-red-950/60 dark:text-red-300"
      >
        {{ error }}
      </p>
      <button
        type="submit"
        :disabled="busy"
        class="w-full rounded-lg bg-neutral-900 px-3 py-2.5 text-sm font-medium text-white transition-colors hover:bg-neutral-800 disabled:opacity-50 dark:bg-white dark:text-neutral-900 dark:hover:bg-neutral-200"
      >
        {{ busy ? 'Signing in…' : 'Sign in' }}
      </button>
      <p class="mt-5 text-center text-xs text-neutral-500">
        Accounts are made by an admin. From the CLI, <code>hyve ui</code> opens this signed in.
      </p>
    </form>
  </div>
</template>
