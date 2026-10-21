<script>
import BackLink from '../components/BackLink.vue';
import Card from '../components/Card.vue';
import ViewDialog from '@pkg/components/ViewDialog.vue';
import {
  CAPI_CLUSTER, DEFAULT_TEMPLATE_NAMESPACE, HYVE, LABELS, PRODUCT
} from '@pkg/config/types';
import { hostRequest } from '@pkg/utils/host';
import { applyObjects } from '@pkg/utils/apply';

// 'hyve import': old hyve Templates and herd ClusterTemplates, pasted or
// loaded from files, converted by the host (the CLI's own code) into hyve
// ClusterTemplates. Creating them is done here, as the signed-in user, and
// for herd templates can label the clusters herd made from them.
export default {
  name:       'HyveImport',
  components: { BackLink, Card, ViewDialog },

  data() {
    return {
      text:              '',
      templateNamespace: DEFAULT_TEMPLATE_NAMESPACE,
      prefixNamespace:   false,
      relabel:           false,
      results:           null,
      applied:           [],
      error:             '',
      busy:              false,
      viewing:           null,
      back:              { name: `${ PRODUCT }-c-cluster-resource`, params: { cluster: '_', resource: HYVE.TEMPLATE } },
    };
  },

  computed: {
    importable() {
      return (this.results || []).filter((r) => r.template);
    },
    anyHerd() {
      return this.importable.some((r) => r.herd);
    },
  },

  methods: {
    load(ev) {
      Promise.all([...(ev.target.files || [])].map((f) => f.text())).then((texts) => {
        this.text = [this.text.trim(), ...texts.map((t) => t.trim())].filter(Boolean).join('\n---\n');
      });
      ev.target.value = '';
    },
    async convert() {
      this.error = '';
      this.results = null;
      this.applied = [];
      this.busy = true;
      try {
        const res = await hostRequest(this.$store, 'import', {
          yaml: this.text, templateNamespace: this.templateNamespace, prefixNamespace: this.prefixNamespace
        });

        this.results = res.results || [];
      } catch (e) {
        this.error = e.message;
      } finally {
        this.busy = false;
      }
    },
    async create() {
      this.error = '';
      this.busy = true;
      try {
        this.applied = await applyObjects(this.$store, this.importable.map((r) => r.template), { templateNamespace: this.templateNamespace });
        if (this.relabel) {
          await this.relabelHerd();
        }
      } catch (e) {
        this.error = e.message;
      } finally {
        this.busy = false;
      }
    },
    // --relabel-clusters: the clusters herd made from a herd template get
    // the new template's labels, so they get its resources and locks.
    async relabelHerd() {
      const clusters = await this.$store.dispatch('management/findAll', { type: CAPI_CLUSTER, opt: { force: true } });

      for (const r of this.importable.filter((x) => x.herd)) {
        const ok = this.applied.find((a) => a.object.endsWith(`/${ r.name }`) && a.action);

        if (!ok) {
          continue;
        }
        const mine = clusters.filter((c) => c.metadata.namespace === r.herd.namespace && c.metadata.labels?.['herd.io/template'] === r.herd.name);

        for (const c of mine) {
          c.metadata.labels = { ...c.metadata.labels, [LABELS.TEMPLATE]: r.name, [LABELS.TEMPLATE_NAMESPACE]: this.templateNamespace };
          await c.save();
          this.applied.push({ object: `Cluster ${ c.metadata.namespace }/${ c.metadata.name }`, action: 'relabelled' });
        }
      }
    },
  },
};
</script>

<template>
  <div class="space-y-4">
    <div>
      <BackLink
        :to="back"
        label="Templates"
      />
      <h1 class="mb-0">
        Import templates
      </h1>
    </div>
    <p class="text-sm text-neutral-500">
      Converts old hyve Templates (hyve-capi-module, hyve-civo-module, hyve-unraid-k3s-module) and herd ClusterTemplates into hyve ClusterTemplates: flavor presets expanded, params mapped onto the ClusterClass, locks, the schedule, and Helm and git resources. What hyve can't express is listed for each. Also <code>hyve import</code>.
    </p>
    <Card>
      <textarea
        v-model="text"
        rows="14"
        class="font-mono text-xs"
        spellcheck="false"
        aria-label="Old templates as YAML"
        placeholder="apiVersion: hyve.io/v1alpha1&#10;kind: Template&#10;..."
      />
      <div class="mt-3 flex flex-wrap items-center gap-3">
        <label class="btn role-tertiary relative overflow-hidden">
          Load files
          <input
            type="file"
            multiple
            accept=".yaml,.yml"
            class="absolute inset-0 cursor-pointer opacity-0"
            @change="load"
          >
        </label>
        <label class="flex items-center gap-2 text-sm">
          <span class="whitespace-nowrap text-neutral-600 dark:text-neutral-400">Template namespace</span>
          <input
            v-model="templateNamespace"
            class="w-44!"
          >
        </label>
        <label class="checkbox">
          <input
            v-model="prefixNamespace"
            type="checkbox"
          >
          Name them &lt;old namespace&gt;-&lt;name&gt;
        </label>
        <span class="flex-1" />
        <button
          type="button"
          class="btn role-primary"
          :disabled="busy || !text.trim()"
          @click="convert"
        >
          Convert
        </button>
      </div>
    </Card>
    <p
      v-if="error"
      class="rounded-lg bg-red-50 px-3 py-2 text-sm text-red-700 dark:bg-red-950/60 dark:text-red-300"
    >
      {{ error }}
    </p>
    <Card
      v-if="results"
      title="Result"
    >
      <table class="sortable-table">
        <thead>
          <tr>
            <th>From</th>
            <th>ClusterTemplate</th>
            <th>Notes</th>
            <th />
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="r in results"
            :key="r.source"
          >
            <td>{{ r.kind }} {{ r.source }}</td>
            <td>
              <span v-if="r.template">{{ templateNamespace }}/{{ r.name }}</span>
              <span
                v-else
                class="text-red-600 dark:text-red-400"
              >Skipped: {{ r.skipped }}</span>
            </td>
            <td class="text-xs">
              <div
                v-for="n in r.notes || []"
                :key="n"
              >
                {{ n }}
              </div>
              <span v-if="!(r.notes || []).length">-</span>
            </td>
            <td class="text-right">
              <button
                v-if="r.template"
                type="button"
                class="btn role-tertiary btn-sm"
                @click="viewing = r"
              >
                View
              </button>
            </td>
          </tr>
        </tbody>
      </table>
      <div
        v-if="importable.length"
        class="mt-4 flex flex-wrap items-center gap-3"
      >
        <label
          v-if="anyHerd"
          class="checkbox"
        >
          <input
            v-model="relabel"
            type="checkbox"
          >
          Label the clusters herd made from them with the new templates
        </label>
        <span class="flex-1" />
        <button
          type="button"
          class="btn role-primary"
          :disabled="busy"
          @click="create"
        >
          Create {{ importable.length }} template{{ importable.length === 1 ? '' : 's' }}
        </button>
      </div>
      <ul
        v-if="applied.length"
        class="mt-3 space-y-1 text-sm"
      >
        <li
          v-for="(a, i) in applied"
          :key="i"
          :class="a.error ? 'text-red-600 dark:text-red-400' : 'text-green-700 dark:text-green-400'"
        >
          {{ a.object }}: {{ a.error || a.action }}
        </li>
      </ul>
    </Card>
    <ViewDialog
      v-if="viewing"
      :title="viewing.name"
      :sections="[{ label: 'ClusterTemplate', object: viewing.template }]"
      @close="viewing = null"
    />
  </div>
</template>
