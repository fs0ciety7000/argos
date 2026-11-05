<script lang="ts">
  import { onMount } from 'svelte'
  import { fly, fade } from 'svelte/transition'
  import { cubicOut } from 'svelte/easing'
  import { ApiError } from '../api'
  import { live } from '../live.svelte'
  import { listActions, restartContainer, type Action, type Container } from '../containers'
  import { bytes, pct } from '../format'
  import { nav } from '../nav.svelte'

  let filter = $state('')
  let onlyIssues = $state(false)
  let actions = $state.raw<Action[]>([])
  let now = $state(Date.now())
  let notice = $state<{ tone: 'ok' | 'crit'; text: string } | null>(null)

  let dialog = $state<HTMLDialogElement>()
  let target = $state<{ host: string; c: Container } | null>(null)

  async function loadActions() {
    try {
      actions = await listActions()
    } catch {
      /* journal is secondary */
    }
  }

  onMount(() => {
    loadActions()
    const j = setInterval(loadActions, 15_000)
    const t = setInterval(() => (now = Date.now()), 1000)
    return () => {
      clearInterval(j)
      clearInterval(t)
    }
  })

  let hosts = $derived(Object.values(live.containers ?? {}).sort((a, b) => a.host.localeCompare(b.host)))

  function visible(list: Container[]): Container[] {
    const q = filter.trim().toLowerCase()
    return list
      .filter((c) => !onlyIssues || c.failing || c.state !== 'running')
      .filter((c) => !q || c.name.toLowerCase().includes(q) || c.image.toLowerCase().includes(q) || c.app?.toLowerCase().includes(q))
      .sort((a, b) => Number(b.failing) - Number(a.failing) || a.name.localeCompare(b.name))
  }

  type Pill = { tone: 'ok' | 'warn' | 'crit' | 'unknown'; text: string }
  function pill(c: Container): Pill {
    if (c.state === 'restarting') return { tone: 'warn', text: '▲ redémarrage' }
    if (c.health === 'unhealthy') return { tone: 'crit', text: '■ unhealthy' }
    if (c.state === 'running') {
      return c.health === 'starting' ? { tone: 'warn', text: '▲ démarrage' } : { tone: 'ok', text: '● actif' }
    }
    if (c.failing) return { tone: 'crit', text: c.oomKilled ? '■ mémoire (OOM)' : `■ arrêté (${c.exitCode})` }
    if (c.state === 'paused') return { tone: 'unknown', text: '◌ en pause' }
    return { tone: 'unknown', text: '◌ arrêté' }
  }

  function uptime(c: Container): string {
    const ref = c.state === 'running' ? c.startedAt : c.finishedAt
    if (!ref) return '—'
    const s = Math.max(0, Math.round((now - Date.parse(ref)) / 1000))
    const txt = s < 60 ? `${s} s` : s < 3600 ? `${Math.floor(s / 60)} min` : s < 86400 ? `${Math.floor(s / 3600)} h` : `${Math.floor(s / 86400)} j`
    return c.state === 'running' ? txt : `il y a ${txt}`
  }

  function ask(host: string, c: Container) {
    target = { host, c }
    dialog?.showModal()
  }

  async function confirm() {
    if (!target) return
    const { host, c } = target
    dialog?.close()
    notice = null
    try {
      await restartContainer(host, c.name)
      notice = { tone: 'ok', text: `Redémarrage de ${c.name} lancé sur ${host}.` }
      setTimeout(loadActions, 3000)
    } catch (err) {
      const msg =
        err instanceof ApiError
          ? err.status === 403
            ? 'ce conteneur est protégé'
            : err.status === 409
              ? 'un redémarrage est déjà en cours'
              : err.status === 404
                ? 'conteneur introuvable'
                : `erreur ${err.status}`
          : 'serveur injoignable'
      notice = { tone: 'crit', text: `Impossible de redémarrer ${c.name} : ${msg}.` }
    }
  }

  const fmtTime = new Intl.DateTimeFormat('fr-FR', { day: '2-digit', month: '2-digit', hour: '2-digit', minute: '2-digit' })
