<script>
export default {
  name:  'Checkbox',
  props: {
    value:    { type: Boolean, default: false },
    label:    { type: String, default: '' },
    mode:     { type: String, default: 'edit' },
    disabled: { type: Boolean, default: false },
  },
  emits: ['update:value'],
};
</script>

<template>
  <label class="checkbox">
    <input
      type="checkbox"
      :checked="value"
      :disabled="disabled || mode === 'view'"
      @change="$emit('update:value', $event.target.checked)"
    >
    {{ label }}
  </label>
</template>
