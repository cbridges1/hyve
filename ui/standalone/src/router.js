import { createRouter, createWebHistory } from 'vue-router';
import Overview from '@pkg/pages/Overview.vue';
import ResourceMatrix from '@pkg/pages/ResourceMatrix.vue';
import Providers from '@pkg/pages/Providers.vue';
import Secrets from '@pkg/pages/Secrets.vue';
import Repos from '@pkg/pages/Repos.vue';
import { HYVE, PRODUCT } from '@pkg/config/types';
import Login from './pages/Login.vue';
import Environments from './pages/Environments.vue';
import Access from './pages/Access.vue';
import ImportPage from './pages/ImportPage.vue';
import CreateClusterPage from './pages/CreateClusterPage.vue';
import ResourceList from './pages/ResourceList.vue';
import ResourceDetail from './pages/ResourceDetail.vue';
import ResourceCreate from './pages/ResourceCreate.vue';

// The extension's route names, so its pages' links work unchanged: they
// navigate by name with Rancher's params (product, cluster, resource...).
export function createHyveRouter(session, store) {
  const page = (id, component) => ({ name: `${ PRODUCT }-c-cluster-${ id }`, path: `/c/:cluster/${ id }`, component });

  const router = createRouter({
    history: createWebHistory('/'),
    routes:  [
      { name: 'login', path: '/login', component: Login, meta: { public: true } },
      { path: '/', redirect: `/c/_/${ HYVE.OVERVIEW }` },
      page(HYVE.OVERVIEW, Overview),
      page(HYVE.MATRIX, ResourceMatrix),
      page(HYVE.PROVIDERS, Providers),
      page(HYVE.SECRETS, Secrets),
      page(HYVE.REPOS, Repos),
      page(HYVE.CREATE_CLUSTER, CreateClusterPage),
      { name: 'hyve-environments', path: '/environments', component: Environments },
      { name: 'hyve-access', path: '/access', component: Access },
      { name: 'hyve-import', path: '/import', component: ImportPage },
      { name: `${ PRODUCT }-c-cluster-resource`, path: '/c/:cluster/:resource', component: ResourceList },
      { name: `${ PRODUCT }-c-cluster-resource-create`, path: '/c/:cluster/:resource/create', component: ResourceCreate },
      { name: `${ PRODUCT }-c-cluster-resource-id`, path: '/c/:cluster/:resource/:id', component: ResourceDetail },
      { name: `${ PRODUCT }-c-cluster-resource-namespace-id`, path: '/c/:cluster/:resource/:namespace/:id/:tab(resources)?', component: ResourceDetail },
      { path: '/:rest(.*)', redirect: `/c/_/${ HYVE.OVERVIEW }` },
    ],
  });

  router.beforeEach(async(to) => {
    if (to.meta.public) {
      return true;
    }
    if (!session.user) {
      return { name: 'login', query: to.fullPath !== '/' ? { redirect: to.fullPath } : {} };
    }
    if (!Object.keys(store.schemas).length) {
      await store.load();
    }

    return true;
  });

  return router;
}
