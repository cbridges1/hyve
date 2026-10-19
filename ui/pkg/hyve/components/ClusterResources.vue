<script>
import Loading from '@shell/components/Loading';
import { LabeledInput } from '@components/Form/LabeledInput';
import { Banner } from '@components/Banner';
import { CAPI_CLUSTER, HYVE, LABELS } from '../config/types';
import {
  clusterItems, clusterResourceObject, clusterStates, helmEntry, stateOn, validate
} from '../utils/resources';

const TURTLES_OWNER = 'cluster-api.cattle.io/capi-cluster-owner';
const TURTLES_OWNER_NS = 'cluster-api.cattle.io/capi-cluster-owner-ns';

// Resources tab for a cluster: what hyve delivers to it (its template's
// resources and its own extras) with Fleet's state there. Works on a CAPI
// Cluster (hyve's Clusters page) and on a Rancher provisioning cluster
// (Cluster Management), through the CAPI cluster Turtles imported it from.
export default {
  name:       'HyveClusterResources',
  components: { Loading, LabeledInput, Banner },
  props:      {
    resource: {
      type:     Object,
      required: true,
    },
  },

  async fetch() {
    await this.load();
  },

  data() {
    return {
      capi:   null,
      items:  [],
      states: null,
      errors: [],
      adding: false,
      form:   {
        name: '', repo: '', chart: '', version: '', namespace: '', secrets: ''
      },
    };
  },

  computed: {
    templateName() {
      return this.capi?.metadata?.labels?.[LABELS.TEMPLATE] || '';
    },
  },

  methods: {
    async findCapi() {
      if (this.resource.type === CAPI_CLUSTER) {
        return this.resource;
      }
      const l = this.resource.mgmt?.metadata?.labels || {};

      if (!l[TURTLES_OWNER]) {
        return null;
      }

      return this.$store.dispatch('management/find', { type: CAPI_CLUSTER, id: `${ l[TURTLES_OWNER_NS] }/${ l[TURTLES_OWNER] }` }).catch(() => null);
    },
    async load() {
      this.capi = await this.findCapi();
      if (!this.capi) {
        return;
      }
      this.items = await clusterItems(this.$store, this.capi);
      this.states = await clusterStates(this.$store, this.capi.metadata.namespace, this.capi.metadata.name);
    },
    state(it) {
      return stateOn(it, this.states) || '-';
    },
    async add() {
      this.errors = [];
      const e = helmEntry({
        name:      this.form.name || undefined,
        repo:      this.form.repo,
        chart:     this.form.chart,
        version:   this.form.version,
        namespace: this.form.namespace,
        secrets:   this.form.secrets.split(',').map((s) => s.trim()).filter(Boolean),
      });
      const err = validate(e);

      if (err) {
        this.errors.push(err);

        return;
      }
      try {
        const model = await this.$store.dispatch('management/create', clusterResourceObject(this.capi, e));

        await model.save();
        this.adding = false;
        this.form = {
          name: '', repo: '', chart: '', version: '', namespace: '', secrets: ''
        };
        await this.load();
      } catch (e) {
        this.errors.push(e?.message || String(e));
      }
    },
    async remove(it) {
      this.errors = [];
      try {
        // An extra's Fleet object is owned by its ClusterResource.
        const cr = await this.$store.dispatch('management/find', { type: HYVE.CLUSTER_RESOURCE, id: `${ it.model.metadata.namespace }/${ it.owner }` });

        await cr.remove();
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
    <Banner
      v-if="!capi"
      color="info"
      :label="t('hyve.resources.notHyve')"
    />
    <template v-else>
      <Banner
        v-if="states === null"
        color="info"
        :label="t('hyve.resources.notRegistered')"
      />
      <Banner
        v-for="(e, i) in errors"
        :key="i"
        color="error"
        :label="e"
      />
      <table class="sortable-table resources">
        <thead>
          <tr>
            <th>{{ t('tableHeaders.name') }}</th>
            <th>{{ t('hyve.resources.from') }}</th>
            <th>{{ t('hyve.resources.kind') }}</th>
            <th>{{ t('hyve.resources.source') }}</th>
            <th>{{ t('hyve.resources.state') }}</th>
            <th />
          </tr>
        </thead>
        <tbody>
          <tr v-if="!items.length">
            <td colspan="6">
              {{ t('hyve.resources.none') }}
            </td>
          </tr>
          <tr
            v-for="it in items"
            :key="it.model.id"
          >
            <td>{{ it.name }}</td>
            <td>{{ it.ownerKind === 'ClusterTemplate' ? t('hyve.resources.fromTemplate', { name: it.owner }) : t('hyve.resources.fromCluster') }}</td>
            <td>{{ it.kind }}</td>
            <td>{{ it.source }}</td>
            <td>{{ state(it) }}</td>
            <td class="text-right">
              <button
                v-if="it.ownerKind === 'ClusterResource'"
                type="button"
                class="btn btn-sm role-link"
                @click="remove(it)"
              >
                {{ t('generic.remove') }}
              </button>
            </td>
          </tr>
        </tbody>
      </table>

      <div
        v-if="adding"
        class="mt-20"
      >
        <h3>{{ t('hyve.resources.addHelm') }}</h3>
        <div class="row mb-10">
          <div class="col span-6">
            <LabeledInput
              v-model:value="form.repo"
              :label="t('hyve.resources.repo')"
              placeholder="https://charts.example.com"
            />
          </div>
          <div class="col span-3">
            <LabeledInput
              v-model:value="form.chart"
              :label="t('hyve.resources.chart')"
              :required="true"
            />
          </div>
          <div class="col span-3">
            <LabeledInput
              v-model:value="form.version"
              :label="t('hyve.resources.version')"
            />
          </div>
        </div>
        <div class="row mb-10">
          <div class="col span-3">
            <LabeledInput
              v-model:value="form.name"
              :label="t('hyve.resources.name')"
              :placeholder="form.chart"
            />
          </div>
          <div class="col span-3">
            <LabeledInput
              v-model:value="form.namespace"
              :label="t('hyve.resources.namespace')"
            />
          </div>
          <div class="col span-6">
            <LabeledInput
              v-model:value="form.secrets"
              :label="t('hyve.resources.secrets')"
              :placeholder="t('hyve.resources.secretsPlaceholder')"
            />
          </div>
        </div>
        <button
          type="button"
          class="btn role-primary mr-10"
          @click="add"
        >
          {{ t('generic.add') }}
        </button>
        <button
          type="button"
          class="btn role-secondary"
          @click="adding = false"
        >
          {{ t('generic.cancel') }}
        </button>
      </div>
      <button
        v-else
        type="button"
        class="btn role-tertiary mt-20"
        @click="adding = true"
      >
        {{ t('hyve.resources.addHelm') }}
      </button>
      <p class="text-muted mt-10">
        {{ t('hyve.resources.cliHint', { name: capi.metadata.name }) }}
      </p>
    </template>
  </div>
</template>

<style lang="scss" scoped>
.resources {
  width: 100%;
  th, td { padding: 8px; text-align: left; }
}
</style>
