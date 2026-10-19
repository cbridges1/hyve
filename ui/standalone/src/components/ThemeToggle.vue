<script setup>
import Icon from './Icon.vue';
import { setTheme, theme } from '../theme';

const options = [
  { value: 'light', label: 'Light theme', icon: 'sun' },
  { value: 'dark', label: 'Dark theme', icon: 'moon' },
  { value: 'system', label: 'Match system theme', icon: 'system' },
];
</script>

<template>
  <div
    class="flex gap-1 rounded-full bg-neutral-100 p-1 dark:bg-neutral-900"
    role="radiogroup"
    aria-label="Theme"
  >
    <button
      v-for="o in options"
      :key="o.value"
      type="button"
      role="radio"
      :aria-checked="theme === o.value"
      :title="o.label"
      :class="['flex items-center justify-center rounded-full p-2 transition-colors', theme === o.value ? 'bg-white text-neutral-900 shadow-sm dark:bg-neutral-700 dark:text-neutral-100' : 'text-neutral-500 hover:text-neutral-700 dark:hover:text-neutral-300']"
      @click="setTheme(o.value)"
    >
      <Icon
        :name="o.icon"
        :size="16"
      />
    </button>
  </div>
</template>
