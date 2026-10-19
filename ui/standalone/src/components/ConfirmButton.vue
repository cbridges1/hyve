<script>
// The console's red Delete: click, then click again to confirm.
export default {
  name:  'ConfirmButton',
  props: {
    label:   { type: String, required: true },
    confirm: { type: String, required: true },
  },
  emits: ['confirm'],
  data() {
    return { armed: false, timer: null };
  },
  beforeUnmount() {
    clearTimeout(this.timer);
  },
  methods: {
    click() {
      if (this.armed) {
        this.armed = false;
        this.$emit('confirm');

        return;
      }
      this.armed = true;
      this.timer = setTimeout(() => {
        this.armed = false;
      }, 4000);
    },
  },
};
</script>

<template>
  <button
    type="button"
    :class="['shrink-0 rounded-lg px-2.5 py-1 text-sm font-medium transition-colors', armed ? 'bg-red-600 text-white hover:bg-red-700' : 'text-red-600 hover:bg-red-50 dark:text-red-400 dark:hover:bg-red-950/40']"
    @click.stop="click"
  >
    {{ armed ? confirm : label }}
  </button>
</template>
