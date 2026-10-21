<script>
import Loading from '../shims/Loading.vue';
import Modal from '../components/Modal.vue';
import ConfirmButton from '../components/ConfirmButton.vue';
import ViewDialog from '@pkg/components/ViewDialog.vue';
import { CAPI_CLUSTER, DEFAULT_TEMPLATE_NAMESPACE, LABELS } from '@pkg/config/types';

const CLUSTER_ACCESS = 'hyve.io.clusteraccess';
const ROLE_BINDING = 'rbac.authorization.k8s.io.rolebinding';
const NAMESPACE = 'namespace';
const USER = 'hyve.io.user';
const ROLES = [
  { value: 'owner', label: 'Owner', hint: 'everything on the cluster' },
  { value: 'member', label: 'Member', hint: 'see the cluster, create namespaces' },
  { value: 'read-only', label: 'Read-only', hint: 'read everything but Secrets' },
  { value: 'env-member', label: 'Environment member', hint: 'create and manage clusters in the environment' },
];

// Who can reach which clusters: 'hyve access grant/list/revoke'. A grant
// is a ClusterAccess in the environment (hyve-controller binds the role on
// the clusters), or, for environment members, RoleBindings in the
// environment and the template namespace.
export default {
  name:       'HyveAccess',
  components: {
    Loading, Modal, ConfirmButton, ViewDialog
  },

  async fetch() {
    await this.load();
  },

  data() {
    return {
      grants:   [],
      envs:     [],
      clusters: [],
      users:    [],
      env:      '',
      adding:   false,
      form:     {},
      error:    '',
      busy:     false,
      viewing:  null,
      roles:    ROLES,
    };
  },

  computed: {
    shown() {
      return this.grants.filter((g) => !this.env || g.ns === this.env);
    },
    envClusters() {
      return this.clusters.filter((c) => c.metadata.namespace === this.form.env).map((c) => c.metadata.name).sort();
    },
  },

  methods: {
    async load() {
      const opt = { force: true };
      const [nss, cas, rbs, clusters, users] = await Promise.all([
        this.$store.dispatch('management/findAll', { type: NAMESPACE, opt }).catch(() => []),
        this.$store.dispatch('management/findAll', { type: CLUSTER_ACCESS, opt }).catch(() => []),
        this.$store.dispatch('management/findAll', { type: ROLE_BINDING, opt }).catch(() => []),
        this.$store.dispatch('management/findAll', { type: CAPI_CLUSTER, opt }).catch(() => []),
        this.$store.dispatch('management/findAll', { type: USER, opt }).catch(() => []),
      ]);

      this.envs = nss.filter((n) => n.metadata?.labels?.[LABELS.ENVIRONMENT] === 'true').map((n) => n.metadata.name).sort();
      this.clusters = clusters;
      this.users = users.map((u) => u.metadata.name).sort();
      const envSet = new Set(this.envs);
      const grants = cas.map((ca) => ({
        id:      `ca:${ ca.id }`,
        name:    ca.metadata.name,
        ns:      ca.metadata.namespace,
        subject: ca.spec?.group ? `group ${ ca.spec.group }` : `user ${ ca.spec?.user }`,
        role:    ca.spec?.role,
        on:      ca.spec?.clusterName || 'every cluster',
        ready:   (ca.status?.conditions || []).find((c) => c.type === 'Ready')?.status || '-',
        model:   ca,
      }));

      rbs.filter((rb) => rb.metadata?.labels?.['hyve.io/env-member'] === 'true' && envSet.has(rb.metadata.namespace)).forEach((rb) => {
        (rb.subjects || []).forEach((s) => grants.push({
          id:      `rb:${ rb.id }:${ s.name }`,
          name:    rb.metadata.name,
          ns:      rb.metadata.namespace,
          subject: `${ String(s.kind).toLowerCase() } ${ String(s.name).replace(/^hyve:/, '') }`,
          role:    'environment member',
          on:      rb.metadata.namespace,
          ready:   '-',
          model:   rb,
        }));
      });
      this.grants = grants.sort((a, b) => `${ a.ns }/${ a.name }`.localeCompare(`${ b.ns }/${ b.name }`));
    },
    startAdd() {
      this.error = '';
      this.form = {
        kind: 'user', subject: '', role: 'owner', env: this.env || this.envs[0] || '', cluster: ''
      };
      this.adding = true;
    },
    async grant() {
      const f = this.form;

      this.error = '';
      if (!f.subject || !f.env) {
        this.error = 'Give a user or group, and an environment.';

        return;
      }
      const subjectKind = f.kind === 'group' ? 'Group' : 'User';
      // The names 'hyve access grant' gives, so the CLI and UI agree.
      const name = `${ subjectKind }-${ f.subject }`.toLowerCase();

      this.busy = true;
      try {
        if (f.role === 'env-member') {
          for (const [ns, role] of [[f.env, 'hyve-environment-member'], [DEFAULT_TEMPLATE_NAMESPACE, 'hyve-template-reader']]) {
            const rbName = `hyve-member-${ name }`;
            const have = await this.$store.dispatch('management/find', { type: ROLE_BINDING, id: `${ ns }/${ rbName }` }).catch(() => null);
            const rb = have || await this.$store.dispatch('management/create', {
              type:     ROLE_BINDING,
              metadata: { name: rbName, namespace: ns, labels: { 'app.kubernetes.io/managed-by': 'hyve', 'hyve.io/env-member': 'true' } },
            });

            rb.roleRef = { apiGroup: 'rbac.authorization.k8s.io', kind: 'ClusterRole', name: role };
            rb.subjects = [{ apiGroup: 'rbac.authorization.k8s.io', kind: subjectKind, name: `hyve:${ f.subject }` }];
            await rb.save();
          }
        } else {
          const spec = { role: f.role, [f.kind === 'group' ? 'group' : 'user']: f.subject };
          let caName = `${ f.role }-${ name }`;

          if (f.cluster) {
            spec.clusterName = f.cluster;
            caName = `${ f.cluster }-${ caName }`;
          }
          const have = await this.$store.dispatch('management/find', { type: CLUSTER_ACCESS, id: `${ f.env }/${ caName }` }).catch(() => null);
          const ca = have || await this.$store.dispatch('management/create', { type: CLUSTER_ACCESS, metadata: { name: caName, namespace: f.env } });

          ca.spec = spec;
          await ca.save();
        }
        this.adding = false;
        await this.load();
      } catch (e) {
        this.error = e.message;
      } finally {
        this.busy = false;
      }
    },
    async revoke(g) {
      this.error = '';
      try {
        if (g.id.startsWith('rb:')) {
          // Membership is two RoleBindings: the environment's and the
          // template namespace's.
          await g.model.remove();
          const other = await this.$store.dispatch('management/find', { type: ROLE_BINDING, id: `${ DEFAULT_TEMPLATE_NAMESPACE }/${ g.name }` }).catch(() => null);

          if (other) {
            await other.remove();
          }
        } else {
          await g.model.remove();
        }
        await this.load();
      } catch (e) {
        this.error = e.message;
      }
    },
  },
};
</script>

