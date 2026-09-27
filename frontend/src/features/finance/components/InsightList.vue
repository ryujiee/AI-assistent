<script setup lang="ts">
import type { Insight } from '@/api/finance'
import AppIcon from '@/components/AppIcon.vue'

defineProps<{ insights: Insight[] }>()
</script>

<template>
  <ul class="flex flex-col gap-3">
    <li v-for="(i, idx) in insights" :key="idx" class="flex items-start gap-3 text-sm">
      <span
        class="mt-0.5 h-6 w-6 shrink-0 rounded-lg flex items-center justify-center border"
        :class="i.severity === 'attention' ? 'bg-amber-500/10 border-amber-500/20 text-amber-300' : 'bg-indigo-500/10 border-indigo-500/20 text-indigo-300'"
      >
        <AppIcon :name="i.severity === 'attention' ? 'warning' : i.kind === 'projection' ? 'trendingUp' : 'sparkles'" class="h-3.5 w-3.5" />
        <span class="sr-only">{{ i.severity === 'attention' ? 'Atenção' : 'Informação' }}</span>
      </span>
      <p class="text-slate-300 leading-relaxed">{{ i.text }}</p>
    </li>
  </ul>
</template>
