<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import AppIcon, { type IconName } from '@/components/AppIcon.vue'
import AppSpinner from '@/components/AppSpinner.vue'
import EmptyState from '@/components/EmptyState.vue'
import PeriodFilter from '../components/PeriodFilter.vue'
import CategorySelect from '../components/CategorySelect.vue'
import CategoryBadge from '../components/CategoryBadge.vue'
import TransactionDrawer from './TransactionDrawer.vue'
import { financeApi, ledgerApi, type Category, type Member, type Transaction } from '@/api/finance'
import { ApiError } from '@/api/client'
import { periodQuery, PENDING_LABELS, SOURCE_LABELS, type PeriodState, type PresetKey } from '../lib/period'
import { formatBRL, formatBRLShort, formatDayHeading, parseBRL } from '../lib/format'

const route = useRoute()
const router = useRouter()
const PAGE = 50

const q = (k: string) => (typeof route.query[k] === 'string' ? (route.query[k] as string) : '')
const period = ref<PeriodState>({ preset: (q('period') || 'this_month') as PresetKey, start: q('start'), end: q('end') })
const filters = reactive({
  search: q('q'),
  category_id: q('category_id') ? Number(q('category_id')) : (null as number | null),
  member_id: null as number | null,
  type: q('type'),
  status: q('status'),
  min: '',
  max: '',
})
const items = ref<Transaction[]>([])
const total = ref(0)
const loading = ref(true)
const loadingMore = ref(false)
const error = ref('')
const categories = ref<Category[]>([])
const members = ref<Member[]>([])
const openId = ref<number | null>(q('id') ? Number(q('id')) : null)
const creating = ref(q('novo') === '1')
const toast = ref<{ text: string; undoId?: number } | null>(null)
let searchTimer: ReturnType<typeof setTimeout> | null = null

function query(offset = 0) {
  const p = periodQuery(period.value)
  if (!p) return null
  return {
    ...p,
    q: filters.search.trim() || undefined,
    category_id: filters.category_id,
    member_id: filters.member_id,
    type: filters.type || undefined,
    status: filters.status || undefined,
    min_cents: filters.min ? parseBRL(filters.min) : null,
    max_cents: filters.max ? parseBRL(filters.max) : null,
    limit: PAGE,
    offset,
  }
}

async function load() {
  const params = query()
  if (!params) return
  loading.value = items.value.length === 0
  error.value = ''
  try {
    const res = await ledgerApi.transactions(params)
    items.value = res.items
    total.value = res.total
  } catch (err) {
    error.value = err instanceof ApiError ? err.message : 'Não foi possível carregar os lançamentos.'
  } finally {
    loading.value = false
  }
}

async function loadMore() {
  const params = query(items.value.length)
  if (!params) return
  loadingMore.value = true
  try {
    const res = await ledgerApi.transactions(params)
    items.value = [...items.value, ...res.items]
    total.value = res.total
  } finally {
    loadingMore.value = false
  }
}

onMounted(async () => {
  load()
  const [cats, mem] = await Promise.allSettled([ledgerApi.categories(), financeApi.members()])
  if (cats.status === 'fulfilled') categories.value = cats.value.categories
  if (mem.status === 'fulfilled') members.value = mem.value.members
})

watch(period, load, { deep: true })
watch(() => [filters.category_id, filters.member_id, filters.type, filters.status], load)
watch(() => [filters.search, filters.min, filters.max], () => {
  if (searchTimer) clearTimeout(searchTimer)
  searchTimer = setTimeout(load, 300)
})

// Keep the URL shareable without reloading the page.
watch([openId, creating], ([id, isNew]) => {
  const next = { ...route.query }
  delete next.id
  delete next.novo
  if (id) next.id = String(id)
  if (isNew) next.novo = '1'
  router.replace({ query: next })
})

const groups = computed(() => {
  const byDay = new Map<string, Transaction[]>()
  for (const t of items.value) {
    if (!byDay.has(t.transaction_date)) byDay.set(t.transaction_date, [])
    byDay.get(t.transaction_date)!.push(t)
  }
  return [...byDay.entries()].map(([day, list]) => ({
    day,
    list,
    spent: list.reduce((s, t) => s + (t.status !== 'CONFIRMED' ? 0 : t.type === 'EXPENSE' ? t.amount_cents : t.type === 'REFUND' ? -t.amount_cents : 0), 0),
  }))
})

