import { request } from './api'

export interface Container {
  id: string
  name: string
  image: string
  state: 'running' | 'exited' | 'restarting' | 'paused' | 'created' | 'dead' | string
  health?: 'healthy' | 'unhealthy' | 'starting' | string
  exitCode: number
  oomKilled: boolean
  startedAt?: string
  finishedAt?: string
  restartCount: number
  autoRestart: boolean
  app?: string
  protected: boolean
  failing: boolean
  cpu?: number
  mem?: number
  memLimit?: number
}

export interface HostContainers {
  host: string
  containers: Container[]
  updatedAt?: string
  error?: string
}

export interface Action {
  ts: string
  host: string
  container: string
  action: 'restart' | 'auto-restart-abandoned' | string
  trigger: 'manual' | 'auto' | string
  ok: boolean
  message?: string
}

export const restartContainer = (host: string, name: string) =>
  request<{ status: string }>(
    `/api/hosts/${encodeURIComponent(host)}/containers/${encodeURIComponent(name)}/restart`,
    { method: 'POST' },
  )
export const listActions = () => request<Action[]>('/api/actions')
