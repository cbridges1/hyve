<script setup>
import Icon from './Icon.vue';

defineProps({
  to:    { type: [String, Object], required: true },
  label: { type: String, required: true },
});
</script>

<template>
  <router-link
    :to="to"
    class="mb-3 inline-flex items-center gap-1 text-sm font-medium text-neutral-500 no-underline transition-colors hover:text-neutral-900 hover:no-underline dark:text-neutral-400 dark:hover:text-neutral-100"
  >
    <span class="rotate-180"><Icon
      name="chevronRight"
      :size="14"
    /></span>
    {{ label }}
  </router-link>
</template>
