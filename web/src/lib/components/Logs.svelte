<script lang="ts">
  import { onMount, tick, untrack } from 'svelte'
  import { fade } from 'svelte/transition'
  import { live } from '../live.svelte'
  import { nav } from '../nav.svelte'
  import { LogStream, MAX_LINES, type LogLine } from '../logs.svelte'

  const stream = new LogStream()

  // Deep link from the Containers page (read once at mount), else the first host.
  const target = nav.logTarget
  nav.logTarget = null
  let hostChoice = $state(target?.host ?? '')
  let container = $state(target?.container ?? '')
  let filter = $state('')
  let follow = $state(true)
  let copied = $state(false)
  let viewer = $state<HTMLDivElement>()

  let hosts = $derived(Object.keys(live.containers ?? {}).sort())
  let host = $derived(hostChoice || hosts[0] || '')
  let containers = $derived(
    [...(live.containers?.[host]?.containers ?? [])].sort(
      (a, b) => Number(b.state === 'running') - Number(a.state === 'running') || a.name.localeCompare(b.name),
    ),
  )

  function start() {
    if (host && container) {
      follow = true
      stream.open(host, container)
    }
  }

  // (Re)open the stream when the selection changes.
  $effect(() => {
    const h = host
    const c = container
    untrack(() => (h && c ? start() : stream.close()))
  })

  onMount(() => () => stream.close())

  let shown = $derived.by(() => {
    const q = filter.trim().toLowerCase()
    return q ? stream.lines.filter((l) => l.text.toLowerCase().includes(q)) : stream.lines
  })

  // Stick to the bottom while following.
  $effect(() => {
    shown.length
    if (follow && viewer) {
      tick().then(() => viewer && (viewer.scrollTop = viewer.scrollHeight))
    }
  })

  function onScroll() {
    if (!viewer) return
    follow = viewer.scrollHeight - viewer.scrollTop - viewer.clientHeight < 24
  }

  function resume() {
    follow = true
    if (viewer) viewer.scrollTop = viewer.scrollHeight
  }

  const timeFmt = new Intl.DateTimeFormat('fr-FR', { hour: '2-digit', minute: '2-digit', second: '2-digit' })
  function time(ts?: string): string {
    if (!ts) return ''
    const d = new Date(ts)
    return Number.isNaN(d.getTime()) ? '' : timeFmt.format(d)
  }

  const CRIT = /\b(error|err|fatal|crit(ical)?|panic|exception|traceback)\b/i
  const WARN = /\b(warn(ing)?|wrn)\b/i
  function level(l: LogLine): 'crit' | 'warn' | '' {
    return CRIT.test(l.text) ? 'crit' : WARN.test(l.text) ? 'warn' : ''
  }

  /** Split text around filter matches, for highlighting (no {@html}). */
  function parts(text: string): { t: string; hit: boolean }[] {
    const q = filter.trim().toLowerCase()
    if (!q) return [{ t: text, hit: false }]
    const out: { t: string; hit: boolean }[] = []
    const low = text.toLowerCase()
    let i = 0
    while (i < text.length) {
      const j = low.indexOf(q, i)
      if (j < 0) {
        out.push({ t: text.slice(i), hit: false })
        break
      }
      if (j > i) out.push({ t: text.slice(i, j), hit: false })
      out.push({ t: text.slice(j, j + q.length), hit: true })
      i = j + q.length
    }
    return out
  }

  async function copy() {
    await navigator.clipboard.writeText(shown.map((l) => (l.ts ? `${l.ts} ${l.text}` : l.text)).join('\n'))
    copied = true
    setTimeout(() => (copied = false), 1500)
  }

  const REASONS: Record<string, string> = {
    ended: 'flux terminé (conteneur arrêté ou logs fermés)',
    timeout: 'durée maximale d’une heure atteinte',
    'not-connected': 'hôte non connecté',
    error: 'erreur côté serveur (script argos-docker à jour ?)',
    session: 'session expirée',
    refused: 'connexion refusée (conteneur introuvable ou trop de flux ouverts)',
    closed: 'connexion fermée',
  }

  let status = $derived.by(() => {
    switch (stream.state) {
      case 'live':
        return { tone: 'ok', text: stream.paused ? '▲ en pause' : '● en direct' }
      case 'connecting':
        return { tone: 'unknown', text: '◌ connexion…' }
      case 'ended':
        return { tone: 'unknown', text: `◌ ${REASONS[stream.reason ?? ''] ?? 'flux terminé'}` }
      case 'error':
        return { tone: 'crit', text: `■ ${REASONS[stream.reason ?? ''] ?? 'erreur'}` }
      default:
        return { tone: 'unknown', text: '◌ choisis un conteneur' }
    }
  })
