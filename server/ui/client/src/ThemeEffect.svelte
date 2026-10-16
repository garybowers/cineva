<!--
  A theme's animated touch, behind the page: snow, drifting leaves (and the
  odd bat), or falling blossom. Decoration only: it takes no clicks, is
  hidden from screen readers, draws in the theme's own colours, and is not
  shown at all when the system asks for reduced motion (the CSS below), so
  the caller only decides whether the moment suits it.
-->
<script lang="ts">
  import type { Effect } from './lib/theme'

  let { kind }: { kind: Effect } = $props()

  // Each piece: where it starts across the screen, how long it takes to
  // fall, when it starts, how big it is and how far it sways. Fixed per
  // page load rather than per frame: the browser animates them.
  type Piece = { left: number; dur: number; delay: number; size: number; sway: number; spin: number; tone: number }
  function pieces(n: number, minDur: number, maxDur: number, minSize: number, maxSize: number): Piece[] {
    return Array.from({ length: n }, () => ({
      left: Math.random() * 100,
      dur: minDur + Math.random() * (maxDur - minDur),
      delay: -Math.random() * maxDur,
      size: minSize + Math.random() * (maxSize - minSize),
      sway: 2 + Math.random() * 6,
      spin: (Math.random() < 0.5 ? -1 : 1) * (180 + Math.random() * 360),
      tone: Math.floor(Math.random() * 3),
    }))
  }
  const counts: Record<Exclude<Effect, ''>, number> = { snow: 46, leaves: 16, blossom: 22 }
  const list = $derived(
    kind === 'snow' ? pieces(counts.snow, 14, 30, 2, 6)
      : kind === 'leaves' ? pieces(counts.leaves, 16, 28, 14, 24)
        : kind === 'blossom' ? pieces(counts.blossom, 14, 26, 8, 14)
          : [],
  )
</script>

{#if kind}
  <div class="effect {kind}" aria-hidden="true">
    {#each list as p, i (i)}
      <span
        class="piece tone{p.tone}"
        style="left: {p.left}%; width: {p.size}px; height: {p.size}px; animation-duration: {p.dur}s; animation-delay: {p.delay}s; --sway: {p.sway}vw; --spin: {p.spin}deg"
      >
        {#if kind === 'leaves'}
          <svg viewBox="0 0 24 24"><path d="M12 2c4 3 7 7 7 11a7 7 0 0 1-14 0c0-4 3-8 7-11zm0 4v15" /></svg>
        {/if}
      </span>
    {/each}
    {#if kind === 'leaves'}
      {#each [0, 1] as b (b)}
        <svg class="bat b{b}" viewBox="-40 -16 80 32"><path d="M0 0 C -10 -12 -24 -12 -36 -4 C -30 -2 -27 4 -28 10 C -20 4 -12 6 -8 12 C -5 6 -2 4 0 6 C 2 4 5 6 8 12 C 12 6 20 4 28 10 C 27 4 30 -2 36 -4 C 24 -12 10 -12 0 0 Z" /></svg>
      {/each}
    {/if}
  </div>
{/if}

<style>
  .effect {
    position: fixed;
    inset: 0;
    z-index: 0;
    overflow: hidden;
    pointer-events: none;
  }
  .piece {
    position: absolute;
    top: -40px;
    display: block;
    animation-name: fall;
    animation-timing-function: linear;
    animation-iteration-count: infinite;
    will-change: transform;
  }
  @keyframes fall {
    0% { transform: translate3d(0, 0, 0) rotate(0deg); }
    25% { transform: translate3d(var(--sway), 27vh, 0) rotate(calc(var(--spin) * 0.25)); }
    50% { transform: translate3d(0, 54vh, 0) rotate(calc(var(--spin) * 0.5)); }
    75% { transform: translate3d(calc(var(--sway) * -1), 81vh, 0) rotate(calc(var(--spin) * 0.75)); }
    100% { transform: translate3d(0, calc(100vh + 60px), 0) rotate(var(--spin)); }
  }

  /* Snow: soft flakes in the theme's text colour. */
  .snow .piece {
    border-radius: 50%;
    background: var(--text);
    opacity: 0.55;
    filter: blur(0.5px);
  }
  .snow .tone1 { opacity: 0.35; }
  .snow .tone2 { opacity: 0.75; }

  /* Leaves: the accent and a darker mix of it. */
  .leaves .piece svg { width: 100%; height: 100%; display: block; }
  .leaves .piece path { stroke: color-mix(in srgb, var(--accent) 60%, var(--bg)); stroke-width: 1; }
  .leaves .tone0 path { fill: var(--accent); }
  .leaves .tone1 path { fill: color-mix(in srgb, var(--accent) 70%, var(--err)); }
  .leaves .tone2 path { fill: color-mix(in srgb, var(--accent) 55%, var(--bg)); }
  .leaves .piece { opacity: 0.7; }

  /* Bats: two, crossing now and then, high up. */
  .bat {
    position: absolute;
    width: 46px;
    height: 18px;
    fill: color-mix(in srgb, var(--bg) 80%, var(--accent-2));
    opacity: 0.8;
    animation: fly 38s linear infinite;
  }
  .bat.b0 { top: 14vh; }
  .bat.b1 { top: 24vh; width: 32px; animation-delay: -19s; animation-duration: 47s; }
  @keyframes fly {
    0% { transform: translateX(-10vw) translateY(0); }
    10% { transform: translateX(10vw) translateY(-3vh); }
    20% { transform: translateX(30vw) translateY(1vh); }
    30% { transform: translateX(50vw) translateY(-2vh); }
    40% { transform: translateX(70vw) translateY(2vh); }
    50% { transform: translateX(110vw) translateY(0); }
    100% { transform: translateX(110vw) translateY(0); }
  }

  /* Blossom: petals in a pink drawn from the theme. */
  .blossom .piece {
    border-radius: 80% 0 80% 0;
    background: color-mix(in srgb, var(--err) 35%, var(--panel));
    opacity: 0.8;
  }
  .blossom .tone1 { background: color-mix(in srgb, var(--accent) 30%, var(--panel)); }
  .blossom .tone2 { background: var(--panel); border: 1px solid color-mix(in srgb, var(--err) 25%, var(--panel)); }

  @media (prefers-reduced-motion: reduce) {
    .effect { display: none; }
  }
</style>
