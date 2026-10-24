// Creating or updating hyve objects from YAML, as 'hyve template apply'
// does: ClusterTemplates, shared Resources and ClusterResources, each in
// its default namespace when it names none (templates and Resources in the
// template namespace, a ClusterResource in the environment).
import { loadAll } from 'js-yaml';
import { kube, newObject, save } from './kube';
import { validate } from './resources';
import { DEFAULT_TEMPLATE_NAMESPACE, TYPES } from './types';

const KINDS: Record<string, string> = {
  ClusterTemplate: TYPES.TEMPLATE,
  Resource:        TYPES.RESOURCE,
  ClusterResource: TYPES.CLUSTER_RESOURCE,
};

export interface Applied { object: string; action?: 'created' | 'updated'; error?: string }

export function parseDocs(text: string): any[] {
  return loadAll(text).filter((d: any) => d && typeof d === 'object');
}

// What's wrong with an object before it's sent, or ''.
function check(o: any): string {
  if (!(o.apiVersion || '').startsWith('hyve.io/') || !KINDS[o.kind]) {
    return "not a hyve ClusterTemplate, Resource or ClusterResource; apply it with kubectl";
  }
  if (!o.metadata?.name) {
    return 'it has no metadata.name';
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
