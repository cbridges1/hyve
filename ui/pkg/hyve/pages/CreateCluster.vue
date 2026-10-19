<script>
import jsyaml from 'js-yaml';
import Loading from '@shell/components/Loading';
import LabeledSelect from '@shell/components/form/LabeledSelect';
import { LabeledInput } from '@components/Form/LabeledInput';
import { RadioGroup } from '@components/Form/Radio';
import { Banner } from '@components/Banner';
import { BLANK_CLUSTER } from '@shell/store/store-types.js';
import {
  ANNOTATIONS, CAPI_CLUSTER, CAPI_CLUSTER_CLASS, HYVE, LABELS, NAMESPACE, NEVER, PRODUCT
} from '../config/types';
import { nextRun } from '../utils/cron';
import { classOf, providerOfClass } from '../utils/providers';

// Mirrors the template edit view's value encoding.
function toText(v) {
  if (v === null || v === undefined) {
    return '';
  }

  return typeof v === 'string' ? v : JSON.stringify(v);
}

function fromText(s) {
  try {
    return JSON.parse(s);
  } catch (e) {
    return s;
  }
}

function isoSeconds(d) {
  return d.toISOString().replace(/\.\d{3}Z$/, 'Z');
}

// Creates a Cluster labelled with a template. hyve-controller fills in the
// rest from the template and its ClusterClass when it's created, so this
// page sends only what the user chose.
export default {
  name:       'HyveCreateCluster',
  components: {
    Loading, LabeledSelect, LabeledInput, RadioGroup, Banner
  },

  async fetch() {
    const [templates, classes, namespaces] = await Promise.all([
      this.$store.dispatch('management/findAll', { type: HYVE.TEMPLATE }),
      this.$store.dispatch('management/findAll', { type: CAPI_CLUSTER_CLASS }),
      this.$store.dispatch('management/findAll', { type: NAMESPACE }),
    ]);

    this.templates = templates;
    this.classes = classes;
    this.environments = namespaces
      .filter((ns) => ns.metadata?.labels?.[LABELS.ENVIRONMENT] !== undefined)
      .map((ns) => ns.metadata.name)
      .sort();
    this.environment = this.environments[0] || '';
    // From a tile on Rancher's Create Cluster page: only that provider's
    // templates.
    this.provider = this.$route.query.provider || '';
    const q = this.$route.query.template;

    if (q && templates.find((t) => t.id === q)) {
      this.templateId = q;
      this.pickTemplate();
    }
  },

  data() {
    return {
      templates:    [],
      classes:      [],
      environments: [],
      environment:  '',
      provider:     '',
      templateId:   '',
      name:         '',
      version:      '',
      workers:      '',
      rows:         [],
      expiry:       'template',
      hours:        8,
      errors:       [],
      saving:       false,
    };
  },

  computed: {
    templateOptions() {
      return this.templates
        .filter((t) => !this.provider || providerOfClass(classOf(t, this.classes)) === this.provider)
        .map((t) => ({ label: `${ t.metadata.namespace }/${ t.metadata.name }`, value: t.id }));
    },
    template() {
      return this.templates.find((t) => t.id === this.templateId);
    },
    classObj() {
      const t = this.template;

      return t && this.classes.find((c) => c.metadata.namespace === (t.spec.clusterClassRef.namespace || t.metadata.namespace) && c.metadata.name === t.spec.clusterClassRef.name);
    },
    expiryOptions() {
      const opts = [];
      const s = this.template?.spec?.schedule;

      if (s) {
        let when = '?';

        try {
          when = nextRun(s, new Date()).toLocaleString();
        } catch (e) {}
        opts.push({ value: 'template', label: this.t('hyve.createCluster.expiryTemplate', { schedule: s, when }) });
      }
      opts.push({ value: 'hours', label: this.t('hyve.createCluster.expiryHours') });
      opts.push({ value: 'never', label: this.t('hyve.createCluster.expiryNone') });

      return opts;
    },
    // The Cluster as sent: hyve-controller completes it.
    request() {
      const t = this.template;

      if (!t || !this.name || !this.environment) {
        return null;
      }
      const topology = {};
      const vars = this.rows.filter((r) => !r.locked && r.value !== r.initial)
        .map((r) => ({ name: r.name, value: fromText(r.value) }));

      if (vars.length) {
        topology.variables = vars;
      }
      if (this.version) {
        topology.version = this.version;
      }
      if (this.workers !== '' && this.classObj) {
        const spec = this.classObj.spec || {};
        const type = t.spec.workers?.type || ((spec.workers?.machinePools || []).length && !(spec.workers?.machineDeployments || []).length ? 'machinePool' : 'machineDeployment');
        const cls = t.spec.workers?.class || spec.workers?.[`${ type }s`]?.[0]?.class;

        if (cls) {
          topology.workers = { [`${ type }s`]: [{ class: cls, name: type === 'machinePool' ? 'mp-0' : 'md-0', replicas: Number(this.workers) }] };
        }
      }
      const annotations = {};

      if (this.expiry === 'hours') {
        annotations[ANNOTATIONS.EXPIRES_AT] = isoSeconds(new Date(Date.now() + (Number(this.hours) || 0) * 3600 * 1000));
      } else if (this.expiry === 'never') {
        annotations[ANNOTATIONS.EXPIRES_AT] = NEVER;
      }

      return {
        apiVersion: 'cluster.x-k8s.io/v1beta2',
        kind:       'Cluster',
        metadata:   {
          name:      this.name,
          namespace: this.environment,
          labels:    { [LABELS.TEMPLATE]: t.metadata.name, [LABELS.TEMPLATE_NAMESPACE]: t.metadata.namespace },
          annotations,
        },
        spec: { topology },
      };
    },
    preview() {
      return this.request ? jsyaml.dump(this.request) : '';
    },
    canCreate() {
      return !!this.request && /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/.test(this.name) && !this.saving;
    },
  },

  methods: {
    pickTemplate() {
      const spec = this.template?.spec || {};
      const locked = spec.lockedVariables || [];

      this.rows = Object.entries(spec.variables || {}).map(([name, v]) => ({
        name, value: toText(v), initial: toText(v), locked: locked.includes(name)
      }));
      this.version = '';
      this.workers = '';
      this.expiry = spec.schedule ? 'template' : 'never';
    },
    async create() {
      this.errors = [];
      this.saving = true;
      try {
        const model = await this.$store.dispatch('management/create', { type: CAPI_CLUSTER, ...this.request });

        await model.save();
        this.$router.push({
          name:   `${ PRODUCT }-c-cluster-resource`,
          params: {
            product: PRODUCT, cluster: BLANK_CLUSTER, resource: CAPI_CLUSTER
          },
        });
      } catch (e) {
        // The webhook's refusals (a locked variable, a missing class) come
        // back here.
        this.errors.push(e?.message || e?._statusText || String(e));
      } finally {
        this.saving = false;
      }
    },
  },
};
</script>

