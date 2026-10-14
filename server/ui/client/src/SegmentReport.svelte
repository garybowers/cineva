<!--
  "Report wrong intro or credits times", from the player: which skip is in
  the wrong place, where playback was, and an optional note. The
  administrator sees it on the Activity page with the editor a click away.
-->
<script lang="ts">
  import { onMount } from 'svelte'
  import { reportSegment, type Item, type SegmentKind } from './lib/api'

  let { item, positionSecs, durationSecs, onclose }: {
    item: Item
    /** Where playback was when the report was opened. */
    positionSecs: number
    durationSecs: number
    onclose: () => void
  } = $props()

  const film = $derived(item.kind === 'movie')
  // A guess the viewer can change: past the middle, it is probably the
  // credits they mean. A film has nothing else to report.
  // svelte-ignore state_referenced_locally
  let kind: SegmentKind = $state(item.kind === 'movie' || (durationSecs > 0 && positionSecs > durationSecs / 2) ? 'credits' : 'intro')
  let note = $state('')
  let busy = $state(false)
  let sent = $state(false)
  let err = $state('')
  let dialog: HTMLDivElement

  onMount(() => dialog?.focus())

  function fmt(secs: number): string {
    const s = Math.max(0, Math.round(secs))
    const h = Math.floor(s / 3600)
    const m = Math.floor((s % 3600) / 60)
    const sec = String(s % 60).padStart(2, '0')
    return h > 0 ? `${h}:${String(m).padStart(2, '0')}:${sec}` : `${m}:${sec}`
  }

  async function send(e: SubmitEvent) {
    e.preventDefault()
    busy = true
    err = ''
    try {
      await reportSegment(item.id, { kind, positionSecs, note: note.trim() || undefined })
      sent = true
    } catch (e) {
      err = e instanceof Error ? e.message : String(e)
    } finally {
      busy = false
    }
  }

  function onKeydown(e: KeyboardEvent) {
    // Kept from the player's own shortcuts: space, arrows and f belong to
    // the form while it is open.
    e.stopPropagation()
    if (e.key === 'Escape') {
      e.preventDefault()
      onclose()
    }
  }
</script>

<div class="rep-back" role="presentation" onclick={onclose}></div>
<div class="rep" role="dialog" aria-modal="true" aria-label="Report wrong intro or credits times" tabindex="-1"
  bind:this={dialog} onkeydown={onKeydown}>
  <h2>Wrong {film ? 'credits' : 'intro or credits'} times</h2>
  {#if sent}
    <p class="sub">Thank you. An administrator will look at {film ? 'this film' : 'this episode'}.</p>
    <div class="foot"><button type="button" class="btn primary" onclick={onclose}>Close</button></div>
  {:else}
    <p class="sub">Reported at {fmt(positionSecs)}. Say what is wrong and an administrator will correct it.</p>
    <form onsubmit={send}>
      {#if !film}
        <fieldset>
          <legend>What is wrong</legend>
          <label><input type="radio" bind:group={kind} value="intro" /> The intro skip</label>
          <label><input type="radio" bind:group={kind} value="credits" /> The credits</label>
        </fieldset>
      {/if}
      <label class="note">
        <span>Note (optional)</span>
        <textarea rows="3" maxlength="500" bind:value={note} placeholder="Skips 10 seconds too far"></textarea>
      </label>
      {#if err}<p class="err" role="alert">{err}</p>{/if}
      <div class="foot">
        <button type="submit" class="btn primary" disabled={busy}>{busy ? 'Sending…' : 'Send report'}</button>
        <button type="button" class="btn" onclick={onclose}>Cancel</button>
      </div>
    </form>
  {/if}
</div>

<style>
  /* Fixed rather than inside the picture, which on a phone is too short
     for a form; with the page fullscreen, the viewport is the screen. */
  .rep-back {
    position: fixed;
    inset: 0;
    background: color-mix(in srgb, var(--bg) 60%, transparent);
    z-index: 500;
  }
  .rep {
    position: fixed;
    top: 50%;
    left: 50%;
    transform: translate(-50%, -50%);
    width: min(24rem, calc(100vw - 2rem));
    max-height: calc(100dvh - 2rem);
    overflow-y: auto;
    padding: 1rem;
    border-radius: 14px;
    border: 1px solid var(--border);
    background: var(--panel);
    color: var(--text);
    z-index: 501;
    display: flex;
    flex-direction: column;
    gap: 0.6rem;
    outline: none;
    text-align: left;
  }
  h2 { margin: 0; font-size: 1.05rem; }
  .sub { margin: 0; color: var(--muted); font-size: 0.85rem; }
  form { display: flex; flex-direction: column; gap: 0.6rem; }
  fieldset { border: 0; padding: 0; margin: 0; display: flex; flex-wrap: wrap; gap: 0.4rem 1rem; }
  legend { padding: 0; margin-bottom: 0.3rem; font-size: 0.8rem; color: var(--muted); }
  fieldset label { display: inline-flex; align-items: center; gap: 0.35rem; font-size: 0.9rem; }
  .note { display: flex; flex-direction: column; gap: 0.25rem; font-size: 0.8rem; color: var(--muted); }
  textarea {
    width: 100%;
    box-sizing: border-box;
    padding: 0.45rem 0.6rem;
    border-radius: 8px;
    border: 1px solid var(--border);
    background: var(--bg);
    color: var(--text);
    font: inherit;
    font-size: 0.9rem;
    resize: vertical;
  }
  .err { margin: 0; color: var(--err); font-size: 0.85rem; }
  .foot { display: flex; gap: 0.5rem; flex-wrap: wrap; }
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
