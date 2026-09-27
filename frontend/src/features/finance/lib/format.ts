// Formatting helpers for money and dates. Amounts are integer cents from the
// API; they are only divided by 100 for display.

const brl = new Intl.NumberFormat('pt-BR', { style: 'currency', currency: 'BRL' })
const brlShort = new Intl.NumberFormat('pt-BR', { style: 'currency', currency: 'BRL', maximumFractionDigits: 0 })

export function formatBRL(cents: number): string {
  return brl.format(cents / 100)
}

/** "R$ 4.820" for whole amounts, "R$ 87,40" otherwise. */
export function formatBRLShort(cents: number): string {
  return cents % 100 === 0 ? brlShort.format(cents / 100) : brl.format(cents / 100)
}

/** Parses "37,90", "1.234,56", "3000" into cents; null when invalid. */
export function parseBRL(input: string): number | null {
  const s = input.replace(/r\$/i, '').replace(/\s/g, '')
  if (!/^\d[\d.,]*$/.test(s)) return null
  let normalized = s
  if (s.includes(',')) normalized = s.replace(/\./g, '').replace(',', '.')
  else if (/^\d{1,3}(\.\d{3})+$/.test(s)) normalized = s.replace(/\./g, '')
  const value = Number(normalized)
  if (!Number.isFinite(value) || value <= 0) return null
  return Math.round(value * 100)
}

/** Converts cents to the editable "37,90" form. */
export function centsToInput(cents: number | null | undefined): string {
  if (cents == null) return ''
  return (cents / 100).toFixed(2).replace('.', ',')
}

const MONTHS = ['jan', 'fev', 'mar', 'abr', 'mai', 'jun', 'jul', 'ago', 'set', 'out', 'nov', 'dez']
const WEEKDAYS = ['domingo', 'segunda', 'terça', 'quarta', 'quinta', 'sexta', 'sábado']

function parseCivil(date: string): Date {
  const [y, m, d] = date.split('-').map(Number)
  return new Date(y, m - 1, d)
}

export function todayCivil(now = new Date()): string {
  const p = (n: number) => String(n).padStart(2, '0')
  return `${now.getFullYear()}-${p(now.getMonth() + 1)}-${p(now.getDate())}`
}

/** "Hoje", "Ontem", "sexta, 25 set", "12 ago 2025". */
export function formatDayHeading(date: string, now = new Date()): string {
  const d = parseCivil(date)
  const today = parseCivil(todayCivil(now))
  const diff = Math.round((today.getTime() - d.getTime()) / 86400000)
  if (diff === 0) return 'Hoje'
  if (diff === 1) return 'Ontem'
  const base = `${d.getDate()} ${MONTHS[d.getMonth()]}`
  if (d.getFullYear() !== today.getFullYear()) return `${base} ${d.getFullYear()}`
  return `${WEEKDAYS[d.getDay()]}, ${base}`
}

export function formatShortDate(date: string): string {
  const d = parseCivil(date)
  return `${String(d.getDate()).padStart(2, '0')}/${String(d.getMonth() + 1).padStart(2, '0')}`
}

export function formatDateTime(iso: string): string {
  const d = new Date(iso)
  return d.toLocaleString('pt-BR', { day: '2-digit', month: '2-digit', year: 'numeric', hour: '2-digit', minute: '2-digit' })
}

/** Masks a phone JID: "+55 11 9••••-0001". */
export function maskPhone(phone: string): string {
  const digits = phone.split('@')[0].replace(/\D/g, '')
  if (digits.length < 8) return ''
  const last = digits.slice(-4)
  const ddd = digits.length >= 12 ? digits.slice(2, 4) : ''
  return `+55 ${ddd} ••••-${last}`.replace('  ', ' ')
}

export function percent(part: number, total: number): number {
  if (!total) return 0
  return (part / total) * 100
}

export function formatPercent(value: number, digits = 0): string {
  return `${value.toLocaleString('pt-BR', { maximumFractionDigits: digits, minimumFractionDigits: digits })}%`
}
