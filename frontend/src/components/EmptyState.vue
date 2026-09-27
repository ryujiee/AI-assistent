<script setup lang="ts">
import AppIcon, { type IconName } from './AppIcon.vue'

defineProps<{ icon: IconName; title: string; description?: string; tone?: 'neutral' | 'warning' | 'error' }>()
</script>

<template>
  <div class="flex flex-col items-center justify-center text-center gap-3 py-10 px-6">
    <div
      class="h-12 w-12 rounded-2xl flex items-center justify-center border"
      :class="{
        'bg-indigo-500/10 border-indigo-500/20 text-indigo-300': !tone || tone === 'neutral',
        'bg-amber-500/10 border-amber-500/20 text-amber-300': tone === 'warning',
        'bg-red-500/10 border-red-500/20 text-red-300': tone === 'error',
      }"
    >
      <AppIcon :name="icon" class="h-6 w-6" />
    </div>
    <div class="flex flex-col gap-1 max-w-md">
      <h3 class="text-sm font-semibold text-slate-200">{{ title }}</h3>
      <p v-if="description" class="text-xs text-slate-400 leading-relaxed">{{ description }}</p>
    </div>
    <slot />
  </div>
</template>
