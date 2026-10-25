// Every page's path, so links are built one way.
import { typeOf, type Obj } from './kube'
import { TYPES } from './types'

const enc = encodeURIComponent

export const paths = {
  overview: '/overview',
  clusters: '/clusters',
  cluster: (namespace: string, name: string, tab?: 'resources' | 'explorer') =>
    `/clusters/${enc(namespace)}/${enc(name)}${tab ? `/${tab}` : ''}`,
  createCluster: (template?: { namespace: string; name: string }) =>
    `/clusters/new${template ? `?template=${enc(`${template.namespace}/${template.name}`)}` : ''}`,
  templates: '/templates',
  template: (namespace: string, name: string, edit = false) => `/templates/${enc(namespace)}/${enc(name)}${edit ? '?mode=edit' : ''}`,
  newTemplate: (namespace?: string) => `/templates/new${namespace ? `?namespace=${enc(namespace)}` : ''}`,
  resources: '/resources',
  resource: (namespace: string, name: string, edit = false) => `/resources/${enc(namespace)}/${enc(name)}${edit ? '?mode=edit' : ''}`,
  newResource: (namespace?: string) => `/resources/new${namespace ? `?namespace=${enc(namespace)}` : ''}`,
  matrix: '/matrix',
  secrets: '/secrets',
  repos: '/repos',
  providers: '/providers',
  users: '/users',
  environments: '/environments',
  access: '/access',
  import: '/import',
  explorer: '/explorer',
  // Any kind, as a list or as YAML.
  list: (type: string) => `/list/${enc(type)}`,
  yaml: (type: string, namespace: string | undefined, name: string, edit = false) =>
    `/yaml/${enc(type)}/${enc(namespace || '_')}/${enc(name)}${edit ? '?mode=edit' : ''}`,
  newYaml: (type: string, namespace?: string) => `/yaml/${enc(type)}/new${namespace ? `?namespace=${enc(namespace)}` : ''}`,
}

// Where an object is shown: its own page for kinds that have one, else as
// YAML.
export function detailPath(type: string, o: Obj): string {
  const { namespace, name } = o.metadata
  switch (type) {
    case TYPES.CLUSTER:
      return paths.cluster(namespace!, name)
    case TYPES.TEMPLATE:
      return paths.template(namespace!, name)
    case TYPES.RESOURCE:
      return paths.resource(namespace!, name)
    default:
      return paths.yaml(type, namespace, name)
  }
}

export function pathOf(o: Obj): string {
  const t = typeOf(o)
  return t ? detailPath(t, o) : paths.yaml(`${o.apiVersion}`, o.metadata.namespace, o.metadata.name)
}
