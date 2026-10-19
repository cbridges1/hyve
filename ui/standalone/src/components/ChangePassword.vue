<script setup>
import { ref } from 'vue';
import Modal from './Modal.vue';
import { auth } from '../api';

const props = defineProps({ username: { type: String, required: true } });
const emit = defineEmits(['close']);
const current = ref('');
const next = ref('');
const error = ref('');
const busy = ref(false);
const done = ref(false);

async function submit() {
  error.value = '';
  busy.value = true;
  try {
    await auth.password(props.username, current.value, next.value);
    done.value = true;
  } catch (e) {
    error.value = e.message;
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <Modal
    title="Change password"
    @close="emit('close')"
  >
    <template v-if="done">
      <p class="mb-4 text-sm text-neutral-600 dark:text-neutral-400">
        Your password has been changed. Your other sessions were signed out.
      </p>
      <div class="flex justify-end">
        <button
          type="button"
          class="btn role-primary"
          @click="emit('close')"
        >
          Done
        </button>
      </div>
    </template>
    <form
      v-else
      @submit.prevent="submit"
    >
      <div class="mb-3 space-y-3">
        <label class="block text-sm">
          <span class="mb-1 block text-neutral-600 dark:text-neutral-400">Current password</span>
          <input
            v-model="current"
            type="password"
            autocomplete="current-password"
            autofocus
          >
        </label>
        <label class="block text-sm">
          <span class="mb-1 block text-neutral-600 dark:text-neutral-400">New password</span>
          <input
            v-model="next"
            type="password"
            autocomplete="new-password"
          >
        </label>
      </div>
      <p
        v-if="error"
        class="mb-3 text-sm text-red-600 dark:text-red-400"
      >
        {{ error }}
      </p>
      <div class="flex justify-end gap-2">
        <button
          type="button"
          class="btn role-tertiary"
          @click="emit('close')"
        >
          Cancel
        </button>
        <button
          type="submit"
          class="btn role-primary"
          :disabled="!current || !next || busy"
        >
          {{ busy ? 'Changing…' : 'Change password' }}
        </button>
      </div>
    </form>
  </Modal>
</template>
