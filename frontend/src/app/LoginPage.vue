<script setup lang="ts">
import { ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import AppIcon from '@/components/AppIcon.vue'
import AppSpinner from '@/components/AppSpinner.vue'
import { login } from '@/api/session'
import { ApiError } from '@/api/client'
import { session } from './session'

const route = useRoute()
const router = useRouter()
const password = ref('')
const submitting = ref(false)
const error = ref('')

async function submit() {
  if (!password.value || submitting.value) return
  submitting.value = true
  error.value = ''
  try {
    await login(password.value)
    session.authenticated = true
    session.checked = true
    password.value = ''
    const target = typeof route.query.redirect === 'string' && route.query.redirect.startsWith('/') && !route.query.redirect.startsWith('//') ? route.query.redirect : '/secretaria'
    router.replace(target)
  } catch (err) {
    error.value = err instanceof ApiError ? err.message : 'Não foi possível entrar agora.'
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <div class="min-h-screen flex items-center justify-center p-4">
    <section class="glass-card rounded-2xl p-8 w-full max-w-sm flex flex-col gap-6">
      <div class="flex flex-col items-center gap-3 text-center">
        <div class="h-12 w-12 rounded-xl bg-indigo-600 flex items-center justify-center shadow-lg shadow-indigo-500/20">
          <AppIcon name="chat" class="h-7 w-7 text-white" />
        </div>
        <div>
          <h1 class="text-xl font-bold tracking-tight">Secretária de IA</h1>
          <p class="text-xs text-slate-400 mt-1">Entre para acessar o painel</p>
        </div>
      </div>

      <div v-if="!session.configured" class="text-xs text-amber-300 bg-amber-500/10 border border-amber-500/20 rounded-xl p-3 leading-relaxed">
        O login ainda não foi configurado no servidor. Defina a variável <b>ADMIN_PASSWORD</b> (mínimo de 12 caracteres) e reinicie o backend.
      </div>

      <form class="flex flex-col gap-4" @submit.prevent="submit">
        <label class="flex flex-col gap-2">
          <span class="text-xs font-medium text-slate-400">Senha de administrador</span>
          <div class="relative">
            <AppIcon name="lock" class="h-4 w-4 text-slate-500 absolute left-3 top-1/2 -translate-y-1/2" />
            <input v-model="password" type="password" autocomplete="current-password" autofocus class="field pl-9" :disabled="!session.configured" />
          </div>
        </label>
        <p v-if="error" role="alert" class="text-xs text-red-400">{{ error }}</p>
        <button type="submit" class="btn-primary w-full" :disabled="submitting || !password || !session.configured">
          <AppSpinner v-if="submitting" class="h-4 w-4 text-white" />
          <span>Entrar</span>
        </button>
      </form>
    </section>
  </div>
</template>
