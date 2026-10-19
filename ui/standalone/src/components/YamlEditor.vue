<script>
import jsyaml from 'js-yaml';
import Loading from '../shims/Loading.vue';
import Card from './Card.vue';
import BackLink from './BackLink.vue';
import ConfirmButton from './ConfirmButton.vue';
import { DEFAULT_TEMPLATE_NAMESPACE, PRODUCT } from '@pkg/config/types';
import { detailRoute } from '../util';

// Any object as YAML: view, edit, or create. The plain way to manage what
// has no form of its own (shared resources, anything else).
export default {
  name:       'HyveYamlEditor',
  components: {
    Loading, Card, BackLink, ConfirmButton
  },
  props: {
    type: { type: String, required: true },
    id:   { type: String, default: '' },
    mode: { type: String, default: 'view' },
  },

  async fetch() {
    const s = this.schema;

    if (this.mode === 'create') {
      this.model = await this.$store.dispatch('management/create', {
        type:     this.type,
        metadata: { name: '', ...(s?.namespaced ? { namespace: this.$route.query.namespace || DEFAULT_TEMPLATE_NAMESPACE } : {}) },
        spec:     {},
      });
    } else {
      this.model = await this.$store.dispatch('management/find', { type: this.type, id: this.id });
    }
    this.text = this.dump();
  },

  data() {
    return {
      model: null, text: '', errors: [], saving: false
    };
  },

  computed: {
    schema() {
      return this.$store.getters['management/schemaFor'](this.type);
    },
    kind() {
      return this.schema?.kind || this.type;
    },
    editing() {
      return this.mode !== 'view';
    },
    can() {
      const v = this.schema?.verbs || [];

      return { update: v.includes('update'), delete: v.includes('delete') };
    },
    // Back to where the object is shown: its own page for kinds that have
    // one, otherwise its list.
    back() {
      if (this.$route.query.as === 'yaml' && this.model?.metadata?.name) {
        return { to: detailRoute(this.type, this.model), label: this.model.metadata.name };
      }

      return { to: { name: `${ PRODUCT }-c-cluster-resource`, params: { cluster: '_', resource: this.type } }, label: `${ this.kind } list` };
    },
  },

  methods: {
    dump() {
      const o = JSON.parse(JSON.stringify(this.model));

      if (o.metadata) {
        delete o.metadata.managedFields;
      }
      if (this.editing) {
        delete o.status;
      }

      return jsyaml.dump(o, { lineWidth: 120, noRefs: true });
    },
    async save() {
      this.errors = [];
      let obj;

      try {
        obj = jsyaml.load(this.text);
      } catch (e) {
        this.errors.push(`Not valid YAML: ${ e.message }`);

        return;
      }
      this.saving = true;
      try {
        if (this.model.status && !obj.status) {
          obj.status = this.model.status;
        }
        this.model.replaceWith(obj);
        await this.model.save();
        this.$router.push(detailRoute(this.type, this.model, this.$route.query.as ? { as: this.$route.query.as } : {}));
      } catch (e) {
        this.errors.push(e.message);
      } finally {
        this.saving = false;
      }
    },
    async remove() {
      try {
        await this.model.remove();
        this.$router.push({ name: `${ PRODUCT }-c-cluster-resource`, params: { cluster: '_', resource: this.type } });
      } catch (e) {
        this.errors.push(e.message);
      }
    },
    cancel() {
      if (this.mode === 'create') {
        this.$router.push(this.back.to);
      } else {
        this.$router.push({ query: { ...this.$route.query, mode: undefined } });
      }
    },
  },
};
</script>

<template>
  <Loading v-if="$fetchState.pending" />
  <div v-else-if="model">
    <BackLink
      :to="back.to"
      :label="back.label"
    />
    <div class="mb-4 flex flex-wrap items-center justify-between gap-3">
      <h1 class="mb-0">
        {{ mode === 'create' ? `New ${ kind }` : `${ kind } ${ id }` }}
      </h1>
      <div
        v-if="!editing"
        class="flex items-center gap-2"
      >
        <router-link
          v-if="can.update"
          class="btn role-primary"
          :to="{ query: { ...$route.query, mode: 'edit' } }"
        >
          Edit
        </router-link>
        <ConfirmButton
          v-if="can.delete"
          label="Delete"
          :confirm="`Delete ${ id }?`"
          @confirm="remove"
        />
      </div>
    </div>
    <p
      v-for="(e, i) in errors"
      :key="i"
      class="mb-3 rounded-lg bg-red-50 px-3 py-2 text-sm text-red-700 dark:bg-red-950/60 dark:text-red-300"
    >
      {{ e }}
    </p>
    <Card>
      <textarea
        v-if="editing"
        v-model="text"
        spellcheck="false"
        class="min-h-[28rem] font-mono text-xs"
      />
      <pre
        v-else
        class="max-h-[40rem] overflow-auto rounded-lg bg-neutral-50 p-3 font-mono text-xs text-neutral-700 dark:bg-neutral-950 dark:text-neutral-300"
      >{{ text }}</pre>
      <div
        v-if="editing"
        class="mt-4 flex justify-end gap-2"
      >
        <button
          type="button"
          class="btn role-tertiary"
          @click="cancel"
        >
          Cancel
        </button>
        <button
          type="button"
          class="btn role-primary"
          :disabled="saving"
          @click="save"
        >
          {{ mode === 'create' ? 'Create' : 'Save' }}
        </button>
      </div>
    </Card>
  </div>
</template>