</script>

<section class="stack" aria-labelledby="ct-title">
  <header class="head">
    <h2 id="ct-title">Conteneurs</h2>
    <div class="tools">
      <label class="search">
        <span class="visually-hidden">Filtrer</span>
        <input type="search" placeholder="Filtrer par nom, image, app…" bind:value={filter} />
      </label>
      <label class="toggle mono">
        <input type="checkbox" bind:checked={onlyIssues} /> problèmes seulement
      </label>
    </div>
  </header>

  {#if notice}
    <p class="banner mono" data-tone={notice.tone} role="status" transition:fade={{ duration: 200 }}>{notice.text}</p>
  {/if}

  {#if live.containers === null}
    <div class="card skeleton" aria-busy="true"></div>
  {:else if hosts.length === 0}
    <div class="card empty">
      <h3>Aucun hôte Docker</h3>
      <p>
        Ajoute <code class="mono">"docker": true</code> à un VPS dans <code class="mono">ARGOS_HOSTS</code> et installe le
        script <code class="mono">argos-docker</code> sur la machine (voir DEPLOY.md).
      </p>
    </div>
  {:else}
    {#each hosts as hc, i (hc.host)}
      {@const rows = visible(hc.containers)}
      {@const issues = hc.containers.filter((c) => c.failing).length}
      <article class="card host" in:fly={{ y: 12, duration: 700, delay: i * 80, easing: cubicOut }}>
        <div class="host-head">
          <h3>{hc.host} <span class="mono count">· {hc.containers.length}</span></h3>
          {#if hc.error}
            <span class="pill mono" data-tone="crit">■ {hc.error.includes('sudo') ? 'script argos-docker absent ou sudo refusé' : 'liste indisponible'}</span>
          {:else if issues > 0}
            <span class="pill mono" data-tone="crit">■ {issues} en échec</span>
          {:else}
            <span class="pill mono" data-tone="ok">● tous sains</span>
          {/if}
        </div>
        {#if hc.error}
          <p class="mono error">{hc.error}</p>
        {/if}
        {#if rows.length > 0}
          <div class="scroll">
            <table>
              <thead>
                <tr>
                  <th scope="col">État</th>
                  <th scope="col">Conteneur</th>
                  <th scope="col" class="hide-sm">Image</th>
                  <th scope="col">Depuis</th>
                  <th scope="col" class="num">CPU</th>
                  <th scope="col" class="num hide-sm">RAM</th>
                  <th scope="col" class="num hide-sm">Redém.</th>
                  <th scope="col"><span class="visually-hidden">Actions</span></th>
                </tr>
              </thead>
              <tbody>
                {#each rows as c (c.id)}
                  {@const p = pill(c)}
                  <tr class:failing={c.failing}>
                    <td><span class="pill mono" data-tone={p.tone}>{p.text}</span></td>
                    <td>
                      <div class="name">{c.name}</div>
                      <div class="tags mono">
                        {#if c.app}<span>{c.app}</span>{/if}
                        {#if c.autoRestart}<span class="tag" title="Redémarrage automatique activé (label argos.autorestart)">auto</span>{/if}
                        {#if c.protected}<span class="tag" title="Argos ne redémarre jamais ce conteneur">protégé</span>{/if}
                      </div>
                    </td>
                    <td class="mono dim hide-sm" title={c.image}>{c.image.length > 40 ? c.image.slice(0, 39) + '…' : c.image}</td>
                    <td class="mono dim">{uptime(c)}</td>
                    <td class="mono num">{c.cpu !== undefined ? `${pct(c.cpu, 1)} %` : '—'}</td>
                    <td class="mono num hide-sm">{c.mem !== undefined ? bytes(c.mem) : '—'}</td>
                    <td class="mono num hide-sm dim">{c.restartCount}</td>
                    <td class="act">
                      <button type="button" class="ghost-sm" onclick={() => nav.openLogs(hc.host, c.name)} aria-label="Journaux de {c.name}">
                        Logs
                      </button>
                      {#if !c.protected}
                        <button
                          type="button"
                          class:primary={c.failing}
                          disabled={c.state === 'restarting'}
                          onclick={() => ask(hc.host, c)}
                          aria-label="Redémarrer {c.name}"
                        >
                          {c.state === 'restarting' ? '…' : 'Redémarrer'}
                        </button>
                      {/if}
                    </td>
                  </tr>
                {/each}
              </tbody>
            </table>
          </div>
        {:else if !hc.error}
          <p class="mono dim pad">{filter || onlyIssues ? 'Aucun conteneur ne correspond.' : 'Aucun conteneur.'}</p>
        {/if}
      </article>
    {/each}
  {/if}

  <article class="card journal" aria-labelledby="journal-title">
    <h3 id="journal-title">Journal des actions</h3>
    {#if actions.length === 0}
      <p class="mono dim">Aucune action pour l’instant.</p>
    {:else}
      <ol class="mono">
        {#each actions as a (a.ts + a.host + a.container + a.action)}
          <li>
            <span class="dim">{fmtTime.format(new Date(a.ts))}</span>
            <span data-tone={a.ok ? 'ok' : 'crit'} class="g">{a.ok ? '●' : '■'}</span>
            <span>
              {a.action === 'auto-restart-abandoned' ? 'redémarrage auto abandonné' : 'redémarrage'}
              <b>{a.container}</b> sur {a.host}
              <span class="dim">· {a.trigger === 'auto' ? 'automatique' : 'manuel'}</span>
              {#if a.message}<span class="dim"> · {a.message}</span>{/if}
            </span>
          </li>
        {/each}
      </ol>
    {/if}
  </article>
</section>

<dialog bind:this={dialog} aria-labelledby="confirm-title" onclose={() => (target = null)}>
  {#if target}
    <h3 id="confirm-title">Redémarrer {target.c.name} ?</h3>
    <p>
      Sur <b>{target.host}</b>. Le conteneur est arrêté (20 s de délai de grâce) puis relancé ; le service est coupé
      pendant ce temps.
    </p>
    <div class="dlg-actions">
      <button type="button" onclick={() => dialog?.close()}>Annuler</button>
      <button type="button" class="primary" onclick={confirm}>Redémarrer</button>
    </div>
  {/if}
</dialog>

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

  .tools {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 12px 20px;
  }

  .search input {
    width: min(320px, 70vw);
    height: 40px;
    padding: 0 14px;
    background: var(--surface-sunk);
    border: 1px solid var(--line-strong);
    border-radius: var(--radius-control);
  }

  .search input:focus-visible {
    outline: none;
    border-color: var(--accent);
  }

  .toggle {
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 12px;
    color: var(--text-2);
    cursor: pointer;
  }

  .toggle input {
    accent-color: var(--accent);
  }

  .card {
    background: var(--surface);
    border: 1px solid var(--line);
    border-radius: var(--radius-panel);
    padding: 20px;
    min-width: 0;
  }

  .host {
    padding: 0;
    overflow: hidden;
  }

  .host-head {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: 12px;
    padding: 18px 20px 12px;
  }

  .count {
    font-size: 12px;
    color: var(--text-2);
    font-weight: 400;
  }

  .scroll {
    overflow-x: auto;
  }

  table {
    width: 100%;
    border-collapse: collapse;
    font-size: 13px;
  }

  th {
    text-align: left;
    font-weight: 400;
    font-size: 12px;
    color: var(--text-2);
    padding: 8px 12px;
    white-space: nowrap;
  }

  td {
    padding: 10px 12px;
    border-top: 1px solid var(--line);
    vertical-align: middle;
  }

  th:first-child,
  td:first-child {
    padding-left: 20px;
  }

  th:last-child,
  td:last-child {
    padding-right: 20px;
  }

  tr.failing td {
    background: color-mix(in srgb, var(--crit) 6%, transparent);
  }

  .num {
    text-align: right;
    white-space: nowrap;
  }

  .dim {
    color: var(--text-2);
  }

  .name {
    font-weight: 500;
    overflow-wrap: anywhere;
  }

  .tags {
    display: flex;
    flex-wrap: wrap;
    gap: 4px 8px;
    font-size: 11px;
    color: var(--text-2);
    margin-top: 2px;
  }

  .tag {
    padding: 0 6px;
    border: 1px solid var(--line-strong);
    border-radius: var(--radius-pill);
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
    display: inline-block;
    font-size: 12px;
    padding: 3px 10px;
    border-radius: var(--radius-pill);
    color: var(--tone);
    background: color-mix(in srgb, var(--tone) 8%, transparent);
    border: 1px solid color-mix(in srgb, var(--tone) 20%, transparent);
    white-space: nowrap;
  }

  .g {
    color: var(--tone);
  }

  .act {
    text-align: right;
    width: 1%;
    white-space: nowrap;
  }

  button {
    min-height: 34px;
    padding: 0 12px;
    font-weight: 500;
    background: var(--fill);
    border: 1px solid var(--line-strong);
    border-radius: var(--radius-control);
    cursor: pointer;
    transition:
      border-color var(--dur-fast),
      filter var(--dur-fast),
      transform var(--dur-fast) var(--ease-out);
  }

  button:hover:not(:disabled) {
    border-color: var(--steel);
  }

  button:active:not(:disabled) {
    transform: scale(0.985);
  }

  button:disabled {
    opacity: 0.5;
    cursor: progress;
  }

  .ghost-sm {
    background: none;
    margin-right: 6px;
  }

  button.primary {
    color: var(--on-accent);
    background: var(--accent);
    border-color: transparent;
  }

  button.primary:hover:not(:disabled) {
    filter: brightness(1.08);
  }

  .banner {
    margin: 0;
    padding: 10px 14px;
    font-size: 12.5px;
    color: var(--tone);
    background: color-mix(in srgb, var(--tone) 8%, transparent);
    border: 1px solid color-mix(in srgb, var(--tone) 20%, transparent);
    border-radius: var(--radius-control);
  }

  .error {
    margin: 0 20px 12px;
    font-size: 12px;
    color: var(--crit);
    overflow-wrap: anywhere;
  }

  .pad {
    margin: 0;
    padding: 4px 20px 18px;
    font-size: 12px;
  }

  .empty p {
    margin: 8px 0 0;
    color: var(--text-2);
  }

  .skeleton {
    min-height: 240px;
    background: linear-gradient(90deg, var(--surface) 0%, var(--fill) 50%, var(--surface) 100%);
    background-size: 200% 100%;
    animation: shimmer 1.6s ease-in-out infinite;
  }

  @keyframes shimmer {
    to {
      background-position: -200% 0;
    }
  }

  .journal ol {
    list-style: none;
    margin: 12px 0 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 8px;
    font-size: 12px;
  }

  .journal li {
    display: flex;
    gap: 10px;
    align-items: baseline;
  }

  .journal b {
    font-weight: 500;
  }

  dialog {
    width: min(440px, calc(100vw - 32px));
    padding: 24px;
    color: var(--text);
    background: var(--surface);
    border: 1px solid var(--line-strong);
    border-radius: var(--radius-panel);
  }

  dialog::backdrop {
    background: color-mix(in srgb, var(--bg) 70%, transparent);
    backdrop-filter: blur(2px);
  }

  dialog[open] {
    animation: pop 220ms var(--ease-out);
  }

  @keyframes pop {
    from {
      opacity: 0;
      transform: translateY(8px) scale(0.98);
    }
  }

  dialog p {
    color: var(--text-2);
    margin: 10px 0 20px;
  }

  .dlg-actions {
    display: flex;
    justify-content: flex-end;
    gap: 10px;
  }

  .dlg-actions button {
    min-height: 40px;
    padding: 0 16px;
  }

  @media (max-width: 720px) {
    h2 {
      font-size: 32px;
    }

    .hide-sm {
      display: none;
    }

    button {
      min-height: 44px;
    }
  }
</style>
