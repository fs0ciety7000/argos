import { ApiError, request } from './api'

type Status = 'loading' | 'anonymous' | 'authenticated' | 'unreachable'

interface SessionResponse {
  authenticated: boolean
  expiresAt: string
}

/** Session state shared by the whole app. */
class Auth {
  status = $state<Status>('loading')
  expiresAt = $state<Date | null>(null)

  async check(): Promise<void> {
    try {
      const s = await request<SessionResponse>('/api/auth/session')
      this.#set(s)
    } catch (err) {
      this.expiresAt = null
      this.status = err instanceof ApiError && err.status === 401 ? 'anonymous' : 'unreachable'
    }
  }

  /** Throws ApiError on failure; the caller turns it into a message. */
  async login(password: string, code: string): Promise<void> {
    const s = await request<SessionResponse>('/api/auth/login', {
      method: 'POST',
      body: { password, code },
    })
    this.#set(s)
  }

  async logout(): Promise<void> {
    try {
      await request<void>('/api/auth/logout', { method: 'POST' })
    } finally {
      this.expiresAt = null
      this.status = 'anonymous'
    }
  }

  #set(s: SessionResponse) {
    this.expiresAt = new Date(s.expiresAt)
    this.status = s.authenticated ? 'authenticated' : 'anonymous'
  }
}

export const auth = new Auth()

/** Human message for a failed login. */
export function loginErrorMessage(err: unknown): string {
  if (!(err instanceof ApiError)) return 'Erreur inattendue. Réessaie.'
  switch (err.status) {
    case 0:
      return 'Serveur injoignable. Vérifie ta connexion.'
    case 401:
      return 'Mot de passe ou code incorrect.'
    case 429: {
      const min = err.retryAfter ? Math.ceil(err.retryAfter / 60) : 15
      return `Trop d’essais. Réessaie dans ${min} min.`
    }
    case 503:
      return 'Connexion non configurée sur le serveur (ARGOS_ADMIN_PASSWORD_HASH, ARGOS_TOTP_SECRET).'
    default:
      return `Erreur serveur (${err.status}). Réessaie.`
  }
}
