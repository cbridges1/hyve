<script>
import { LabeledInput } from '@components/Form/LabeledInput';
import LabeledSelect from '@shell/components/form/LabeledSelect';
import KeyValue from '@shell/components/form/KeyValue';
import { Banner } from '@components/Banner';

const NAME_RE = /^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$/;
const KEY_RE = /^[-._a-zA-Z0-9]+$/;
const TYPES = ['Opaque', 'kubernetes.io/basic-auth', 'kubernetes.io/ssh-auth', 'kubernetes.io/tls', 'kubernetes.io/dockerconfigjson'];

function toBase64(bytes) {
  let bin = '';

  bytes.forEach((b) => {
    bin += String.fromCharCode(b);
  });

  return btoa(bin);
}

function fromBase64(b64) {
  const bin = atob(b64 || '');

  return Uint8Array.from(bin, (c) => c.charCodeAt(0));
}

// Text when the bytes are UTF-8, else null (a binary value).
function asText(bytes) {
  try {
    return new TextDecoder('utf-8', { fatal: true }).decode(bytes);
  } catch (e) {
    return null;
  }
}

// Creates a Secret, or changes an existing one's keys, labels and values,
// as the signed-in user: 'hyve secrets create' and 'hyve secrets set'.
// Values are hidden while typed unless shown; a file's contents can fill
// one, binary files included.
export default {
  name:       'HyveSecretEdit',
  components: {
    LabeledInput, LabeledSelect, KeyValue, Banner
  },
  props: {
    // An existing Secret to change; null to create one.
    namespace:  { type: String, default: '' },
    name:       { type: String, default: '' },
    existing:   { type: Boolean, default: false },
    // Namespaces to offer for a new Secret.
    namespaces: { type: Array, default: () => [] },
  },
  emits: ['close', 'saved'],

  async fetch() {
    if (!this.existing) {
      return;
    }
    try {
      this.secret = await this.$store.dispatch('management/find', { type: 'secret', id: `${ this.namespace }/${ this.name }`, opt: { force: true } });
      this.typ = this.secret._type || 'Opaque';
      this.labels = { ...(this.secret.metadata?.labels || {}) };
      this.rows = Object.keys(this.secret.data || {}).sort().map((k) => {
        const bytes = fromBase64(this.secret.data[k]);
        const text = asText(bytes);

        return {
          key: k, value: text ?? '', binary: text === null ? this.secret.data[k] : null, size: bytes.length, shown: false
        };
      });
    } catch (e) {
      this.errors = [e?.message || String(e)];
    }
  },

  data() {
    return {
      secret:  null,
      ns:      this.namespace || this.namespaces[0] || '',
      nm:      this.name,
      typ:     'Opaque',
      labels:  {},
      rows:    this.existing ? [] : [{
        key: '', value: '', binary: null, size: 0, shown: true
      }],
      errors:  [],
      saving:  false,
      types:   TYPES,
    };
  },

  computed: {
    title() {
      return this.existing ? this.t('hyve.secrets.editTitle', { name: `${ this.namespace }/${ this.name }` }) : this.t('hyve.secrets.createTitle');
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
    addRow() {
      this.rows.push({
        key: '', value: '', binary: null, size: 0, shown: true
      });
    },
    removeRow(i) {
      this.rows.splice(i, 1);
    },
    pickFile(row, e) {
      const f = e.target.files?.[0];

      if (!f) {
        return;
      }
      const reader = new FileReader();

      reader.onload = () => {
        const bytes = new Uint8Array(reader.result);
        const text = asText(bytes);

        row.key = row.key || f.name.replace(/[^-._a-zA-Z0-9]/g, '_');
        row.size = bytes.length;
        if (text === null) {
          row.binary = toBase64(bytes);
          row.value = '';
        } else {
          row.binary = null;
          row.value = text;
        }
      };
      reader.readAsArrayBuffer(f);
      e.target.value = '';
    },
    validate() {
      const out = [];

      if (!this.existing) {
        if (!NAME_RE.test(this.nm) || this.nm.length > 253) {
          out.push(this.t('hyve.secrets.badName'));
        }
        if (!this.ns) {
          out.push(this.t('hyve.secrets.needNamespace'));
        }
      }
      const seen = new Set();

      this.rows.forEach((r) => {
        if (!KEY_RE.test(r.key) || r.key === '.' || r.key === '..') {
          out.push(this.t('hyve.secrets.badKey', { key: r.key || '""' }));
        } else if (seen.has(r.key)) {
          out.push(this.t('hyve.secrets.dupKey', { key: r.key }));
        }
        seen.add(r.key);
      });
      if (!this.rows.length && !this.existing) {
        out.push(this.t('hyve.secrets.needKey'));
      }

      return out;
    },
    async save() {
      this.errors = this.validate();
      if (this.errors.length) {
        return;
      }
      const data = {};

      this.rows.forEach((r) => {
        data[r.key] = r.binary ?? toBase64(new TextEncoder().encode(r.value));
      });
      const labels = Object.keys(this.labels).length ? this.labels : undefined;

      this.saving = true;
      try {
        let s = this.secret;

        if (!s) {
          s = await this.$store.dispatch('management/create', {
            type: 'secret', metadata: { name: this.nm, namespace: this.ns }
          });
          s._type = this.typ || 'Opaque';
        }
        s.metadata.labels = labels;
        s.data = data;
        delete s.stringData;
        await s.save();
        this.$emit('saved', { namespace: s.metadata.namespace, name: s.metadata.name });
      } catch (e) {
        this.errors = [e?.message || String(e)];
      } finally {
        this.saving = false;
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
      <form
        class="hyve-view-box"
        role="dialog"
        :aria-label="title"
        @submit.prevent="save"
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
        <p
          v-if="$fetchState.pending"
          class="mb-10"
        >
          {{ t('generic.loading') }}
        </p>
        <template v-else>
          <Banner
            v-for="(e, i) in errors"
            :key="i"
            color="error"
            :label="e"
          />
          <div
            v-if="!existing"
            class="row mb-10"
          >
            <div class="col span-4">
              <LabeledInput
                v-model:value="nm"
                :label="t('tableHeaders.name')"
                :required="true"
              />
            </div>
            <div class="col span-4">
              <LabeledSelect
                v-model:value="ns"
                :label="t('tableHeaders.namespace')"
                :options="namespaces"
                :taggable="true"
                :required="true"
              />
            </div>
            <div class="col span-4">
              <LabeledSelect
                v-model:value="typ"
                :label="t('hyve.secrets.type')"
                :options="types"
                :taggable="true"
              />
            </div>
          </div>
          <div
            v-else
            class="text-muted mb-10 hyve-secret-type"
          >
            {{ t('hyve.secrets.type') }}: {{ typ }}
          </div>

          <h3 class="hyve-secret-h">
            {{ t('hyve.secrets.keys') }}
          </h3>
          <div
            v-for="(r, i) in rows"
            :key="i"
            class="hyve-secret-row"
          >
            <input
              v-model="r.key"
              class="hyve-secret-key"
              :placeholder="t('hyve.secrets.key')"
              :aria-label="t('hyve.secrets.key')"
            >
            <div class="hyve-secret-val">
              <div
                v-if="r.binary !== null"
                class="text-muted hyve-secret-binary"
              >
                {{ t('hyve.secrets.binary', { size: r.size }) }}
              </div>
              <textarea
                v-else-if="r.shown"
                v-model="r.value"
                rows="2"
                :placeholder="t('hyve.secrets.value')"
                :aria-label="t('hyve.secrets.value')"
                spellcheck="false"
              />
              <input
                v-else
                v-model="r.value"
                type="password"
                autocomplete="new-password"
                :placeholder="t('hyve.secrets.value')"
                :aria-label="t('hyve.secrets.value')"
              >
            </div>
            <div class="hyve-secret-actions">
              <button
                v-if="r.binary === null"
                type="button"
                class="btn btn-sm role-link"
                @click="r.shown = !r.shown"
              >
                {{ r.shown ? t('hyve.secrets.hide') : t('hyve.secrets.reveal') }}
              </button>
              <label class="btn btn-sm role-link hyve-secret-file">
                {{ t('hyve.secrets.fromFile') }}
                <input
                  type="file"
                  @change="pickFile(r, $event)"
                >
              </label>
              <button
                type="button"
                class="btn btn-sm role-link"
                @click="removeRow(i)"
              >
                {{ t('generic.remove') }}
              </button>
            </div>
          </div>
          <button
            type="button"
            class="btn btn-sm role-tertiary mb-20"
            @click="addRow"
          >
            {{ t('hyve.secrets.addKey') }}
          </button>

          <h3 class="hyve-secret-h">
            {{ t('hyve.secrets.labels') }}
          </h3>
          <KeyValue
            v-model:value="labels"
            :add-label="t('hyve.secrets.addLabel')"
          />

          <div class="hyve-secret-foot">
            <button
              type="button"
              class="btn role-secondary"
              @click="$emit('close')"
            >
              {{ t('generic.cancel') }}
            </button>
            <button
              type="submit"
              class="btn role-primary"
              :disabled="saving"
            >
              {{ existing ? t('generic.save') : t('generic.create') }}
            </button>
          </div>
        </template>
      </form>
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
.hyve-secret-type { font-size: 0.85rem; }
.hyve-secret-h { margin: 12px 0 8px; font-size: 0.85rem; font-weight: 600; }
.hyve-secret-row {
  display: grid;
  grid-template-columns: minmax(120px, 1fr) 2fr auto;
  gap: 8px;
  align-items: start;
  margin-bottom: 8px;
  @media (max-width: 640px) { grid-template-columns: 1fr; }
}
.hyve-secret-val textarea { min-height: 36px; font-family: monospace; font-size: 12px; }
.hyve-secret-binary { display: flex; align-items: center; height: 36px; font-size: 0.85rem; }
.hyve-secret-actions { display: flex; align-items: center; gap: 4px; min-height: 36px; }
.hyve-secret-file {
  position: relative;
  overflow: hidden;
  input { position: absolute; inset: 0; opacity: 0; cursor: pointer; }
}
.hyve-secret-foot {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 20px;
}
</style>
