// hyve's host API (internal/hostapi): what the UI needs hyve's own code
// for. It's there only in hyve's own UI; in Rancher, pages do without it.

export function onHyveHost(store: any): boolean {
  return !!store.getters['hyve/onHost'];
}

export function hostRequest(store: any, path: string, data: any): Promise<any> {
  return store.dispatch('hyve/host', { path, data });
}
