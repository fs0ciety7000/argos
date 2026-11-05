<script lang="ts">
  import { onMount } from 'svelte'
  import { Tween } from 'svelte/motion'
  import { cubicOut } from 'svelte/easing'
  import { fly } from 'svelte/transition'
  import { live } from '../live.svelte'
  import { history, storage, throughput, type DiskTrend, type Range, type Throughput } from '../metrics'
  import { bitrate, reducedMotion } from '../format'
  import ThroughputChart from './ThroughputChart.svelte'
  import StorageCard from './StorageCard.svelte'
  import HostCard from './HostCard.svelte'
  import { nav } from '../nav.svelte'
  import type { Container } from '../containers'

  const RANGES: { id: Range; label: string }[] = [
    { id: '1h', label: '1h' },
    { id: '24h', label: '24h' },
    { id: '7d', label: '7j' },
  ]
  const HOUR = 3600

  let range = $state<Range>('1h')
  let tp = $state.raw<Throughput>({ ts: [], rx: [], tx: [] })
  let trends = $state.raw<DiskTrend[] | null>(null)
  /** Last hour of CPU per host, extended by live samples. */
  let cpu = $state.raw<Record<string, { ts: number[]; v: (number | null)[] }>>({})

  async function loadThroughput() {
    try {
      tp = await throughput(range)
    } catch {
      /* keep the previous chart */
    }
  }

  async function loadStorage() {
    try {
      trends = await storage()
    } catch {
      trends ??= []
    }
  }

  async function loadCPU(name: string) {
    try {
      const s = await history(name, '1h')
      cpu = { ...cpu, [name]: { ts: s.ts, v: s.cpu } }
    } catch {
      /* the card still shows live values */
    }
  }

  // Refetch throughput on range change, then periodically.
  $effect(() => {
    range
    loadThroughput()
    const t = setInterval(loadThroughput, range === '1h' ? 30_000 : 300_000)
    return () => clearInterval(t)
  })

  let requested = new Set<string>()
  $effect(() => {
    for (const h of live.hosts ?? []) {
      if (!requested.has(h.host.name)) {
        requested.add(h.host.name)
        loadCPU(h.host.name)
      }
    }
  })

  onMount(() => {
    loadStorage()
    const st = setInterval(loadStorage, 60_000)
    const off = live.onSample((s) => {
      if (s.cpu < 0) return
      const now = Date.parse(s.ts) / 1000
      const cur = cpu[s.host] ?? { ts: [], v: [] }
      let start = 0
      while (start < cur.ts.length && cur.ts[start] < now - HOUR) start++
      cpu = { ...cpu, [s.host]: { ts: [...cur.ts.slice(start), now], v: [...cur.v.slice(start), s.cpu] } }
    })
    return () => {
      clearInterval(st)
      off()
    }
  })

  // Live aggregate rates for the headline numbers.
  let rates = $derived.by(() => {
    let rx = 0
    let tx = 0
    let any = false
    for (const s of Object.values(live.latest)) {
      if (s.rxBps >= 0) {
        rx += s.rxBps
        tx += s.txBps
        any = true
      }
    }
    return any ? { rx, tx } : null
  })
  const duration = reducedMotion() ? 0 : 900
  const rxT = new Tween(0, { duration, easing: cubicOut })
  const txT = new Tween(0, { duration, easing: cubicOut })
  $effect(() => {
    if (rates) {
      rxT.target = rates.rx
      txT.target = rates.tx
    }
  })
  let rxLabel = $derived(bitrate(rates ? rxT.current : null))
  let txLabel = $derived(bitrate(rates ? txT.current : null))

  let hosts = $derived(live.hosts ?? [])

  // Failing containers across Docker hosts, most recent failure first.
  const MAX_FAILING = 6
  let failing = $derived(
    Object.values(live.containers ?? {})
      .flatMap((hc) => hc.containers.filter((c) => c.failing).map((c) => ({ host: hc.host, c })))
      .sort((a, b) => (b.c.finishedAt ?? '').localeCompare(a.c.finishedAt ?? '')),
  )
  let nowTick = $state(Date.now())
  onMount(() => {
    const t = setInterval(() => (nowTick = Date.now()), 30_000)
    return () => clearInterval(t)
  })
  function reason(c: Container): string {
    if (c.state === 'restarting') return '▲ boucle de redémarrage'
    if (c.health === 'unhealthy') return '■ unhealthy'
    return c.oomKilled ? '■ mémoire (OOM)' : `■ arrêté (code ${c.exitCode})`
  }
  function since(c: Container): string {
    const ref = c.state === 'running' ? c.startedAt : c.finishedAt
    if (!ref) return ''
    const s = Math.max(0, Math.round((nowTick - Date.parse(ref)) / 1000))
    if (s < 3600) return `depuis ${Math.max(1, Math.floor(s / 60))} min`
    if (s < 86400) return `depuis ${Math.floor(s / 3600)} h`
    return `depuis ${Math.floor(s / 86400)} j`
  }
  let reachable = $derived(hosts.filter((h) => h.state === 'connected').length)