const hasFilters = computed(() => !!(filters.search || filters.category_id || filters.member_id || filters.type || filters.status || filters.min || filters.max))

function clearFilters() {
  Object.assign(filters, { search: '', category_id: null, member_id: null, type: '', status: '', min: '', max: '' })
}

const sourceIcon: Record<string, IconName> = { WHATSAPP_TEXT: 'chat', WHATSAPP_AUDIO: 'microphone', WHATSAPP_RECEIPT: 'paperclip', WEB: 'globe' }

function label(t: Transaction) {
  if (t.type === 'TRANSFER') return 'Transferência'
  if (!t.category_name) return 'Sem categoria'
  return t.parent_category_name ? `${t.parent_category_name} › ${t.category_name}` : t.category_name
}

function onChanged() {
  load()
}
function onDeleted(id: number) {
  openId.value = null
  items.value = items.value.filter((t) => t.id !== id)
  total.value = Math.max(0, total.value - 1)
  toast.value = { text: 'Lançamento excluído.', undoId: id }
  setTimeout(() => {
    if (toast.value?.undoId === id) toast.value = null
  }, 8000)
}
async function undoDelete() {
  const id = toast.value?.undoId
  if (!id) return
  toast.value = null
  await ledgerApi.restoreTransaction(id)
  load()
}
</script>

