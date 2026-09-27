import { describe, it, expect, vi, afterEach } from 'vitest'
import { api, buildQuery, ApiError, onUnauthorized } from '../client'

afterEach(() => vi.unstubAllGlobals())

describe('buildQuery', () => {
  it('drops empty values and encodes the rest', () => {
    expect(buildQuery({ a: 'x y', b: '', c: null, d: undefined, e: 0 })).toBe('?a=x+y&e=0')
    expect(buildQuery({})).toBe('')
  })
})

describe('api', () => {
  it('surfaces the friendly backend message, never raw text', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ error: 'bad', message: 'Mensagem amigável' }), { status: 400 })))
    await expect(api('/api/x')).rejects.toMatchObject({ status: 400, code: 'bad', message: 'Mensagem amigável' })
  })

  it('uses a generic message when the body is not JSON', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('panic: nil pointer at db.go:42', { status: 500 })))
    const err = (await api('/api/x').catch((e) => e)) as ApiError
    expect(err).toBeInstanceOf(ApiError)
    expect(err.message).not.toContain('panic')
  })

  it('notifies listeners on 401', async () => {
    const spy = vi.fn()
    const off = onUnauthorized(spy)
    vi.stubGlobal('fetch', vi.fn(async () => new Response('{}', { status: 401 })))
    await api('/api/x').catch(() => {})
    off()
    expect(spy).toHaveBeenCalledOnce()
  })

  it('reports network failures', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => { throw new TypeError('Failed to fetch') }))
    await expect(api('/api/x')).rejects.toMatchObject({ status: 0, code: 'network' })
  })

  it('sends JSON bodies same-origin', async () => {
    const fetchMock = vi.fn(async () => new Response('{"ok":true}', { status: 200 }))
    vi.stubGlobal('fetch', fetchMock)
    await api('/api/x', { method: 'POST', body: { a: 1 } })
    const [, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit]
    expect(init.credentials).toBe('same-origin')
    expect(init.body).toBe('{"a":1}')
  })
})
