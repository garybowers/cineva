<!--
  An episode's intro and credits, for an administrator to correct: what
  detection found (or did not), set by hand with m:ss times, said to be
  absent, or handed back to detection — and the whole season sent back to
  detection. A value set by hand is locked: re-detection leaves it alone
  until it is handed back.
-->
<script lang="ts">
  import { onMount } from 'svelte'
  import {
    itemSegments, setItemSegment, clearItemSegment, redetectSeason,
    type Item, type ItemSegments, type SegmentKind, type SegmentState,
  } from './lib/api'

  let { episode, onclose }: { episode: Item; onclose: () => void } = $props()

  const KINDS: { kind: SegmentKind; label: string; none: string }[] = [
    { kind: 'intro', label: 'Intro', none: 'No intro' },
    { kind: 'credits', label: 'Credits', none: 'No credits' },
  ]

  let data: ItemSegments | null = $state(null)
  let loadErr = $state('')
  let msg = $state('')
  let busy = $state(false)
  // The m:ss fields, per kind, as typed.
  let start: Record<SegmentKind, string> = $state({ intro: '', credits: '' })
  let end: Record<SegmentKind, string> = $state({ intro: '', credits: '' })
  let dialog: HTMLDivElement

  onMount(() => {
    dialog?.focus()
    itemSegments(episode.id).then(take).catch((err) => (loadErr = errText(err)))
  })

  const errText = (err: unknown) => (err instanceof Error ? err.message : String(err))

  function take(d: ItemSegments) {
    data = d
    for (const s of d.segments) {
      start[s.kind] = s.found ? fmt(s.startSecs ?? 0) : ''
      end[s.kind] = s.found ? fmt(s.endSecs ?? 0) : ''
    }
  }

  const stateOf = (kind: SegmentKind): SegmentState | undefined => data?.segments.find((s) => s.kind === kind)

  function status(s: SegmentState | undefined): string {
    if (!s) return ''
    if (s.locked) return s.found ? 'Set by hand' : 'None — set by hand'
    if (s.found) return 'Found by detection'
    return s.analysed ? 'None found' : 'Not analysed yet'
  }

  /** "1:02", "1:02:30" or plain seconds; NaN when it is none of those. */
  function parse(text: string): number {
    const parts = text.trim().split(':')
    if (parts.length > 3 || parts.some((p) => !/^\d+(\.\d+)?$/.test(p.trim()))) return NaN
    return parts.reduce((secs, p) => secs * 60 + Number(p), 0)
  }

  function fmt(secs: number): string {
    const s = Math.round(secs)
    const h = Math.floor(s / 3600)
    const m = Math.floor((s % 3600) / 60)
    const sec = String(s % 60).padStart(2, '0')
    return h > 0 ? `${h}:${String(m).padStart(2, '0')}:${sec}` : `${m}:${sec}`
  }

  async function run(fn: () => Promise<ItemSegments>, done: string) {
    busy = true
    msg = ''
    try {
      take(await fn())
      msg = done
    } catch (err) {
      msg = errText(err)
    } finally {
      busy = false
    }
  }

  function save(kind: SegmentKind, label: string) {
    const a = parse(start[kind])
    const b = parse(end[kind])
    if (isNaN(a) || isNaN(b)) {
      msg = `Give the ${label.toLowerCase()}'s start and end as minutes and seconds, like 1:05.`
      return
    }
    if (b <= a) {
      msg = `The ${label.toLowerCase()} must end after it starts.`
      return
    }
    run(() => setItemSegment(episode.id, kind, { startSecs: a, endSecs: b }), `${label} saved. Re-detection will leave it alone.`)
  }

  async function redetect() {
    busy = true
    msg = ''
    try {
      await redetectSeason(episode.id)
      msg = 'This season will be detected again, starting once nothing is playing. Times set by hand are kept.'
    } catch (err) {
      msg = errText(err)
    } finally {
      busy = false
    }
  }

  function onKeydown(e: KeyboardEvent) {
    if (e.key === 'Escape') {
      e.preventDefault()
      e.stopPropagation()
      onclose()
    }
  }

  const code = $derived(`S${String(episode.seasonNum ?? 0).padStart(2, '0')}E${String(episode.episodeNum ?? 0).padStart(2, '0')}`)
</script>

