<script>
export default {
  name:  'Banner',
  props: {
    color: { type: String, default: 'info' },
    label: { type: String, default: '' },
  },
};
</script>

<template>
  <div
    class="banner"
    :class="`banner-${ color }`"
    role="status"
  >
    <slot>{{ label }}</slot>
  </div>
</template>
