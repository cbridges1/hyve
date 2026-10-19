import { ClusterProvisionerContext, IClusterProvisioner } from '@shell/core/types';
import { BLANK_CLUSTER } from '@shell/store/store-types.js';
import { HYVE, PRODUCT } from '../config/types';
import { TILES } from '../utils/providers';

// Tiles on Rancher's Create Cluster page (HYVE-PROPOSAL.md, "Creating
// clusters"): each opens hyve's Create Cluster page with that provider's
// templates. The cluster is a CAPI Cluster made from a template; Turtles
// imports it into Rancher once it's up.
function tile(id: string, label: string, provider = ''): any {
  return class implements IClusterProvisioner {
    static ID = id;

    constructor(_context: ClusterProvisionerContext) {}

    get id() {
      return id;
    }

    get label() {
      return label;
    }

    get group() {
      return 'hyve';
    }

    get link() {
      return `/${ PRODUCT }/c/${ BLANK_CLUSTER }/${ HYVE.CREATE_CLUSTER }${ provider ? `?provider=${ encodeURIComponent(provider) }` : '' }`;
    }

    get detailTabs() {
      return {
        machines: true, logs: false, registration: false, snapshots: false, related: true, events: true, conditions: true
      };
    }
  };
}

export const PROVISIONERS = [
  ...TILES.map((t) => tile(`hyve-${ t.id }`, t.label, t.id)),
  tile('hyve-template', 'Any hyve template'),
];
