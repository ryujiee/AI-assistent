<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import AppIcon from '@/components/AppIcon.vue'
import AppSpinner from '@/components/AppSpinner.vue'
import CategoryBadge from '../components/CategoryBadge.vue'
import TransactionForm from './TransactionForm.vue'
import { ledgerApi, type Category, type Member, type TransactionDetail } from '@/api/finance'
import { ApiError } from '@/api/client'
import { formatBRL, formatDateTime, formatDayHeading } from '../lib/format'
import { PENDING_LABELS, TYPE_LABELS } from '../lib/period'

const props = defineProps<{ id: number | null; creating: boolean; categories: Category[]; members: Member[] }>()
const emit = defineEmits<{ close: []; changed: []; deleted: [id: number] }>()

const detail = ref<TransactionDetail | null>(null)
const loading = ref(false)
const error = ref('')
const editing = ref(false)
const busy = ref(false)
const receiptIsPdf = ref(false)

async function load() {
  if (!props.id) {
    detail.value = null
    return
  }
  loading.value = true
  error.value = ''
  receiptIsPdf.value = false
  try {
    detail.value = await ledgerApi.transaction(props.id)
  } catch (err) {
    error.value = err instanceof ApiError ? err.message : 'Não foi possível abrir o lançamento.'
  } finally {
    loading.value = false
  }
}
watch(() => props.id, () => {
  editing.value = false
  load()
}, { immediate: true })

function onKey(e: KeyboardEvent) {
  if (e.key === 'Escape') emit('close')
}
onMounted(() => document.addEventListener('keydown', onKey))
onBeforeUnmount(() => document.removeEventListener('keydown', onKey))

const d = computed(() => detail.value)
const categoryPath = computed(() => {
  if (!d.value) return ''
  if (d.value.type === 'TRANSFER') return 'Transferência entre contas'
  if (!d.value.category_name) return 'Sem categoria'
  return d.value.parent_category_name ? `${d.value.parent_category_name} › ${d.value.category_name}` : d.value.category_name
})
const methodLabels: Record<string, string> = { PIX: 'PIX', CREDIT_CARD: 'Cartão de crédito', DEBIT_CARD: 'Cartão de débito', CASH: 'Dinheiro', BOLETO: 'Boleto', TRANSFER: 'Transferência', OTHER: 'Outro' }
const actionLabels: Record<string, string> = { CREATE: 'Registrado', UPDATE: 'Alterado', DELETE: 'Excluído', UNDO: 'Desfeito' }

async function confirmPending() {
  if (!d.value) return
  busy.value = true
  try {
    detail.value = await ledgerApi.updateTransaction(d.value.id, { confirm: true })
    emit('changed')
  } catch (err) {
    error.value = err instanceof ApiError ? err.message : 'Não foi possível confirmar.'
  } finally {
    busy.value = false
  }
}

async function remove() {
  if (!d.value || !confirm('Excluir este lançamento? Ele deixa de contar nos relatórios (é possível desfazer).')) return
  busy.value = true
  try {
    await ledgerApi.deleteTransaction(d.value.id)
    emit('deleted', d.value.id)
  } catch (err) {
    error.value = err instanceof ApiError ? err.message : 'Não foi possível excluir.'
  } finally {
    busy.value = false
  }
}

function onSaved(id: number) {
  editing.value = false
  emit('changed')
  if (props.creating) emit('close')
  else if (id) load()
}
</script>

