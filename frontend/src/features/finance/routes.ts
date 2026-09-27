import type { RouteRecordRaw } from 'vue-router'

export const financeRoutes: RouteRecordRaw[] = [
  {
    path: 'financeiro',
    name: 'finance',
    component: () => import('./FinanceComingSoon.vue'),
    meta: { title: 'Financeiro', wide: true },
  },
]
