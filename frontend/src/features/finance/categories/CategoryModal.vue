<script setup lang="ts">
import { reactive, ref } from 'vue'
import ModalDialog from '@/components/ModalDialog.vue'
import AppSpinner from '@/components/AppSpinner.vue'
import { ledgerApi, type Category, type Essentiality } from '@/api/finance'
import { ApiError } from '@/api/client'
import { ESSENTIALITY } from '../lib/period'

const props = defineProps<{ category?: Category | null; parent?: Category | null; kind: 'EXPENSE' | 'INCOME'; parents: Category[] }>()
const emit = defineEmits<{ close: []; saved: [] }>()

const form = reactive({
  name: props.category?.name ?? '',
  icon: props.category?.icon ?? '',
  essentiality: (props.category?.essentiality ?? props.parent?.essentiality ?? 'IMPORTANT') as Essentiality,
  parent_id: props.category?.parent_id ?? props.parent?.id ?? (null as number | null),
})
const saving = ref(false)
const error = ref('')
const suggestions = ['🛒', '🍝', '🛵', '⛽', '🚕', '🏠', '💡', '💊', '🎮', '📺', '📚', '🐾', '👕', '✈️', '🎁', '🧾', '💳', '💼', '💰', '☕', '🏋️', '👶']

async function submit() {
  if (!form.name.trim()) {
    error.value = 'Informe o nome.'
    return
  }
  saving.value = true
  error.value = ''
  try {
    if (props.category) {
      await ledgerApi.updateCategory(props.category.id, { name: form.name, icon: form.icon, essentiality: form.essentiality })
    } else {
      await ledgerApi.createCategory({ name: form.name, icon: form.icon, essentiality: form.essentiality, parent_id: form.parent_id, kind: props.kind })
    }
    emit('saved')
  } catch (err) {
    error.value = err instanceof ApiError ? err.message : 'Não foi possível salvar.'
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <ModalDialog :title="category ? 'Editar categoria' : parent ? `Nova subcategoria de ${parent.name}` : 'Nova categoria'" @close="emit('close')">
    <form id="category-form" class="flex flex-col gap-4" @submit.prevent="submit">
      <div class="grid grid-cols-[80px_1fr] gap-3">
        <label class="flex flex-col gap-1.5">
          <span class="text-xs font-medium text-slate-400">Ícone</span>
          <input v-model="form.icon" maxlength="4" class="field text-center text-lg" aria-label="Ícone" />
        </label>
        <label class="flex flex-col gap-1.5">
          <span class="text-xs font-medium text-slate-400">Nome</span>
          <input v-model="form.name" maxlength="40" class="field" required autofocus />
        </label>
      </div>
      <div class="flex flex-wrap gap-1.5" aria-label="Sugestões de ícone">
        <button v-for="s in suggestions" :key="s" type="button" class="h-8 w-8 rounded-lg hover:bg-white/10 text-base" :class="form.icon === s ? 'bg-white/10' : ''" @click="form.icon = s">{{ s }}</button>
      </div>

      <label v-if="!category && !parent && kind === 'EXPENSE'" class="flex flex-col gap-1.5">
        <span class="text-xs font-medium text-slate-400">Dentro de</span>
        <select v-model="form.parent_id" class="field">
          <option :value="null">Categoria principal</option>
          <option v-for="p in parents" :key="p.id" :value="p.id">{{ p.icon }} {{ p.name }}</option>
        </select>
      </label>

      <fieldset v-if="kind === 'EXPENSE'" class="flex flex-col gap-2">
        <legend class="text-xs font-medium text-slate-400 mb-1">Essencialidade</legend>
        <label
          v-for="(e, key) in ESSENTIALITY"
          :key="key"
          class="flex items-start gap-3 p-3 rounded-xl border cursor-pointer transition"
          :class="form.essentiality === key ? 'border-indigo-500/50 bg-indigo-500/10' : 'border-white/5 hover:border-white/10'"
        >
          <input v-model="form.essentiality" type="radio" :value="key" class="mt-0.5 accent-indigo-500" />
          <span>
            <span class="text-sm text-slate-100 block">{{ e.label }}</span>
            <span class="text-[11px] text-slate-500">{{ e.hint }}</span>
          </span>
        </label>
        <p class="text-[11px] text-slate-500">A IA usa isso para não sugerir cortes em gastos essenciais.</p>
      </fieldset>
      <p v-if="error" role="alert" class="text-xs text-red-400">{{ error }}</p>
    </form>
    <template #footer>
      <button type="button" class="btn-ghost" @click="emit('close')">Cancelar</button>
      <button type="submit" form="category-form" class="btn-primary" :disabled="saving">
        <AppSpinner v-if="saving" class="h-4 w-4 text-white" />Salvar
      </button>
    </template>
  </ModalDialog>
</template>
