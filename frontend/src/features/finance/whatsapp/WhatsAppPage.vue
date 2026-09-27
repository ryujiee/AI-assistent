<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import AppIcon from '@/components/AppIcon.vue'
import AppSpinner from '@/components/AppSpinner.vue'
import EmptyState from '@/components/EmptyState.vue'
import GroupPickerModal from './GroupPickerModal.vue'
import { financeApi, type FinanceWhatsApp, type Workspace } from '@/api/finance'
import { ApiError } from '@/api/client'
import { formatDateTime, maskPhone } from '../lib/format'

const state = ref<FinanceWhatsApp | null>(null)
const settings = ref<Workspace | null>(null)
const loading = ref(true)
const error = ref('')
const picking = ref(false)
const unlinking = ref(false)
const names = reactive<Record<number, string>>({})
const savingMember = ref<number | null>(null)
const memberMessage = ref('')
const settingsMessage = ref('')

async function load() {
  loading.value = !state.value
  error.value = ''
  try {
    const [wa, ws] = await Promise.all([financeApi.whatsapp(), financeApi.settings()])
    state.value = wa
    settings.value = ws
    for (const m of wa.members) names[m.id] = m.display_name
  } catch (err) {
    error.value = err instanceof ApiError ? err.message : 'Não foi possível carregar a configuração.'
  } finally {
    loading.value = false
  }
}

async function unlink() {
  if (!confirm('Desvincular o grupo? A IA deixa de ler as mensagens dele até você escolher outro.')) return
  unlinking.value = true
  try {
    state.value = await financeApi.unlinkGroup()
  } catch (err) {
    error.value = err instanceof ApiError ? err.message : 'Não foi possível desvincular.'
  } finally {
    unlinking.value = false
  }
}

async function saveName(id: number) {
  const name = (names[id] ?? '').trim()
  if (!name) return
  savingMember.value = id
  memberMessage.value = ''
  try {
    await financeApi.renameMember(id, name)
    memberMessage.value = 'Nome salvo.'
    await load()
  } catch (err) {
    memberMessage.value = err instanceof ApiError ? err.message : 'Não foi possível salvar.'
  } finally {
    savingMember.value = null
  }
}

async function saveSettings(patch: Parameters<typeof financeApi.updateSettings>[0]) {
  settingsMessage.value = ''
  try {
    settings.value = await financeApi.updateSettings(patch)
    settingsMessage.value = 'Preferências salvas.'
  } catch (err) {
    settingsMessage.value = err instanceof ApiError ? err.message : 'Não foi possível salvar.'
  }
}

const threshold = computed(() => Math.round((settings.value?.confidence_threshold ?? 0.75) * 100))

function onPicked() {
  picking.value = false
  load()
}

onMounted(load)
</script>

