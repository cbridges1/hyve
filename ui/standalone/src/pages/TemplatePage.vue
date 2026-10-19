<script>
import Loading from '../shims/Loading.vue';
import BackLink from '../components/BackLink.vue';
import Card from '../components/Card.vue';
import CruClusterTemplate from '@pkg/edit/hyve.io.clustertemplate.vue';
import { DEFAULT_TEMPLATE_NAMESPACE, HYVE, PRODUCT } from '@pkg/config/types';
import { detailRoute } from '../util';

// The extension's template form, given its object the way Rancher's
// create/edit pages would.
export default {
  name:       'HyveTemplatePage',
  components: {
    Loading, BackLink, Card, CruClusterTemplate
  },
  props: {
    type: { type: String, default: HYVE.TEMPLATE },
    id:   { type: String, default: '' },
    mode: { type: String, default: 'view' },
  },

  async fetch() {
    if (this.mode === 'create') {
      this.value = await this.$store.dispatch('management/create', {
        type: HYVE.TEMPLATE, metadata: { name: '', namespace: DEFAULT_TEMPLATE_NAMESPACE }, spec: {}
      });
    } else {
      this.value = await this.$store.dispatch('management/find', { type: HYVE.TEMPLATE, id: this.id });
    }
  },

  data() {
    return { value: null, back: { name: `${ PRODUCT }-c-cluster-resource`, params: { cluster: '_', resource: HYVE.TEMPLATE } } };
  },

  computed: {
    yamlRoute() {
      return this.value && this.mode !== 'create' ? detailRoute(HYVE.TEMPLATE, this.value, { as: 'yaml' }) : null;
    },
  },
};
</script>

<template>
  <Loading v-if="$fetchState.pending" />
  <div v-else-if="value">
    <BackLink
      :to="back"
      label="Templates"
    />
    <div class="mb-4 flex flex-wrap items-center justify-between gap-3">
      <h1 class="mb-0">
        {{ mode === 'create' ? 'New template' : value.metadata.name }}
      </h1>
      <div
        v-if="mode === 'view'"
        class="flex items-center gap-2"
      >
        <router-link
          class="btn role-secondary"
          :to="yamlRoute"
        >
          YAML
        </router-link>
        <button
          type="button"
          class="btn role-primary"
          @click="value.createCluster()"
        >
          New cluster
        </button>
      </div>
    </div>
    <Card>
      <CruClusterTemplate
        :value="value"
        :mode="mode"
      />
    </Card>
  </div>
</template>
