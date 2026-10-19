<script>
import Loading from '../shims/Loading.vue';
import { age, createRoute, detailRoute } from '../util';

export default {
  name:       'HyveGenericList',
  components: { Loading },
  props:      { type: { type: String, required: true } },

  async fetch() {
    this.rows = (await this.$store.dispatch('management/findAll', { type: this.type, opt: { force: true } }))
      .sort((a, b) => a.id.localeCompare(b.id));
  },

  data() {
    return { rows: [] };
  },

  computed: {
    schema() {
      return this.$store.getters['management/schemaFor'](this.type);
    },
  },

  methods: {
    age, detailRoute, createRoute
  },
};
</script>

<template>
  <Loading v-if="$fetchState.pending" />
  <div v-else>
    <div class="mb-4 flex items-center justify-between">
      <h1 class="mb-0">
        {{ schema ? schema.kind : type }}
      </h1>
      <router-link
        v-if="schema && schema.verbs.includes('create')"
        class="btn role-primary"
        :to="createRoute(type)"
      >
        New
      </router-link>
    </div>
    <table class="sortable-table">
      <thead>
        <tr>
          <th>Name</th>
          <th v-if="schema && schema.namespaced">
            {{ t('tableHeaders.namespace') }}
          </th>
          <th>Age</th>
        </tr>
      </thead>
      <tbody>
        <tr v-if="!rows.length">
          <td colspan="3">
            Nothing here yet.
          </td>
        </tr>
        <tr
          v-for="r in rows"
          :key="r.id"
        >
          <td>
            <router-link :to="detailRoute(type, r)">
              {{ r.metadata.name }}
            </router-link>
          </td>
          <td v-if="schema && schema.namespaced">
            {{ r.metadata.namespace }}
          </td>
          <td>{{ age(r.metadata.creationTimestamp) }}</td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