</script>

<div class="stack">
  <section class="row">
    <article class="card hero" in:fly={{ y: 14, duration: 800, easing: cubicOut }}>
      <div class="grid-bg" aria-hidden="true"></div>
      <div class="hero-head">
        <div>
          <div class="label">Débit agrégé · {reachable}/{hosts.length} hôtes joignables</div>
          <div class="mono headline" aria-live="off">
            <span class="big">{rxLabel.value}</span><span class="u">{rxLabel.unit} ↓</span>
            <span class="small">{txLabel.value}</span><span class="u">{txLabel.unit} ↑</span>
          </div>
        </div>
        <div class="controls">
          <div class="mono legend">
            <span><i class="sw rx"></i>Descendant</span>
            <span><i class="sw tx"></i>Montant</span>
          </div>
          <div class="seg mono" role="group" aria-label="Plage temporelle">
            {#each RANGES as r (r.id)}
              <button type="button" aria-pressed={range === r.id} onclick={() => (range = r.id)}>{r.label}</button>
            {/each}
          </div>
        </div>
      </div>
      {#if tp.ts.length > 1}
        {#key range}
          <ThroughputChart ts={tp.ts} rx={tp.rx} tx={tp.tx} days={range === '7d'} />
        {/key}
      {:else}
        <div class="placeholder mono">Collecte en cours : la courbe apparaît après quelques mesures.</div>
      {/if}
    </article>

    <div class="side" in:fly={{ y: 14, duration: 800, delay: 80, easing: cubicOut }}>
      <StorageCard {trends} />
    </div>
  </section>

  {#if failing.length > 0}
    <section class="card failing" aria-labelledby="failing-title" in:fly={{ y: 10, duration: 600, easing: cubicOut }}>
      <header class="f-head">
        <h3 id="failing-title"><span class="crit">■</span> Conteneurs en échec <span class="mono count">· {failing.length}</span></h3>
        <button type="button" class="link mono" onclick={() => nav.openContainerIssues()}>Voir dans Conteneurs →</button>
      </header>
      <ul role="list">
        {#each failing.slice(0, MAX_FAILING) as f (f.host + f.c.id)}
          <li>
            <span class="mono reason">{reason(f.c)}</span>
            <span class="who"><b>{f.c.name}</b> <span class="mono dim">· {f.host}{f.c.autoRestart ? ' · auto' : ''}</span></span>
            <span class="mono dim since">{since(f.c)}</span>
            <button type="button" class="ghost mono" onclick={() => nav.openLogs(f.host, f.c.name)} aria-label="Journaux de {f.c.name}">Logs</button>
          </li>
        {/each}
      </ul>
      {#if failing.length > MAX_FAILING}
        <p class="mono dim more">+ {failing.length - MAX_FAILING} autre{failing.length - MAX_FAILING > 1 ? 's' : ''}</p>
      {/if}
    </section>
  {/if}

  {#if live.hosts === null}
    <div class="hosts">
      {#each [0, 1, 2] as i (i)}<div class="card skeleton"></div>{/each}
    </div>
  {:else if hosts.length === 0}
    <p class="card empty">Aucun hôte configuré : voir la page « Hôtes ».</p>
  {:else}
    <section class="hosts" aria-label="Hôtes">
      {#each hosts as h, i (h.host.name)}
        <div in:fly={{ y: 14, duration: 800, delay: 160 + i * 80, easing: cubicOut }}>
          <HostCard
            status={h}
            sample={live.latest[h.host.name]}
            cpuHistory={cpu[h.host.name]?.v ?? []}
            containers={live.containers?.[h.host.name]}
          />
        </div>
      {/each}
    </section>
  {/if}
</div>

<style>
  .stack {
    display: flex;
    flex-direction: column;
    gap: 20px;
  }

  .row {
    display: flex;
    flex-wrap: wrap;
    gap: 20px;
  }

  .card {
    background: var(--surface);
    border: 1px solid var(--line);
    border-radius: var(--radius-panel);
  }

  .hero {
    position: relative;
    overflow: hidden;
    flex: 999 1 620px;
    min-width: 0;
    padding: 24px 24px 12px;
    display: flex;
    flex-direction: column;
    gap: 16px;
  }

  .grid-bg {
    position: absolute;
    inset: 0;
    background-image:
      linear-gradient(to right, color-mix(in srgb, var(--text) 4.5%, transparent) 1px, transparent 1px),
      linear-gradient(to bottom, color-mix(in srgb, var(--text) 4.5%, transparent) 1px, transparent 1px);
    background-size: 32px 32px;
    pointer-events: none;
  }

  .hero > :not(.grid-bg) {
    position: relative;
  }

  .side {
    flex: 1 1 340px;
    min-width: 0;
    display: flex;
  }

  .side > :global(*) {
    flex: 1;
  }

  .hero-head {
    display: flex;
    flex-wrap: wrap;
    align-items: flex-end;
    justify-content: space-between;
    gap: 16px 32px;
  }

  .label {
    font-size: 13px;
    color: var(--text-2);
  }

  .headline {
    display: flex;
    align-items: baseline;
    flex-wrap: wrap;
    gap: 4px 10px;
    margin-top: 6px;
  }

  .big {
    font-family: var(--font-sans);
    font-size: 64px;
    font-weight: 300;
    letter-spacing: -0.045em;
    line-height: 1;
    font-variant-numeric: tabular-nums;
  }

  .small {
    margin-left: 12px;
    font-size: 15px;
  }

  .u {
    font-size: 14px;
    color: var(--text-2);
  }

  .controls {
    display: flex;
    flex-direction: column;
    align-items: flex-end;
    gap: 12px;
  }

  .legend {
    display: flex;
    gap: 18px;
    font-size: 12px;
    color: var(--text-2);
  }

  .legend span {
    display: flex;
    align-items: center;
    gap: 6px;
  }

  .sw {
    width: 14px;
    height: 0;
    border-top: 2px solid var(--accent);
  }

  .sw.tx {
    border-top: 2px dashed var(--steel);
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
    min-height: 30px;
    padding: 0 12px;
    background: none;
    color: var(--text-2);
    border: 0;
    border-radius: 7px;
    cursor: pointer;
    transition:
      background var(--dur-fast),
      color var(--dur-fast);
  }

  .seg button:hover {
    color: var(--text);
  }

  .seg button[aria-pressed='true'] {
    background: var(--fill);
    color: var(--text);
  }

  .placeholder {
    height: 220px;
    display: grid;
    place-items: center;
    font-size: 12px;
    color: var(--text-2);
  }

  .failing {
    padding: 18px 20px;
    border-color: color-mix(in srgb, var(--crit) 30%, transparent);
  }

  .f-head {
    display: flex;
    flex-wrap: wrap;
    justify-content: space-between;
    align-items: center;
    gap: 8px 16px;
    margin-bottom: 10px;
  }

  .f-head h3 {
    margin: 0;
    font-size: 15px;
    font-weight: 500;
  }

  .crit {
    color: var(--crit);
  }

  .count,
  .dim {
    color: var(--text-2);
    font-weight: 400;
  }

  .link {
    min-height: 32px;
    padding: 0;
    font-size: 12px;
    color: var(--accent);
    background: none;
    border: 0;
    cursor: pointer;
  }

  .failing ul {
    list-style: none;
    margin: 0;
    padding: 0;
  }

  .failing li {
    display: grid;
    grid-template-columns: minmax(150px, auto) minmax(0, 1fr) auto auto;
    align-items: center;
    gap: 6px 16px;
    padding: 8px 0;
    border-top: 1px solid var(--line);
    font-size: 13px;
  }

  .reason {
    font-size: 12px;
    color: var(--crit);
    white-space: nowrap;
  }

  .who {
    min-width: 0;
    overflow-wrap: anywhere;
  }

  .who b {
    font-weight: 500;
  }

  .since {
    font-size: 12px;
  }

  .ghost {
    min-height: 32px;
    padding: 0 12px;
    font-size: 12px;
    background: var(--fill);
    border: 1px solid var(--line-strong);
    border-radius: var(--radius-control);
    cursor: pointer;
  }

  .more {
    margin: 8px 0 0;
    font-size: 12px;
  }

  @media (max-width: 720px) {
    .failing li {
      grid-template-columns: minmax(0, 1fr) auto;
    }

    .ghost {
      min-height: 44px;
    }
  }

  .hosts {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(300px, 1fr));
    gap: 20px;
  }

  .hosts > :global(div) {
    display: flex;
  }

  .hosts > :global(div > *) {
    flex: 1;
  }

  .skeleton {
    min-height: 230px;
    background: linear-gradient(90deg, var(--surface) 0%, var(--fill) 50%, var(--surface) 100%);
    background-size: 200% 100%;
    animation: shimmer 1.6s ease-in-out infinite;
  }

  @keyframes shimmer {
    to {
      background-position: -200% 0;
    }
  }

  .empty {
    margin: 0;
    padding: 24px;
    color: var(--text-2);
  }

  @media (max-width: 720px) {
    .big {
      font-size: 48px;
    }

    .controls {
      align-items: flex-start;
    }

    .seg button {
      min-height: 40px;
    }
  }
</style>
