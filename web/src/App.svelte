<script lang="ts">
  import { onMount } from 'svelte'
  import { auth } from './lib/auth.svelte'
  import Login from './lib/components/Login.svelte'
  import Shell from './lib/components/Shell.svelte'
  import Logo from './lib/components/Logo.svelte'

  onMount(() => {
    auth.check()
  })
</script>

{#if auth.status === 'loading'}
  <div class="center" aria-busy="true" aria-label="Chargement">
    <Logo size={40} />
  </div>
{:else if auth.status === 'authenticated'}
  <Shell />
{:else if auth.status === 'unreachable'}
  <div class="center">
    <p class="mono">■ Serveur injoignable.</p>
    <button type="button" onclick={() => auth.check()}>Réessayer</button>
  </div>
{:else}
  <Login />
{/if}

<style>
  .center {
    min-height: 100vh;
    display: grid;
    place-content: center;
    justify-items: center;
    gap: 16px;
    color: var(--text-2);
  }

  .center p {
    margin: 0;
    color: var(--crit);
  }

  button {
    min-height: 44px;
    padding: 0 16px;
    background: var(--fill);
    border: 1px solid var(--line-strong);
    border-radius: var(--radius-control);
    cursor: pointer;
  }
</style>
