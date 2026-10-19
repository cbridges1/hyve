<script>
import LabeledInput from './LabeledInput.vue';
import LabeledSelect from './LabeledSelect.vue';

// Name and namespace: chosen when creating, fixed after.
export default {
  name:       'NameNsDescription',
  components: { LabeledInput, LabeledSelect },
  props:      {
    value:              { type: Object, required: true },
    mode:               { type: String, default: 'create' },
    descriptionHidden:  { type: Boolean, default: false },
    namespaced:         { type: Boolean, default: true },
    registerBeforeHook: { type: Function, default: null },
  },
  async fetch() {
    if (this.mode === 'create' && this.namespaced) {
      const all = await this.$store.dispatch('management/findAll', { type: 'namespace' }).catch(() => []);

      this.namespaces = all.map((n) => n.metadata.name).sort();
    }
  },
  data() {
    return { namespaces: [] };
  },
};
</script>

<template>
  <div class="row mb-20">
    <div class="col span-6">
      <LabeledInput
        v-model:value="value.metadata.name"
        :mode="mode === 'create' ? 'edit' : 'view'"
        :label="t('tableHeaders.name')"
        :required="true"
      />
    </div>
    <div
      v-if="namespaced"
      class="col span-6"
    >
      <LabeledSelect
        v-model:value="value.metadata.namespace"
        :mode="mode === 'create' ? 'edit' : 'view'"
        :options="namespaces"
        :taggable="true"
        :label="t('tableHeaders.namespace')"
        :required="true"
      />
    </div>
  </div>
</template>
