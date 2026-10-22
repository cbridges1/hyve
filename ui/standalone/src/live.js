// Live updates: what the current page has read is watched through the
// host's /v1/hyve/watch (Kubernetes watches run as the user, multiplexed
// onto one server-sent event stream), and when something changes the
// page's components that opt in (a `live: true` option, or a liveRefresh()
// hook) load their data again, quietly. Rancher does the same with its
// websocket; this is the standalone UI's version.
//
// Watches follow reads: the store reports each kind (and namespace, and
// name) a page reads with 'interest', and a navigation starts over. So a
// page that reads Secrets one namespace at a time is told about those
// namespaces only.

// How long to gather changes before refreshing, and the least time between
// two refreshes of one component.
const SETTLE = 400;
const MIN_GAP = 1500;
// The host's limit per stream.
const MAX = 32;

export function createLive(store) {
  // key -> {type, namespace, name}; a key is type@namespace#name.
  const interest = new Map();
  // Whole-kind watches the user may not run: watched per namespace instead.
  const denied = new Set();
  const listeners = new Set();
  let source = null;
  let targets = [];
  let reconnect = null;
  let changed = new Set();
  let notify = null;

  const keyOf = (type, namespace = '', name = '') => `${ type }@${ namespace }#${ name }`;

  function target(t) {
    const s = store.schemas[t.type];

    return s && `${ s.group }/${ s.version }/${ s.resource }/${ s.namespaced ? t.namespace : '' }/${ t.name }`;
  }

  function fire() {
    const types = changed;

    changed = new Set();
    notify = null;
    types.forEach((type) => store.forget(type));
    listeners.forEach((fn) => fn(types));
  }

  function changedType(type) {
    changed.add(type);
    if (!notify) {
      notify = setTimeout(fire, SETTLE);
    }
  }

  async function expandDenied(t) {
    denied.add(t.type);
    const nss = await store.myNamespaces().catch(() => []);

    nss.forEach((n) => add(t.type, n.name));
    schedule();
  }

  function connect() {
    reconnect = null;
    const wanted = [];

    interest.forEach((t) => {
      if (!t.namespace && !t.name && denied.has(t.type)) {
        return;
      }
      const w = target(t);

      if (w && !wanted.some((x) => x.w === w)) {
        wanted.push({ w, t });
      }
    });
    wanted.splice(MAX);
    const same = wanted.length === targets.length && wanted.every((x, i) => x.w === targets[i].w);

    if (same && source) {
      return;
    }
    source?.close();
    source = null;
    targets = wanted;
    if (!targets.length) {
      return;
    }
    const url = `/v1/hyve/watch?${ targets.map((x) => `w=${ encodeURIComponent(x.w) }`).join('&') }`;
    let opened = false;

    source = new EventSource(url, { withCredentials: true });
    // A reconnect after a drop may have missed changes.
    source.onopen = () => {
      if (opened) {
        targets.forEach((x) => changedType(x.t.type));
      }
      opened = true;
    };
    source.onmessage = (m) => {
      let ev;

      try {
        ev = JSON.parse(m.data);
      } catch (e) {
        return;
      }
      const x = targets[ev.w];

      if (!x) {
        return;
      }
      if (ev.type === 'ERROR') {
        if (!x.t.namespace && !x.t.name && !denied.has(x.t.type)) {
          expandDenied(x.t);
        }

        return;
      }
      changedType(x.t.type);
    };
  }

  function schedule() {
    if (!reconnect) {
      reconnect = setTimeout(connect, 300);
    }
  }

  function add(type, namespace, name) {
    const key = keyOf(type, namespace, name);

    if (!interest.has(key)) {
      interest.set(key, { type, namespace: namespace || '', name: name || '' });
      schedule();
    }
  }

  return {
    // A read: findAll (type, maybe a namespace) or find (type, id).
    interest(type, namespace, name) {
      if (type) {
        add(type, namespace, name);
      }
    },
    // A new page: forget the old one's reads.
    reset() {
      interest.clear();
      schedule();
    },
    subscribe(fn) {
      listeners.add(fn);

      return () => listeners.delete(fn);
    },
    stop() {
      source?.close();
      source = null;
      targets = [];
    },
  };
}

// The component side, as a mixin: components with `live: true` re-run
// fetch() when something the page reads changes; a liveRefresh() option
// replaces fetch() for that (to keep what the user is typing). No spinner:
// $fetchState.pending isn't touched.
export function liveMixin(live) {
  return {
    mounted() {
      const opts = this.$options;
      const refresh = typeof opts.liveRefresh === 'function' ? opts.liveRefresh : (opts.live && opts.fetch);

      if (typeof refresh !== 'function') {
        return;
      }
      let running = false;
      let again = false;
      let last = 0;
      let timer = null;
      const run = async() => {
        timer = null;
        if (running) {
          again = true;

          return;
        }
        running = true;
        last = Date.now();
        try {
          await refresh.call(this);
        } catch (e) {
          console.warn('live refresh', e); // eslint-disable-line no-console
        } finally {
          running = false;
          if (again) {
            again = false;
            queue();
          }
        }
      };
      const queue = () => {
        if (!timer) {
          timer = setTimeout(run, Math.max(0, MIN_GAP - (Date.now() - last)));
        }
      };

      this.hyveLiveOff = live.subscribe(() => {
        if (!this.$fetchState?.pending) {
          queue();
        }
      });
      this.hyveLiveTimer = () => clearTimeout(timer);
    },
    beforeUnmount() {
      this.hyveLiveOff?.();
      this.hyveLiveTimer?.();
    },
  };
}
