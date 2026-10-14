<script lang="ts">
  // Shown instead of the admin pages on a TV: administration is for a
  // computer or phone (see docs/tv.md), and a remote cannot work these
  // forms anyway. Not a lock: a TV is only a browser, and the admin API
  // answers any admin's request.
  import { onMount } from 'svelte'
  let back: HTMLAnchorElement
  onMount(() => back.focus())
</script>

<main class="tvnote">
  <h1>The admin pages are for a computer or phone</h1>
  <p>Open Cineva's address there and go to Administration. This screen is set up as a TV.</p>
  <a href="/" bind:this={back}>Back to Cineva</a>
</main>

<style>
  .tvnote {
    min-height: 100vh;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 1.2rem;
    padding: 2rem;
    text-align: center;
    font-size: clamp(16px, 1.4vw, 40px);
  }
  h1 { font-size: 2em; margin: 0; }
  p { color: var(--muted); margin: 0; max-width: 36em; }
  a {
    margin-top: 0.8rem;
    padding: 0.7em 1.4em;
    border-radius: 12px;
    background: var(--accent);
    color: var(--on-accent);
    text-decoration: none;
    font-weight: 600;
  }
  a:focus { outline: 0.2em solid var(--accent-2); outline-offset: 0.2em; }
</style>
