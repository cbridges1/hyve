<script>
import LabeledSelect from '@shell/components/form/LabeledSelect';
import { LABELS, NAMESPACE } from '../config/types';

const KEY = 'hyve-environment';

// Picks the environment (a namespace labelled hyve.io/environment) hyve's
// pages show, or all of them; remembered in the browser.
export default {
  name:       'HyveEnvironmentPicker',
  live:       true,
  components: { LabeledSelect },
  emits:      ['update:value'],
  props:      {
    value: {
      type:    String,
      default: '',
    },
  },

  async fetch() {
    const namespaces = await this.$store.dispatch('management/findAll', { type: NAMESPACE }).catch(() => []);

    this.environments = namespaces
      .filter((ns) => ns.metadata?.labels?.[LABELS.ENVIRONMENT] !== undefined)
      .map((ns) => ns.metadata.name)
      .sort();
    let saved = '';

    try {
      saved = window.localStorage.getItem(KEY) || '';
    } catch (e) {}
    if (saved && !this.environments.includes(saved)) {
      saved = '';
    }
    this.$emit('update:value', saved);
  },

  data() {
    return { environments: [] };
  },

  computed: {
    options() {
      return [{ label: this.t('hyve.environment.all'), value: '' }, ...this.environments.map((e) => ({ label: e, value: e }))];
    },
  },

  methods: {
    pick(v) {
      try {
        window.localStorage.setItem(KEY, v || '');
      } catch (e) {}
      this.$emit('update:value', v || '');
    },
  },
};
</script>

<template>
  <LabeledSelect
    class="hyve-env-picker"
    :value="value"
    :options="options"
    :label="t('hyve.environment.label')"
    @update:value="pick"
  />
</template>

<style lang="scss" scoped>
.hyve-env-picker {
  min-width: 220px;
  max-width: 320px;
}
</style>
