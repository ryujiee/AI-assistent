<script setup lang="ts">
import { onMounted } from 'vue'
import { useRoute } from 'vue-router'
import AppIcon, { type IconName } from '@/components/AppIcon.vue'
import EmptyState from '@/components/EmptyState.vue'
import { financeStatus, loadFinanceStatus } from './useFinanceStatus'

const route = useRoute()

const tabs: { to: string; label: string; icon: IconName; exact?: boolean }[] = [
  { to: '/financeiro', label: 'Visão geral', icon: 'chartPie', exact: true },
  { to: '/financeiro/transacoes', label: 'Transações', icon: 'list' },
  { to: '/financeiro/categorias', label: 'Categorias & Orçamentos', icon: 'tag' },
  { to: '/financeiro/whatsapp', label: 'WhatsApp Financeiro', icon: 'chat' },
]

const isActive = (t: (typeof tabs)[number]) => (t.exact ? route.path === t.to : route.path.startsWith(t.to))

onMounted(loadFinanceStatus)
</script>

<template>
  <div class="flex flex-col gap-6">
    <div class="flex flex-col md:flex-row md:items-end justify-between gap-4">
      <div>
        <h2 class="text-2xl font-bold tracking-tight">Gestão Financeira</h2>
        <p class="text-sm text-slate-400 mt-1">Organize as finanças do casal com ajuda da IA.</p>
      </div>
    </div>

    <nav class="flex gap-1 overflow-x-auto border-b border-white/5 -mx-1 px-1" aria-label="Seções do financeiro">
      <RouterLink
        v-for="tab in tabs"
        :key="tab.to"
        :to="tab.to"
        class="px-3 py-2.5 text-sm font-medium flex items-center gap-2 whitespace-nowrap border-b-2 -mb-px transition"
        :class="isActive(tab) ? 'border-indigo-500 text-slate-100' : 'border-transparent text-slate-400 hover:text-slate-200'"
      >
        <AppIcon :name="tab.icon" class="h-4 w-4" />
        {{ tab.label }}
      </RouterLink>
    </nav>

    <div v-if="financeStatus.loading && !financeStatus.status" class="grid gap-4 md:grid-cols-4">
      <div v-for="i in 4" :key="i" class="skeleton h-28" />
    </div>
    <section v-else-if="financeStatus.error" class="glass-card rounded-2xl">
      <EmptyState icon="warning" tone="error" title="Não foi possível carregar o financeiro" :description="financeStatus.error">
        <button class="btn-secondary" @click="loadFinanceStatus">Tentar novamente</button>
      </EmptyState>
    </section>
    <section v-else-if="financeStatus.status && !financeStatus.status.enabled" class="glass-card rounded-2xl">
      <EmptyState
        icon="lock"
        tone="warning"
        title="Módulo financeiro desativado"
        description="Defina FINANCE_ENABLED=true no servidor, aplique as migrations (secretary migrate apply) e reinicie o backend."
      />
    </section>
    <section v-else-if="financeStatus.status && !financeStatus.status.migrated" class="glass-card rounded-2xl">
      <EmptyState
        icon="warning"
        tone="warning"
        title="Falta criar as tabelas do financeiro"
        description="Rode secretary migrate apply no servidor. Nenhuma tabela é criada automaticamente."
      />
    </section>
    <RouterView v-else />
  </div>
</template>
