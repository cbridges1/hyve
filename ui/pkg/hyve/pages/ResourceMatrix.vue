<script>
import Loading from '@shell/components/Loading';
import LabeledSelect from '@shell/components/form/LabeledSelect';
import { RadioGroup } from '@components/Form/Radio';
import { Banner } from '@components/Banner';
import { BLANK_CLUSTER } from '@shell/store/store-types.js';
import {
  CAPI_CLUSTER, HYVE, LABELS, PRODUCT
} from '../config/types';
import {
  allClusterStates, allItems, clusterResourceObject, problem, stateColor, stateOn
} from '../utils/resources';
import EnvironmentPicker from '../components/EnvironmentPicker.vue';
import ViewDialog from '../components/ViewDialog.vue';
import ResourceForm from '../components/ResourceForm.vue';

function emptyForm() {
  return {
    target: 'template', template: '', cluster: '', entry: null, editing: false, key: 0
  };
}

// Resources down the side, clusters across the top: each cell is Fleet's
// state for that resource on that cluster, and why it's there (the
// template, or an extra on the cluster). Adding asks whether a resource
// goes on a template (every cluster made from it) or one cluster; Edit
// changes a row's resource where it's defined.
export default {
  name:       'HyveResourceMatrix',
  live:       true,
  components: {
    Loading, LabeledSelect, RadioGroup, Banner, EnvironmentPicker, ViewDialog, ResourceForm
  },

  async fetch() {
    await this.load();
  },

  data() {
    return {
      environment:      '',
      items:            [],
      clusters:         [],
      templates:        [],
      clusterResources: [],
      states:           {},
      reaches:          () => [],
      adding:           false,
      form:             emptyForm(),
      errors:           [],
      saving:           false,
      viewing:          null,
      shared:           [],
    };
  },

  computed: {
    columns() {
      return this.clusters
        .filter((c) => !this.environment || c.metadata.namespace === this.environment)
        .sort((a, b) => `${ a.metadata.namespace }/${ a.metadata.name }`.localeCompare(`${ b.metadata.namespace }/${ b.metadata.name }`));
    },
    rows() {
      const ids = new Set(this.columns.map((c) => c.id));

      // A template's Fleet object outlives its entry until hyve-controller
      // deletes it; don't show it once the entry's gone.
      const listed = (it) => it.ownerKind !== 'ClusterTemplate' || this.templates.some((t) => t.metadata.name === it.owner &&
        t.metadata.namespace === it.model.metadata.namespace && (t.spec?.resources || []).some((e) => (e.name || e.ref) === it.name));

      return this.items
        .filter(listed)
        .map((it) => ({ it, on: new Set(this.reaches(it).map((c) => c.id)), own: problem(it.model) }))
        .filter((r) => !this.environment || [...r.on].some((id) => ids.has(id)) || r.it.ownerKind === 'ClusterTemplate');
    },
    templateOptions() {
      return this.templates.map((t) => ({ label: `${ t.metadata.namespace }/${ t.metadata.name }`, value: t.id }));
    },
    clusterOptions() {
      return this.columns.map((c) => ({ label: `${ c.metadata.namespace }/${ c.metadata.name }`, value: c.id }));
    },
    targetOptions() {
      return [
        { value: 'template', label: this.t('hyve.matrix.toTemplate') },
        { value: 'cluster', label: this.t('hyve.matrix.toCluster') },
      ];
    },
    // Which clusters the new resource will reach, before it's saved.
    preview() {
      if (this.form.target === 'cluster') {
        const c = this.clusters.find((x) => x.id === this.form.cluster);

        return c ? [c] : [];
      }
      const t = this.templates.find((x) => x.id === this.form.template);

      return t ? this.reaches({ ownerKind: 'ClusterTemplate', owner: t.metadata.name, model: { metadata: { namespace: t.metadata.namespace } } }) : [];
    },
    previewText() {
      return this.preview.map((c) => `${ c.metadata.namespace }/${ c.metadata.name }`).join(', ');
    },
  },

  methods: {
    async load() {
      const [all, states, shared] = await Promise.all([
        allItems(this.$store), allClusterStates(this.$store),
        this.$store.dispatch('management/findAll', { type: HYVE.RESOURCE }).catch(() => []),
      ]);

      this.shared = shared.map((x) => ({ name: x.metadata.name, namespace: x.metadata.namespace }));

      this.items = all.items;
      this.clusters = all.clusters;
      this.templates = all.templates;
      this.clusterResources = all.clusterResources;
      this.reaches = all.reaches;
      this.states = states;
    },
    cell(r, c) {
      if (!r.on.has(c.id)) {
        return '';
      }

      return stateOn(r.it, this.states[`${ c.metadata.namespace }/${ c.metadata.name }`] || null) || this.t('hyve.matrix.waiting');
    },
    stateColor,
    // What's behind a row: the resource as its template or cluster has it
    // (a shared Resource's spec too, when it refers to one), and the Fleet
    // object hyve made from it.
    // A row's resource where it's defined: the template, or the cluster's
    // ClusterResource (whose spec is the entry, less its name).
    owner(r) {
      const it = r.it;
      const ns = it.model.metadata.namespace;

      if (it.ownerKind === 'ClusterTemplate') {
        const tpl = this.templates.find((x) => x.metadata.name === it.owner && x.metadata.namespace === ns);
        const entry = (tpl?.spec?.resources || []).find((e) => (e.name || e.ref) === it.name) || null;

        return { tpl, entry };
      }
      const cr = this.clusterResources.find((x) => x.metadata.name === it.owner && x.metadata.namespace === ns);

      if (!cr) {
        return {};
      }
      const { clusterName, ...entry } = cr.spec || {};

      return { cr, entry: { ...entry, name: it.name }, clusterName };
    },
    // The template as it is now: hyve-controller writes its status, so a
    // listed copy may be stale by the time it's saved.
    // After a change, list again rather than from the store's cache.
    forget() {
      [HYVE.TEMPLATE, HYVE.CLUSTER_RESOURCE, 'fleet.cattle.io.helmop', 'fleet.cattle.io.bundle', 'fleet.cattle.io.gitrepo'].forEach((t) => this.$store.forget?.(t));
    },
    fresh(id) {
      return this.$store.dispatch('management/find', { type: HYVE.TEMPLATE, id, opt: { force: true } });
    },
    async view(r) {
      const it = r.it;
      const ns = it.model.metadata.namespace;
      const sections = [];
      const { tpl, cr, entry } = this.owner(r);

      if (tpl && entry) {
        sections.push({ label: `${ this.t('hyve.view.entry') } (template ${ it.owner })`, object: entry });
      } else if (cr) {
        sections.push({ label: `${ this.t('hyve.view.entry') } (ClusterResource ${ ns }/${ it.owner })`, object: cr });
      }
      if (entry?.ref) {
        try {
          const shared = await this.$store.dispatch('management/find', { type: HYVE.RESOURCE, id: `${ entry.refNamespace || ns }/${ entry.ref }` });

          sections.push({ label: `Resource ${ entry.refNamespace || ns }/${ entry.ref }`, object: shared });
        } catch (e) {}
      }
      sections.push({ label: this.t('hyve.view.delivered', { kind: it.kind }), object: it.model });
      this.viewing = { title: it.name, sections };
    },
    clusterRoute(c) {
      return {
        name:   `${ PRODUCT }-c-cluster-resource-namespace-id`,
        params: {
          product: PRODUCT, cluster: BLANK_CLUSTER, resource: CAPI_CLUSTER, namespace: c.metadata.namespace, id: c.metadata.name
        },
      };
    },
    // An empty cell starts adding that row's resource to that cluster, as
    // an extra.
    startAdd(c, r) {
      const key = this.form.key + 1;

      this.form = { ...emptyForm(), key };
      if (c) {
        this.form.target = 'cluster';
        this.form.cluster = c.id;
      }
      if (r) {
        const { entry } = this.owner(r);

        this.form.entry = entry ? JSON.parse(JSON.stringify(entry)) : null;
      }
      this.errors = [];
      this.adding = true;
    },
    // Edit changes the resource where it's defined.
    startEdit(r) {
      const { tpl, cr, entry, clusterName } = this.owner(r);

      if (!entry) {
        return;
      }
      const key = this.form.key + 1;

      this.form = { ...emptyForm(), key, editing: true, entry: JSON.parse(JSON.stringify(entry)) };
      if (tpl) {
        this.form.template = tpl.id;
      } else {
        this.form.target = 'cluster';
        this.form.cluster = `${ cr.metadata.namespace }/${ clusterName }`;
      }
      this.errors = [];
      this.adding = true;
    },
    async save() {
      this.errors = [];
      let e;

      try {
        e = this.$refs.rform.entry();
      } catch (err) {
        this.errors.push(err.message);

        return;
      }
      const replacing = this.form.editing ? this.form.entry?.name : null;

      this.saving = true;
      try {
        if (this.form.target === 'cluster') {
          const c = this.clusters.find((x) => x.id === this.form.cluster);

          if (!c) {
            throw new Error(this.t('hyve.matrix.pickCluster'));
          }
          // 'hyve cluster resource add' replaces the one of the same name.
          const want = clusterResourceObject(c, e);
          const have = this.clusterResources.find((x) => x.metadata.namespace === c.metadata.namespace && x.spec?.clusterName === c.metadata.name &&
            (x.metadata.labels?.[LABELS.RESOURCE] || x.metadata.name) === (replacing || e.name));

          if (have && (!this.form.editing || replacing === e.name)) {
            have.spec = want.spec;
            await have.save();
          } else {
            const model = await this.$store.dispatch('management/create', want);

            await model.save();
            if (have) {
              await have.remove();
            }
          }
        } else {
          if (!this.form.template) {
            throw new Error(this.t('hyve.matrix.pickTemplate'));
          }
          const t = await this.fresh(this.form.template);
          const list = (t.spec.resources || []).filter((x) => x.name !== replacing);

          if (list.some((x) => x.name === e.name)) {
            throw new Error(this.t('hyve.matrix.exists', { name: e.name, template: t.metadata.name }));
          }
          const at = (t.spec.resources || []).findIndex((x) => x.name === replacing);

          if (at >= 0) {
            list.splice(at, 0, e);
          } else {
            list.push(e);
          }
          t.spec.resources = list;
          await t.save();
        }
        this.adding = false;
        this.forget();
        await this.load();
      } catch (ex) {
        this.errors.push(ex?.message || String(ex));
      } finally {
        this.saving = false;
      }
    },
    async remove(r) {
      this.errors = [];
      try {
        if (r.it.ownerKind === 'ClusterTemplate') {
          const t = await this.fresh(`${ r.it.model.metadata.namespace }/${ r.it.owner }`);

          t.spec.resources = (t.spec.resources || []).filter((x) => x.name !== r.it.name);
          await t.save();
        } else {
          const cr = this.clusterResources.find((x) => x.metadata.name === r.it.owner && x.metadata.namespace === r.it.model.metadata.namespace);

          await cr.remove();
        }
        this.forget();
        await this.load();
      } catch (ex) {
        this.errors.push(ex?.message || String(ex));
      }
    },
  },
};
</script>

