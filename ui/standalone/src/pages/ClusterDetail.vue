<script>
import Loading from '../shims/Loading.vue';
import Card from '../components/Card.vue';
import Field from '../components/Field.vue';
import BackLink from '../components/BackLink.vue';
import Pill from '../components/Pill.vue';
import ConfirmButton from '../components/ConfirmButton.vue';
import ClusterResources from '@pkg/components/ClusterResources.vue';
import {
  ANNOTATIONS, CAPI_CLUSTER, LABELS, NEVER, PRODUCT
} from '@pkg/config/types';
import { detailRoute } from '../util';
import { clusterStatus, expiry } from '../status';

const EXTEND_HOURS = 8;

function isoSeconds(d) {
  return d.toISOString().replace(/\.\d{3}Z$/, 'Z');
}

// A cluster, the console's way: its fields, conditions and resources in
// cards, with scale, expiry, YAML and delete.
export default {
  name:       'HyveClusterDetail',
  components: {
    Loading, Card, Field, BackLink, Pill, ConfirmButton, ClusterResources
  },
  props: {
    type: { type: String, default: CAPI_CLUSTER },
    id:   { type: String, required: true },
  },

  async fetch() {
    await this.load();
  },

  data() {
    return {
      value: null, workers: '', errors: [], busy: false, back: { name: `${ PRODUCT }-c-cluster-resource`, params: { cluster: '_', resource: CAPI_CLUSTER } }
    };
  },

  computed: {
    topo() {
      return this.value?.spec?.topology || {};
    },
    pools() {
      return [...(this.topo.workers?.machineDeployments || []), ...(this.topo.workers?.machinePools || [])];
    },
    expires() {
      return this.value?.metadata?.annotations?.[ANNOTATIONS.EXPIRES_AT] || '';
    },
    status() {
      return clusterStatus(this.value);
    },
    expiryPill() {
      return expiry(this.expires);
    },
    fields() {
      const v = this.value;
      const l = v.metadata?.labels || {};
      const topo = this.topo;

      return [
        ['Environment', v.metadata?.namespace],
        ['Template', l[LABELS.TEMPLATE] ? `${ l[LABELS.TEMPLATE_NAMESPACE] || v.metadata.namespace }/${ l[LABELS.TEMPLATE] }` : '—'],
        ['ClusterClass', topo.classRef ? `${ topo.classRef.namespace || v.metadata.namespace }/${ topo.classRef.name }` : (topo.class || '—')],
        ['Kubernetes', topo.version || '—'],
        ['Control plane', topo.controlPlane?.replicas ?? '—'],
        ['Workers', this.pools.map((w) => `${ w.replicas } × ${ w.class }`).join(', ') || '—'],
        ['Expires', !this.expires || this.expires === NEVER ? 'Never' : new Date(this.expires).toLocaleString()],
        ['Created by', v.metadata?.annotations?.[ANNOTATIONS.CREATOR] || '—'],
        ['kubectl', `hyve cluster kubeconfig ${ v.metadata.name } -e ${ v.metadata.namespace }`],
      ];
    },
    variables() {
      return (this.topo.variables || []).map((x) => [x.name, JSON.stringify(x.value)]);
    },
    conditions() {
      return (this.value?.status?.conditions || []).filter((c) => c.status !== 'True' && c.message && c.type !== 'Paused' && c.type !== 'Deleting');
    },
  },

  methods: {
    async load() {
      this.value = await this.$store.dispatch('management/find', { type: CAPI_CLUSTER, id: this.id });
      this.workers = String(this.pools[0]?.replicas ?? '');
    },
    async act(fn) {
      this.errors = [];
      this.busy = true;
      try {
        await fn();
        await this.load();
      } catch (e) {
        this.errors.push(e.message);
        await this.load().catch(() => {});
      } finally {
        this.busy = false;
      }
    },
    setExpiry(v) {
      return this.act(async() => {
        this.value.metadata.annotations = { ...(this.value.metadata.annotations || {}), [ANNOTATIONS.EXPIRES_AT]: v };
        await this.value.save();
      });
    },
    extend() {
      const cur = Date.parse(this.expires);

      return this.setExpiry(isoSeconds(new Date(Math.max(Date.now(), isNaN(cur) ? 0 : cur) + EXTEND_HOURS * 3600 * 1000)));
    },
    scale() {
      return this.act(async() => {
        this.pools[0].replicas = Number(this.workers);
        await this.value.save();
      });
    },
    remove() {
      return this.act(async() => {
        await this.value.remove();
        this.$router.push(this.back);
      });
    },
    yamlRoute() {
      return detailRoute(CAPI_CLUSTER, this.value, { as: 'yaml' });
    },
  },
};
</script>

