<script>
import { Banner } from '@components/Banner';
import { applyObjects, parseDocs } from '../utils/apply';

// Creates or updates ClusterTemplates, shared Resources and
// ClusterResources from YAML: 'hyve template apply -f'. Pasted, or loaded
// from files.
export default {
  name:       'HyveApplyYaml',
  components: { Banner },
  emits:      ['close', 'applied'],

  data() {
    return {
      text: '', results: [], error: '', busy: false
    };
  },

  mounted() {
    window.addEventListener('keydown', this.onKey);
  },

  beforeUnmount() {
    window.removeEventListener('keydown', this.onKey);
  },

  methods: {
    onKey(e) {
      if (e.key === 'Escape') {
        this.$emit('close');
      }
    },
    load(ev) {
      const files = [...(ev.target.files || [])];

      Promise.all(files.map((f) => f.text())).then((texts) => {
        this.text = [this.text.trim(), ...texts.map((t) => t.trim())].filter(Boolean).join('\n---\n');
      });
      ev.target.value = '';
    },
    async apply() {
      this.error = '';
      this.results = [];
      let docs;

      try {
        docs = parseDocs(this.text);
      } catch (e) {
        this.error = e.message;

        return;
      }
      if (!docs.length) {
        this.error = this.t('hyve.apply.empty');

        return;
      }
      this.busy = true;
      try {
        this.results = await applyObjects(this.$store, docs);
        if (this.results.some((r) => r.action)) {
          this.$emit('applied');
        }
      } finally {
        this.busy = false;
      }
    },
  },
};
</script>

<template>
  <Teleport to="body">
    <div class="hyve-view">
      <button
        type="button"
        class="hyve-view-backdrop"
        aria-label="Close"
        @click="$emit('close')"
      />
      <div
        class="hyve-view-box"
        role="dialog"
        :aria-label="t('hyve.apply.title')"
      >
        <div class="hyve-view-head">
          <h2>{{ t('hyve.apply.title') }}</h2>
          <button
            type="button"
            class="hyve-view-x"
            aria-label="Close"
            @click="$emit('close')"
          >
            ✕
          </button>
        </div>
        <p class="text-muted hyve-apply-hint">
          {{ t('hyve.apply.hint') }}
        </p>
        <textarea
          v-model="text"
          class="hyve-apply-text"
          rows="16"
          spellcheck="false"
          :aria-label="t('hyve.apply.title')"
          placeholder="apiVersion: hyve.io/v1alpha1&#10;kind: ClusterTemplate&#10;..."
        />
        <Banner
          v-if="error"
          color="error"
          :label="error"
        />
        <ul
          v-if="results.length"
          class="hyve-apply-results"
        >
          <li
            v-for="(r, i) in results"
            :key="i"
            :class="r.error ? 'text-error' : 'text-success'"
          >
            {{ r.object }}: {{ r.error || t(`hyve.apply.${ r.action }`) }}
          </li>
        </ul>
        <div class="hyve-apply-foot">
          <label class="btn role-tertiary hyve-apply-file">
            {{ t('hyve.form.loadFiles') }}
            <input
              type="file"
              multiple
              accept=".yaml,.yml,.json"
              @change="load"
            >
          </label>
          <span class="hyve-apply-grow" />
          <button
            type="button"
            class="btn role-secondary"
            @click="$emit('close')"
          >
            {{ t('generic.close') }}
          </button>
          <button
            type="button"
            class="btn role-primary"
            :disabled="busy || !text.trim()"
            @click="apply"
          >
            {{ t('hyve.apply.apply') }}
          </button>
        </div>
      </div>
    </div>
  </Teleport>
</template>

<style lang="scss" scoped>
.hyve-view {
  position: fixed;
  inset: 0;
  z-index: 60;
  display: flex;
  align-items: flex-start;
  justify-content: center;
  padding: 6vh 16px 16px;
  overflow-y: auto;
}
.hyve-view-backdrop {
  position: fixed;
  inset: 0;
  border: 0;
  background: rgba(0, 0, 0, 0.5);
  cursor: default;
}
.hyve-view-box {
  position: relative;
  width: 100%;
  max-width: 860px;
  border: 1px solid var(--border);
  border-radius: var(--border-radius, 12px);
  background: var(--box-bg, #fff);
  color: var(--body-text, inherit);
  padding: 20px;
  box-shadow: 0 20px 50px rgba(0, 0, 0, 0.35);
}
.hyve-view-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 12px;
  h2 { margin: 0; font-size: 1rem; font-weight: 600; }
}
.hyve-view-x {
  border: 0;
  background: none;
  color: inherit;
  opacity: 0.6;
  font-size: 1.1rem;
  cursor: pointer;
  &:hover { opacity: 1; }
}
.hyve-apply-hint { font-size: 0.85rem; margin-bottom: 10px; }
.hyve-apply-text {
  width: 100%;
  font-family: monospace;
  font-size: 12px;
  margin-bottom: 10px;
}
.hyve-apply-results {
  margin: 0 0 10px;
  padding-left: 18px;
  font-size: 0.85rem;
}
.hyve-apply-foot {
  display: flex;
  align-items: center;
  gap: 8px;
}
.hyve-apply-grow { flex: 1; }
.hyve-apply-file {
  position: relative;
  overflow: hidden;
  input { position: absolute; inset: 0; opacity: 0; cursor: pointer; }
}
</style>
