// hyve resources in the UI, the same model as the CLI's internal/resources:
// an entry is Fleet's own HelmOp, Bundle or GitRepo spec (or a ref to a
// shared Resource), listed in a template's spec.resources or as a
// ClusterResource. hyve-controller turns each into a Fleet object owned by
// it; the UI only edits hyve's objects and reads Fleet's state.
import { Reader, typeOf, type Obj } from './kube';
import { ANNOTATIONS, LABELS, TYPES } from './types';

export const FLEET = {
  HELM_OP: TYPES.FLEET_HELM_OP,
  GIT_REPO: TYPES.FLEET_GIT_REPO,
  BUNDLE: TYPES.FLEET_BUNDLE,
  BUNDLE_DEPLOYMENT: TYPES.FLEET_BUNDLE_DEPLOYMENT,
  CLUSTER: TYPES.FLEET_CLUSTER,
};

export interface Entry {
  name?: string;
  namespace?: string;
  secrets?: string[];
  ref?: string;
  refNamespace?: string;
  values?: any;
  helmOp?: any;
  bundle?: any;
  gitRepo?: any;
}

export function kindOf(e: Entry): string {
  return e.helmOp ? 'HelmOp' : e.bundle ? 'Bundle' : e.gitRepo ? 'GitRepo' : e.ref ? 'Ref' : '';
}

export function describe(e: Entry): string {
  if (e.ref) {
    return `ref ${ e.ref }`;
  }
  if (e.helmOp) {
    const h = e.helmOp.helm || {};

    if (!h.chart) {
      return `${ h.repo || '' }${ h.version ? `@${ h.version }` : '' }`;
    }

    return `${ h.repo ? `${ h.repo } ` : '' }${ h.chart }${ h.version ? `@${ h.version }` : '' }`;
  }
  if (e.gitRepo) {
    const g = e.gitRepo;

    return `${ g.repo }${ g.branch ? `@${ g.branch }` : '' }${ g.paths?.length ? ` ${ g.paths.join(',') }` : '' }`;
  }
  if (e.bundle) {
    return `${ (e.bundle.resources || []).length } manifests`;
  }

  return '';
}

const NAME_RE = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/;

export function validate(e: Entry): string {
  const name = e.name || '';

  if (!NAME_RE.test(name) || name.length > 40) {
    return `Resource name "${ name }": lowercase letters, digits and dashes, at most 40`;
  }
  const kinds = [e.helmOp, e.bundle, e.gitRepo, e.ref].filter(Boolean).length;

  if (kinds !== 1) {
    return `Resource ${ name }: give exactly one of a Helm chart, git repo or existing resource`;
  }
  if (e.helmOp) {
    const h = e.helmOp.helm || {};

    if (!h.chart && !(h.repo || '').startsWith('oci://')) {
      return `Resource ${ name }: chart is required (or an oci:// repository)`;
    }
  }
  if (e.gitRepo && !e.gitRepo.repo) {
    return `Resource ${ name }: git repo is required`;
  }
  if (e.gitRepo && e.secrets?.length) {
    return `Resource ${ name }: a git repo can't copy secrets; list them in its fleet.yaml`;
  }

  return '';
}

// A Helm chart entry from simple fields; OCI charts go whole into repo, as
// Fleet wants them.
export function helmEntry(f: { name?: string; repo?: string; chart: string; version?: string; namespace?: string; secrets?: string[] }): Entry {
  let repo = f.repo || '';
  let chart = f.chart;

  if (chart.startsWith('oci://')) {
    repo = chart;
    chart = '';
  } else if (repo.startsWith('oci://') && chart) {
    repo = `${ repo.replace(/\/+$/, '') }/${ chart }`;
    chart = '';
  }
  const helm: any = {};

  if (repo) {
    helm.repo = repo;
  }
  if (chart) {
    helm.chart = chart;
  }
  if (f.version) {
    helm.version = f.version;
  }
  const e: Entry = { name: f.name || (f.chart.split('/').pop() || ''), helmOp: { helm } };

  if (f.namespace) {
    e.namespace = f.namespace;
  }
  if (f.secrets?.length) {
    e.secrets = f.secrets;
  }

  return e;
}

