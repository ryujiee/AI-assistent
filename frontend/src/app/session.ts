import { reactive } from 'vue'
import { getSession } from '@/api/session'

// Session state shared by the router guard and the shell. `checked` avoids a
// round trip on every navigation once the session is known.
export const session = reactive({ checked: false, authenticated: false, configured: true })

export async function refreshSession(): Promise<boolean> {
  try {
    const info = await getSession()
    session.authenticated = info.authenticated
    session.configured = info.auth_configured
  } catch {
    session.authenticated = false
  }
  session.checked = true
  return session.authenticated
}

export function markLoggedOut() {
  session.authenticated = false
  session.checked = true
}
