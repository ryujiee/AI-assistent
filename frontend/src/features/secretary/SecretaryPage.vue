<script setup lang="ts">
import { ref, watch, onMounted } from 'vue'
import AppIcon from '@/components/AppIcon.vue'
import AppSpinner from '@/components/AppSpinner.vue'
import { saveTargetJID, refreshQRCode } from '@/api/secretary'
import { ApiError } from '@/api/client'
import { useConnection, addLog, fetchStatus } from './useConnection'

const connection = useConnection()
const inputJID = ref('')
const saving = ref(false)
const refreshingQR = ref(false)

let started = false
onMounted(() => {
  if (!started && connection.logs.length === 0) addLog('🚀 Painel de controle iniciado com sucesso.')
  started = true
  inputJID.value = connection.activeJID.split('@')[0]
})

watch(
  () => connection.activeJID,
  (jid) => {
    inputJID.value = jid.split('@')[0]
  },
)

async function saveConfig() {
  if (!inputJID.value) return
  saving.value = true
  addLog(`💾 Salvando JID alvo: ${inputJID.value}...`)
  try {
    const data = await saveTargetJID(inputJID.value)
    if (data.success) {
      addLog('✨ Configuração gravada no banco de dados!')
      fetchStatus()
    } else {
      addLog(`❌ Falha ao salvar: ${data.message}`)
    }
  } catch (err) {
    addLog(err instanceof ApiError && err.status !== 0 ? `❌ Falha ao salvar: ${err.message}` : '❌ Erro de rede ao tentar salvar configuração.')
  } finally {
    saving.value = false
  }
}

async function requestNewQRCode() {
  if (refreshingQR.value) return
  refreshingQR.value = true
  addLog('🔄 Solicitando um novo QR Code ao backend...')

  // Clear the old image right away: it is the code the user just told us
  // does not work.
  connection.qrcode = ''

  try {
    const data = await refreshQRCode()
    addLog(data.success ? '⏳ Novo QR Code sendo gerado, aguarde alguns segundos...' : `❌ ${data.message}`)
  } catch (err) {
    addLog(err instanceof ApiError && err.status !== 0 ? `❌ ${err.message}` : '❌ Erro de rede ao solicitar um novo QR Code.')
  } finally {
    // The backend takes a moment to receive the new code from WhatsApp, so
    // keep the button busy until the polling has had a chance to pick it up.
    setTimeout(() => {
      refreshingQR.value = false
    }, 4000)
    fetchStatus()
  }
}
</script>

