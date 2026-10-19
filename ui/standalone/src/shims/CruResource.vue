<script>
import Banner from './Banner.vue';

// Rancher's create/edit frame: the form, its errors, and Save or Cancel.
// In view mode, an Edit button instead.
export default {
  name:       'CruResource',
  components: { Banner },
  props:      {
    mode:             { type: String, default: 'create' },
    resource:         { type: Object, default: null },
    doneRoute:        { type: [String, Object], default: null },
    validationPassed: { type: Boolean, default: true },
    errors:           { type: Array, default: () => [] },
  },
  emits: ['finish', 'cancel', 'error'],
  data() {
    return { saving: false };
  },
  methods: {
    finish() {
      this.saving = true;
      this.$emit('finish', () => {
        this.saving = false;
      });
    },
    edit() {
      this.$router.push({ query: { ...this.$route.query, mode: 'edit' } });
    },
  },
};
</script>

<template>
  <form
    class="cru"
    @submit.prevent="finish"
  >
    <slot />
    <Banner
      v-for="(e, i) in errors"
      :key="i"
      color="error"
      :label="String(e)"
    />
    <div class="cru-footer">
      <template v-if="mode === 'view'">
        <button
          type="button"
          class="btn role-primary"
          @click="edit"
        >
          {{ t('generic.edit') }}
        </button>
      </template>
      <template v-else>
        <button
          type="button"
          class="btn role-secondary mr-10"
          @click="$emit('cancel')"
        >
          {{ t('generic.cancel') }}
        </button>
        <button
          type="submit"
          class="btn role-primary"
          :disabled="!validationPassed || saving"
        >
          {{ mode === 'create' ? t('generic.create') : t('generic.save') }}
        </button>
      </template>
    </div>
  </form>
</template>
