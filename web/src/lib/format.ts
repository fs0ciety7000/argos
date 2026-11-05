const nf = (digits: number) =>
  new Intl.NumberFormat('fr-FR', { minimumFractionDigits: digits, maximumFractionDigits: digits })

/** 1234567890 → "1,23 Go"-style, in SI units (TB for disks, like df -H). */
export function bytes(n: number | null | undefined, digits = 1): string {
  if (n === null || n === undefined || !Number.isFinite(n)) return '—'
  const units = ['o', 'Ko', 'Mo', 'Go', 'To', 'Po']
  let i = 0
  let v = Math.abs(n)
  while (v >= 1000 && i < units.length - 1) {
    v /= 1000
    i++
  }
  return `${nf(i === 0 ? 0 : digits).format(Math.sign(n) * v)} ${units[i]}`
}

/** Bytes per second → bits per second with a unit, e.g. "84,2 Mb/s". */
export function bitrate(bps: number | null | undefined): { value: string; unit: string } {
  if (bps === null || bps === undefined || !Number.isFinite(bps) || bps < 0) return { value: '—', unit: '' }
  const units = ['b/s', 'Kb/s', 'Mb/s', 'Gb/s']
  let v = bps * 8
  let i = 0
  while (v >= 1000 && i < units.length - 1) {
    v /= 1000
    i++
  }
  return { value: nf(v >= 100 || i === 0 ? 0 : 1).format(v), unit: units[i] }
}

export function pct(v: number | null | undefined, digits = 0): string {
  if (v === null || v === undefined || !Number.isFinite(v) || v < 0) return '—'
  return nf(digits).format(v)
}

/** Threshold tone for a fill ratio (0..1). */
export function fillTone(ratio: number): 'ok' | 'warn' | 'crit' {
  if (ratio >= 0.95) return 'crit'
  if (ratio >= 0.85) return 'warn'
  return 'ok'
}

export const reducedMotion = () =>
  typeof matchMedia !== 'undefined' && matchMedia('(prefers-reduced-motion: reduce)').matches
