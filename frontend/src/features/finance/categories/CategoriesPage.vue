<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import AppIcon from '@/components/AppIcon.vue'
import EmptyState from '@/components/EmptyState.vue'
import CategoryRow from './CategoryRow.vue'
import CategoryModal from './CategoryModal.vue'
import { ledgerApi, type Category, type Essentiality, type Period } from '@/api/finance'
import { ApiError } from '@/api/client'
import { formatBRLShort } from '../lib/format'

const categories = ref<Category[]>([])
const spent = ref<Record<string, number>>({})
const month = ref<Period | null>(null)
const loading = ref(true)
const error = ref('')
const message = ref('')
const savingId = ref<number | null>(null)
const kind = ref<'EXPENSE' | 'INCOME'>('EXPENSE')
const modal = ref<{ category?: Category | null; parent?: Category | null } | null>(null)

async function load() {
  error.value = ''
  try {
    const res = await ledgerApi.categories()
    categories.value = res.categories
    spent.value = res.spent
    month.value = res.month
  } catch (err) {
    error.value = err instanceof ApiError ? err.message : 'Não foi possível carregar as categorias.'
  } finally {
    loading.value = false
  }
}
onMounted(load)

const tree = computed(() => {
  const list = categories.value.filter((c) => c.kind === kind.value)
  return list.filter((c) => c.parent_id === null).map((p) => ({ parent: p, children: list.filter((c) => c.parent_id === p.id) }))
})
const parents = computed(() => categories.value.filter((c) => c.kind === 'EXPENSE' && c.parent_id === null))
const budgeted = computed(() => categories.value.filter((c) => c.monthly_budget_cents))
const budgetTotal = computed(() => budgeted.value.filter((c) => c.parent_id === null || !budgeted.value.some((p) => p.id === c.parent_id)).reduce((s, c) => s + (c.monthly_budget_cents ?? 0), 0))
const spentOf = (id: number) => spent.value[String(id)] ?? 0

async function update(c: Category, patch: Parameters<typeof ledgerApi.updateCategory>[1], done: string) {
  savingId.value = c.id
  message.value = ''
  try {
    await ledgerApi.updateCategory(c.id, patch)
    message.value = done
    await load()
  } catch (err) {
    message.value = err instanceof ApiError ? err.message : 'Não foi possível salvar.'
  } finally {
    savingId.value = null
  }
}

const setBudget = (c: Category, cents: number | null) =>
  update(c, cents === null ? { clear_budget: true } : { monthly_budget_cents: cents }, cents === null ? `Orçamento de ${c.name} removido.` : `Orçamento de ${c.name}: ${formatBRLShort(cents)}/mês.`)
const setEssentiality = (c: Category, e: Essentiality) => update(c, { essentiality: e }, `${c.name} atualizada.`)
function archive(c: Category) {
  if (!confirm(`Arquivar “${c.name}”? Ela some das opções, mas os lançamentos antigos continuam nos relatórios.`)) return
  update(c, { archived: true }, `${c.name} arquivada.`)
}
function onSaved() {
  modal.value = null
  load()
}
</script>

<template>
  <div class="flex flex-col gap-5">
    <div class="flex flex-wrap items-center gap-3">
      <div class="flex items-center gap-1 p-1 rounded-xl bg-slate-900/60 border border-white/5" role="group" aria-label="Tipo de categoria">
        <button
          v-for="k in [{ k: 'EXPENSE', l: 'Despesas' }, { k: 'INCOME', l: 'Receitas' }]"
          :key="k.k"
          class="px-3 py-1.5 rounded-lg text-xs font-medium transition"
          :class="kind === k.k ? 'bg-indigo-600 text-white' : 'text-slate-400 hover:text-slate-100'"
          :aria-pressed="kind === k.k"
          @click="kind = k.k as 'EXPENSE' | 'INCOME'"
        >
          {{ k.l }}
        </button>
      </div>
      <p v-if="month" class="text-xs text-slate-500">Gastos de {{ month.label.toLowerCase() }}</p>
      <button class="btn-primary ml-auto !py-2" @click="modal = { category: null, parent: null }"><AppIcon name="plus" class="h-4 w-4" />Nova categoria</button>
    </div>

    <div v-if="kind === 'EXPENSE' && !loading" class="grid gap-4 sm:grid-cols-3">
      <div class="glass-card rounded-2xl p-4">
        <p class="text-xs text-slate-400">Categorias com orçamento</p>
        <p class="text-2xl font-semibold mt-1">{{ budgeted.length }}</p>
      </div>
      <div class="glass-card rounded-2xl p-4">
        <p class="text-xs text-slate-400">Total planejado no mês</p>
        <p class="text-2xl font-semibold mt-1">{{ formatBRLShort(budgetTotal) }}</p>
      </div>
      <div class="glass-card rounded-2xl p-4 text-xs text-slate-400 leading-relaxed flex items-center gap-3">
        <AppIcon name="info" class="h-5 w-5 text-indigo-300 shrink-0" />
        Alertas no grupo ao atingir 70%, 80% e 100% de cada orçamento, uma vez por mês.
      </div>
    </div>

    <div v-if="loading" class="flex flex-col gap-2"><div v-for="i in 6" :key="i" class="skeleton h-14" /></div>
    <section v-else-if="error" class="glass-card rounded-2xl">
      <EmptyState icon="warning" tone="error" title="Não foi possível carregar" :description="error">
        <button class="btn-secondary" @click="load">Tentar novamente</button>
      </EmptyState>
    </section>
    <section v-else class="glass-card rounded-2xl overflow-hidden">
      <div class="hidden md:grid grid-cols-[minmax(0,1.4fr)_150px_110px_minmax(0,1.3fr)_108px] gap-x-4 px-5 py-3 border-b border-white/5 text-[11px] font-semibold uppercase tracking-wider text-slate-500">
        <span>Categoria</span><span>{{ kind === 'EXPENSE' ? 'Essencialidade' : '' }}</span><span class="text-right">No mês</span><span>{{ kind === 'EXPENSE' ? 'Orçamento mensal' : '' }}</span><span />
      </div>
      <EmptyState v-if="!tree.length" icon="tag" title="Nenhuma categoria" description="Crie a primeira categoria." />
      <div v-for="node in tree" :key="node.parent.id" class="border-b border-white/5 last:border-0">
        <CategoryRow
          :category="node.parent"
          :spent="spentOf(node.parent.id)"
          :saving="savingId === node.parent.id"
          @budget="setBudget(node.parent, $event)"
          @essentiality="setEssentiality(node.parent, $event)"
          @edit="modal = { category: node.parent }"
          @add-child="modal = { parent: node.parent }"
          @archive="archive(node.parent)"
        />
        <CategoryRow
          v-for="child in node.children"
          :key="child.id"
          child
          :category="child"
          :spent="spentOf(child.id)"
          :saving="savingId === child.id"
          @budget="setBudget(child, $event)"
          @essentiality="setEssentiality(child, $event)"
          @edit="modal = { category: child }"
          @archive="archive(child)"
        />
      </div>
    </section>
    <p v-if="message" role="status" class="text-xs text-slate-400">{{ message }}</p>

    <CategoryModal v-if="modal" :category="modal.category" :parent="modal.parent" :kind="kind" :parents="parents" @close="modal = null" @saved="onSaved" />
  </div>
</template>
