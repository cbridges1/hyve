<script setup>
import { onBeforeUnmount, onMounted } from 'vue';

defineProps({ title: { type: String, required: true } });
const emit = defineEmits(['close']);
const onKey = (e) => e.key === 'Escape' && emit('close');

onMounted(() => window.addEventListener('keydown', onKey));
onBeforeUnmount(() => window.removeEventListener('keydown', onKey));
</script>

<template>
  <Teleport to="body">
    <div class="fixed inset-0 z-50 flex items-start justify-center overflow-y-auto p-4 pt-[10vh]">
      <button
        type="button"
        aria-label="Close"
        class="fixed inset-0 bg-black/50 backdrop-blur-sm"
        @click="emit('close')"
      />
      <div class="relative w-full max-w-lg rounded-2xl border border-neutral-200 bg-white p-5 shadow-2xl ring-1 ring-black/5 dark:border-neutral-700 dark:bg-neutral-800 dark:ring-white/10">
        <div class="mb-4 flex items-center justify-between">
          <h2 class="mb-0 text-base font-semibold text-neutral-900 dark:text-neutral-100">
            {{ title }}
          </h2>
          <button
            type="button"
            aria-label="Close"
            class="rounded-lg p-1 text-lg leading-none text-neutral-400 transition-colors hover:bg-neutral-100 hover:text-neutral-600 dark:hover:bg-neutral-700 dark:hover:text-neutral-300"
            @click="emit('close')"
          >
            ✕
          </button>
        </div>
        <slot />
      </div>
    </div>
  </Teleport>
</template>
