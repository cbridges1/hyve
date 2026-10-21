<script>
import jsyaml from 'js-yaml';
import { LabeledInput } from '@components/Form/LabeledInput';
import LabeledSelect from '@shell/components/form/LabeledSelect';
import { Checkbox } from '@components/Form/Checkbox';
import { kindOf, validate } from '../utils/resources';

const KINDS = ['helm', 'git', 'bundle', 'ref'];

function lastSegment(s) {
  const t = (s || '').replace(/\/+$/, '');

  return t.slice(t.lastIndexOf('/') + 1);
}

function list(s) {
  return (s || '').split(',').map((x) => x.trim()).filter(Boolean);
}

function dump(v) {
  return v && Object.keys(v).length ? jsyaml.dump(v, { lineWidth: -1 }) : '';
}

// One resource entry, every way the CLI can give one ('--helm', '--git',
// '--path', '--ref' and their options): a Helm chart with values, git
// paths, manifests sent inline, or a shared Resource with value
// overrides. Fields the form doesn't show are kept from the entry it
// started from. The parent reads the result with entry().
export default {
  name:       'HyveResourceForm',
  components: {
    LabeledInput, LabeledSelect, Checkbox
  },
  props: {
    // The entry to edit, or null for a new one.
    value:    { type: Object, default: null },
    // Whether a shared Resource may be referred to (not in a shared
    // Resource itself).
    allowRef: { type: Boolean, default: true },
    // Shared Resources to offer: [{ name, namespace }].
    shared:   { type: Array, default: () => [] },
    // Hide the name field (a shared Resource's name is its object's).
    noName:   { type: Boolean, default: false },
  },

  data() {
    const e = this.value || {};
    const kind = ({
      HelmOp: 'helm', GitRepo: 'git', Bundle: 'bundle', Ref: 'ref'
    })[kindOf(e)] || 'helm';
    const h = e.helmOp?.helm || {};
    const g = e.gitRepo || {};
    const spec = e.helmOp || e.gitRepo || e.bundle || {};

    return {
      kind,
      name:         e.name || '',
      namespace:    e.namespace || '',
      secrets:      (e.secrets || []).join(', '),
      correctDrift: !!spec.correctDrift?.enabled,
      repo:         h.repo || '',
      chart:        h.chart || '',
      version:      h.version || '',
      releaseName:  h.releaseName || '',
      values:       dump(kind === 'ref' ? e.values : h.values),
      gitRepo:      g.repo || '',
      branch:       g.branch || '',
      revision:     g.revision || '',
      paths:        (g.paths || []).join(', '),
      files:        (e.bundle?.resources || []).map((r) => ({ name: r.name || '', content: r.content || '' })),
      ref:          e.ref ? `${ e.refNamespace ? `${ e.refNamespace }/` : '' }${ e.ref }` : '',
      error:        '',
    };
  },

  computed: {
    kinds() {
      return KINDS.filter((k) => k !== 'ref' || this.allowRef).map((k) => ({ value: k, label: this.t(`hyve.form.kind.${ k }`) }));
    },
    refOptions() {
      return this.shared.map((r) => ({ label: `${ r.namespace }/${ r.name }`, value: r.name }));
    },
    namePlaceholder() {
      switch (this.kind) {
      case 'helm': return lastSegment(this.chart || this.repo);
      case 'git': return lastSegment(this.gitRepo).replace(/\.git$/, '').toLowerCase();
      case 'ref': return this.ref;
      default: return '';
      }
    },
  },

  watch: {
    kind(k) {
      if (k === 'bundle' && !this.files.length) {
        this.addFile();
      }
    },
  },

  created() {
    if (this.kind === 'bundle' && !this.files.length) {
      this.addFile();
    }
  },

  methods: {
    addFile() {
      this.files.push({ name: this.files.length ? `manifest-${ this.files.length + 1 }.yaml` : 'manifests.yaml', content: '' });
    },
    removeFile(i) {
      this.files.splice(i, 1);
    },
    loadFiles(ev) {
      [...(ev.target.files || [])].forEach((f) => {
        const reader = new FileReader();

        reader.onload = () => {
          const empty = this.files.findIndex((x) => !x.content);
          const item = { name: f.name, content: String(reader.result) };

          if (empty >= 0) {
            this.files.splice(empty, 1, item);
          } else {
            this.files.push(item);
          }
        };
        reader.readAsText(f);
      });
      ev.target.value = '';
    },
    parseValues() {
      if (!this.values.trim()) {
        return undefined;
      }
      const v = jsyaml.load(this.values);

      if (!v || typeof v !== 'object' || Array.isArray(v)) {
        throw new Error(this.t('hyve.form.badValues'));
      }

      return v;
    },
    // The entry, or throws with what's wrong.
    entry() {
      const orig = JSON.parse(JSON.stringify(this.value || {}));
      const e = { name: this.name.trim() || this.namePlaceholder };
      const drift = (spec) => {
        if (this.correctDrift) {
          spec.correctDrift = { ...(spec.correctDrift || {}), enabled: true };
        } else if (spec.correctDrift) {
          delete spec.correctDrift.enabled;
          if (!Object.keys(spec.correctDrift).length) {
            delete spec.correctDrift;
          }
        }

        return spec;
      };

      if (this.namespace.trim()) {
        e.namespace = this.namespace.trim();
      }
      const secrets = list(this.secrets);

      if (secrets.length) {
        e.secrets = secrets;
      }
      switch (this.kind) {
      case 'helm': {
        const spec = { ...(orig.helmOp || {}) };
        const helm = { ...(spec.helm || {}) };
        let repo = this.repo.trim();
        let chart = this.chart.trim();

        // Fleet wants an OCI chart as the whole reference in repo.
        if (chart.startsWith('oci://')) {
          repo = chart;
          chart = '';
        } else if (repo.startsWith('oci://') && chart) {
          repo = `${ repo.replace(/\/+$/, '') }/${ chart }`;
          chart = '';
        }
        const set = (k, v) => {
          if (v) {
            helm[k] = v;
          } else {
            delete helm[k];
          }
        };

        set('repo', repo);
        set('chart', chart);
        set('version', this.version.trim());
        set('releaseName', this.releaseName.trim());
        set('values', this.parseValues());
        spec.helm = helm;
        e.helmOp = drift(spec);
        break;
      }
      case 'git': {
        const g = { ...(orig.gitRepo || {}), repo: this.gitRepo.trim() };

        ['branch', 'revision'].forEach((k) => {
          if (this[k].trim()) {
            g[k] = this[k].trim();
          } else {
            delete g[k];
          }
        });
        const paths = list(this.paths);

        if (paths.length) {
          g.paths = paths;
        } else {
          delete g.paths;
        }
        e.gitRepo = drift(g);
        break;
      }
      case 'bundle': {
        const files = this.files.filter((f) => f.content.trim());

        if (!files.length) {
          throw new Error(this.t('hyve.form.needManifests'));
        }
        files.forEach((f) => {
          try {
            jsyaml.loadAll(f.content);
          } catch (err) {
            throw new Error(this.t('hyve.form.badManifest', { name: f.name, error: err.message }));
          }
        });
        e.bundle = drift({ ...(orig.bundle || {}), resources: files.map((f) => ({ name: f.name || 'manifests.yaml', content: f.content })) });
        if (!e.name) {
          e.name = (files[0].name || '').replace(/\.(ya?ml|json)$/i, '').toLowerCase().replace(/[^-a-z0-9]/g, '-');
        }
        break;
      }
      case 'ref': {
        let ref = this.ref.trim();

        if (ref.includes('/')) {
          e.refNamespace = ref.slice(0, ref.indexOf('/'));
          ref = ref.slice(ref.indexOf('/') + 1);
        } else if (orig.refNamespace) {
          e.refNamespace = orig.refNamespace;
        }
        e.ref = ref;
        if (!e.name) {
          e.name = ref;
        }
        const values = this.parseValues();

        if (values) {
          e.values = values;
        }
        break;
      }
      }
      if (this.noName) {
        delete e.name;
      }
      const bad = this.noName ? validate({ ...e, name: 'x' }) : validate(e);

      if (bad) {
        throw new Error(bad);
      }

      return e;
    },
  },
};
</script>

