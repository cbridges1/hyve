// Which Cluster API provider a template's clusters run on, from its
// ClusterClass's infrastructure template kind: CivoClusterTemplate is
// "civo", UnraidClusterTemplate "unraid", DockerClusterTemplate "docker".
export function providerOfClass(cls: any): string {
  const ref = cls?.spec?.infrastructure?.templateRef || cls?.spec?.infrastructure?.ref || {};
  const kind: string = ref.kind || '';

  return kind.replace(/ClusterTemplate$/, '').toLowerCase();
}

export function classOf(template: any, classes: any[]): any {
  const ref = template?.spec?.clusterClassRef || {};
  const ns = ref.namespace || template?.metadata?.namespace;

  return classes.find((c: any) => c.metadata.namespace === ns && c.metadata.name === ref.name);
}

// The providers with a tile on Rancher's Create Cluster page.
export const TILES = [
  { id: 'civo', label: 'Civo' },
  { id: 'unraid', label: 'Unraid' },
];
