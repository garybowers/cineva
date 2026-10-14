<script lang="ts">
  // A track's rating: five stars, the viewer's own. Choosing the lit star
  // again clears it. Where it sits in a track row it shows only once rated,
  // or while the row is pointed at or focused (the row's CSS decides, from
  // the "unrated" class); on a phone the track menu carries it instead.
  import { Star } from '@lucide/svelte'
  import type { Item } from '../lib/api'
  import { rate, stars } from './actions.svelte'

  let { track, size = 14 }: { track: Item; size?: number } = $props()

  const n = $derived(stars(track))
  let hover = $state(0)
  const lit = $derived(hover || n)
</script>

<span class="stars" class:unrated={n === 0} role="group" aria-label="Rating for {track.title}" onpointerleave={() => (hover = 0)}>
  {#each [1, 2, 3, 4, 5] as i (i)}
    <button
      class="star" class:on={i <= lit}
      aria-label={i === n ? `${i} ${i === 1 ? 'star' : 'stars'}, choose again to clear` : `${i} ${i === 1 ? 'star' : 'stars'}`}
      aria-pressed={i <= n}
      title={i === n ? 'Clear rating' : `${i} ${i === 1 ? 'star' : 'stars'}`}
      onpointerenter={() => (hover = i)}
      onclick={(e) => { e.stopPropagation(); rate(track, i) }}
    ><Star {size} /></button>
  {/each}
</span>

<style>
  .stars { display: inline-flex; align-items: center; flex: none; }
  .star {
    display: grid; place-items: center;
    padding: 0.15rem 0.08rem;
    border: 0; background: none; border-radius: 4px;
    color: color-mix(in srgb, var(--muted) 55%, transparent);
    cursor: pointer;
  }
  .star.on { color: var(--accent-2); }
  .star.on :global(svg) { fill: currentColor; }
  .star:focus-visible { outline: 2px solid var(--accent-2); outline-offset: 1px; }
</style>
