<script>
import Loading from '../shims/Loading.vue';
import Modal from '../components/Modal.vue';
import Pill from '../components/Pill.vue';
import ConfirmButton from '../components/ConfirmButton.vue';
import { CAPI_CLUSTER, LABELS, NAMESPACE } from '@pkg/config/types';

const NAME_RE = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/;

// Environments are namespaces labelled hyve.io/environment, as
// `hyve env create` makes them; clusters live in them.
export default {
  name:       'HyveEnvironments',
  components: {
    Loading, Modal, Pill, ConfirmButton
  },

  async fetch() {
    await this.load();
  },

  data() {
    return {
      envs: [], counts: {}, creating: false, name: '', error: '', busy: false
    };
  },

  methods: {
    async load() {
      const [nss, clusters] = await Promise.all([
        this.$store.dispatch('management/findAll', { type: NAMESPACE, opt: { force: true } }),
        this.$store.dispatch('management/findAll', { type: CAPI_CLUSTER, opt: { force: true } }).catch(() => []),
      ]);

      this.envs = nss.filter((n) => n.metadata?.labels?.[LABELS.ENVIRONMENT] !== undefined).sort((a, b) => a.metadata.name.localeCompare(b.metadata.name));
      const counts = {};

      clusters.forEach((c) => {
        counts[c.metadata.namespace] = (counts[c.metadata.namespace] || 0) + 1;
      });
      this.counts = counts;
    },
    async create() {
      this.error = '';
      if (!NAME_RE.test(this.name) || this.name.length > 63) {
        this.error = 'Lowercase letters, digits and dashes';

        return;
      }
      this.busy = true;
      try {
        const ns = await this.$store.dispatch('management/create', {
          type: NAMESPACE, metadata: { name: this.name, labels: { [LABELS.ENVIRONMENT]: 'true', [LABELS.MANAGED_BY]: 'hyve' } }
        });

        await ns.save();
        this.creating = false;
        this.name = '';
        await this.load();
      } catch (e) {
        this.error = e.message;
      } finally {
        this.busy = false;
      }
    },
    async remove(env) {
      this.error = '';
      try {
        await env.remove();
        await this.load();
      } catch (e) {
        this.error = e.message;
      }
    },
  },
};
</script>

<template>
  <Loading v-if="$fetchState.pending" />
  <div v-else>
    <div class="mb-4 flex items-center justify-between">
      <h1 class="mb-0">
        Environments
      </h1>
      <button
        type="button"
        class="btn role-primary"
        @click="creating = true"
      >
        New environment
      </button>
    </div>
    <p
      v-if="error && !creating"
      class="mb-4 text-sm text-red-600 dark:text-red-400"
    >
      {{ error }}
    </p>
    <div class="overflow-hidden rounded-xl border border-neutral-200 bg-white shadow-sm dark:border-neutral-800 dark:bg-neutral-900">
      <p
        v-if="!envs.length"
        class="p-6 text-center text-sm text-neutral-500"
      >
        No environments yet.
      </p>
      <div class="divide-y divide-neutral-100 dark:divide-neutral-800">
        <div
          v-for="env in envs"
          :key="env.metadata.name"
          class="flex items-center justify-between gap-3 px-4 py-3"
        >
          <div class="flex items-center gap-2">
            <span class="font-medium text-neutral-900 dark:text-neutral-100">{{ env.metadata.name }}</span>
            <Pill
              :label="`${ counts[env.metadata.name] || 0 } clusters`"
            />
          </div>
          <ConfirmButton
            v-if="!counts[env.metadata.name]"
            label="Delete"
            :confirm="`Delete ${ env.metadata.name }?`"
            @confirm="remove(env)"
          />
          <span
            v-else
            class="text-xs text-neutral-400"
          >Delete its clusters first</span>
        </div>
      </div>
    </div>
    <Modal
      v-if="creating"
      title="New environment"
      @close="creating = false"
    >
      <form @submit.prevent="create">
        <label class="mb-3 block text-sm">
          <span class="mb-1 block text-neutral-600 dark:text-neutral-400">Name</span>
          <input
            v-model="name"
            autofocus
            placeholder="dev"
          >
          <span class="mt-1 block text-xs text-neutral-500">A namespace; clusters created in it are this environment's.</span>
        </label>
        <p
          v-if="error"
          class="mb-3 text-sm text-red-600 dark:text-red-400"
        >
          {{ error }}
        </p>
        <div class="flex justify-end gap-2">
          <button
            type="button"
            class="btn role-tertiary"
            @click="creating = false"
          >
            Cancel
          </button>
          <button
            type="submit"
            class="btn role-primary"
            :disabled="!name || busy"
          >
            {{ busy ? 'Creating…' : 'Create' }}
          </button>
        </div>
      </form>
    </Modal>
  </div>
</template>
