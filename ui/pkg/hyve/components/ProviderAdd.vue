<script>
import { LabeledInput } from '@components/Form/LabeledInput';
import LabeledSelect from '@shell/components/form/LabeledSelect';
import { Banner } from '@components/Banner';
import { hostRequest, onHyveHost } from '../utils/host';

const TYPES = ['infrastructure', 'bootstrap', 'controlPlane', 'core', 'addon', 'ipam', 'runtimeExtension'];
const NAME_RE = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/;
// What ClusterClasses need, as install.FeatureVariables has it.
const FEATURES = {
  CLUSTER_TOPOLOGY: 'true', EXP_MACHINE_POOL: 'true', EXP_CLUSTER_RESOURCE_SET: 'true'
};

function toBase64(bytes) {
  let bin = '';

  bytes.forEach((b) => {
    bin += String.fromCharCode(b);
  });

  return btoa(bin);
}

// Adds a Cluster API provider: 'hyve provider add'. On hyve's host the
// host runs the CLI's own code (Turtles, the CAPI Operator, or local
// mode's manifests); in Rancher it's a Turtles CAPIProvider, with the
// variables in a Secret it names.
export default {
  name:       'HyveProviderAdd',
  components: { LabeledInput, LabeledSelect, Banner },
  emits:      ['close', 'added'],

  data() {
    return {
      name:         '',
      type:         '',
      version:      '',
      namespace:    '',
      providerName: '',
      fetchURL:     '',
      vars:         [],
      errors:       [],
      saving:       false,
    };
  },

  computed: {
    typeOptions() {
      return [{ label: this.t('hyve.providers.typeDefault'), value: '' }, ...TYPES.map((t) => ({ label: t, value: t }))];
    },
  },

  methods: {
    addVar() {
      this.vars.push({
        key: '', value: '', b64: false, bytes: null, file: ''
      });
    },
    pickFile(v, e) {
      const f = e.target.files?.[0];

      if (!f) {
        return;
      }
      const reader = new FileReader();

      reader.onload = () => {
        v.bytes = new Uint8Array(reader.result);
        v.file = f.name;
        v.value = '';
      };
      reader.readAsArrayBuffer(f);
      e.target.value = '';
    },
    request() {
      const variables = {};

      // A file is --variable-b64-file with base64, else --variable-file
      // (its text, without trailing newlines).
      this.vars.filter((v) => v.key).forEach((v) => {
        const bytes = v.bytes || new TextEncoder().encode(v.value);

        variables[v.key] = v.b64 ? toBase64(bytes) : (v.bytes ? new TextDecoder().decode(bytes).replace(/\n+$/, '') : v.value);
      });

      return {
        name:         this.name.trim(),
        type:         this.type || undefined,
        version:      this.version.trim() || undefined,
        namespace:    this.namespace.trim() || undefined,
        providerName: this.providerName.trim() || undefined,
        fetchURL:     this.fetchURL.trim() || undefined,
        variables:    Object.keys(variables).length ? variables : undefined,
      };
    },
    // Rancher: Turtles' CAPIProvider, as install.AddProvider makes it.
    async addTurtles(r) {
      const ns = r.namespace || `${ r.name }-system`;
      const nsObj = await this.$store.dispatch('management/find', { type: 'namespace', id: ns }).catch(() => null);

      if (!nsObj) {
        const n = await this.$store.dispatch('management/create', { type: 'namespace', metadata: { name: ns } });

        await n.save();
      }
      const secretName = `${ r.name }-variables`;
      const data = {};

      Object.entries({ ...FEATURES, ...(r.variables || {}) }).forEach(([k, v]) => {
        data[k] = btoa(unescape(encodeURIComponent(v)));
      });
      let secret = await this.$store.dispatch('management/find', { type: 'secret', id: `${ ns }/${ secretName }` }).catch(() => null);

      if (!secret) {
        secret = await this.$store.dispatch('management/create', { type: 'secret', metadata: { name: secretName, namespace: ns } });
        secret._type = 'Opaque';
      }
      secret.data = data;
      await secret.save();
      const spec = { name: r.providerName || r.name, type: r.type || 'infrastructure', configSecret: { name: secretName } };

      if (r.version) {
        spec.version = r.version;
      }
      if (r.fetchURL) {
        spec.fetchConfig = { url: r.fetchURL };
      }
      const p = await this.$store.dispatch('management/create', {
        type: 'turtles-capi.cattle.io.capiprovider', metadata: { name: r.name, namespace: ns }, spec
      });

      await p.save();

      return `Applied CAPIProvider ${ ns }/${ r.name }`;
    },
    async save() {
      this.errors = [];
      const r = this.request();

      if (!NAME_RE.test(r.name)) {
        this.errors.push(this.t('hyve.providers.badName'));

        return;
      }
      if (r.fetchURL && !/^https?:\/\//.test(r.fetchURL)) {
        this.errors.push(this.t('hyve.providers.badURL'));

        return;
      }
      this.saving = true;
      try {
        const msg = onHyveHost(this.$store) ? (await hostRequest(this.$store, 'providers', r)).message : await this.addTurtles(r);

        this.$emit('added', msg);
      } catch (e) {
        this.errors.push(e?.message || String(e));
      } finally {
        this.saving = false;
      }
    },
  },
};
</script>