<div class="seg-back" role="presentation" onclick={onclose}></div>
<div class="seg" role="dialog" aria-modal="true" aria-label="Intro and credits" tabindex="-1" bind:this={dialog} onkeydown={onKeydown}>
  <h2>Intro &amp; credits</h2>
  <p class="sub">{code} · {episode.title}{#if data?.runtimeSecs} · {fmt(data.runtimeSecs)} long{/if}</p>

  {#if loadErr}
    <p class="msg err">{loadErr}</p>
  {:else if !data}
    <p class="sub">Loading…</p>
  {:else}
    {#each KINDS as k (k.kind)}
      {@const s = stateOf(k.kind)}
      <section class="kind">
        <header>
          <strong>{k.label}</strong>
          <span class="state" class:locked={s?.locked}>{status(s)}</span>
        </header>
        <div class="times">
          <label>
            <span>Starts</span>
            <input type="text" inputmode="decimal" placeholder="m:ss" bind:value={start[k.kind]} disabled={busy} />
          </label>
          <label>
            <span>Ends</span>
            <input type="text" inputmode="decimal" placeholder="m:ss" bind:value={end[k.kind]} disabled={busy} />
          </label>
        </div>
        <div class="acts">
          <button type="button" class="btn primary" onclick={() => save(k.kind, k.label)} disabled={busy}>Save</button>
          <button
            type="button" class="btn"
            onclick={() => run(() => setItemSegment(episode.id, k.kind, { none: true }), `Marked as having no ${k.label.toLowerCase()}.`)}
            disabled={busy || (s?.locked && !s.found)}>{k.none}</button>
          {#if s?.locked}
            <button
              type="button" class="btn"
              title="Forget the times set by hand; detection looks at this episode again"
              onclick={() => run(() => clearItemSegment(episode.id, k.kind), `${k.label} handed back to detection.`)}
              disabled={busy}>Back to detection</button>
          {/if}
        </div>
      </section>
    {/each}
  {/if}

  {#if msg}<p class="msg" role="status">{msg}</p>{/if}

  <div class="foot">
    {#if data?.detection}
      <button
        type="button" class="btn"
        title="Detect every episode of this season again, keeping times set by hand"
        onclick={redetect} disabled={busy}>Re-detect season</button>
    {/if}
    <span class="spacer"></span>
    <button type="button" class="btn" onclick={onclose}>Close</button>
  </div>
</div>

<style>
  .seg-back {
    position: fixed;
    inset: 0;
    background: color-mix(in srgb, var(--bg) 78%, transparent);
    z-index: 400;
  }
  .seg {
    position: fixed;
    top: 50%;
    left: 50%;
    transform: translate(-50%, -50%);
    width: min(28rem, calc(100vw - 2rem));
    max-height: calc(100dvh - 2rem);
    overflow-y: auto;
    padding: 1rem;
    border-radius: 14px;
    border: 1px solid var(--border);
    background: var(--panel);
    color: var(--text);
    z-index: 401;
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
    outline: none;
  }
  h2 { margin: 0; font-size: 1.05rem; }
  .sub { margin: -0.4rem 0 0; color: var(--muted); font-size: 0.85rem; overflow-wrap: anywhere; }
  .kind {
    display: flex;
    flex-direction: column;
    gap: 0.55rem;
    padding: 0.75rem;
    border: 1px solid var(--border);
    border-radius: 10px;
  }
  .kind header { display: flex; align-items: baseline; justify-content: space-between; gap: 0.5rem; flex-wrap: wrap; }
  .state { color: var(--muted); font-size: 0.8rem; }
  .state.locked { color: var(--accent-2); }
  .times { display: flex; gap: 0.75rem; }
  .times label { flex: 1; display: flex; flex-direction: column; gap: 0.25rem; font-size: 0.8rem; color: var(--muted); }
  .times input {
    width: 100%;
    box-sizing: border-box;
    padding: 0.45rem 0.6rem;
    border-radius: 8px;
    border: 1px solid var(--border);
    background: var(--bg);
    color: var(--text);
    font: inherit;
    font-variant-numeric: tabular-nums;
  }
  .acts { display: flex; flex-wrap: wrap; gap: 0.5rem; }
  .msg { margin: 0; font-size: 0.85rem; color: var(--muted); }
  .msg.err { color: var(--err); }
  .foot { display: flex; align-items: center; gap: 0.5rem; flex-wrap: wrap; }
  .spacer { flex: 1; }
  .btn {
    padding: 0.45rem 0.9rem;
    border-radius: 8px;
    border: 1px solid var(--border);
    background: none;
    color: var(--text);
    font: inherit;
    font-size: 0.88rem;
    cursor: pointer;
  }
  .btn:hover:not(:disabled) { border-color: var(--accent); }
  .btn:disabled { opacity: 0.5; cursor: default; }
  /* White on the accent, as every primary button in both UIs. */
  .btn.primary { background: var(--accent); border-color: var(--accent); color: white; font-weight: 600; }
</style>
