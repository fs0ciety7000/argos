<script lang="ts">
  import { fly } from 'svelte/transition'
  import { cubicOut } from 'svelte/easing'
  import { auth, loginErrorMessage } from '../auth.svelte'
  import Logo from './Logo.svelte'

  let password = $state('')
  let code = $state('')
  let pending = $state(false)
  let error = $state<string | null>(null)

  let canSubmit = $derived(password.length > 0 && /^\d{6}$/.test(code) && !pending)

  async function submit(event: SubmitEvent) {
    event.preventDefault()
    if (!canSubmit) return
    pending = true
    error = null
    try {
      await auth.login(password, code)
      password = ''
    } catch (err) {
      error = loginErrorMessage(err)
      code = ''
    } finally {
      pending = false
    }
  }

  function onCodeInput(event: Event & { currentTarget: HTMLInputElement }) {
    code = event.currentTarget.value.replace(/\D/g, '').slice(0, 6)
  }
</script>

<main class="screen">
  <div class="grid" aria-hidden="true"></div>

  <section class="card" in:fly={{ y: 14, duration: 800, easing: cubicOut }} aria-labelledby="login-title">
    <header class="brand">
      <Logo size={34} />
      <div>
        <h1 id="login-title">Argos</h1>
        <p class="mono">control center · accès restreint</p>
      </div>
    </header>

    <form onsubmit={submit} novalidate>
      <label class="field">
        <span>Mot de passe</span>
        <input
          type="password"
          name="password"
          autocomplete="current-password"
          required
          bind:value={password}
          disabled={pending}
        />
      </label>

      <label class="field">
        <span>Code à 6 chiffres</span>
        <input
          class="mono code"
          type="text"
          name="code"
          inputmode="numeric"
          autocomplete="one-time-code"
          pattern="[0-9]{6}"
          maxlength="6"
          required
          value={code}
          oninput={onCodeInput}
          disabled={pending}
          aria-describedby="code-hint"
        />
        <small id="code-hint" class="hint">Depuis ton application d’authentification.</small>
      </label>

      {#if error}
        <p class="error mono" role="alert" transition:fly={{ y: -4, duration: 250 }}>■ {error}</p>
      {/if}

      <button type="submit" disabled={!canSubmit} aria-busy={pending}>
        {#if pending}
          <span class="dot" aria-hidden="true"></span> Vérification…
        {:else}
          Se connecter
        {/if}
      </button>
    </form>
  </section>
</main>

<style>
  .screen {
    position: relative;
    min-height: 100vh;
    display: grid;
    place-items: center;
    padding: 24px 16px;
    overflow: hidden;
  }

  .grid {
    position: absolute;
    inset: 0;
    background-image:
      linear-gradient(to right, color-mix(in srgb, var(--text) 4%, transparent) 1px, transparent 1px),
      linear-gradient(to bottom, color-mix(in srgb, var(--text) 4%, transparent) 1px, transparent 1px);
    background-size: 32px 32px;
    mask-image: radial-gradient(ellipse at center, #000 30%, transparent 75%);
    pointer-events: none;
  }

  .card {
    position: relative;
    width: min(400px, 100%);
    padding: 32px;
    background: var(--surface);
    border: 1px solid var(--line);
    border-radius: var(--radius-panel);
    display: flex;
    flex-direction: column;
    gap: 28px;
  }

  .brand {
    display: flex;
    align-items: center;
    gap: 14px;
  }

  h1 {
    margin: 0;
    font-size: 22px;
    font-weight: 600;
    letter-spacing: -0.02em;
    line-height: 1.1;
  }

  .brand p {
    margin: 2px 0 0;
    font-size: 12px;
    color: var(--text-2);
  }

  form {
    display: flex;
    flex-direction: column;
    gap: 18px;
  }

  .field {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }

  .field > span {
    font-size: 13px;
    color: var(--text-2);
  }

  input {
    height: 44px;
    padding: 0 14px;
    background: var(--surface-sunk);
    border: 1px solid var(--line-strong);
    border-radius: var(--radius-control);
    transition: border-color var(--dur-fast) var(--ease-out);
  }

  input:hover:not(:disabled) {
    border-color: var(--steel);
  }

  input:focus-visible {
    outline: none;
    border-color: var(--accent);
    box-shadow: 0 0 0 3px color-mix(in srgb, var(--accent) 22%, transparent);
  }

  input:disabled {
    opacity: 0.6;
  }

  .code {
    font-size: 20px;
    letter-spacing: 0.4em;
  }

  .hint {
    font-size: 12px;
    color: var(--text-2);
  }

  .error {
    margin: 0;
    padding: 10px 12px;
    font-size: 12.5px;
    color: var(--crit);
    background: color-mix(in srgb, var(--crit) 8%, transparent);
    border: 1px solid color-mix(in srgb, var(--crit) 20%, transparent);
    border-radius: var(--radius-control);
  }

  button {
    height: 44px;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: 8px;
    font-weight: 500;
    color: var(--on-accent);
    background: var(--accent);
    border: 0;
    border-radius: var(--radius-control);
    cursor: pointer;
    transition:
      transform var(--dur-fast) var(--ease-out),
      filter var(--dur-fast) var(--ease-out),
      opacity var(--dur-fast);
  }

  button:hover:not(:disabled) {
    filter: brightness(1.08);
  }

  button:active:not(:disabled) {
    transform: scale(0.985);
  }

  button:disabled {
    opacity: 0.45;
    cursor: not-allowed;
  }

  .dot {
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background: currentColor;
    animation: pulse 1.2s ease-in-out infinite;
  }

  @keyframes pulse {
    50% {
      opacity: 0.25;
    }
  }

  @media (max-width: 480px) {
    .card {
      padding: 24px;
    }
  }
</style>
