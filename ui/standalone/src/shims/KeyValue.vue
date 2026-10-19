<script>
// A map of strings as rows of key and value.
export default {
  name:  'KeyValue',
  props: {
    value:       { type: Object, default: () => ({}) },
    mode:        { type: String, default: 'edit' },
    protip:      { type: String, default: '' },
    addLabel:    { type: String, default: 'Add' },
    readAllowed: { type: Boolean, default: false },
  },
  emits: ['update:value'],
  data() {
    return { rows: Object.entries(this.value || {}).map(([key, value]) => ({ key, value })) };
  },
  methods: {
    emit() {
      const out = {};

      this.rows.filter((r) => r.key).forEach((r) => {
        out[r.key] = r.value;
      });
      this.$emit('update:value', out);
    },
    add() {
      this.rows.push({ key: '', value: '' });
    },
    remove(i) {
      this.rows.splice(i, 1);
      this.emit();
    },
  },
};
</script>

<template>
  <div class="key-value">
    <p
      v-if="protip"
      class="text-muted"
    >
      {{ protip }}
    </p>
    <div
      v-for="(r, i) in rows"
      :key="i"
      class="row mb-10"
    >
      <div class="col span-5">
        <input
          v-model="r.key"
          placeholder="key"
          :disabled="mode === 'view'"
          @input="emit"
        >
      </div>
      <div class="col span-6">
        <input
          v-model="r.value"
          placeholder="value"
          :disabled="mode === 'view'"
          @input="emit"
        >
      </div>
      <div
        v-if="mode !== 'view'"
        class="col span-1"
      >
        <button
          type="button"
          class="btn role-link"
          @click="remove(i)"
        >
          {{ t('generic.remove') }}
        </button>
      </div>
    </div>
    <p
      v-if="mode === 'view' && !rows.length"
      class="text-muted"
    >
      —
    </p>
    <button
      v-if="mode !== 'view'"
      type="button"
      class="btn role-tertiary"
      @click="add"
    >
      {{ addLabel }}
    </button>
  </div>
</template>