<template>
  <Loading v-if="$fetchState.pending" />
  <div
    v-else-if="value"
    class="space-y-4"
  >
    <div>
      <BackLink
        :to="back"
        label="Clusters"
      />
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div class="flex min-w-0 items-center gap-3">
          <h1 class="mb-0 truncate">
            {{ value.metadata.name }}
          </h1>
          <Pill
            tone="env"
            :label="value.metadata.namespace"
          />
          <Pill
            v-if="expiryPill"
            v-bind="expiryPill"
          />
          <Pill v-bind="status" />
        </div>
        <div class="flex items-center gap-2">
          <router-link
            class="btn role-secondary"
            :to="yamlRoute()"
          >
            YAML
          </router-link>
          <ConfirmButton
            v-if="!value.metadata.deletionTimestamp"
            label="Delete"
            :confirm="`Delete ${ value.metadata.name }?`"
            @confirm="remove"
          />
        </div>
      </div>
    </div>
    <p
      v-for="(e, i) in errors"
      :key="i"
      class="rounded-lg bg-red-50 px-3 py-2 text-sm text-red-700 dark:bg-red-950/60 dark:text-red-300"
    >
      {{ e }}
    </p>

    <div class="grid gap-4 lg:grid-cols-3">
      <div class="lg:col-span-2">
        <Card title="Details">
          <Field
            v-for="[k, v] in fields"
            :key="k"
            :label="k"
          >
            <code v-if="k === 'kubectl'">{{ v }}</code>
            <template v-else>
              {{ v }}
            </template>
          </Field>
        </Card>
      </div>
      <div class="space-y-4">
        <Card
          v-if="pools.length"
          title="Workers"
        >
          <form
            class="flex gap-2"
            @submit.prevent="scale"
          >
            <input
              v-model="workers"
              type="number"
              min="0"
              aria-label="Worker replicas"
            >
            <button
              type="submit"
              class="btn role-primary"
              :disabled="busy || workers === '' || Number(workers) === pools[0].replicas"
            >
              Scale
            </button>
          </form>
        </Card>
        <Card
          v-if="expires && expires !== 'never'"
          title="Expiry"
        >
          <div class="flex flex-wrap gap-2">
            <button
              type="button"
              class="btn role-secondary btn-sm"
              :disabled="busy"
              @click="extend"
            >
              Extend 8 hours
            </button>
            <button
              type="button"
              class="btn role-tertiary btn-sm"
              :disabled="busy"
              @click="setExpiry('never')"
            >
              Never expire
            </button>
          </div>
        </Card>
        <Card
          v-if="variables.length"
          title="Variables"
        >
          <Field
            v-for="[k, v] in variables"
            :key="k"
            :label="k"
          >
            <code>{{ v }}</code>
          </Field>
        </Card>
      </div>
    </div>

    <Card
      v-if="conditions.length"
      title="Conditions"
    >
      <Field
        v-for="c in conditions"
        :key="c.type"
        :label="c.type"
      >
        {{ c.message }}
      </Field>
    </Card>

    <Card title="Resources">
      <ClusterResources :resource="value" />
    </Card>
  </div>
</template>