<template>
  <div class="flex flex-col gap-5">
    <div class="flex flex-wrap items-center gap-3">
      <PeriodFilter v-model="period" with-all />
      <button class="btn-primary ml-auto !py-2" @click="creating = true"><AppIcon name="plus" class="h-4 w-4" />Novo lançamento</button>
    </div>

    <section class="glass-card rounded-2xl p-4 grid gap-3 grid-cols-2 md:grid-cols-3 xl:grid-cols-6">
      <div class="relative col-span-2 md:col-span-3 xl:col-span-2">
        <AppIcon name="search" class="h-4 w-4 text-slate-500 absolute left-3 top-1/2 -translate-y-1/2" />
        <input v-model="filters.search" type="search" placeholder="Buscar descrição, estabelecimento..." aria-label="Buscar" class="field pl-9 !py-2" />
      </div>
      <CategorySelect v-model="filters.category_id" :categories="categories" class="!py-2" aria-label="Categoria" />
      <select v-model="filters.member_id" class="field !py-2" aria-label="Pessoa">
        <option :value="null">Todas as pessoas</option>
        <option v-for="m in members" :key="m.id" :value="m.id">{{ m.display_name }}</option>
      </select>
      <select v-model="filters.type" class="field !py-2" aria-label="Tipo">
        <option value="">Todos os tipos</option>
        <option value="EXPENSE">Despesas</option>
        <option value="INCOME">Receitas</option>
        <option value="TRANSFER">Transferências</option>
        <option value="REFUND">Estornos</option>
      </select>
      <select v-model="filters.status" class="field !py-2" aria-label="Situação">
        <option value="">Todas as situações</option>
        <option value="CONFIRMED">Confirmados</option>
        <option value="PENDING">Pendentes</option>
      </select>
      <div class="col-span-2 md:col-span-3 xl:col-span-6 flex flex-wrap items-center gap-2 text-xs text-slate-500">
        <span>Valor entre</span>
        <input v-model="filters.min" inputmode="decimal" placeholder="R$ mín." aria-label="Valor mínimo" class="field !py-1.5 !w-28" />
        <span>e</span>
        <input v-model="filters.max" inputmode="decimal" placeholder="R$ máx." aria-label="Valor máximo" class="field !py-1.5 !w-28" />
        <button v-if="hasFilters" class="ml-auto text-slate-400 hover:text-slate-100" @click="clearFilters">Limpar filtros</button>
        <span v-if="!loading" class="tabular" :class="hasFilters ? '' : 'ml-auto'">{{ total }} lançamento(s)</span>
      </div>
    </section>

    <div v-if="loading" class="flex flex-col gap-3">
      <div v-for="i in 5" :key="i" class="skeleton h-16" />
    </div>

    <section v-else-if="error" class="glass-card rounded-2xl">
      <EmptyState icon="warning" tone="error" title="Não foi possível carregar" :description="error">
        <button class="btn-secondary" @click="load">Tentar novamente</button>
      </EmptyState>
    </section>

    <section v-else-if="!items.length" class="glass-card rounded-2xl">
      <EmptyState
        icon="inbox"
        :title="hasFilters ? 'Nada encontrado com esses filtros' : 'Nenhum lançamento nesse período'"
        :description="hasFilters ? 'Tente outro período ou limpe os filtros.' : 'Os gastos enviados no grupo do WhatsApp aparecem aqui automaticamente.'"
      >
        <button v-if="hasFilters" class="btn-secondary" @click="clearFilters">Limpar filtros</button>
      </EmptyState>
    </section>

    <div v-else class="flex flex-col gap-5">
      <section v-for="g in groups" :key="g.day" class="glass-card rounded-2xl overflow-hidden">
        <header class="flex items-center justify-between px-5 py-3 border-b border-white/5 bg-white/[0.02]">
          <h3 class="text-xs font-semibold text-slate-300 first-letter:uppercase">{{ formatDayHeading(g.day) }}</h3>
          <span v-if="g.spent" class="text-xs text-slate-500 tabular">{{ formatBRLShort(g.spent) }} em gastos</span>
        </header>
        <ul class="divide-y divide-white/5">
          <li v-for="t in g.list" :key="t.id">
            <button type="button" class="w-full flex items-center gap-3 px-5 py-3.5 text-left hover:bg-white/[0.03] focus-visible:bg-white/[0.05] outline-none transition" @click="openId = t.id">
              <CategoryBadge :icon="t.type === 'TRANSFER' ? '⇄' : t.category_icon" />
              <div class="flex-1 min-w-0">
                <p class="text-sm text-slate-100 truncate flex items-center gap-2">
                  {{ t.description || label(t) }}
                  <span v-if="t.status === 'PENDING'" class="text-[10px] font-semibold uppercase tracking-wider px-1.5 py-0.5 rounded bg-amber-500/10 text-amber-300 shrink-0">
                    {{ PENDING_LABELS[t.pending_reasons[0]] ?? 'Pendente' }}
                  </span>
                </p>
                <p class="text-xs text-slate-500 truncate flex items-center gap-1.5">
                  <span class="truncate">{{ label(t) }}</span>
                  <template v-if="t.payer_name"><span>·</span><span>{{ t.payer_name }}</span></template>
                </p>
              </div>
              <span class="hidden sm:flex items-center text-slate-500" :title="SOURCE_LABELS[t.source]">
                <AppIcon :name="sourceIcon[t.source]" class="h-4 w-4" />
                <span class="sr-only">{{ SOURCE_LABELS[t.source] }}</span>
              </span>
              <span
                class="text-sm font-semibold tabular w-28 text-right"
                :class="{
                  'text-emerald-300': t.type === 'INCOME' || t.type === 'REFUND',
                  'text-slate-400': t.type === 'TRANSFER',
                  'text-slate-50': t.type === 'EXPENSE',
                  'opacity-60': t.status === 'PENDING',
                }"
              >
                {{ t.type === 'INCOME' || t.type === 'REFUND' ? '+ ' : '' }}{{ formatBRL(t.amount_cents) }}
              </span>
            </button>
          </li>
        </ul>
      </section>
      <button v-if="items.length < total" class="btn-secondary self-center" :disabled="loadingMore" @click="loadMore">
        <AppSpinner v-if="loadingMore" class="h-4 w-4" />Carregar mais ({{ total - items.length }})
      </button>
    </div>

    <TransactionDrawer
      v-if="openId || creating"
      :id="creating ? null : openId"
      :creating="creating"
      :categories="categories"
      :members="members"
      @close="openId = null; creating = false"
      @changed="onChanged"
      @deleted="onDeleted"
    />

    <div v-if="toast" role="status" class="fixed bottom-6 left-1/2 -translate-x-1/2 z-50 glass-card !bg-slate-900/95 rounded-xl px-4 py-3 flex items-center gap-4 text-sm">
      {{ toast.text }}
      <button v-if="toast.undoId" class="text-indigo-300 font-semibold hover:text-indigo-200" @click="undoDelete">Desfazer</button>
    </div>
  </div>
</template>
