<script>
import Loading from '../shims/Loading.vue';
import Modal from '../components/Modal.vue';
import Pill from '../components/Pill.vue';
import ConfirmButton from '../components/ConfirmButton.vue';
import { auth } from '../api';
import { session } from '../session';

const USER = 'hyve.io.user';
const NAME_RE = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/;

function emptyForm() {
  return {
    name: '', displayName: '', groups: '', password: '', mustChange: true
  };
}

// hyve's Users, as `hyve user` manages them: the User object through the
// API, its password through the host.
export default {
  name:       'HyveUsers',
  components: {
    Loading, Modal, Pill, ConfirmButton
  },

  async fetch() {
    await this.load();
  },

  data() {
    return {
      users: [], adding: false, form: emptyForm(), error: '', formError: '', busy: false, passwordFor: '', newPassword: '', session
    };
  },

  methods: {
    async load() {
      this.users = (await this.$store.dispatch('management/findAll', { type: USER, opt: { force: true } }))
        .sort((a, b) => a.metadata.name.localeCompare(b.metadata.name));
    },
    async act(fn, onError = (m) => {
      this.error = m;
    }) {
      this.error = '';
      this.formError = '';
      this.busy = true;
      try {
        await fn();
        await this.load();

        return true;
      } catch (e) {
        onError(e.message);

        return false;
      } finally {
        this.busy = false;
      }
    },
    async add() {
      const f = this.form;

      if (!NAME_RE.test(f.name) || f.name.length > 63) {
        this.formError = "A username is a DNS label: lowercase letters, digits and '-', at most 63";

        return;
      }
      const ok = await this.act(async() => {
        const groups = f.groups.split(',').map((g) => g.trim()).filter(Boolean);
        const u = await this.$store.dispatch('management/create', {
          type: USER, metadata: { name: f.name }, spec: { ...(f.displayName ? { displayName: f.displayName } : {}), ...(groups.length ? { groups } : {}) }
        });

        await u.save();
        try {
          await auth.password(f.name, '', f.password);
        } catch (e) {
          await u.remove().catch(() => {});
          throw new Error(`${ e.message } (the user was removed again)`);
        }
        // Setting a password clears mustChangePassword, so set it after.
        if (f.mustChange) {
          const fresh = await this.$store.dispatch('management/find', { type: USER, id: f.name });

          fresh.spec = { ...fresh.spec, mustChangePassword: true };
          await fresh.save();
        }
      }, (m) => {
        this.formError = m;
      });

      if (ok) {
        this.adding = false;
        this.form = emptyForm();
      }
    },
    setDisabled(u, disabled) {
      return this.act(async() => {
        u.spec = { ...u.spec, disabled };
        await u.save();
      });
    },
    async setPassword(u) {
      const ok = await this.act(() => auth.password(u.metadata.name, '', this.newPassword), (m) => {
        this.formError = m;
      });

      if (ok) {
        this.passwordFor = '';
        this.newPassword = '';
      }
    },
    remove(u) {
      return this.act(() => u.remove());
    },
    lastLogin(u) {
      return u.status?.lastLogin ? `last signed in ${ new Date(u.status.lastLogin).toLocaleString() }` : 'never signed in';
    },
  },
};
</script>

