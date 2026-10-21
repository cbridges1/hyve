<script>
import { CAPI_CLUSTER, HYVE, PRODUCT } from '@pkg/config/types';
import TemplatePage from './TemplatePage.vue';
import SharedResourcePage from './SharedResourcePage.vue';
import YamlEditor from '../components/YamlEditor.vue';

export default {
  name: 'HyveResourceCreate',
  created() {
    // Clusters are made from templates.
    if (this.$route.params.resource === CAPI_CLUSTER) {
      this.$router.replace({ name: `${ PRODUCT }-c-cluster-${ HYVE.CREATE_CLUSTER }`, params: { cluster: '_' } });
    }
  },
  computed: {
    type() {
      return this.$route.params.resource;
    },
    page() {
      return { [HYVE.TEMPLATE]: TemplatePage, [HYVE.RESOURCE]: SharedResourcePage }[this.type] || YamlEditor;
    },
  },
};
</script>

<template>
  <component
    :is="page"
    :type="type"
    mode="create"
  />
</template>
