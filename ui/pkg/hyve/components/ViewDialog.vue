<script>
import jsyaml from 'js-yaml';

// Kubernetes' own fields; a model's store fields (links, actions, id...)
// aren't the object.
const FIELDS = ['apiVersion', 'kind', 'metadata', 'spec', 'status'];

// What an object is, as YAML: the same as 'kubectl get -o yaml' without
// managedFields.
export function toYAML(obj) {
  if (!obj) {
    return '';
  }
  const plain = JSON.parse(JSON.stringify(obj));
  const out = {};

  (plain.apiVersion || plain.metadata ? FIELDS : Object.keys(plain)).forEach((k) => {
    if (plain[k] !== undefined) {
      out[k] = plain[k];
    }
  });
  if (out.metadata) {
    delete out.metadata.managedFields;
  }

  return jsyaml.dump(out, { lineWidth: -1, noRefs: true });
}

// A read-only view of one or more objects as YAML, in a dialog: what's
// behind a row on a page. It brings its own markup and styles, so it works
// the same in hyve's UI and in Rancher.
export default {
  name:  'HyveViewDialog',
  props: {
    title: { type: String, required: true },
    // [{ label, object }], or one object.
    sections: { type: Array, default: () => [] },
  },
  emits: ['close'],

  data() {
    return { copied: -1 };
  },

  computed: {
    shown() {
      return this.sections.map((s) => ({ label: s.label, text: typeof s.object === 'string' ? s.object : toYAML(s.object) }));
    },
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
    async copy(i) {
      try {
        await navigator.clipboard.writeText(this.shown[i].text);
        this.copied = i;
        setTimeout(() => {
          this.copied = -1;
        }, 1500);
      } catch (e) {}
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
        :aria-label="title"
      >
        <div class="hyve-view-head">
          <h2>{{ title }}</h2>
          <button
            type="button"
            class="hyve-view-x"
            aria-label="Close"
            @click="$emit('close')"
          >
            ✕
          </button>
        </div>
        <slot />
        <div
          v-for="(s, i) in shown"
          :key="i"
          class="hyve-view-section"
        >
          <div class="hyve-view-label">
            <span>{{ s.label }}</span>
            <button
              type="button"
              class="btn btn-sm role-link"
              @click="copy(i)"
            >
              {{ copied === i ? t('hyve.view.copied') : t('hyve.view.copy') }}
            </button>
          </div>
          <pre>{{ s.text }}</pre>
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
  h2 { margin: 0; font-size: 1rem; font-weight: 600; word-break: break-all; }
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
.hyve-view-section + .hyve-view-section { margin-top: 16px; }
.hyve-view-label {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 6px;
  font-size: 0.75rem;
  font-weight: 600;
  letter-spacing: 0.05em;
  text-transform: uppercase;
  opacity: 0.7;
}
pre {
  margin: 0;
  max-height: 60vh;
  overflow: auto;
  padding: 12px;
  border-radius: 8px;
  background: var(--input-bg, rgba(0, 0, 0, 0.04));
  font-size: 12px;
  line-height: 1.5;
  white-space: pre;
}
</style>