<template>
  <div>
    <div class="header">
      <h1 class="mb-0">
        Access
      </h1>
      <div class="header-right">
        <select
          v-model="env"
          class="w-auto!"
          aria-label="Environment"
        >
          <option value="">
            All environments
          </option>
          <option
            v-for="e in envs"
            :key="e"
            :value="e"
          >
            {{ e }}
          </option>
        </select>
        <button
          type="button"
          class="btn role-primary"
          @click="startAdd"
        >
          Grant access
        </button>
      </div>
    </div>
    <p class="mb-4 text-sm text-neutral-500">
      Roles on clusters, as Rancher's cluster roles: owner, member or read-only, on one cluster or every cluster in an environment (now and later). A cluster's creator owns it. Also <code>hyve access</code>.
    </p>
    <p
      v-if="error && !adding"
      class="mb-4 text-sm text-red-600 dark:text-red-400"
    >
      {{ error }}
    </p>
    <Loading v-if="$fetchState.pending" />
    <table
      v-else
      class="sortable-table"
    >
      <thead>
        <tr>
          <th>Subject</th>
          <th>Role</th>
          <th>Environment</th>
          <th>On</th>
          <th>Ready</th>
          <th />
        </tr>
      </thead>
      <tbody>
        <tr v-if="!shown.length">
          <td colspan="6">
            No grants here.
          </td>
        </tr>
        <tr
          v-for="g in shown"
          :key="g.id"
        >
          <td>{{ g.subject }}</td>
          <td>{{ g.role }}</td>
          <td>{{ g.ns }}</td>
          <td>{{ g.on }}</td>
          <td>{{ g.ready }}</td>
          <td class="text-right whitespace-nowrap">
            <button
              type="button"
              class="btn role-tertiary btn-sm"
              @click="viewing = g"
            >
              View
            </button>
            <ConfirmButton
              label="Revoke"
              :confirm="`Revoke ${ g.role } for ${ g.subject }?`"
              @confirm="revoke(g)"
            />
          </td>
        </tr>
      </tbody>
    </table>

    <Modal
      v-if="adding"
      title="Grant access"
      @close="adding = false"
    >
      <form
        class="space-y-3"
        @submit.prevent="grant"
      >
        <div class="flex gap-4 text-sm">
          <label class="radio"><input
            v-model="form.kind"
            type="radio"
            value="user"
          > User</label>
          <label class="radio"><input
            v-model="form.kind"
            type="radio"
            value="group"
          > Group</label>
        </div>
        <label class="block text-sm">
          <span class="mb-1 block text-neutral-600 dark:text-neutral-400">{{ form.kind === 'group' ? 'Group' : 'User' }}</span>
          <input
            v-model="form.subject"
            :list="form.kind === 'user' ? 'access-users' : undefined"
            autocomplete="off"
          >
          <datalist id="access-users">
            <option
              v-for="u in users"
              :key="u"
              :value="u"
            />
          </datalist>
        </label>
        <label class="block text-sm">
          <span class="mb-1 block text-neutral-600 dark:text-neutral-400">Role</span>
          <select v-model="form.role">
            <option
              v-for="r in roles"
              :key="r.value"
              :value="r.value"
            >
              {{ r.label }} — {{ r.hint }}
            </option>
          </select>
        </label>
        <label class="block text-sm">
          <span class="mb-1 block text-neutral-600 dark:text-neutral-400">Environment</span>
          <select v-model="form.env">
            <option
              v-for="e in envs"
              :key="e"
              :value="e"
            >
              {{ e }}
            </option>
          </select>
        </label>
        <label
          v-if="form.role !== 'env-member'"
          class="block text-sm"
        >
          <span class="mb-1 block text-neutral-600 dark:text-neutral-400">Cluster</span>
          <select v-model="form.cluster">
            <option value="">
              Every cluster in {{ form.env || 'the environment' }}
            </option>
            <option
              v-for="c in envClusters"
              :key="c"
              :value="c"
            >
              {{ c }}
            </option>
          </select>
        </label>
        <p
          v-if="error"
          class="text-sm text-red-600 dark:text-red-400"
        >
          {{ error }}
        </p>
        <div class="flex justify-end gap-2">
          <button
            type="button"
            class="btn role-tertiary"
            @click="adding = false"
          >
            Cancel
          </button>
          <button
            type="submit"
            class="btn role-primary"
            :disabled="busy"
          >
            Grant
          </button>
        </div>
      </form>
    </Modal>
    <ViewDialog
      v-if="viewing"
      :title="viewing.name"
      :sections="[{ label: viewing.model.kind || 'Object', object: viewing.model }]"
      @close="viewing = null"
    />
  </div>
</template>
