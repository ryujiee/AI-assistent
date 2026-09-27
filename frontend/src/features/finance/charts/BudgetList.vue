<script setup lang="ts">
// Budget meters: the fill carries the state (accent -> warning -> critical),
// always paired with an icon and a label; the track is a lighter step of the
// same blue.
import type { BudgetStatus } from '@/api/finance'
import AppIcon from '@/components/AppIcon.vue'
import CategoryBadge from '../components/CategoryBadge.vue'
import { formatBRLShort, formatPercent } from '../lib/format'

defineProps<{ budgets: BudgetStatus[]; compact?: boolean }>()

const fill = (b: BudgetStatus) =>
  b.state === 'over' ? 'var(--status-critical)' : b.state === 'warning' ? 'var(--status-warning)' : 'var(--viz-series-1)'
</script>

<template>
  <ul class="viz flex flex-col gap-4">
    <li v-for="b in budgets" :key="b.category_id" class="flex items-center gap-3">
      <CategoryBadge v-if="!compact" :icon="b.icon" size="sm" />
      <div class="flex-1 min-w-0 flex flex-col gap-1.5">
        <div class="flex items-center justify-between gap-2 text-sm">
          <span class="text-slate-200 truncate">{{ b.name }}</span>
          <span class="text-slate-400 text-xs tabular shrink-0">
            <span class="text-slate-50 font-semibold">{{ formatBRLShort(b.spent_cents) }}</span> de {{ formatBRLShort(b.budget_cents) }}
          </span>
        </div>
        <div class="h-2 rounded-full overflow-hidden" style="background: var(--viz-track)">
          <div class="h-2 rounded-full" :style="{ width: `${Math.min(100, b.pct)}%`, background: fill(b) }" />
        </div>
        <div class="flex items-center justify-between gap-2 text-[11px]">
          <span
            class="flex items-center gap-1"
            :class="b.state === 'over' ? 'text-red-300' : b.state === 'warning' ? 'text-amber-300' : 'text-slate-500'"
          >
            <AppIcon v-if="b.state !== 'ok'" name="warning" class="h-3 w-3" />
            {{ b.state === 'over' ? `Acima em ${formatBRLShort(-b.remaining_cents)}` : `Restam ${formatBRLShort(b.remaining_cents)}` }}
            · {{ formatPercent(b.pct) }}
          </span>
          <span v-if="b.projection_cents > b.budget_cents && b.state !== 'over'" class="text-slate-500">Projeção ~{{ formatBRLShort(Math.round(b.projection_cents / 1000) * 1000) }}</span>
        </div>
      </div>
    </li>
  </ul>
</template>
