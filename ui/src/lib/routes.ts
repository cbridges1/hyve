// Every page's path, so links are built one way.
import type { Location } from 'react-router-dom'
import { schemaFor, typeOf, type Obj } from './kube'
import { LABELS, TYPES } from './types'

const enc = encodeURIComponent

export const paths = {
  overview: '/overview',
  clusters: '/clusters',
  cluster: (namespace: string, name: string, tab?: 'resources' | 'explorer') =>
    `/clusters/${enc(namespace)}/${enc(name)}${tab ? `/${tab}` : ''}`,
  // The host cluster, hyve's management cluster: local, as in the proxy's
  // paths and the CLI. It's in no environment.
  localCluster: (tab?: 'resources' | 'explorer') => `/clusters/local${tab ? `/${tab}` : ''}`,
  createCluster: (template?: { namespace: string; name: string }) =>
    `/clusters/new${template ? `?template=${enc(`${template.namespace}/${template.name}`)}` : ''}`,
  templates: '/templates',
  template: (namespace: string, name: string, edit = false) => `/templates/${enc(namespace)}/${enc(name)}${edit ? '?mode=edit' : ''}`,
  newTemplate: (namespace?: string) => `/templates/new${namespace ? `?namespace=${enc(namespace)}` : ''}`,
  resources: '/resources',
  resource: (namespace: string, name: string, edit = false) => `/resources/${enc(namespace)}/${enc(name)}${edit ? '?mode=edit' : ''}`,
  newResource: (namespace?: string) => `/resources/new${namespace ? `?namespace=${enc(namespace)}` : ''}`,
  secrets: '/secrets',
  repos: '/repos',
  repo: (namespace: string, name: string) => `/repos/${enc(namespace)}/${enc(name)}`,
  providers: '/providers',
  users: '/users',
  environments: '/environments',
  access: '/access',
  settings: '/settings',
  import: '/import',
  // Any kind, as a list or as YAML.
  list: (type: string) => `/list/${enc(type)}`,
  yaml: (type: string, namespace: string | undefined, name: string, edit = false) =>
    `/yaml/${enc(type)}/${enc(namespace || '_')}/${enc(name)}${edit ? '?mode=edit' : ''}`,
  newYaml: (type: string, namespace?: string) => `/yaml/${enc(type)}/new${namespace ? `?namespace=${enc(namespace)}` : ''}`,
}

// People (pages/People.tsx): Users when the host has its own, else Access.
export const peoplePath = () => (schemaFor(TYPES.USER) ? paths.users : paths.access)

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
    case TYPES.FLEET_GIT_REPO:
      return o.metadata.labels?.[LABELS.OWNER_KIND] ? paths.yaml(type, namespace, name) : paths.repo(namespace!, name)
    default:
      return paths.yaml(type, namespace, name)
  }
}

export function pathOf(o: Obj): string {
  const t = typeOf(o)
  return t ? detailPath(t, o) : paths.yaml(`${o.apiVersion}`, o.metadata.namespace, o.metadata.name)
}

// Where a page's back link goes when it was opened from another page (a
// bundle's YAML from a repo's or a cluster's page): a link passes
// backFrom(location, "Repo x") as its state, and BackLink goes there
// instead of its own default.
export type Back = { to: string; label: string }

export function backFrom(location: Location, label: string): { back: Back } {
  return { back: { to: location.pathname + location.search, label } }
}

export function backOf(location: Location): Back | undefined {
  const b = (location.state as { back?: Back } | null)?.back
  return b && typeof b.to === 'string' && typeof b.label === 'string' ? b : undefined
}
