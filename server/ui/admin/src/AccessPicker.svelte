<script lang="ts">
  // Who may see something: everyone, or only chosen users and groups. The
  // same control for libraries, Home Assistant entities and modes, since
  // they follow one access model. Admins always see everything, so they are
  // not offered.
  import type { LibraryAccess, User, UserGroup } from './lib/api'

  let {
    access = $bindable(),
    users,
    groups,
    noun,
  }: { access: LibraryAccess; users: User[]; groups: UserGroup[]; noun: string } = $props()

  const name = `access-${Math.random().toString(36).slice(2)}`
  const userName = (id: string) => {
    const u = users.find((x) => x.id === id)
    return u ? u.displayName || u.username : id.slice(0, 8)
  }
  const groupName = (id: string) => groups.find((g) => g.id === id)?.name ?? id.slice(0, 8)
  const chosenU = $derived(new Set(access.userIds))
  const chosenG = $derived(new Set(access.groupIds))

  // The picker's value is "u:<id>" or "g:<id>".
  function add(v: string) {
    const [kind, id] = [v.slice(0, 1), v.slice(2)]
    if (kind === 'u' && !chosenU.has(id)) access = { ...access, userIds: [...access.userIds, id] }
    if (kind === 'g' && !chosenG.has(id)) access = { ...access, groupIds: [...access.groupIds, id] }
  }
</script>

<div class="access">
  <label class="check"><input type="radio" {name} checked={!access.restricted} onchange={() => (access = { ...access, restricted: false })} /> Everyone</label>
  <label class="check"><input type="radio" {name} checked={access.restricted} onchange={() => (access = { ...access, restricted: true })} /> Only these people and groups</label>
  {#if access.restricted}
    <div class="principals">
      {#each access.groupIds as id (id)}
        <span class="chip">{groupName(id)} <span class="muted">group</span>
          <button type="button" class="chipx" aria-label="Remove {groupName(id)}" onclick={() => (access = { ...access, groupIds: access.groupIds.filter((x) => x !== id) })}>✕</button></span>
      {/each}
      {#each access.userIds as id (id)}
        <span class="chip">{userName(id)}
          <button type="button" class="chipx" aria-label="Remove {userName(id)}" onclick={() => (access = { ...access, userIds: access.userIds.filter((x) => x !== id) })}>✕</button></span>
      {/each}
      <select aria-label="Add a user or group" onchange={(e) => { const el = e.currentTarget; if (el.value) add(el.value); el.value = '' }}>
        <option value="">+ Add a user or group…</option>
        {#if groups.some((g) => !chosenG.has(g.id))}
          <optgroup label="Groups">
            {#each groups.filter((g) => !chosenG.has(g.id)) as g (g.id)}<option value="g:{g.id}">{g.name}</option>{/each}
          </optgroup>
        {/if}
        <optgroup label="Users">
          {#each users.filter((u) => u.role !== 'admin' && !chosenU.has(u.id)) as u (u.id)}<option value="u:{u.id}">{u.displayName || u.username}</option>{/each}
        </optgroup>
      </select>
    </div>
    {#if access.userIds.length === 0 && access.groupIds.length === 0}
      <p class="hint">Nobody listed: only admins will see this {noun}.</p>
    {/if}
  {/if}
</div>

<style>
  .access { display: flex; flex-direction: column; gap: 0.6rem; }
  .check { display: flex; align-items: center; gap: 0.5rem; color: var(--text); font-size: 0.9rem; }
  .principals { display: flex; flex-wrap: wrap; gap: 0.4rem; align-items: center; }
  .chip {
    padding: 0.3rem 0.75rem;
    border-radius: 999px;
    border: 1px solid var(--accent);
    color: var(--text);
    background: color-mix(in srgb, var(--accent) 18%, transparent);
    font-size: 0.85rem;
  }
  .chip .muted { color: var(--muted); font-size: 0.78rem; }
  .chipx { margin-left: 0.3rem; color: var(--muted); font-size: 0.75rem; }
  .chipx:hover { color: var(--err); }
  select {
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: 8px;
    color: var(--text);
    padding: 0.4rem 0.6rem;
    font: inherit;
    font-size: 0.85rem;
    max-width: 100%;
  }
  .hint { color: var(--muted); font-size: 0.8rem; }
</style>
