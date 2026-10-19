<script setup>
defineProps({ label: { type: String, required: true } });
</script>

<template>
  <div class="flex flex-col gap-0.5 border-t border-neutral-100 py-2.5 first:border-t-0 sm:flex-row sm:items-baseline sm:gap-4 dark:border-neutral-800/70">
    <span class="w-full shrink-0 text-xs font-medium tracking-wide text-neutral-500 uppercase sm:w-40">{{ label }}</span>
    <span class="min-w-0 text-sm text-neutral-800 dark:text-neutral-200"><slot /></span>
  </div>
</template>
