// Automated layout QA of the panel (system Chrome via playwright-core):
// console errors, failed requests, horizontal overflow, clipped text and
// overlapping chart labels, per route and viewport.
//
//   BASE_URL=http://localhost:8000 ADMIN_PASSWORD=... node scripts/ui-qa.mjs [route ...]
import { chromium } from 'playwright-core'

const base = process.env.BASE_URL ?? 'http://localhost:8000'
const sizes = (process.env.SIZES ?? '1440x900,1366x768,1024x768,390x844').split(',').map((s) => s.split('x').map(Number))
const routes = process.argv.slice(2).length ? process.argv.slice(2) : ['/login', '/secretaria', '/financeiro', '/financeiro/transacoes', '/financeiro/categorias', '/financeiro/whatsapp']
const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH ?? '/usr/bin/google-chrome', headless: true })
let problems = 0

for (const [width, height] of sizes) {
  const context = await browser.newContext({ viewport: { width, height } })
  const page = await context.newPage()
  const issues = []
  page.on('console', (m) => m.type() === 'error' && issues.push(`console: ${m.text()}`))
  page.on('pageerror', (e) => issues.push(`pageerror: ${e.message}`))
  page.on('response', (r) => r.status() >= 400 && !r.url().includes('/api/session') && issues.push(`http ${r.status()} ${new URL(r.url()).pathname}`))

  await page.goto(base + '/login')
  await page.fill('input[type=password]', process.env.ADMIN_PASSWORD)
  await page.click('button[type=submit]')
  await page.waitForURL((u) => !u.pathname.startsWith('/login'))

  for (const route of routes) {
    if (route === '/login') continue
    issues.length = 0
    await page.goto(base + route)
    await page.waitForLoadState('networkidle')
    await page.waitForTimeout(700)
    const found = await page.evaluate(() => {
      const out = []
      if (document.documentElement.scrollWidth > window.innerWidth + 1) out.push(`horizontal overflow: ${document.documentElement.scrollWidth}px > ${window.innerWidth}px`)
      // Text that overflows its box without an intentional ellipsis.
      for (const el of document.querySelectorAll('p, span, h1, h2, h3, a, button, dd, dt, label, li')) {
        const cs = getComputedStyle(el)
        if (cs.display === 'none' || el.children.length > 2) continue
        if (el.scrollWidth > el.clientWidth + 2 && cs.textOverflow !== 'ellipsis' && cs.overflow === 'visible' && el.clientWidth > 0 && cs.whiteSpace === 'nowrap') {
          out.push(`clipped text: "${el.textContent.trim().slice(0, 40)}" (${el.scrollWidth}>${el.clientWidth})`)
        }
        const r = el.getBoundingClientRect()
        let scroller = el.parentElement
        while (scroller && !['auto', 'scroll'].includes(getComputedStyle(scroller).overflowX)) scroller = scroller.parentElement
        if (!scroller && r.width > 0 && (r.right > window.innerWidth + 1 || r.left < -1) && cs.position !== 'fixed') {
          out.push(`offscreen: "${el.textContent.trim().slice(0, 40)}" (${Math.round(r.left)}..${Math.round(r.right)})`)
        }
      }
      // Overlapping SVG labels inside each chart.
      for (const svg of document.querySelectorAll('svg')) {
        const texts = [...svg.querySelectorAll('text')].map((t) => ({ t: t.textContent, r: t.getBoundingClientRect() })).filter((x) => x.r.width > 0)
        for (let i = 0; i < texts.length; i++)
          for (let j = i + 1; j < texts.length; j++) {
            const a = texts[i].r, b = texts[j].r
            if (a.left < b.right - 1 && b.left < a.right - 1 && a.top < b.bottom - 1 && b.top < a.bottom - 1) out.push(`svg labels overlap: "${texts[i].t}" / "${texts[j].t}"`)
          }
      }
      return [...new Set(out)].slice(0, 15)
    })
    const all = [...issues, ...found]
    problems += all.length
    console.log(`${width}x${height} ${route}: ${all.length ? '\n  - ' + all.join('\n  - ') : 'ok'}`)
  }
  await context.close()
}
await browser.close()
console.log(problems ? `${problems} problem(s)` : 'no problems found')
process.exit(problems ? 1 : 0)
