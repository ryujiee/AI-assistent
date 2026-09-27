<script setup lang="ts">
defineProps<{ icon?: string; size?: 'sm' | 'md' }>()
</script>

<template>
  <span
    class="shrink-0 rounded-xl bg-white/5 border border-white/5 flex items-center justify-center"
    :class="size === 'sm' ? 'h-7 w-7 text-sm' : 'h-9 w-9 text-base'"
    aria-hidden="true"
  >
    {{ icon || '•' }}
  </span>
</template>
