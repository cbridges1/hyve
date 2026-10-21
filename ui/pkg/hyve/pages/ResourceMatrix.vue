<script>
import Loading from '@shell/components/Loading';
import LabeledSelect from '@shell/components/form/LabeledSelect';
import { LabeledInput } from '@components/Form/LabeledInput';
import { RadioGroup } from '@components/Form/Radio';
import { Banner } from '@components/Banner';
import { BLANK_CLUSTER } from '@shell/store/store-types.js';
import { CAPI_CLUSTER, HYVE, PRODUCT } from '../config/types';
import {
  allClusterStates, allItems, clusterResourceObject, helmEntry, problem, stateColor, stateOn, validate
} from '../utils/resources';
import EnvironmentPicker from '../components/EnvironmentPicker.vue';
import ViewDialog from '../components/ViewDialog.vue';

function emptyForm() {
  return {
    name: '', repo: '', chart: '', version: '', namespace: '', secrets: '', target: 'template', template: '', cluster: ''
  };
}

// Resources down the side, clusters across the top: each cell is Fleet's
// state for that resource on that cluster, and why it's there (the
// template, or an extra on the cluster). Adding asks whether a resource
// goes on a template (every cluster made from it) or one cluster.
export default {
  name:       'HyveResourceMatrix',
  components: {
    Loading, LabeledSelect, LabeledInput, RadioGroup, Banner, EnvironmentPicker, ViewDialog
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

      return this.items
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
    entry() {
      return helmEntry({
        name:      this.form.name || undefined,
        repo:      this.form.repo,
        chart:     this.form.chart,
        version:   this.form.version,
        namespace: this.form.namespace,
        secrets:   this.form.secrets.split(',').map((s) => s.trim()).filter(Boolean),
      });
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
      const [all, states] = await Promise.all([allItems(this.$store), allClusterStates(this.$store)]);

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
    async view(r) {
      const it = r.it;
      const ns = it.model.metadata.namespace;
      const sections = [];
      let entry = null;

      if (it.ownerKind === 'ClusterTemplate') {
        const tpl = this.templates.find((x) => x.metadata.name === it.owner && x.metadata.namespace === ns);

        entry = (tpl?.spec?.resources || []).find((e) => (e.name || e.ref) === it.name) || null;
        if (entry) {
          sections.push({ label: `${ this.t('hyve.view.entry') } (template ${ it.owner })`, object: entry });
        }
      } else {
        const cr = this.clusterResources.find((x) => x.metadata.name === it.owner && x.metadata.namespace === ns);

        if (cr) {
          entry = cr.spec || null;
          sections.push({ label: `${ this.t('hyve.view.entry') } (ClusterResource ${ ns }/${ it.owner })`, object: cr });
        }
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
      this.form = emptyForm();
      if (c) {
        this.form.target = 'cluster';
        this.form.cluster = c.id;
      }
      const helm = r?.it?.kind === 'HelmOp' ? r.it.model.spec?.helm || {} : null;

      if (helm) {
        this.form.name = r.it.name;
        this.form.repo = helm.repo || '';
        this.form.chart = helm.chart || '';
        this.form.version = helm.version || '';
        this.form.namespace = r.it.model.spec?.namespace || '';
      }
      this.errors = [];
      this.adding = true;
    },
    async save() {
      this.errors = [];
      const e = this.entry;
      const err = validate(e);

      if (err) {
        this.errors.push(err);

        return;
      }
      this.saving = true;
      try {
        if (this.form.target === 'cluster') {
          const c = this.clusters.find((x) => x.id === this.form.cluster);

          if (!c) {
            throw new Error(this.t('hyve.matrix.pickCluster'));
          }
          const model = await this.$store.dispatch('management/create', clusterResourceObject(c, e));

          await model.save();
        } else {
          const t = this.templates.find((x) => x.id === this.form.template);

          if (!t) {
            throw new Error(this.t('hyve.matrix.pickTemplate'));
          }
          if ((t.spec.resources || []).some((x) => x.name === e.name)) {
            throw new Error(this.t('hyve.matrix.exists', { name: e.name, template: t.metadata.name }));
          }
          t.spec.resources = [...(t.spec.resources || []), e];
          await t.save();
        }
        this.adding = false;
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
          const t = this.templates.find((x) => x.metadata.name === r.it.owner && x.metadata.namespace === r.it.model.metadata.namespace);

          t.spec.resources = (t.spec.resources || []).filter((x) => x.name !== r.it.name);
          await t.save();
        } else {
          const cr = this.clusterResources.find((x) => x.metadata.name === r.it.owner && x.metadata.namespace === r.it.model.metadata.namespace);

          await cr.remove();
        }
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
      <h3>{{ t('hyve.resources.addHelm') }}</h3>
      <div class="row mb-10">
        <div class="col span-6">
          <RadioGroup
            v-model:value="form.target"
            name="target"
            :options="targetOptions"
          />
        </div>
        <div class="col span-6">
          <LabeledSelect
            v-if="form.target === 'template'"
            v-model:value="form.template"
            :options="templateOptions"
            :label="t('hyve.createCluster.template')"
          />
          <LabeledSelect
            v-else
            v-model:value="form.cluster"
            :options="clusterOptions"
            :label="t('hyve.matrix.cluster')"
          />
        </div>
      </div>
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
