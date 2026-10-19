<script>
let n = 0;

export default {
  name:         'LabeledInput',
  inheritAttrs: false,
  props:        {
    value:       { type: [String, Number], default: '' },
    label:       { type: String, default: '' },
    placeholder: { type: String, default: '' },
    type:        { type: String, default: 'text' },
    required:    { type: Boolean, default: false },
    disabled:    { type: Boolean, default: false },
    mode:        { type: String, default: 'edit' },
    tooltip:     { type: String, default: '' },
    subLabel:    { type: String, default: '' },
  },
  emits: ['update:value'],
  data() {
    return { uid: `li-${ ++n }` };
  },
  computed: {
    isView() {
      return this.mode === 'view';
    },
  },
};
</script>

<template>
  <div class="labeled">
    <label :for="uid">
      {{ label }}<span
        v-if="required && mode !== 'view'"
        class="required"
      >*</span>
      <span
        v-if="tooltip"
        class="hint"
        :title="tooltip"
      >?</span>
    </label>
    <div
      v-if="isView"
      class="view-value"
    >
      {{ value === '' || value === null || value === undefined ? '—' : value }}
    </div>
    <textarea
      v-else-if="type === 'multiline'"
      :id="uid"
      :value="value"
      :placeholder="placeholder"
      :disabled="disabled"
      v-bind="$attrs"
      @input="$emit('update:value', $event.target.value)"
    />
    <input
      v-else
      :id="uid"
      :type="type"
      :value="value"
      :placeholder="placeholder"
      :disabled="disabled"
      :required="required"
      v-bind="$attrs"
      @input="$emit('update:value', $event.target.value)"
    >
    <div
      v-if="subLabel"
      class="sub-label"
    >
      {{ subLabel }}
    </div>
  </div>
</template>
