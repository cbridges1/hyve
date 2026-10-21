<script>
import Loading from '../shims/Loading.vue';
import BackLink from '../components/BackLink.vue';
import Card from '../components/Card.vue';
import CruSharedResource from '@pkg/edit/hyve.io.resource.vue';
import { DEFAULT_TEMPLATE_NAMESPACE, HYVE, PRODUCT } from '@pkg/config/types';
import { detailRoute } from '../util';

// The extension's shared Resource form, given its object the way Rancher's
// create/edit pages would.
export default {
  name:       'HyveSharedResourcePage',
  components: {
    Loading, BackLink, Card, CruSharedResource
  },
  props: {
    type: { type: String, default: HYVE.RESOURCE },
    id:   { type: String, default: '' },
    mode: { type: String, default: 'view' },
  },

  async fetch() {
    if (this.mode === 'create') {
      this.value = await this.$store.dispatch('management/create', {
        type: HYVE.RESOURCE, metadata: { name: '', namespace: DEFAULT_TEMPLATE_NAMESPACE }, spec: {}
      });
    } else {
      this.value = await this.$store.dispatch('management/find', { type: HYVE.RESOURCE, id: this.id });
    }
  },

  data() {
    return { value: null, back: { name: `${ PRODUCT }-c-cluster-resource`, params: { cluster: '_', resource: HYVE.RESOURCE } } };
  },

  computed: {
    yamlRoute() {
      return this.value && this.mode !== 'create' ? detailRoute(HYVE.RESOURCE, this.value, { as: 'yaml' }) : null;
    },
  },
};
</script>

<template>
  <Loading v-if="$fetchState.pending" />
  <div v-else-if="value">
    <BackLink
      :to="back"
      label="Shared resources"
    />
    <div class="header">
      <h1 class="mb-0">
        {{ mode === 'create' ? 'New shared resource' : value.metadata.name }}
      </h1>
      <router-link
        v-if="yamlRoute"
        class="btn role-secondary"
        :to="yamlRoute"
      >
        YAML
      </router-link>
    </div>
    <Card>
      <CruSharedResource
        :value="value"
        :mode="mode"
      />
    </Card>
  </div>
</template>
