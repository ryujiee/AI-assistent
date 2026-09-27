<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import AppSpinner from '@/components/AppSpinner.vue'
import CategorySelect from '../components/CategorySelect.vue'
import { ledgerApi, type Category, type Member, type Transaction, type TxType } from '@/api/finance'
import { ApiError } from '@/api/client'
import { centsToInput, parseBRL, todayCivil } from '../lib/format'
import { TYPE_LABELS } from '../lib/period'

const props = defineProps<{ transaction?: Transaction | null; categories: Category[]; members: Member[] }>()
const emit = defineEmits<{ saved: [id: number]; cancel: [] }>()

const t = props.transaction
const form = reactive({
  type: (t?.type ?? 'EXPENSE') as TxType,
  amount: centsToInput(t?.amount_cents),
  description: t?.description ?? '',
  merchant: t?.merchant ?? '',
  category_id: t?.category_id ?? (null as number | null),
  date: t?.transaction_date ?? todayCivil(),
  payer: t?.payer_member_id ?? (null as number | null),
  notes: t?.notes ?? '',
})
const saving = ref(false)
const error = ref('')

const kind = computed(() => (form.type === 'INCOME' ? 'INCOME' : 'EXPENSE'))
const needsCategory = computed(() => form.type !== 'TRANSFER')

function setType(type: TxType) {
  const wasIncome = form.type === 'INCOME'
  form.type = type
  if (wasIncome !== (type === 'INCOME')) form.category_id = null
}

async function submit() {
  error.value = ''
  const cents = parseBRL(form.amount)
  if (!cents) {
    error.value = 'Informe um valor válido, por exemplo 37,90.'
    return
  }
  if (needsCategory.value && !form.category_id) {
    error.value = 'Escolha uma categoria.'
    return
  }
  saving.value = true
  try {
    if (t) {
      const d = await ledgerApi.updateTransaction(t.id, {
        type: form.type,
        amount_cents: cents,
        description: form.description,
        merchant: form.merchant,
        category_id: needsCategory.value && form.category_id ? form.category_id : undefined,
        transaction_date: form.date,
        payer_member_id: form.payer ?? undefined,
        notes: form.notes,
        confirm: true,
      })
      emit('saved', d.id)
    } else {
      const created = await ledgerApi.createTransaction({
        type: form.type,
        amount_cents: cents,
        description: form.description,
        merchant: form.merchant,
        category_id: needsCategory.value ? form.category_id : null,
        transaction_date: form.date,
        payer_member_id: form.payer,
        notes: form.notes,
      })
      emit('saved', created.id)
    }
  } catch (err) {
    error.value = err instanceof ApiError ? err.message : 'Não foi possível salvar.'
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <form class="flex flex-col gap-4" @submit.prevent="submit">
    <div class="grid grid-cols-4 gap-1 p-1 rounded-xl bg-slate-900/60 border border-white/5" role="group" aria-label="Tipo">
      <button
        v-for="type in ['EXPENSE', 'INCOME', 'TRANSFER', 'REFUND'] as TxType[]"
        :key="type"
        type="button"
        class="px-2 py-1.5 rounded-lg text-xs font-medium transition"
        :class="form.type === type ? 'bg-indigo-600 text-white' : 'text-slate-400 hover:text-slate-100'"
        :aria-pressed="form.type === type"
        @click="setType(type)"
      >
        {{ TYPE_LABELS[type] }}
      </button>
    </div>
    <p v-if="form.type === 'TRANSFER'" class="text-[11px] text-slate-500 -mt-2">
      Dinheiro entre contas próprias ou pagamento da fatura do cartão. Não conta como gasto.
    </p>

    <label class="flex flex-col gap-1.5">
      <span class="text-xs font-medium text-slate-400">Valor</span>
      <div class="relative">
        <span class="absolute left-3 top-1/2 -translate-y-1/2 text-sm text-slate-500">R$</span>
        <input v-model="form.amount" inputmode="decimal" placeholder="0,00" class="field pl-9 text-lg font-semibold tabular" required />
      </div>
    </label>

    <label class="flex flex-col gap-1.5">
      <span class="text-xs font-medium text-slate-400">Descrição</span>
      <input v-model="form.description" maxlength="200" placeholder="Ex.: Compra no mercado" class="field" />
    </label>

    <label v-if="needsCategory" class="flex flex-col gap-1.5">
      <span class="text-xs font-medium text-slate-400">Categoria</span>
      <CategorySelect v-model="form.category_id" :categories="categories" :kind="kind" placeholder="Escolha a categoria" />
    </label>

    <div class="grid grid-cols-2 gap-3">
      <label class="flex flex-col gap-1.5">
        <span class="text-xs font-medium text-slate-400">Data</span>
        <input v-model="form.date" type="date" :max="todayCivil()" class="field" required />
      </label>
      <label class="flex flex-col gap-1.5">
        <span class="text-xs font-medium text-slate-400">Pago por</span>
        <select v-model="form.payer" class="field">
          <option :value="null">Não informado</option>
          <option v-for="m in members" :key="m.id" :value="m.id">{{ m.display_name }}</option>
        </select>
      </label>
    </div>

    <label class="flex flex-col gap-1.5">
      <span class="text-xs font-medium text-slate-400">Estabelecimento <span class="text-slate-600">(opcional)</span></span>
      <input v-model="form.merchant" maxlength="120" class="field" />
    </label>
    <label class="flex flex-col gap-1.5">
      <span class="text-xs font-medium text-slate-400">Observações <span class="text-slate-600">(opcional)</span></span>
      <textarea v-model="form.notes" rows="2" maxlength="500" class="field resize-none" />
    </label>

    <p v-if="error" role="alert" class="text-xs text-red-400">{{ error }}</p>
    <div class="flex justify-end gap-2 pt-1">
      <button type="button" class="btn-ghost" @click="emit('cancel')">Cancelar</button>
      <button type="submit" class="btn-primary" :disabled="saving">
        <AppSpinner v-if="saving" class="h-4 w-4 text-white" />
        {{ transaction ? 'Salvar alterações' : 'Lançar' }}
      </button>
    </div>
  </form>
</template>
