<script lang="ts">
  // Time-series line chart: 2px lines, 10% area wash for a single series,
  // hairline grid, crosshair + one tooltip listing every series at that X.
  // Colours come from the theme's --chart-N tokens; text never wears them.
  export interface Series {
    name: string
    token: 1 | 2 | 3
    points: { t: number; v: number }[]
  }
  let {
    series,
    yMax,
    format = (v: number) => String(Math.round(v)),
    height = 180,
  }: { series: Series[]; yMax?: number; format?: (v: number) => string; height?: number } = $props()

  const padL = 44, padR = 12, padT = 10, padB = 24
  let width = $state(600)
  let hoverX: number | null = $state(null)
  let showTable = $state(false)

  const all = $derived(series.flatMap((s) => s.points))
  const tMin = $derived(all.length ? Math.min(...all.map((p) => p.t)) : 0)
  const tMax = $derived(all.length ? Math.max(...all.map((p) => p.t)) : 1)
  const vMax = $derived(yMax ?? Math.max(1, ...all.map((p) => p.v)) * 1.05)
  const x = (t: number) => padL + ((t - tMin) / Math.max(1, tMax - tMin)) * (width - padL - padR)
  const y = (v: number) => padT + (1 - v / vMax) * (height - padT - padB)
  const path = (pts: { t: number; v: number }[]) =>
    pts.map((p, i) => `${i ? 'L' : 'M'}${x(p.t).toFixed(1)},${y(p.v).toFixed(1)}`).join(' ')
  const area = (pts: { t: number; v: number }[]) =>
    pts.length ? `${path(pts)} L${x(pts[pts.length - 1].t).toFixed(1)},${y(0)} L${x(pts[0].t).toFixed(1)},${y(0)} Z` : ''
  const ticks = $derived([0, 0.25, 0.5, 0.75, 1].map((f) => f * vMax))
  const timeLabel = (t: number) => new Date(t).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })

  // Nearest sample to the pointer, per series.
  const hover = $derived.by(() => {
    if (hoverX === null || !all.length) return null
    const t = tMin + ((hoverX - padL) / Math.max(1, width - padL - padR)) * (tMax - tMin)
    const rows = series.map((s) => {
      let best = s.points[0]
      for (const p of s.points) if (Math.abs(p.t - t) < Math.abs(best.t - t)) best = p
      return { name: s.name, token: s.token, p: best }
    }).filter((r) => r.p)
    return rows.length ? { t: rows[0].p.t, rows } : null
  })

  function onMove(e: MouseEvent) {
    const r = (e.currentTarget as SVGElement).getBoundingClientRect()
    const px = ((e.clientX - r.left) / r.width) * width
    hoverX = px < padL || px > width - padR ? null : px
  }
</script>

