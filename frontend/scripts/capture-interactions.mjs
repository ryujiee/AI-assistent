// Screenshots of interactive states: chart tooltip, transaction drawer with a
// receipt, group picker, category modal, mobile overview.
import { chromium } from 'playwright-core'

const base = process.env.BASE_URL ?? 'http://localhost:8000'
const out = process.env.OUT_DIR ?? new URL('../../docs/finance/screenshots/', import.meta.url).pathname
const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH ?? '/usr/bin/google-chrome', headless: true })

async function session(width, height) {
  const ctx = await browser.newContext({ viewport: { width, height } })
  const page = await ctx.newPage()
  await page.goto(base + '/login')
  await page.fill('input[type=password]', process.env.ADMIN_PASSWORD)
  await page.click('button[type=submit]')
  await page.waitForURL((u) => !u.pathname.startsWith('/login'))
  return page
}

const page = await session(1440, 900)
await page.goto(base + '/financeiro')
await page.waitForSelector('svg[role=img]')
const svg = page.locator('svg[role=img]').first()
const box = await svg.boundingBox()
await page.mouse.move(box.x + box.width * 0.55, box.y + box.height * 0.5)
await page.waitForTimeout(300)
await svg.screenshot({ path: out + 'chart-tooltip-1440.png' })
await page.locator('section', { hasText: 'Evolução dos gastos' }).first().screenshot({ path: out + 'chart-evolution-card-1440.png' })
console.log('saved chart shots')

await page.goto(base + '/financeiro/transacoes')
await page.waitForSelector('text=R$ 89,90')
await page.getByText('R$ 89,90').first().click()
await page.waitForSelector('text=Comprovante')
await page.waitForTimeout(400)
await page.screenshot({ path: out + 'financeiro-transacao-detalhe-1440.png' })
console.log('saved drawer')

await page.goto(base + '/financeiro/transacoes?status=PENDING&period=all')
await page.waitForTimeout(600)
await page.screenshot({ path: out + 'financeiro-transacoes-pendentes-1440.png' })

await page.goto(base + '/financeiro/whatsapp')
await page.getByRole('button', { name: 'Alterar grupo' }).click()
await page.waitForSelector('text=Futebol de quinta')
await page.waitForTimeout(300)
await page.screenshot({ path: out + 'financeiro-whatsapp-modal-1440.png' })
console.log('saved group modal')

await page.goto(base + '/financeiro/categorias')
await page.getByRole('button', { name: 'Nova categoria' }).click()
await page.waitForSelector('text=Essencialidade')
await page.screenshot({ path: out + 'financeiro-categoria-modal-1440.png' })
console.log('saved category modal')

const mobile = await session(390, 844)
await mobile.goto(base + '/financeiro')
await mobile.waitForSelector('text=Gastos no período')
await mobile.waitForTimeout(500)
await mobile.screenshot({ path: out + 'financeiro-390.png', fullPage: true })
console.log('saved mobile')
await browser.close()
