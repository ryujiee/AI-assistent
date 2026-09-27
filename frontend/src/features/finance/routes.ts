import type { RouteRecordRaw } from 'vue-router'

export const financeRoutes: RouteRecordRaw[] = [
  {
    path: 'financeiro',
    component: () => import('./FinanceLayout.vue'),
    meta: { wide: true },
    children: [
      { path: '', name: 'finance', component: () => import('./overview/OverviewPage.vue'), meta: { title: 'Financeiro' } },
      { path: 'transacoes', name: 'finance-transactions', component: () => import('./transactions/TransactionsPage.vue'), meta: { title: 'Transações' } },
      { path: 'categorias', name: 'finance-categories', component: () => import('./categories/CategoriesPage.vue'), meta: { title: 'Categorias' } },
      { path: 'whatsapp', name: 'finance-whatsapp', component: () => import('./whatsapp/WhatsAppPage.vue'), meta: { title: 'WhatsApp Financeiro' } },
    ],
  },
]
