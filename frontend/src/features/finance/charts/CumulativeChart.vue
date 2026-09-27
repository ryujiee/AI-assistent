<script setup lang="ts">
// Cumulative spending (or income) of the period against the same span of the
// comparison period. Emphasis form: the current period in the accent hue with
// a light wash, the baseline in gray. Crosshair + tooltip on hover and on
// keyboard focus; every value is also in the table view.
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import type { Series } from '@/api/finance'
import { formatBRL, formatCompactBRL, formatShortDate, niceTicks } from '../lib/format'

const props = defineProps<{ series: Series; currentLabel: string; previousLabel: string }>()

const wrap = ref<HTMLElement | null>(null)
const width = ref(640)
const hover = ref<number | null>(null)
const showTable = ref(false)
let observer: ResizeObserver | null = null

onMounted(() => {
  if (!wrap.value || typeof ResizeObserver === 'undefined') return
  observer = new ResizeObserver(([entry]) => {
    width.value = Math.max(280, Math.round(entry.contentRect.width))
  })
  observer.observe(wrap.value)
})
onBeforeUnmount(() => observer?.disconnect())

const PLOT_H = 220
const PAD = { top: 20, right: 20, bottom: 28, left: 64 }

const cumulative = computed(() => {
  let c = 0
  let p = 0
  return props.series.points.map((pt) => {
    c += pt.current_cents
    p += pt.previous_cents
    return { ...pt, cur: c, prev: p }
  })
})

const n = computed(() => cumulative.value.length)
const plotW = computed(() => width.value - PAD.left - PAD.right)
const ticks = computed(() => {
  const last = cumulative.value[n.value - 1]
  // At least R$ 100 of range, so an empty period never shows "R$ 0" ticks.
  return niceTicks(Math.max(last?.cur ?? 0, last?.prev ?? 0, 10000))
})
const yMax = computed(() => ticks.value[ticks.value.length - 1] || 1)

const x = (i: number) => PAD.left + (n.value <= 1 ? plotW.value / 2 : (i * plotW.value) / (n.value - 1))
const y = (v: number) => PAD.top + PLOT_H - (v / yMax.value) * PLOT_H

function linePath(key: 'cur' | 'prev') {
  return cumulative.value.map((p, i) => `${i ? 'L' : 'M'}${x(i).toFixed(1)},${y(p[key]).toFixed(1)}`).join(' ')
}
const currentPath = computed(() => linePath('cur'))
const previousPath = computed(() => linePath('prev'))
const areaPath = computed(() => {
  if (!n.value) return ''
  return `${currentPath.value} L${x(n.value - 1).toFixed(1)},${y(0)} L${x(0).toFixed(1)},${y(0)} Z`
})
const hasPrevious = computed(() => cumulative.value.some((p) => p.prev > 0))

const xLabels = computed(() => {
  const step = Math.max(1, Math.ceil(n.value / Math.max(2, Math.floor(plotW.value / 70))))
  return cumulative.value.map((p, i) => ({ i, label: p.label })).filter(({ i }) => i % step === 0 || i === n.value - 1)
})

const last = computed(() => cumulative.value[n.value - 1])
const endLabelAnchor = computed(() => (x(n.value - 1) > width.value - 90 ? 'end' : 'start'))

function onMove(e: PointerEvent) {
  const rect = (e.currentTarget as SVGElement).getBoundingClientRect()
  const px = ((e.clientX - rect.left) / rect.width) * width.value
  const i = n.value <= 1 ? 0 : Math.round(((px - PAD.left) / plotW.value) * (n.value - 1))
  hover.value = Math.min(n.value - 1, Math.max(0, i))
}
function onKey(e: KeyboardEvent) {
  if (e.key !== 'ArrowLeft' && e.key !== 'ArrowRight') return
  e.preventDefault()
  const cur = hover.value ?? n.value - 1
  hover.value = Math.min(n.value - 1, Math.max(0, cur + (e.key === 'ArrowRight' ? 1 : -1)))
}

const tip = computed(() => {
  if (hover.value === null) return null
  const p = cumulative.value[hover.value]
  const left = x(hover.value)
  return { p, left, flip: left > width.value * 0.62 }
})
</script>

