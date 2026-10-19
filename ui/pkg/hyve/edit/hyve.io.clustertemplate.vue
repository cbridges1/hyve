<script>
import CreateEditView from '@shell/mixins/create-edit-view';
import CruResource from '@shell/components/CruResource';
import NameNsDescription from '@shell/components/form/NameNsDescription';
import LabeledSelect from '@shell/components/form/LabeledSelect';
import KeyValue from '@shell/components/form/KeyValue';
import { LabeledInput } from '@components/Form/LabeledInput';
import { Checkbox } from '@components/Form/Checkbox';
import { Banner } from '@components/Banner';
import { _VIEW } from '@shell/config/query-params';
import { CAPI_CLUSTER_CLASS, DEFAULT_TEMPLATE_NAMESPACE } from '../config/types';
import { nextRun } from '../utils/cron';
import { describe, kindOf, validate } from '../utils/resources';

// A variable's value as text: JSON for anything but plain strings.
function toText(v) {
  if (v === null || v === undefined) {
    return '';
  }

  return typeof v === 'string' ? v : JSON.stringify(v);
}

// Text back to a value: JSON when it parses, otherwise the string.
function fromText(s) {
  if (s === '') {
    return null;
  }
  try {
    return JSON.parse(s);
  } catch (e) {
    return s;
  }
}

export default {
  name:         'CruClusterTemplate',
  inheritAttrs: false,
  components:   {
    CruResource, NameNsDescription, LabeledSelect, LabeledInput, Checkbox, KeyValue, Banner
  },
  mixins: [CreateEditView],

  async fetch() {
    this.classes = await this.$store.dispatch('management/findAll', { type: CAPI_CLUSTER_CLASS });
  },

  data() {
    if (!this.value.metadata.namespace) {
      this.value.metadata.namespace = DEFAULT_TEMPLATE_NAMESPACE;
    }
    const spec = this.value.spec || {};

    spec.clusterClassRef = spec.clusterClassRef || { name: '' };
    this.value.spec = spec;
    const locked = spec.lockedVariables || [];

    return {
      classes:      [],
      controlPlane: spec.controlPlane?.replicas ?? 1,
      workers:      spec.workers?.count ?? 1,
      rows:         Object.entries(spec.variables || {}).map(([name, v]) => ({
        name, value: toText(v), locked: locked.includes(name)
      })),
      labels:    { ...(spec.labels || {}) },
      // Each entry is Fleet's own spec; the form edits the common fields
      // and keeps the rest (Helm values, options) as they are.
      resources: (spec.resources || []).map((r) => ({
        name:      r.name,
        namespace: r.namespace || '',
        type:      ({
          HelmOp: 'helm', GitRepo: 'git', Ref: 'ref', Bundle: 'bundle'
        })[kindOf(r)] || 'helm',
        repo:      r.helmOp?.helm?.repo || r.gitRepo?.repo || r.ref || '',
        chart:     r.helmOp?.helm?.chart || '',
        version:   r.helmOp?.helm?.version || r.gitRepo?.branch || '',
        paths:     (r.gitRepo?.paths || []).join(','),
        secrets:   (r.secrets || []).join(','),
        orig:      r,
      })),
    };
  },

  computed: {
    isView() {
      return this.mode === _VIEW;
    },
    classOptions() {
      return this.classes
        .filter((c) => c.metadata.namespace === this.value.metadata.namespace)
        .map((c) => c.metadata.name);
    },
    selectedClass() {
      return this.classes.find((c) => c.metadata.namespace === this.value.metadata.namespace && c.metadata.name === this.value.spec.clusterClassRef.name);
    },
    scheduleError() {
      if (!this.value.spec.schedule) {
        return '';
      }
      try {
        return `Next run: ${ nextRun(this.value.spec.schedule, new Date()).toLocaleString() }`;
      } catch (e) {
        return e.message;
      }
    },
    validationPassed() {
      return !!this.value.metadata.name && !!this.value.spec.clusterClassRef.name && !this.scheduleError.startsWith('schedule');
    },
  },

  methods: {
    loadDefaults() {
      const have = new Set(this.rows.map((r) => r.name));

      (this.selectedClass?.spec?.variables || []).forEach((v) => {
        if (!have.has(v.name)) {
          this.rows.push({
            name: v.name, value: toText(v.schema?.openAPIV3Schema?.default), locked: false
          });
        }
      });
    },
    addResource(type) {
      this.resources.push({
        name: '', namespace: '', type, repo: '', chart: '', version: '', paths: '', secrets: '', orig: null
      });
    },
    removeResource(i) {
      this.resources.splice(i, 1);
    },
    describe,
    // Back to entries: Fleet's specs with the form's fields set, everything
    // else kept from the original.
    resourceSources() {
      return this.resources.filter((r) => r.name || r.chart || r.repo).map((r) => {
        if (r.type === 'bundle') {
          return r.orig;
        }
        const orig = JSON.parse(JSON.stringify(r.orig || {}));
        const out = { name: r.name || r.chart || r.repo };

        if (r.namespace) {
          out.namespace = r.namespace;
        }
        const secrets = r.secrets.split(',').map((x) => x.trim()).filter(Boolean);

        if (secrets.length) {
          out.secrets = secrets;
        }
        if (r.type === 'helm') {
          const helmOp = orig.helmOp || {};
          const helm = { ...(helmOp.helm || {}) };

          ['repo', 'chart', 'version'].forEach((k) => {
            if (r[k]) {
              helm[k] = r[k];
            } else {
              delete helm[k];
            }
          });
          out.helmOp = { ...helmOp, helm };
        } else if (r.type === 'git') {
          const g = { ...(orig.gitRepo || {}), repo: r.repo };

          if (r.version) {
            g.branch = r.version;
          } else {
            delete g.branch;
          }
          const paths = r.paths.split(',').map((p) => p.trim()).filter(Boolean);

          if (paths.length) {
            g.paths = paths;
          } else {
            delete g.paths;
          }
          out.gitRepo = g;
        } else if (r.type === 'ref') {
          out.ref = r.repo;
          if (orig.refNamespace) {
            out.refNamespace = orig.refNamespace;
          }
          if (orig.values) {
            out.values = orig.values;
          }
        }

        return out;
      });
    },
    addRow() {
      this.rows.push({
        name: '', value: '', locked: false
      });
    },
    removeRow(i) {
      this.rows.splice(i, 1);
    },
    syncSpec() {
      const spec = this.value.spec;
      const variables = {};

      this.rows.filter((r) => r.name).forEach((r) => {
        variables[r.name] = fromText(r.value);
      });
      spec.variables = variables;
      spec.lockedVariables = this.rows.filter((r) => r.name && r.locked).map((r) => r.name);
      spec.controlPlane = { replicas: Number(this.controlPlane) || 1 };
      spec.workers = { ...(spec.workers || {}), count: Number(this.workers) };
      spec.labels = Object.keys(this.labels).length ? this.labels : undefined;
      ['kubernetesVersion', 'schedule', 'description'].forEach((k) => {
        if (!spec[k]) {
          delete spec[k];
        }
      });
      if (!spec.lockedVariables.length) {
        delete spec.lockedVariables;
      }
      const resources = this.resourceSources();

      spec.resources = resources.length ? resources : undefined;
    },
    async save(buttonDone) {
      this.errors = [];
      this.syncSpec();
      const bad = (this.value.spec.resources || []).map(validate).filter(Boolean);

      if (bad.length) {
        this.errors = bad;
        buttonDone(false);

        return;
      }
      try {
        // hyve-controller turns the resources into Fleet objects.
        await this.value.save();
        buttonDone(true);
        this.done();
      } catch (e) {
        this.errors = [e?.message || String(e)];
        buttonDone(false);
      }
    },
  },
};
</script>

