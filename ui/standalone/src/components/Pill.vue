<script setup>
// The console's status pills.
const TONES = {
  good: 'bg-green-100 text-green-800 dark:bg-green-950 dark:text-green-300',
  busy: 'bg-amber-100 text-amber-800 dark:bg-amber-950 dark:text-amber-300',
  bad:  'bg-red-100 text-red-800 dark:bg-red-950 dark:text-red-300',
  idle: 'bg-neutral-100 text-neutral-600 dark:bg-neutral-800 dark:text-neutral-400',
  env:  'bg-sky-100 text-sky-800 dark:bg-sky-950 dark:text-sky-300',
  info: 'bg-indigo-100 text-indigo-800 dark:bg-indigo-950 dark:text-indigo-300',
};

defineProps({
  tone:  { type: String, default: 'idle' },
  label: { type: String, required: true },
  title: { type: String, default: '' },
});
</script>

<template>
  <span
    :title="title || undefined"
    :class="['inline-block rounded px-2 py-0.5 text-xs font-medium whitespace-nowrap', TONES[tone] || TONES.idle]"
  >{{ label }}</span>
</template>
