<script lang="ts">
  import { untrack } from 'svelte'
  import uPlot from 'uplot'
  import 'uplot/dist/uPlot.min.css'
  import { bytes } from '../format'
  import type { DiskSeries } from '../metrics'

  let { series, days = false, tone = 'ok', label }: { series: DiskSeries; days?: boolean; tone?: 'ok' | 'warn' | 'crit'; label: string } =
    $props()

  let chart: uPlot | null = null
  const css = (n: string) => getComputedStyle(document.documentElement).getPropertyValue(n).trim()
  const timeFmt = new Intl.DateTimeFormat('fr-FR', { hour: '2-digit', minute: '2-digit' })
  const dayFmt = new Intl.DateTimeFormat('fr-FR', { weekday: 'short', day: 'numeric' })

  function data(): uPlot.AlignedData {
    return [series.ts, series.used, series.size]
  }

  function plot(node: HTMLDivElement) {
    const color = tone === 'ok' ? css('--accent') : css(tone === 'warn' ? '--warn' : '--crit')
    const axis: uPlot.Axis = {
      stroke: css('--text-2'),
      grid: { stroke: css('--line'), width: 1 },
      ticks: { show: false },
      font: `11px ${css('--font-mono')}`,
    }
    chart = new uPlot(
      {
        width: node.clientWidth,
        height: 150,
        legend: { show: false },
        cursor: { points: { size: 6 }, drag: { x: false, y: false } },
        scales: { x: { time: true }, y: { range: (_u, _min, max) => [0, Math.max(max * 1.05, 1)] } },
        axes: [
          { ...axis, space: 70, values: (_u, s) => s.map((t) => (days ? dayFmt : timeFmt).format(new Date(t * 1000))) },
          { ...axis, size: 70, values: (_u, v) => v.map((x) => bytes(x, 0)) },
        ],
        series: [
          {},
          {
            label: 'Utilisé',
            stroke: color,
            width: 2,
            fill: (u) => {
              const g = u.ctx.createLinearGradient(0, u.bbox.top, 0, u.bbox.top + u.bbox.height)
              g.addColorStop(0, `${color}40`) // canvas has no color-mix(): #rrggbbaa
              g.addColorStop(1, `${color}00`)
              return g
            },
            points: { show: false },
          },
          { label: 'Capacité', stroke: css('--steel'), width: 1, dash: [4, 4], points: { show: false } },
        ],
      },
      untrack(data),
      node,
    )
    const ro = new ResizeObserver(() => chart?.setSize({ width: node.clientWidth, height: 150 }))
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

<div class="chart" {@attach plot} role="img" aria-label={label}></div>

<style>
  .chart {
    width: 100%;
    min-width: 0;
  }
</style>
