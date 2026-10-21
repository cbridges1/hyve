<script>
import Loading from '@shell/components/Loading';
import { Banner } from '@components/Banner';
import {
  DEFAULT_TEMPLATE_NAMESPACE, HYVE, LABELS
} from '../config/types';
import EnvironmentPicker from '../components/EnvironmentPicker.vue';
import SecretView from '../components/SecretView.vue';
import SecretEdit from '../components/SecretEdit.vue';

// Secrets that aren't hyve's to show: service account tokens, Helm's
// release records, and Cluster API's own per-cluster Secrets.
function hidden(s) {
  const t = s._type || '';

  return t === 'kubernetes.io/service-account-token' || t.startsWith('helm.sh/') ||
    !!s.metadata?.labels?.['cluster.x-k8s.io/cluster-name'] || !!(s.metadata?.ownerReferences || []).length;
}

// The Secrets in the template and environment namespaces, and the ones
// each resource copies to clusters (whether they exist comes from
// hyve-controller's status, for namespaces the user can't list). They can
// be created, changed and deleted here, as the signed-in user: 'hyve
// secrets create/set/delete'.
export default {
  name:       'HyveSecrets',
  components: {
    Loading, Banner, EnvironmentPicker, SecretView, SecretEdit
  },

  async fetch() {
    await this.load();
  },

  data() {
    return {
      environment: '', rows: [], namespaces: [], templateNamespaces: [], viewing: null, editing: null, confirming: '', errors: []
    };
  },

  computed: {
    shown() {
      if (!this.environment) {
        return this.rows;
      }

      return this.rows
        .map((r) => ({ ...r, clusters: r.clusters.filter((c) => c.metadata.namespace === this.environment) }))
        .filter((r) => r.clusters.length || r.ns === this.environment || this.templateNamespaces.includes(r.ns));
    },
  },

  methods: {
    async load() {
      const opt = { force: true };
      const [templates, clusterResources, shared, clusters, nss] = await Promise.all([
        this.$store.dispatch('management/findAll', { type: HYVE.TEMPLATE, opt }).catch(() => []),
        this.$store.dispatch('management/findAll', { type: HYVE.CLUSTER_RESOURCE, opt }).catch(() => []),
        this.$store.dispatch('management/findAll', { type: HYVE.RESOURCE, opt }).catch(() => []),
        this.$store.dispatch('management/findAll', { type: 'cluster.x-k8s.io.cluster', opt }).catch(() => []),
        this.$store.dispatch('management/findAll', { type: 'namespace', opt }).catch(() => []),
      ]);
      const envs = nss.filter((n) => n.metadata?.labels?.[LABELS.ENVIRONMENT] === 'true').map((n) => n.metadata.name);
      const tplNS = [...new Set([DEFAULT_TEMPLATE_NAMESPACE, ...templates.map((t) => t.metadata.namespace), ...shared.map((r) => r.metadata.namespace)])];

      this.templateNamespaces = tplNS;
      this.namespaces = [...new Set([...tplNS, ...envs])].sort();
      // Per namespace, so no other Secret is read.
      const lists = await Promise.all(this.namespaces.map((ns) => this.$store.dispatch('management/findAll', { type: 'secret', opt: { namespaced: ns, force: true } }).catch(() => null)));
      const listed = new Set();
      const rows = {};
      const row = (ns, secret) => {
        const key = `${ ns }/${ secret }`;

        return rows[key] || (rows[key] = {
          key, ns, secret, neededBy: [], missing: false, exists: false, type: '', keys: [], clusters: new Map()
        });
      };

      lists.forEach((list, i) => {
        if (!list) {
          return;
        }
        listed.add(this.namespaces[i]);
        list.filter((s) => !hidden(s)).forEach((s) => {
          const r = row(s.metadata.namespace, s.metadata.name);

          r.exists = true;
          r.type = s._type || 'Opaque';
          r.keys = Object.keys(s.data || {}).sort();
        });
      });
      const add = (ns, secret, owner, resource, missing, reach) => {
        const r = row(ns, secret);

        r.neededBy.push(`${ owner } › ${ resource }`);
        // What the listing saw wins; else hyve-controller's word.
        if (!listed.has(ns)) {
          r.missing = r.missing || missing;
          r.exists = !r.missing;
        }
        reach.forEach((c) => r.clusters.set(c.id, c));
      };
      // A template entry's Secrets: its own, or its shared Resource's.
      const entrySecrets = (e, ns) => {
        if (e.ref) {
          const res = shared.find((x) => x.metadata.name === e.ref && x.metadata.namespace === (e.refNamespace || ns));

          return res?.spec?.secrets || [];
        }

        return e.secrets || [];
      };

      templates.forEach((t) => {
        const ns = t.metadata.namespace;
        const missing = t.status?.missingSecrets || [];
        const reach = clusters.filter((c) => c.metadata?.labels?.[LABELS.TEMPLATE] === t.metadata.name && (c.metadata.labels[LABELS.TEMPLATE_NAMESPACE] || c.metadata.namespace) === ns);

        (t.spec?.resources || []).forEach((e) => {
          entrySecrets(e, ns).forEach((s) => add(ns, s, t.metadata.name, e.name || e.ref, missing.includes(s), reach));
        });
      });
      clusterResources.forEach((cr) => {
        const ns = cr.metadata.namespace;
        const missing = cr.status?.missingSecrets || [];
        const reach = clusters.filter((c) => c.metadata.namespace === ns && c.metadata.name === cr.spec?.clusterName);
        const name = cr.metadata?.labels?.[LABELS.RESOURCE] || cr.metadata.name;

        entrySecrets(cr.spec || {}, ns).forEach((s) => add(ns, s, cr.spec?.clusterName || cr.metadata.name, name, missing.includes(s), reach));
      });
      this.rows = Object.values(rows)
        .map((r) => ({ ...r, missing: !r.exists, clusters: [...r.clusters.values()] }))
        .sort((a, b) => a.key.localeCompare(b.key));
    },
    async saved() {
      this.editing = null;
      await this.load();
    },
    async remove(r) {
      this.errors = [];
      this.confirming = '';
      try {
        const s = await this.$store.dispatch('management/find', { type: 'secret', id: r.key, opt: { force: true } });

        await s.remove();
        await this.load();
      } catch (e) {
        this.errors.push(e?.message || String(e));
      }
    },
  },
};
</script>

