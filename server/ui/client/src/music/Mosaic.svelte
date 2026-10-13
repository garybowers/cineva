<script lang="ts">
  // A playlist's artwork: four album covers in a square, or one when it has
  // fewer, or an icon when it has none.
  import { ListMusic, Sparkles } from '@lucide/svelte'

  let { covers, smart = false, size = '100%' }: { covers: string[]; smart?: boolean; size?: string } = $props()
</script>

<span class="mosaic" class:four={covers.length >= 4} style="width: {size}">
  {#if covers.length >= 4}
    {#each covers.slice(0, 4) as c (c)}<img src={c} alt="" loading="lazy" />{/each}
  {:else if covers.length > 0}
    <img src={covers[0]} alt="" loading="lazy" />
  {:else}
    <span class="ph">{#if smart}<Sparkles size={28} />{:else}<ListMusic size={28} />{/if}</span>
  {/if}
  {#if smart && covers.length > 0}<span class="badge" title="Smart playlist"><Sparkles size={13} /></span>{/if}
</span>

<style>
  .mosaic {
    position: relative; display: grid; aspect-ratio: 1; border-radius: 10px; overflow: hidden; flex: none;
    border: 1px solid var(--border); background: var(--panel);
  }
  .mosaic.four { grid-template-columns: 1fr 1fr; grid-template-rows: 1fr 1fr; }
  img { width: 100%; height: 100%; object-fit: cover; display: block; min-width: 0; min-height: 0; }
  .ph { display: grid; place-items: center; color: var(--muted); background: color-mix(in srgb, var(--accent) 10%, var(--panel)); }
  .badge {
    position: absolute; left: 0.35rem; top: 0.35rem; display: grid; place-items: center;
    width: 1.5rem; height: 1.5rem; border-radius: 50%; background: var(--panel); color: var(--accent-2);
  }
</style>
