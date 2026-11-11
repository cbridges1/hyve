// What the template and shared Resource pages show of their objects (the
// old ClusterTemplate and Resource models' getters).
import type { Obj } from './kube'

// hyve-controller's Ready condition: True, or why not.
export function readyDisplay(t: Obj): string {
  const c = (t.status?.conditions || []).find((x: any) => x.type === 'Ready')
  if (!c) return '-'
  return c.status === 'True' ? 'Ready' : c.message || c.reason || 'Not ready'
}

// One line under a template's name when it has no description.
export function templateSummary(t: Obj): string {
  const s = t.spec || {}
  return [
    s.clusterClassRef?.name && `class ${s.clusterClassRef.name}`,
    s.kubernetesVersion,
    s.schedule && `expires ${s.schedule}`,
    `${(s.resources || []).length} resources`,
  ]
    .filter(Boolean)
    .join(' · ')
}
