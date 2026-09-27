import { api } from './client'

export type TxType = 'EXPENSE' | 'INCOME' | 'TRANSFER' | 'REFUND'
export type TxStatus = 'CONFIRMED' | 'PENDING'
export type Essentiality = 'ESSENTIAL' | 'IMPORTANT' | 'DISCRETIONARY'

export interface Workspace {
  id: number
  name: string
  group_name: string | null
  group_linked_at: string | null
  confidence_threshold: number
  monthly_summary: 'off' | 'last_day' | 'first_day'
  weekly_summary: boolean
}

export interface FinanceStatus {
  enabled: boolean
  migrated: boolean
  workspace?: Workspace
}

export interface Member {
  id: number
  display_name: string
  phone: string
}

export interface WhatsAppGroup {
  jid: string
  name: string
  participant_count: number
  linked: boolean
}

export interface FinanceWhatsApp {
  connected: boolean
  fake: boolean
  group: { name: string; linked_at: string | null; participant_count: number | null } | null
  members: Member[]
}

export interface SettingsPatch {
  confidence_threshold?: number
  monthly_summary?: Workspace['monthly_summary']
  weekly_summary?: boolean
}

export const financeApi = {
  status: () => api<FinanceStatus>('/api/finance/status'),
  settings: () => api<Workspace>('/api/finance/settings'),
  updateSettings: (p: SettingsPatch) => api<Workspace>('/api/finance/settings', { method: 'PATCH', body: p }),

  whatsapp: () => api<FinanceWhatsApp>('/api/finance/whatsapp'),
  groups: () => api<{ groups: WhatsAppGroup[] }>('/api/finance/whatsapp/groups'),
  linkGroup: (jid: string) => api<FinanceWhatsApp>('/api/finance/whatsapp/group', { method: 'POST', body: { jid } }),
  unlinkGroup: () => api<FinanceWhatsApp>('/api/finance/whatsapp/group', { method: 'DELETE' }),
  members: () => api<{ members: Member[] }>('/api/finance/members'),
  renameMember: (id: number, display_name: string) => api<Member>(`/api/finance/members/${id}`, { method: 'PATCH', body: { display_name } }),
}

// ---- ledger & reports ----

export interface Period {
  key: string
  start: string
  end: string
  label: string
}

export interface Category {
  id: number
  parent_id: number | null
  name: string
  icon: string
  kind: 'EXPENSE' | 'INCOME'
  essentiality: Essentiality
  monthly_budget_cents: number | null
  sort_order: number
  archived_at: string | null
}

export interface CategoryTotal {
  id: number
  name: string
  icon: string
  essentiality: Essentiality
  amount_cents: number
  previous_cents: number
  share_pct: number
  change_pct: number | null
  budget_cents: number | null
  transactions: number
}

export interface MemberTotal {
  id: number
  name: string
  expenses_cents: number
  income_cents: number
}

export interface Summary {
  period: Period
  previous: Period
  expenses_cents: number
  income_cents: number
  balance_cents: number
  transfers_cents: number
  previous_expenses_cents: number
  previous_income_cents: number
  expense_change_pct: number | null
  income_change_pct: number | null
  daily_average_cents: number
  projection_cents: number | null
  transactions: number
  pending: number
  categories: CategoryTotal[]
  income_categories: CategoryTotal[]
  by_essentiality: Record<Essentiality, number>
  members: MemberTotal[]
}

export interface SeriesPoint {
  label: string
  start: string
  current_cents: number
  previous_cents: number
}

export interface Series {
  granularity: 'day' | 'week' | 'month'
  points: SeriesPoint[]
}

export interface BudgetStatus {
  category_id: number
  name: string
  icon: string
  essentiality: Essentiality
  budget_cents: number
  spent_cents: number
  remaining_cents: number
  pct: number
  projection_cents: number
  state: 'ok' | 'warning' | 'over'
}

export interface Insight {
  kind: string
  severity: 'info' | 'attention'
  text: string
  category_id?: number
}

