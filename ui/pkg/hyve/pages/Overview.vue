<script>
import Loading from '@shell/components/Loading';
import { Banner } from '@components/Banner';
import { BLANK_CLUSTER } from '@shell/store/store-types.js';
import {
  ANNOTATIONS, CAPI_CLUSTER, CAPI_CLUSTER_CLASS, HYVE, LABELS, NEVER, PRODUCT
} from '../config/types';
import {
  allClusterStates, allItems, problem, stateColor, stateOn
} from '../utils/resources';
import { classOf, providerOfClass } from '../utils/providers';
import EnvironmentPicker from '../components/EnvironmentPicker.vue';
import ApplyYaml from '../components/ApplyYaml.vue';

const SOON_HOURS = 24;
const RANCHER_CLUSTER = 'provisioning.cattle.io.cluster';
const TURTLES_OWNER = 'cluster-api.cattle.io/capi-cluster-owner';
const TURTLES_OWNER_NS = 'cluster-api.cattle.io/capi-cluster-owner-ns';
const EXTEND_HOURS = 8;

function isoSeconds(d) {
  return d.toISOString().replace(/\.\d{3}Z$/, 'Z');
}

// Every hyve cluster with its provider, template, environment, phase and
// expiry; resources failing anywhere; and clusters expiring soon.
export default {
  name:       'HyveOverview',
  components: {
    Loading, Banner, EnvironmentPicker, ApplyYaml
  },

  async fetch() {
    await this.load();
  },

  data() {
    return {
      environment: '',
      applying:    false,
      clusters:    [],
      templates:   [],
      classes:     [],
      rancher:     [],
      items:       [],
      states:      {},
      reaches:     () => [],
      errors:      [],
      now:         Date.now(),
    };
  },

  computed: {
    // Without Rancher (hyve's own UI), there's no Rancher state to show.
    hasRancher() {
      return !!this.$store.getters['management/schemaFor'](RANCHER_CLUSTER);
    },
    shown() {
      return this.clusters.filter((c) => !this.environment || c.metadata.namespace === this.environment);
    },
    rows() {
      return this.shown.map((c) => {
        const l = c.metadata?.labels || {};
        const t = this.templates.find((x) => x.metadata.name === l[LABELS.TEMPLATE] && x.metadata.namespace === (l[LABELS.TEMPLATE_NAMESPACE] || c.metadata.namespace));
        const cls = this.classes.find((x) => x.metadata.name === c.spec?.topology?.classRef?.name && x.metadata.namespace === (c.spec?.topology?.classRef?.namespace || c.metadata.namespace)) || classOf(t, this.classes);
        const expires = c.metadata?.annotations?.[ANNOTATIONS.EXPIRES_AT] || '';
        const at = expires && expires !== NEVER ? Date.parse(expires) : NaN;

        return {
          cluster:  c,
          provider: providerOfClass(cls) || '-',
          template: l[LABELS.TEMPLATE] || '-',
          phase:    c.status?.phase || '-',
          rancher:  this.rancherState(c),
          expires:  isNaN(at) ? '' : new Date(at).toLocaleString(),
          soon:     !isNaN(at) && at - this.now < SOON_HOURS * 3600 * 1000,
        };
      });
    },
    expiringSoon() {
      return this.rows.filter((r) => r.soon);
    },
    // Each failing resource, with the clusters it's failing on.
    failing() {
      const out = [];

      this.items.forEach((it) => {
        const own = problem(it.model);
        const on = this.reaches(it)
          .filter((c) => !this.environment || c.metadata.namespace === this.environment)
          .map((c) => ({ c, state: stateOn(it, this.states[`${ c.metadata.namespace }/${ c.metadata.name }`] || null) }))
          .filter((x) => x.state && x.state !== 'Ready');

        if (own || on.length) {
          out.push({
            it, own, on
          });
        }
      });

      return out;
    },
  },

  methods: {
    async load() {
      const [all, states, classes, rancher] = await Promise.all([
        allItems(this.$store),
        allClusterStates(this.$store),
        this.$store.dispatch('management/findAll', { type: CAPI_CLUSTER_CLASS }).catch(() => []),
        // Rancher mode only: the clusters Turtles imported.
        this.$store.getters['management/schemaFor'](RANCHER_CLUSTER) ? this.$store.dispatch('management/findAll', { type: RANCHER_CLUSTER }).catch(() => []) : [],
      ]);

      this.rancher = rancher;

      this.clusters = all.clusters;
      this.templates = all.templates;
      this.items = all.items;
      this.reaches = all.reaches;
      this.states = states;
      this.classes = classes;
      this.now = Date.now();
    },
    // The state of the Rancher cluster Turtles imported c as, if any.
    rancherState(c) {
      const pc = this.rancher.find((x) => {
        const l = x.metadata?.labels || {};

        return l[TURTLES_OWNER] === c.metadata.name && l[TURTLES_OWNER_NS] === c.metadata.namespace;
      });

      return pc ? (pc.stateDisplay || (pc.status?.ready ? 'Active' : 'Pending')) : '';
    },
    detail(c) {
      return {
        name:   `${ PRODUCT }-c-cluster-resource-namespace-id`,
        params: {
          product: PRODUCT, cluster: BLANK_CLUSTER, resource: CAPI_CLUSTER, namespace: c.metadata.namespace, id: c.metadata.name
        },
      };
    },
    templateRoute(name, ns) {
      return {
        name:   `${ PRODUCT }-c-cluster-resource-namespace-id`,
        params: {
          product: PRODUCT, cluster: BLANK_CLUSTER, resource: HYVE.TEMPLATE, namespace: ns, id: name
        },
      };
    },
    async extend(c) {
      this.errors = [];
      try {
        const cur = Date.parse(c.metadata?.annotations?.[ANNOTATIONS.EXPIRES_AT] || '');
        const base = Math.max(Date.now(), isNaN(cur) ? 0 : cur);

        c.metadata.annotations = { ...(c.metadata.annotations || {}), [ANNOTATIONS.EXPIRES_AT]: isoSeconds(new Date(base + EXTEND_HOURS * 3600 * 1000)) };
        await c.save();
        await this.load();
      } catch (e) {
        this.errors.push(e?.message || String(e));
      }
    },
    stateColor,
  },
};
</script>

