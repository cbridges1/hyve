<script>
import { CAPI_CLUSTER, HYVE } from '@pkg/config/types';
import ClusterList from './ClusterList.vue';
import TemplateList from './TemplateList.vue';
import SharedResourceList from './SharedResourceList.vue';
import Users from './Users.vue';
import GenericList from './GenericList.vue';

const PAGES = {
  [CAPI_CLUSTER]:  ClusterList,
  [HYVE.TEMPLATE]: TemplateList,
  [HYVE.RESOURCE]: SharedResourceList,
  'hyve.io.user':  Users,
};

// Rancher's list route: hyve's own list for its kinds, a plain one for the
// rest.
export default {
  name:     'HyveResourceList',
  computed: {
    page() {
      return PAGES[this.$route.params.resource] || GenericList;
    },
  },
};
</script>

<template>
  <component
    :is="page"
    :type="$route.params.resource"
  />
</template>