export interface Transaction {
  id: number
  type: TxType
  status: TxStatus
  amount_cents: number
  currency: string
  description: string
  merchant: string | null
  transaction_date: string
  category_id: number | null
  category_name: string
  category_icon: string
  parent_category_id: number | null
  parent_category_name: string
  payer_member_id: number | null
  payer_name: string
  shared: boolean
  payment_method: string | null
  source: 'WHATSAPP_TEXT' | 'WHATSAPP_AUDIO' | 'WHATSAPP_RECEIPT' | 'WEB'
  attachment_id: number | null
  ai_confidence: number | null
  pending_reasons: string[]
  possible_duplicate_of: number | null
  notes: string | null
  created_at: string
  updated_at: string
}

export interface FieldChange {
  field: string
  before: string
  after: string
}

export interface TransactionEvent {
  id: number
  action: 'CREATE' | 'UPDATE' | 'DELETE' | 'UNDO'
  channel: 'WHATSAPP' | 'WEB' | 'SYSTEM'
  actor_name: string
  undone: boolean
  created_at: string
  changes: FieldChange[]
}

export interface TransactionDetail extends Transaction {
  events: TransactionEvent[]
  origin: { channel: 'WHATSAPP' | 'WEB'; kind?: string; text?: string; message_at?: string; sender?: string }
}

export interface Overview {
  summary: Summary
  series: Series
  largest: Transaction[]
  insights: Insight[]
  budgets: BudgetStatus[]
}

export interface PeriodQuery {
  period: string
  month?: string
  start?: string
  end?: string
}

export interface TransactionQuery extends PeriodQuery {
  category_id?: number | null
  member_id?: number | null
  type?: string
  status?: string
  q?: string
  min_cents?: number | null
  max_cents?: number | null
  order?: 'date' | 'amount'
  limit?: number
  offset?: number
}

export interface TransactionInput {
  type: TxType
  amount_cents: number
  description: string
  merchant: string
  category_id: number | null
  transaction_date: string
  payer_member_id: number | null
  notes: string
}

export interface TransactionPatch {
  type?: TxType
  amount_cents?: number
  description?: string
  merchant?: string
  category_id?: number
  transaction_date?: string
  payer_member_id?: number
  notes?: string
  confirm?: boolean
}

export interface CategoryInput {
  parent_id?: number | null
  name?: string
  icon?: string
  kind?: 'EXPENSE' | 'INCOME'
  essentiality?: Essentiality
  monthly_budget_cents?: number | null
  clear_budget?: boolean
  archived?: boolean
}

type Q = Record<string, string | number | boolean | null | undefined>

export const ledgerApi = {
  overview: (q: PeriodQuery & { member_id?: number | null; type?: string }) => api<Overview>('/api/finance/overview', { query: q as unknown as Q }),
  transactions: (q: TransactionQuery) => api<{ items: Transaction[]; total: number }>('/api/finance/transactions', { query: q as unknown as Q }),
  transaction: (id: number) => api<TransactionDetail>(`/api/finance/transactions/${id}`),
  createTransaction: (input: TransactionInput) => api<Transaction>('/api/finance/transactions', { method: 'POST', body: input }),
  updateTransaction: (id: number, patch: TransactionPatch) => api<TransactionDetail>(`/api/finance/transactions/${id}`, { method: 'PATCH', body: patch }),
  deleteTransaction: (id: number) => api<{ deleted: boolean }>(`/api/finance/transactions/${id}`, { method: 'DELETE' }),
  restoreTransaction: (id: number) => api<TransactionDetail>(`/api/finance/transactions/${id}/restore`, { method: 'POST' }),
  categories: () => api<{ categories: Category[]; month: Period; spent: Record<string, number> }>('/api/finance/categories'),
  createCategory: (input: CategoryInput) => api<Category>('/api/finance/categories', { method: 'POST', body: input }),
  updateCategory: (id: number, input: CategoryInput) => api<Category>(`/api/finance/categories/${id}`, { method: 'PATCH', body: input }),
  attachmentUrl: (id: number) => `/api/finance/attachments/${id}`,
}
