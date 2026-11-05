<script lang="ts">
  import { onMount } from 'svelte'
  import { fly } from 'svelte/transition'
  import { cubicOut } from 'svelte/easing'
  import { storage, storageHistory, type DiskSeries, type DiskTrend } from '../metrics'
  import { bytes, fillTone, pct } from '../format'
  import DiskChart from './DiskChart.svelte'

  type Range = '24h' | '7d'
  let range = $state<Range>('24h')
  let trends = $state.raw<DiskTrend[] | null>(null)
  let error = $state(false)
  let series = $state.raw<Record<string, DiskSeries>>({})

  const key = (t: DiskTrend) => `${t.host}\u0000${t.mount}`

  async function loadTrends() {
    try {
      trends = await storage()
      error = false
    } catch {
      error = true
      trends ??= []
    }
  }

  async function loadSeries(list: DiskTrend[], r: Range) {
    const entries = await Promise.all(
      list.map(async (t) => {
        try {
          return [key(t), await storageHistory(t.host, t.mount, r)] as const
        } catch {
          return null
        }
      }),
    )
    if (r !== range) return // range changed meanwhile
    series = Object.fromEntries(entries.filter((e) => e !== null))
  }

  onMount(() => {
    loadTrends()
    const t = setInterval(loadTrends, 60_000)
    return () => clearInterval(t)
  })

  // History follows the volume list and the selected range.
  $effect(() => {
    const list = trends
    const r = range
    if (list && list.length) loadSeries(list, r)
  })

  /** Most urgent first: projected days to full, then fill ratio. */
  function days(t: DiskTrend): number | undefined {
    const ds = [t.daysToFull24h, t.daysToFull].filter((d): d is number => d !== undefined)
    return ds.length ? Math.min(...ds) : undefined
  }

  let rows = $derived(
    (trends ?? [])
      .filter((t) => t.size > 0)
      .map((t) => ({ ...t, ratio: t.used / t.size, tone: fillTone(t.used / t.size), days: days(t) }))
      .sort((a, b) => (a.days ?? Infinity) - (b.days ?? Infinity) || b.ratio - a.ratio),
  )

  let summary = $derived.by(() => {
    let used = 0
    let size = 0
    let warn = 0
    let crit = 0
    for (const r of rows) {
      used += r.used
      size += r.size
      if (r.tone === 'warn') warn++
      if (r.tone === 'crit') crit++
    }
    const urgent = rows.find((r) => r.days !== undefined)
    return { used, size, warn, crit, urgent }
  })

  const dateFmt = new Intl.DateTimeFormat('fr-FR', { day: 'numeric', month: 'short' })
  function projection(d: number | undefined): string {
    if (d === undefined) return 'stable'
    if (d > 365) return 'plein dans > 1 an'
    const when = dateFmt.format(new Date(Date.now() + d * 86_400_000))
    return d < 1 ? `plein en moins d’un jour` : `plein vers le ${when} (~${Math.round(d)} j)`
  }

  function rate(v: number | undefined): string {
    if (v === undefined) return '—'
    if (Math.abs(v) < 1e6) return 'stable'
    return `${v > 0 ? '+' : '−'}${bytes(Math.abs(v))}/j`
  }

  const glyph = { ok: '●', warn: '▲', crit: '■' } as const
  const mountLabel = (m: string) => (m === 'quota' ? 'quota du compte' : m)
</script>