export interface Item { name: string; kind: string; source: string; ownerKind: string; owner: string; model: Obj; bundles?: Obj[] }

// hyve's Fleet objects owned by the given template or ClusterResources.
export async function listItems(store: Reader, owners: { kind: string; name: string; namespace: string }[]): Promise<Item[]> {
  const all = await Promise.all([FLEET.HELM_OP, FLEET.GIT_REPO, FLEET.BUNDLE].map((type) => store.findAll(type).catch(() => [])));
  const out: Item[] = [];
  // The Fleet bundles behind each object, which Fleet labels with its
  // name: a HelmOp's or a GitRepo's; or a Bundle itself.
  const bundlesOf = (m: any) => {
    if (typeOf(m) === FLEET.BUNDLE) {
      return [m];
    }
    const label = typeOf(m) === FLEET.GIT_REPO ? 'fleet.cattle.io/repo-name' : 'fleet.cattle.io/fleet-helm-name';

    return all[2].filter((b: any) => b.metadata.namespace === m.metadata.namespace && b.metadata.labels?.[label] === m.metadata.name);
  };

  all.flat().forEach((m: any) => {
    const l = m.metadata?.labels || {};

    if (l[LABELS.MANAGED_BY] !== 'hyve' || !l[LABELS.RESOURCE]) {
      return;
    }
    if (typeOf(m) === FLEET.BUNDLE && (l['fleet.cattle.io/repo-name'] || (m.metadata.ownerReferences || []).some((r: any) => !String(r.apiVersion).startsWith('hyve.io/')))) {
      return;
    }
    if (!owners.some((o) => o.kind === l[LABELS.OWNER_KIND] && o.name === l[LABELS.OWNER] && o.namespace === m.metadata.namespace)) {
      return;
    }
    out.push({
      name:      l[LABELS.RESOURCE],
      kind:      m.kind || '',
      source:    m.metadata.annotations?.[ANNOTATIONS.SOURCE] || '',
      ownerKind: l[LABELS.OWNER_KIND],
      owner:     l[LABELS.OWNER],
      model:     m,
      bundles:   bundlesOf(m),
    });
  });

  return out.sort((a, b) => `${ a.ownerKind }${ a.name }`.localeCompare(`${ b.ownerKind }${ b.name }`));
}

// What hyve delivers to one CAPI cluster: its template's resources and its
// own extras.
export async function clusterItems(store: Reader, capi: any): Promise<Item[]> {
  const l = capi.metadata?.labels || {};
  const owners: { kind: string; name: string; namespace: string }[] = [];

  if (l[LABELS.TEMPLATE]) {
    owners.push({ kind: 'ClusterTemplate', name: l[LABELS.TEMPLATE], namespace: l[LABELS.TEMPLATE_NAMESPACE] || capi.metadata.namespace });
  }
  const crs = await store.findAll(TYPES.CLUSTER_RESOURCE).catch(() => [] as Obj[]);

  crs.filter((cr: any) => cr.metadata.namespace === capi.metadata.namespace && cr.spec?.clusterName === capi.metadata.name)
    .forEach((cr: any) => owners.push({ kind: 'ClusterResource', name: cr.metadata.name, namespace: cr.metadata.namespace }));

  return listItems(store, owners);
}

// A Fleet Cluster's own summary, for users who can't read its
// BundleDeployments (they live in Fleet's per-cluster namespace): every
// bundle is Ready except the ones it lists as not ready.
const SUMMARY = '__summary';

function fromSummary(fc: any, out: Record<string, string>) {
  const sum = fc?.status?.summary;

  if (!sum) {
    return;
  }
  out[SUMMARY] = `${ fc.metadata.namespace }/${ fc.metadata.name }`;
  (sum.nonReadyResources || []).forEach((r: any) => {
    out[`nonready:${ fc.metadata.namespace }/${ r.name }`] = r.bundleState || 'NotReady';
  });
}

