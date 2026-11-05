<script lang="ts">
  import { Tween } from 'svelte/motion'
  import { cubicOut } from 'svelte/easing'
  import type { HostStatus } from '../hosts'
  import type { Sample } from '../live.svelte'
  import { bitrate, bytes, fillTone, pct, reducedMotion } from '../format'
  import Sparkline from './Sparkline.svelte'

  let { status, sample, cpuHistory }: { status: HostStatus; sample?: Sample; cpuHistory: (number | null)[] } =
    $props()

  const duration = reducedMotion() ? 0 : 800
  const cpu = new Tween(0, { duration, easing: cubicOut })
  const ram = new Tween(0, { duration, easing: cubicOut })

  let connected = $derived(status.state === 'connected')
  let memRatio = $derived(sample && sample.memTotal > 0 ? sample.memUsed / sample.memTotal : 0)
  let disk = $derived.by(() => {
    const ds = sample?.disks ?? []
    if (ds.length === 0) return null
    return ds.reduce((a, b) => (b.size > a.size ? b : a))
  })
  let diskRatio = $derived(disk ? disk.used / disk.size : 0)
  let rx = $derived(bitrate(sample?.rxBps))
  let tx = $derived(bitrate(sample?.txBps))

  // Keep the last known CPU when a reading is unknown (counter reset).
  let hasCPU = $state(false)
  $effect(() => {
    if (sample && sample.cpu >= 0) {
      cpu.target = sample.cpu
      hasCPU = true
    }
  })
  $effect(() => {
    ram.target = memRatio * 100
  })

  type Pill = { tone: string; text: string }
  let pill = $derived.by((): Pill => {
    switch (status.state) {
      case 'connected':
        if (diskRatio >= 0.95) return { tone: 'crit', text: `■ disque ${pct(diskRatio * 100)} %` }
        if (diskRatio >= 0.85) return { tone: 'warn', text: `▲ disque ${pct(diskRatio * 100)} %` }
        return { tone: 'ok', text: '● nominal' }
      case 'connecting':
        return { tone: 'unknown', text: '◌ connexion…' }
      case 'hostkey_changed':
        return { tone: 'warn', text: '▲ clé d’hôte modifiée' }
      default:
        return { tone: 'crit', text: '■ injoignable' }
    }
  })
</script>

<article class="card" class:sunk={!connected} data-tone={pill.tone}>
  <header>
    <div>
      <h3>{status.host.name}</h3>
      <p class="mono meta">
        {status.host.kind === 'vps' ? 'VPS' : 'Seedbox · machine partagée'}{status.host.docker ? ' · docker' : ''}
      </p>
    </div>
    <span class="pill mono" data-tone={pill.tone}>{pill.text}</span>
  </header>

  <div class="main" class:dim={!connected}>
    <div>
      <div class="label">CPU</div>
      <div class="big mono">
        {hasCPU ? pct(cpu.current) : '—'}<span class="unit"> %</span>
      </div>
    </div>
    <div class="spark">
      <Sparkline values={cpuHistory} label="CPU de {status.host.name}, dernière heure" tone={connected ? 'accent' : 'muted'} />
    </div>
  </div>

  <div class="bars" class:dim={!connected}>
    <div>
      <div class="mono row"><span>RAM</span><span class="v">{sample ? `${pct(ram.current)} %` : '—'}</span></div>
      <div class="track"><div class="fill" style:width="{ram.current}%"></div></div>
    </div>
    <div>
      <div class="mono row">
        <span>Disque</span><span
          class="v"
          data-tone={fillTone(diskRatio)}
          title={disk ? `${disk.mount === 'quota' ? 'quota du compte' : disk.mount} : ${bytes(disk.used)} / ${bytes(disk.size)}` : undefined}
          >{disk ? `${pct(diskRatio * 100)} %` : '—'}</span
        >
      </div>
      <div class="track">
        <div class="fill" data-tone={fillTone(diskRatio)} style:width="{diskRatio * 100}%"></div>
        <div class="threshold" aria-hidden="true"></div>
      </div>
    </div>
  </div>

  <p class="mono net" class:dim={!connected}>
    <span>↓ <b>{rx.value}</b> {rx.unit}</span>
    <span>↑ <b>{tx.value}</b> {tx.unit}</span>
    {#if sample}<span>charge <b>{sample.load1.toFixed(2)}</b></span>{/if}
  </p>
  {#if !connected && status.error}
    <p class="mono error">{status.error}</p>
  {/if}
</article>

<style>
  .card {
    background: var(--surface);
    border: 1px solid var(--line);
    border-radius: var(--radius-panel);
    padding: 20px;
    display: flex;
    flex-direction: column;
    gap: 16px;
    min-width: 0;
    transition:
      border-color 350ms var(--ease-out),
      transform 350ms var(--ease-out);
  }

  .card:hover {
    border-color: var(--line-strong);
    transform: translateY(-2px);
  }

  .card[data-tone='warn'] {
    border-color: color-mix(in srgb, var(--warn) 30%, transparent);
  }

  .card[data-tone='crit'] {
    border-color: color-mix(in srgb, var(--crit) 25%, transparent);
  }

  .sunk {
    background: var(--surface-sunk);
    border-style: dashed;
  }

  header {
    display: flex;
    justify-content: space-between;
    align-items: flex-start;
    gap: 8px;
  }

  h3 {
    margin: 0;
    font-size: 15px;
    font-weight: 500;
  }

  .meta {
    margin: 2px 0 0;
    font-size: 12px;
    color: var(--text-2);
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
  [data-tone='unknown'] {
    --tone: var(--unknown);
  }

  .pill {
    flex-shrink: 0;
    font-size: 12px;
    padding: 3px 10px;
    border-radius: var(--radius-pill);
    color: var(--tone);
    background: color-mix(in srgb, var(--tone) 8%, transparent);
    border: 1px solid color-mix(in srgb, var(--tone) 20%, transparent);
    white-space: nowrap;
  }

  .main {
    display: flex;
    align-items: flex-end;
    justify-content: space-between;
    gap: 12px;
  }

  .spark {
    width: 55%;
  }

  .label {
    font-size: 12px;
    color: var(--text-2);
  }

  .big {
    font-family: var(--font-sans);
    font-size: 40px;
    font-weight: 300;
    letter-spacing: -0.04em;
    line-height: 1.05;
    font-variant-numeric: tabular-nums;
  }

  .unit {
    font-size: 15px;
    color: var(--text-2);
    letter-spacing: 0;
  }

  .bars {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 16px;
  }

  .row {
    display: flex;
    justify-content: space-between;
    gap: 8px;
    font-size: 12px;
    color: var(--text-2);
    margin-bottom: 6px;
  }

  .v {
    color: var(--text);
    white-space: nowrap;
  }

  .v[data-tone='warn'],
  .v[data-tone='crit'] {
    color: var(--tone);
  }

  .track {
    position: relative;
    height: 4px;
    background: var(--fill);
    border-radius: var(--radius-pill);
  }

  .fill {
    height: 4px;
    max-width: 100%;
    background: var(--steel);
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
    top: -3px;
    width: 1px;
    height: 10px;
    background: var(--text-2);
  }

  .net {
    margin: 0;
    display: flex;
    flex-wrap: wrap;
    gap: 6px 18px;
    font-size: 12px;
    color: var(--text-2);
  }

  .net b {
    color: var(--text);
    font-weight: 400;
  }

  .dim {
    opacity: 0.45;
  }

  .error {
    margin: 0;
    font-size: 12px;
    color: var(--crit);
    overflow-wrap: anywhere;
  }
</style>
