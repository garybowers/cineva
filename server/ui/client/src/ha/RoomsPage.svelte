<!--
  The Rooms page: the household's modes as large buttons, then every room
  with something this user may see, its controls and its cameras. All of
  it goes through Cineva, which holds the only Home Assistant credential.
-->
<script lang="ts">
  import { onMount } from 'svelte'
  import { CloudOff } from '@lucide/svelte'
  import type { HAEntity } from '../lib/api'
  import { camerasOf, controlsOf, ha, loadHA } from '../lib/homeassistant.svelte'
  import ControlList from './ControlList.svelte'
  import ModeButtons from './ModeButtons.svelte'
  import CameraTile from './CameraTile.svelte'
  import CameraLive from './CameraLive.svelte'

  // Fresh states on opening the page; events keep them current after that.
  onMount(() => {
    void loadHA()
  })

  const rooms = $derived(ha.rooms)
  const offline = $derived(!!rooms && !rooms.connected)
  const areas = $derived((rooms?.areas ?? []).filter((a) => a.entities.length > 0))
  let watching: HAEntity | null = $state(null)
</script>

<h1>Rooms</h1>

{#if offline}
  <div class="ha-note" role="status">
    <CloudOff size={16} />
    <span>Home Assistant cannot be reached just now, so the controls are switched off. They come back when it does.</span>
  </div>
{/if}

{#if ha.modes.length > 0}
  <section class="ha-sec">
    <h2>Modes</h2>
    <ModeButtons modes={ha.modes} disabled={offline} />
  </section>
{/if}

<!-- One column on a phone, as many as fit on a wider screen. -->
{#if areas.length > 0}
<div class="rooms">
{#each areas as a (a.id)}
  {@const controls = controlsOf(a)}
  {@const cameras = camerasOf(a)}
  <section class="ha-sec">
    <h2>
      {a.name || 'Elsewhere'}
      {#if a.id && a.id === rooms?.currentAreaId}<span class="here">This device</span>{/if}
    </h2>
    {#if controls.length > 0}
      <ControlList entities={controls} disabled={offline} />
    {/if}
    {#if cameras.length > 0}
      <div class="cams" class:after={controls.length > 0}>
        {#each cameras as c (c.entityId)}
          <CameraTile entity={c} disabled={offline} onopen={(e) => (watching = e)} />
        {/each}
      </div>
    {/if}
  </section>
{/each}
</div>
{:else if ha.modes.length === 0}
  <div class="ha-empty">Nothing from Home Assistant has been shared with you yet. An administrator chooses what appears here.</div>
{/if}

{#if watching}
  <CameraLive entity={watching} onclose={() => (watching = null)} />
{/if}

<style>
  h1 { font-size: 1.4rem; margin-bottom: 1rem; }
  h2 {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    font-size: 1rem;
    color: var(--muted);
    margin: 0 0 0.6rem;
  }
  .here {
    font-size: 0.72rem;
    font-weight: 500;
    padding: 0.1rem 0.5rem;
    border-radius: 999px;
    color: var(--accent-2);
    border: 1px solid color-mix(in srgb, var(--accent-2) 40%, transparent);
  }
  .ha-sec { margin-bottom: 1.6rem; max-width: 60rem; }
  .rooms {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(min(100%, 22rem), 1fr));
    gap: 1.6rem 1.25rem;
    align-items: start;
  }
  .rooms .ha-sec { margin-bottom: 0; max-width: none; min-width: 0; }
  .cams {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(min(100%, 15rem), 1fr));
    gap: 0.6rem;
  }
  .cams.after { margin-top: 0.75rem; }
  .ha-note {
    display: flex;
    align-items: flex-start;
    gap: 0.5rem;
    max-width: 60rem;
    margin-bottom: 1.2rem;
    padding: 0.7rem 0.9rem;
    border-radius: 12px;
    border: 1px solid var(--border);
    background: var(--panel);
    color: var(--muted);
    font-size: 0.9rem;
  }
  .ha-note :global(svg) { flex: none; margin-top: 0.1rem; }
  .ha-empty {
    background: var(--panel);
    border: 1px dashed var(--border);
    border-radius: 12px;
    padding: 1.5rem;
    color: var(--muted);
    font-size: 0.95rem;
  }
</style>
