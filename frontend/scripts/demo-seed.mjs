// Fills a LOCAL finance workspace with fictitious data (Ana & Bruno) through
// the public API and the fake WhatsApp gateway, so the dashboard can be
// reviewed. Requires the backend running with WHATSAPP_FAKE and OPENAI_FAKE.
//
//   BASE_URL=http://localhost:8000 ADMIN_PASSWORD=... node scripts/demo-seed.mjs
//
// It refuses to run against anything that is not localhost.
const base = process.env.BASE_URL ?? 'http://localhost:8000'
if (!/^http:\/\/(localhost|127\.0\.0\.1)(:\d+)?$/.test(base)) {
  console.error('demo-seed only runs against localhost')
  process.exit(1)
}
const origin = { Origin: base, 'Content-Type': 'application/json' }
let cookie = ''

async function call(method, path, body) {
  const res = await fetch(base + path, { method, headers: { ...origin, Cookie: cookie }, body: body ? JSON.stringify(body) : undefined })
  const setCookie = res.headers.get('set-cookie')
  if (setCookie) cookie = setCookie.split(';')[0]
  const text = await res.text()
  if (!res.ok) throw new Error(`${method} ${path} -> ${res.status} ${text}`)
  return text ? JSON.parse(text) : null
}

await call('POST', '/api/login', { password: process.env.ADMIN_PASSWORD })
const wa = await call('GET', '/api/finance/whatsapp')
if (!wa.fake) throw new Error('backend is not in WHATSAPP_FAKE mode; refusing to seed')

const GROUP = '120363000000000001@g.us'
const ANA = '5511900000001@s.whatsapp.net'
const BRUNO = '5511900000002@s.whatsapp.net'
await call('POST', '/api/finance/whatsapp/group', { jid: GROUP })
const { members } = await call('GET', '/api/finance/members')
const id = (name) => members.find((m) => m.display_name === name)?.id
const { categories } = await call('GET', '/api/finance/categories')
const cat = (name) => categories.find((c) => c.name === name)?.id

for (const [name, reais] of [['Restaurantes', 600], ['Delivery', 400], ['Lazer', 400], ['Mercado', 1200], ['Combustível', 500]]) {
  await call('PATCH', `/api/finance/categories/${cat(name)}`, { monthly_budget_cents: reais * 100 })
}

