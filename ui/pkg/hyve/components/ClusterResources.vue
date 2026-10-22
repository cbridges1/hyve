<script>
import Loading from '@shell/components/Loading';
import { Banner } from '@components/Banner';
import { CAPI_CLUSTER, HYVE, LABELS } from '../config/types';
import {
  clusterItems, clusterResourceObject, clusterStates, stateOn
} from '../utils/resources';
import ResourceForm from './ResourceForm.vue';
import ViewDialog from './ViewDialog.vue';

const TURTLES_OWNER = 'cluster-api.cattle.io/capi-cluster-owner';
const TURTLES_OWNER_NS = 'cluster-api.cattle.io/capi-cluster-owner-ns';

// Resources tab for a cluster: what hyve delivers to it (its template's
// resources and its own extras) with Fleet's state there. Works on a CAPI
// Cluster (hyve's Clusters page) and on a Rancher provisioning cluster
// (Cluster Management), through the CAPI cluster Turtles imported it from.
// Extras can be added, edited and removed here ('hyve cluster resource').
export default {
  name:       'HyveClusterResources',
  live:       true,
  components: {
    Loading, Banner, ResourceForm, ViewDialog
  },
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
      // null, or { cr, entry } for the extra being added (cr null) or
      // edited.
      editing: null,
      formKey: 0,
      shared:  [],
      viewing: null,
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
      const [items, states, shared] = await Promise.all([
        clusterItems(this.$store, this.capi),
        clusterStates(this.$store, this.capi.metadata.namespace, this.capi.metadata.name),
        this.$store.dispatch('management/findAll', { type: HYVE.RESOURCE }).catch(() => []),
      ]);

      this.items = items;
      this.states = states;
      this.shared = shared.map((x) => ({ name: x.metadata.name, namespace: x.metadata.namespace }));
    },
    // An extra's Fleet object is owned by its ClusterResource.
    findCR(it) {
      return this.$store.dispatch('management/find', { type: HYVE.CLUSTER_RESOURCE, id: `${ it.model.metadata.namespace }/${ it.owner }`, opt: { force: true } });
    },
    startAdd() {
      this.errors = [];
      this.formKey++;
      this.editing = { cr: null, entry: null };
    },
    async startEdit(it) {
      this.errors = [];
      try {
        const cr = await this.findCR(it);
        const { clusterName, ...entry } = cr.spec || {};

        this.formKey++;
        this.editing = { cr, entry: { ...entry, name: it.name } };
      } catch (e) {
        this.errors.push(e?.message || String(e));
      }
    },
    async view(it) {
      const sections = [];

      if (it.ownerKind === 'ClusterResource') {
        const cr = await this.findCR(it).catch(() => null);

        if (cr) {
          sections.push({ label: `${ this.t('hyve.view.entry') } (ClusterResource)`, object: cr });
        }
      } else {
        const tpl = await this.$store.dispatch('management/find', { type: HYVE.TEMPLATE, id: `${ it.model.metadata.namespace }/${ it.owner }` }).catch(() => null);
        const entry = (tpl?.spec?.resources || []).find((e) => (e.name || e.ref) === it.name);

        if (entry) {
          sections.push({ label: `${ this.t('hyve.view.entry') } (template ${ it.owner })`, object: entry });
        }
      }
      sections.push({ label: this.t('hyve.view.delivered', { kind: it.kind }), object: it.model });
      this.viewing = { title: it.name, sections };
    },
    state(it) {
      return stateOn(it, this.states) || '-';
    },
    async save() {
      this.errors = [];
      try {
        const e = this.$refs.rform.entry();
        const want = clusterResourceObject(this.capi, e);
        const cr = this.editing.cr;

        // Renamed: the ClusterResource's name and label follow the
        // resource's, so make the new one and drop the old.
        if (cr && cr.metadata.name === want.metadata.name) {
          cr.spec = want.spec;
          await cr.save();
        } else {
          const taken = this.items.some((it) => it.ownerKind === 'ClusterResource' && it.name === e.name);

          if (taken && !cr) {
            throw new Error(this.t('hyve.template.dupResource', { name: e.name }));
          }
          const model = await this.$store.dispatch('management/create', want);

          await model.save();
          if (cr) {
            await cr.remove();
          }
        }
        this.editing = null;
        await this.load();
      } catch (e) {
        this.errors.push(e?.message || String(e));
      }
    },
    async remove(it) {
      this.errors = [];
      try {
        // An extra's Fleet object is owned by its ClusterResource.
        const cr = await this.findCR(it);

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
            <td class="text-right actions">
              <button
                type="button"
                class="btn btn-sm role-link"
                @click="view(it)"
              >
                {{ t('hyve.view.view') }}
              </button>
              <template v-if="it.ownerKind === 'ClusterResource'">
                <button
                  type="button"
                  class="btn btn-sm role-link"
                  @click="startEdit(it)"
                >
                  {{ t('generic.edit') }}
                </button>
                <button
                  type="button"
                  class="btn btn-sm role-link"
                  @click="remove(it)"
                >
                  {{ t('generic.remove') }}
                </button>
              </template>
            </td>
          </tr>
        </tbody>
      </table>

      <div
        v-if="editing"
        class="mt-20"
      >
        <h3>{{ editing.cr ? t('hyve.template.editResource', { name: editing.entry.name }) : t('hyve.resources.addExtra') }}</h3>
        <ResourceForm
          ref="rform"
          :key="formKey"
          :value="editing.entry"
          :shared="shared"
        />
        <button
          type="button"
          class="btn role-primary mr-10"
          @click="save"
        >
          {{ editing.cr ? t('generic.save') : t('generic.add') }}
        </button>
        <button
          type="button"
          class="btn role-secondary"
          @click="editing = null"
        >
          {{ t('generic.cancel') }}
        </button>
      </div>
      <button
        v-else
        type="button"
        class="btn role-tertiary mt-20"
        @click="startAdd"
      >
        {{ t('hyve.resources.addExtra') }}
      </button>
      <p class="text-muted mt-10">
        {{ t('hyve.resources.cliHint', { name: capi.metadata.name }) }}
      </p>
    </template>
    <ViewDialog
      v-if="viewing"
      :title="viewing.title"
      :sections="viewing.sections"
      @close="viewing = null"
    />
  </div>
</template>

<style lang="scss" scoped>
.resources {
  width: 100%;
  th, td { padding: 8px; text-align: left; }
  .actions { white-space: nowrap; }
}
</style>
