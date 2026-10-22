import jsyaml from 'js-yaml';
import { PRODUCT } from '@pkg/config/types';
import { request } from './api';
import { cliServer } from './session';

// How long ago an RFC 3339 time was, as kubectl shows it.
export function age(ts) {
  if (!ts) {
    return '';
  }
  const s = Math.max(0, Math.floor((Date.now() - Date.parse(ts)) / 1000));

  if (s < 120) {
    return `${ s }s`;
  }
  if (s < 7200) {
    return `${ Math.floor(s / 60) }m`;
  }
  if (s < 172800) {
    return `${ Math.floor(s / 3600) }h`;
  }

  return `${ Math.floor(s / 86400) }d`;
}

// A detail page, or one of its tabs (a namespaced object's only).
export function detailRoute(type, m, query, tab) {
  const md = m.metadata;

  return {
    name:   md.namespace ? `${ PRODUCT }-c-cluster-resource-namespace-id` : `${ PRODUCT }-c-cluster-resource-id`,
    params: {
      cluster: '_', resource: type, ...(md.namespace ? { namespace: md.namespace } : {}), id: md.name, ...(tab ? { tab } : {})
    },
    query,
  };
}

export function createRoute(type, query) {
  return { name: `${ PRODUCT }-c-cluster-resource-create`, params: { cluster: '_', resource: type }, query };
}

// A kubeconfig for a cluster through the host's proxy, as 'hyve
// kubeconfig' and 'hyve cluster kubeconfig' print it: it holds no secret,
// and runs 'hyve token' for the CLI's session with this host. id is
// "local" (the management cluster) or "<environment>.<name>".
export async function kubeconfig(id) {
  const server = cliServer();
  let ca = '';

  try {
    ca = (await request('GET', '/v3/settings/cacerts'))?.value || '';
  } catch (e) {}
  const host = new URL(server.url).hostname;
  const name = id === 'local' ? host : `${ host }-${ id }`;
  const cluster = { server: `${ server.url }/k8s/clusters/${ id }` };

  if (ca) {
    cluster['certificate-authority-data'] = btoa(ca);
  }

  return jsyaml.dump({
    apiVersion:        'v1',
    kind:              'Config',
    clusters:          [{ name, cluster }],
    users:             [{
      name: host,
      user: {
        exec: {
          apiVersion:         'client.authentication.k8s.io/v1',
          command:            'hyve',
          args:               ['token', '--server', server.name],
          interactiveMode:    'IfAvailable',
          provideClusterInfo: false,
          installHint:        'hyve is needed to use this kubeconfig: https://github.com/cbridges1/hyve',
        },
      },
    }],
    contexts:          [{ name, context: { cluster: name, user: host } }],
    'current-context': name,
  }, { lineWidth: -1 });
}

// Saves text as a file.
export function download(filename, text) {
  const a = document.createElement('a');

  a.href = URL.createObjectURL(new Blob([text], { type: 'application/yaml' }));
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  setTimeout(() => {
    URL.revokeObjectURL(a.href);
    a.remove();
  }, 0);
}
