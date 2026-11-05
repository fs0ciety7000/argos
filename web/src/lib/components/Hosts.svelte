<script lang="ts">
  import { onMount } from 'svelte'
  import { fly } from 'svelte/transition'
  import { cubicOut } from 'svelte/easing'
  import { ApiError } from '../api'
  import { auth } from '../auth.svelte'
  import {
    getPublicKey,
    listHosts,
    reconnectHost,
    STALE_AFTER_MS,
    type HostStatus,
    type PublicKey,
  } from '../hosts'

  import { live } from '../live.svelte'

  const POLL_MS = 5000

  // Live WebSocket first; REST polling only while the socket is down.
  let polled = $state.raw<HostStatus[] | null>(null)
  let hosts = $derived(live.state === 'open' && live.hosts ? live.hosts : (polled ?? live.hosts))
  let key = $state.raw<PublicKey | null>(null)
  let loadError = $state<string | null>(null)
  let now = $state(Date.now())
  let pending = $state<Record<string, boolean>>({})
  let copied = $state(false)

  async function refresh() {
    try {
      polled = await listHosts()
      loadError = null
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        await auth.check()
        return
      }
      loadError = 'Impossible de charger les hôtes.'
    }
  }

  onMount(() => {
    refresh()
    getPublicKey()
      .then((k) => (key = k))
      .catch(() => {})
    const poll = setInterval(() => {
      if (live.state !== 'open') refresh()
    }, POLL_MS)
    const tick = setInterval(() => (now = Date.now()), 1000)
    return () => {
      clearInterval(poll)
      clearInterval(tick)
    }
  })

  let notice = $state<string | null>(null)

  async function reconnect(name: string) {
    pending[name] = true
    notice = null
    try {
      await reconnectHost(name)
      if (live.state !== 'open') setTimeout(refresh, 800)
    } catch (err) {
      notice =
        err instanceof ApiError && err.status === 429
          ? `Reconnexion déjà demandée pour ${name}, patiente quelques secondes.`
          : `Reconnexion de ${name} impossible.`
    } finally {
      setTimeout(() => (pending[name] = false), 800)
    }
  }

  async function copyKey() {
    if (!key) return
    await navigator.clipboard.writeText(key.publicKey)
    copied = true
    setTimeout(() => (copied = false), 1600)
  }

  type View = { tone: 'ok' | 'warn' | 'crit' | 'unknown'; glyph: string; label: string }

  function view(s: HostStatus): View {
    switch (s.state) {
      case 'connected': {
        const age = s.lastSeen ? now - Date.parse(s.lastSeen) : 0
        if (age > STALE_AFTER_MS) {
          return { tone: 'unknown', glyph: '◌', label: `figé ${Math.round(age / 1000)} s` }
        }
        const ms = s.latencyMs !== undefined ? ` · ${s.latencyMs.toFixed(0)} ms` : ''
        return { tone: 'ok', glyph: '●', label: `connecté${ms}` }
      }
      case 'connecting':
        return { tone: 'unknown', glyph: '◌', label: 'connexion…' }
      case 'hostkey_changed':
        return { tone: 'warn', glyph: '▲', label: 'clé d’hôte modifiée' }
      default:
        return { tone: 'crit', glyph: '■', label: 'injoignable' }
    }
  }

  function since(iso?: string): string {
    if (!iso) return '—'
    const s = Math.max(0, Math.round((now - Date.parse(iso)) / 1000))
    if (s < 60) return `${s} s`
    if (s < 3600) return `${Math.floor(s / 60)} min`
    if (s < 86400) return `${Math.floor(s / 3600)} h ${Math.floor((s % 3600) / 60)} min`
    return `${Math.floor(s / 86400)} j ${Math.floor((s % 86400) / 3600)} h`
  }

  let counts = $derived.by(() => {
    const c = { ok: 0, issue: 0 }
    for (const h of hosts ?? []) {
      if (view(h).tone === 'ok') c.ok++
      else c.issue++
    }
    return c
  })
</script>