<template>
  <CruResource
    :done-route="doneRoute"
    :mode="mode"
    :resource="value"
    :validation-passed="validationPassed"
    :errors="errors"
    @error="e=>errors = e"
    @finish="save"
    @cancel="done"
  >
    <NameNsDescription
      :value="value"
      :mode="mode"
      :description-hidden="true"
      :register-before-hook="registerBeforeHook"
    />

    <div class="row mb-20">
      <div class="col span-12">
        <LabeledInput
          v-model:value="value.spec.description"
          :mode="mode"
          :label="t('hyve.template.description')"
        />
      </div>
    </div>

    <div class="row mb-20">
      <div class="col span-4">
        <LabeledSelect
          v-model:value="value.spec.clusterClassRef.name"
          :mode="mode"
          :options="classOptions"
          :label="t('hyve.template.clusterClass')"
          :tooltip="t('hyve.template.clusterClassHint')"
          :required="true"
          :taggable="true"
        />
      </div>
      <div class="col span-4">
        <LabeledInput
          v-model:value="value.spec.kubernetesVersion"
          :mode="mode"
          :label="t('hyve.template.version')"
          :placeholder="t('hyve.template.versionPlaceholder')"
        />
      </div>
      <div class="col span-2">
        <LabeledInput
          v-model:value="controlPlane"
          type="number"
          min="1"
          :mode="mode"
          :label="t('hyve.template.controlPlane')"
        />
      </div>
      <div class="col span-2">
        <LabeledInput
          v-model:value="workers"
          type="number"
          min="0"
          :mode="mode"
          :label="t('hyve.template.workers')"
        />
      </div>
    </div>

    <div class="row mb-20">
      <div class="col span-6">
        <LabeledInput
          v-model:value="value.spec.schedule"
          :mode="mode"
          :label="t('hyve.template.schedule')"
          :placeholder="t('hyve.template.schedulePlaceholder')"
          :tooltip="t('hyve.template.scheduleHint')"
          :sub-label="scheduleError"
        />
      </div>
    </div>

    <h3>{{ t('hyve.template.variables') }}</h3>
    <Banner
      color="info"
      :label="t('hyve.template.variablesHint') + ' ' + t('hyve.template.lockedHint')"
    />
    <div
      v-for="(row, i) in rows"
      :key="i"
      class="row mb-10 var-row"
    >
      <div class="col span-3">
        <LabeledInput
          v-model:value="row.name"
          :mode="mode"
          label="Name"
        />
      </div>
      <div class="col span-6">
        <LabeledInput
          v-model:value="row.value"
          :mode="mode"
          label="Value"
        />
      </div>
      <div class="col span-2 locked">
        <Checkbox
          v-model:value="row.locked"
          :mode="mode"
          :label="t('hyve.template.locked')"
        />
      </div>
      <div
        v-if="!isView"
        class="col span-1"
      >
        <button
          type="button"
          class="btn role-link"
          @click="removeRow(i)"
        >
          {{ t('generic.remove') }}
        </button>
      </div>
    </div>
    <div
      v-if="!isView"
      class="mb-20"
    >
      <button
        type="button"
        class="btn role-tertiary mr-10"
        @click="addRow"
      >
        {{ t('generic.add') }}
      </button>
      <button
        type="button"
        class="btn role-tertiary"
        :disabled="!selectedClass"
        @click="loadDefaults"
      >
        {{ t('hyve.template.loadDefaults') }}
      </button>
    </div>

    <h3>{{ t('hyve.template.resources') }}</h3>
    <Banner
      color="info"
      :label="t('hyve.template.resourcesHint')"
    />
    <div
      v-for="(r, i) in resources"
      :key="i"
      class="row mb-10 var-row"
    >
      <template v-if="r.type === 'bundle'">
        <div class="col span-11">
          <b>{{ r.name }}</b> — {{ t('hyve.template.bundleRow', { source: describe(r.orig) }) }}
        </div>
      </template>
      <template v-else-if="r.type === 'ref'">
        <div class="col span-3">
          <LabeledInput
            v-model:value="r.name"
            :mode="mode"
            :label="t('hyve.resources.name')"
            :placeholder="r.repo"
          />
        </div>
        <div class="col span-3">
          <LabeledInput
            v-model:value="r.repo"
            :mode="mode"
            :label="t('hyve.resources.ref')"
          />
        </div>
        <div class="col span-5">
          <LabeledInput
            v-model:value="r.secrets"
            :mode="mode"
            :label="t('hyve.resources.secrets')"
          />
        </div>
      </template>
      <template v-else>
        <div class="col span-2">
          <LabeledInput
            v-model:value="r.name"
            :mode="mode"
            :label="t('hyve.resources.name')"
            :placeholder="r.chart"
          />
        </div>
        <div class="col span-4">
          <LabeledInput
            v-model:value="r.repo"
            :mode="mode"
            :label="r.type === 'helm' ? t('hyve.resources.repo') : t('hyve.resources.gitRepo')"
          />
        </div>
        <div
          v-if="r.type === 'helm'"
          class="col span-2"
        >
          <LabeledInput
            v-model:value="r.chart"
            :mode="mode"
            :label="t('hyve.resources.chart')"
          />
        </div>
        <div
          v-else
          class="col span-2"
        >
          <LabeledInput
            v-model:value="r.paths"
            :mode="mode"
            :label="t('hyve.resources.paths')"
          />
        </div>
        <div class="col span-1">
          <LabeledInput
            v-model:value="r.version"
            :mode="mode"
            :label="r.type === 'helm' ? t('hyve.resources.version') : t('hyve.resources.branch')"
          />
        </div>
        <div class="col span-2">
          <LabeledInput
            v-model:value="r.namespace"
            :mode="mode"
            :label="t('hyve.resources.namespace')"
          />
        </div>
        <div
          v-if="r.type === 'helm'"
          class="col span-12 mt-10"
        >
          <LabeledInput
            v-model:value="r.secrets"
            :mode="mode"
            :label="t('hyve.resources.secrets')"
            :placeholder="t('hyve.resources.secretsPlaceholder')"
          />
        </div>
      </template>
      <div
        v-if="!isView"
        class="col span-1"
      >
        <button
          type="button"
          class="btn role-link"
          @click="removeResource(i)"
        >
          {{ t('generic.remove') }}
        </button>
      </div>
    </div>
    <div
      v-if="!isView"
      class="mb-20"
    >
      <button
        type="button"
        class="btn role-tertiary mr-10"
        @click="addResource('helm')"
      >
        {{ t('hyve.resources.addHelm') }}
      </button>
      <button
        type="button"
        class="btn role-tertiary mr-10"
        @click="addResource('git')"
      >
        {{ t('hyve.resources.addGit') }}
      </button>
      <button
        type="button"
        class="btn role-tertiary"
        @click="addResource('ref')"
      >
        {{ t('hyve.resources.addRef') }}
      </button>
    </div>

    <h3>{{ t('hyve.template.labels') }}</h3>
    <KeyValue
      v-model:value="labels"
      :mode="mode"
      :protip="t('hyve.template.labelsHint')"
      :add-label="t('generic.add')"
      :read-allowed="false"
    />
  </CruResource>
</template>

<style lang="scss" scoped>
.var-row {
  align-items: center;
}
.locked {
  display: flex;
  align-items: center;
}
</style>