<template>
  <div class="add mb-20">
    <h3 class="provider-title">
      {{ t('hyve.providers.add') }}
    </h3>
    <div class="row mb-10">
      <div class="col span-4">
        <LabeledInput
          v-model:value="name"
          :label="t('tableHeaders.name')"
          placeholder="docker, aws, civo..."
          :required="true"
        />
      </div>
      <div class="col span-4">
        <LabeledSelect
          v-model:value="type"
          :label="t('hyve.providers.type')"
          :options="typeOptions"
        />
      </div>
      <div class="col span-4">
        <LabeledInput
          v-model:value="version"
          :label="t('hyve.resources.version')"
          :placeholder="t('hyve.providers.versionPlaceholder')"
        />
      </div>
    </div>
    <div class="row mb-10">
      <div class="col span-4">
        <LabeledInput
          v-model:value="namespace"
          :label="t('tableHeaders.namespace')"
          :placeholder="name ? `${ name }-system` : 'NAME-system'"
        />
      </div>
      <div class="col span-4">
        <LabeledInput
          v-model:value="providerName"
          :label="t('hyve.providers.providerName')"
          :placeholder="name"
        />
      </div>
      <div class="col span-4">
        <LabeledInput
          v-model:value="fetchURL"
          :label="t('hyve.providers.fetchURL')"
          placeholder="https://.../infrastructure-components.yaml"
        />
      </div>
    </div>
    <h3 class="provider-sub">
      {{ t('hyve.providers.variables') }}
    </h3>
    <p class="text-muted small mb-10">
      {{ t('hyve.providers.variablesHint') }}
    </p>
    <div
      v-for="(v, i) in vars"
      :key="i"
      class="provider-var mb-10"
    >
      <input
        v-model="v.key"
        :placeholder="t('hyve.providers.varKey')"
        :aria-label="t('hyve.providers.varKey')"
      >
      <input
        v-model="v.value"
        type="password"
        autocomplete="new-password"
        :placeholder="v.file ? t('hyve.providers.fromFileName', { name: v.file }) : t('hyve.secrets.value')"
        :aria-label="t('hyve.secrets.value')"
        @input="v.bytes = null; v.file = ''"
      >
      <label class="provider-b64">
        <input
          v-model="v.b64"
          type="checkbox"
        >
        base64
      </label>
      <label class="btn btn-sm role-link provider-file">
        {{ t('hyve.secrets.fromFile') }}
        <input
          type="file"
          @change="pickFile(v, $event)"
        >
      </label>
      <button
        type="button"
        class="btn btn-sm role-link"
        @click="vars.splice(i, 1)"
      >
        {{ t('generic.remove') }}
      </button>
    </div>
    <button
      type="button"
      class="btn btn-sm role-tertiary mb-20"
      @click="addVar"
    >
      {{ t('hyve.providers.addVariable') }}
    </button>
    <Banner
      v-for="(e, i) in errors"
      :key="i"
      color="error"
      :label="e"
    />
    <div>
      <button
        type="button"
        class="btn role-primary mr-10"
        :disabled="saving"
        @click="save"
      >
        {{ saving ? t('hyve.providers.adding') : t('generic.add') }}
      </button>
      <button
        type="button"
        class="btn role-secondary"
        @click="$emit('close')"
      >
        {{ t('generic.cancel') }}
      </button>
    </div>
  </div>
</template>

<style lang="scss" scoped>
.add {
  border: 1px solid var(--border);
  border-radius: var(--border-radius);
  padding: 15px;
}
.small { font-size: 0.85em; }
.provider-title { margin-top: 0; }
.provider-sub { margin: 12px 0 4px; font-size: 0.9rem; }
.provider-var {
  display: grid;
  grid-template-columns: minmax(140px, 1fr) 2fr auto auto auto;
  gap: 8px;
  align-items: center;
  @media (max-width: 640px) { grid-template-columns: 1fr; }
}
.provider-b64 {
  display: flex;
  align-items: center;
  gap: 4px;
  font-size: 0.85em;
  white-space: nowrap;
  input { margin: 0; }
}
.provider-file {
  position: relative;
  overflow: hidden;
  input { position: absolute; inset: 0; opacity: 0; cursor: pointer; }
}
</style>
