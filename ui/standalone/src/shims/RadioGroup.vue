<script>
export default {
  name:  'RadioGroup',
  props: {
    value:   { type: [String, Number, Boolean], default: '' },
    name:    { type: String, required: true },
    options: { type: Array, default: () => [] },
    mode:     { type: String, default: 'edit' },
    disabled: { type: Boolean, default: false },
  },
  emits: ['update:value'],
};
</script>

<template>
  <div
    class="radio-group"
    role="radiogroup"
  >
    <label
      v-for="o in options"
      :key="String(o.value)"
      class="radio"
    >
      <input
        type="radio"
        :name="name"
        :value="o.value"
        :checked="o.value === value"
        :disabled="disabled || mode === 'view'"
        @change="$emit('update:value', o.value)"
      >
      {{ o.label }}
    </label>
  </div>
</template>
