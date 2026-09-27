// Thin fetch wrapper for the Go API. The session lives in an HttpOnly cookie,
// so requests only need to stay same-origin. Errors carry a friendly message:
// raw backend errors never reach the screen.

export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
  ) {
    super(message)
  }
}

type Query = Record<string, string | number | boolean | null | undefined>

export interface RequestOptions {
  method?: 'GET' | 'POST' | 'PATCH' | 'PUT' | 'DELETE'
  body?: unknown
  query?: Query
}

const unauthorizedListeners = new Set<() => void>()

/** Registers a callback fired whenever the API answers 401 (session expired). */
export function onUnauthorized(cb: () => void): () => void {
  unauthorizedListeners.add(cb)
  return () => unauthorizedListeners.delete(cb)
}

export function buildQuery(query?: Query): string {
  if (!query) return ''
  const params = new URLSearchParams()
  for (const [key, value] of Object.entries(query)) {
    if (value === undefined || value === null || value === '') continue
    params.set(key, String(value))
  }
  const s = params.toString()
  return s ? `?${s}` : ''
}

const GENERIC_ERROR = 'Não foi possível concluir a operação. Tente novamente.'

export async function api<T>(path: string, opts: RequestOptions = {}): Promise<T> {
  const init: RequestInit = {
    method: opts.method ?? 'GET',
    credentials: 'same-origin',
    headers: { Accept: 'application/json' },
  }
  if (opts.body !== undefined) {
    init.body = JSON.stringify(opts.body)
    ;(init.headers as Record<string, string>)['Content-Type'] = 'application/json'
  }

  let res: Response
  try {
    res = await fetch(path + buildQuery(opts.query), init)
  } catch {
    throw new ApiError(0, 'network', 'Sem conexão com o servidor. Verifique sua rede.')
  }

  const text = await res.text()
  let data: unknown = null
  if (text) {
    try {
      data = JSON.parse(text)
    } catch {
      data = null
    }
  }

  if (!res.ok) {
    if (res.status === 401) unauthorizedListeners.forEach((cb) => cb())
    const body = (data ?? {}) as { error?: string; message?: string }
    throw new ApiError(res.status, body.error ?? `http_${res.status}`, body.message ?? GENERIC_ERROR)
  }
  return data as T
}
