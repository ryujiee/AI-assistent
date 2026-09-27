<script setup lang="ts">
import { PRESETS, type PeriodState } from '../lib/period'
import { todayCivil } from '../lib/format'

const model = defineModel<PeriodState>({ required: true })
const props = defineProps<{ withAll?: boolean }>()
const presets = props.withAll ? [...PRESETS.slice(0, 3), { key: 'all' as const, label: 'Tudo' }, PRESETS[3]] : PRESETS

function choose(key: PeriodState['preset']) {
  if (key === 'custom' && !model.value.start) {
    const today = todayCivil()
    model.value = { preset: key, start: today.slice(0, 8) + '01', end: today }
    return
  }
  model.value = { ...model.value, preset: key }
}
</script>

<template>
  <div class="flex flex-wrap items-center gap-2">
    <div class="flex items-center gap-1 p-1 rounded-xl bg-slate-900/60 border border-white/5" role="group" aria-label="Período">
      <button
        v-for="p in presets"
        :key="p.key"
        type="button"
        class="px-3 py-1.5 rounded-lg text-xs font-medium transition"
        :class="model.preset === p.key ? 'bg-indigo-600 text-white' : 'text-slate-400 hover:text-slate-100 hover:bg-white/5'"
        :aria-pressed="model.preset === p.key"
        @click="choose(p.key)"
      >
        {{ p.label }}
      </button>
    </div>
    <div v-if="model.preset === 'custom'" class="flex items-center gap-2 text-xs text-slate-400">
      <input v-model="model.start" type="date" aria-label="Data inicial" class="field !py-1.5 !w-auto" :max="model.end || undefined" />
      <span>a</span>
      <input v-model="model.end" type="date" aria-label="Data final" class="field !py-1.5 !w-auto" :min="model.start || undefined" />
    </div>
  </div>
</template>
