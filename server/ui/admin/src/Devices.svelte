<script lang="ts">
  // The Devices page: every screen Cineva knows, signed in on or not. A
  // device outlives its sessions (docs/devices.md), so this is where the
  // living-room TV is named, put in a room, set to sign in by itself, or
  // forgotten.
  import { onMount } from 'svelte'
  import {
    listAllDevices, updateDevice, forgetDevice, haRooms,
    type AdminDevice, type User,
  } from './lib/api'
  import Modal from './Modal.svelte'
  import { Pencil, Trash, LogIn } from '@lucide/svelte'

  let { users, homeAssistant = false }: {
    users: User[]
    /** Home Assistant is set up, so devices can be put in rooms. */
    homeAssistant?: boolean
  } = $props()

  let devices: AdminDevice[] = $state([])
  let areas: { id: string; name: string }[] = $state([])
  let loaded = $state(false)
  let err = $state('')
  const errText = (e: unknown) => (e instanceof Error ? e.message : String(e))

  async function load() {
    try {
      devices = await listAllDevices()
      if (homeAssistant) areas = (await haRooms().catch(() => null))?.allAreas ?? []
      err = ''
    } catch (e) {
      err = errText(e)
    } finally {
      loaded = true
    }
  }
  onMount(load)
  /** From the event stream: something about devices changed. */
  export function refresh() {
    void load()
  }

  // A device signs in by itself only as someone who is neither an admin nor
  // disabled: whoever sits in front of it has that account.
  const autoCandidates = $derived(users.filter((u) => u.role !== 'admin' && !u.disabled))
  const userName = (id: string) => {
    const u = users.find((x) => x.id === id)
    return u ? u.displayName || u.username : 'someone removed'
  }
  const label = (d: AdminDevice) => d.name || d.client || 'Unknown browser'

  function seen(iso: string): string {
    const t = Date.parse(iso.includes('T') ? iso : iso.replace(' ', 'T') + 'Z')
    const mins = Math.round((Date.now() - t) / 60000)
    if (mins < 2) return 'just now'
    if (mins < 60) return `${mins} min ago`
    const hours = Math.round(mins / 60)
    if (hours < 48) return `${hours} h ago`
    return `${Math.round(hours / 24)} days ago`
  }

  async function change(d: AdminDevice, patch: { name?: string; areaId?: string; autoUserId?: string }) {
    err = ''
    try {
      const updated = await updateDevice(d.id, patch)
      devices = devices.map((x) => (x.id === d.id ? { ...x, ...updated, userName: x.userName, online: x.online } : x))
    } catch (e) {
      err = `${label(d)}: ${errText(e)}`
      void load()
    }
  }

  // Renaming, in a dialogue.
  let renaming: { device: AdminDevice; name: string } | null = $state(null)
  let renameErr = $state('')
  async function saveName(e: Event) {
    e.preventDefault()
    if (!renaming) return
    renameErr = ''
    try {
      await updateDevice(renaming.device.id, { name: renaming.name })
      renaming = null
      void load()
    } catch (err) {
      renameErr = errText(err)
    }
  }

  // Forgetting, confirmed first.
  let forgetting: AdminDevice | null = $state(null)
  let busy = $state(false)
  async function forget() {
    if (!forgetting) return
    busy = true
    try {
      await forgetDevice(forgetting.id)
      devices = devices.filter((d) => d.id !== forgetting?.id)
      forgetting = null
    } catch (e) {
      err = errText(e)
    } finally {
      busy = false
    }
  }
</script>

<h1>Devices</h1>
<p class="hint intro">
  Every screen Cineva knows. A device keeps its name, room and settings when people sign out or switch profile, and
  holds one person at a time. One that signs in by itself becomes that person whenever nobody is signed in on it, with
  the profile picker still there to switch.
