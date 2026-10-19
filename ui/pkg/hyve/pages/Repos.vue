<script>
import Loading from '@shell/components/Loading';
import { LabeledInput } from '@components/Form/LabeledInput';
import { Banner } from '@components/Banner';
import { LABELS } from '../config/types';
import { FLEET, problem } from '../utils/resources';

const NAME_RE = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/;

function emptyForm() {
  return {
    name: '', namespace: 'fleet-local', repo: '', branch: 'main', paths: ''
  };
}

// Fleet GitRepos (not the ones hyve makes for template resources) and
// their bundles. fleet-local deploys to the management cluster itself:
// ClusterClasses, templates, anything else it runs.
export default {
  name:       'HyveRepos',
  components: {
    Loading, LabeledInput, Banner
  },

  async fetch() {
    await this.load();
  },

  data() {
    return {
      repos: [], adding: false, form: emptyForm(), errors: [], saving: false
    };
  },

  methods: {
    async load() {
      const all = await this.$store.dispatch('management/findAll', { type: FLEET.GIT_REPO }).catch(() => []);

      this.repos = all
        .filter((r) => r.metadata?.labels?.[LABELS.MANAGED_BY] !== 'hyve')
        .sort((a, b) => a.id.localeCompare(b.id));
    },
    bundles(r) {
      const s = r.status?.summary;

      return s ? `${ s.ready || 0 }/${ s.desiredReady || 0 }` : '-';
    },
    state(r) {
      return problem(r) || r.status?.display?.state || (r.status?.summary?.ready === r.status?.summary?.desiredReady ? 'Ready' : 'Pending');
    },
    async save() {
      this.errors = [];
      const f = this.form;

      if (!NAME_RE.test(f.name)) {
        this.errors.push(this.t('hyve.repos.badName'));

        return;
      }
      if (!f.repo) {
        this.errors.push(this.t('hyve.repos.needRepo'));

        return;
      }
      this.saving = true;
      try {
        const spec = { repo: f.repo, branch: f.branch || 'main' };
        const paths = f.paths.split(',').map((s) => s.trim()).filter(Boolean);

        if (paths.length) {
          spec.paths = paths;
        }
        const model = await this.$store.dispatch('management/create', {
          type: FLEET.GIT_REPO, metadata: { name: f.name, namespace: f.namespace || 'fleet-local' }, spec
        });

        await model.save();
        this.adding = false;
        this.form = emptyForm();
        await this.load();
      } catch (e) {
        this.errors.push(e?.message || String(e));
      } finally {
        this.saving = false;
      }
    },
    async remove(r) {
      this.errors = [];
      try {
        await r.remove();
        await this.load();
      } catch (e) {
        this.errors.push(e?.message || String(e));
      }
    },
  },
};
</script>

<template>
  <Loading v-if="$fetchState.pending" />
  <div v-else>
    <div class="header">
      <h1>{{ t('hyve.repos.title') }}</h1>
      <button
        v-if="!adding"
        type="button"
        class="btn role-primary"
        @click="adding = true"
      >
        {{ t('hyve.repos.add') }}
      </button>
    </div>
    <p class="text-muted mb-20">
      {{ t('hyve.repos.hint') }}
    </p>
    <Banner
      v-for="(e, i) in errors"
      :key="i"
      color="error"
      :label="e"
    />
    <div
      v-if="adding"
      class="add mb-20"
    >
      <div class="row mb-10">
        <div class="col span-3">
          <LabeledInput
            v-model:value="form.name"
            :label="t('tableHeaders.name')"
            :required="true"
          />
        </div>
        <div class="col span-3">
          <LabeledInput
            v-model:value="form.namespace"
            :label="t('hyve.repos.workspace')"
          />
        </div>
        <div class="col span-6">
          <LabeledInput
            v-model:value="form.repo"
            :label="t('hyve.repos.repo')"
            placeholder="https://github.com/org/repo.git"
            :required="true"
          />
        </div>
      </div>
      <div class="row mb-10">
        <div class="col span-3">
          <LabeledInput
            v-model:value="form.branch"
            :label="t('hyve.repos.branch')"
          />
        </div>
        <div class="col span-9">
          <LabeledInput
            v-model:value="form.paths"
            :label="t('hyve.repos.paths')"
            :placeholder="t('hyve.repos.pathsPlaceholder')"
          />
        </div>
      </div>
      <p class="text-muted mb-10">
        {{ t('hyve.repos.privateHint') }}
      </p>
      <button
        type="button"
        class="btn role-primary mr-10"
        :disabled="saving"
        @click="save"
      >
        {{ t('generic.add') }}
      </button>
      <button
        type="button"
        class="btn role-secondary"
        @click="adding = false"
      >
        {{ t('generic.cancel') }}
      </button>
    </div>
    <table class="sortable-table hyve-table">
      <thead>
        <tr>
          <th>{{ t('tableHeaders.name') }}</th>
          <th>{{ t('hyve.repos.workspace') }}</th>
          <th>{{ t('hyve.repos.repo') }}</th>
          <th>{{ t('hyve.repos.branch') }}</th>
          <th>{{ t('hyve.repos.bundles') }}</th>
          <th>{{ t('hyve.repos.state') }}</th>
          <th />
        </tr>
      </thead>
      <tbody>
        <tr v-if="!repos.length">
          <td colspan="7">
            {{ t('hyve.repos.none') }}
          </td>
        </tr>
        <tr
          v-for="r in repos"
          :key="r.id"
        >
          <td>{{ r.metadata.name }}</td>
          <td>{{ r.metadata.namespace }}</td>
          <td>
            {{ r.spec.repo }}
            <div
              v-if="(r.spec.paths || []).length"
              class="text-muted small"
            >
              {{ r.spec.paths.join(', ') }}
            </div>
          </td>
          <td>{{ r.spec.branch || r.spec.revision || '-' }}</td>
          <td>{{ bundles(r) }}</td>
          <td>{{ state(r) }}</td>
          <td class="text-right">
            <button
              type="button"
              class="btn btn-sm role-link"
              @click="remove(r)"
            >
              {{ t('generic.remove') }}
            </button>
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>

<style lang="scss" scoped>
.header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 10px;
}
.add {
  border: 1px solid var(--border);
  border-radius: var(--border-radius);
  padding: 15px;
}
.hyve-table {
  width: 100%;
  th, td { padding: 8px; text-align: left; vertical-align: top; }
  .small { font-size: 0.85em; }
}
</style>
