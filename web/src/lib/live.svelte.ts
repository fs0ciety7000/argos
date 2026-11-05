import { auth } from './auth.svelte'
import type { HostStatus } from './hosts'
import type { HostContainers } from './containers'

export interface Disk {
  mount: string
  size: number
  used: number
}

export interface Sample {
  host: string
  ts: string
  cpu: number // -1 = unknown
  memUsed: number
  memTotal: number
  load1: number
  rxBps: number // bytes/s, -1 = unknown
  txBps: number
  disks: Disk[] | null
}

type Message =
  | { type: 'snapshot'; hosts: HostStatus[]; latest?: Record<string, Sample>; containers?: HostContainers[] }
  | { type: 'containers'; containers: HostContainers[] }
  | { type: 'status'; hosts: HostStatus[] }
  | { type: 'sample'; sample: Sample }

type Listener = (s: Sample) => void

/** Real-time connection to /api/ws, shared by every view. */
class Live {
  state = $state<'connecting' | 'open' | 'closed'>('closed')
  hosts = $state.raw<HostStatus[] | null>(null)
  latest = $state.raw<Record<string, Sample>>({})
  /** Docker hosts' containers, keyed by host name (null until the snapshot). */
  containers = $state.raw<Record<string, HostContainers> | null>(null)
  lastMessage = $state(0)

  #ws: WebSocket | null = null
  #retry = 1000
  #timer: ReturnType<typeof setTimeout> | null = null
  #stopped = true
  #listeners = new Set<Listener>()

  start() {
    if (!this.#stopped) return
    this.#stopped = false
    this.#open()
  }

  stop() {
    this.#stopped = true
    if (this.#timer) clearTimeout(this.#timer)
    this.#ws?.close()
    this.#ws = null
    this.state = 'closed'
  }

  /** Called for every new sample; returns an unsubscribe function. */
  onSample(fn: Listener): () => void {
    this.#listeners.add(fn)
    return () => this.#listeners.delete(fn)
  }

  #open() {
    const proto = location.protocol === 'https:' ? 'wss' : 'ws'
    const ws = new WebSocket(`${proto}://${location.host}/api/ws`)
    this.#ws = ws
    this.state = 'connecting'

    ws.onopen = () => {
      this.state = 'open'
      this.#retry = 1000
    }
    ws.onmessage = (ev) => {
      let msg: Message
      try {
        msg = JSON.parse(ev.data as string) as Message
      } catch {
        return
      }
      this.lastMessage = Date.now()
      switch (msg.type) {
        case 'snapshot':
          this.hosts = msg.hosts ?? []
          this.latest = msg.latest ?? {}
          this.containers = Object.fromEntries((msg.containers ?? []).map((c) => [c.host, { ...c, containers: c.containers ?? [] }]))
          break
        case 'containers': {
          const next = { ...(this.containers ?? {}) }
          for (const c of msg.containers ?? []) next[c.host] = { ...c, containers: c.containers ?? [] }
          this.containers = next
          break
        }
        case 'status':
          this.hosts = msg.hosts ?? []
          break
        case 'sample':
          this.latest = { ...this.latest, [msg.sample.host]: msg.sample }
          for (const fn of this.#listeners) fn(msg.sample)
          break
      }
    }
    ws.onclose = (ev) => {
      this.#ws = null
      this.state = 'closed'
      if (this.#stopped) return
      if (ev.code === 1008) {
        // Session expired server-side: let the app fall back to the login.
        void auth.check()
        return
      }
      this.#timer = setTimeout(() => this.#open(), this.#retry)
      this.#retry = Math.min(this.#retry * 2, 15_000)
    }
  }
}

export const live = new Live()
