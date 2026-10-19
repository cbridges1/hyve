// Rancher's create-edit-view mixin, as far as the extension's editors use
// it: a value and mode from the page, errors, and where to go when done.
import { _CREATE, _VIEW } from './query-params';

export default {
  props: {
    value: { type: Object, required: true },
    mode:  { type: String, default: _CREATE },
  },
  data() {
    return { errors: [] };
  },
  computed: {
    isCreate() {
      return this.mode === _CREATE;
    },
    doneRoute() {
      return { name: 'hyve-c-cluster-resource', params: { cluster: '_', resource: this.value.type } };
    },
  },
  methods: {
    done() {
      if (this.mode === _CREATE || this.mode === _VIEW) {
        this.$router.push(this.doneRoute);
      } else {
        this.$router.push({ name: 'hyve-c-cluster-resource-namespace-id', params: { cluster: '_', resource: this.value.type, namespace: this.value.metadata.namespace, id: this.value.metadata.name } });
      }
    },
    // Rancher runs these before saving; the extension's editors register
    // none that matter here.
    registerBeforeHook() {},
  },
};
