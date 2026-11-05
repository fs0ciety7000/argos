/** Error returned by the Argos API, with the HTTP status and server message. */
export class ApiError extends Error {
  constructor(
    readonly status: number,
    message: string,
    readonly retryAfter?: number,
  ) {
    super(message)
    this.name = 'ApiError'
  }
}

/**
 * Same-origin JSON request. Throws ApiError on non-2xx responses and on
 * network failure (status 0).
 */
export async function request<T>(path: string, init: { method?: string; body?: unknown } = {}): Promise<T> {
  let res: Response
  try {
    res = await fetch(path, {
      method: init.method ?? 'GET',
      credentials: 'same-origin',
      headers: init.body === undefined ? undefined : { 'Content-Type': 'application/json' },
      body: init.body === undefined ? undefined : JSON.stringify(init.body),
    })
  } catch {
    throw new ApiError(0, 'network error')
  }

  if (!res.ok) {
    let message = res.statusText
    try {
      const data = (await res.json()) as { error?: string }
      if (data.error) message = data.error
    } catch {
      // Non-JSON error body: keep the status text.
    }
    const retry = Number(res.headers.get('Retry-After'))
    throw new ApiError(res.status, message, Number.isFinite(retry) && retry > 0 ? retry : undefined)
  }

  if (res.status === 204) return undefined as T
  return (await res.json()) as T
}
