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
