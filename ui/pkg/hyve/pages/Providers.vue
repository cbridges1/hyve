<script>
import Loading from '@shell/components/Loading';
import { Banner } from '@components/Banner';
import { BLANK_CLUSTER } from '@shell/store/store-types.js';
import { PRODUCT } from '../config/types';
import ViewDialog from '../components/ViewDialog.vue';

const DEPLOYMENT = 'apps.deployment';
const TURTLES_PROVIDER = 'turtles-capi.cattle.io.capiprovider';
const PROVIDER_LABEL = 'cluster.x-k8s.io/provider';
// The CAPI Operator's provider kinds.
const OPERATOR_TYPES = ['core', 'bootstrap', 'controlplane', 'infrastructure', 'addon', 'ipam', 'runtimeextension'].map((k) => `operator.cluster.x-k8s.io.${ k }provider`);
const TYPES = ['infrastructure', 'bootstrap', 'control-plane', 'addon', 'ipam', 'runtime-extension'];

// The installed Cluster API providers and their health, however they were
// installed (Turtles, the CAPI Operator, or local mode's lean install):
// every provider's controller is a Deployment labelled with its name.
export default {
  name:       'HyveProviders',
  components: { Loading, Banner, ViewDialog },

  async fetch() {
    const deployments = await this.$store.dispatch('management/findAll', { type: DEPLOYMENT }).catch(() => []);

    this.rows = deployments
      .filter((d) => d.metadata?.labels?.[PROVIDER_LABEL])
      .map((d) => {
        const full = d.metadata.labels[PROVIDER_LABEL];
        let type = full === 'fleet' ? 'addon' : 'core';
        let name = full;

        TYPES.forEach((t) => {
          if (full.startsWith(`${ t }-`)) {
            type = t;
            name = full.slice(t.length + 1);
          }
        });
        const image = d.spec?.template?.spec?.containers?.[0]?.image || '';
        const i = image.lastIndexOf(':');
        const want = d.spec?.replicas ?? 1;
        const ready = d.status?.availableReplicas || 0;

        return {
          d,
          id:      d.id,
          name,
          type,
          ns:      d.metadata.namespace,
          version: i > image.lastIndexOf('/') ? image.slice(i + 1) : '',
          healthy: ready >= want && want > 0,
          state:   `${ ready }/${ want }`,
        };
      })
      .sort((a, b) => `${ a.type }${ a.name }`.localeCompare(`${ b.type }${ b.name }`));
    this.turtles = !!this.$store.getters['management/schemaFor'](TURTLES_PROVIDER);
  },

  data() {
    return {
      rows: [], turtles: false, viewing: null
    };
  },

  methods: {
    // A provider's controller, and the object it was installed from
    // (Turtles' or the CAPI Operator's) when there is one.
    async view(r) {
      const sections = [];

      for (const type of [TURTLES_PROVIDER, ...OPERATOR_TYPES]) {
        if (!this.$store.getters['management/schemaFor'](type)) {
          continue;
        }
        const all = await this.$store.dispatch('management/findAll', { type }).catch(() => []);

        all.filter((p) => p.metadata.namespace === r.ns).forEach((p) => sections.push({ label: p.kind || type, object: p }));
      }
      sections.push({ label: 'Deployment', object: r.d });
      this.viewing = { title: `${ r.type } ${ r.name }`, sections };
    },
  },

  computed: {
    addRoute() {
      return {
        name:   `${ PRODUCT }-c-cluster-resource-create`,
        params: {
          product: PRODUCT, cluster: BLANK_CLUSTER, resource: TURTLES_PROVIDER
        },
      };
    },
  },
};
</script>

<template>
  <Loading v-if="$fetchState.pending" />
  <div v-else>
    <div class="header">
      <h1>{{ t('hyve.providers.title') }}</h1>
      <router-link
        v-if="turtles"
        :to="addRoute"
        class="btn role-primary"
      >
        {{ t('hyve.providers.add') }}
      </router-link>
    </div>
    <Banner
      v-if="!turtles"
      color="info"
      :label="t('hyve.providers.cliHint')"
    />
    <table class="sortable-table hyve-table">
      <thead>
        <tr>
          <th>{{ t('tableHeaders.name') }}</th>
          <th>{{ t('hyve.providers.type') }}</th>
          <th>{{ t('tableHeaders.namespace') }}</th>
          <th>{{ t('hyve.resources.version') }}</th>
          <th>{{ t('hyve.providers.health') }}</th>
          <th />
        </tr>
      </thead>
      <tbody>
        <tr v-if="!rows.length">
          <td colspan="6">
            {{ t('hyve.providers.none') }}
          </td>
        </tr>
        <tr
          v-for="r in rows"
          :key="r.id"
        >
          <td>{{ r.name }}</td>
          <td>{{ r.type }}</td>
          <td>{{ r.ns }}</td>
          <td>{{ r.version }}</td>
          <td :class="r.healthy ? 'text-success' : 'text-warning'">
            {{ r.healthy ? t('hyve.providers.ready') : t('hyve.providers.notReady', { state: r.state }) }}
          </td>
          <td class="text-right">
            <button
              type="button"
              class="btn btn-sm role-link"
              @click="view(r)"
            >
              {{ t('hyve.view.view') }}
            </button>
          </td>
        </tr>
      </tbody>
    </table>
    <ViewDialog
      v-if="viewing"
      :title="viewing.title"
      :sections="viewing.sections"
      @close="viewing = null"
    />
  </div>
</template>

<style lang="scss" scoped>
.header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 20px;
}
.hyve-table {
  width: 100%;
  th, td { padding: 8px; text-align: left; }
}
</style>
