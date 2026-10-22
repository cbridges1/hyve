<script>
import Loading from '@shell/components/Loading';
import { LabeledInput } from '@components/Form/LabeledInput';
import { Checkbox } from '@components/Form/Checkbox';
import { Banner } from '@components/Banner';
import { LABELS } from '../config/types';
import { FLEET, problem } from '../utils/resources';
import ViewDialog from '../components/ViewDialog.vue';

const NAME_RE = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/;

function emptyForm() {
  return {
    name: '', namespace: 'fleet-local', repo: '', branch: 'main', revision: '', paths: '', correctDrift: false, username: '', token: '', editing: null
  };
}

// Fleet GitRepos (not the ones hyve makes for template resources) and
// their bundles. fleet-local deploys to the management cluster itself:
// ClusterClasses, templates, anything else it runs. Adding and editing do
// what 'hyve repo add' does, credentials for a private repo included (a
// basic-auth Secret the GitRepo names).
export default {
  name:       'HyveRepos',
  live:       true,
  components: {
    Loading, LabeledInput, Checkbox, Banner, ViewDialog
  },

  async fetch() {
    await this.load();
  },

  data() {
    return {
      repos: [], adding: false, form: emptyForm(), errors: [], saving: false, viewing: null
    };
  },

  methods: {
    async load() {
      const all = await this.$store.dispatch('management/findAll', { type: FLEET.GIT_REPO }).catch(() => []);

      this.repos = all
        .filter((r) => !r.metadata?.labels?.[LABELS.OWNER_KIND])
        .sort((a, b) => a.id.localeCompare(b.id));
    },
    bundles(r) {
      const s = r.status?.summary;

      return s ? `${ s.ready || 0 }/${ s.desiredReady || 0 }` : '-';
    },
    state(r) {
      return problem(r) || r.status?.display?.state || (r.status?.summary?.ready === r.status?.summary?.desiredReady ? 'Ready' : 'Pending');
    },
    startAdd() {
      this.errors = [];
      this.form = emptyForm();
      this.adding = true;
    },
    startEdit(r) {
      this.errors = [];
      this.form = {
        ...emptyForm(),
        name:         r.metadata.name,
        namespace:    r.metadata.namespace,
        repo:         r.spec.repo || '',
        branch:       r.spec.branch || '',
        revision:     r.spec.revision || '',
        paths:        (r.spec.paths || []).join(', '),
        correctDrift: !!r.spec.correctDrift?.enabled,
        editing:      r,
      };
      this.adding = true;
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
      if (f.token && !f.username) {
        this.errors.push(this.t('hyve.repos.needUsername'));

        return;
      }
      this.saving = true;
      try {
        const ns = f.namespace || 'fleet-local';
        let model = f.editing ? await this.$store.dispatch('management/find', { type: FLEET.GIT_REPO, id: `${ ns }/${ f.name }`, opt: { force: true } }) : null;
        const spec = { ...(model?.spec || {}), repo: f.repo };

        ['branch', 'revision'].forEach((k) => {
          if (f[k]) {
            spec[k] = f[k];
          } else {
            delete spec[k];
          }
        });
        if (!spec.branch && !spec.revision) {
          spec.branch = 'main';
        }
        const paths = f.paths.split(',').map((s) => s.trim()).filter(Boolean);

        if (paths.length) {
          spec.paths = paths;
        } else {
          delete spec.paths;
        }
        if (f.correctDrift) {
          spec.correctDrift = { ...(spec.correctDrift || {}), enabled: true };
        } else {
          delete spec.correctDrift;
        }
        if (f.token) {
          // The same Secret 'hyve repo add --username --token-env' makes.
          const secretName = `${ f.name }-git-auth`;
          const stringData = { username: f.username, password: f.token };
          let secret = await this.$store.dispatch('management/find', { type: 'secret', id: `${ ns }/${ secretName }`, opt: { force: true } }).catch(() => null);

          if (!secret) {
            secret = await this.$store.dispatch('management/create', { type: 'secret', metadata: { name: secretName, namespace: ns } });
            secret._type = 'kubernetes.io/basic-auth';
          }
          secret.data = { username: btoa(unescape(encodeURIComponent(stringData.username))), password: btoa(unescape(encodeURIComponent(stringData.password))) };
          await secret.save();
          spec.clientSecretName = secretName;
        }
        if (!model) {
          model = await this.$store.dispatch('management/create', {
            type: FLEET.GIT_REPO, metadata: { name: f.name, namespace: ns, labels: { [LABELS.MANAGED_BY]: 'hyve' } }
          });
        }
        model.spec = spec;
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
        @click="startAdd"
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
      <h3 class="mt-0">
        {{ form.editing ? t('hyve.repos.editTitle', { name: form.name }) : t('hyve.repos.add') }}
      </h3>
      <div class="row mb-10">
        <div class="col span-3">
          <LabeledInput
            v-model:value="form.name"
            :label="t('tableHeaders.name')"
            :required="true"
            :disabled="!!form.editing"
          />
        </div>
        <div class="col span-3">
          <LabeledInput
            v-model:value="form.namespace"
            :label="t('hyve.repos.workspace')"
            :disabled="!!form.editing"
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
        <div class="col span-3">
          <LabeledInput
            v-model:value="form.revision"
            :label="t('hyve.form.revision')"
          />
        </div>
        <div class="col span-6">
          <LabeledInput
            v-model:value="form.paths"
            :label="t('hyve.repos.paths')"
            :placeholder="t('hyve.repos.pathsPlaceholder')"
          />
        </div>
      </div>
      <div class="row mb-10">
        <div class="col span-3">
          <LabeledInput
            v-model:value="form.username"
            :label="t('hyve.repos.username')"
            autocomplete="off"
          />
        </div>
        <div class="col span-5">
          <LabeledInput
            v-model:value="form.token"
            type="password"
            :label="t('hyve.repos.token')"
            :placeholder="form.editing?.spec?.clientSecretName ? t('hyve.repos.tokenKeep') : ''"
            autocomplete="new-password"
          />
        </div>
        <div class="col span-4 repo-check">
          <Checkbox
            v-model:value="form.correctDrift"
            :label="t('hyve.form.correctDrift')"
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
        {{ form.editing ? t('generic.save') : t('generic.add') }}
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
          <td class="text-right actions">
            <button
              type="button"
              class="btn btn-sm role-link"
              @click="viewing = r"
            >
              {{ t('hyve.view.view') }}
            </button>
            <button
              type="button"
              class="btn btn-sm role-link"
              @click="startEdit(r)"
            >
              {{ t('generic.edit') }}
            </button>
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
    <ViewDialog
      v-if="viewing"
      :title="`GitRepo ${ viewing.metadata.namespace }/${ viewing.metadata.name }`"
      :sections="[{ label: 'GitRepo', object: viewing }]"
      @close="viewing = null"
    />
  </div>
</template>

<style lang="scss" scoped>
.header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 10px;
}
.repo-check {
  display: flex;
  align-items: flex-end;
  padding-bottom: 8px;
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
  .actions { white-space: nowrap; }
}
</style>
