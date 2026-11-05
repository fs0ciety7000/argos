<script lang="ts">
  let {
    values,
    max = 100,
    label,
    tone = 'accent',
  }: { values: (number | null)[]; max?: number; label: string; tone?: 'accent' | 'muted' } = $props()

  const W = 200
  const H = 44

  let points = $derived.by(() => {
    const vs = values.filter((v): v is number => v !== null && v >= 0)
    if (vs.length < 2) return ''
    const step = W / (vs.length - 1)
    return vs.map((v, i) => `${(i * step).toFixed(1)},${(H - Math.min(v / max, 1) * (H - 4) - 2).toFixed(1)}`).join(' ')
  })
  let last = $derived(points ? points.slice(points.lastIndexOf(' ') + 1).split(',') : null)
</script>

<svg viewBox="0 0 {W} {H}" preserveAspectRatio="none" role="img" aria-label={label} class:muted={tone === 'muted'}>
  {#if points}
    <polyline {points} fill="none" stroke-width="1.75" stroke-linejoin="round" vector-effect="non-scaling-stroke" />
    {#if last}
      <circle class="ping" cx={last[0]} cy={last[1]} r="2.5" />
    {/if}
  {/if}
</svg>

<style>
  svg {
    width: 100%;
    height: 44px;
    display: block;
    overflow: visible;
    stroke: var(--accent);
    fill: var(--accent);
  }

  svg.muted {
    stroke: var(--unknown);
    fill: var(--unknown);
  }

  .ping {
    transform-box: fill-box;
    transform-origin: center;
    animation: ping 1.6s cubic-bezier(0, 0, 0.2, 1) infinite;
  }

  @keyframes ping {
    0% {
      transform: scale(1);
      opacity: 0.9;
    }
    100% {
      transform: scale(3);
      opacity: 0;
    }
  }
</style>
