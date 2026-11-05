import { request } from './api'

export type HostKind = 'vps' | 'seedbox'
export type HostState = 'connecting' | 'connected' | 'error' | 'hostkey_changed'

export interface Host {
  name: string
  kind: HostKind
  address: string
  port: number
  user: string
  docker?: boolean
}

export interface HostStatus {
  host: Host
  state: HostState
  error?: string
  latencyMs?: number
  lastSeen?: string
  connectedSince?: string
  hostKey?: string
}

export interface PublicKey {
  publicKey: string
  fingerprint: string
}

export const listHosts = () => request<HostStatus[]>('/api/hosts')
export const reconnectHost = (name: string) =>
  request<void>(`/api/hosts/${encodeURIComponent(name)}/reconnect`, { method: 'POST' })
export const getPublicKey = () => request<PublicKey>('/api/ssh/public-key')

/** A connected host whose last keepalive is older than this is "stale". */
export const STALE_AFTER_MS = 45_000
