// t() over the extension's strings (../pkg/hyve/l10n/en-us.yaml), with the
// few of Rancher's own it borrows. Rancher's messages are ICU; this handles
// what hyve's use: {name} and {count, plural, one {...} other {...}}.
import extension from '@pkg/l10n/en-us.yaml';

const RANCHER = {
  generic: {
    add: 'Add', cancel: 'Cancel', remove: 'Remove', save: 'Save', create: 'Create', edit: 'Edit', delete: 'Delete', close: 'Close', refresh: 'Refresh', loading: 'Loading…'
  },
  tableHeaders: { name: 'Name', namespace: 'Namespace' },
};

function merge(a, b) {
  const out = { ...a };

  Object.entries(b || {}).forEach(([k, v]) => {
    out[k] = v && typeof v === 'object' && !Array.isArray(v) && typeof out[k] === 'object' ? merge(out[k], v) : v;
  });

  return out;
}

const strings = merge(RANCHER, extension);

function lookup(key) {
  // Keys may contain dots in their last part (typeLabel's
  // "cluster.x-k8s.io.cluster"): take the longest match at each level.
  let node = strings;
  const parts = key.split('.');
  let i = 0;

  while (i < parts.length) {
    let found = false;

    for (let j = parts.length; j > i; j--) {
      const k = parts.slice(i, j).join('.');

      if (node && typeof node === 'object' && k in node) {
        node = node[k];
        i = j;
        found = true;
        break;
      }
    }
    if (!found) {
      return undefined;
    }
  }

  return typeof node === 'string' ? node : undefined;
}

function plural(msg, args) {
  return msg.replace(/\{\s*(\w+)\s*,\s*plural\s*,((?:[^{}]|\{[^{}]*\})*)\}/g, (_, name, body) => {
    const n = Number(args?.[name]);
    const forms = {};

    body.replace(/(=\d+|\w+)\s*\{([^{}]*)\}/g, (__, k, v) => {
      forms[k] = v;
    });
    const v = forms[`=${ n }`] ?? (n === 1 ? forms.one : undefined) ?? forms.other ?? '';

    return v.replace(/#/g, String(n)).trim();
  });
}

export function t(key, args) {
  const msg = lookup(key);

  if (msg === undefined) {
    return key;
  }

  return plural(msg, args).replace(/\{\s*(\w+)\s*\}/g, (m, name) => (args && name in args ? String(args[name]) : m));
}

export function exists(key) {
  return lookup(key) !== undefined;
}
