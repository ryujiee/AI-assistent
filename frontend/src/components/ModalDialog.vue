<script setup lang="ts">
import { onMounted, onBeforeUnmount, ref } from 'vue'
import AppIcon from './AppIcon.vue'

const props = defineProps<{ title: string; subtitle?: string; wide?: boolean }>()
const emit = defineEmits<{ close: [] }>()
const panel = ref<HTMLElement | null>(null)
let previous: Element | null = null

function onKey(e: KeyboardEvent) {
  if (e.key === 'Escape') emit('close')
}

onMounted(() => {
  previous = document.activeElement
  document.addEventListener('keydown', onKey)
  panel.value?.focus()
})
onBeforeUnmount(() => {
  document.removeEventListener('keydown', onKey)
  if (previous instanceof HTMLElement) previous.focus()
})
</script>

<template>
  <Teleport to="body">
    <div class="fixed inset-0 z-40 flex items-start sm:items-center justify-center p-4 bg-black/60 backdrop-blur-sm" @mousedown.self="emit('close')">
      <section
        ref="panel"
        role="dialog"
        aria-modal="true"
        :aria-label="props.title"
        tabindex="-1"
        class="glass-card !bg-slate-900/95 rounded-2xl w-full flex flex-col max-h-[90vh] outline-none"
        :class="wide ? 'max-w-2xl' : 'max-w-lg'"
      >
        <header class="flex items-start justify-between gap-4 p-6 pb-4 border-b border-white/5">
          <div>
            <h2 class="text-lg font-semibold">{{ title }}</h2>
            <p v-if="subtitle" class="text-xs text-slate-400 mt-1">{{ subtitle }}</p>
          </div>
          <button class="btn-ghost !p-2" aria-label="Fechar" @click="emit('close')"><AppIcon name="x" class="h-4 w-4" /></button>
        </header>
        <div class="p-6 overflow-y-auto">
          <slot />
        </div>
        <footer v-if="$slots.footer" class="p-6 pt-4 border-t border-white/5 flex justify-end gap-2">
          <slot name="footer" />
        </footer>
      </section>
    </div>
  </Teleport>
</template>
