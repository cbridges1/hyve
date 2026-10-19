import { IPlugin } from '@shell/core/types';
import { BLANK_CLUSTER } from '@shell/store/store-types.js';
import {
  ANNOTATIONS, CAPI_CLUSTER, HYVE, LABELS, PRODUCT
} from './config/types';

export function init($plugin: IPlugin, store: any) {
  const {
    product, configureType, basicType, virtualType, headers, weightType
  } = $plugin.DSL(store, PRODUCT);

  product({
    icon:                'globe',
    inStore:             'management',
    weight:              90,
    showNamespaceFilter: true,
    to:                  {
      name:   `${ PRODUCT }-c-cluster-resource`,
      params: {
        product: PRODUCT, cluster: BLANK_CLUSTER, resource: CAPI_CLUSTER
      },
    },
  });

  configureType(HYVE.TEMPLATE, {
    isCreatable: true, isEditable: true, isRemovable: true, canYaml: true,
  });
  configureType(CAPI_CLUSTER, {
    isCreatable: false, isEditable: true, isRemovable: true, canYaml: true,
  });
  configureType(HYVE.RESOURCE, {
    isCreatable: true, isEditable: true, isRemovable: true, canYaml: true,
  });
  configureType(HYVE.CLUSTER_RESOURCE, {
    isCreatable: false, isEditable: true, isRemovable: true, canYaml: true,
  });

  virtualType({
    labelKey:   'hyve.createCluster.menuLabel',
    name:       HYVE.CREATE_CLUSTER,
    namespaced: false,
    route:      {
      name:   `${ PRODUCT }-c-cluster-${ HYVE.CREATE_CLUSTER }`,
      params: { product: PRODUCT, cluster: BLANK_CLUSTER },
    },
  });

  basicType([CAPI_CLUSTER, HYVE.CREATE_CLUSTER, HYVE.TEMPLATE, HYVE.RESOURCE]);
  weightType(CAPI_CLUSTER, 4, true);
  weightType(HYVE.CREATE_CLUSTER, 3, true);
  weightType(HYVE.TEMPLATE, 2, true);
  weightType(HYVE.RESOURCE, 1, true);

  headers(HYVE.TEMPLATE, [
    { name: 'name', labelKey: 'tableHeaders.name', value: 'nameDisplay', sort: ['nameSort'], formatter: 'LinkDetail' },
    { name: 'namespace', labelKey: 'tableHeaders.namespace', value: 'metadata.namespace', sort: 'metadata.namespace' },
    { name: 'class', labelKey: 'hyve.headers.class', value: 'spec.clusterClassRef.name', sort: 'spec.clusterClassRef.name' },
    { name: 'version', labelKey: 'hyve.headers.version', value: 'spec.kubernetesVersion' },
    { name: 'schedule', labelKey: 'hyve.headers.schedule', value: 'spec.schedule' },
    { name: 'resources', labelKey: 'hyve.headers.resources', value: 'spec.resources.length' },
    { name: 'ready', labelKey: 'hyve.headers.ready', value: 'readyDisplay' },
    { name: 'description', labelKey: 'tableHeaders.description', value: 'spec.description' },
    { name: 'age', labelKey: 'tableHeaders.age', value: 'metadata.creationTimestamp', formatter: 'LiveDate', width: 100 },
  ]);

  headers(CAPI_CLUSTER, [
    { name: 'state', labelKey: 'tableHeaders.state', value: 'stateDisplay', formatter: 'BadgeStateFormatter', width: 100 },
    { name: 'name', labelKey: 'tableHeaders.name', value: 'nameDisplay', sort: ['nameSort'], formatter: 'LinkDetail' },
    { name: 'namespace', labelKey: 'hyve.headers.environment', value: 'metadata.namespace' },
    { name: 'template', labelKey: 'hyve.headers.template', value: `$.metadata.labels['${ LABELS.TEMPLATE }']` },
    { name: 'class', labelKey: 'hyve.headers.class', value: 'spec.topology.classRef.name' },
    { name: 'version', labelKey: 'hyve.headers.version', value: 'spec.topology.version' },
    { name: 'phase', labelKey: 'hyve.headers.phase', value: 'status.phase' },
    { name: 'expires', labelKey: 'hyve.headers.expires', value: `$.metadata.annotations['${ ANNOTATIONS.EXPIRES_AT }']` },
    { name: 'age', labelKey: 'tableHeaders.age', value: 'metadata.creationTimestamp', formatter: 'LiveDate', width: 100 },
  ]);

  headers(HYVE.RESOURCE, [
    { name: 'name', labelKey: 'tableHeaders.name', value: 'nameDisplay', sort: ['nameSort'], formatter: 'LinkDetail' },
    { name: 'namespace', labelKey: 'tableHeaders.namespace', value: 'metadata.namespace', sort: 'metadata.namespace' },
    { name: 'kind', labelKey: 'hyve.resources.kind', value: 'fleetKind' },
    { name: 'source', labelKey: 'hyve.resources.source', value: 'sourceDisplay' },
    { name: 'secrets', labelKey: 'hyve.resources.secrets', value: 'secretsDisplay' },
    { name: 'age', labelKey: 'tableHeaders.age', value: 'metadata.creationTimestamp', formatter: 'LiveDate', width: 100 },
  ]);
}