<div class="chart" bind:clientWidth={width}>
  {#if series.length > 1}
    <div class="legend">
      {#each series as s (s.name)}
        <span><i class="key" style="background: var(--chart-{s.token})"></i>{s.name}</span>
      {/each}
      <button class="tbl" onclick={() => (showTable = !showTable)}>{showTable ? 'Chart' : 'Table'}</button>
    </div>
  {:else}
    <div class="legend end"><button class="tbl" onclick={() => (showTable = !showTable)}>{showTable ? 'Chart' : 'Table'}</button></div>
  {/if}
  {#if showTable}
    <div class="tablewrap"><table>
      <thead><tr><th>Time</th>{#each series as s (s.name)}<th>{s.name}</th>{/each}</tr></thead>
      <tbody>
        {#each (series[0]?.points ?? []).slice(-40).reverse() as p, i (p.t)}
          <tr><td>{timeLabel(p.t)}</td>{#each series as s (s.name)}<td>{format(s.points[s.points.length - 1 - i]?.v ?? 0)}</td>{/each}</tr>
        {/each}
      </tbody>
    </table></div>
  {:else}
    <svg viewBox="0 0 {width} {height}" preserveAspectRatio="none" role="img" aria-label={series.map((s) => s.name).join(', ')}
      onmousemove={onMove} onmouseleave={() => (hoverX = null)}>
      {#each ticks as tv, ti (ti)}
        <line class="grid" x1={padL} x2={width - padR} y1={y(tv)} y2={y(tv)} />
        <text class="axis" x={padL - 6} y={y(tv) + 3.5} text-anchor="end">{format(tv)}</text>
      {/each}
      {#if all.length}
        <text class="axis" x={padL} y={height - 6}>{timeLabel(tMin)}</text>
        <text class="axis" x={width - padR} y={height - 6} text-anchor="end">{timeLabel(tMax)}</text>
      {/if}
      {#each series as s (s.name)}
        {#if series.length === 1}<path class="area" d={area(s.points)} style="fill: var(--chart-{s.token})" />{/if}
        <path class="line" d={path(s.points)} style="stroke: var(--chart-{s.token})" />
      {/each}
      {#if hover}
        <line class="cross" x1={x(hover.t)} x2={x(hover.t)} y1={padT} y2={height - padB} />
        {#each hover.rows as r (r.name)}
          <circle cx={x(r.p.t)} cy={y(r.p.v)} r="4" style="fill: var(--chart-{r.token})" class="dot" />
        {/each}
      {/if}
      {#if !all.length}
        <text class="axis" x={width / 2} y={height / 2} text-anchor="middle">Collecting samples…</text>
      {/if}
    </svg>
    {#if hover}
      <div class="tip" style="left: {Math.min(x(hover.t) / width * 100, 70)}%">
        <div class="tt">{timeLabel(hover.t)}</div>
        {#each hover.rows as r (r.name)}
          <div class="tr"><i class="key" style="background: var(--chart-{r.token})"></i><strong>{format(r.p.v)}</strong><span>{r.name}</span></div>
        {/each}
      </div>
    {/if}
  {/if}
</div>

<style>
  .chart { position: relative; width: 100%; }
  svg { width: 100%; height: auto; display: block; overflow: visible; }
  .grid { stroke: var(--border); stroke-width: 1; }
  .axis { fill: var(--muted); font-size: 10px; }
  .line { fill: none; stroke-width: 2; stroke-linejoin: round; stroke-linecap: round; vector-effect: non-scaling-stroke; }
  .area { opacity: 0.1; }
  .cross { stroke: var(--muted); stroke-width: 1; }
  .dot { stroke: var(--panel); stroke-width: 2; }
  .legend { display: flex; gap: 0.9rem; align-items: center; font-size: 0.78rem; color: var(--muted); margin-bottom: 0.4rem; }
  .legend.end { justify-content: flex-end; }
  .legend span { display: inline-flex; align-items: center; gap: 0.35rem; }
  .key { display: inline-block; width: 12px; height: 2px; border-radius: 1px; }
  .tbl { margin-left: auto; color: var(--muted); font-size: 0.75rem; border: 1px solid var(--border); border-radius: 999px; padding: 0.1rem 0.5rem; }
  .tip {
    position: absolute; top: 1.6rem; transform: translateX(8px);
    background: var(--panel); border: 1px solid var(--border); border-radius: 8px;
    padding: 0.4rem 0.6rem; font-size: 0.78rem; pointer-events: none; white-space: nowrap;
    box-shadow: 0 4px 16px color-mix(in srgb, var(--bg) 60%, transparent);
  }
  .tt { color: var(--muted); margin-bottom: 0.2rem; }
  .tr { display: flex; align-items: center; gap: 0.4rem; }
  .tr span { color: var(--muted); }
  .tablewrap { max-height: 12rem; overflow: auto; font-size: 0.78rem; }
  table { width: 100%; border-collapse: collapse; }
  th, td { text-align: left; padding: 0.25rem 0.4rem; border-bottom: 1px solid var(--border); }
  th { color: var(--muted); font-weight: 500; }
</style>
