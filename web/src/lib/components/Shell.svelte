<script lang="ts">
  import { auth } from '../auth.svelte'
  import Logo from './Logo.svelte'
  import Hosts from './Hosts.svelte'

  const sections = ['Vue d’ensemble', 'Hôtes', 'Conteneurs', 'Journaux', 'Stockage']
  let current = $state(sections[0])
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
      {#each sections as s (s)}
        <a
          href="#{s}"
          aria-current={current === s ? 'page' : undefined}
          onclick={(e) => {
            e.preventDefault()
            current = s
          }}>{s}</a
        >
      {/each}
    </nav>

    <div class="meta">
      <span class="mono session">session · {expires}</span>
      <button type="button" class="ghost" onclick={logout} disabled={leaving}>Se déconnecter</button>
    </div>
  </header>

  <main>
    {#if current === 'Hôtes'}
      <Hosts />
    {:else}
      <section class="empty">
        <p class="mono kicker">{current}</p>
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
