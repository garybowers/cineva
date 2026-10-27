<script lang="ts">
  // A track's lyrics, from its tags or an .lrc file beside it: timed lines
  // follow the music (the current one lit and kept in view); plain text
  // just reads.
  import { itemLyrics, type Item, type Lyrics } from '../../lib/api'
  import { mq } from './queue.svelte'

  let { track }: { track: Item } = $props()

  let lyrics = $state<Lyrics | null | undefined>(undefined)
  $effect(() => {
    const id = track.id
    lyrics = undefined
    itemLyrics(id)
      .then((l) => { if (id === track.id) lyrics = l })
      .catch(() => (lyrics = null))
  })

  // "[01:23.45] words" lines, with their times.
  const lines = $derived.by(() => {
    if (!lyrics) return []
    const out: { at: number; text: string }[] = []
    for (const raw of lyrics.text.split('\n')) {
      const m = raw.match(/^\[(\d{1,3}):(\d{2})(?:[.:](\d{1,3}))?\](.*)$/)
      if (m && lyrics.synced) {
        const frac = m[3] ? Number(`0.${m[3]}`) : 0
        out.push({ at: Number(m[1]) * 60 + Number(m[2]) + frac, text: m[4].trim() })
      } else if (!lyrics.synced) {
        out.push({ at: -1, text: raw })
      }
    }
    return out
  })
  const currentLine = $derived.by(() => {
    if (!lyrics?.synced) return -1
    let i = -1
    for (let k = 0; k < lines.length; k++) if (lines[k].at <= mq.positionSecs) i = k
    return i
  })
  let box = $state<HTMLElement | null>(null)
  $effect(() => {
    const i = currentLine
    if (i < 0 || !box) return
    box.querySelector<HTMLElement>(`[data-line="${i}"]`)?.scrollIntoView({ block: 'center', behavior: 'smooth' })
  })
</script>

<div class="lyrics" bind:this={box}>
  {#if lyrics === undefined}
    <p class="muted">Looking for lyrics…</p>
  {:else if !lyrics}
    <p class="muted">No lyrics for this track. Lyrics come from the file's tags or an .lrc file beside it.</p>
  {:else if lyrics.synced}
    {#each lines as l, i (i)}
      <p class="line" class:on={i === currentLine} class:past={i < currentLine} data-line={i}>
        <button class="seekline" onclick={() => mq.controls?.seek(l.at)}>{l.text || '…'}</button>
      </p>
    {/each}
  {:else}
    {#each lines as l, i (i)}
      <p class="line plain">{l.text}</p>
    {/each}
  {/if}
</div>

<style>
  .lyrics { max-width: 40rem; margin: 0 auto; padding: 0.5rem 0 4rem; font-size: 1.05rem; line-height: 1.5; }
  .muted { color: var(--muted); text-align: center; }
  .line { margin: 0.35rem 0; transition: color 0.2s ease, opacity 0.2s ease; }
  .line.plain { white-space: pre-wrap; }
  .seekline { text-align: left; padding: 0.1rem 0; color: var(--muted); font-size: inherit; line-height: inherit; }
  .line.on .seekline { color: var(--text); font-weight: 600; font-size: 1.15em; }
  .line.past .seekline { opacity: 0.6; }
  .seekline:hover { color: var(--accent-2); }
</style>
