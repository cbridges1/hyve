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
import { CAPI_CLUSTER_CLASS, DEFAULT_TEMPLATE_NAMESPACE, HYVE } from '../config/types';
import { nextRun } from '../utils/cron';
import { describe, kindOf, validate } from '../utils/resources';
import ViewDialog from '../components/ViewDialog.vue';
import ResourceForm from '../components/ResourceForm.vue';

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
    CruResource, NameNsDescription, LabeledSelect, LabeledInput, Checkbox, KeyValue, Banner, ViewDialog, ResourceForm
  },
  mixins: [CreateEditView],

  async fetch() {
    const [classes, shared] = await Promise.all([
      this.$store.dispatch('management/findAll', { type: CAPI_CLUSTER_CLASS }),
      this.$store.dispatch('management/findAll', { type: HYVE.RESOURCE }).catch(() => []),
    ]);

    this.classes = classes;
    this.shared = shared.map((r) => ({ name: r.metadata.name, namespace: r.metadata.namespace }));
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
      viewing:      null,
      classes:      [],
      controlPlane: spec.controlPlane?.replicas ?? 1,
      workers:      spec.workers?.count ?? 1,
      rows:         Object.entries(spec.variables || {}).map(([name, v]) => ({
        name, value: toText(v), locked: locked.includes(name)
      })),
      labels:    { ...(spec.labels || {}) },
      // Each entry is Fleet's own spec (or a ref), edited whole in the
      // resource form.
      resources: JSON.parse(JSON.stringify(spec.resources || [])),
      shared:    [],
      // The entry being edited: its index, -1 for a new one, null for none.
      editing:   null,
      formError: '',
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
    startEdit(i) {
      this.formError = '';
      this.editing = i;
    },
    saveEntry() {
      this.formError = '';
      try {
        const e = this.$refs.rform.entry();
        const clash = this.resources.findIndex((r, j) => r.name === e.name && j !== this.editing);

        if (clash >= 0) {
          throw new Error(this.t('hyve.template.dupResource', { name: e.name }));
        }
        if (this.editing < 0) {
          this.resources.push(e);
        } else {
          this.resources.splice(this.editing, 1, e);
        }
        this.editing = null;
      } catch (err) {
        this.formError = err.message;
      }
    },
    removeResource(i) {
      this.resources.splice(i, 1);
    },
    describe,
    kindOf,
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
      spec.resources = this.resources.length ? JSON.parse(JSON.stringify(this.resources)) : undefined;
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
    <table
      v-if="resources.length"
      class="sortable-table hyve-rtable mb-10"
    >
      <thead>
        <tr>
          <th>{{ t('hyve.resources.name') }}</th>
          <th>{{ t('hyve.form.kindLabel') }}</th>
          <th>{{ t('hyve.resources.source') }}</th>
          <th>{{ t('hyve.resources.namespace') }}</th>
          <th>{{ t('hyve.resources.secrets') }}</th>
          <th />
        </tr>
      </thead>
      <tbody>
        <tr
          v-for="(r, i) in resources"
          :key="r.name || i"
        >
          <td>{{ r.name }}</td>
          <td>{{ kindOf(r) }}</td>
          <td class="hyve-rsource">
            {{ describe(r) }}
          </td>
          <td>{{ r.namespace || '-' }}</td>
          <td>{{ (r.secrets || []).join(', ') || '-' }}</td>
          <td class="text-right hyve-ractions">
            <button
              type="button"
              class="btn btn-sm role-link"
              @click="viewing = r"
            >
              {{ t('hyve.view.view') }}
            </button>
            <template v-if="!isView">
              <button
                type="button"
                class="btn btn-sm role-link"
                @click="startEdit(i)"
              >
                {{ t('generic.edit') }}
              </button>
              <button
                type="button"
                class="btn btn-sm role-link"
                @click="removeResource(i)"
              >
                {{ t('generic.remove') }}
              </button>
            </template>
          </td>
        </tr>
      </tbody>
    </table>
    <div
      v-if="editing !== null"
      class="add mb-20"
    >
      <h3 class="hyve-rform-title">
        {{ editing < 0 ? t('hyve.template.newResource') : t('hyve.template.editResource', { name: resources[editing].name }) }}
      </h3>
      <ResourceForm
        ref="rform"
        :key="editing"
        :value="editing < 0 ? null : resources[editing]"
        :shared="shared"
      />
      <Banner
        v-if="formError"
        color="error"
        :label="formError"
      />
      <button
        type="button"
        class="btn role-primary mr-10"
        @click="saveEntry"
      >
        {{ editing < 0 ? t('generic.add') : t('hyve.template.applyResource') }}
      </button>
      <button
        type="button"
        class="btn role-secondary"
        @click="editing = null"
      >
        {{ t('generic.cancel') }}
      </button>
    </div>
    <div
      v-else-if="!isView"
      class="mb-20"
    >
      <button
        type="button"
        class="btn role-tertiary"
        @click="startEdit(-1)"
      >
        {{ t('hyve.template.addResource') }}
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
    <ViewDialog
      v-if="viewing"
      :title="viewing.name"
      :sections="[{ label: t('hyve.view.entry'), object: viewing }]"
      @close="viewing = null"
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
.hyve-rtable {
  width: 100%;
  th, td { padding: 8px; text-align: left; vertical-align: top; }
}
.hyve-rsource { word-break: break-all; }
.hyve-ractions { white-space: nowrap; }
.hyve-rform-title { margin-top: 0; }
</style>