const web = [
  // June / July: grocery history so the outlier check has a baseline
  ...['06-04', '06-12', '06-20', '06-28', '07-05', '07-13', '07-21', '07-29'].map((d, i) => ['EXPENSE', 180 + i * 15, 'Mercado', `2026-${d}`, i % 2 ? 'Bruno' : 'Ana', 'Mercado da semana']),
  // August
  ['INCOME', 6500, 'Salário', '2026-08-05', 'Ana', 'Salário'],
  ['INCOME', 5200, 'Salário', '2026-08-05', 'Bruno', 'Salário'],
  ['EXPENSE', 1800, 'Aluguel', '2026-08-05', 'Ana', 'Aluguel de agosto'],
  ['EXPENSE', 320, 'Mercado', '2026-08-03', 'Bruno', 'Compra do mês'],
  ['EXPENSE', 280, 'Mercado', '2026-08-11', 'Ana', 'Mercado'],
  ['EXPENSE', 410, 'Mercado', '2026-08-19', 'Bruno', 'Mercado'],
  ['EXPENSE', 190, 'Mercado', '2026-08-26', 'Ana', 'Hortifruti'],
  ['EXPENSE', 120, 'Restaurantes', '2026-08-09', 'Bruno', 'Jantar'],
  ['EXPENSE', 95, 'Restaurantes', '2026-08-16', 'Ana', 'Almoço'],
  ['EXPENSE', 180, 'Restaurantes', '2026-08-23', 'Bruno', 'Aniversário'],
  ['EXPENSE', 60, 'Delivery', '2026-08-07', 'Ana', 'iFood'],
  ['EXPENSE', 45, 'Delivery', '2026-08-14', 'Bruno', 'Pizza'],
  ['EXPENSE', 80, 'Delivery', '2026-08-21', 'Ana', 'iFood'],
  ['EXPENSE', 230, 'Combustível', '2026-08-06', 'Bruno', 'Posto'],
  ['EXPENSE', 250, 'Combustível', '2026-08-24', 'Bruno', 'Posto'],
  ['EXPENSE', 120, 'Internet', '2026-08-10', 'Ana', 'Internet'],
  ['EXPENSE', 210, 'Contas da casa', '2026-08-12', 'Ana', 'Luz'],
  ['EXPENSE', 85, 'Farmácia', '2026-08-15', 'Bruno', 'Farmácia'],
  ['EXPENSE', 90, 'Lazer', '2026-08-17', 'Ana', 'Cinema'],
  ['EXPENSE', 55, 'Assinaturas', '2026-08-01', 'Ana', 'Streaming'],
  ['EXPENSE', 22, 'Assinaturas', '2026-08-02', 'Bruno', 'Música'],
  // September, early month
  ['INCOME', 6500, 'Salário', '2026-09-05', 'Ana', 'Salário'],
  ['INCOME', 5200, 'Salário', '2026-09-05', 'Bruno', 'Salário'],
  ['INCOME', 850, 'Renda extra', '2026-09-18', 'Bruno', 'Freela'],
  ['EXPENSE', 1800, 'Aluguel', '2026-09-05', 'Ana', 'Aluguel de setembro'],
  ['EXPENSE', 340, 'Mercado', '2026-09-02', 'Bruno', 'Compra do mês'],
  ['EXPENSE', 890, 'Mercado', '2026-09-12', 'Ana', 'Compra grande (festa)'],
  ['EXPENSE', 150, 'Restaurantes', '2026-09-06', 'Bruno', 'Jantar'],
  ['EXPENSE', 210, 'Restaurantes', '2026-09-13', 'Ana', 'Almoço de domingo'],
  ['EXPENSE', 118, 'Restaurantes', '2026-09-20', 'Bruno', 'Jantar'],
  ['EXPENSE', 72, 'Delivery', '2026-09-04', 'Ana', 'iFood'],
  ['EXPENSE', 95, 'Delivery', '2026-09-11', 'Bruno', 'Pizza'],
  ['EXPENSE', 88, 'Delivery', '2026-09-17', 'Ana', 'iFood'],
  ['EXPENSE', 64, 'Delivery', '2026-09-22', 'Bruno', 'Japonês'],
  ['EXPENSE', 120, 'Internet', '2026-09-10', 'Ana', 'Internet'],
  ['EXPENSE', 236, 'Contas da casa', '2026-09-12', 'Ana', 'Luz'],
  ['EXPENSE', 250, 'Lazer', '2026-09-19', 'Bruno', 'Show'],
  ['EXPENSE', 55, 'Assinaturas', '2026-09-01', 'Ana', 'Streaming'],
  ['EXPENSE', 22, 'Assinaturas', '2026-09-02', 'Bruno', 'Música'],
  ['EXPENSE', 140, 'Pets', '2026-09-08', 'Ana', 'Ração'],
]
for (const [type, reais, category, date, payer, description] of web) {
  await call('POST', '/api/finance/transactions', {
    type, amount_cents: reais * 100, category_id: cat(category), transaction_date: date, payer_member_id: id(payer), description,
  })
}

// Recent days through the WhatsApp path (router -> inbox -> agent -> ledger).
const say = async (sender, text, extra = {}) => {
  await call('POST', '/api/dev/whatsapp/messages', { chat_jid: GROUP, sender_jid: sender, push_name: sender === ANA ? 'Ana' : 'Bruno', text, ...extra })
  await new Promise((r) => setTimeout(r, 350))
}
await say(ANA, 'gastei 87,40 no mercado')
await say(BRUNO, 'paguei 250 de gasolina')
await say(ANA, 'gastei 42 no almoço')
await say(BRUNO, 'paguei a fatura do cartão 1.200')
await say(ANA, 'gastei 80')
await say(ANA, 'delivery')
await say(BRUNO, 'gastei 95 no ifood ontem')
// A receipt (tiny PNG) with a caption; the fake extractor reads the caption.
const png = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==', 'base64')
await say(ANA, 'pix do mercado 89,90', { kind: 'IMAGE', mime: 'image/png', media_base64: png.toString('base64') })
await say(BRUNO, 'gastei 35')
await say(ANA, 'quanto gastamos esse mês?')

const out = await call('GET', '/api/dev/whatsapp/outbox')
for (const m of out.messages) console.log('bot>', m.text.split('\n')[0])
console.log('seeded', web.length, 'panel entries and', out.messages.length, 'bot replies')