</script>

<section class="stack" aria-labelledby="logs-title">
  <header class="head">
    <h2 id="logs-title">Journaux</h2>
    <div class="pickers">
      <label>
        <span class="mono lbl">hôte</span>
        <select
          value={host}
          onchange={(e) => {
            hostChoice = e.currentTarget.value
            container = ''
          }}
        >
          {#each hosts as h (h)}<option value={h}>{h}</option>{/each}
        </select>
      </label>
      <label>
        <span class="mono lbl">conteneur</span>
        <select bind:value={container} disabled={containers.length === 0}>
          <option value="" disabled>{containers.length ? 'Choisir…' : 'aucun'}</option>
          {#each containers as c (c.id)}
            <option value={c.name}>{c.state === 'running' ? '●' : '◌'} {c.name}</option>
          {/each}
        </select>
      </label>
    </div>
  </header>

  {#if live.containers !== null && hosts.length === 0}
    <div class="card empty">
      <h3>Aucun hôte Docker</h3>
      <p>Les journaux sont disponibles sur les VPS où Docker est activé (voir DEPLOY.md).</p>
    </div>
  {:else}
    <article class="card term">
      <div class="toolbar">
        <span class="pill mono" data-tone={status.tone}>{status.text}</span>
        <span class="mono counts">
          {stream.lines.length.toLocaleString('fr-FR')}{stream.lines.length >= MAX_LINES ? ' (max)' : ''} lignes
          {#if filter}· {shown.length.toLocaleString('fr-FR')} affichées{/if}
          {#if stream.dropped > 0}· <span class="warn">{stream.dropped.toLocaleString('fr-FR')} perdues</span>{/if}
          {#if stream.pendingCount > 0}· {stream.pendingCount} en attente{/if}
        </span>
        <label class="search">
          <span class="visually-hidden">Filtrer les lignes</span>
          <input type="search" placeholder="Filtrer…" bind:value={filter} />
        </label>
        <div class="buttons">
          {#if stream.state === 'ended' || stream.state === 'error'}
            <button type="button" class="primary" onclick={start}>Reprendre le flux</button>
          {:else}
            <button type="button" onclick={() => stream.setPaused(!stream.paused)} disabled={stream.state !== 'live'}>
              {stream.paused ? 'Reprendre' : 'Pause'}
            </button>
          {/if}
          <button type="button" onclick={() => stream.clear()}>Effacer</button>
          <button type="button" onclick={copy} disabled={shown.length === 0}>{copied ? 'Copié ✓' : 'Copier'}</button>
        </div>
      </div>

      <div
        class="viewer mono"
        bind:this={viewer}
        onscroll={onScroll}
        role="log"
        aria-live="off"
        aria-label="Journal de {container || 'aucun conteneur'}"
      >
        {#if shown.length === 0}
          <p class="placeholder">
            {stream.state === 'connecting' ? 'Connexion au flux…' : container ? 'Aucune ligne pour l’instant.' : 'Choisis un hôte et un conteneur.'}
          </p>
        {:else}
          {#each shown as l, i (i)}
            {@const lv = level(l)}
            <div class="line" data-level={lv}>
              <span class="ts">{time(l.ts)}</span>
              <span class="lv" aria-hidden="true">{lv === 'crit' ? '■' : lv === 'warn' ? '▲' : ''}</span>
              <span class="tx"
                >{#each parts(l.text) as p, k (k)}{#if p.hit}<mark>{p.t}</mark>{:else}{p.t}{/if}{/each}{#if l.truncated}<span
                    class="cut"> … (ligne tronquée)</span
                  >{/if}</span
              >
            </div>
          {/each}
        {/if}
      </div>
      {#if !follow && shown.length > 0}
        <button type="button" class="jump" onclick={resume} transition:fade={{ duration: 150 }}>↓ Revenir en direct</button>
      {/if}
    </article>
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
    gap: 12px 24px;
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

  .pickers {
    display: flex;
    flex-wrap: wrap;
    gap: 12px;
  }

  .pickers label {
    display: flex;
    flex-direction: column;
    gap: 4px;
  }

  .lbl {
    font-size: 11px;
    color: var(--text-2);
  }

  select,
  .search input {
    height: 40px;
    padding: 0 12px;
    background: var(--surface-sunk);
    border: 1px solid var(--line-strong);
    border-radius: var(--radius-control);
  }

  select {
    min-width: 180px;
    max-width: 320px;
  }

  select:focus-visible,
  .search input:focus-visible {
    outline: none;
    border-color: var(--accent);
  }

  .card {
    background: var(--surface);
    border: 1px solid var(--line);
    border-radius: var(--radius-panel);
    padding: 20px;
    min-width: 0;
  }

  .empty p {
    margin: 8px 0 0;
    color: var(--text-2);
  }

  .term {
    position: relative;
    padding: 0;
    overflow: hidden;
    display: flex;
    flex-direction: column;
  }

  .toolbar {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 10px 16px;
    padding: 14px 16px;
    border-bottom: 1px solid var(--line);
  }

  .counts {
    font-size: 12px;
    color: var(--text-2);
    flex-grow: 1;
  }

  .warn {
    color: var(--warn);
  }

  .search input {
    width: 200px;
    height: 36px;
  }

  .buttons {
    display: flex;
    gap: 8px;
  }

  button {
    min-height: 36px;
    padding: 0 12px;
    font-weight: 500;
    background: var(--fill);
    border: 1px solid var(--line-strong);
    border-radius: var(--radius-control);
    cursor: pointer;
    transition:
      border-color var(--dur-fast),
      filter var(--dur-fast);
  }

  button:hover:not(:disabled) {
    border-color: var(--steel);
  }

  button:disabled {
    opacity: 0.5;
    cursor: default;
  }

  button.primary {
    color: var(--on-accent);
    background: var(--accent);
    border-color: transparent;
  }

  [data-tone='ok'] {
    --tone: var(--ok);
  }
  [data-tone='crit'] {
    --tone: var(--crit);
  }
  [data-tone='unknown'] {
    --tone: var(--unknown);
  }

  .pill {
    font-size: 12px;
    padding: 3px 10px;
    border-radius: var(--radius-pill);
    color: var(--tone);
    background: color-mix(in srgb, var(--tone) 8%, transparent);
    border: 1px solid color-mix(in srgb, var(--tone) 20%, transparent);
    white-space: nowrap;
    max-width: 100%;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .viewer {
    height: min(68vh, 760px);
    overflow: auto;
    padding: 12px 0;
    background: var(--surface-sunk);
    font-size: 12.5px;
    line-height: 1.7;
  }

  .viewer:focus-visible {
    outline: 2px solid var(--text);
    outline-offset: -2px;
  }

  .placeholder {
    margin: 0;
    padding: 24px 16px;
    color: var(--text-2);
  }

  .line {
    display: grid;
    grid-template-columns: 64px 14px minmax(0, 1fr);
    gap: 8px;
    padding: 0 16px;
    content-visibility: auto;
    contain-intrinsic-size: auto 21px;
  }

  .line:hover {
    background: color-mix(in srgb, var(--text) 4%, transparent);
  }

  .ts {
    color: var(--text-2);
    white-space: nowrap;
  }

  .lv {
    color: var(--tone, transparent);
  }

  .line[data-level='crit'] {
    --tone: var(--crit);
  }

  .line[data-level='warn'] {
    --tone: var(--warn);
  }

  .line[data-level='crit'] .tx {
    color: color-mix(in srgb, var(--crit) 75%, var(--text));
  }

  .tx {
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }

  mark {
    color: var(--on-accent);
    background: var(--accent);
    border-radius: 2px;
  }

  .cut {
    color: var(--text-2);
  }

  .jump {
    position: absolute;
    right: 20px;
    bottom: 18px;
    color: var(--on-accent);
    background: var(--accent);
    border-color: transparent;
    box-shadow: 0 0 0 4px var(--surface-sunk);
  }

  @media (max-width: 720px) {
    h2 {
      font-size: 32px;
    }

    select {
      min-width: 0;
      width: 100%;
    }

    .pickers,
    .pickers label {
      width: 100%;
    }

    .search,
    .search input {
      width: 100%;
    }

    .line {
      grid-template-columns: 56px minmax(0, 1fr);
    }

    .lv {
      display: none;
    }

    button {
      min-height: 44px;
    }
  }
</style>
