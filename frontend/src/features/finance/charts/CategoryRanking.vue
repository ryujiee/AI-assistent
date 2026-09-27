<script setup lang="ts">
// Where the money went: a ranked list with one-hue bars (length = amount).
// Every value is written beside its bar, so no tooltip is needed to read it.
import { computed, ref } from 'vue'
import type { CategoryTotal } from '@/api/finance'
import CategoryBadge from '../components/CategoryBadge.vue'
import DeltaChip from '../components/DeltaChip.vue'
import AppIcon from '@/components/AppIcon.vue'
import { formatBRLShort, formatPercent } from '../lib/format'

const props = defineProps<{ categories: CategoryTotal[]; income?: boolean; previousLabel?: string }>()
const emit = defineEmits<{ select: [id: number] }>()
const expanded = ref(false)

const visible = computed(() => props.categories.filter((c) => c.amount_cents > 0))
const shown = computed(() => (expanded.value ? visible.value : visible.value.slice(0, 7)))
const max = computed(() => Math.max(1, ...visible.value.map((c) => c.amount_cents)))
</script>

<template>
  <div class="viz flex flex-col gap-1">
    <ul class="flex flex-col">
      <li v-for="c in shown" :key="c.id">
        <button
          type="button"
          class="w-full grid grid-cols-[auto_1fr_auto] items-center gap-3 py-2.5 px-2 -mx-2 rounded-xl text-left hover:bg-white/[0.03] focus-visible:bg-white/[0.05] outline-none transition"
          :title="`Ver transações de ${c.name}`"
          @click="emit('select', c.id)"
        >
          <CategoryBadge :icon="c.icon" />
          <div class="min-w-0 flex flex-col gap-1.5">
            <div class="flex items-center justify-between gap-2">
              <span class="text-sm text-slate-100 truncate">{{ c.name }}</span>
              <span class="text-[11px] text-slate-500 tabular shrink-0">{{ formatPercent(c.share_pct, c.share_pct < 10 ? 1 : 0) }}</span>
            </div>
            <div class="relative h-2 rounded-full" style="background: var(--viz-track)">
              <div class="h-2 rounded-full" :style="{ width: `${Math.max(1.5, (c.amount_cents / max) * 100)}%`, background: 'var(--viz-series-1)' }" />
              <span
                v-if="!income && c.budget_cents"
                class="absolute -top-1 h-4 w-0.5 rounded bg-slate-200"
                :style="{ left: `${Math.min(100, (c.budget_cents / max) * 100)}%` }"
                :title="`Orçamento ${formatBRLShort(c.budget_cents)}`"
              />
            </div>
            <div v-if="(!income && c.budget_cents) || c.change_pct !== null" class="flex flex-wrap items-center gap-x-3 gap-y-1">
              <DeltaChip v-if="c.change_pct !== null" :pct="c.change_pct" :up-is-good="income" :label="previousLabel ? `vs ${previousLabel}` : undefined" />
              <span v-if="!income && c.budget_cents" class="text-[11px] flex items-center gap-1" :class="c.amount_cents > c.budget_cents ? 'text-red-300' : 'text-slate-500'">
                <AppIcon v-if="c.amount_cents > c.budget_cents" name="warning" class="h-3 w-3" />
                {{ c.amount_cents > c.budget_cents ? 'Acima do orçamento de' : 'Orçamento' }} {{ formatBRLShort(c.budget_cents) }}
              </span>
            </div>
          </div>
          <span class="w-28 text-right text-sm font-semibold text-slate-50 tabular">{{ formatBRLShort(c.amount_cents) }}</span>
        </button>
      </li>
    </ul>
    <button v-if="visible.length > 7" type="button" class="self-start text-xs text-slate-400 hover:text-slate-100 mt-1" @click="expanded = !expanded">
      {{ expanded ? 'Mostrar menos' : `Ver todas (${visible.length})` }}
    </button>
  </div>
</template>
