<script>
import Loading from '../shims/Loading.vue';
import Pill from '../components/Pill.vue';
import ConfirmButton from '../components/ConfirmButton.vue';
import ApplyYaml from '@pkg/components/ApplyYaml.vue';
import { HYVE } from '@pkg/config/types';
import { createRoute, detailRoute } from '../util';

export default {
  name:       'HyveSharedResourceList',
  components: {
    Loading, Pill, ConfirmButton, ApplyYaml
  },

  async fetch() {
    await this.load();
  },

  data() {
    return { rows: [], error: '', applying: false };
  },

  methods: {
    async load() {
      this.rows = (await this.$store.dispatch('management/findAll', { type: HYVE.RESOURCE, opt: { force: true } }))
        .sort((a, b) => a.id.localeCompare(b.id));
    },
    async remove(r) {
      this.error = '';
      try {
        await r.remove();
        await this.load();
      } catch (e) {
        this.error = e.message;
      }
    },
    open(r) {
      this.$router.push(detailRoute(HYVE.RESOURCE, r));
    },
    create() {
      return createRoute(HYVE.RESOURCE);
    },
  },
};
</script>

<template>
  <div>
    <div class="header">
      <h1 class="mb-0">
        Shared resources
      </h1>
      <div class="header-right">
        <button
          type="button"
          class="btn role-secondary"
          @click="applying = true"
        >
          Apply YAML
        </button>
        <router-link
          class="btn role-primary"
          :to="create()"
        >
          New shared resource
        </router-link>
      </div>
    </div>
    <ApplyYaml
      v-if="applying"
      @close="applying = false"
      @applied="load"
    />
    <p class="mb-4 text-sm text-neutral-500">
      Fleet specs that templates and clusters refer to by name: a Helm chart, git paths or manifests. Also <code>hyve resource create</code>.
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
        v-if="!rows.length"
        class="p-6 text-center text-sm text-neutral-500"
      >
        No shared resources yet.
      </p>
      <div class="divide-y divide-neutral-100 dark:divide-neutral-800">
        <div
          v-for="r in rows"
          :key="r.id"
          class="flex cursor-pointer items-center justify-between gap-3 px-4 py-3 transition-colors hover:bg-neutral-50 dark:hover:bg-neutral-800/50"
          @click="open(r)"
        >
          <div class="min-w-0">
            <div class="font-medium text-neutral-900 dark:text-neutral-100">
              {{ r.metadata.name }}
            </div>
            <div class="truncate text-xs text-neutral-500">
              {{ r.sourceDisplay }}<template v-if="r.secretsDisplay">
                · secrets {{ r.secretsDisplay }}
              </template>
            </div>
          </div>
          <div class="flex shrink-0 items-center gap-3">
            <span class="text-xs text-neutral-500">{{ r.metadata.namespace }}</span>
            <Pill
              tone="info"
              :label="r.fleetKind || '?'"
            />
            <ConfirmButton
              label="Delete"
              :confirm="`Delete ${ r.metadata.name }?`"
              @confirm="remove(r)"
            />
          </div>
        </div>
      </div>
    </div>
  </div>
</template>
