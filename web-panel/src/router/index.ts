import { createRouter, createWebHistory } from 'vue-router'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    {
      path: '/',
      redirect: '/teams',
    },
    {
      path: '/teams',
      name: 'teams',
      component: () => import('@/views/TeamsView.vue'),
      meta: { title: '团队管理' },
    },
    {
      path: '/teams/:id',
      name: 'team-detail',
      component: () => import('@/views/TeamDetailView.vue'),
      meta: { title: '团队详情' },
    },
    {
      path: '/identity-cards',
      name: 'identity-cards',
      component: () => import('@/views/IdentityCardsView.vue'),
      meta: { title: '身份卡' },
    },
    {
      path: '/api-keys',
      name: 'api-keys',
      component: () => import('@/views/ApiKeysView.vue'),
      meta: { title: 'API Key' },
    },
    {
      path: '/upstreams',
      name: 'upstreams',
      component: () => import('@/views/UpstreamsView.vue'),
      meta: { title: '上游通道' },
    },
    {
      path: '/health',
      name: 'health',
      component: () => import('@/views/HealthDashboardView.vue'),
      meta: { title: '录入健康看板' },
    },
    {
      path: '/:pathMatch(.*)*',
      redirect: '/teams',
    },
  ],
})

export default router
