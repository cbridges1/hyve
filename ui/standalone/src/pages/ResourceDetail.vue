<script>
import { CAPI_CLUSTER, HYVE } from '@pkg/config/types';
import ClusterDetail from './ClusterDetail.vue';
import TemplatePage from './TemplatePage.vue';
import SharedResourcePage from './SharedResourcePage.vue';
import YamlEditor from '../components/YamlEditor.vue';

export default {
  name:     'HyveResourceDetail',
  computed: {
    type() {
      return this.$route.params.resource;
    },
    id() {
      const p = this.$route.params;

      return p.namespace ? `${ p.namespace }/${ p.id }` : p.id;
    },
    yaml() {
      return this.$route.query.as === 'yaml';
    },
    page() {
      if (this.yaml) {
        return YamlEditor;
      }

      return { [CAPI_CLUSTER]: ClusterDetail, [HYVE.TEMPLATE]: TemplatePage, [HYVE.RESOURCE]: SharedResourcePage }[this.type] || YamlEditor;
    },
  },
};
</script>

<template>
  <component
    :is="page"
    :type="type"
    :id="id"
    :mode="$route.query.mode === 'edit' ? 'edit' : 'view'"
  />
</template>
