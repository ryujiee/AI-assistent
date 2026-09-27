import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'
import { session, refreshSession, markLoggedOut } from './session'
import { onUnauthorized } from '@/api/client'
import AppShell from './AppShell.vue'
import LoginPage from './LoginPage.vue'
import SecretaryPage from '@/features/secretary/SecretaryPage.vue'
import { financeRoutes } from '@/features/finance/routes'

declare module 'vue-router' {
  interface RouteMeta {
    public?: boolean
    wide?: boolean
    title?: string
  }
}

const routes: RouteRecordRaw[] = [
  { path: '/login', name: 'login', component: LoginPage, meta: { public: true, title: 'Entrar' } },
  {
    path: '/',
    component: AppShell,
    children: [
      { path: '', redirect: '/secretaria' },
      { path: 'secretaria', name: 'secretary', component: SecretaryPage, meta: { title: 'Secretária' } },
      ...financeRoutes,
    ],
  },
  { path: '/:pathMatch(.*)*', redirect: '/secretaria' },
]

export const router = createRouter({
  history: createWebHistory(),
  routes,
})

router.beforeEach(async (to) => {
  if (to.meta.public) {
    if (!session.checked) await refreshSession()
    return true
  }
  if (!session.checked) await refreshSession()
  if (!session.authenticated) {
    return { name: 'login', query: to.fullPath !== '/' ? { redirect: to.fullPath } : {} }
  }
  return true
})

router.afterEach((to) => {
  document.title = to.meta.title ? `${to.meta.title} · Secretária de IA` : 'Painel de Controle - Secretária Pessoal IA'
})

// Session expired mid-use: go back to the login screen.
onUnauthorized(() => {
  markLoggedOut()
  const current = router.currentRoute.value
  if (!current.meta.public) {
    router.push({ name: 'login', query: { redirect: current.fullPath } })
  }
})