<template>
  <div class="hyve-rform">
    <div class="row mb-10">
      <div class="col span-4">
        <LabeledSelect
          v-model:value="kind"
          :label="t('hyve.form.kindLabel')"
          :options="kinds"
        />
      </div>
      <div
        v-if="!noName"
        class="col span-4"
      >
        <LabeledInput
          v-model:value="name"
          :label="t('hyve.resources.name')"
          :placeholder="namePlaceholder"
        />
      </div>
      <div class="col span-4">
        <LabeledInput
          v-model:value="namespace"
          :label="t('hyve.resources.namespace')"
          :placeholder="t('hyve.form.namespacePlaceholder')"
        />
      </div>
    </div>

    <template v-if="kind === 'helm'">
      <div class="row mb-10">
        <div class="col span-6">
          <LabeledInput
            v-model:value="repo"
            :label="t('hyve.resources.repo')"
            placeholder="https://charts.example.com, or oci://..."
          />
        </div>
        <div class="col span-6">
          <LabeledInput
            v-model:value="chart"
            :label="t('hyve.resources.chart')"
            :required="!repo.startsWith('oci://')"
          />
        </div>
      </div>
      <div class="row mb-10">
        <div class="col span-6">
          <LabeledInput
            v-model:value="version"
            :label="t('hyve.resources.version')"
          />
        </div>
        <div class="col span-6">
          <LabeledInput
            v-model:value="releaseName"
            :label="t('hyve.form.releaseName')"
            :placeholder="name || namePlaceholder"
          />
        </div>
      </div>
    </template>

    <template v-else-if="kind === 'git'">
      <div class="row mb-10">
        <div class="col span-12">
          <LabeledInput
            v-model:value="gitRepo"
            :label="t('hyve.resources.gitRepo')"
            :required="true"
          />
        </div>
      </div>
      <div class="row mb-10">
        <div class="col span-3">
          <LabeledInput
            v-model:value="branch"
            :label="t('hyve.resources.branch')"
          />
        </div>
        <div class="col span-3">
          <LabeledInput
            v-model:value="revision"
            :label="t('hyve.form.revision')"
          />
        </div>
        <div class="col span-6">
          <LabeledInput
            v-model:value="paths"
            :label="t('hyve.resources.paths')"
          />
        </div>
      </div>
    </template>

    <template v-else-if="kind === 'bundle'">
      <p class="text-muted mb-10 small">
        {{ t('hyve.form.manifestsHint') }}
      </p>
      <div
        v-for="(f, i) in files"
        :key="i"
        class="hyve-rform-file mb-10"
      >
        <div class="hyve-rform-file-head">
          <input
            v-model="f.name"
            :aria-label="t('hyve.form.fileName')"
          >
          <button
            type="button"
            class="btn btn-sm role-link"
            @click="removeFile(i)"
          >
            {{ t('generic.remove') }}
          </button>
        </div>
        <textarea
          v-model="f.content"
          rows="8"
          spellcheck="false"
          :aria-label="f.name"
          placeholder="apiVersion: v1&#10;kind: ConfigMap&#10;..."
        />
      </div>
      <div class="mb-10 hyve-rform-file-actions">
        <button
          type="button"
          class="btn btn-sm role-tertiary"
          @click="addFile"
        >
          {{ t('hyve.form.addFile') }}
        </button>
        <label class="btn btn-sm role-tertiary hyve-rform-upload">
          {{ t('hyve.form.loadFiles') }}
          <input
            type="file"
            multiple
            accept=".yaml,.yml,.json"
            @change="loadFiles"
          >
        </label>
      </div>
    </template>

    <template v-else-if="kind === 'ref'">
      <div class="row mb-10">
        <div class="col span-12">
          <LabeledSelect
            v-model:value="ref"
            :label="t('hyve.form.ref')"
            :options="refOptions"
            :taggable="true"
            :required="true"
          />
        </div>
      </div>
    </template>

    <div
      v-if="kind === 'helm' || kind === 'ref'"
      class="row mb-10"
    >
      <div class="col span-12">
        <label class="hyve-rform-label">
          {{ kind === 'ref' ? t('hyve.form.valueOverrides') : t('hyve.form.values') }}
          <textarea
            v-model="values"
            class="hyve-rform-values"
            rows="6"
            spellcheck="false"
            placeholder="replicaCount: 2"
          />
        </label>
      </div>
    </div>

    <div class="row mb-10">
      <div
        v-if="kind !== 'git'"
        class="col span-8"
      >
        <LabeledInput
          v-model:value="secrets"
          :label="t('hyve.resources.secrets')"
          :placeholder="t('hyve.resources.secretsPlaceholder')"
        />
      </div>
      <div
        v-if="kind !== 'ref'"
        class="col span-4 hyve-rform-check"
      >
        <Checkbox
          v-model:value="correctDrift"
          :label="t('hyve.form.correctDrift')"
        />
      </div>
    </div>
  </div>
</template>

<style lang="scss" scoped>
.small { font-size: 0.85em; }
.hyve-rform textarea {
  width: 100%;
  font-family: monospace;
  font-size: 12px;
}
.hyve-rform-label {
  display: block;
  color: var(--input-label);
  font-size: 0.85rem;
  textarea { margin-top: 4px; color: var(--body-text, inherit); }
}
.hyve-rform-values { min-height: 120px; }
.hyve-rform-file-head {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 4px;
  input { flex: 1; }
}
.hyve-rform-file-actions { display: flex; gap: 8px; }
.hyve-rform-upload {
  position: relative;
  overflow: hidden;
  input { position: absolute; inset: 0; opacity: 0; cursor: pointer; }
}
.hyve-rform-check {
  display: flex;
  align-items: flex-end;
  padding-bottom: 8px;
}
</style>