<template>
  <div>
    <div class="mb-4 flex items-center justify-between">
      <h1 class="mb-0">
        Users
      </h1>
      <button
        type="button"
        class="btn role-primary"
        @click="adding = true; formError = ''"
      >
        New user
      </button>
    </div>
    <p class="mb-4 text-sm text-neutral-500">
      Kubernetes sees each user as <code>hyve:&lt;username&gt;</code>, in groups <code>hyve:&lt;group&gt;</code>; grant access with <code>hyve access grant</code> or RBAC on those names.
    </p>
    <p
      v-if="error"
      class="mb-4 text-sm text-red-600 dark:text-red-400"
    >
      {{ error }}
    </p>
    <Loading v-if="$fetchState.pending" />
    <div
      v-else
      class="overflow-hidden rounded-xl border border-neutral-200 bg-white shadow-sm dark:border-neutral-800 dark:bg-neutral-900"
    >
      <p
        v-if="!users.length"
        class="p-6 text-center text-sm text-neutral-500"
      >
        No users yet.
      </p>
      <div class="divide-y divide-neutral-100 dark:divide-neutral-800">
        <div
          v-for="u in users"
          :key="u.metadata.name"
          class="flex items-center justify-between gap-3 px-4 py-3"
        >
          <div class="min-w-0 flex-1">
            <div class="flex flex-wrap items-center gap-2">
              <span class="font-medium text-neutral-900 dark:text-neutral-100">{{ u.metadata.name }}</span>
              <span
                v-if="u.metadata.name === session.user.username"
                class="text-xs text-neutral-400"
              >(you)</span>
              <span
                v-for="g in u.spec?.groups || []"
                :key="g"
                class="rounded-full bg-neutral-100 px-2 py-0.5 text-xs font-medium text-neutral-600 dark:bg-neutral-800 dark:text-neutral-400"
              >{{ g }}</span>
              <Pill
                v-if="u.spec?.disabled"
                tone="bad"
                label="Disabled"
              />
              <Pill
                v-if="u.spec?.mustChangePassword"
                tone="busy"
                label="Must change password"
              />
            </div>
            <div class="mt-0.5 truncate text-xs text-neutral-500">
              {{ u.spec?.displayName ? `${ u.spec.displayName } · ` : '' }}{{ lastLogin(u) }}
            </div>
          </div>
          <div
            v-if="u.metadata.name !== session.user.username"
            class="flex shrink-0 items-center gap-1"
          >
            <button
              type="button"
              class="btn role-tertiary btn-sm"
              @click="passwordFor = u.metadata.name; newPassword = ''; formError = ''"
            >
              Set password
            </button>
            <button
              type="button"
              class="btn role-tertiary btn-sm"
              :disabled="busy"
              @click="setDisabled(u, !u.spec?.disabled)"
            >
              {{ u.spec?.disabled ? 'Enable' : 'Disable' }}
            </button>
            <ConfirmButton
              label="Delete"
              :confirm="`Delete ${ u.metadata.name }?`"
              @confirm="remove(u)"
            />
          </div>
        </div>
      </div>
    </div>

    <Modal
      v-if="adding"
      title="New user"
      @close="adding = false"
    >
      <form @submit.prevent="add">
        <div class="mb-3 grid gap-3 sm:grid-cols-2">
          <label class="block text-sm">
            <span class="mb-1 block text-neutral-600 dark:text-neutral-400">Username</span>
            <input
              v-model="form.name"
              autofocus
            >
          </label>
          <label class="block text-sm">
            <span class="mb-1 block text-neutral-600 dark:text-neutral-400">Display name</span>
            <input v-model="form.displayName">
          </label>
        </div>
        <label class="mb-3 block text-sm">
          <span class="mb-1 block text-neutral-600 dark:text-neutral-400">Groups</span>
          <input
            v-model="form.groups"
            placeholder="comma-separated"
          >
        </label>
        <label class="mb-3 block text-sm">
          <span class="mb-1 block text-neutral-600 dark:text-neutral-400">Initial password</span>
          <input
            v-model="form.password"
            type="password"
            autocomplete="new-password"
          >
        </label>
        <label class="checkbox mb-3">
          <input
            v-model="form.mustChange"
            type="checkbox"
          >
          Must change it at first sign-in
        </label>
        <p
          v-if="formError"
          class="mb-3 text-sm text-red-600 dark:text-red-400"
        >
          {{ formError }}
        </p>
        <div class="flex justify-end gap-2">
          <button
            type="button"
            class="btn role-tertiary"
            @click="adding = false"
          >
            Cancel
          </button>
          <button
            type="submit"
            class="btn role-primary"
            :disabled="busy || !form.name || !form.password"
          >
            {{ busy ? 'Creating…' : 'Create' }}
          </button>
        </div>
      </form>
    </Modal>

    <Modal
      v-if="passwordFor"
      :title="`Set ${ passwordFor }'s password`"
      @close="passwordFor = ''"
    >
      <form @submit.prevent="setPassword(users.find((u) => u.metadata.name === passwordFor))">
        <label class="mb-3 block text-sm">
          <span class="mb-1 block text-neutral-600 dark:text-neutral-400">New password</span>
          <input
            v-model="newPassword"
            type="password"
            autocomplete="new-password"
            autofocus
          >
          <span class="mt-1 block text-xs text-neutral-500">Signs them out everywhere.</span>
        </label>
        <p
          v-if="formError"
          class="mb-3 text-sm text-red-600 dark:text-red-400"
        >
          {{ formError }}
        </p>
        <div class="flex justify-end gap-2">
          <button
            type="button"
            class="btn role-tertiary"
            @click="passwordFor = ''"
          >
            Cancel
          </button>
          <button
            type="submit"
            class="btn role-primary"
            :disabled="busy || !newPassword"
          >
            Set password
          </button>
        </div>
      </form>
    </Modal>
  </div>
</template>