<template>
  <div v-if="loading" class="grid gap-6 lg:grid-cols-3">
    <div class="skeleton h-64 lg:col-span-2" />
    <div class="skeleton h-64" />
  </div>

  <section v-else-if="error && !state" class="glass-card rounded-2xl">
    <EmptyState icon="warning" tone="error" title="Não foi possível carregar" :description="error">
      <button class="btn-secondary" @click="load">Tentar novamente</button>
    </EmptyState>
  </section>

  <div v-else-if="state" class="grid gap-6 lg:grid-cols-3">
    <div class="flex flex-col gap-6 lg:col-span-2">
      <!-- Group -->
      <section class="glass-card rounded-2xl p-6 flex flex-col gap-5">
        <div class="flex items-start justify-between gap-4">
          <h3 class="text-lg font-semibold flex items-center gap-2">
            <AppIcon name="chat" class="h-5 w-5 text-indigo-400" />
            WhatsApp financeiro
          </h3>
          <span
            class="text-[11px] font-semibold uppercase tracking-wider px-3 py-1 rounded-full border"
            :class="state.connected ? 'border-green-500/30 text-green-400' : 'border-red-500/30 text-red-400'"
          >
            {{ state.connected ? 'Sessão conectada' : 'Sessão desconectada' }}
          </span>
        </div>

        <div v-if="state.group" class="flex flex-col sm:flex-row sm:items-center gap-4 p-4 rounded-xl bg-slate-900/50 border border-white/5">
          <div class="h-12 w-12 shrink-0 rounded-xl bg-emerald-500/10 border border-emerald-500/20 flex items-center justify-center text-xl" aria-hidden="true">💬</div>
          <div class="flex-1 min-w-0">
            <p class="text-xs text-slate-400">Grupo financeiro</p>
            <p class="text-base font-semibold text-slate-100 truncate">{{ state.group.name || 'Grupo sem nome' }}</p>
            <p class="text-xs text-slate-400 mt-0.5">
              <template v-if="state.group.participant_count !== null">{{ state.group.participant_count }} participantes · </template>
              <template v-if="state.group.linked_at">vinculado em {{ formatDateTime(state.group.linked_at) }}</template>
            </p>
          </div>
          <div class="flex gap-2">
            <button class="btn-secondary" :disabled="!state.connected" @click="picking = true">Alterar grupo</button>
            <button class="btn-ghost" :disabled="unlinking" title="Desvincular grupo" @click="unlink">
              <AppIcon name="x" class="h-4 w-4" />
            </button>
          </div>
        </div>

        <EmptyState
          v-else
          icon="users"
          title="Nenhum grupo financeiro"
          :description="
            state.connected
              ? 'Escolha o grupo do WhatsApp em que vocês vão registrar gastos. Só as mensagens novas serão lidas.'
              : 'Conecte o WhatsApp na aba Secretária para escolher um grupo.'
          "
        >
          <button class="btn-primary" :disabled="!state.connected" @click="picking = true">
            <AppIcon name="plus" class="h-4 w-4" />
            Escolher grupo
          </button>
        </EmptyState>

        <p v-if="state.fake" class="text-[11px] text-amber-300/80 flex items-center gap-1.5">
          <AppIcon name="info" class="h-3.5 w-3.5" />
          Modo de desenvolvimento: grupos fictícios, nenhuma conta real do WhatsApp é usada.
        </p>
        <p v-if="error" role="alert" class="text-xs text-red-400">{{ error }}</p>
      </section>

      <!-- Members -->
      <section class="glass-card rounded-2xl p-6 flex flex-col gap-4">
        <div>
          <h3 class="text-lg font-semibold flex items-center gap-2">
            <AppIcon name="users" class="h-5 w-5 text-indigo-400" />
            Quem é quem
          </h3>
          <p class="text-xs text-slate-400 mt-1">Os nomes aparecem nos relatórios e nas respostas da IA (“quanto a Ana gastou?”).</p>
        </div>

        <EmptyState v-if="!state.members.length" icon="user" title="Nenhum participante ainda" description="Os participantes aparecem aqui quando um grupo é escolhido." />
        <ul v-else class="flex flex-col divide-y divide-white/5">
          <li v-for="m in state.members" :key="m.id" class="flex items-center gap-3 py-3">
            <div class="h-9 w-9 shrink-0 rounded-full bg-indigo-500/15 text-indigo-300 flex items-center justify-center text-sm font-semibold">
              {{ (m.display_name || '?').charAt(0).toUpperCase() }}
            </div>
            <form class="flex-1 flex items-center gap-2" @submit.prevent="saveName(m.id)">
              <input v-model="names[m.id]" class="field !py-2" :aria-label="`Nome de ${m.display_name}`" maxlength="60" />
              <button
                type="submit"
                class="btn-secondary !px-3 !py-2"
                :disabled="savingMember === m.id || !names[m.id]?.trim() || names[m.id] === m.display_name"
              >
                <AppSpinner v-if="savingMember === m.id" class="h-4 w-4" />
                <span>Salvar</span>
              </button>
            </form>
            <span v-if="maskPhone(m.phone)" class="hidden sm:block text-xs text-slate-500 font-mono w-32 text-right">{{ maskPhone(m.phone) }}</span>
          </li>
        </ul>
        <p v-if="memberMessage" class="text-xs text-slate-400">{{ memberMessage }}</p>
      </section>
    </div>

    <div class="flex flex-col gap-6">
      <!-- How it works -->
      <section class="glass-card rounded-2xl p-6 flex flex-col gap-3">
        <h3 class="text-sm font-semibold flex items-center gap-2"><AppIcon name="sparkles" class="h-4 w-4 text-indigo-400" />Como usar</h3>
        <ul class="flex flex-col gap-2 text-xs text-slate-300">
          <li class="px-3 py-2 rounded-lg bg-slate-900/50 border border-white/5">“gastei 42 no almoço”</li>
          <li class="px-3 py-2 rounded-lg bg-slate-900/50 border border-white/5">📎 foto ou PDF de comprovante</li>
          <li class="px-3 py-2 rounded-lg bg-slate-900/50 border border-white/5">“na verdade foi 60” · “desfaz”</li>
          <li class="px-3 py-2 rounded-lg bg-slate-900/50 border border-white/5">“quanto gastamos esse mês?”</li>
        </ul>
      </section>

      <!-- Preferences -->
      <section v-if="settings" class="glass-card rounded-2xl p-6 flex flex-col gap-5">
        <h3 class="text-sm font-semibold flex items-center gap-2"><AppIcon name="adjustments" class="h-4 w-4 text-indigo-400" />Preferências</h3>

        <label class="flex flex-col gap-2">
          <span class="text-xs font-medium text-slate-300">Resumo mensal no grupo</span>
          <select
            class="field"
            :value="settings.monthly_summary"
            @change="saveSettings({ monthly_summary: ($event.target as HTMLSelectElement).value as Workspace['monthly_summary'] })"
          >
            <option value="off">Não enviar</option>
            <option value="last_day">No último dia do mês</option>
            <option value="first_day">No dia 1 do mês seguinte</option>
          </select>
        </label>

        <label class="flex items-center justify-between gap-3">
          <span class="flex flex-col">
            <span class="text-xs font-medium text-slate-300">Resumo semanal</span>
            <span class="text-[11px] text-slate-500">Toda segunda-feira, sobre a semana anterior.</span>
          </span>
          <input
            type="checkbox"
            class="h-5 w-9 appearance-none rounded-full bg-slate-700 checked:bg-indigo-600 relative cursor-pointer transition before:content-[''] before:absolute before:h-4 before:w-4 before:rounded-full before:bg-white before:top-0.5 before:left-0.5 checked:before:translate-x-4 before:transition"
            :checked="settings.weekly_summary"
            @change="saveSettings({ weekly_summary: ($event.target as HTMLInputElement).checked })"
          />
        </label>

        <label class="flex flex-col gap-2">
          <span class="flex justify-between text-xs font-medium text-slate-300">
            <span>Confiança mínima para registrar sozinho</span>
            <span class="text-indigo-300">{{ threshold }}%</span>
          </span>
          <input
            type="range"
            min="50"
            max="100"
            step="5"
            :value="threshold"
            class="accent-indigo-500"
            @change="saveSettings({ confidence_threshold: Number(($event.target as HTMLInputElement).value) / 100 })"
          />
          <span class="text-[11px] text-slate-500">Abaixo disso a IA pergunta antes de registrar.</span>
        </label>
        <p v-if="settingsMessage" class="text-xs text-slate-400">{{ settingsMessage }}</p>
      </section>
    </div>

    <GroupPickerModal v-if="picking" @close="picking = false" @selected="onPicked" />
  </div>
</template>
