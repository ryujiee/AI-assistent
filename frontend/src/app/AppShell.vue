<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import AppIcon from '@/components/AppIcon.vue'
import { useConnection } from '@/features/secretary/useConnection'
import { logout } from '@/api/session'
import { markLoggedOut } from './session'

const route = useRoute()
const router = useRouter()
const connection = useConnection()
const leaving = ref(false)

// The Secretária keeps the original narrow, vertically centered layout; the
// finance area needs room for tables and charts.
const wide = computed(() => route.matched.some((r) => r.meta.wide))

const tabs = [
  { to: '/secretaria', label: 'Secretária', icon: 'chat' as const },
  { to: '/financeiro', label: 'Financeiro', icon: 'cash' as const },
]

const badge = computed(() => {
  if (connection.connected) return { text: 'Conectado', tone: 'green' }
  if (connection.qrcode) return { text: 'Aguardando QR Code', tone: 'amber' }
  return { text: 'Desconectado', tone: 'red' }
})

const toneClasses: Record<string, { border: string; ping: string; dot: string }> = {
  green: { border: 'border-green-500/30 text-green-400', ping: 'bg-green-400', dot: 'bg-green-500' },
  amber: { border: 'border-amber-500/30 text-amber-400', ping: 'bg-amber-400', dot: 'bg-amber-500' },
  red: { border: 'border-red-500/30 text-red-400', ping: 'bg-red-400', dot: 'bg-red-500' },
}

async function signOut() {
  leaving.value = true
  try {
    await logout()
  } catch {
    // The cookie is cleared server-side on success; on failure the next API
    // call answers 401 and sends the user to the login screen anyway.
  }
  markLoggedOut()
  router.push('/login')
}
</script>

<template>
  <div class="min-h-screen flex flex-col items-center justify-between p-4 md:p-8">
    <div class="w-full flex flex-col gap-8" :class="wide ? 'max-w-7xl' : 'max-w-5xl my-auto'">
      <header class="flex flex-col lg:flex-row items-center justify-between gap-4 border-b border-white/5 pb-6">
        <div class="flex items-center gap-3">
          <div class="h-10 w-10 rounded-xl bg-indigo-600 flex items-center justify-center shadow-lg shadow-indigo-500/20">
            <AppIcon name="chat" class="h-6 w-6 text-white" />
          </div>
          <div>
            <h1 class="text-xl font-bold tracking-tight">Secretária de IA</h1>
            <p class="text-xs text-slate-400">Painel Administrativo do WhatsApp</p>
          </div>
        </div>

        <nav class="flex items-center gap-1 p-1 rounded-xl bg-slate-900/60 border border-white/5" aria-label="Seções">
          <RouterLink
            v-for="tab in tabs"
            :key="tab.to"
            :to="tab.to"
            class="px-4 py-2 rounded-lg text-sm font-medium flex items-center gap-2 transition"
            :class="route.path.startsWith(tab.to) ? 'bg-indigo-600 text-white shadow-lg shadow-indigo-600/20' : 'text-slate-400 hover:text-slate-100 hover:bg-white/5'"
          >
            <AppIcon :name="tab.icon" class="h-4 w-4" />
            {{ tab.label }}
          </RouterLink>
        </nav>

        <div class="flex items-center gap-3">
          <!-- Global Connection Badge -->
          <div class="flex items-center gap-2 px-4 py-2 rounded-full border bg-slate-900/60" :class="toneClasses[badge.tone].border">
            <span class="relative flex h-2 w-2">
              <span class="animate-ping absolute inline-flex h-full w-full rounded-full opacity-75" :class="toneClasses[badge.tone].ping"></span>
              <span class="relative inline-flex rounded-full h-2 w-2" :class="toneClasses[badge.tone].dot"></span>
            </span>
            <span class="text-xs font-semibold uppercase tracking-wider">{{ badge.text }}</span>
          </div>
          <button class="btn-ghost" :disabled="leaving" title="Sair" @click="signOut">
            <AppIcon name="logout" class="h-4 w-4" />
            <span class="hidden sm:inline">Sair</span>
          </button>
        </div>
      </header>

      <RouterView />

      <footer class="text-center text-[10px] text-slate-600 mt-6 pb-2">AI Personal Secretary &copy; 2026. Desenvolvido em Go + Vue.js.</footer>
    </div>
  </div>
</template>