<template>
  <Loading v-if="$fetchState.pending" />
  <div v-else>
    <div class="header">
      <h1>{{ t('hyve.secrets.title') }}</h1>
      <div class="header-right">
        <EnvironmentPicker v-model:value="environment" />
        <button
          type="button"
          class="btn role-primary"
          @click="editing = { existing: false, namespace: environment || '', name: '' }"
        >
          {{ t('hyve.secrets.create') }}
        </button>
      </div>
    </div>
    <p class="text-muted mb-20">
      {{ t('hyve.secrets.hint') }}
    </p>
    <Banner
      v-for="(e, i) in errors"
      :key="i"
      color="error"
      :label="e"
    />
    <table class="sortable-table hyve-table">
      <thead>
        <tr>
          <th>{{ t('tableHeaders.name') }}</th>
          <th>{{ t('tableHeaders.namespace') }}</th>
          <th>{{ t('hyve.secrets.keys') }}</th>
          <th>{{ t('hyve.secrets.exists') }}</th>
          <th>{{ t('hyve.secrets.neededBy') }}</th>
          <th>{{ t('hyve.secrets.deliveredTo') }}</th>
          <th />
        </tr>
      </thead>
      <tbody>
        <tr v-if="!shown.length">
          <td colspan="7">
            {{ t('hyve.secrets.none') }}
          </td>
        </tr>
        <tr
          v-for="r in shown"
          :key="r.key"
        >
          <td>
            {{ r.secret }}
            <div
              v-if="r.type && r.type !== 'Opaque'"
              class="text-muted small"
            >
              {{ r.type }}
            </div>
          </td>
          <td>{{ r.ns }}</td>
          <td class="small">
            {{ r.keys.join(', ') || '-' }}
          </td>
          <td :class="r.missing ? 'text-error' : 'text-success'">
            {{ r.missing ? t('hyve.secrets.missing') : t('hyve.secrets.present') }}
          </td>
          <td>
            <span
              v-if="!r.neededBy.length"
              class="text-muted small"
            >{{ t('hyve.secrets.unused') }}</span>
            <div
              v-for="n in r.neededBy"
              :key="n"
            >
              {{ n }}
            </div>
          </td>
          <td>
            <span v-if="!r.clusters.length">-</span>
            <div
              v-for="c in r.clusters"
              :key="c.id"
            >
              {{ c.metadata.namespace }}/{{ c.metadata.name }}
            </div>
          </td>
          <td class="text-right hyve-actions">
            <template v-if="confirming === r.key">
              <span class="small">{{ t('hyve.secrets.confirmDelete', { name: r.secret }) }}</span>
              <button
                type="button"
                class="btn btn-sm role-danger bg-error"
                @click="remove(r)"
              >
                {{ t('generic.delete') }}
              </button>
              <button
                type="button"
                class="btn btn-sm role-link"
                @click="confirming = ''"
              >
                {{ t('generic.cancel') }}
              </button>
            </template>
            <template v-else-if="r.missing">
              <button
                type="button"
                class="btn btn-sm role-secondary"
                @click="editing = { existing: false, namespace: r.ns, name: r.secret }"
              >
                {{ t('generic.create') }}
              </button>
            </template>
            <template v-else>
              <button
                type="button"
                class="btn btn-sm role-link"
                @click="viewing = r"
              >
                {{ t('hyve.view.view') }}
              </button>
              <button
                type="button"
                class="btn btn-sm role-link"
                @click="editing = { existing: true, namespace: r.ns, name: r.secret }"
              >
                {{ t('generic.edit') }}
              </button>
              <button
                type="button"
                class="btn btn-sm role-link"
                @click="confirming = r.key"
              >
                {{ t('generic.delete') }}
              </button>
            </template>
          </td>
        </tr>
      </tbody>
    </table>
    <SecretView
      v-if="viewing"
      :namespace="viewing.ns"
      :name="viewing.secret"
      @close="viewing = null"
    />
    <SecretEdit
      v-if="editing"
      :existing="editing.existing"
      :namespace="editing.namespace"
      :name="editing.name"
      :namespaces="namespaces"
      @close="editing = null"
      @saved="saved"
    />
  </div>
</template>

<style lang="scss" scoped>
.header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 20px;
  flex-wrap: wrap;
  margin-bottom: 10px;
}
.header-right {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}
.hyve-table {
  width: 100%;
  th, td { padding: 8px; text-align: left; vertical-align: top; }
  .small { font-size: 0.85em; }
}
.hyve-actions {
  white-space: nowrap;
  .small { margin-right: 6px; }
}
</style>