<template>
  <Loading v-if="$fetchState.pending" />
  <div v-else>
    <div class="header">
      <h1>{{ t('hyve.matrix.title') }}</h1>
      <div class="header-right">
        <EnvironmentPicker v-model:value="environment" />
        <button
          type="button"
          class="btn role-primary"
          @click="startAdd()"
        >
          {{ t('hyve.matrix.add') }}
        </button>
      </div>
    </div>
    <p class="text-muted mb-20">
      {{ t('hyve.matrix.hint') }}
    </p>
    <Banner
      v-for="(e, i) in errors"
      :key="i"
      color="error"
      :label="e"
    />

    <div
      v-if="adding"
      class="add mb-20"
    >
      <h3>{{ form.editing ? t('hyve.template.editResource', { name: form.entry.name }) : t('hyve.matrix.add') }}</h3>
      <div class="row mb-10">
        <div class="col span-6">
          <RadioGroup
            v-model:value="form.target"
            name="target"
            :options="targetOptions"
            :disabled="form.editing"
          />
        </div>
        <div class="col span-6">
          <LabeledSelect
            v-if="form.target === 'template'"
            v-model:value="form.template"
            :options="templateOptions"
            :label="t('hyve.createCluster.template')"
            :disabled="form.editing"
          />
          <LabeledSelect
            v-else
            v-model:value="form.cluster"
            :options="clusterOptions"
            :label="t('hyve.matrix.cluster')"
            :disabled="form.editing"
          />
        </div>
      </div>
      <ResourceForm
        ref="rform"
        :key="form.key"
        :value="form.entry"
        :shared="shared"
      />
      <Banner
        color="info"
        :label="preview.length ? t('hyve.matrix.willReach', { clusters: previewText }) : t('hyve.matrix.reachesNone')"
      />
      <button
        type="button"
        class="btn role-primary mr-10"
        :disabled="saving"
        @click="save"
      >
        {{ form.editing ? t('generic.save') : t('generic.add') }}
      </button>
      <button
        type="button"
        class="btn role-secondary"
        @click="adding = false"
      >
        {{ t('generic.cancel') }}
      </button>
    </div>

    <div class="matrix-wrap">
      <table class="sortable-table matrix">
        <thead>
          <tr>
            <th>{{ t('hyve.matrix.resource') }}</th>
            <th>{{ t('hyve.resources.from') }}</th>
            <th
              v-for="c in columns"
              :key="c.id"
              class="col-cluster"
            >
              <router-link :to="clusterRoute(c)">
                {{ c.metadata.name }}
              </router-link>
              <div class="text-muted ns">
                {{ c.metadata.namespace }}
              </div>
            </th>
            <th />
          </tr>
        </thead>
        <tbody>
          <tr v-if="!rows.length">
            <td :colspan="columns.length + 3">
              {{ t('hyve.resources.none') }}
            </td>
          </tr>
          <tr
            v-for="r in rows"
            :key="r.it.model.id"
          >
            <td>
              {{ r.it.name }}
              <div class="text-muted small">
                {{ r.it.kind }} {{ r.it.source }}
              </div>
              <div
                v-if="r.own"
                class="text-error small"
              >
                {{ r.own }}
              </div>
            </td>
            <td>{{ r.it.ownerKind === 'ClusterTemplate' ? t('hyve.resources.fromTemplate', { name: r.it.owner }) : t('hyve.resources.fromCluster') }}</td>
            <td
              v-for="c in columns"
              :key="c.id"
              class="col-cluster"
            >
              <span
                v-if="cell(r, c)"
                :class="stateColor(cell(r, c))"
              >{{ cell(r, c) }}</span>
              <a
                v-else
                class="add-cell"
                href="#"
                :title="t('hyve.matrix.addHere')"
                @click.prevent="startAdd(c, r)"
              >+</a>
            </td>
            <td class="text-right">
              <button
                type="button"
                class="btn btn-sm role-link"
                @click="view(r)"
              >
                {{ t('hyve.view.view') }}
              </button>
              <button
                type="button"
                class="btn btn-sm role-link"
                @click="startEdit(r)"
              >
                {{ t('generic.edit') }}
              </button>
              <button
                type="button"
                class="btn btn-sm role-link"
                @click="remove(r)"
              >
                {{ t('generic.remove') }}
              </button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <ViewDialog
      v-if="viewing"
      :title="viewing.title"
      :sections="viewing.sections"
      @close="viewing = null"
    />
  </div>
</template>

<style lang="scss" scoped>
.header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 20px;
  flex-wrap: wrap;
}
.header-right {
  display: flex;
  align-items: center;
  gap: 10px;
}
.add {
  border: 1px solid var(--border);
  border-radius: var(--border-radius);
  padding: 15px;
}
.matrix-wrap {
  overflow-x: auto;
}
.matrix {
  min-width: 100%;
  th, td { padding: 8px; text-align: left; vertical-align: top; white-space: nowrap; }
  .col-cluster { text-align: center; }
  .ns, .small { font-size: 0.85em; }
  .add-cell { opacity: 0.4; }
  .add-cell:hover { opacity: 1; }
}
</style>
