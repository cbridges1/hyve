<script>
import Loading from '@shell/components/Loading';
import { HYVE, LABELS } from '../config/types';
import EnvironmentPicker from '../components/EnvironmentPicker.vue';

// The Secrets each resource copies to clusters, by name: whether each
// exists (from hyve-controller's status, so the page never reads a
// Secret) and which clusters it's delivered to. Values are managed outside
// hyve, so there's nothing to edit here.
export default {
  name:       'HyveSecrets',
  components: { Loading, EnvironmentPicker },

  async fetch() {
    const [templates, clusterResources, shared, clusters] = await Promise.all([
      this.$store.dispatch('management/findAll', { type: HYVE.TEMPLATE }).catch(() => []),
      this.$store.dispatch('management/findAll', { type: HYVE.CLUSTER_RESOURCE }).catch(() => []),
      this.$store.dispatch('management/findAll', { type: HYVE.RESOURCE }).catch(() => []),
      this.$store.dispatch('management/findAll', { type: 'cluster.x-k8s.io.cluster' }).catch(() => []),
    ]);
    const rows = {};
    const add = (ns, secret, owner, resource, missing, reach) => {
      const key = `${ ns }/${ secret }`;
      const r = rows[key] || (rows[key] = {
        key, ns, secret, neededBy: [], missing: false, clusters: new Map()
      });

      r.neededBy.push(`${ owner } › ${ resource }`);
      r.missing = r.missing || missing;
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
      .map((r) => ({ ...r, clusters: [...r.clusters.values()] }))
      .sort((a, b) => a.key.localeCompare(b.key));
  },

  data() {
    return { environment: '', rows: [] };
  },

  computed: {
    shown() {
      if (!this.environment) {
        return this.rows;
      }

      return this.rows
        .map((r) => ({ ...r, clusters: r.clusters.filter((c) => c.metadata.namespace === this.environment) }))
        .filter((r) => r.clusters.length || r.ns === this.environment);
    },
  },
};
</script>

<template>
  <Loading v-if="$fetchState.pending" />
  <div v-else>
    <div class="header">
      <h1>{{ t('hyve.secrets.title') }}</h1>
      <EnvironmentPicker v-model:value="environment" />
    </div>
    <p class="text-muted mb-20">
      {{ t('hyve.secrets.hint') }}
    </p>
    <table class="sortable-table hyve-table">
      <thead>
        <tr>
          <th>{{ t('tableHeaders.name') }}</th>
          <th>{{ t('tableHeaders.namespace') }}</th>
          <th>{{ t('hyve.secrets.exists') }}</th>
          <th>{{ t('hyve.secrets.neededBy') }}</th>
          <th>{{ t('hyve.secrets.deliveredTo') }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-if="!shown.length">
          <td colspan="5">
            {{ t('hyve.secrets.none') }}
          </td>
        </tr>
        <tr
          v-for="r in shown"
          :key="r.key"
        >
          <td>{{ r.secret }}</td>
          <td>{{ r.ns }}</td>
          <td :class="r.missing ? 'text-error' : 'text-success'">
            {{ r.missing ? t('hyve.secrets.missing') : t('hyve.secrets.present') }}
          </td>
          <td>
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
        </tr>
      </tbody>
    </table>
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
.hyve-table {
  width: 100%;
  th, td { padding: 8px; text-align: left; vertical-align: top; }
}
</style>