<template>
  <Teleport to="body">
    <div class="fixed inset-0 z-40 bg-black/50 backdrop-blur-[2px]" @mousedown="emit('close')" />
    <aside
      class="fixed inset-y-0 right-0 z-50 w-full max-w-md glass-card !bg-slate-900/95 !rounded-none border-l border-white/10 flex flex-col"
      role="dialog"
      aria-modal="true"
      :aria-label="creating ? 'Novo lançamento' : 'Detalhe do lançamento'"
    >
      <header class="flex items-center justify-between gap-3 px-6 py-4 border-b border-white/5">
        <h2 class="text-sm font-semibold">{{ creating ? 'Novo lançamento' : editing ? 'Editar lançamento' : 'Lançamento' }}</h2>
        <button class="btn-ghost !p-2" aria-label="Fechar" @click="emit('close')"><AppIcon name="x" class="h-4 w-4" /></button>
      </header>

      <div class="flex-1 overflow-y-auto px-6 py-5">
        <TransactionForm v-if="creating" :categories="categories" :members="members" @saved="onSaved" @cancel="emit('close')" />

        <div v-else-if="loading" class="flex flex-col gap-4">
          <div class="skeleton h-16" />
          <div class="skeleton h-40" />
          <div class="skeleton h-24" />
        </div>

        <p v-else-if="error && !d" class="text-sm text-red-400">{{ error }}</p>

        <TransactionForm v-else-if="d && editing" :transaction="d" :categories="categories" :members="members" @saved="onSaved" @cancel="editing = false" />

        <div v-else-if="d" class="flex flex-col gap-6">
          <div class="flex items-start gap-4">
            <CategoryBadge :icon="d.type === 'TRANSFER' ? '⇄' : d.category_icon" />
            <div class="flex-1 min-w-0">
              <p class="text-3xl font-semibold tracking-tight" :class="d.type === 'INCOME' || d.type === 'REFUND' ? 'text-emerald-300' : 'text-slate-50'">
                {{ d.type === 'INCOME' || d.type === 'REFUND' ? '+ ' : '' }}{{ formatBRL(d.amount_cents) }}
              </p>
              <p class="text-sm text-slate-300 mt-1 truncate">{{ d.description || categoryPath }}</p>
              <div class="flex flex-wrap gap-2 mt-2">
                <span class="text-[11px] px-2 py-0.5 rounded-md bg-white/5 text-slate-300">{{ TYPE_LABELS[d.type] }}</span>
                <span v-if="d.status === 'PENDING'" class="text-[11px] px-2 py-0.5 rounded-md bg-amber-500/10 text-amber-300 flex items-center gap-1">
                  <AppIcon name="clock" class="h-3 w-3" />Pendente
                </span>
              </div>
            </div>
          </div>

          <div v-if="d.status === 'PENDING'" class="rounded-xl border border-amber-500/20 bg-amber-500/5 p-4 flex flex-col gap-3">
            <p class="text-xs text-amber-200">
              Aguardando confirmação: {{ d.pending_reasons.map((r) => PENDING_LABELS[r] ?? r).join(', ') }}. Não entra nos totais até ser confirmado.
            </p>
            <div class="flex gap-2">
              <button class="btn-primary !py-2" :disabled="busy || (d.type !== 'TRANSFER' && !d.category_id)" @click="confirmPending">Confirmar</button>
              <button class="btn-secondary !py-2" @click="editing = true">Corrigir</button>
            </div>
          </div>

          <dl class="grid grid-cols-[auto_1fr] gap-x-6 gap-y-3 text-sm">
            <dt class="text-slate-500">Categoria</dt>
            <dd class="text-slate-100">{{ categoryPath }}</dd>
            <dt class="text-slate-500">Data</dt>
            <dd class="text-slate-100">{{ formatDayHeading(d.transaction_date) }}</dd>
            <dt class="text-slate-500">Pago por</dt>
            <dd class="text-slate-100">{{ d.payer_name || 'Não informado' }}</dd>
            <template v-if="d.merchant">
              <dt class="text-slate-500">Estabelecimento</dt>
              <dd class="text-slate-100">{{ d.merchant }}</dd>
            </template>
            <template v-if="d.payment_method">
              <dt class="text-slate-500">Pagamento</dt>
              <dd class="text-slate-100">{{ methodLabels[d.payment_method] ?? d.payment_method }}</dd>
            </template>
            <template v-if="d.notes">
              <dt class="text-slate-500">Observações</dt>
              <dd class="text-slate-100 whitespace-pre-line">{{ d.notes }}</dd>
            </template>
          </dl>

          <section v-if="d.attachment_id" class="flex flex-col gap-2">
            <h3 class="text-xs font-semibold text-slate-400 flex items-center gap-1.5"><AppIcon name="paperclip" class="h-3.5 w-3.5" />Comprovante</h3>
            <a :href="ledgerApi.attachmentUrl(d.attachment_id)" target="_blank" rel="noopener" class="block rounded-xl overflow-hidden border border-white/10 bg-black/30">
              <img v-if="!receiptIsPdf" :src="ledgerApi.attachmentUrl(d.attachment_id)" alt="Comprovante enviado no WhatsApp" class="w-full max-h-96 object-contain" @error="receiptIsPdf = true" />
              <iframe v-else :src="ledgerApi.attachmentUrl(d.attachment_id)" title="Comprovante em PDF" class="w-full h-96 bg-white" />
            </a>
            <p class="text-[11px] text-slate-500">Guardado de forma privada; só abre com login.</p>
          </section>

          <section class="rounded-xl bg-slate-950/40 border border-white/5 p-4 flex flex-col gap-2">
            <p class="text-xs font-semibold text-slate-300 flex items-center gap-1.5">
              <AppIcon :name="d.origin.channel === 'WHATSAPP' ? 'chat' : 'globe'" class="h-3.5 w-3.5 text-indigo-300" />
              {{ d.origin.channel === 'WHATSAPP' ? 'Registrado a partir de uma mensagem no WhatsApp' : 'Lançado pelo painel' }}
            </p>
            <blockquote v-if="d.origin.text" class="text-sm text-slate-300 border-l-2 border-indigo-500/40 pl-3 italic">
              {{ d.origin.kind === 'AUDIO' ? '🎙️ ' : '' }}“{{ d.origin.text }}”
            </blockquote>
            <p v-else-if="d.origin.kind === 'IMAGE' || d.origin.kind === 'DOCUMENT'" class="text-xs text-slate-400">Comprovante enviado sem legenda.</p>
            <p v-if="d.origin.sender || d.origin.message_at" class="text-[11px] text-slate-500">
              {{ d.origin.sender }}<template v-if="d.origin.sender && d.origin.message_at"> · </template>{{ d.origin.message_at ? formatDateTime(d.origin.message_at) : '' }}
            </p>
          </section>

          <section class="flex flex-col gap-3">
            <h3 class="text-xs font-semibold text-slate-400 flex items-center gap-1.5"><AppIcon name="clock" class="h-3.5 w-3.5" />Histórico</h3>
            <ol class="flex flex-col gap-3 border-l border-white/10 ml-1.5">
              <li v-for="e in d.events" :key="e.id" class="pl-4 relative">
                <span class="absolute -left-[5px] top-1.5 h-2.5 w-2.5 rounded-full bg-indigo-500/60 ring-2 ring-slate-900" />
                <p class="text-xs text-slate-300">
                  <span class="font-semibold" :class="e.undone ? 'line-through text-slate-500' : ''">{{ actionLabels[e.action] }}</span>
                  <span class="text-slate-500"> · {{ e.actor_name || (e.channel === 'WEB' ? 'painel' : 'sistema') }} · {{ formatDateTime(e.created_at) }}</span>
                </p>
                <ul v-if="e.action !== 'CREATE' && e.changes.length" class="mt-1 flex flex-col gap-0.5">
                  <li v-for="c in e.changes" :key="c.field" class="text-[11px] text-slate-400">
                    {{ c.field }}: <span class="text-slate-500 line-through">{{ c.before }}</span> → <span class="text-slate-200">{{ c.after }}</span>
                  </li>
                </ul>
              </li>
            </ol>
          </section>
          <p v-if="error" class="text-xs text-red-400">{{ error }}</p>
        </div>
      </div>

      <footer v-if="d && !editing && !creating" class="px-6 py-4 border-t border-white/5 flex items-center justify-between gap-2">
        <button class="btn-ghost !text-red-300 hover:!bg-red-500/10" :disabled="busy" @click="remove">
          <AppIcon name="trash" class="h-4 w-4" />Excluir
        </button>
        <button class="btn-secondary" @click="editing = true"><AppIcon name="pencil" class="h-4 w-4" />Editar</button>
      </footer>
      <div v-if="busy" class="absolute top-4 right-16"><AppSpinner class="h-4 w-4 text-slate-400" /></div>
    </aside>
  </Teleport>
</template>
