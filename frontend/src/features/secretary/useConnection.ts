import { reactive, onMounted, onBeforeUnmount } from 'vue'
import { getStatus } from '@/api/secretary'

// WhatsApp connection state shared by the header badge, the Secretária page
// and the finance WhatsApp settings. One poller runs while any view uses it,
// every 3 seconds like the original panel.

const POLL_MS = 3000
const MAX_LOGS = 50

export const connection = reactive({
  loaded: false,
  connected: false,
  qrcode: '',
  activeJID: '',
  reachable: true,
  logs: [] as string[],
})

export function addLog(message: string) {
  const time = new Date().toLocaleTimeString('pt-BR', { hour12: false })
  connection.logs.unshift(`[${time}] ${message}`)
  if (connection.logs.length > MAX_LOGS) connection.logs.pop()
}

export async function fetchStatus() {
  try {
    const data = await getStatus()
    connection.reachable = true

    if (data.connected !== connection.connected || !connection.loaded) {
      if (connection.loaded || data.connected) {
        addLog(data.connected ? '✅ WhatsApp Conectado com sucesso!' : '❌ WhatsApp Desconectado.')
      }
      connection.connected = data.connected
    }

    if (data.qrcode && data.qrcode !== connection.qrcode) {
      connection.qrcode = data.qrcode
      addLog('📷 Novo código QR gerado pelo backend.')
    } else if (!data.qrcode) {
      connection.qrcode = ''
    }

    if (data.active_jid !== connection.activeJID) {
      connection.activeJID = data.active_jid
      addLog(data.active_jid ? `🎯 Chat monitorado ativo: ${data.active_jid}` : '⚠️ Nenhum chat configurado no banco. Configure acima.')
    }
  } catch {
    // Avoid spamming the console with the same connection error.
    if (connection.reachable || connection.logs.length === 0) {
      addLog('⚠️ Erro de conexão com o servidor. Tentando novamente...')
    }
    connection.reachable = false
  } finally {
    connection.loaded = true
  }
}

let users = 0
let timer: ReturnType<typeof setInterval> | null = null

/** Keeps the status poller running while the calling component is mounted. */
export function useConnection() {
  onMounted(() => {
    users++
    if (!timer) {
      fetchStatus()
      timer = setInterval(fetchStatus, POLL_MS)
    }
  })
  onBeforeUnmount(() => {
    users--
    if (users <= 0 && timer) {
      clearInterval(timer)
      timer = null
      users = 0
    }
  })
  return connection
}
