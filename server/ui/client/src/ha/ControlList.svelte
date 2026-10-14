<!-- A room's controls, one row each, in a card. -->
<script lang="ts">
  import type { HAEntity } from '../lib/api'
  import EntityControl from './EntityControl.svelte'

  let { entities, disabled = false }: { entities: HAEntity[]; disabled?: boolean } = $props()
</script>

<div class="ctl-list">
  {#each entities as e (e.entityId)}
    <EntityControl entity={e} {disabled} />
  {/each}
</div>

<style>
  .ctl-list {
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: 14px;
    overflow: hidden;
  }
  .ctl-list > :global(* + *) { border-top: 1px solid var(--border); }
</style>
