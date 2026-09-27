// Local end-to-end run: login -> finance -> pick the (fake) WhatsApp group ->
// "gastei 50 no mercado" -> transaction in the panel -> "na verdade foi 60"
// -> corrected -> "quanto gastamos esse mês?" -> real total in the reply.
// Needs the backend with WHATSAPP_FAKE and OPENAI_FAKE on a fresh local DB.
import { chromium } from 'playwright-core'
import assert from 'node:assert/strict'

const base = process.env.BASE_URL ?? 'http://localhost:8000'
const shots = process.env.OUT_DIR ?? new URL('../../docs/finance/screenshots/', import.meta.url).pathname
const GROUP = '120363000000000001@g.us'
const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH ?? '/usr/bin/google-chrome', headless: true })
const page = await (await browser.newContext({ viewport: { width: 1440, height: 900 } })).newPage()
const step = (s) => console.log('✓', s)

async function say(text) {
  const res = await page.request.post(base + '/api/dev/whatsapp/messages', {
    headers: { Origin: base },
    data: { chat_jid: GROUP, sender_jid: '5511900000001@s.whatsapp.net', push_name: 'Ana', text },
  })
  assert.equal(res.status(), 200, await res.text())
  assert.equal((await res.json()).route, 'finance')
  await page.waitForTimeout(600)
  const out = await (await page.request.get(base + '/api/dev/whatsapp/outbox')).json()
  return out.messages.at(-1)?.text ?? ''
}

try {
  await page.goto(base + '/financeiro')
  await page.waitForURL(/\/login/)
  await page.fill('input[type=password]', process.env.ADMIN_PASSWORD)
  await page.click('button[type=submit]')
  await page.waitForURL(/\/financeiro/)
  step('login redirects back to /financeiro')

  await page.goto(base + '/financeiro/whatsapp')
  await page.getByRole('button', { name: /Escolher grupo|Alterar grupo/ }).first().click()
  await page.getByPlaceholder('Buscar grupo...').fill('ana')
  await page.getByRole('listitem').filter({ hasText: 'Financeiro Ana & Bruno' }).getByRole('button', { name: 'Usar' }).click()
  await page.waitForSelector('text=vinculado em')
  assert.ok(await page.getByText('Financeiro Ana & Bruno').first().isVisible())
  step('group selected through the modal')

  let reply = await say('gastei 50 no mercado')
  assert.equal(reply, '✅ R$ 50,00 · Mercado · hoje')
  step(`bot replied: ${reply}`)

  await page.goto(base + '/financeiro/transacoes')
  await page.waitForSelector('text=R$ 50,00')
  assert.ok(await page.getByText('Alimentação › Mercado').first().isVisible())
  step('transaction listed in the panel')

  await page.goto(base + '/financeiro')
  await page.waitForSelector('text=Gastos no período')
  const kpi = await page.locator('section', { hasText: 'Gastos no período' }).first().innerText()
  assert.match(kpi.replace(/ /g, ' '), /R\$ 50/)
  await page.screenshot({ path: shots + 'e2e-1-dashboard-50.png' })
  step('dashboard shows R$ 50')

  reply = await say('na verdade foi 60')
  assert.match(reply, /Corrigido: R\$ 60,00 · Mercado/)
  step(`bot replied: ${reply}`)

  await page.goto(base + '/financeiro/transacoes')
  await page.waitForSelector('text=R$ 60,00')
  await page.getByText('R$ 60,00').first().click()
  await page.waitForSelector('text=Histórico')
  const drawer = (await page.locator('aside').innerText()).replace(/ /g, ' ')
  assert.match(drawer, /Alterado/)
  assert.match(drawer, /R\$ 50,00 → R\$ 60,00/)
  assert.match(drawer, /Registrado a partir de uma mensagem no WhatsApp/)
  await page.screenshot({ path: shots + 'e2e-2-drawer-corrected.png' })
  step('correction audited in the drawer')

  reply = await say('quanto gastamos esse mês?')
  assert.match(reply, /Total gasto: \*R\$ 60\*/)
  step('monthly question answered from the ledger:\n' + reply)

  await page.goto(base + '/financeiro')
  await page.waitForSelector('text=Gastos no período')
  const kpi2 = (await page.locator('section', { hasText: 'Gastos no período' }).first().innerText()).replace(/ /g, ' ')
  assert.match(kpi2, /R\$ 60/)
  step('dashboard updated to R$ 60')
  console.log('E2E OK')
} catch (err) {
  await page.screenshot({ path: shots + 'e2e-failure.png', fullPage: true })
  console.error('E2E FAILED:', err.message)
  process.exitCode = 1
} finally {
  await browser.close()
}
