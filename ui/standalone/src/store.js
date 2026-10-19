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

  async function list(type) {
    const s = schemaOrThrow(type);
    const apiVersion = s.group ? `${ s.group }/${ s.version }` : s.version;
    const wrap = (items) => items.map((o) => model(type, { apiVersion, kind: s.kind, ...o }));

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
      const hit = lists.get(type);

      if (hit && !opt?.force && Date.now() - hit.at < LIST_TTL) {
        return hit.promise;
      }
      const promise = list(type);

      lists.set(type, { at: Date.now(), promise });
      promise.catch(() => lists.delete(type));

      return promise;
    },
    async 'management/find'({ type, id }) {
      const s = schemaOrThrow(type);
      const [ns, name] = s.namespaced ? id.split('/') : [undefined, id];
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
  };

  const store = {
    async load() {
      schemas = await discover();
    },
    forget(type) {
      lists.delete(type);
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
      'i18n/t':               (key, args) => t(key, args),
    },
  };

  return store;
}
