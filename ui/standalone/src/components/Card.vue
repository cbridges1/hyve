<script setup>
defineProps({ title: { type: String, default: '' } });
</script>

<template>
  <div class="rounded-xl border border-neutral-200 bg-white p-4 shadow-sm sm:p-5 dark:border-neutral-800 dark:bg-neutral-900">
    <div
      v-if="title || $slots.action"
      class="mb-3 flex items-center justify-between gap-3"
    >
      <h2
        v-if="title"
        class="mb-0"
      >
        {{ title }}
      </h2>
      <slot name="action" />
    </div>
    <slot />
  </div>
</template>
