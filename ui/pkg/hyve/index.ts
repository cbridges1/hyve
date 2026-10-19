import { importTypes } from '@rancher/auto-import';
import {
  ActionLocation, IPlugin, TabLocation, TableColumnLocation
} from '@shell/core/types';
import extensionRouting from './routing/extension-routing';
import { PROVISIONERS } from './provisioners/tiles';
import { ANNOTATIONS, CAPI_CLUSTER, NEVER } from './config/types';
import {
  CLI_LOGIN_PATH, CLI_LOGIN_ROUTE, clearStash, stashRequest, stashedQuery
} from './utils/cli-login';

const EXTEND_HOURS = 8;

// Set a CAPI Cluster's expiry ("never" for none); hyve-controller deletes
// it at that time.
async function setExpiry(cluster: any, value: string) {
  cluster.metadata.annotations = { ...(cluster.metadata.annotations || {}), [ANNOTATIONS.EXPIRES_AT]: value };
  await cluster.save();
}

function isoSeconds(d: Date): string {
  return d.toISOString().replace(/\.\d{3}Z$/, 'Z');
}

// Init the package
export default function(plugin: IPlugin): void {
  // Auto-import model, detail, edit from the folders
  importTypes(plugin);

  // Provide plugin metadata from package.json
  plugin.metadata = require('./package.json');

  plugin.addProduct(require('./product'));
  plugin.addRoutes(extensionRouting);

  // Tiles on Rancher's Create Cluster page.
  PROVISIONERS.forEach((p) => plugin.register('provisioner', p.ID, p));

  // The hyve CLI's sign-in page. If the user isn't signed in to Rancher,
  // Rancher's sign-in comes first, and for GitHub, Google and OIDC it leaves
  // the dashboard and comes back to the home page. So keep the page's query
  // now, while the dashboard loads, and return to the page after sign-in.
  stashRequest(window.location.pathname, window.location.search);
  plugin.addNavHooks({
    onLogin: async(store: any) => {
      const query = stashedQuery();
      const router = store.$router;

      // Opened while signed in: the page is already loading.
      if (window.location.pathname.replace(/\/+$/, '').endsWith(CLI_LOGIN_PATH)) {
        return;
      }
      if (!query || !router) {
        return;
      }
      clearStash();
      // After the navigation that signed in, which this hook runs inside.
      setTimeout(() => {
        if (router.currentRoute.value?.name !== CLI_LOGIN_ROUTE) {
          router.replace({ name: CLI_LOGIN_ROUTE, query });
        }
      }, 0);
    },
  });

  // Resources tab on a cluster's detail page: hyve's own Clusters page (CAPI
  // Cluster) and Cluster Management's (provisioning cluster).
  plugin.addTab(
    TabLocation.RESOURCE_DETAIL,
    { resource: ['cluster.x-k8s.io.cluster', 'provisioning.cattle.io.cluster'] },
    {
      name:       'hyve-resources',
      labelKey:   'hyve.resources.tab',
      weight:     -5,
      showHeader: true,
      component:  () => import('./components/ClusterResources.vue'),
    }
  );

  plugin.addAction(ActionLocation.TABLE, { resource: [CAPI_CLUSTER] }, {
    labelKey: 'hyve.expiry.extend',
    icon:     'icon-clock',
    multiple: true,
    invoke:   async(_opts: any, resources: any[]) => {
      for (const c of resources) {
        const cur = Date.parse(c.metadata?.annotations?.[ANNOTATIONS.EXPIRES_AT] || '');
        const base = Math.max(Date.now(), isNaN(cur) ? 0 : cur);

        await setExpiry(c, isoSeconds(new Date(base + EXTEND_HOURS * 3600 * 1000)));
      }

      return true;
    },
  });
  plugin.addAction(ActionLocation.TABLE, { resource: [CAPI_CLUSTER] }, {
    labelKey: 'hyve.expiry.remove',
    icon:     'icon-close',
    multiple: true,
    enabled:  (ctx: any) => (ctx?.metadata?.annotations?.[ANNOTATIONS.EXPIRES_AT] || NEVER) !== NEVER,
    invoke:   async(_opts: any, resources: any[]) => {
      for (const c of resources) {
        await setExpiry(c, NEVER);
      }

      return true;
    },
  });
}
