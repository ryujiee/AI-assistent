<script setup lang="ts">
// Part-to-whole of spending by essentiality: three fixed classes, slots 1-3
// of the validated palette, 2px surface gaps, legend with values always shown.
import { computed } from 'vue'
import { ESSENTIALITY } from '../lib/period'
import { formatBRLShort, formatPercent } from '../lib/format'

const props = defineProps<{ split: Record<string, number> }>()

const order = [
  { key: 'ESSENTIAL', color: 'var(--viz-series-1)' },
  { key: 'IMPORTANT', color: 'var(--viz-series-3)' },
  { key: 'DISCRETIONARY', color: 'var(--viz-series-2)' },
]
const total = computed(() => order.reduce((s, o) => s + Math.max(0, props.split[o.key] ?? 0), 0))
const parts = computed(() =>
  order.map((o) => {
    const value = Math.max(0, props.split[o.key] ?? 0)
    return { ...o, label: ESSENTIALITY[o.key].label, hint: ESSENTIALITY[o.key].hint, value, pct: total.value ? (value / total.value) * 100 : 0 }
  }),
)
</script>

<template>
  <div class="viz flex flex-col gap-4">
    <div v-if="total > 0" class="flex h-3 w-full gap-[2px]" role="img" :aria-label="parts.map((p) => `${p.label} ${formatPercent(p.pct)}`).join(', ')">
      <template v-for="p in parts" :key="p.key">
        <div
          v-if="p.value > 0"
          class="h-3 first:rounded-l-full last:rounded-r-full"
          :style="{ width: `${p.pct}%`, background: p.color }"
          :title="`${p.label}: ${formatBRLShort(p.value)} (${formatPercent(p.pct)})`"
        />
      </template>
    </div>
    <div v-else class="h-3 rounded-full" style="background: var(--viz-track)" />
    <ul class="flex flex-col gap-2.5">
      <li v-for="p in parts" :key="p.key" class="flex items-start gap-3">
        <span class="mt-1 h-2.5 w-2.5 rounded-sm shrink-0" :style="{ background: p.color }" />
        <div class="flex-1 min-w-0">
          <div class="flex items-center justify-between gap-2 text-sm">
            <span class="text-slate-200">{{ p.label }}</span>
            <span class="text-slate-50 font-semibold tabular">{{ formatBRLShort(p.value) }} <span class="text-slate-500 font-normal">· {{ formatPercent(p.pct) }}</span></span>
          </div>
          <p class="text-[11px] text-slate-500">{{ p.hint }}</p>
        </div>
      </li>
    </ul>
  </div>
</template>
