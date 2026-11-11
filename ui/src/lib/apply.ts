// Creating or updating objects from YAML, as 'hyve apply' does:
// ClusterTemplates, Deployments (Fleet GitRepos, HelmOps, Bundles) and
// ClusterGroups, in the template namespace when they name none, and
// ClusterClasses with their provider templates, beside the templates.
// Older templates' resources, shared Resources and ClusterResources are
// still taken; hyve-controller turns them into Deployments.
import { loadAll } from 'js-yaml';
import { ApiError, LOCAL, request } from './api';
import { allSchemas, kube, loadSchemas, newObject, save } from './kube';
import { validate } from './resources';
import { DEFAULT_TEMPLATE_NAMESPACE, TYPES } from './types';

const KINDS: Record<string, string> = {
  ClusterTemplate: TYPES.TEMPLATE,
  Resource:        TYPES.RESOURCE,
  ClusterResource: TYPES.CLUSTER_RESOURCE,
  GitRepo:         TYPES.FLEET_GIT_REPO,
  HelmOp:          TYPES.FLEET_HELM_OP,
  Bundle:          TYPES.FLEET_BUNDLE,
  ClusterGroup:    'fleet.cattle.io.clustergroup',
};

const FLEET_KINDS = new Set(['GitRepo', 'HelmOp', 'Bundle', 'ClusterGroup']);

export interface Applied { object: string; action?: 'created' | 'updated'; error?: string }

export function parseDocs(text: string): any[] {
  return loadAll(text).filter((d: any) => d && typeof d === 'object');
}

// A ClusterClass, or a provider template one refers to
// (CivoClusterTemplate, KubeadmControlPlaneTemplate...).
export function isClassObject(o: any): boolean {
  const [group] = (o.apiVersion || '').split('/');

  if (group === 'cluster.x-k8s.io') {
    return o.kind === 'ClusterClass';
  }

  return group.endsWith('.cluster.x-k8s.io') && (o.kind || '').endsWith('Template');
}

// Server-side applies a ClusterClass object, at the version it's written in.
async function applyClass(o: any, ns: string): Promise<'created' | 'updated'> {
  const [group, version] = o.apiVersion.split('/');
  const find = () => Object.values(allSchemas()).find((x) => x.group === group && x.kind === o.kind);
  let s = find();

  if (!s) {
    // A provider added since the page loaded.
    await loadSchemas();
    s = find();
  }

  if (!s) {
    throw new Error(`the cluster doesn't serve ${ o.kind }: install its provider first`);
  }
  const url = `${ LOCAL }/apis/${ group }/${ version }/namespaces/${ encodeURIComponent(ns) }/${ s.resource }/${ encodeURIComponent(o.metadata.name) }`;
  const existed = await request('GET', url).then(() => true, (e) => {
    if (e instanceof ApiError && e.status === 404) {
      return false;
    }
    throw e;
  });
  const body = { ...o, metadata: { ...o.metadata, namespace: ns } };

  await request('PATCH', `${ url }?fieldManager=hyve-ui&force=true`, JSON.stringify(body), { contentType: 'application/apply-patch+yaml' });

  return existed ? 'updated' : 'created';
}

// What's wrong with an object before it's sent, or ''.
function check(o: any): string {
  const group = (o.apiVersion || '').split('/')[0];

  if (!['hyve.io', 'fleet.cattle.io'].includes(group) || !KINDS[o.kind] || (group === 'fleet.cattle.io') !== FLEET_KINDS.has(o.kind)) {
    return 'not a ClusterTemplate, a Deployment (Fleet GitRepo, HelmOp or Bundle), a ClusterGroup or a ClusterClass; apply it with kubectl';
  }
  if (!o.metadata?.name) {
    return 'it has no metadata.name';
  }
  if (FLEET_KINDS.has(o.kind)) {
    return '';
  }
  const entries = o.kind === 'ClusterTemplate' ? (o.spec?.resources || []) : [{ ...(o.spec || {}), name: o.metadata.labels?.['hyve.io/resource'] || o.metadata.name }];

  for (const e of entries) {
    if (e.bundle?.path) {
      return `resource ${ e.name }: 'bundle: {path: ...}' reads files next to the YAML, which only the CLI can do`;
    }
    const bad = validate(o.kind === 'ClusterTemplate' ? e : { ...e, name: 'x' });

    if (bad) {
      return bad;
    }
  }

  return '';
}

export async function applyObjects(objs: any[], opts: { templateNamespace?: string; environment?: string } = {}): Promise<Applied[]> {
  const out: Applied[] = [];

  for (const o of objs) {
    const ns = o.metadata?.namespace || (o.kind === 'ClusterResource' ? (opts.environment || 'default') : (opts.templateNamespace || DEFAULT_TEMPLATE_NAMESPACE));
    const label = `${ o.kind || '?' } ${ ns }/${ o.metadata?.name || '?' }`;

    if (isClassObject(o)) {
      try {
        if (!o.metadata?.name) {
          throw new Error('it has no metadata.name');
        }
        out.push({ object: label, action: await applyClass(o, ns) });
      } catch (e: any) {
        out.push({ object: label, error: e?.message || String(e) });
      }
      continue;
    }
    const bad = check(o);

    if (bad) {
      out.push({ object: label, error: bad });
      continue;
    }
    const type = KINDS[o.kind];

    try {
      const have = await kube.find(type, ns, o.metadata.name).catch((e: any) => {
        if (e?.status === 404) {
          return null;
        }
        throw e;
      });

      if (have) {
        have.spec = o.spec || {};
        have.metadata.labels = { ...(have.metadata.labels || {}), ...(o.metadata.labels || {}) };
        have.metadata.annotations = { ...(have.metadata.annotations || {}), ...(o.metadata.annotations || {}) };
        await save(type, have);
        out.push({ object: label, action: 'updated' });
      } else {
        const m = newObject(type, {
          metadata: {
            name: o.metadata.name, namespace: ns, labels: o.metadata.labels, annotations: o.metadata.annotations
          },
          spec: o.spec || {},
        });

        await save(type, m);
        out.push({ object: label, action: 'created' });
      }
    } catch (e: any) {
      out.push({ object: label, error: e?.message || String(e) });
    }
  }

  return out;
}
