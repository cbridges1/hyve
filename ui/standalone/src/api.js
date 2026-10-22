// Talks to hyve's host: sign-in under /v1 and /v1-public, and the
// management cluster's API at /k8s/clusters/local as the signed-in user.
// The session is an HttpOnly cookie; every request carries X-Hyve-UI, which
// the host requires of cookie-authenticated writes.

export const LOCAL = '/k8s/clusters/local';

export class ApiError extends Error {
  constructor(status, message, body) {
    super(message);
    this.status = status;
    this.body = body;
  }
}

let onUnauthorized = () => {};

export function setUnauthorizedHandler(fn) {
  onUnauthorized = fn;
}

export async function request(method, url, body, { quiet401 = false, headers = {} } = {}) {
  const res = await fetch(url, {
    method,
    credentials: 'same-origin',
    headers:     {
      Accept:      'application/json',
      'X-Hyve-UI': '1',
      ...(body !== undefined ? { 'Content-Type': 'application/json' } : {}),
      ...headers,
    },
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  const text = await res.text();
  let data = null;

  try {
    data = text ? JSON.parse(text) : null;
  } catch (e) {
    data = text;
  }
  if (!res.ok) {
    if (res.status === 401 && !quiet401) {
      onUnauthorized();
    }
    // A Kubernetes Status, or the host's {code, message}.
    const message = (data && (data.message || data.code)) || `${ res.status } ${ res.statusText }`;

    throw new ApiError(res.status, message, data);
  }

  return data;
}

// Sign-in.
export const auth = {
  me:       () => request('GET', '/v1/me', undefined, { quiet401: true }),
  login:    (username, password, newPassword) => request('POST', '/v1-public/login', {
    type: 'localProvider', responseType: 'cookie', username, password, ...(newPassword ? { newPassword } : {})
  }, { quiet401: true }),
  redeem:   (token) => request('POST', '/v1-public/ui-session', { token }, { quiet401: true }),
  // Local mode: the host signs a browser on its machine in without a
  // password (404 elsewhere).
  local:    () => request('POST', '/v1-public/local-session', {}, { quiet401: true }),
  logout:   () => request('POST', '/v1/logout', {}, { quiet401: true }),
  password: (username, currentPassword, newPassword) => request('PUT', `/v1/users/${ encodeURIComponent(username) }/password`, { currentPassword, newPassword }),
};

// Rancher's type IDs, which the extension's pages use: the core group's
// singular name ("namespace"), or "<group>.<singular>"
// ("cluster.x-k8s.io.cluster").
function typeId(group, singular) {
  return group ? `${ group }.${ singular }` : singular;
}

// Discovery: every served kind, by type ID, at its preferred version.
export async function discover() {
  const out = {};
  const add = (group, version, r) => {
    if (r.resource?.includes('/') || r.name?.includes('/')) {
      return;
    }
    const resource = r.resource || r.name;
    const kind = r.responseKind?.kind || r.kind;
    const singular = r.singularResource || r.singularName || kind.toLowerCase();
    const id = typeId(group, singular);

    if (out[id]) {
      return;
    }
    out[id] = {
      id,
      group,
      version,
      resource,
      kind,
      namespaced: r.scope ? r.scope === 'Namespaced' : !!r.namespaced,
      verbs:      r.verbs || [],
    };
  };

  // Aggregated discovery (Kubernetes 1.30+): two requests for everything.
  const accept = 'application/json;g=apidiscovery.k8s.io;v=v2;as=APIGroupDiscoveryList,application/json;g=apidiscovery.k8s.io;v=v2beta1;as=APIGroupDiscoveryList';

  try {
    const [core, groups] = await Promise.all([
      request('GET', `${ LOCAL }/api`, undefined, { headers: { Accept: accept } }),
      request('GET', `${ LOCAL }/apis`, undefined, { headers: { Accept: accept } }),
    ]);

    if (core?.kind !== 'APIGroupDiscoveryList' || groups?.kind !== 'APIGroupDiscoveryList') {
      throw new Error('no aggregated discovery');
    }
    [...core.items, ...groups.items].forEach((g) => {
      const v = g.versions?.[0];

      (v?.resources || []).forEach((r) => add(g.metadata?.name || '', v.version, r));
    });

    return out;
  } catch (e) {
    if (e instanceof ApiError && e.status === 401) {
      throw e;
    }
  }

  // The older way: one request per group.
  const core = await request('GET', `${ LOCAL }/api/v1`);

  core.resources.forEach((r) => add('', 'v1', r));
  const groups = await request('GET', `${ LOCAL }/apis`);

  await Promise.all(groups.groups.map(async(g) => {
    const v = g.preferredVersion.version;
    const list = await request('GET', `${ LOCAL }/apis/${ g.name }/${ v }`).catch(() => null);

    (list?.resources || []).forEach((r) => add(g.name, v, r));
  }));

  return out;
}

// The URL of a kind's collection (in ns, or across namespaces), or of one
// object.
export function urlFor(schema, namespace, name) {
  const base = schema.group ? `${ LOCAL }/apis/${ schema.group }/${ schema.version }` : `${ LOCAL }/api/${ schema.version }`;
  const ns = schema.namespaced && namespace ? `/namespaces/${ encodeURIComponent(namespace) }` : '';
  const n = name ? `/${ encodeURIComponent(name) }` : '';

  return `${ base }${ ns }/${ schema.resource }${ n }`;
}
