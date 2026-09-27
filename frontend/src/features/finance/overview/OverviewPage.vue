<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import AppIcon from '@/components/AppIcon.vue'
import EmptyState from '@/components/EmptyState.vue'
import PeriodFilter from '../components/PeriodFilter.vue'
import StatTile from '../components/StatTile.vue'
import DeltaChip from '../components/DeltaChip.vue'
import SectionCard from '../components/SectionCard.vue'
import InsightList from '../components/InsightList.vue'
import CategoryBadge from '../components/CategoryBadge.vue'
import CumulativeChart from '../charts/CumulativeChart.vue'
import CategoryRanking from '../charts/CategoryRanking.vue'
import EssentialityBar from '../charts/EssentialityBar.vue'
import BudgetList from '../charts/BudgetList.vue'
import { financeApi, ledgerApi, type Member, type Overview } from '@/api/finance'
import { ApiError } from '@/api/client'
import { periodQuery, type PeriodState } from '../lib/period'
import { formatBRL, formatBRLRound, formatBRLShort, formatShortDate, formatPercent } from '../lib/format'

const router = useRouter()
const period = ref<PeriodState>({ preset: 'this_month', start: '', end: '' })
const memberId = ref<number | null>(null)
const type = ref<'EXPENSE' | 'INCOME'>('EXPENSE')
const members = ref<Member[]>([])
const data = ref<Overview | null>(null)
const loading = ref(true)
const refreshing = ref(false)
const error = ref('')
const allInsights = ref(false)

async function load() {
  const q = periodQuery(period.value)
  if (!q) return
  if (data.value) refreshing.value = true
  else loading.value = true
  error.value = ''
  try {
    data.value = await ledgerApi.overview({ ...q, member_id: memberId.value, type: type.value })
  } catch (err) {
    error.value = err instanceof ApiError ? err.message : 'Não foi possível carregar os dados.'
  } finally {
    loading.value = false
    refreshing.value = false
  }
}

onMounted(async () => {
  load()
  try {
    members.value = (await financeApi.members()).members
  } catch {
    members.value = []
  }
})
watch([period, memberId, type], load, { deep: true })

const s = computed(() => data.value?.summary)
const income = computed(() => type.value === 'INCOME')
const previousShort = computed(() => (s.value ? s.value.previous.label.replace(/ de \d{4}/, '') : ''))
const isEmpty = computed(() => s.value && s.value.transactions === 0 && s.value.previous_expenses_cents === 0 && s.value.previous_income_cents === 0)
const ranking = computed(() => (income.value ? s.value?.income_categories : s.value?.categories) ?? [])
const memberTotal = computed(() => (s.value?.members ?? []).reduce((a, m) => a + Math.max(0, m.expenses_cents), 0))

function openCategory(id: number) {
  const q = periodQuery(period.value)
  router.push({ path: '/financeiro/transacoes', query: { ...q, category_id: String(id) } as Record<string, string> })
}
</script>

