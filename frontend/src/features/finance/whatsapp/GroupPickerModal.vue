<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import ModalDialog from '@/components/ModalDialog.vue'
import AppIcon from '@/components/AppIcon.vue'
import AppSpinner from '@/components/AppSpinner.vue'
import EmptyState from '@/components/EmptyState.vue'
import { financeApi, type WhatsAppGroup } from '@/api/finance'
import { ApiError } from '@/api/client'

const emit = defineEmits<{ close: []; selected: [] }>()

const groups = ref<WhatsAppGroup[]>([])
const loading = ref(true)
const error = ref('')
const search = ref('')
const saving = ref('')

const filtered = computed(() => {
  const q = search.value.trim().toLocaleLowerCase('pt-BR')
  return q ? groups.value.filter((g) => g.name.toLocaleLowerCase('pt-BR').includes(q)) : groups.value
})

async function load() {
  loading.value = true
  error.value = ''
  try {
    groups.value = (await financeApi.groups()).groups
  } catch (err) {
    error.value = err instanceof ApiError ? err.message : 'Não foi possível listar os grupos.'
  } finally {
    loading.value = false
  }
}

async function choose(g: WhatsAppGroup) {
  saving.value = g.jid
  error.value = ''
  try {
    await financeApi.linkGroup(g.jid)
    emit('selected')
  } catch (err) {
    error.value = err instanceof ApiError ? err.message : 'Não foi possível usar esse grupo.'
  } finally {
    saving.value = ''
  }
}

onMounted(load)
</script>

<template>
  <ModalDialog title="Escolha o grupo financeiro" subtitle="Somente mensagens novas desse grupo serão lidas pela IA." @close="emit('close')">
    <div class="flex flex-col gap-4">
      <div class="relative">
        <AppIcon name="search" class="h-4 w-4 text-slate-500 absolute left-3 top-1/2 -translate-y-1/2" />
        <input v-model="search" type="search" placeholder="Buscar grupo..." aria-label="Buscar grupo" class="field pl-9" />
      </div>

      <p v-if="error && groups.length" role="alert" class="text-xs text-red-400">{{ error }}</p>

      <div v-if="loading" class="flex flex-col gap-2">
        <div v-for="i in 3" :key="i" class="skeleton h-16" />
      </div>
      <EmptyState v-else-if="error && !groups.length" icon="warning" tone="warning" title="Não foi possível listar os grupos" :description="error">
        <button class="btn-secondary" @click="load"><AppIcon name="refresh" class="h-4 w-4" />Tentar novamente</button>
      </EmptyState>
      <EmptyState
        v-else-if="!groups.length"
        icon="users"
        title="Nenhum grupo encontrado"
        description="Crie um grupo no WhatsApp com vocês dois e o número da Secretária, depois volte aqui."
      />
      <EmptyState v-else-if="!filtered.length" icon="search" title="Nenhum grupo com esse nome" />

      <ul v-else class="flex flex-col gap-2">
        <li
          v-for="g in filtered"
          :key="g.jid"
          class="flex items-center gap-3 p-3 rounded-xl border transition"
          :class="g.linked ? 'border-indigo-500/40 bg-indigo-500/10' : 'border-white/5 bg-slate-900/40 hover:border-white/10'"
        >
          <div class="h-10 w-10 shrink-0 rounded-xl bg-emerald-500/10 border border-emerald-500/20 flex items-center justify-center text-lg" aria-hidden="true">💬</div>
          <div class="flex-1 min-w-0">
            <p class="text-sm font-medium text-slate-100 truncate">{{ g.name || 'Grupo sem nome' }}</p>
            <p class="text-xs text-slate-400">{{ g.participant_count }} {{ g.participant_count === 1 ? 'participante' : 'participantes' }}</p>
          </div>
          <span v-if="g.linked" class="text-[11px] font-semibold uppercase tracking-wider text-indigo-300">Em uso</span>
          <button v-else class="btn-primary !px-4 !py-2" :disabled="!!saving" @click="choose(g)">
            <AppSpinner v-if="saving === g.jid" class="h-4 w-4 text-white" />
            <span>Usar</span>
          </button>
        </li>
      </ul>
    </div>
  </ModalDialog>
</template>
