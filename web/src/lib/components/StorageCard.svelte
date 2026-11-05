<script lang="ts">
  import type { DiskTrend } from '../metrics'
  import { bytes, fillTone, pct } from '../format'

  let { trends }: { trends: DiskTrend[] | null } = $props()

  const MAX_ROWS = 5

  let rows = $derived(
    (trends ?? [])
      .filter((t) => t.size > 0)
      .sort((a, b) => b.size - a.size)
      .slice(0, MAX_ROWS)
      .map((t) => ({ ...t, ratio: t.used / t.size, tone: fillTone(t.used / t.size) })),
  )
  let total = $derived(rows.reduce((a, r) => ({ used: a.used + r.used, size: a.size + r.size }), { used: 0, size: 0 }))

  function projection(t: DiskTrend): string {
    if (t.daysToFull === undefined) return t.bytesPerDay < 0 ? 'en baisse' : 'stable'
    if (t.daysToFull > 365) return 'plein dans > 1 an'
    return `plein dans ~${Math.max(1, Math.round(t.daysToFull))} j`
  }

  const glyph = { ok: '●', warn: '▲', crit: '■' }
</script>

<article class="card">
  <div class="head">
    <span class="label">Stockage</span>
    <span class="mono label">{rows.length} volume{rows.length > 1 ? 's' : ''}</span>
  </div>
  {#if trends === null}
    <div class="skeleton" aria-busy="true"></div>
  {:else if rows.length === 0}
    <p class="empty">Pas encore de mesure de disque. La première arrive dans la minute.</p>
  {:else}
    <div class="mono total">
      <span class="big">{bytes(total.used)}</span><span class="of">/ {bytes(total.size)}</span>
    </div>
    <ul role="list">
      {#each rows as r (r.host + r.mount)}
        <li>
          <div class="mono row">
            <span class="name" title={r.mount}><span data-tone={r.tone} class="g">{glyph[r.tone]}</span> {r.host}
              <span class="mount">{r.mount}</span></span>
            <span data-tone={r.tone} class="p">{pct(r.ratio * 100)} %</span>
          </div>
          <div class="track" role="img" aria-label="{r.host} {r.mount} : {pct(r.ratio * 100)} % utilisé">
            <div class="fill" data-tone={r.tone} style:width="{Math.min(r.ratio, 1) * 100}%"></div>
            <div class="threshold" aria-hidden="true"></div>
          </div>
          <div class="mono sub">
            <span>{bytes(r.used)} / {bytes(r.size)}</span>
            <span>{projection(r)}</span>
          </div>
        </li>
      {/each}
    </ul>
  {/if}
</article>

<style>
  .card {
    background: var(--surface);
    border: 1px solid var(--line);
    border-radius: var(--radius-panel);
    padding: 24px;
    display: flex;
    flex-direction: column;
    gap: 18px;
    min-width: 0;
  }

  .head {
    display: flex;
    justify-content: space-between;
  }

  .label {
    font-size: 13px;
    color: var(--text-2);
  }

  .total {
    display: flex;
    align-items: baseline;
    gap: 8px;
  }

  .big {
    font-family: var(--font-sans);
    font-size: 48px;
    font-weight: 300;
    letter-spacing: -0.04em;
    line-height: 1;
  }

  .of {
    color: var(--text-2);
  }

  ul {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 16px;
  }

  .row,
  .sub {
    display: flex;
    justify-content: space-between;
    gap: 8px;
    font-size: 12px;
  }

  .row {
    margin-bottom: 8px;
  }

  .sub {
    margin-top: 6px;
    color: var(--text-2);
  }

  .name {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .mount {
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

  .g,
  .p[data-tone='warn'],
  .p[data-tone='crit'] {
    color: var(--tone);
  }

  .track {
    position: relative;
    height: 8px;
    background: var(--fill);
    border-radius: var(--radius-pill);
    overflow: hidden;
  }

  .fill {
    height: 8px;
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
    height: 8px;
    background: var(--text);
    opacity: 0.6;
  }

  .skeleton {
    height: 180px;
    border-radius: var(--radius-control);
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
    color: var(--text-2);
  }
</style>
