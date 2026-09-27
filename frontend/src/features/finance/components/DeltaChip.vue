<script setup lang="ts">
import { computed } from 'vue'
import AppIcon from '@/components/AppIcon.vue'
import { formatPercent } from '../lib/format'

// Direction x whether "up" is good decides the tone; the arrow and the text
// always carry the meaning too, so color is never the only signal.
const props = defineProps<{ pct: number | null; upIsGood?: boolean; label?: string }>()

const flat = computed(() => props.pct !== null && Math.abs(props.pct) < 0.05)
const tone = computed(() => {
  if (props.pct === null || flat.value) return 'neutral'
  const up = props.pct > 0
  return up === !!props.upIsGood ? 'good' : 'bad'
})
</script>

<template>
  <span v-if="pct !== null" class="inline-flex items-center gap-1 text-xs">
    <span
      class="inline-flex items-center gap-0.5 font-semibold px-1.5 py-0.5 rounded-md"
      :class="{
        'text-emerald-300 bg-emerald-500/10': tone === 'good',
        'text-amber-300 bg-amber-500/10': tone === 'bad',
        'text-slate-300 bg-white/5': tone === 'neutral',
      }"
    >
      <template v-if="flat">= estável</template>
      <template v-else>
        <AppIcon :name="pct >= 0 ? 'trendingUp' : 'trendingDown'" class="h-3 w-3" />
        {{ formatPercent(Math.abs(pct), Math.abs(pct) < 10 ? 1 : 0) }}
      </template>
    </span>
    <span v-if="label" class="text-slate-500">{{ label }}</span>
  </span>
</template>
