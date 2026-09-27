<script setup lang="ts">
import AppIcon, { type IconName } from '@/components/AppIcon.vue'

defineProps<{ label: string; value: string; icon: IconName; hero?: boolean; loading?: boolean; exact?: string }>()
</script>

<template>
  <section class="glass-card rounded-2xl p-5 flex flex-col gap-3 min-w-0">
    <div class="flex items-center justify-between gap-2">
      <h3 class="text-xs font-medium text-slate-400">{{ label }}</h3>
      <span class="h-8 w-8 rounded-lg bg-white/5 border border-white/5 flex items-center justify-center text-slate-400">
        <AppIcon :name="icon" class="h-4 w-4" />
      </span>
    </div>
    <div v-if="loading" class="skeleton h-9 w-32" />
    <p v-else class="font-semibold tracking-tight text-slate-50 truncate" :class="hero ? 'text-4xl lg:text-5xl' : 'text-2xl lg:text-3xl'" :title="exact">{{ value }}</p>
    <div class="min-h-[20px] flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-slate-500">
      <slot />
    </div>
  </section>
</template>
