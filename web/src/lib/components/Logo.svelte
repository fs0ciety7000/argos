<script lang="ts">
  let { size = 26, spin = true }: { size?: number; spin?: boolean } = $props()
</script>

<svg width={size} height={size} viewBox="0 0 26 26" aria-hidden="true">
  <circle cx="13" cy="13" r="11.5" fill="none" stroke="var(--line-strong)" stroke-width="1" />
  <circle
    class:spin
    cx="13"
    cy="13"
    r="11.5"
    fill="none"
    stroke="var(--accent)"
    stroke-width="1.5"
    stroke-dasharray="18 54"
    stroke-linecap="round"
  />
  <circle cx="13" cy="13" r="4" fill="var(--accent)" />
</svg>

<style>
  .spin {
    transform-box: fill-box;
    transform-origin: center;
    animation: spin 9s linear infinite;
  }

  @keyframes spin {
    to {
      transform: rotate(360deg);
    }
  }
</style>
