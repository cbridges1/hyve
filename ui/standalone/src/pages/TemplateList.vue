<script>
import Loading from '../shims/Loading.vue';
import Pill from '../components/Pill.vue';
import ConfirmButton from '../components/ConfirmButton.vue';
import { HYVE } from '@pkg/config/types';
import { createRoute, detailRoute } from '../util';

export default {
  name:       'HyveTemplateList',
  components: { Loading, Pill, ConfirmButton },

  async fetch() {
    await this.load();
  },

  data() {
    return { templates: [], error: '' };
  },

  methods: {
    async load() {
      this.templates = (await this.$store.dispatch('management/findAll', { type: HYVE.TEMPLATE, opt: { force: true } }))
        .sort((a, b) => a.id.localeCompare(b.id));
    },
    async remove(tpl) {
      this.error = '';
      try {
        await tpl.remove();
        await this.load();
      } catch (e) {
        this.error = e.message;
      }
    },
    sub(tpl) {
      const s = tpl.spec || {};

      return [s.clusterClassRef?.name && `class ${ s.clusterClassRef.name }`, s.kubernetesVersion, s.schedule && `expires ${ s.schedule }`, `${ (s.resources || []).length } resources`].filter(Boolean).join(' · ');
    },
    open(tpl) {
      this.$router.push(detailRoute(HYVE.TEMPLATE, tpl));
    },
    create() {
      return createRoute(HYVE.TEMPLATE);
    },
  },
};
</script>

<template>
  <div>
    <div class="header">
      <h1 class="mb-0">
        Templates
      </h1>
      <router-link
        class="btn role-primary"
        :to="create()"
      >
        New template
      </router-link>
    </div>
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
        v-if="!templates.length"
        class="p-6 text-center text-sm text-neutral-500"
      >
        No templates yet.
      </p>
      <div class="divide-y divide-neutral-100 dark:divide-neutral-800">
        <div
          v-for="tpl in templates"
          :key="tpl.id"
          class="flex cursor-pointer flex-col gap-2 px-4 py-3 transition-colors hover:bg-neutral-50 sm:flex-row sm:items-center sm:justify-between dark:hover:bg-neutral-800/50"
          @click="open(tpl)"
        >
          <div class="min-w-0">
            <div class="font-medium text-neutral-900 dark:text-neutral-100">
              {{ tpl.metadata.name }}
            </div>
            <div class="truncate text-xs text-neutral-500">
              {{ tpl.spec.description || sub(tpl) }}
            </div>
          </div>
          <div class="flex shrink-0 items-center gap-3">
            <span class="text-xs text-neutral-500">{{ tpl.metadata.namespace }}</span>
            <Pill
              :tone="tpl.readyDisplay === 'Ready' ? 'good' : 'bad'"
              :label="tpl.readyDisplay === 'Ready' ? 'Ready' : 'Not ready'"
              :title="tpl.readyDisplay"
            />
            <button
              type="button"
              class="btn role-tertiary btn-sm"
              @click.stop="tpl.createCluster()"
            >
              New cluster
            </button>
            <ConfirmButton
              label="Delete"
              :confirm="`Delete ${ tpl.metadata.name }?`"
              @confirm="remove(tpl)"
            />
          </div>
        </div>
      </div>
    </div>
  </div>
</template>
