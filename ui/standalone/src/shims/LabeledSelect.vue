<script>
let n = 0;

// Options are strings or {label, value}. taggable lets the user type a
// value that isn't listed.
export default {
  name:         'LabeledSelect',
  inheritAttrs: false,
  props:        {
    value:    { type: [String, Number, Boolean, Object], default: '' },
    options:  { type: Array, default: () => [] },
    label:    { type: String, default: '' },
    required: { type: Boolean, default: false },
    disabled: { type: Boolean, default: false },
    taggable: { type: Boolean, default: false },
    mode:     { type: String, default: 'edit' },
    tooltip:  { type: String, default: '' },
  },
  emits: ['update:value'],
  data() {
    return { uid: `ls-${ ++n }` };
  },
  computed: {
    opts() {
      return this.options.map((o) => (o && typeof o === 'object' ? o : { label: String(o), value: o }));
    },
    current() {
      const o = this.opts.find((x) => x.value === this.value);

      return o ? o.label : this.value;
    },
  },
  methods: {
    pick(e) {
      const i = Number(e.target.value);

      this.$emit('update:value', i >= 0 ? this.opts[i].value : '');
    },
  },
};
</script>

<template>
  <div class="labeled">
    <label :for="uid">
      {{ label }}<span
        v-if="required && mode !== 'view'"
        class="required"
      >*</span>
      <span
        v-if="tooltip"
        class="hint"
        :title="tooltip"
      >?</span>
    </label>
    <div
      v-if="mode === 'view'"
      class="view-value"
    >
      {{ current || '—' }}
    </div>
    <template v-else-if="taggable">
      <input
        :id="uid"
        :value="value"
        :list="`${ uid }-list`"
        :disabled="disabled"
        @input="$emit('update:value', $event.target.value)"
      >
      <datalist :id="`${ uid }-list`">
        <option
          v-for="o in opts"
          :key="String(o.value)"
          :value="o.value"
        >
          {{ o.label }}
        </option>
      </datalist>
    </template>
    <select
      v-else
      :id="uid"
      :disabled="disabled"
      @change="pick"
    >
      <option
        value="-1"
        :selected="!opts.some((o) => o.value === value)"
        disabled
      >
        Choose…
      </option>
      <option
        v-for="(o, i) in opts"
        :key="String(o.value)"
        :value="i"
        :selected="o.value === value"
      >
        {{ o.label }}
      </option>
    </select>
  </div>
</template>
