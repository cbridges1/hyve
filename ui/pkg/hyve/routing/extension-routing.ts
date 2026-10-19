import ListResource from '@shell/pages/c/_cluster/_product/_resource/index.vue';
import CreateResource from '@shell/pages/c/_cluster/_product/_resource/create.vue';
import ViewResource from '@shell/pages/c/_cluster/_product/_resource/_id.vue';
import ViewNamespacedResource from '@shell/pages/c/_cluster/_product/_resource/_namespace/_id.vue';
import { BLANK_CLUSTER } from '@shell/store/store-types.js';
import CreateCluster from '../pages/CreateCluster.vue';
import Overview from '../pages/Overview.vue';
import ResourceMatrix from '../pages/ResourceMatrix.vue';
import Providers from '../pages/Providers.vue';
import Secrets from '../pages/Secrets.vue';
import Repos from '../pages/Repos.vue';
import CliLogin from '../pages/CliLogin.vue';
import { HYVE, PRODUCT } from '../config/types';
import { CLI_LOGIN_PATH, CLI_LOGIN_ROUTE } from '../utils/cli-login';

const meta = { product: PRODUCT, cluster: BLANK_CLUSTER, pkg: PRODUCT };

const routes: any[] = [
  // The hyve CLI's sign-in page: Rancher's header but no side nav, and no
  // product or cluster, so any signed-in user can reach it.
  {
    parent: 'plain',
    route:  {
      name:      CLI_LOGIN_ROUTE,
      path:      CLI_LOGIN_PATH,
      component: CliLogin,
    },
  },
  ...[
    [HYVE.OVERVIEW, Overview],
    [HYVE.MATRIX, ResourceMatrix],
    [HYVE.PROVIDERS, Providers],
    [HYVE.SECRETS, Secrets],
    [HYVE.REPOS, Repos],
  ].map(([page, component]) => ({
    name:      `${ PRODUCT }-c-cluster-${ page }`,
    path:      `/${ PRODUCT }/c/:cluster/${ page }`,
    component,
    meta,
  })),
  {
    name:      `${ PRODUCT }-c-cluster-${ HYVE.CREATE_CLUSTER }`,
    path:      `/${ PRODUCT }/c/:cluster/${ HYVE.CREATE_CLUSTER }`,
    component: CreateCluster,
    meta,
  },
  {
    name:      `${ PRODUCT }-c-cluster-resource`,
    path:      `/${ PRODUCT }/c/:cluster/:resource`,
    component: ListResource,
    meta,
  },
  {
    name:      `${ PRODUCT }-c-cluster-resource-create`,
    path:      `/${ PRODUCT }/c/:cluster/:resource/create`,
    component: CreateResource,
    meta,
  },
  {
    name:      `${ PRODUCT }-c-cluster-resource-id`,
    path:      `/${ PRODUCT }/c/:cluster/:resource/:id`,
    component: ViewResource,
    meta,
  },
  {
    name:      `${ PRODUCT }-c-cluster-resource-namespace-id`,
    path:      `/${ PRODUCT }/c/:cluster/:resource/:namespace/:id`,
    component: ViewNamespacedResource,
    meta,
  },
];

export default routes;
