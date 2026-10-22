<script>
import Loading from '../shims/Loading.vue';
import Pill from '../components/Pill.vue';
import EnvironmentPicker from '@pkg/components/EnvironmentPicker.vue';
import {
  ANNOTATIONS, CAPI_CLUSTER, HYVE, LABELS, PRODUCT
} from '@pkg/config/types';
import { detailRoute } from '../util';
import { clusterStatus, expiry } from '../status';

// The console's cluster list: one row per cluster, its environment,
// expiry and state as pills; a row opens the cluster.
export default {
  name:       'HyveClusterList',
  live:       true,
  components: { Loading, Pill, EnvironmentPicker },

  async fetch() {
    this.clusters = (await this.$store.dispatch('management/findAll', { type: CAPI_CLUSTER, opt: { force: true } }))
      .sort((a, b) => a.id.localeCompare(b.id));
  },

  data() {
    return { environment: '', clusters: [] };
  },

  computed: {
    rows() {
      return this.clusters
        .filter((c) => !this.environment || c.metadata.namespace === this.environment)
        .map((c) => {
          const l = c.metadata.labels || {};
          const topo = c.spec?.topology || {};
          const workers = [...(topo.workers?.machineDeployments || []), ...(topo.workers?.machinePools || [])].reduce((n, w) => n + (w.replicas || 0), 0);

          return {
            c,
            sub:     [l[LABELS.TEMPLATE] ? `template ${ l[LABELS.TEMPLATE] }` : '', topo.classRef?.name || topo.class, topo.version, `${ workers } worker${ workers === 1 ? '' : 's' }`].filter(Boolean).join(' · '),
            status:  clusterStatus(c),
            expires: expiry(c.metadata.annotations?.[ANNOTATIONS.EXPIRES_AT]),
          };
        });
    },
    createRoute() {
      return { name: `${ PRODUCT }-c-cluster-${ HYVE.CREATE_CLUSTER }`, params: { cluster: '_' } };
    },
  },

  methods: {
    open(c) {
      this.$router.push(detailRoute(CAPI_CLUSTER, c));
    },
  },
};
</script>

<template>
  <div>
    <div class="header">
      <h1 class="mb-0">
        Clusters
      </h1>
      <div class="header-right">
        <EnvironmentPicker v-model:value="environment" />
        <router-link
          class="btn role-primary"
          :to="createRoute"
        >
          New cluster
        </router-link>
      </div>
    </div>
    <Loading v-if="$fetchState.pending" />
    <div
      v-else
      class="overflow-hidden rounded-xl border border-neutral-200 bg-white shadow-sm dark:border-neutral-800 dark:bg-neutral-900"
    >
      <p
        v-if="!rows.length"
        class="p-6 text-center text-sm text-neutral-500"
      >
        No clusters yet.
      </p>
      <div class="divide-y divide-neutral-100 dark:divide-neutral-800">
        <div
          v-for="r in rows"
          :key="r.c.id"
          class="flex cursor-pointer flex-col gap-2 px-4 py-3 transition-colors hover:bg-neutral-50 sm:flex-row sm:items-center sm:justify-between dark:hover:bg-neutral-800/50"
          @click="open(r.c)"
        >
          <div class="min-w-0">
            <div class="font-medium text-neutral-900 dark:text-neutral-100">
              {{ r.c.metadata.name }}
            </div>
            <div class="truncate text-xs text-neutral-500">
              {{ r.sub }}
            </div>
          </div>
          <div class="flex shrink-0 items-center gap-3">
            <Pill
              tone="env"
              :label="r.c.metadata.namespace"
            />
            <Pill
              v-if="r.expires"
              v-bind="r.expires"
            />
            <Pill v-bind="r.status" />
          </div>
        </div>
      </div>
    </div>
  </div>
</template>