<template>
  <Loading v-if="$fetchState.pending" />
  <div v-else>
    <h1>{{ t('hyve.createCluster.title') }}</h1>

    <Banner
      v-if="!environments.length"
      color="warning"
      :label="t('hyve.createCluster.noEnvironments')"
    />
    <Banner
      v-if="provider && !templateOptions.length"
      color="warning"
      :label="t('hyve.createCluster.noProviderTemplates', { provider })"
    />

    <div class="row mb-20">
      <div class="col span-4">
        <LabeledSelect
          v-model:value="environment"
          :options="environments"
          :label="t('hyve.createCluster.environment')"
          :required="true"
        />
      </div>
      <div class="col span-4">
        <LabeledSelect
          v-model:value="templateId"
          :options="templateOptions"
          :label="t('hyve.createCluster.template')"
          :required="true"
          @update:value="pickTemplate"
        />
      </div>
      <div class="col span-4">
        <LabeledInput
          v-model:value="name"
          :label="t('hyve.createCluster.name')"
          :required="true"
        />
      </div>
    </div>

    <template v-if="template">
      <p
        v-if="template.spec.description"
        class="mb-20"
      >
        {{ template.spec.description }}
      </p>
      <div class="row mb-20">
        <div class="col span-4">
          <LabeledInput
            v-model:value="version"
            :label="t('hyve.createCluster.version')"
            :placeholder="template.spec.kubernetesVersion || t('hyve.template.versionPlaceholder')"
          />
        </div>
        <div class="col span-4">
          <LabeledInput
            v-model:value="workers"
            type="number"
            min="0"
            :label="t('hyve.createCluster.workers')"
            :placeholder="String(template.spec.workers?.count ?? 1)"
          />
        </div>
      </div>

      <h3 v-if="rows.length">
        {{ t('hyve.template.variables') }}
      </h3>
      <div
        v-for="row in rows"
        :key="row.name"
        class="row mb-10"
      >
        <div class="col span-6">
          <LabeledInput
            v-model:value="row.value"
            :label="row.locked ? t('hyve.createCluster.lockedValue', { value: row.name }) : row.name"
            :disabled="row.locked"
          />
        </div>
      </div>

      <h3>{{ t('hyve.createCluster.expiry') }}</h3>
      <div class="row mb-20">
        <div class="col span-6">
          <RadioGroup
            v-model:value="expiry"
            name="expiry"
            :options="expiryOptions"
          />
        </div>
        <div
          v-if="expiry === 'hours'"
          class="col span-2"
        >
          <LabeledInput
            v-model:value="hours"
            type="number"
            min="1"
            :label="t('hyve.createCluster.hours')"
          />
        </div>
      </div>

      <Banner
        v-if="!classObj"
        color="warning"
        :label="t('hyve.createCluster.noClass', { name: template.spec.clusterClassRef.name })"
      />
      <Banner
        v-for="(e, i) in errors"
        :key="i"
        color="error"
        :label="e"
      />

      <template v-if="preview">
        <h3>{{ t('hyve.createCluster.preview') }}</h3>
        <p class="text-muted">
          {{ t('hyve.createCluster.previewHint') }}
        </p>
        <pre class="preview">{{ preview }}</pre>
      </template>

      <button
        type="button"
        class="btn role-primary mt-20"
        :disabled="!canCreate"
        @click="create"
      >
        {{ t('hyve.createCluster.create') }}
      </button>
    </template>
  </div>
</template>

<style lang="scss" scoped>
.preview {
  max-height: 360px;
  overflow: auto;
  padding: 10px;
  background: var(--body-bg);
  border: 1px solid var(--border);
}
</style>