<template>
  <main class="grid grid-cols-1 md:grid-cols-2 gap-8">
    <!-- Left Column: Settings and QR Code -->
    <div class="flex flex-col gap-8">
      <!-- Target JID Configuration -->
      <section class="glass-card rounded-2xl p-6 flex flex-col gap-4">
        <h2 class="text-lg font-semibold flex items-center gap-2">
          <AppIcon name="cog" class="h-5 w-5 text-indigo-400" />
          Número Alvo (WhatsApp JID)
        </h2>
        <p class="text-xs text-slate-400 leading-relaxed">
          A Inteligência Artificial só responderá e processará mensagens recebidas deste número específico. Mensagens de outros números serão ignoradas.
        </p>

        <form class="flex gap-2 mt-2" @submit.prevent="saveConfig">
          <div class="relative flex-grow">
            <span class="absolute inset-y-0 left-0 pl-3 flex items-center text-slate-500 text-sm font-semibold pointer-events-none">+</span>
            <input
              v-model="inputJID"
              type="text"
              placeholder="5511999999999"
              aria-label="Número alvo do WhatsApp"
              class="w-full pl-7 pr-3 py-2.5 bg-slate-900/60 border border-white/10 rounded-xl focus:border-indigo-500 focus:ring-1 focus:ring-indigo-500 outline-none text-slate-200 placeholder-slate-600 transition text-sm"
            />
          </div>
          <button type="submit" :disabled="saving || !inputJID" class="btn-primary">
            <AppSpinner v-if="saving" class="h-4 w-4 text-white" />
            <span>Salvar</span>
          </button>
        </form>

        <div v-if="connection.activeJID" class="mt-2 text-xs bg-slate-900/40 border border-white/5 rounded-lg p-3 flex justify-between items-center text-slate-400">
          <span>Número Ativo Atual:</span>
          <span class="font-mono text-indigo-400 font-semibold select-all">{{ connection.activeJID }}</span>
        </div>
      </section>

      <!-- QR Code Connection Card -->
      <section class="glass-card rounded-2xl p-6 flex flex-col items-center gap-4">
        <h2 class="text-lg font-semibold w-full text-left flex items-center gap-2">
          <AppIcon name="qrcode" class="h-5 w-5 text-indigo-400" />
          Conexão WhatsApp
        </h2>

        <!-- Connected State -->
        <div v-if="connection.connected" class="flex flex-col items-center justify-center py-8 gap-4 w-full">
          <div class="h-20 w-20 rounded-full bg-green-500/10 border border-green-500/20 flex items-center justify-center glow-green">
            <AppIcon name="check" :stroke-width="2.5" class="h-10 w-10 text-green-400" />
          </div>
          <div class="text-center">
            <h3 class="font-semibold text-slate-200">WhatsApp Vinculado com Sucesso</h3>
            <p class="text-xs text-slate-400 mt-1">A IA está monitorando e respondendo ao chat configurado.</p>
          </div>
        </div>

        <!-- Scan QR Code State -->
        <div v-else-if="connection.qrcode" class="flex flex-col items-center justify-center gap-4 w-full">
          <p class="text-xs text-slate-400 text-center leading-relaxed max-w-sm">
            Abra o WhatsApp no seu celular, vá em <b>Aparelhos Conectados</b> e escaneie o código abaixo:
          </p>
          <div class="p-4 bg-white rounded-2xl shadow-xl shadow-black/40">
            <img :src="connection.qrcode" alt="WhatsApp QR Code" class="w-56 h-56" />
          </div>
          <p class="text-[10px] text-indigo-400 flex items-center gap-1.5 animate-pulse">
            <span class="inline-block h-1.5 w-1.5 rounded-full bg-indigo-400"></span>
            Atualizando QR Code dinamicamente...
          </p>
        </div>

        <!-- Disconnected / Loading state -->
        <div v-else class="flex flex-col items-center justify-center py-12 gap-4 w-full">
          <AppSpinner class="h-8 w-8 text-indigo-500" />
          <p class="text-xs text-slate-400">Tentando estabelecer conexão com o WhatsApp...</p>
        </div>

        <!-- A QR code is only valid for a short time. When the one on screen is
             already dead, this asks for a fresh one. -->
        <div v-if="!connection.connected" class="w-full flex flex-col items-center gap-2 pt-2 border-t border-white/5">
          <button :disabled="refreshingQR" class="btn-secondary" @click="requestNewQRCode">
            <AppSpinner v-if="refreshingQR" class="h-4 w-4" />
            <AppIcon v-else name="refresh" class="h-4 w-4" />
            <span>{{ refreshingQR ? 'Gerando...' : 'Gerar novo QR Code' }}</span>
          </button>
          <p class="text-[10px] text-slate-500 text-center max-w-xs">
            Use se o código acima já expirou e o celular não reconhecer a leitura.
          </p>
        </div>
      </section>
    </div>

    <!-- Right Column: Live Logs -->
    <section class="glass-card rounded-2xl p-6 flex flex-col gap-4 h-[510px]">
      <h2 class="text-lg font-semibold flex items-center gap-2">
        <AppIcon name="document" class="h-5 w-5 text-indigo-400" />
        Console de Eventos (Live Logs)
      </h2>

      <div class="flex-grow bg-slate-950/80 border border-white/5 rounded-xl p-4 font-mono text-xs text-slate-400 overflow-y-auto flex flex-col gap-2 shadow-inner">
        <div v-for="(entry, idx) in connection.logs" :key="idx" class="border-l-2 pl-2 border-slate-700 hover:border-indigo-500 transition py-0.5 leading-relaxed">
          <span class="text-indigo-400">{{ entry.substring(0, 10) }}</span>
          <span class="text-slate-300">{{ entry.substring(10) }}</span>
        </div>
        <div v-if="connection.logs.length === 0" class="text-slate-600 text-center my-auto">Nenhum log registrado ainda.</div>
      </div>
    </section>
  </main>
</template>