// "<bundle namespace>/<bundle name>" -> BundleDeployment state on the Fleet
// cluster CAAPF registered for a CAPI cluster (same name and namespace);
// GitRepo bundles roll up under "repo:<namespace>/<name>".
export async function clusterStates(store: Reader, ns: string, name: string): Promise<Record<string, string> | null> {
  const fc = await store.find(FLEET.CLUSTER, ns, name).catch(() => null);
  const bdNS = fc?.status?.namespace;

  if (!bdNS) {
    return null;
  }
  const bds = await store.findAll(FLEET.BUNDLE_DEPLOYMENT).catch(() => [] as Obj[]);
  const out: Record<string, string> = {};

  fromSummary(fc, out);

  bds.filter((bd: any) => bd.metadata.namespace === bdNS).forEach((bd: any) => {
    const l = bd.metadata.labels || {};
    const state = bd.status?.display?.state || (bd.status?.ready ? 'Ready' : 'Pending');
    const key = `${ l['fleet.cattle.io/bundle-namespace'] }/${ l['fleet.cattle.io/bundle-name'] }`;

    out[key] = state;
    const repo = l['fleet.cattle.io/repo-name'];

    if (repo) {
      const rk = `repo:${ l['fleet.cattle.io/bundle-namespace'] }/${ repo }`;
      const cur = out[rk];

      out[rk] = !cur || cur === 'Ready' ? state : cur;
    }
  });

  return out;
}

// A Fleet object's error: a False condition's message, or "".
export function problem(m: any): string {
  if (m?.status?.display?.error && m.status.display.message) {
    return `Error: ${ m.status.display.message }`;
  }
  // Ready=False is progress (a rollout); the others are real errors.
  const c = (m?.status?.conditions || []).find((x: any) => x.status === 'False' && x.type !== 'Ready' && x.message);

  return c ? `${ c.type }: ${ c.message }` : '';
}

export function stateOn(it: Item, states: Record<string, string> | null): string {
  const p = problem(it.model);

  if (p) {
    return p;
  }
  if (!states) {
    return '';
  }
  const key = `${ it.model.metadata.namespace }/${ it.model.metadata.name }`;
  const direct = it.kind === 'GitRepo' ? states[`repo:${ key }`] : states[key];

  if (direct || !states[SUMMARY]) {
    return direct || '';
  }
  // Only the Fleet Cluster's summary: what it lists as not ready (a
  // GitRepo's bundles are named "<repo>-<path>"), else what each bundle's
  // own summary says about this cluster. No bundle yet: Fleet hasn't
  // made it.
  const nonReady = Object.keys(states).find((k) => k === `nonready:${ key }` || (it.kind === 'GitRepo' && k.startsWith(`nonready:${ key }-`)));

  if (nonReady) {
    return states[nonReady];
  }
  if (!it.bundles?.length) {
    return 'Pending';
  }
  for (const b of it.bundles) {
    const r = (b.status?.summary?.nonReadyResources || []).find((x: any) => x.name === states[SUMMARY]);

    if (r) {
      return r.bundleState || 'NotReady';
    }
  }

  return 'Ready';
}

// A ClusterResource object for an extra on one cluster.
export function clusterResourceObject(capi: any, e: Entry): Obj {
  const { name, ...spec } = e;

  return {
    metadata: {
      name:      `${ capi.metadata.name }-${ name }`,
      namespace: capi.metadata.namespace,
      labels:    { [LABELS.CLUSTER]: capi.metadata.name, [LABELS.RESOURCE]: name || "", [LABELS.CLUSTER_INDEXED]: 'true' },
    },
    spec: { ...spec, clusterName: capi.metadata.name },
  };
}