<section class="stack" aria-labelledby="hosts-title">
  <header class="head">
    <div>
      <h2 id="hosts-title">Hôtes</h2>
      {#if hosts && hosts.length > 0}
        <p class="mono sub">
          <span class="ok">● {counts.ok} connecté{counts.ok > 1 ? 's' : ''}</span>
          {#if counts.issue > 0}<span class="crit"> · ■ {counts.issue} à vérifier</span>{/if}
        </p>
      {/if}
    </div>
  </header>

  {#if loadError}
    <p class="banner mono" role="alert">■ {loadError}</p>
  {/if}
  {#if notice}
    <p class="banner mono" role="status">{notice}</p>
  {/if}

  {#if hosts === null}
    <div class="grid" aria-busy="true">
      {#each [0, 1, 2] as i (i)}<div class="card skeleton"></div>{/each}
    </div>
  {:else if hosts.length === 0}
    <div class="card empty">
      <h3>Aucun hôte configuré</h3>
      <p>Ajoute la variable <code class="mono">ARGOS_HOSTS</code> dans Coolify puis redéploie :</p>
      <pre class="mono">[
  &#123;"name":"netcup-01","kind":"vps","address":"152.53.176.13","user":"argos"&#125;,
  &#123;"name":"hbd","kind":"vps","address":"85.17.179.101","user":"argos"&#125;,
  &#123;"name":"whatbox","kind":"seedbox","address":"…","user":"…"&#125;
]</pre>
    </div>
  {:else}
    <ul class="grid" role="list">
      {#each hosts as s, i (s.host.name)}
        {@const v = view(s)}
        <li
          class="card host"
          class:sunk={v.tone === 'unknown' && s.state !== 'connecting'}
          data-tone={v.tone}
          in:fly={{ y: 12, duration: 700, delay: i * 80, easing: cubicOut }}
        >
          <div class="row">
            <div class="id">
              <h3>{s.host.name}</h3>
              <p class="mono meta">
                {s.host.kind === 'vps' ? 'VPS' : 'Seedbox · lecture seule'}{s.host.docker ? ' · docker' : ''}
              </p>
            </div>
            <span class="pill mono" data-tone={v.tone}>{v.glyph} {v.label}</span>
          </div>

          <dl class="mono facts">
            <div><dt>accès</dt><dd>{s.host.user}@{s.host.address}:{s.host.port}</dd></div>
            <div><dt>connecté depuis</dt><dd>{since(s.connectedSince)}</dd></div>
            <div><dt>dernier contact</dt><dd>{s.lastSeen ? `il y a ${since(s.lastSeen)}` : '—'}</dd></div>
          </dl>

          {#if s.error}
            <p class="error mono" data-tone={v.tone}>{s.error}</p>
          {/if}
          {#if s.state === 'hostkey_changed'}
            <p class="hint">
              Si le serveur a été réinstallé, retire sa ligne de <code class="mono">/data/ssh/known_hosts</code>,
              puis reconnecte. Sinon, ne fais rien : quelqu’un se fait peut-être passer pour lui.
            </p>
          {/if}

          {#if s.state !== 'connected'}
            <button type="button" class="ghost" onclick={() => reconnect(s.host.name)} disabled={pending[s.host.name]}>
              {pending[s.host.name] ? 'Reconnexion…' : 'Reconnecter'}
            </button>
          {/if}
          {#if s.hostKey}
            <p class="mono fp" title="Empreinte de la clé d’hôte">{s.hostKey}</p>
          {/if}
        </li>
      {/each}
    </ul>
  {/if}

  {#if key}
    <section class="card key" aria-labelledby="key-title">
      <div class="row">
        <div>
          <h3 id="key-title">Clé publique d’Argos</h3>
          <p class="meta mono">{key.fingerprint}</p>
        </div>
        <button type="button" class="primary" onclick={copyKey}>{copied ? 'Copiée ✓' : 'Copier'}</button>
      </div>
      <p class="hint">
        À ajouter sur chaque hôte, dans <code class="mono">~/.ssh/authorized_keys</code> de l’utilisateur configuré.
        L’option <code class="mono">restrict</code> interdit tunnels, transferts et terminal : Argos n’exécute que des
        commandes nommées, et une clé volée ne pourrait pas ouvrir de tunnel.
      </p>
      <pre class="mono">{key.publicKey}</pre>
    </section>
  {/if}
</section>

<style>
  .stack {
    display: flex;
    flex-direction: column;
    gap: 20px;
  }

  h2 {
    margin: 0;
    font-size: 40px;
    font-weight: 300;
    letter-spacing: -0.04em;
    line-height: 1.1;
  }

  .sub {
    margin: 6px 0 0;
    font-size: 12px;
  }

  h3 {
    margin: 0;
    font-size: 15px;
    font-weight: 500;
  }

  .grid {
    list-style: none;
    margin: 0;
    padding: 0;
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(320px, 1fr));
    gap: 20px;
  }

  .card {
    background: var(--surface);
    border: 1px solid var(--line);
    border-radius: var(--radius-panel);
    padding: 20px;
    display: flex;
    flex-direction: column;
    gap: 16px;
    transition:
      border-color 350ms var(--ease-out),
      transform 350ms var(--ease-out);
  }

  .host:hover {
    border-color: var(--line-strong);
    transform: translateY(-2px);
  }

  .host[data-tone='warn'] {
    border-color: color-mix(in srgb, var(--warn) 30%, transparent);
  }

  .host[data-tone='crit'] {
    border-color: color-mix(in srgb, var(--crit) 25%, transparent);
  }

  .sunk {
    background: var(--surface-sunk);
    border-style: dashed;
  }

  .skeleton {
    min-height: 190px;
    background: linear-gradient(90deg, var(--surface) 0%, var(--fill) 50%, var(--surface) 100%);
    background-size: 200% 100%;
    animation: shimmer 1.6s ease-in-out infinite;
  }

  @keyframes shimmer {
    to {
      background-position: -200% 0;
    }
  }

  .row {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 12px;
  }

  .meta {
    margin: 2px 0 0;
    font-size: 12px;
    color: var(--text-2);
  }

  .pill {
    flex-shrink: 0;
    font-size: 12px;
    padding: 3px 10px;
    border-radius: var(--radius-pill);
    border: 1px solid;
    white-space: nowrap;
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

  .pill,
  .error {
    color: var(--tone);
    background: color-mix(in srgb, var(--tone) 8%, transparent);
    border-color: color-mix(in srgb, var(--tone) 20%, transparent);
  }

  .facts {
    margin: 0;
    display: grid;
    gap: 6px;
    font-size: 12px;
  }

  .facts div {
    display: flex;
    justify-content: space-between;
    gap: 12px;
  }

  .facts dt {
    color: var(--text-2);
  }

  .facts dd {
    margin: 0;
    text-align: right;
    overflow-wrap: anywhere;
  }

  .error {
    margin: 0;
    padding: 10px 12px;
    font-size: 12px;
    border: 1px solid;
    border-radius: var(--radius-control);
    overflow-wrap: anywhere;
  }

  .hint {
    margin: 0;
    font-size: 13px;
    color: var(--text-2);
  }

  .fp {
    margin: 0;
    font-size: 11px;
    color: var(--text-2);
    overflow-wrap: anywhere;
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

  pre {
    margin: 0;
    padding: 14px 16px;
    font-size: 12px;
    line-height: 1.7;
    background: var(--surface-sunk);
    border: 1px solid var(--line);
    border-radius: var(--radius-control);
    overflow-x: auto;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }

  code {
    font-size: 0.92em;
  }

  .empty p {
    margin: 0;
    color: var(--text-2);
  }

  button {
    min-height: 40px;
    padding: 0 14px;
    border-radius: var(--radius-control);
    cursor: pointer;
    font-weight: 500;
    transition:
      border-color var(--dur-fast),
      filter var(--dur-fast),
      transform var(--dur-fast) var(--ease-out);
  }

  button:active:not(:disabled) {
    transform: scale(0.985);
  }

  button:disabled {
    opacity: 0.5;
    cursor: progress;
  }

  .ghost {
    align-self: flex-start;
    background: var(--fill);
    border: 1px solid var(--line-strong);
  }

  .ghost:hover:not(:disabled) {
    border-color: var(--steel);
  }

  .primary {
    flex-shrink: 0;
    color: var(--on-accent);
    background: var(--accent);
    border: 0;
  }

  .primary:hover {
    filter: brightness(1.08);
  }

  .ok {
    color: var(--ok);
  }

  .crit {
    color: var(--crit);
  }

  @media (max-width: 720px) {
    h2 {
      font-size: 32px;
    }

    button {
      min-height: 44px;
    }
  }
</style>
