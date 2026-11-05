<script lang="ts">
  import { auth } from '../auth.svelte'
  import Logo from './Logo.svelte'
  import Hosts from './Hosts.svelte'
  import Overview from './Overview.svelte'
  import Containers from './Containers.svelte'
  import Logs from './Logs.svelte'
  import { nav, SECTIONS } from '../nav.svelte'
  import { onMount } from 'svelte'
  import { live } from '../live.svelte'

  onMount(() => {
    live.start()
    return () => live.stop()
  })

  let liveLabel = $derived(
    live.state === 'open' ? 'LIVE' : live.state === 'connecting' ? 'connexion…' : 'hors ligne',
  )

  let leaving = $state(false)

  let expires = $derived(
    auth.expiresAt?.toLocaleTimeString('fr-FR', { hour: '2-digit', minute: '2-digit' }) ?? '—',
  )

  async function logout() {
    leaving = true
    try {
      await auth.logout()
    } finally {
      leaving = false
    }
  }
</script>

<div class="page">
  <header class="bar">
    <div class="brand">
      <Logo />
      <span>Argos</span>
    </div>

    <nav aria-label="Sections">
      {#each SECTIONS as s (s)}
        <a
          href="#{s}"
          aria-current={nav.section === s ? 'page' : undefined}
          onclick={(e) => {
            e.preventDefault()
            nav.section = s
          }}>{s}</a
        >
      {/each}
    </nav>

    <div class="meta">
      <span class="mono live" data-state={live.state}><i aria-hidden="true"></i>{liveLabel}</span>
      <span class="mono session">session · {expires}</span>
      <button type="button" class="ghost" onclick={logout} disabled={leaving}>Se déconnecter</button>
    </div>
  </header>

  <main>
    {#if nav.section === 'Vue d’ensemble'}
      <Overview />
    {:else if nav.section === 'Hôtes'}
      <Hosts />
    {:else if nav.section === 'Conteneurs'}
      <Containers />
    {:else if nav.section === 'Journaux'}
      <Logs />
    {:else}
      <section class="empty">
        <p class="mono kicker">{nav.section}</p>
        <h2>Bientôt ici.</h2>
        <p>Les métriques et le flux temps réel arrivent dans une prochaine étape. Les hôtes sont déjà dans « Hôtes ».</p>
      </section>
    {/if}
  </main>
</div>

<style>
  .page {
    max-width: 1440px;
    margin: 0 auto;
    padding: 20px 32px 48px;
    display: flex;
    flex-direction: column;
    gap: 20px;
  }

  .bar {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 16px 32px;
    padding: 10px 12px 10px 16px;
    background: var(--surface);
    border: 1px solid var(--line);
    border-radius: var(--radius-bar);
  }

  .brand {
    display: flex;
    align-items: center;
    gap: 10px;
    font-size: 16px;
    font-weight: 600;
    letter-spacing: -0.01em;
  }

  nav {
    display: flex;
    flex-wrap: wrap;
    gap: 4px;
    flex-grow: 1;
  }

  nav a {
    padding: 6px 12px;
    white-space: nowrap;
    border-radius: 8px;
    color: var(--text-2);
    text-decoration: none;
    transition:
      color var(--dur-fast),
      background var(--dur-fast);
  }

  nav a:hover {
    color: var(--text);
  }

  nav a[aria-current='page'] {
    color: var(--text);
    background: var(--fill);
  }

  .meta {
    display: flex;
    align-items: center;
    gap: 16px;
  }

  .session {
    font-size: 12px;
    color: var(--text-2);
  }

  .live {
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 12px;
    color: var(--text-2);
  }

  .live i {
    position: relative;
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: var(--unknown);
  }

  .live[data-state='open'] {
    color: var(--text);
  }

  .live[data-state='open'] i {
    background: var(--accent);
  }

  .live[data-state='open'] i::after {
    content: '';
    position: absolute;
    inset: 0;
    border-radius: 50%;
    background: var(--accent);
    animation: ping 1.6s cubic-bezier(0, 0, 0.2, 1) infinite;
  }

  @keyframes ping {
    to {
      transform: scale(3.2);
      opacity: 0;
    }
  }

  .ghost {
    min-height: 36px;
    padding: 0 14px;
    background: var(--fill);
    border: 1px solid var(--line-strong);
    border-radius: var(--radius-control);
    cursor: pointer;
    transition: border-color var(--dur-fast);
  }

  .ghost:hover:not(:disabled) {
    border-color: var(--steel);
  }

  .empty {
    padding: 64px 32px;
    background: var(--surface);
    border: 1px solid var(--line);
    border-radius: var(--radius-panel);
  }

  .kicker {
    margin: 0;
    font-size: 12px;
    color: var(--accent);
  }

  h2 {
    margin: 8px 0;
    font-size: 40px;
    font-weight: 300;
    letter-spacing: -0.04em;
  }

  .empty p:last-child {
    margin: 0;
    color: var(--text-2);
  }

  @media (max-width: 720px) {
    .page {
      padding: 16px;
    }

    nav {
      flex-wrap: nowrap;
      overflow-x: auto;
      order: 3;
      width: 100%;
    }

    .ghost {
      min-height: 44px;
    }
  }
</style>
