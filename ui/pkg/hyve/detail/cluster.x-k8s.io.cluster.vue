<script>
import CreateEditView from '@shell/mixins/create-edit-view';
import ResourceTabs from '@shell/components/form/ResourceTabs';
import Tab from '@shell/components/Tabbed/Tab';
import { ANNOTATIONS, LABELS } from '../config/types';

// Detail page for a CAPI Cluster: an overview of hyve's view of it, plus
// Rancher's standard tabs and extension tabs (hyve's Resources tab).
export default {
  name:       'HyveCapiClusterDetail',
  components: { ResourceTabs, Tab },
  mixins:     [CreateEditView],

  computed: {
    rows() {
      const v = this.value;
      const l = v.metadata?.labels || {};
      const topo = v.spec?.topology || {};
      const workers = [...(topo.workers?.machineDeployments || []), ...(topo.workers?.machinePools || [])];

      return [
        [this.t('hyve.headers.environment'), v.metadata?.namespace],
        [this.t('hyve.headers.template'), l[LABELS.TEMPLATE] ? `${ l[LABELS.TEMPLATE_NAMESPACE] || v.metadata.namespace }/${ l[LABELS.TEMPLATE] }` : '-'],
        [this.t('hyve.headers.class'), topo.classRef ? `${ topo.classRef.namespace || v.metadata.namespace }/${ topo.classRef.name }` : '-'],
        [this.t('hyve.headers.version'), topo.version || '-'],
        [this.t('hyve.detail.controlPlane'), topo.controlPlane?.replicas ?? '-'],
        [this.t('hyve.detail.workers'), workers.map((w) => `${ w.replicas } × ${ w.class }`).join(', ') || '-'],
        [this.t('hyve.headers.phase'), v.status?.phase || '-'],
        [this.t('hyve.headers.expires'), (v.metadata?.annotations?.[ANNOTATIONS.EXPIRES_AT] || 'never') === 'never' ? this.t('hyve.detail.never') : v.metadata.annotations[ANNOTATIONS.EXPIRES_AT]],
        [this.t('hyve.detail.creator'), v.metadata?.annotations?.[ANNOTATIONS.CREATOR] || '-'],
      ];
    },
    variables() {
      return (this.value.spec?.topology?.variables || []).map((x) => [x.name, JSON.stringify(x.value)]);
    },
  },
};
</script>

<template>
  <ResourceTabs
    :value="value"
    :mode="mode"
    :need-related="false"
  >
    <Tab
      name="hyve-overview"
      :label="t('hyve.detail.overview')"
      :weight="10"
    >
      <table class="kv">
        <tr
          v-for="[k, v] in rows"
          :key="k"
        >
          <th>{{ k }}</th>
          <td>{{ v }}</td>
        </tr>
      </table>
      <template v-if="variables.length">
        <h3 class="mt-20">
          {{ t('hyve.template.variables') }}
        </h3>
        <table class="kv">
          <tr
            v-for="[k, v] in variables"
            :key="k"
          >
            <th>{{ k }}</th>
            <td><code>{{ v }}</code></td>
          </tr>
        </table>
      </template>
    </Tab>
  </ResourceTabs>
</template>

<style lang="scss" scoped>
.kv {
  th { text-align: left; padding: 4px 20px 4px 0; color: var(--input-label); font-weight: normal; }
  td { padding: 4px 0; }
}
</style>
