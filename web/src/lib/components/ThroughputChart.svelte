<script lang="ts">
  import { untrack } from 'svelte'
  import uPlot from 'uplot'
  import 'uplot/dist/uPlot.min.css'
  import { bitrate } from '../format'

  let {
    ts,
    rx,
    tx,
    height = 220,
    days = false,
  }: { ts: number[]; rx: (number | null)[]; tx: (number | null)[]; height?: number; days?: boolean } = $props()

  const timeFmt = new Intl.DateTimeFormat('fr-FR', { hour: '2-digit', minute: '2-digit' })
  const dayFmt = new Intl.DateTimeFormat('fr-FR', { weekday: 'short', day: 'numeric' })

  let chart: uPlot | null = null

  const css = (name: string) => getComputedStyle(document.documentElement).getPropertyValue(name).trim()

  function data(): uPlot.AlignedData {
    // Bytes/s → bits/s for display.
    return [ts, rx.map((v) => (v === null ? null : v * 8)), tx.map((v) => (v === null ? null : v * 8))]
  }

  function plot(node: HTMLDivElement) {
    const accent = css('--accent')
    const steel = css('--steel')
    const line = css('--line')
    const text2 = css('--text-2')
    const mono = css('--font-mono')
    const axis: uPlot.Axis = {
      stroke: text2,
      grid: { stroke: line, width: 1 },
      ticks: { show: false },
      font: `11px ${mono}`,
    }
    const opts: uPlot.Options = {
      width: node.clientWidth,
      height,
      legend: { show: false },
      cursor: { points: { size: 7 }, drag: { x: false, y: false } },
      scales: { x: { time: true }, y: { range: (_u, _min, max) => [0, Math.max(max * 1.15, 1000)] } },
      axes: [
        {
          ...axis,
          space: 80,
          // French 24 h clock; day names on the 7-day view.
          values: (_u, splits) => splits.map((t) => (days ? dayFmt : timeFmt).format(new Date(t * 1000))),
        },
        { ...axis, size: 64, values: (_u, vals) => vals.map((v) => {
            const b = bitrate(v / 8)
            return `${b.value} ${b.unit}`
          }) },
      ],
      series: [
        {},
        {
          label: 'Descendant',
          stroke: accent,
          width: 2,
          fill: (u) => {
            const g = u.ctx.createLinearGradient(0, u.bbox.top, 0, u.bbox.top + u.bbox.height)
            // Canvas does not understand color-mix(): use #rrggbbaa.
            g.addColorStop(0, `${accent}47`)
            g.addColorStop(1, `${accent}00`)
            return g
          },
          points: { show: false },
        },
        { label: 'Montant', stroke: steel, width: 1.5, dash: [4, 4], points: { show: false } },
      ],
    }
    chart = new uPlot(opts, untrack(data), node)
    const ro = new ResizeObserver(() => chart?.setSize({ width: node.clientWidth, height }))
    ro.observe(node)
    return () => {
      ro.disconnect()
      chart?.destroy()
      chart = null
    }
  }

  $effect(() => {
    const d = data()
    chart?.setData(d)
  })
</script>

<div class="chart" {@attach plot} role="img" aria-label="Débit réseau agrégé, descendant et montant"></div>

<style>
  .chart {
    width: 100%;
    min-width: 0;
    animation: fade 1.2s ease 0.2s both;
  }

  .chart :global(.u-over) {
    cursor: crosshair;
  }

  @keyframes fade {
    from {
      opacity: 0;
    }
  }
</style>
