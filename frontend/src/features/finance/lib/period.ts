import type { PeriodQuery } from '@/api/finance'

export type PresetKey = 'this_month' | 'last_month' | 'last_30_days' | 'custom' | 'all'

export const PRESETS: { key: PresetKey; label: string }[] = [
  { key: 'this_month', label: 'Este mês' },
  { key: 'last_month', label: 'Mês anterior' },
  { key: 'last_30_days', label: 'Últimos 30 dias' },
  { key: 'custom', label: 'Personalizado' },
]

export interface PeriodState {
  preset: PresetKey
  start: string
  end: string
}

/** Builds the API query; a custom range needs both dates in order. */
export function periodQuery(p: PeriodState): PeriodQuery | null {
  if (p.preset !== 'custom') return { period: p.preset }
  if (!p.start || !p.end || p.end < p.start) return null
  return { period: 'custom', start: p.start, end: p.end }
}

export const TYPE_LABELS: Record<string, string> = {
  EXPENSE: 'Despesa',
  INCOME: 'Receita',
  TRANSFER: 'Transferência',
  REFUND: 'Estorno',
}

export const SOURCE_LABELS: Record<string, string> = {
  WHATSAPP_TEXT: 'Mensagem no WhatsApp',
  WHATSAPP_AUDIO: 'Áudio no WhatsApp',
  WHATSAPP_RECEIPT: 'Comprovante no WhatsApp',
  WEB: 'Painel',
}

export const PENDING_LABELS: Record<string, string> = {
  category_missing: 'Falta categoria',
  low_confidence: 'Confirmar',
  amount_not_in_message: 'Confirmar valor',
  possible_duplicate: 'Possível duplicata',
  date_unclear: 'Confirmar data',
}

export const ESSENTIALITY: Record<string, { label: string; short: string; hint: string }> = {
  ESSENTIAL: { label: 'Essencial', short: 'Essencial', hint: 'Moradia, mercado, saúde: não é onde cortar.' },
  IMPORTANT: { label: 'Importante', short: 'Importante', hint: 'Necessário, com alguma margem.' },
  DISCRETIONARY: { label: 'Discricionária', short: 'Discricionária', hint: 'Escolha do casal: lazer, delivery, restaurantes.' },
}
