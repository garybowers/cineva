<!--
  The player's room panel: the controls of the room this device is in,
  and the modes, this room's first, over the picture. A mode that plays the
  title it is started from plays what is on screen.
-->
<script lang="ts">
  import { CloudOff } from '@lucide/svelte'
  import type { HAMode } from '../lib/api'
  import { areaName, controlsOf, currentRoom, ha, modesForHere } from '../lib/homeassistant.svelte'
  import Sheet from '../Sheet.svelte'
  import ControlList from './ControlList.svelte'
  import ModeButtons from './ModeButtons.svelte'

  let { itemId, onclose, onmodestart }: {
    /** What is playing, for a mode that plays the title it is started from. */
    itemId: string
    onclose: () => void
    onmodestart?: (mode: HAMode, runId: string) => void
  } = $props()

  const areaId = $derived(ha.rooms?.currentAreaId ?? '')
  const room = $derived(currentRoom())
  const controls = $derived(room ? controlsOf(room) : [])
  const modes = $derived(modesForHere())
  const offline = $derived(!!ha.rooms && !ha.rooms.connected)
</script>

<Sheet title={room?.name || areaName(areaId) || 'Modes'} {onclose}>
  {#if offline}
    <p class="ha-off" role="status"><CloudOff size={15} /> Home Assistant cannot be reached just now, so the controls are switched off.</p>
  {/if}
  {#if modes.length > 0}
    <div class="part">
      <ModeButtons {modes} {itemId} disabled={offline} onstart={onmodestart} />
    </div>
  {/if}
  {#if controls.length > 0}
    <ControlList entities={controls} disabled={offline} />
  {:else if modes.length === 0}
    <p class="ha-off">Nothing in this room has been shared with you.</p>
  {/if}
</Sheet>

<style>
  .part { margin-bottom: 0.75rem; }
  .ha-off {
    display: flex;
    align-items: flex-start;
    gap: 0.45rem;
    margin: 0 0 0.75rem;
    color: var(--muted);
    font-size: 0.85rem;
  }
  .ha-off :global(svg) { flex: none; margin-top: 0.1rem; }
</style>
