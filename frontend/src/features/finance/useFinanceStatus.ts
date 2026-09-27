import { reactive } from 'vue'
import { financeApi, type FinanceStatus } from '@/api/finance'
import { ApiError } from '@/api/client'

// Whether the finance module is on and migrated. Loaded once per visit to
// the finance area; every finance page renders behind this gate.
export const financeStatus = reactive({
  loading: true,
  error: '',
  status: null as FinanceStatus | null,
})

export async function loadFinanceStatus() {
  financeStatus.loading = true
  financeStatus.error = ''
  try {
    financeStatus.status = await financeApi.status()
  } catch (err) {
    financeStatus.error = err instanceof ApiError ? err.message : 'Não foi possível carregar o módulo financeiro.'
  } finally {
    financeStatus.loading = false
  }
}
