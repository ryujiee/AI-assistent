import { api } from './client'

export interface WhatsAppStatus {
  connected: boolean
  qrcode: string
  active_jid: string
}

export interface ActionResult {
  success: boolean
  message: string
}

export const getStatus = () => api<WhatsAppStatus>('/api/status')
export const saveTargetJID = (jid: string) => api<ActionResult>('/api/config', { method: 'POST', body: { jid } })
export const refreshQRCode = () => api<ActionResult>('/api/qrcode/refresh', { method: 'POST' })