// Fleet's states for every CAPI cluster at once: "<ns>/<name>" -> the same
// map clusterStates gives one cluster (missing until CAAPF registers it).
export async function allClusterStates(store: Reader): Promise<Record<string, Record<string, string>>> {
  const [fcs, bds] = await Promise.all([
    store.findAll(FLEET.CLUSTER).catch(() => [] as Obj[]),
    store.findAll(FLEET.BUNDLE_DEPLOYMENT).catch(() => [] as Obj[]),
  ]);
  const byNS: Record<string, string> = {};

  fcs.forEach((fc: any) => {
    if (fc.status?.namespace) {
      byNS[fc.status.namespace] = `${ fc.metadata.namespace }/${ fc.metadata.name }`;
    }
  });
  const out: Record<string, Record<string, string>> = {};

  fcs.forEach((fc: any) => {
    fromSummary(fc, out[`${ fc.metadata.namespace }/${ fc.metadata.name }`] = {});
  });
  bds.forEach((bd: any) => {
    const cluster = byNS[bd.metadata.namespace];

    if (!cluster) {
      return;
    }
    const states = out[cluster] || (out[cluster] = {});
    const l = bd.metadata.labels || {};
    const state = bd.status?.display?.state || (bd.status?.ready ? 'Ready' : 'Pending');

    states[`${ l['fleet.cattle.io/bundle-namespace'] }/${ l['fleet.cattle.io/bundle-name'] }`] = state;
    const repo = l['fleet.cattle.io/repo-name'];

    if (repo) {
      const rk = `repo:${ l['fleet.cattle.io/bundle-namespace'] }/${ repo }`;
      const cur = states[rk];

      states[rk] = !cur || cur === 'Ready' ? state : cur;
    }
  });

  return out;
}

// Every template's and ClusterResource's Fleet objects, with the clusters
// each reaches: a template's go to every cluster made from it, a
// ClusterResource's to its one cluster.
export async function allItems(store: Reader): Promise<{ items: Item[]; clusters: Obj[]; templates: Obj[]; clusterResources: Obj[]; reaches:(it: Item) => Obj[] }> {
  const [clusters, templates, clusterResources] = await Promise.all([
    store.findAll(TYPES.CLUSTER).catch(() => [] as Obj[]),
    store.findAll(TYPES.TEMPLATE).catch(() => [] as Obj[]),
    store.findAll(TYPES.CLUSTER_RESOURCE).catch(() => [] as Obj[]),
  ]);
  const owners = [
    ...templates.map((t: any) => ({ kind: 'ClusterTemplate', name: t.metadata.name, namespace: t.metadata.namespace })),
    ...clusterResources.map((cr: any) => ({ kind: 'ClusterResource', name: cr.metadata.name, namespace: cr.metadata.namespace })),
  ];
  const items = await listItems(store, owners);
  const reaches = (it: Item) => {
    if (it.ownerKind === 'ClusterTemplate') {
      return clusters.filter((c: any) => {
        const l = c.metadata?.labels || {};

        return l[LABELS.TEMPLATE] === it.owner && (l[LABELS.TEMPLATE_NAMESPACE] || c.metadata.namespace) === it.model.metadata.namespace;
      });
    }
    const cr = clusterResources.find((x: any) => x.metadata.name === it.owner && x.metadata.namespace === it.model.metadata.namespace);

    return cr ? clusters.filter((c: any) => c.metadata.namespace === cr.metadata.namespace && c.metadata.name === cr.spec?.clusterName) : [];
  };

  return {
    items, clusters, templates, clusterResources, reaches
  };
}

// A Fleet state as a badge colour.
export function stateColor(state: string): string {
  if (!state || state === '-') {
    return 'text-muted';
  }
  if (state === 'Ready') {
    return 'text-success';
  }
  if (/^(Error|ErrApplied|Modified|OutOfSync|NotReady)|^[A-Z][a-zA-Z]*: /.test(state)) {
    return 'text-error';
  }

  return 'text-warning';
}