</p>
{#if err}<div class="error slim">{err}</div>{/if}

{#if !loaded}
  <div class="empty">Loading…</div>
{:else if devices.length === 0}
  <div class="empty">No devices yet. One appears when someone signs in on it.</div>
{:else}
  <ul class="rows">
    {#each devices as d (d.id)}
      <li class="drow">
        <span class="dot" class:on={d.online} title={d.online ? 'Cineva open now' : 'Not open'} aria-hidden="true"></span>
        <span class="dtext">
          <span class="dname">{label(d)}</span>
          <span class="muted small">
            {d.name ? `${d.client} · ` : ''}{d.online ? 'open now' : `seen ${seen(d.lastSeenAt)}`}
          </span>
          <span class="small who">
            {#if d.userId}Signed in: {d.userName || userName(d.userId)}{:else}<span class="muted">Nobody signed in</span>{/if}
          </span>
        </span>
        <div class="dctl">
          <label class="field">
            <span class="muted small"><LogIn size={13} /> Signs in as</span>
            <select value={d.autoUserId} onchange={(ev) => change(d, { autoUserId: ev.currentTarget.value })}
              aria-label="Who {label(d)} signs in as">
              <option value="">Nobody (sign-in screen)</option>
              {#if d.autoUserId && !autoCandidates.some((u) => u.id === d.autoUserId)}
                <option value={d.autoUserId}>{userName(d.autoUserId)} (cannot be used)</option>
              {/if}
              {#each autoCandidates as u (u.id)}<option value={u.id}>{u.displayName || u.username}</option>{/each}
            </select>
          </label>
          {#if homeAssistant}
            <label class="field">
              <span class="muted small">Room</span>
              <select value={d.areaId} onchange={(ev) => change(d, { areaId: ev.currentTarget.value })} aria-label="Room for {label(d)}">
                <option value="">No room</option>
                {#if d.areaId && !areas.some((a) => a.id === d.areaId)}<option value={d.areaId}>{d.areaId}</option>{/if}
                {#each areas as a (a.id)}<option value={a.id}>{a.name}</option>{/each}
              </select>
            </label>
          {/if}
          <div class="acts">
            <button class="lib-btn ibtn" onclick={() => { renaming = { device: d, name: d.name }; renameErr = '' }}>
              <Pencil size={15} /> Rename
            </button>
            <button class="lib-btn ibtn danger" onclick={() => (forgetting = d)}><Trash size={15} /> Forget</button>
          </div>
        </div>
      </li>
    {/each}
  </ul>
  <p class="hint foot">
    A device that signs in by itself trusts whoever is in front of it with that account, and its browser's device
    cookie signs that account in. Choose an account made for the screen, such as one for the family, with only the
    libraries everyone may see. Admin accounts cannot be chosen.
  </p>
{/if}

{#if renaming}
  <Modal title="Rename {label(renaming.device)}" onclose={() => (renaming = null)}>
    <form id="rename-device" class="form" onsubmit={saveName}>
      <label>Name
        <input bind:value={renaming.name} maxlength="40" placeholder={renaming.device.client || 'Living-room TV'} />
      </label>
      <p class="hint">Blank goes back to the browser's own name.</p>
      {#if renameErr}<div class="error slim">{renameErr}</div>{/if}
    </form>
    {#snippet foot()}
      <button type="button" class="lib-btn" onclick={() => (renaming = null)}>Cancel</button>
      <button class="primary" type="submit" form="rename-device">Save</button>
    {/snippet}
  </Modal>
{/if}

{#if forgetting}
  {@const d = forgetting}
  <Modal title="Forget {label(d)}?" onclose={() => (forgetting = null)}>
    <p>
      Its name, room and settings go{d.userId ? `, and ${d.userName || userName(d.userId)} is signed out of it` : ''}.
      Modes that play on it will need another device. If it signs in again it starts as a new device.
    </p>
    {#snippet foot()}
      <button type="button" class="lib-btn" onclick={() => (forgetting = null)}>Cancel</button>
      <button type="button" class="lib-btn ibtn danger solid" disabled={busy} onclick={forget}>
        <Trash size={15} /> {busy ? 'Forgetting…' : 'Forget'}
      </button>
    {/snippet}
  </Modal>
{/if}

<style>
  h1 { font-size: 1.35rem; margin-bottom: 0.6rem; }
  .intro { margin-bottom: 1rem; max-width: 52rem; }
  .foot { margin-top: 0.9rem; max-width: 52rem; }
  .hint { color: var(--muted); font-size: 0.8rem; }
  .muted { color: var(--muted); }
  .small { font-size: 0.78rem; }
  .empty {
    background: var(--panel); border: 1px dashed var(--border); border-radius: 12px;
    padding: 1.5rem; color: var(--muted);
  }
  .error {
    background: color-mix(in srgb, var(--err) 15%, transparent); border: 1px solid var(--err);
    border-radius: 8px; padding: 0.5rem 0.8rem; margin-bottom: 0.8rem;
  }

  /* On a phone each device stacks: name, then its settings underneath;
     wider, the settings sit to the right on the same line. */
  .rows { list-style: none; background: var(--panel); border: 1px solid var(--border); border-radius: 12px; }
  .rows > li + li { border-top: 1px solid var(--border); }
  .drow {
    display: grid; grid-template-columns: auto minmax(0, 1fr); align-items: start;
    gap: 0.6rem 0.75rem; padding: 0.8rem 1rem;
  }
  .dot { width: 0.6rem; height: 0.6rem; margin-top: 0.4rem; border-radius: 50%; background: var(--border); }
  .dot.on { background: var(--ok); }
  .dtext { display: flex; flex-direction: column; gap: 0.15rem; min-width: 0; }
  .dname { overflow-wrap: anywhere; font-weight: 500; }
  .who { color: var(--text); }
  .dctl { grid-column: 2; display: flex; flex-wrap: wrap; align-items: flex-end; gap: 0.6rem 0.9rem; }
  .field { display: flex; flex-direction: column; gap: 0.25rem; min-width: 0; flex: 1 1 12rem; }
  .field > span { display: inline-flex; align-items: center; gap: 0.3rem; }
  .field select {
    background: var(--bg); border: 1px solid var(--border); border-radius: 8px;
    color: var(--text); padding: 0.45rem 0.65rem; font: inherit; font-size: 0.9rem; min-width: 0;
  }
  .acts { display: flex; gap: 0.5rem; flex-wrap: wrap; }
  @media (min-width: 64rem) {
    .drow { grid-template-columns: auto minmax(12rem, 1fr) auto; align-items: center; }
    .dctl { grid-column: auto; flex-wrap: nowrap; align-items: flex-end; }
    .field { flex: 0 0 15rem; }
  }

  .form { display: flex; flex-direction: column; gap: 0.6rem; }
  .form label { display: flex; flex-direction: column; gap: 0.3rem; font-size: 0.9rem; color: var(--muted); }
  .form input {
    background: var(--bg); border: 1px solid var(--border); border-radius: 8px;
    color: var(--text); padding: 0.5rem 0.7rem; font: inherit;
  }

  .lib-btn {
    background: var(--bg); border: 1px solid var(--border); border-radius: 8px;
    color: var(--text); padding: 0.4rem 0.75rem; font: inherit; font-size: 0.875rem;
  }
  .lib-btn:hover { border-color: var(--accent); }
  .lib-btn.danger { color: var(--err); border-color: color-mix(in srgb, var(--err) 45%, var(--border)); }
  .lib-btn.danger:hover { border-color: var(--err); }
  .lib-btn.danger.solid { background: var(--err); border-color: var(--err); color: var(--bg); }
  .lib-btn:disabled { opacity: 0.5; }
  .ibtn { display: inline-flex; align-items: center; gap: 0.4rem; }
  .primary { background: var(--accent); color: var(--on-accent, var(--bg)); padding: 0.45rem 0.9rem; border-radius: 8px; }
</style>
