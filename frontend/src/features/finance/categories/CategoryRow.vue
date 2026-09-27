<script setup lang="ts">
import { computed, nextTick, ref } from 'vue'
import AppIcon from '@/components/AppIcon.vue'
import CategoryBadge from '../components/CategoryBadge.vue'
import type { Category, Essentiality } from '@/api/finance'
import { ESSENTIALITY } from '../lib/period'
import { centsToInput, formatBRLShort, formatPercent, parseBRL } from '../lib/format'

const props = defineProps<{ category: Category; spent: number; child?: boolean; saving?: boolean }>()
const emit = defineEmits<{
  budget: [cents: number | null]
  essentiality: [value: Essentiality]
  edit: []
  addChild: []
  archive: []
}>()

const editingBudget = ref(false)
const budgetInput = ref('')
const input = ref<HTMLInputElement | null>(null)
const budgetError = ref('')

const budget = computed(() => props.category.monthly_budget_cents)
const pct = computed(() => (budget.value ? (props.spent / budget.value) * 100 : 0))
const state = computed(() => (pct.value >= 100 ? 'over' : pct.value >= 80 ? 'warning' : 'ok'))

async function startBudget() {
  budgetInput.value = centsToInput(budget.value)
  budgetError.value = ''
  editingBudget.value = true
  await nextTick()
  input.value?.focus()
  input.value?.select()
}
function saveBudget() {
  const raw = budgetInput.value.trim()
  if (!raw) {
    emit('budget', null)
    editingBudget.value = false
    return
  }
  const cents = parseBRL(raw)
  if (!cents) {
    budgetError.value = 'Valor inválido'
    return
  }
  emit('budget', cents)
  editingBudget.value = false
}
</script>

<template>
  <div class="viz grid grid-cols-[1fr_auto] md:grid-cols-[minmax(0,1.4fr)_150px_110px_minmax(0,1.3fr)_108px] items-center gap-x-4 gap-y-2 px-5 py-3.5" :class="child ? 'bg-white/[0.015]' : ''">
    <div class="flex items-center gap-3 min-w-0" :class="child ? 'pl-8' : ''">
      <CategoryBadge :icon="category.icon" :size="child ? 'sm' : 'md'" />
      <span class="text-sm truncate" :class="child ? 'text-slate-300' : 'text-slate-100 font-medium'">{{ category.name }}</span>
    </div>

    <select
      v-if="category.kind === 'EXPENSE'"
      :value="category.essentiality"
      class="hidden md:block text-xs rounded-lg px-2 py-1.5 bg-slate-900/60 border border-white/10 outline-none focus:border-indigo-500"
      :aria-label="`Essencialidade de ${category.name}`"
      :title="ESSENTIALITY[category.essentiality].hint"
      @change="emit('essentiality', ($event.target as HTMLSelectElement).value as Essentiality)"
    >
      <option v-for="(e, key) in ESSENTIALITY" :key="key" :value="key">{{ e.short }}</option>
    </select>
    <span v-else class="hidden md:block" />

    <span class="hidden md:block text-sm text-right tabular" :class="spent ? 'text-slate-100' : 'text-slate-600'">{{ formatBRLShort(spent) }}</span>

    <div class="col-span-2 md:col-span-1 flex flex-col gap-1.5 min-w-0">
      <template v-if="category.kind === 'EXPENSE'">
        <form v-if="editingBudget" class="flex items-center gap-2" @submit.prevent="saveBudget">
          <div class="relative flex-1">
            <span class="absolute left-2.5 top-1/2 -translate-y-1/2 text-xs text-slate-500">R$</span>
            <input ref="input" v-model="budgetInput" inputmode="decimal" placeholder="Sem orçamento" class="field !py-1.5 pl-8 text-xs" :aria-label="`Orçamento mensal de ${category.name}`" @keydown.esc="editingBudget = false" />
          </div>
          <button type="submit" class="btn-primary !px-3 !py-1.5 text-xs">OK</button>
          <button type="button" class="btn-ghost !px-2 !py-1.5" aria-label="Cancelar" @click="editingBudget = false"><AppIcon name="x" class="h-3.5 w-3.5" /></button>
        </form>
        <button v-else type="button" class="text-left text-xs flex items-center gap-2 group" :disabled="saving" @click="startBudget">
          <span v-if="budget" class="text-slate-300"><span class="text-slate-100 font-semibold tabular">{{ formatBRLShort(budget) }}</span>/mês</span>
          <span v-else class="text-slate-500 group-hover:text-slate-300">+ Definir orçamento</span>
          <AppIcon name="pencil" class="h-3 w-3 text-slate-600 opacity-0 group-hover:opacity-100" />
        </button>
        <p v-if="budgetError" class="text-[11px] text-red-400">{{ budgetError }}</p>
        <div v-if="budget && !editingBudget" class="flex items-center gap-2">
          <div class="flex-1 h-1.5 rounded-full overflow-hidden" style="background: var(--viz-track)">
            <div
              class="h-1.5 rounded-full"
              :style="{ width: `${Math.min(100, pct)}%`, background: state === 'over' ? 'var(--status-critical)' : state === 'warning' ? 'var(--status-warning)' : 'var(--viz-series-1)' }"
            />
          </div>
          <span class="text-[11px] tabular flex items-center gap-1" :class="state === 'over' ? 'text-red-300' : state === 'warning' ? 'text-amber-300' : 'text-slate-500'">
            <AppIcon v-if="state !== 'ok'" name="warning" class="h-3 w-3" />{{ formatPercent(pct) }}
          </span>
        </div>
      </template>
    </div>

    <div class="flex items-center justify-end gap-0.5 row-start-1 col-start-2 md:row-auto md:col-auto">
      <button v-if="!child && category.kind === 'EXPENSE'" class="btn-ghost !p-2" :title="`Nova subcategoria em ${category.name}`" @click="emit('addChild')">
        <AppIcon name="plus" class="h-4 w-4" />
      </button>
      <button class="btn-ghost !p-2" :title="`Editar ${category.name}`" @click="emit('edit')"><AppIcon name="pencil" class="h-4 w-4" /></button>
      <button class="btn-ghost !p-2" :title="`Arquivar ${category.name}`" @click="emit('archive')"><AppIcon name="trash" class="h-4 w-4" /></button>
    </div>
  </div>
</template>