<template>
  <div class="flex flex-col gap-6">
    <!-- Filters: one row, scoping everything below -->
    <div class="flex flex-wrap items-center gap-3">
      <PeriodFilter v-model="period" />
      <select v-model="memberId" class="field !w-auto !py-2 text-xs" aria-label="Pessoa">
        <option :value="null">Todas as pessoas</option>
        <option v-for="m in members" :key="m.id" :value="m.id">{{ m.display_name }}</option>
      </select>
      <div class="flex items-center gap-1 p-1 rounded-xl bg-slate-900/60 border border-white/5" role="group" aria-label="Tipo">
        <button
          v-for="t in [{ k: 'EXPENSE', l: 'Gastos' }, { k: 'INCOME', l: 'Receitas' }]"
          :key="t.k"
          type="button"
          class="px-3 py-1.5 rounded-lg text-xs font-medium transition"
          :class="type === t.k ? 'bg-white/10 text-white' : 'text-slate-400 hover:text-slate-100'"
          :aria-pressed="type === t.k"
          @click="type = t.k as 'EXPENSE' | 'INCOME'"
        >
          {{ t.l }}
        </button>
      </div>
      <span v-if="s" class="text-xs text-slate-500 ml-auto">{{ s.period.label }} · comparado a {{ s.previous.label }}</span>
    </div>

    <div v-if="loading" class="flex flex-col gap-6">
      <div class="grid gap-4 grid-cols-1 sm:grid-cols-2 xl:grid-cols-4"><div v-for="i in 4" :key="i" class="skeleton h-36" /></div>
      <div class="grid gap-6 xl:grid-cols-3"><div class="skeleton h-80 xl:col-span-2" /><div class="skeleton h-80" /></div>
    </div>

    <section v-else-if="error && !data" class="glass-card rounded-2xl">
      <EmptyState icon="warning" tone="error" title="Não foi possível carregar a visão geral" :description="error">
        <button class="btn-secondary" @click="load">Tentar novamente</button>
      </EmptyState>
    </section>

    <template v-else-if="s && data">
      <section v-if="isEmpty" class="glass-card rounded-2xl">
        <EmptyState icon="inbox" title="Nenhum lançamento nesse período" description="Escreva no grupo financeiro, por exemplo “gastei 42 no almoço”, ou envie a foto de um comprovante. Também dá para lançar pelo painel.">
          <div class="flex gap-2">
            <RouterLink to="/financeiro/whatsapp" class="btn-secondary"><AppIcon name="chat" class="h-4 w-4" />WhatsApp Financeiro</RouterLink>
            <RouterLink to="/financeiro/transacoes?novo=1" class="btn-primary"><AppIcon name="plus" class="h-4 w-4" />Novo lançamento</RouterLink>
          </div>
        </EmptyState>
      </section>

      <div v-else class="flex flex-col gap-6 transition-opacity" :class="refreshing ? 'opacity-60' : ''">
        <!-- KPIs -->
        <div class="grid gap-4 grid-cols-1 sm:grid-cols-2 xl:grid-cols-4">
          <StatTile label="Gastos no período" :value="formatBRLRound(s.expenses_cents)" :exact="formatBRL(s.expenses_cents)" icon="arrowUp" hero>
            <DeltaChip :pct="s.expense_change_pct" :label="`vs ${previousShort}`" />
            <span v-if="s.expense_change_pct === null">Sem base de comparação</span>
          </StatTile>
          <StatTile label="Receitas" :value="formatBRLRound(s.income_cents)" :exact="formatBRL(s.income_cents)" icon="arrowDown">
            <DeltaChip :pct="s.income_change_pct" up-is-good :label="`vs ${previousShort}`" />
            <span v-if="s.income_change_pct === null">{{ s.income_cents ? 'Sem base de comparação' : 'Nenhuma receita registrada' }}</span>
          </StatTile>
          <StatTile label="Saldo" :value="formatBRLRound(s.balance_cents)" :exact="formatBRL(s.balance_cents)" icon="scale">
            <span>Receitas menos gastos</span>
            <span v-if="s.transfers_cents" class="text-slate-600">· transferências fora da conta</span>
          </StatTile>
          <StatTile label="Média diária" :value="formatBRLRound(s.daily_average_cents)" :exact="formatBRL(s.daily_average_cents)" icon="calendar">
            <span v-if="s.projection_cents !== null">Projeção do mês: ~{{ formatBRLShort(Math.round(s.projection_cents / 1000) * 1000) }}</span>
            <span v-else>{{ s.transactions }} lançamentos</span>
          </StatTile>
        </div>

        <p v-if="s.pending > 0" class="text-xs text-amber-300/90 flex items-center gap-2 -mt-2">
          <AppIcon name="warning" class="h-3.5 w-3.5" />
          {{ s.pending }} lançamento(s) aguardando confirmação não entram nos totais.
          <RouterLink to="/financeiro/transacoes?status=PENDING&period=all" class="underline underline-offset-2 hover:text-amber-200">Revisar</RouterLink>
        </p>

        <!-- Evolution + highlights -->
        <div class="grid gap-6 xl:grid-cols-3 items-start">
          <SectionCard class="xl:col-span-2" :title="income ? 'Evolução das receitas' : 'Evolução dos gastos'" :subtitle="`Acumulado no período · ${s.period.label}`">
            <CumulativeChart :series="data.series" :current-label="s.period.label" :previous-label="s.previous.label" />
          </SectionCard>
          <SectionCard title="Destaques do período" subtitle="Fatos calculados a partir dos lançamentos">
            <InsightList v-if="data.insights.length" :insights="allInsights ? data.insights : data.insights.slice(0, 5)" />
            <button v-if="data.insights.length > 5" type="button" class="self-start text-xs text-slate-400 hover:text-slate-100" @click="allInsights = !allInsights">
              {{ allInsights ? 'Mostrar menos' : `Ver todos (${data.insights.length})` }}
            </button>
            <p v-else class="text-sm text-slate-500">Nada fora do comum até agora.</p>
          </SectionCard>
        </div>

        <!-- Categories + side column -->
        <div class="grid gap-6 xl:grid-cols-3 items-start">
          <SectionCard class="xl:col-span-2" :title="income ? 'Receitas por categoria' : 'Para onde foi o dinheiro'" subtitle="Clique numa categoria para ver os lançamentos">
            <CategoryRanking v-if="ranking.some((c) => c.amount_cents > 0)" :categories="ranking" :income="income" :previous-label="previousShort" @select="openCategory" />
            <p v-else class="text-sm text-slate-500">Nenhum valor nesse período.</p>
          </SectionCard>

          <div class="flex flex-col gap-6">
            <SectionCard v-if="!income" title="Essencial × escolha" subtitle="Pela essencialidade de cada categoria">
              <EssentialityBar :split="s.by_essentiality" />
            </SectionCard>

            <SectionCard title="Quem pagou" subtitle="Mostra quem pagou, não quem deve a quem">
              <ul v-if="s.members.length" class="viz flex flex-col gap-3">
                <li v-for="m in s.members" :key="m.id" class="flex flex-col gap-1.5">
                  <div class="flex items-center justify-between text-sm">
                    <span class="flex items-center gap-2 text-slate-200">
                      <span class="h-6 w-6 rounded-full bg-indigo-500/15 text-indigo-300 text-[11px] font-semibold flex items-center justify-center">{{ m.name.charAt(0).toUpperCase() }}</span>
                      {{ m.name }}
                    </span>
                    <span class="font-semibold text-slate-50 tabular">{{ formatBRLShort(income ? m.income_cents : m.expenses_cents) }}</span>
                  </div>
                  <div v-if="!income" class="h-1.5 rounded-full" style="background: var(--viz-track)">
                    <div class="h-1.5 rounded-full" :style="{ width: `${memberTotal ? (Math.max(0, m.expenses_cents) / memberTotal) * 100 : 0}%`, background: 'var(--viz-series-1)' }" />
                  </div>
                </li>
              </ul>
              <p v-else class="text-sm text-slate-500">Escolha o grupo na aba WhatsApp Financeiro para identificar quem pagou.</p>
            </SectionCard>

            <SectionCard title="Orçamentos do mês">
              <template #action>
                <RouterLink to="/financeiro/categorias" class="text-xs text-slate-400 hover:text-slate-100">Editar</RouterLink>
              </template>
              <BudgetList v-if="data.budgets.length" :budgets="data.budgets.slice(0, 5)" compact />
              <p v-else class="text-sm text-slate-500">
                Nenhum orçamento definido. Defina limites em Categorias & Orçamentos ou diga no grupo “limite de 600 para restaurantes”.
              </p>
            </SectionCard>
          </div>
        </div>

        <!-- Largest -->
        <SectionCard :title="income ? 'Maiores receitas' : 'Maiores gastos'" :subtitle="s.period.label">
          <ul v-if="data.largest.length" class="flex flex-col divide-y divide-white/5">
            <li v-for="t in data.largest" :key="t.id">
              <RouterLink :to="`/financeiro/transacoes?id=${t.id}`" class="flex items-center gap-3 py-3 hover:bg-white/[0.02] -mx-2 px-2 rounded-lg">
                <CategoryBadge :icon="t.category_icon" size="sm" />
                <div class="flex-1 min-w-0">
                  <p class="text-sm text-slate-100 truncate">{{ t.description || t.category_name || 'Sem descrição' }}</p>
                  <p class="text-xs text-slate-500 truncate">{{ t.category_name || 'Sem categoria' }} · {{ formatShortDate(t.transaction_date) }}<template v-if="t.payer_name"> · {{ t.payer_name }}</template></p>
                </div>
                <span class="text-sm font-semibold text-slate-50 tabular">{{ formatBRL(t.amount_cents) }}</span>
                <span class="hidden sm:block text-xs text-slate-500 w-12 text-right tabular">{{ s.expenses_cents && !income ? formatPercent((t.amount_cents / s.expenses_cents) * 100) : '' }}</span>
              </RouterLink>
            </li>
          </ul>
          <p v-else class="text-sm text-slate-500">Nenhum lançamento nesse período.</p>
        </SectionCard>
      </div>
    </template>
  </div>
</template>