<section class="stack" aria-labelledby="st-title">
  <header class="head">
    <h2 id="st-title">Stockage</h2>
    <div class="seg mono" role="group" aria-label="Période de l’historique">
      <button type="button" aria-pressed={range === '24h'} onclick={() => (range = '24h')}>24h</button>
      <button type="button" aria-pressed={range === '7d'} onclick={() => (range = '7d')}>7j</button>
    </div>
  </header>

  {#if error}
    <p class="banner mono" role="alert">■ Impossible de charger le stockage.</p>
  {/if}

  {#if trends === null}
    <div class="card skeleton" aria-busy="true"></div>
  {:else if rows.length === 0}
    <div class="card empty">
      <h3>Pas encore de mesure de disque</h3>
      <p>La première arrive dans la minute qui suit la connexion d’un hôte.</p>
    </div>
  {:else}
    <section class="summary" aria-label="Synthèse">
      <article class="card tile" in:fly={{ y: 12, duration: 700, easing: cubicOut }}>
        <span class="lbl">Utilisé</span>
        <div class="mono big-row"><span class="big">{bytes(summary.used)}</span><span class="of">/ {bytes(summary.size)}</span></div>
        <span class="mono sub">{pct((summary.used / summary.size) * 100)} % · {rows.length} volume{rows.length > 1 ? 's' : ''}</span>
      </article>
      <article class="card tile" in:fly={{ y: 12, duration: 700, delay: 80, easing: cubicOut }}>
        <span class="lbl">Alertes</span>
        <div class="mono big-row">
          <span class="big" class:crit={summary.crit > 0}>{summary.crit}</span><span class="of">■ critiques</span>
          <span class="big small" class:warn={summary.warn > 0}>{summary.warn}</span><span class="of">▲ alertes</span>
        </div>
        <span class="mono sub">seuils 85 % et 95 %</span>
      </article>
      <article class="card tile" in:fly={{ y: 12, duration: 700, delay: 160, easing: cubicOut }}>
        <span class="lbl">Prochain plein</span>
        {#if summary.urgent}
          <div class="mono big-row"><span class="big">{summary.urgent.host}</span></div>
          <span class="mono sub">{mountLabel(summary.urgent.mount)} · {projection(summary.urgent.days)}</span>
        {:else}
          <div class="mono big-row"><span class="big">—</span></div>
          <span class="mono sub">aucun volume en croissance</span>
        {/if}
      </article>
    </section>

    <section class="volumes" aria-label="Volumes">
      {#each rows as r, i (key(r))}
        {@const s = series[key(r)]}
        <article class="card vol" data-tone={r.tone} in:fly={{ y: 12, duration: 700, delay: 200 + i * 60, easing: cubicOut }}>
          <header class="vol-head">
            <div class="id">
              <h3><span class="g" data-tone={r.tone}>{glyph[r.tone]}</span> {r.host}</h3>
              <p class="mono meta">{mountLabel(r.mount)}</p>
            </div>
            <span class="mono pct" data-tone={r.tone}>{pct(r.ratio * 100, 1)} %</span>
          </header>

          <div class="track" role="img" aria-label="{r.host} {mountLabel(r.mount)} : {pct(r.ratio * 100)} % utilisé">
            <div class="fill" data-tone={r.tone} style:width="{Math.min(r.ratio, 1) * 100}%"></div>
            <div class="threshold" aria-hidden="true"></div>
          </div>

          <dl class="mono facts">
            <div><dt>utilisé</dt><dd>{bytes(r.used)} / {bytes(r.size)}</dd></div>
            <div><dt>libre</dt><dd>{bytes(Math.max(r.size - r.used, 0))}</dd></div>
            <div><dt>croissance 24 h</dt><dd>{rate(r.bytesPerDay24h)}</dd></div>
            <div><dt>croissance 7 j</dt><dd>{rate(r.bytesPerDay)}</dd></div>
            <div class="wide"><dt>projection</dt><dd data-tone={r.days !== undefined && r.days < 14 ? 'crit' : undefined}>{projection(r.days)}</dd></div>
          </dl>

          {#if s && s.ts.length > 1}
            <DiskChart series={s} days={range === '7d'} tone={r.tone} label="Remplissage de {r.host} {mountLabel(r.mount)}, {range === '7d' ? '7 jours' : '24 heures'}" />
          {:else}
            <p class="mono placeholder">Historique en cours de constitution (un relevé par minute).</p>
          {/if}
        </article>
      {/each}
    </section>
  {/if}
</section>

<style>
  .stack {
    display: flex;
    flex-direction: column;
    gap: 20px;
  }

  .head {
    display: flex;
    flex-wrap: wrap;
    align-items: flex-end;
    justify-content: space-between;
    gap: 12px;
  }

  h2 {
    margin: 0;
    font-size: 40px;
    font-weight: 300;
    letter-spacing: -0.04em;
    line-height: 1.1;
  }

  h3 {
    margin: 0;
    font-size: 15px;
    font-weight: 500;
  }

  .seg {
    display: flex;
    padding: 3px;
    background: var(--bg);
    border: 1px solid var(--line);
    border-radius: var(--radius-control);
    font-size: 12px;
  }

  .seg button {
    min-height: 32px;
    padding: 0 14px;
    background: none;
    color: var(--text-2);
    border: 0;
    border-radius: 7px;
    cursor: pointer;
  }

  .seg button[aria-pressed='true'] {
    background: var(--fill);
    color: var(--text);
  }

  .card {
    background: var(--surface);
    border: 1px solid var(--line);
    border-radius: var(--radius-panel);
    padding: 20px;
    min-width: 0;
  }

  .summary {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(240px, 1fr));
    gap: 20px;
  }

  .tile {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }

  .lbl {
    font-size: 13px;
    color: var(--text-2);
  }

  .big-row {
    display: flex;
    align-items: baseline;
    flex-wrap: wrap;
    gap: 4px 8px;
  }

  .big {
    font-family: var(--font-sans);
    font-size: 40px;
    font-weight: 300;
    letter-spacing: -0.04em;
    line-height: 1.05;
    overflow-wrap: anywhere;
  }

  .big.small {
    margin-left: 12px;
  }

  .big.crit {
    color: var(--crit);
  }

  .big.warn {
    color: var(--warn);
  }

  .of,
  .sub {
    font-size: 12px;
    color: var(--text-2);
  }

  .volumes {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(420px, 1fr));
    gap: 20px;
  }

  .vol {
    display: flex;
    flex-direction: column;
    gap: 14px;
    transition:
      border-color 350ms var(--ease-out),
      transform 350ms var(--ease-out);
  }

  .vol:hover {
    border-color: var(--line-strong);
    transform: translateY(-2px);
  }

  .vol[data-tone='warn'] {
    border-color: color-mix(in srgb, var(--warn) 30%, transparent);
  }

  .vol[data-tone='crit'] {
    border-color: color-mix(in srgb, var(--crit) 30%, transparent);
  }

  .vol-head {
    display: flex;
    justify-content: space-between;
    align-items: flex-start;
    gap: 12px;
  }

  .meta {
    margin: 2px 0 0;
    font-size: 12px;
    color: var(--text-2);
    overflow-wrap: anywhere;
  }

  [data-tone='ok'] {
    --tone: var(--ok);
  }
  [data-tone='warn'] {
    --tone: var(--warn);
  }
  [data-tone='crit'] {
    --tone: var(--crit);
  }

  .g,
  .pct[data-tone='warn'],
  .pct[data-tone='crit'],
  dd[data-tone='crit'] {
    color: var(--tone);
  }

  .pct {
    font-family: var(--font-sans);
    font-size: 32px;
    font-weight: 300;
    letter-spacing: -0.03em;
    line-height: 1;
  }

  .track {
    position: relative;
    height: 10px;
    background: var(--fill);
    border-radius: var(--radius-pill);
    overflow: hidden;
  }

  .fill {
    height: 10px;
    background: var(--accent);
    border-radius: var(--radius-pill);
    transition: width 1.2s var(--ease-out);
  }

  .fill[data-tone='warn'],
  .fill[data-tone='crit'] {
    background: var(--tone);
  }

  .threshold {
    position: absolute;
    left: 85%;
    top: 0;
    width: 1px;
    height: 10px;
    background: var(--text);
    opacity: 0.6;
  }

  .facts {
    margin: 0;
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(210px, 1fr));
    gap: 6px 20px;
    font-size: 12px;
  }

  .facts div {
    display: flex;
    justify-content: space-between;
    gap: 8px;
  }

  .facts .wide {
    grid-column: 1 / -1;
  }

  dt {
    color: var(--text-2);
    white-space: nowrap;
  }

  dd {
    margin: 0;
    text-align: right;
  }

  .placeholder {
    margin: 0;
    padding: 24px 0;
    font-size: 12px;
    color: var(--text-2);
    text-align: center;
  }

  .banner {
    margin: 0;
    padding: 10px 14px;
    font-size: 12.5px;
    color: var(--crit);
    background: color-mix(in srgb, var(--crit) 8%, transparent);
    border: 1px solid color-mix(in srgb, var(--crit) 20%, transparent);
    border-radius: var(--radius-control);
  }

  .empty p {
    margin: 8px 0 0;
    color: var(--text-2);
  }

  .skeleton {
    min-height: 260px;
    background: linear-gradient(90deg, var(--surface) 0%, var(--fill) 50%, var(--surface) 100%);
    background-size: 200% 100%;
    animation: shimmer 1.6s ease-in-out infinite;
  }

  @keyframes shimmer {
    to {
      background-position: -200% 0;
    }
  }

  @media (max-width: 720px) {
    h2 {
      font-size: 32px;
    }

    .volumes {
      grid-template-columns: minmax(0, 1fr);
    }

    .seg button {
      min-height: 40px;
    }
  }
</style>
