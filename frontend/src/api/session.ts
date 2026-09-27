import { api } from './client'

export interface SessionInfo {
  authenticated: boolean
  auth_configured: boolean
}

export const getSession = () => api<SessionInfo>('/api/session')
export const login = (password: string) => api<{ authenticated: boolean }>('/api/login', { method: 'POST', body: { password } })
export const logout = () => api<{ authenticated: boolean }>('/api/logout', { method: 'POST' })
