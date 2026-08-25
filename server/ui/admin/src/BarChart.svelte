<script lang="ts">
  // Stacked daily columns: ≤24px thick, 4px rounded top, 2px surface gaps
  // between segments and bars, per-bar hover tooltip listing every series.
  export interface Stack {
    label: string
    values: number[] // one per series, in series order
  }
  let {
    bars,
    series, // names, in the fixed categorical order; index+1 is the --chart-N token
    height = 180,
  }: { bars: Stack[]; series: string[]; height?: number } = $props()

  const padL = 36, padR = 8, padT = 10, padB = 24
  let width = $state(600)
  let hoverI: number | null = $state(null)
  let showTable = $state(false)

  const totals = $derived(bars.map((b) => b.values.reduce((a, v) => a + v, 0)))
  const vMax = $derived(Math.max(1, ...totals) * 1.05)
  const slot = $derived((width - padL - padR) / Math.max(1, bars.length))
  const bw = $derived(Math.min(24, Math.max(3, slot - 2)))
  const x = (i: number) => padL + i * slot + (slot - bw) / 2
  const y = (v: number) => padT + (1 - v / vMax) * (height - padT - padB)
  // Deduplicated: with a tiny max, 0.5·max and 1·max round to the same value.
  const ticks = $derived([...new Set([0, 0.5, 1].map((f) => Math.round(f * vMax)))])
  const dayLabel = (d: string) => new Date(d + 'T00:00:00Z').toLocaleDateString([], { day: 'numeric', month: 'short' })
  // Label every Nth day so labels never collide.
  const every = $derived(Math.max(1, Math.ceil(bars.length / Math.max(1, Math.floor((width - padL - padR) / 44)))))

  // Segment geometry with a 2px surface gap; rounded top only on the topmost segment.
  function segs(b: Stack) {
    let acc = 0
    const out: { i: number; y0: number; y1: number }[] = []
    b.values.forEach((v, i) => {
      if (v <= 0) return
      const y1 = y(acc), y0 = y(acc + v)
      out.push({ i, y0, y1 })
      acc += v
    })
    // top of stack gets the rounded end
    return out.map((s, k) => ({ ...s, top: k === out.length - 1, y1: k === 0 ? s.y1 : s.y1 - 2 }))
  }
  const seg = (s: { y0: number; y1: number; top: boolean }, xx: number) => {
    const h = Math.max(0, s.y1 - s.y0)
    const r = Math.min(4, h / 2, bw / 2)
    if (!s.top || h < 1) return `M${xx},${s.y1} v${-h} h${bw} v${h} Z`
    return `M${xx},${s.y1} v${-(h - r)} a${r},${r} 0 0 1 ${r},${-r} h${bw - 2 * r} a${r},${r} 0 0 1 ${r},${r} v${h - r} Z`
  }
</script>

<div class="chart" bind:clientWidth={width}>
  <div class="legend">
    {#each series as s, i (s)}
      <span><i class="key" style="background: var(--chart-{i + 1})"></i>{s}</span>
    {/each}
    <button class="tbl" onclick={() => (showTable = !showTable)}>{showTable ? 'Chart' : 'Table'}</button>
  </div>
  {#if showTable}
    <div class="tablewrap"><table>
      <thead><tr><th>Day</th>{#each series as s (s)}<th>{s}</th>{/each}<th>Total</th></tr></thead>
      <tbody>
        {#each bars.slice().reverse() as b, k (b.label)}
          <tr><td>{dayLabel(b.label)}</td>{#each b.values as v, i (i)}<td>{v}</td>{/each}<td>{totals[bars.length - 1 - k]}</td></tr>
        {/each}
      </tbody>
    </table></div>
  {:else}
    <svg viewBox="0 0 {width} {height}" preserveAspectRatio="none" role="img" aria-label="Plays per day" onmouseleave={() => (hoverI = null)}>
      {#each ticks as tv (tv)}
        <line class="grid" x1={padL} x2={width - padR} y1={y(tv)} y2={y(tv)} />
        <text class="axis" x={padL - 6} y={y(tv) + 3.5} text-anchor="end">{tv}</text>
      {/each}
      {#each bars as b, i (b.label)}
        <!-- hit target is the whole slot, not just the bar -->
        <rect class="hit" x={padL + i * slot} y={padT} width={slot} height={height - padT - padB} onmouseenter={() => (hoverI = i)} />
        {#each segs(b) as s (s.i)}
          <path d={seg(s, x(i))} style="fill: var(--chart-{s.i + 1})" opacity={hoverI === null || hoverI === i ? 1 : 0.55} />
        {/each}
        {#if i % every === 0}
          <text class="axis" x={x(i) + bw / 2} y={height - 6} text-anchor="middle">{dayLabel(b.label)}</text>
        {/if}
      {/each}
      {#if !totals.some((t) => t > 0)}
        <text class="axis" x={width / 2} y={height / 2} text-anchor="middle">No plays yet</text>
      {/if}
    </svg>
    {#if hoverI !== null && bars[hoverI]}
      <div class="tip" style="left: {Math.min((x(hoverI) / width) * 100, 70)}%">
        <div class="tt">{dayLabel(bars[hoverI].label)} · {totals[hoverI]} plays</div>
        {#each series as s, i (s)}
          <div class="tr"><i class="key" style="background: var(--chart-{i + 1})"></i><strong>{bars[hoverI].values[i]}</strong><span>{s}</span></div>
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
  .hit { fill: transparent; }
  .legend { display: flex; gap: 0.9rem; align-items: center; font-size: 0.78rem; color: var(--muted); margin-bottom: 0.4rem; }
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
