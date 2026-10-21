<script>
import jsyaml from 'js-yaml';
import Modal from './Modal.vue';
import ViewDialog from '@pkg/components/ViewDialog.vue';
import { CAPI_CLUSTER_CLASS, HYVE, LABELS } from '@pkg/config/types';
import { hostRequest } from '@pkg/utils/host';

// 'hyve render' for a template on this cluster: the Cluster and Fleet
// objects hyve would create for a cluster made from it, without making
// anything. The host renders them with the CLI's code, from the template,
// its ClusterClass and the shared Resources it refers to.
export default {
  name:       'HyveRenderDialog',
  components: { Modal, ViewDialog },
  props:      { template: { type: Object, required: true } },
  emits:      ['close'],

  async fetch() {
    const nss = await this.$store.dispatch('management/findAll', { type: 'namespace' }).catch(() => []);

    this.envs = nss.filter((n) => n.metadata?.labels?.[LABELS.ENVIRONMENT] === 'true').map((n) => n.metadata.name).sort();
    this.env = this.envs.includes('default') ? 'default' : (this.envs[0] || 'default');
  },

  data() {
    return {
      name: 'example', env: 'default', envs: [], sets: '', error: '', busy: false, result: null
    };
  },

  methods: {
    // name=value lines, as --set takes them: JSON values are parsed.
    variables() {
      const out = {};

      this.sets.split('\n').map((l) => l.trim()).filter(Boolean).forEach((l) => {
        const i = l.indexOf('=');

        if (i < 1) {
          throw new Error(`"${ l }": want name=value`);
        }
        const v = l.slice(i + 1);

        try {
          out[l.slice(0, i)] = JSON.parse(v);
        } catch (e) {
          out[l.slice(0, i)] = v;
        }
      });

      return out;
    },
    async render() {
      this.error = '';
      this.busy = true;
      try {
        const t = this.template;
        const ns = t.metadata.namespace;
        const docs = [t];
        const cls = t.spec?.clusterClassRef;

        if (cls?.name) {
          const c = await this.$store.dispatch('management/find', { type: CAPI_CLUSTER_CLASS, id: `${ cls.namespace || ns }/${ cls.name }` }).catch(() => null);

          if (c) {
            docs.push(c);
          }
        }
        for (const e of (t.spec?.resources || []).filter((x) => x.ref)) {
          const r = await this.$store.dispatch('management/find', { type: HYVE.RESOURCE, id: `${ e.refNamespace || ns }/${ e.ref }` }).catch(() => null);

          if (r) {
            docs.push(r);
          }
        }
        const yaml = docs.map((d) => jsyaml.dump(JSON.parse(JSON.stringify(d)), { lineWidth: -1 })).join('---\n');
        const res = await hostRequest(this.$store, 'render', {
          yaml, template: t.metadata.name, name: this.name || 'example', env: this.env, variables: this.variables()
        });

        this.result = {
          notes:    res.notes || [],
          sections: (res.objects || []).map((o) => ({ label: `${ o.kind } ${ o.metadata?.namespace }/${ o.metadata?.name }`, object: o })),
        };
      } catch (e) {
        this.error = e.message;
      } finally {
        this.busy = false;
      }
    },
  },
};
</script>

<template>
  <Modal
    v-if="!result"
    :title="`Preview a cluster from ${ template.metadata.name }`"
    @close="$emit('close')"
  >
    <form
      class="space-y-3"
      @submit.prevent="render"
    >
      <p class="text-sm text-neutral-500">
        What hyve would create, without creating it. Also <code>hyve render</code>.
      </p>
      <label class="block text-sm">
        <span class="mb-1 block text-neutral-600 dark:text-neutral-400">Cluster name</span>
        <input v-model="name">
      </label>
      <label class="block text-sm">
        <span class="mb-1 block text-neutral-600 dark:text-neutral-400">Environment</span>
        <select v-model="env">
          <option
            v-for="e in envs"
            :key="e"
            :value="e"
          >
            {{ e }}
          </option>
        </select>
      </label>
      <label class="block text-sm">
        <span class="mb-1 block text-neutral-600 dark:text-neutral-400">Variables (name=value, one a line)</span>
        <textarea
          v-model="sets"
          rows="3"
          class="font-mono text-xs"
          spellcheck="false"
        />
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
          @click="$emit('close')"
        >
          Cancel
        </button>
        <button
          type="submit"
          class="btn role-primary"
          :disabled="busy"
        >
          Preview
        </button>
      </div>
    </form>
  </Modal>
  <ViewDialog
    v-else
    :title="`${ name } from ${ template.metadata.name }`"
    :sections="result.sections"
    @close="$emit('close')"
  >
    <p
      v-for="n in result.notes"
      :key="n"
      class="mb-2 text-sm text-amber-700 dark:text-amber-400"
    >
      {{ n }}
    </p>
  </ViewDialog>
</template>
