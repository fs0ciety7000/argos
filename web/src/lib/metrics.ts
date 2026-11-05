import { request } from './api'

export type Range = '1h' | '24h' | '7d'

export interface Series {
  ts: number[] // unix seconds
  cpu: (number | null)[]
  memPct: (number | null)[]
  rx: (number | null)[]
  tx: (number | null)[]
}

export interface Throughput {
  ts: number[]
  rx: (number | null)[]
  tx: (number | null)[]
}

export interface DiskTrend {
  host: string
  mount: string
  size: number
  used: number
  bytesPerDay: number
  daysToFull?: number
  bytesPerDay24h?: number
  daysToFull24h?: number
  since: number
  updatedAt: number
}

export interface DiskSeries {
  ts: number[]
  used: number[]
  size: number[]
}

export const history = (host: string, range: Range) =>
  request<Series>(`/api/metrics?host=${encodeURIComponent(host)}&range=${range}`)
export const throughput = (range: Range) => request<Throughput>(`/api/throughput?range=${range}`)
export const storage = () => request<DiskTrend[]>('/api/storage')
export const storageHistory = (host: string, mount: string, range: '24h' | '7d') =>
  request<DiskSeries>(
    `/api/storage/history?host=${encodeURIComponent(host)}&mount=${encodeURIComponent(mount)}&range=${range}`,
  )
