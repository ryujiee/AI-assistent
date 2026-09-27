<script setup lang="ts">
import { computed } from 'vue'
import type { Category } from '@/api/finance'

// Categories grouped under their parent; kind narrows to expense or income.
const props = defineProps<{ categories: Category[]; kind?: 'EXPENSE' | 'INCOME'; placeholder?: string }>()
const model = defineModel<number | null>({ default: null })

const groups = computed(() => {
  const list = props.categories.filter((c) => !c.archived_at && (!props.kind || c.kind === props.kind))
  return list
    .filter((c) => c.parent_id === null)
    .map((parent) => ({ parent, children: list.filter((c) => c.parent_id === parent.id) }))
})
</script>

<template>
  <select v-model="model" class="field">
    <option :value="null">{{ placeholder ?? 'Todas as categorias' }}</option>
    <template v-for="g in groups" :key="g.parent.id">
      <option v-if="!g.children.length" :value="g.parent.id">{{ g.parent.icon }} {{ g.parent.name }}</option>
      <optgroup v-else :label="`${g.parent.icon} ${g.parent.name}`">
        <option :value="g.parent.id">{{ g.parent.name }} (geral)</option>
        <option v-for="c in g.children" :key="c.id" :value="c.id">{{ c.icon }} {{ c.name }}</option>
      </optgroup>
    </template>
  </select>
</template>
