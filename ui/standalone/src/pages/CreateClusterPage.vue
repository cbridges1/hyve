<script>
import CreateCluster from '@pkg/pages/CreateCluster.vue';
import BackLink from '../components/BackLink.vue';
import Card from '../components/Card.vue';
import { CAPI_CLUSTER, PRODUCT } from '@pkg/config/types';

// The extension's Create Cluster page, under the console's back link.
export default {
  name:       'HyveCreateClusterPage',
  components: { CreateCluster, BackLink, Card },
  data() {
    return { back: { name: `${ PRODUCT }-c-cluster-resource`, params: { cluster: '_', resource: CAPI_CLUSTER } } };
  },
};
</script>

<template>
  <div>
    <BackLink
      :to="back"
      label="Clusters"
    />
    <Card>
      <CreateCluster />
    </Card>
  </div>
</template>