<template>
  <Loading v-if="$fetchState.pending" />
  <div v-else>
    <div class="header">
      <h1>{{ t('hyve.overview.title') }}</h1>
      <div class="header-right">
        <EnvironmentPicker v-model:value="environment" />
        <button
          type="button"
          class="btn role-secondary"
          @click="applying = true"
        >
          {{ t('hyve.apply.title') }}
        </button>
      </div>
    </div>
    <ApplyYaml
      v-if="applying"
      @close="applying = false"
      @applied="load"
    />
    <Banner
      v-for="(e, i) in errors"
      :key="i"
      color="error"
      :label="e"
    />

    <div class="cards mb-20">
      <div class="card">
        <div class="count">
          {{ shown.length }}
        </div>
        <div>{{ t('hyve.overview.clusters') }}</div>
      </div>
      <div class="card">
        <div :class="['count', failing.length ? 'text-error' : 'text-success']">
          {{ failing.length }}
        </div>
        <div>{{ t('hyve.overview.failing') }}</div>
      </div>
      <div class="card">
        <div :class="['count', expiringSoon.length ? 'text-warning' : '']">
          {{ expiringSoon.length }}
        </div>
        <div>{{ t('hyve.overview.expiringSoon') }}</div>
      </div>
    </div>

    <template v-if="failing.length">
      <h2>{{ t('hyve.overview.failingTitle') }}</h2>
      <table class="sortable-table hyve-table mb-20">
        <thead>
          <tr>
            <th>{{ t('tableHeaders.name') }}</th>
            <th>{{ t('hyve.resources.from') }}</th>
            <th>{{ t('hyve.overview.problem') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="f in failing"
            :key="f.it.model.id"
          >
            <td>{{ f.it.name }}</td>
            <td>
              <router-link
                v-if="f.it.ownerKind === 'ClusterTemplate'"
                :to="templateRoute(f.it.owner, f.it.model.metadata.namespace)"
              >
                {{ t('hyve.resources.fromTemplate', { name: f.it.owner }) }}
              </router-link>
              <span v-else>{{ t('hyve.resources.fromCluster') }}</span>
            </td>
            <td>
              <div
                v-if="f.own"
                class="text-error"
              >
                {{ f.own }}
              </div>
              <div
                v-for="x in f.on"
                :key="x.c.id"
              >
                <router-link :to="detail(x.c)">
                  {{ x.c.metadata.namespace }}/{{ x.c.metadata.name }}
                </router-link>:
                <span :class="stateColor(x.state)">{{ x.state }}</span>
              </div>
            </td>
          </tr>
        </tbody>
      </table>
    </template>

    <h2>{{ t('hyve.overview.clustersTitle') }}</h2>
    <table class="sortable-table hyve-table">
      <thead>
        <tr>
          <th>{{ t('tableHeaders.name') }}</th>
          <th>{{ t('hyve.headers.environment') }}</th>
          <th>{{ t('hyve.overview.provider') }}</th>
          <th>{{ t('hyve.headers.template') }}</th>
          <th>{{ t('hyve.headers.phase') }}</th>
          <th v-if="hasRancher">
            {{ t('hyve.overview.rancher') }}
          </th>
          <th>{{ t('hyve.headers.expires') }}</th>
          <th />
        </tr>
      </thead>
      <tbody>
        <tr v-if="!rows.length">
          <td colspan="8">
            {{ t('hyve.overview.none') }}
          </td>
        </tr>
        <tr
          v-for="r in rows"
          :key="r.cluster.id"
        >
          <td>
            <router-link :to="detail(r.cluster)">
              {{ r.cluster.metadata.name }}
            </router-link>
          </td>
          <td>{{ r.cluster.metadata.namespace }}</td>
          <td>{{ r.provider }}</td>
          <td>{{ r.template }}</td>
          <td>{{ r.phase }}</td>
          <td v-if="hasRancher">
            {{ r.rancher || '-' }}
          </td>
          <td :class="r.soon ? 'text-warning' : ''">
            {{ r.expires || t('hyve.overview.never') }}
          </td>
          <td class="text-right">
            <button
              v-if="r.expires"
              type="button"
              class="btn btn-sm role-link"
              @click="extend(r.cluster)"
            >
              {{ t('hyve.expiry.extend') }}
            </button>
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>

<style lang="scss" scoped>
.header-right {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}
.header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 20px;
  flex-wrap: wrap;
  margin-bottom: 20px;
}
.cards {
  display: flex;
  gap: 20px;
  flex-wrap: wrap;
}
.card {
  border: 1px solid var(--border);
  border-radius: var(--border-radius);
  padding: 15px 20px;
  min-width: 160px;
  .count {
    font-size: 2em;
    line-height: 1.2;
  }
}
.hyve-table {
  width: 100%;
  th, td { padding: 8px; text-align: left; vertical-align: top; }
}
</style>
