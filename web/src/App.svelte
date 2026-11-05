<script lang="ts">
  let status = $state<'loading' | 'ok' | 'down'>('loading')

  fetch('/health')
    .then((r) => (status = r.ok ? 'ok' : 'down'))
    .catch(() => (status = 'down'))
</script>

<main>
  <h1>ARGOS</h1>
  <p>backend: {status}</p>
</main>

<style>
  main { padding: 2rem; }
  h1 { letter-spacing: 0.3em; font-weight: 500; }
</style>
