<script>
import CreateEditView from '@shell/mixins/create-edit-view';
import CruResource from '@shell/components/CruResource';
import NameNsDescription from '@shell/components/form/NameNsDescription';
import { Banner } from '@components/Banner';
import { _VIEW } from '@shell/config/query-params';
import { DEFAULT_TEMPLATE_NAMESPACE } from '../config/types';
import { describe, kindOf } from '../utils/resources';
import ResourceForm from '../components/ResourceForm.vue';
import ViewDialog from '../components/ViewDialog.vue';

// A shared Resource: one Fleet spec that templates and clusters refer to
// by name, with the same form as their own resources ('hyve resource
// create NAME --helm/--git/--path').
export default {
  name:         'CruSharedResource',
  inheritAttrs: false,
  components:   {
    CruResource, NameNsDescription, Banner, ResourceForm, ViewDialog
  },
  mixins: [CreateEditView],

  data() {
    if (!this.value.metadata.namespace) {
      this.value.metadata.namespace = DEFAULT_TEMPLATE_NAMESPACE;
    }
    if (!this.value.spec) {
      this.value.spec = {};
    }

    return { viewing: false };
  },

  computed: {
    isView() {
      return this.mode === _VIEW;
    },
    entry() {
      return Object.keys(this.value.spec || {}).length ? { ...this.value.spec, name: this.value.metadata.name } : null;
    },
  },

  methods: {
    describe,
    kindOf,
    async save(buttonDone) {
      this.errors = [];
      try {
        const e = this.$refs.rform.entry();

        delete e.name;
        this.value.spec = e;
        await this.value.save();
        buttonDone(true);
        this.done();
      } catch (err) {
        this.errors = [err?.message || String(err)];
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
    :validation-passed="!!value.metadata.name"
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
    <Banner
      color="info"
      :label="t('hyve.sharedResource.hint')"
    />
    <div
      v-if="isView"
      class="mb-20"
    >
      <table class="kv">
        <tbody>
          <tr>
            <th>{{ t('hyve.form.kindLabel') }}</th>
            <td>{{ entry ? kindOf(entry) : '-' }}</td>
          </tr>
          <tr>
            <th>{{ t('hyve.resources.source') }}</th>
            <td>{{ entry ? describe(entry) : '-' }}</td>
          </tr>
          <tr>
            <th>{{ t('hyve.resources.namespace') }}</th>
            <td>{{ value.spec.namespace || '-' }}</td>
          </tr>
          <tr>
            <th>{{ t('hyve.resources.secrets') }}</th>
            <td>{{ (value.spec.secrets || []).join(', ') || '-' }}</td>
          </tr>
        </tbody>
      </table>
      <button
        type="button"
        class="btn btn-sm role-link mt-10"
        @click="viewing = true"
      >
        {{ t('hyve.view.view') }}
      </button>
    </div>
    <ResourceForm
      v-else
      ref="rform"
      :value="entry"
      :allow-ref="false"
      :no-name="true"
    />
    <ViewDialog
      v-if="viewing"
      :title="value.metadata.name"
      :sections="[{ label: 'Resource', object: value }]"
      @close="viewing = false"
    />
  </CruResource>
</template>
