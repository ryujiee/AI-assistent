// Captures the panel at the viewport sizes we validate. Uses the system
// Chrome through playwright-core (no browser download).
//
//   BASE_URL=http://localhost:8000 ADMIN_PASSWORD=... node scripts/screenshots.mjs [route ...]
//
// Routes default to the main screens. Output goes to ../docs/finance/screenshots.
import { chromium } from 'playwright-core'
import { mkdirSync } from 'node:fs'

const base = process.env.BASE_URL ?? 'http://localhost:8000'
const password = process.env.ADMIN_PASSWORD
const out = process.env.OUT_DIR ?? new URL('../../docs/finance/screenshots/', import.meta.url).pathname
const executablePath = process.env.CHROME_PATH ?? '/usr/bin/google-chrome'
const sizes = (process.env.SIZES ?? '1440x900,1366x768,1024x768').split(',').map((s) => s.split('x').map(Number))
const routes = process.argv.slice(2).length ? process.argv.slice(2) : ['/login', '/secretaria']

mkdirSync(out, { recursive: true })
const browser = await chromium.launch({ executablePath, headless: true })
try {
  for (const [width, height] of sizes) {
    const context = await browser.newContext({ viewport: { width, height }, deviceScaleFactor: 1 })
    const page = await context.newPage()
    for (const route of routes) {
      if (route !== '/login' && !(await context.cookies()).length) {
        await page.goto(base + '/login')
        await page.fill('input[type=password]', password)
        await page.click('button[type=submit]')
        await page.waitForURL((u) => !u.pathname.startsWith('/login'))
      }
      if (route === '/login') await context.clearCookies()
      await page.goto(base + route)
      await page.waitForLoadState('networkidle')
      await page.waitForTimeout(Number(process.env.SETTLE_MS ?? 600))
      const name = `${route.replace(/^\//, '').replace(/[/?=&]+/g, '-') || 'root'}-${width}.png`
      await page.screenshot({ path: out + name, fullPage: process.env.FULL_PAGE === '1' })
      console.log('saved', name)
    }
    await context.close()
  }
} finally {
  await browser.close()
}