<template>
  <div class="viz flex flex-col gap-3">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <ul class="flex flex-wrap items-center gap-4 text-xs text-slate-300" aria-label="Legenda">
        <li class="flex items-center gap-2"><span class="h-0.5 w-4 rounded" style="background: var(--viz-series-1)"></span>{{ currentLabel }}</li>
        <li v-if="hasPrevious" class="flex items-center gap-2"><span class="h-0.5 w-4 rounded" style="background: var(--viz-previous)"></span>{{ previousLabel }}</li>
      </ul>
      <button type="button" class="text-xs text-slate-400 hover:text-slate-100 underline-offset-2 hover:underline" @click="showTable = !showTable">
        {{ showTable ? 'Ver gráfico' : 'Ver tabela' }}
      </button>
    </div>

    <div v-show="!showTable" ref="wrap" class="relative">
      <svg
        :viewBox="`0 0 ${width} ${PLOT_H + PAD.top + PAD.bottom}`"
        :width="width"
        :height="PLOT_H + PAD.top + PAD.bottom"
        class="block w-full h-auto outline-none focus-visible:ring-1 focus-visible:ring-indigo-500 rounded-lg"
        role="img"
        :aria-label="`Evolução acumulada: ${currentLabel} ${last ? formatBRL(last.cur) : ''}`"
        tabindex="0"
        @pointermove="onMove"
        @pointerleave="hover = null"
        @focus="hover = n - 1"
        @blur="hover = null"
        @keydown="onKey"
      >
        <g>
          <template v-for="t in ticks" :key="t">
            <line :x1="PAD.left" :x2="width - PAD.right" :y1="y(t)" :y2="y(t)" :stroke="t === 0 ? 'var(--viz-baseline)' : 'var(--viz-grid)'" stroke-width="1" />
            <text :x="PAD.left - 10" :y="y(t) + 4" text-anchor="end" class="fill-slate-500 tabular" font-size="11">{{ formatCompactBRL(t) }}</text>
          </template>
          <text v-for="l in xLabels" :key="l.i" :x="x(l.i)" :y="PAD.top + PLOT_H + 20" text-anchor="middle" class="fill-slate-500 tabular" font-size="11">{{ l.label }}</text>
        </g>
        <path :d="areaPath" fill="var(--viz-series-1)" fill-opacity="0.1" />
        <path v-if="hasPrevious" :d="previousPath" fill="none" stroke="var(--viz-previous)" stroke-width="2" stroke-linejoin="round" stroke-linecap="round" />
        <path :d="currentPath" fill="none" stroke="var(--viz-series-1)" stroke-width="2" stroke-linejoin="round" stroke-linecap="round" />
        <template v-if="last">
          <circle :cx="x(n - 1)" :cy="y(last.cur)" r="4" fill="var(--viz-series-1)" stroke="var(--viz-surface)" stroke-width="2" />
          <text :x="x(n - 1) + (endLabelAnchor === 'start' ? 8 : -8)" :y="y(last.cur) - 10" :text-anchor="endLabelAnchor" class="fill-slate-200 tabular" font-size="12" font-weight="600">
            {{ formatCompactBRL(last.cur) }}
          </text>
        </template>
        <g v-if="tip">
          <line :x1="tip.left" :x2="tip.left" :y1="PAD.top" :y2="PAD.top + PLOT_H" stroke="rgba(255,255,255,0.25)" stroke-width="1" />
          <circle :cx="tip.left" :cy="y(tip.p.cur)" r="4" fill="var(--viz-series-1)" stroke="var(--viz-surface)" stroke-width="2" />
          <circle v-if="hasPrevious" :cx="tip.left" :cy="y(tip.p.prev)" r="4" fill="var(--viz-previous)" stroke="var(--viz-surface)" stroke-width="2" />
        </g>
      </svg>
      <div
        v-if="tip"
        class="pointer-events-none absolute top-2 z-10 min-w-[190px] rounded-xl border border-white/10 bg-slate-950/95 px-3 py-2 text-xs shadow-xl"
        :style="tip.flip ? { right: `${width - tip.left + 12}px` } : { left: `${tip.left + 12}px` }"
      >
        <p class="text-slate-400 mb-1.5">{{ series.granularity === 'day' ? formatShortDate(tip.p.start) : `Semana de ${formatShortDate(tip.p.start)}` }}</p>
        <p class="flex items-center justify-between gap-4">
          <span class="flex items-center gap-2 text-slate-400"><span class="h-0.5 w-3 rounded" style="background: var(--viz-series-1)"></span>Acumulado</span>
          <span class="font-semibold text-slate-50 tabular">{{ formatBRL(tip.p.cur) }}</span>
        </p>
        <p class="flex items-center justify-between gap-4 mt-0.5">
          <span class="text-slate-500 pl-5">No dia</span>
          <span class="text-slate-300 tabular">{{ formatBRL(tip.p.current_cents) }}</span>
        </p>
        <p v-if="hasPrevious" class="flex items-center justify-between gap-4 mt-1">
          <span class="flex items-center gap-2 text-slate-400"><span class="h-0.5 w-3 rounded" style="background: var(--viz-previous)"></span>Anterior</span>
          <span class="font-semibold text-slate-200 tabular">{{ formatBRL(tip.p.prev) }}</span>
        </p>
      </div>
    </div>

    <div v-if="showTable" class="max-h-72 overflow-y-auto rounded-xl border border-white/5">
      <table class="w-full text-xs">
        <thead class="sticky top-0 bg-slate-900 text-slate-400">
          <tr>
            <th class="text-left font-medium px-3 py-2">{{ series.granularity === 'day' ? 'Dia' : 'Início' }}</th>
            <th class="text-right font-medium px-3 py-2">No período</th>
            <th class="text-right font-medium px-3 py-2">Acumulado</th>
            <th v-if="hasPrevious" class="text-right font-medium px-3 py-2">Acumulado anterior</th>
          </tr>
        </thead>
        <tbody class="tabular">
          <tr v-for="p in cumulative" :key="p.start" class="border-t border-white/5">
            <td class="px-3 py-1.5 text-slate-300">{{ formatShortDate(p.start) }}</td>
            <td class="px-3 py-1.5 text-right text-slate-300">{{ formatBRL(p.current_cents) }}</td>
            <td class="px-3 py-1.5 text-right text-slate-100">{{ formatBRL(p.cur) }}</td>
            <td v-if="hasPrevious" class="px-3 py-1.5 text-right text-slate-400">{{ formatBRL(p.prev) }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
