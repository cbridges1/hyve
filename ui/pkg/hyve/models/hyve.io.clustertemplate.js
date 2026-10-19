import SteveModel from '@shell/plugins/steve/steve-class';
import { BLANK_CLUSTER } from '@shell/store/store-types.js';
import { HYVE, PRODUCT } from '../config/types';

export default class ClusterTemplate extends SteveModel {
  get _availableActions() {
    return [
      {
        action:  'createCluster',
        label:   this.t('hyve.createCluster.action'),
        icon:    'icon icon-plus',
        enabled: true,
      },
      { divider: true },
      ...super._availableActions,
    ];
  }

  createCluster() {
    this.currentRouter().push({
      name:   `${ PRODUCT }-c-cluster-${ HYVE.CREATE_CLUSTER }`,
      params: { product: PRODUCT, cluster: BLANK_CLUSTER },
      query:  { template: `${ this.metadata.namespace }/${ this.metadata.name }` },
    });
  }

  get lockedVariables() {
    return this.spec?.lockedVariables || [];
  }

  // hyve-controller's Ready condition: True, or why not.
  get readyDisplay() {
    const c = (this.status?.conditions || []).find((x) => x.type === 'Ready');

    if (!c) {
      return '-';
    }

    return c.status === 'True' ? 'Ready' : (c.message || c.reason || 'Not ready');
  }
}
