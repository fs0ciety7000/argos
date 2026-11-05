export interface LogLine {
  ts?: string
  text: string
  truncated?: boolean
}

type Message = { type: 'lines'; lines?: LogLine[]; dropped?: number } | { type: 'end'; reason: string }

export const MAX_LINES = 5000

export type StreamState = 'idle' | 'connecting' | 'live' | 'ended' | 'error'

/** One live log stream (/api/logs/ws). Keeps the last MAX_LINES lines. */
export class LogStream {
  state = $state<StreamState>('idle')
  reason = $state<string | null>(null)
  lines = $state.raw<LogLine[]>([])
  dropped = $state(0)
  paused = $state(false)
  pendingCount = $state(0)

  #ws: WebSocket | null = null
  #pending: LogLine[] = []

  open(host: string, container: string) {
    this.close()
    this.lines = []
    this.dropped = 0
    this.reason = null
    this.#pending = []
    this.pendingCount = 0
    this.state = 'connecting'
    const proto = location.protocol === 'https:' ? 'wss' : 'ws'
    const q = new URLSearchParams({ host, container })
    const ws = new WebSocket(`${proto}://${location.host}/api/logs/ws?${q}`)
    this.#ws = ws
    ws.onopen = () => {
      if (this.#ws === ws) this.state = 'live'
    }
    ws.onmessage = (ev) => {
      if (this.#ws !== ws) return
      let msg: Message
      try {
        msg = JSON.parse(ev.data as string) as Message
      } catch {
        return
      }
      if (msg.type === 'end') {
        this.reason = msg.reason
        this.state = 'ended'
        return
      }
      if (msg.dropped) this.dropped += msg.dropped
      if (msg.lines?.length) this.#push(msg.lines)
    }
    ws.onclose = (ev) => {
      if (this.#ws !== ws) return
      this.#ws = null
      if (this.state !== 'ended') {
        this.state = 'error'
        this.reason = ev.code === 1008 ? 'session' : ev.code === 1006 ? 'rejected' : 'closed'
      }
    }
  }

  close() {
    const ws = this.#ws
    this.#ws = null
    ws?.close()
    this.state = 'idle'
  }

  setPaused(p: boolean) {
    this.paused = p
    if (!p && this.#pending.length) {
      const pending = this.#pending
      this.#pending = []
      this.pendingCount = 0
      this.#push(pending)
    }
  }

  clear() {
    this.lines = []
    this.#pending = []
    this.pendingCount = 0
    this.dropped = 0
  }

  #push(batch: LogLine[]) {
    if (this.paused) {
      this.#pending = [...this.#pending, ...batch].slice(-MAX_LINES)
      this.pendingCount = this.#pending.length
      return
    }
    const next = this.lines.length + batch.length > MAX_LINES ? [...this.lines, ...batch].slice(-MAX_LINES) : [...this.lines, ...batch]
    this.lines = next
  }
}
