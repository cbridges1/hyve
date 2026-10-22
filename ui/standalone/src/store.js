// The stand-in for Rancher's Vuex store: the few actions and getters the
// extension's pages use, over the Kubernetes API.
import {
  ApiError, discover, request, urlFor
} from './api';
import Model from './model';
import ClusterTemplate from '@pkg/models/hyve.io.clustertemplate.js';
import SharedResource from '@pkg/models/hyve.io.resource.js';

const MODELS = {
  'hyve.io.clustertemplate': ClusterTemplate,
  'hyve.io.resource':        SharedResource,
};

// How long a list is reused: one page load asks for the same kinds from
// several components.
const LIST_TTL = 3000;

export function createStore(t) {
  let schemas = {};
  const lists = new Map();
  // Told of each read, for live updates (live.js); set by main.js.
  let onRead = () => {};

  function schemaOrThrow(type) {
    const s = schemas[type];

    if (!s) {
      throw new Error(`The server doesn't serve ${ type }`);
    }

    return s;
  }

  function model(type, data) {
    const Cls = MODELS[type] || Model;

    return new Cls(data, type);
  }

  // The namespaces the user can work in, from the host, for when they
  // can't list namespaces themselves.
  let mine = null;

  function myNamespaces() {
    if (!mine) {
      mine = request('GET', '/v1/namespaces').then((r) => r.items || []);
      mine.catch(() => {
        mine = null;
      });
      setTimeout(() => {
        mine = null;
      }, 30000);
    }

    return mine;
  }

  const forbidden = (e) => e instanceof ApiError && e.status === 403;

  // One namespace's objects when namespace is set (Rancher's
  // opt.namespaced), else every namespace's.
  async function list(type, namespace) {
    const s = schemaOrThrow(type);
    const apiVersion = s.group ? `${ s.group }/${ s.version }` : s.version;
    const wrap = (items) => items.map((o) => model(type, { apiVersion, kind: s.kind, ...o }));

    if (namespace && s.namespaced) {
      return wrap((await request('GET', urlFor(s, namespace))).items || []);
    }
    try {
      return wrap((await request('GET', urlFor(s))).items || []);
    } catch (e) {
      if (!forbidden(e)) {
        throw e;
      }
    }
    // Kubernetes answers a list across namespaces all or nothing; Rancher
    // filters it by what the user can see. Do the same, one namespace at a
    // time.
    const nss = await myNamespaces();

    if (type === 'namespace') {
      return wrap(nss.map((n) => ({ metadata: { name: n.name, labels: n.labels || {} } })));
    }
    if (!s.namespaced) {
      return [];
    }
    const lists = await Promise.all(nss.map((n) => request('GET', urlFor(s, n.name)).then((r) => r.items || [], (e) => {
      if (forbidden(e) || e.status === 404) {
        return [];
      }
      throw e;
    })));

    return wrap(lists.flat());
  }

  const actions = {
    'management/findAll'({ type, opt }) {
      onRead(type, opt?.namespaced);
      const key = opt?.namespaced ? `${ type }@${ opt.namespaced }` : type;
      const hit = lists.get(key);

      if (hit && !opt?.force && Date.now() - hit.at < LIST_TTL) {
        return hit.promise;
      }
      const promise = list(type, opt?.namespaced);

      lists.set(key, { at: Date.now(), promise });
      promise.catch(() => lists.delete(key));

      return promise;
    },
    async 'management/find'({ type, id }) {
      const s = schemaOrThrow(type);
      const [ns, name] = s.namespaced ? id.split('/') : [undefined, id];

      onRead(type, ns, name);
      const o = await request('GET', urlFor(s, ns, name));

      return model(type, o);
    },
    'management/create'(obj) {
      const { type, ...data } = obj;
      const s = schemaOrThrow(type);

      return model(type, {
        apiVersion: s.group ? `${ s.group }/${ s.version }` : s.version,
        kind:       s.kind,
        metadata:   {},
        ...data,
      });
    },
    'management/request'({ url, method = 'GET', data }) {
      return request(method, url, data);
    },
    // The host's API for what needs hyve's own code (internal/hostapi).
    'hyve/host'({ path, data }) {
      return request('POST', `/v1/hyve/${ path }`, data);
    },
  };

  const store = {
    async load() {
      schemas = await discover();
    },
    onRead(fn) {
      onRead = fn;
    },
    myNamespaces,
    forget(type) {
      [...lists.keys()].filter((k) => k === type || k.startsWith(`${ type }@`)).forEach((k) => lists.delete(k));
    },
    get schemas() {
      return schemas;
    },
    dispatch(name, args) {
      const a = actions[name];

      if (!a) {
        return Promise.reject(new Error(`${ name } isn't available outside Rancher`));
      }
      try {
        return Promise.resolve(a(args || {}));
      } catch (e) {
        return Promise.reject(e);
      }
    },
    getters: {
      'management/schemaFor': (type) => schemas[type],
      // On hyve's host, not in Rancher: 'hyve/host' is there.
      'hyve/onHost':          true,
      'i18n/t':               (key, args) => t(key, args),
    },
  };

  return store;
}
