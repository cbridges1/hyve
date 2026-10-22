<script>
import Loading from '@shell/components/Loading';
import { Banner } from '@components/Banner';
import ViewDialog from '../components/ViewDialog.vue';
import ProviderAdd from '../components/ProviderAdd.vue';
import { onHyveHost } from '../utils/host';

const DEPLOYMENT = 'apps.deployment';
const TURTLES_PROVIDER = 'turtles-capi.cattle.io.capiprovider';
const PROVIDER_LABEL = 'cluster.x-k8s.io/provider';
// The CAPI Operator's provider kinds.
const OPERATOR_TYPES = ['core', 'bootstrap', 'controlplane', 'infrastructure', 'addon', 'ipam', 'runtimeextension'].map((k) => `operator.cluster.x-k8s.io.${ k }provider`);
const TYPES = ['infrastructure', 'bootstrap', 'control-plane', 'addon', 'ipam', 'runtime-extension'];

// The installed Cluster API providers and their health, however they were
// installed (Turtles, the CAPI Operator, or local mode's lean install):
// every provider's controller is a Deployment labelled with its name.
// Adding and removing do what 'hyve provider add/remove' do.
export default {
  name:       'HyveProviders',
  live:       true,
  components: {
    Loading, Banner, ViewDialog, ProviderAdd
  },

  async fetch() {
    await this.load();
  },

  data() {
    return {
      rows: [], turtles: false, viewing: null, adding: false, message: '', errors: [], confirming: '', objects: []
    };
  },

  methods: {
    async load() {
      const deployments = await this.$store.dispatch('management/findAll', { type: DEPLOYMENT, opt: { force: true } }).catch(() => []);

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
      this.objects = await this.providerObjects();
    },
    // Turtles' and the CAPI Operator's provider objects: what removing a
    // provider deletes.
    async providerObjects() {
      const out = [];

      for (const type of [TURTLES_PROVIDER, ...OPERATOR_TYPES]) {
        if (this.$store.getters['management/schemaFor'](type)) {
          out.push(...await this.$store.dispatch('management/findAll', { type, opt: { force: true } }).catch(() => []));
        }
      }

      return out;
    },
    objectFor(r) {
      return this.objects.find((p) => p.metadata.namespace === r.ns && p.metadata.name === r.name) ||
        this.objects.find((p) => p.metadata.namespace === r.ns && (p.spec?.name || p.metadata.name) === r.name);
    },
    async added(msg) {
      this.adding = false;
      this.message = this.t('hyve.providers.added', { message: msg });
      await this.load();
    },
    async remove(r) {
      this.errors = [];
      this.confirming = '';
      try {
        await this.objectFor(r).remove();
        this.message = '';
        await this.load();
      } catch (e) {
        this.errors.push(e?.message || String(e));
      }
    },
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
    canAdd() {
      return this.turtles || onHyveHost(this.$store);
    },
  },
};
</script>

<template>
  <Loading v-if="$fetchState.pending" />
  <div v-else>
    <div class="header">
      <h1>{{ t('hyve.providers.title') }}</h1>
      <button
        v-if="canAdd && !adding"
        type="button"
        class="btn role-primary"
        @click="adding = true; message = ''"
      >
        {{ t('hyve.providers.add') }}
      </button>
    </div>
    <p class="text-muted mb-20">
      {{ t('hyve.providers.hint') }}
    </p>
    <Banner
      v-if="!canAdd"
      color="info"
      :label="t('hyve.providers.cliHint')"
    />
    <Banner
      v-if="message"
      color="success"
      :label="message"
    />
    <Banner
      v-for="(e, i) in errors"
      :key="i"
      color="error"
      :label="e"
    />
    <ProviderAdd
      v-if="adding"
      @close="adding = false"
      @added="added"
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
          <td class="text-right actions">
            <template v-if="confirming === r.id">
              <span class="small">{{ t('hyve.providers.confirmRemove', { name: r.name }) }}</span>
              <button
                type="button"
                class="btn btn-sm role-danger bg-error"
                @click="remove(r)"
              >
                {{ t('generic.remove') }}
              </button>
              <button
                type="button"
                class="btn btn-sm role-link"
                @click="confirming = ''"
              >
                {{ t('generic.cancel') }}
              </button>
            </template>
            <template v-else>
              <button
                type="button"
                class="btn btn-sm role-link"
                @click="view(r)"
              >
                {{ t('hyve.view.view') }}
              </button>
              <button
                v-if="objectFor(r)"
                type="button"
                class="btn btn-sm role-link"
                @click="confirming = r.id"
              >
                {{ t('generic.remove') }}
              </button>
            </template>
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
  margin-bottom: 10px;
}
.hyve-table {
  width: 100%;
  th, td { padding: 8px; text-align: left; }
  .actions { white-space: nowrap; }
  .small { font-size: 0.85em; margin-right: 6px; }
}
</style>
