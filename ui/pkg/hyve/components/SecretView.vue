<script>
import ViewDialog from './ViewDialog.vue';

// One Secret: its keys, with each value hidden until revealed. It's read
// as the signed-in user, so what they can't read says so instead.
export default {
  name:       'HyveSecretView',
  components: { ViewDialog },
  props:      {
    namespace: { type: String, required: true },
    name:      { type: String, required: true },
  },
  emits: ['close'],

  async fetch() {
    try {
      this.secret = await this.$store.dispatch('management/find', { type: 'secret', id: `${ this.namespace }/${ this.name }`, opt: { force: true } });
    } catch (e) {
      const status = e?.status || e?._status;

      this.error = status === 404 ? this.t('hyve.secrets.notFound') : status === 403 ? this.t('hyve.secrets.forbidden') : (e?.message || String(e));
    }
  },

  data() {
    return {
      secret: null, error: '', revealed: {}
    };
  },

  computed: {
    keys() {
      const d = this.secret?.data || {};

      return Object.keys(d).sort().map((k) => ({ key: k, value: decode(d[k]) }));
    },
    // The Secret without its values.
    meta() {
      if (!this.secret) {
        return [];
      }
      const md = JSON.parse(JSON.stringify(this.secret.metadata || {}));

      delete md.managedFields;
      // kubectl apply keeps the whole Secret, values included, here.
      if (md.annotations) {
        delete md.annotations['kubectl.kubernetes.io/last-applied-configuration'];
      }

      return [{
        label: this.t('hyve.view.metadata'), object: { apiVersion: 'v1', kind: 'Secret', metadata: md }
      }];
    },
  },

  methods: {
    toggle(k) {
      this.revealed = { ...this.revealed, [k]: !this.revealed[k] };
    },
  },
};

function decode(b64) {
  try {
    const bin = atob(b64 || '');

    return new TextDecoder().decode(Uint8Array.from(bin, (c) => c.charCodeAt(0)));
  } catch (e) {
    return b64;
  }
}
</script>

<template>
  <ViewDialog
    :title="`Secret ${ namespace }/${ name }`"
    :sections="meta"
    @close="$emit('close')"
  >
    <p v-if="$fetchState.pending">
      {{ t('generic.loading') }}
    </p>
    <p
      v-else-if="error"
      class="text-error"
    >
      {{ error }}
    </p>
    <template v-else>
      <div class="hyve-secret-type text-muted">
        {{ t('hyve.secrets.type') }}: {{ secret._type || secret.type || 'Opaque' }}
      </div>
      <table class="sortable-table hyve-secret-keys">
        <thead>
          <tr>
            <th>{{ t('hyve.secrets.key') }}</th>
            <th>{{ t('hyve.secrets.value') }}</th>
            <th />
          </tr>
        </thead>
        <tbody>
          <tr v-if="!keys.length">
            <td colspan="3">
              {{ t('hyve.secrets.noKeys') }}
            </td>
          </tr>
          <tr
            v-for="k in keys"
            :key="k.key"
          >
            <td><code>{{ k.key }}</code></td>
            <td class="hyve-secret-value">
              <pre v-if="revealed[k.key]">{{ k.value }}</pre>
              <span v-else>••••••••</span>
            </td>
            <td class="text-right">
              <button
                type="button"
                class="btn btn-sm role-link"
                @click="toggle(k.key)"
              >
                {{ revealed[k.key] ? t('hyve.secrets.hide') : t('hyve.secrets.reveal') }}
              </button>
            </td>
          </tr>
        </tbody>
      </table>
    </template>
  </ViewDialog>
</template>

<style lang="scss" scoped>
.hyve-secret-type { margin-bottom: 10px; font-size: 0.85rem; }
.hyve-secret-keys {
  width: 100%;
  margin-bottom: 16px;
  th, td { padding: 8px; text-align: left; vertical-align: middle; }
}
.hyve-secret-value pre {
  margin: 0;
  max-height: 200px;
  overflow: auto;
  white-space: pre-wrap;
  word-break: break-all;
  font-size: 12px;
}
</style>
