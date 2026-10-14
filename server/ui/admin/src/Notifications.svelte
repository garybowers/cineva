<script lang="ts">
  // The Notifications page (docs/notifications.md): the household's
  // destinations, the events each receives, and the two settings that
  // shape every message: the public address links point at, and the ntfy
  // server people may use for their own notifications. The form for a
  // destination is drawn from its kind's fields as the server lists them,
  // so a new kind needs no change here beyond, at most, an icon.
  import { onMount } from 'svelte'
  import {
    getNotifications, putNotifySettings, createNotifyDestination, updateNotifyDestination,
    deleteNotifyDestination, testNotifyDestination,
    type NotificationsOverview, type NotifyDestination, type NotifyKind, type NotifyKindId, type Library,
  } from './lib/api'
  import Modal from './Modal.svelte'
  import {
    Plus, Bell, MessageSquare, MessageCircle, Send, Webhook, ChevronRight, Pencil, Trash, RefreshCw,
  } from '@lucide/svelte'

  let { libraries }: { libraries: Library[] } = $props()

  let data: NotificationsOverview | null = $state(null)
  let loaded = $state(false)
  let err = $state('')
  const errText = (e: unknown) => (e instanceof Error ? e.message : String(e))

  // The settings as the form holds them; refreshed from the server unless
  // the admin is part-way through changing them.
  let publicUrl = $state('')
  let ntfyServer = $state('')
  const settingsDirty = $derived(
    !!data && (publicUrl.trim() !== data.settings.publicUrl || ntfyServer.trim() !== data.settings.ntfyServer),
  )

  async function load() {
    try {
      const next = await getNotifications()
      if (!data || !settingsDirty) {
        publicUrl = next.settings.publicUrl
        ntfyServer = next.settings.ntfyServer
      }
      data = next
      err = ''
    } catch (e) {
      err = errText(e)
    } finally {
      loaded = true
    }
  }
  onMount(load)

  // ---- Settings ----
  let settingsSaving = $state(false)
  let settingsErr = $state('')
  let settingsSaved = $state(false)
  async function saveSettings(e: Event) {
    e.preventDefault()
    if (!data) return
    settingsSaving = true
    settingsErr = ''
    settingsSaved = false
    try {
      const s = await putNotifySettings({ publicUrl: publicUrl.trim(), ntfyServer: ntfyServer.trim() })
      data.settings = s
      publicUrl = s.publicUrl
      ntfyServer = s.ntfyServer
      settingsSaved = true
    } catch (e) {
      settingsErr = errText(e)
    } finally {
      settingsSaving = false
    }
  }

  // ---- Kinds ----
  const kindIcons: Record<string, typeof Bell> = {
    ntfy: Bell, gotify: MessageSquare, discord: MessageCircle, telegram: Send, webhook: Webhook,
  }
  const kindBlurbs: Record<string, string> = {
    ntfy: 'Push notifications to the ntfy app on phones and computers, from ntfy.sh or a server of your own.',
    gotify: 'Messages to a Gotify server you run, and the apps signed in to it.',
    discord: "Posts in a Discord channel, through the channel's webhook.",
    telegram: 'Messages from a Telegram bot to a chat, group or channel.',
    webhook: 'A JSON message posted to any address, for your own automations.',
  }
  const kindOf = (id: string): NotifyKind | undefined => data?.kinds.find((k) => k.id === id)
  const kindLabel = (id: string) => kindOf(id)?.label ?? id
  const eventLabel = (id: string) => data?.events.find((e) => e.id === id)?.label ?? id
  const libraryName = (id: string) => libraries.find((l) => l.id === id)?.name ?? 'a removed library'
  const openLibraries = $derived(libraries.filter((l) => !l.restricted))

  /** Where it sends, from the settings that are not secret; a masked secret when that is all there is. */
  function target(d: NotifyDestination): string {
    const fields = kindOf(d.kind)?.fields ?? []
    const plain = fields.filter((f) => !f.secret && d.config[f.name]).map((f) => d.config[f.name])
    if (plain.length > 0) return plain.join(' · ')
    return fields.filter((f) => d.config[f.name]).map((f) => d.config[f.name]).join(' · ')
  }

  // SQLite's datetime('now') is UTC without a zone; say so before parsing.
  function since(s: string): string {
    const t = Date.parse(s.includes('T') ? s : s.replace(' ', 'T') + 'Z')
    if (Number.isNaN(t)) return s
    const mins = Math.round((Date.now() - t) / 60000)
    if (mins < 2) return 'just now'
    if (mins < 60) return `${mins} min ago`
    const hours = Math.round(mins / 60)
    if (hours < 48) return `${hours} h ago`
    return `${Math.round(hours / 24)} days ago`
  }

  // ---- Test ----
  type TestResult = 'sending' | { ok: true } | { ok: false; error: string }
  let tests: Record<string, TestResult> = $state({})
  async function test(id: string) {
    tests = { ...tests, [id]: 'sending' }
    let result: TestResult
    try {
      await testNotifyDestination(id)
      result = { ok: true }
    } catch (e) {
      result = { ok: false, error: errText(e) }
    }
    tests = { ...tests, [id]: result }
    void load() // the test is recorded as the last delivery or error
  }

  // ---- On and off ----
  async function setEnabled(d: NotifyDestination, on: boolean) {
    if (!data) return
    err = ''
    data.destinations = data.destinations.map((x) => (x.id === d.id ? { ...x, enabled: on } : x))
    try {
      const saved = await updateNotifyDestination(d.id, { enabled: on })
      if (data) data.destinations = data.destinations.map((x) => (x.id === d.id ? saved : x))
    } catch (e) {
      err = `${d.name}: ${errText(e)}`
      void load()
    }
  }

  // ---- Adding and editing ----
  // Step one picks the kind (adding only); step two is the form; after
  // saving, the dialogue offers a test before it closes.
  const defaultEvents = ['request.waiting', 'storage.down', 'scan.failed']
  let picking = $state(false)
  let editing: {
    id: string
    kind: NotifyKindId
    name: string
    /** Typed values; a secret starts blank and stays so unless replaced. */
    config: Record<string, string>
    /** The saved secrets as the server shows them, for placeholders. */
    masked: Record<string, string>
    events: string[]
    libraryIds: string[]
    enabled: boolean
  } | null = $state(null)
  let formErr = $state('')
  let saving = $state(false)
  /** The destination just saved, offered a test before the dialogue closes. */
  let saved: NotifyDestination | null = $state(null)

  function startAdd(kind: NotifyKind) {
    picking = false
    const config: Record<string, string> = {}
    for (const f of kind.fields) config[f.name] = ''
    const names = new Set(data?.destinations.map((d) => d.name) ?? [])
    let name = kind.label
    for (let n = 2; names.has(name); n++) name = `${kind.label} ${n}`
    editing = {
      id: '', kind: kind.id, name, config, masked: {},
      events: defaultEvents.filter((id) => data?.events.some((e) => e.id === id)),
      libraryIds: [], enabled: true,
    }
    formErr = ''
  }
  function startEdit(d: NotifyDestination) {
    const kind = kindOf(d.kind)
    const config: Record<string, string> = {}
    const masked: Record<string, string> = {}
    for (const f of kind?.fields ?? []) {
      if (f.secret) {
        config[f.name] = ''
        masked[f.name] = d.config[f.name] ?? ''
      } else {
        config[f.name] = d.config[f.name] ?? ''
      }
    }
    editing = {
      id: d.id, kind: d.kind, name: d.name, config, masked,
      events: [...d.events], libraryIds: [...d.libraryIds], enabled: d.enabled,
    }
    formErr = ''
  }
  function toggle(list: string[], id: string): string[] {
    return list.includes(id) ? list.filter((x) => x !== id) : [...list, id]
  }
  async function save(e: Event) {
    e.preventDefault()
    if (!editing) return
    saving = true
    formErr = ''
    try {
      const config: Record<string, string> = {}
      for (const [k, v] of Object.entries(editing.config)) config[k] = v.trim()
      // Libraries only matter to "new in the library"; without it they go.
      const libraryIds = editing.events.includes('library.new') ? editing.libraryIds : []
      const body = { name: editing.name.trim(), config, events: editing.events, libraryIds, enabled: editing.enabled }
      saved = editing.id
        ? await updateNotifyDestination(editing.id, body)
        : await createNotifyDestination({ kind: editing.kind, ...body })
      editing = null
      void load()
    } catch (x) {
      formErr = errText(x)
    } finally {
      saving = false
    }
  }

  // ---- Deleting, confirmed first ----
  let deleting: NotifyDestination | null = $state(null)
  let deleteBusy = $state(false)
  async function remove() {
    if (!deleting) return
    const id = deleting.id
    deleteBusy = true
    try {
      await deleteNotifyDestination(id)
      if (data) data.destinations = data.destinations.filter((d) => d.id !== id)
      deleting = null
      void load()
    } catch (e) {
      err = errText(e)
      deleting = null
    } finally {
      deleteBusy = false
    }
  }
</script>

{#snippet testLine(id: string)}
  {@const t = tests[id]}
  {#if t === 'sending'}
    <p class="small muted testline" role="status">Sending a test message…</p>
  {:else if t && 'error' in t}
    <p class="small testline bad" role="status">{t.error}</p>
  {:else if t}
    <p class="small testline good" role="status">Delivered</p>
  {/if}
{/snippet}

<div class="titlerow">
  <h1>Notifications</h1>
  <button class="primary ibtn" disabled={!data} onclick={() => (picking = true)}><Plus size={16} /> Add destination</button>
</div>
<p class="hint intro">
  Where Cineva tells the household what happened: requests waiting for approval, new titles, storage and scans
  that need attention. Nothing is sent anywhere until a destination is added here. People add their own, for
  their requests and the series they watch, in their Settings.
</p>
{#if err}<div class="error">{err}</div>{/if}

{#if !loaded}
  <div class="empty">Loading…</div>
{:else if data}
  <form class="panel settings" onsubmit={saveSettings}>
    <label class="field">
      <span>Public address for links</span>
      <span class="inputrow">
        <input type="url" bind:value={publicUrl} placeholder="https://cineva.example.com"
          spellcheck="false" autocapitalize="off" oninput={() => (settingsSaved = false)} />
        {#if publicUrl.trim() !== location.origin}
          <button type="button" class="lib-btn" onclick={() => { publicUrl = location.origin; settingsSaved = false }}>Use this address</button>
        {/if}
      </span>
      <span class="hint">Links in messages open Cineva here. Leave it blank and messages carry no links.</span>
    </label>
    <label class="field">
      <span>ntfy server for users' own notifications</span>
      <input type="url" bind:value={ntfyServer} placeholder="https://ntfy.sh"
        spellcheck="false" autocapitalize="off" oninput={() => (settingsSaved = false)} />
      <span class="hint">People can only add ntfy to their own notifications when this is set: https://ntfy.sh, or your own.</span>
    </label>
    {#if settingsErr}<div class="error slim">{settingsErr}</div>{/if}
    <div class="setfoot">
      {#if settingsSaved && !settingsDirty}<span class="small good" role="status">Saved</span>{/if}
      <button class="primary" type="submit" disabled={settingsSaving || !settingsDirty}>{settingsSaving ? 'Saving…' : 'Save'}</button>
    </div>
  </form>

  <h2>Household destinations</h2>
  {#if data.destinations.length === 0}
    <div class="empty">No destinations yet. Add the family's Discord channel, a Telegram chat or your own ntfy topic to hear about requests, new titles and problems.</div>
  {:else}
    <div class="grid">
      {#each data.destinations as d (d.id)}
        {@const Icon = kindIcons[d.kind] ?? Bell}
        <div class="panel card" class:off={!d.enabled}>
          <div class="head">
            <div class="headtext">
              <h3 class="name">{d.name}</h3>
              <span class="tag kindtag"><Icon size={13} /> {kindLabel(d.kind)}</span>
            </div>
            <label class="switch">
              <input type="checkbox" role="switch" checked={d.enabled} aria-label="Send to {d.name}"
                onchange={(ev) => setEnabled(d, ev.currentTarget.checked)} />
              <span class="track" aria-hidden="true"></span>
              {d.enabled ? 'On' : 'Off'}
            </label>
          </div>
          {#if target(d)}<div class="muted small wrap">{target(d)}</div>{/if}
          <div class="chips">
            {#each d.events as ev (ev)}<span class="tag">{eventLabel(ev)}</span>{/each}
            {#if d.events.length === 0}<span class="muted small">No events chosen, so nothing is sent</span>{/if}
          </div>
          <dl class="facts small">
            {#if d.events.includes('library.new')}
              <dt>New titles from</dt>
              <dd>{d.libraryIds.length > 0 ? d.libraryIds.map(libraryName).join(', ') : 'Libraries open to everyone'}</dd>
            {/if}
            <dt>Last delivered</dt>
            <dd>{d.lastSentAt ? since(d.lastSentAt) : 'Nothing yet'}</dd>
            {#if d.lastError}
              <dt>Last error</dt>
              <dd class="bad">{d.lastError}{#if d.lastErrorAt}{' · '}{since(d.lastErrorAt)}{/if}</dd>
            {/if}
          </dl>
          {@render testLine(d.id)}
          <div class="actions">
            <button class="lib-btn ibtn" disabled={tests[d.id] === 'sending'} onclick={() => test(d.id)}>
              <RefreshCw size={15} class={tests[d.id] === 'sending' ? 'spin' : ''} /> Test
            </button>
            <button class="lib-btn ibtn" onclick={() => startEdit(d)}><Pencil size={15} /> Edit</button>
            <button class="lib-btn ibtn danger push" onclick={() => (deleting = d)}><Trash size={15} /> Delete</button>
          </div>
        </div>
      {/each}
    </div>
  {/if}
{/if}

{#if picking && data}
  <Modal title="Add a destination" onclose={() => (picking = false)}>
    <div class="kindlist">
      {#each data.kinds as k (k.id)}
        {@const Icon = kindIcons[k.id] ?? Bell}
        <button class="kindopt" onclick={() => startAdd(k)}>
          <span class="kindicon"><Icon size={20} /></span>
          <span class="kindtext">
            <strong>{k.label}</strong>
            {#if kindBlurbs[k.id]}<span class="muted small">{kindBlurbs[k.id]}</span>{/if}
          </span>
          <ChevronRight size={18} class="kindgo" />
        </button>
      {/each}
    </div>
  </Modal>
{/if}

{#if editing && data}
  {@const kind = kindOf(editing.kind)}
  {@const label = kindLabel(editing.kind)}
  <Modal title={editing.id ? `Edit ${editing.name || label}` : `Add ${label}`} onclose={() => (editing = null)}>
    <form id="notify-form" class="form" onsubmit={save}>
      <label>Name
        <input bind:value={editing.name} required maxlength="60" placeholder={label} />
      </label>
      {#each kind?.fields ?? [] as f (f.name)}
        {@const keep = !!editing.id && !!f.secret && !!editing.masked[f.name]}
        <label>{f.label}{#if keep}{' '}(leave blank to keep){:else if !f.required}{' '}(optional){/if}
          {#if f.secret}
            <input type="password" bind:value={editing.config[f.name]} required={!!f.required && !keep}
              placeholder={keep ? editing.masked[f.name] : ''} autocomplete="off" spellcheck="false" autocapitalize="off" />
          {:else}
            <input bind:value={editing.config[f.name]} required={!!f.required}
              autocomplete="off" spellcheck="false" autocapitalize="off" />
          {/if}
        </label>
        {#if f.hint}<p class="hint">{f.hint}</p>{/if}
      {/each}

      <fieldset class="group">
        <legend>Events it receives</legend>
        {#each data.events as ev (ev.id)}
          <label class="check">
            <input type="checkbox" checked={editing.events.includes(ev.id)}
              onchange={() => editing && (editing.events = toggle(editing.events, ev.id))} />
            {ev.label}
          </label>
        {/each}
      </fieldset>

      {#if editing.events.includes('library.new')}
        <fieldset class="group">
          <legend>Libraries it may name</legend>
          {#each libraries as l (l.id)}
            <label class="check">
              <input type="checkbox" checked={editing.libraryIds.includes(l.id)}
                onchange={() => editing && (editing.libraryIds = toggle(editing.libraryIds, l.id))} />
              {l.name}{#if l.restricted}<span class="muted small">{' '}restricted</span>{/if}
            </label>
          {/each}
          {#each editing.libraryIds.filter((id) => !libraries.some((l) => l.id === id)) as id (id)}
            <label class="check">
              <input type="checkbox" checked onchange={() => editing && (editing.libraryIds = toggle(editing.libraryIds, id))} />
              <span class="muted">A removed library</span>
            </label>
          {/each}
          <p class="hint">
            {#if editing.libraryIds.length === 0}
              None ticked: libraries open to everyone{openLibraries.length > 0 ? ` (${openLibraries.map((l) => l.name).join(', ')})` : ', of which there are none now'}.
            {:else}
              New titles are named only from the libraries ticked. Everyone who reads this destination sees them, so
              think twice before ticking a restricted one.
            {/if}
          </p>
        </fieldset>
      {/if}

      <label class="switch">
        <input type="checkbox" role="switch" bind:checked={editing.enabled} />
        <span class="track" aria-hidden="true"></span>
        Enabled
      </label>
      {#if formErr}<div class="error slim">{formErr}</div>{/if}
    </form>
    {#snippet foot()}
      {#if editing && !editing.id}
        <button type="button" class="lib-btn push-end" onclick={() => { editing = null; picking = true }}>Back</button>
      {/if}
      <button type="button" class="lib-btn" onclick={() => (editing = null)}>Cancel</button>
      <button class="primary" type="submit" form="notify-form" disabled={saving}>
        {saving ? 'Saving…' : editing?.id ? 'Save' : 'Add'}
      </button>
    {/snippet}
  </Modal>
{/if}

{#if saved}
  {@const s = saved}
  <Modal title="{s.name} saved" onclose={() => (saved = null)}>
    <p>Send a test message to check that it arrives{s.enabled ? '' : '. It is switched off, so nothing else is sent until it is switched on'}.</p>
    {@render testLine(s.id)}
    {#snippet foot()}
      <button type="button" class="lib-btn ibtn push-end" disabled={tests[s.id] === 'sending'} onclick={() => test(s.id)}>
        <RefreshCw size={15} class={tests[s.id] === 'sending' ? 'spin' : ''} /> Send a test
      </button>
      <button type="button" class="primary" onclick={() => (saved = null)}>Done</button>
    {/snippet}
  </Modal>
{/if}

{#if deleting}
  {@const d = deleting}
  <Modal title="Delete {d.name}?" onclose={() => (deleting = null)}>
    <p>Cineva stops sending to it and forgets its settings. Nothing is changed on the {kindLabel(d.kind)} side.</p>
    {#snippet foot()}
      <button type="button" class="lib-btn" onclick={() => (deleting = null)}>Cancel</button>
      <button type="button" class="lib-btn ibtn danger solid" disabled={deleteBusy} onclick={remove}>
        <Trash size={15} /> {deleteBusy ? 'Deleting…' : 'Delete'}
      </button>
    {/snippet}
  </Modal>
{/if}

<style>
  h1 { font-size: 1.35rem; margin-bottom: 0.6rem; }
  h2 { font-size: 0.95rem; color: var(--muted); margin: 1.5rem 0 0.6rem; }
  .intro { max-width: 52rem; margin-bottom: 1rem; }
  .hint { color: var(--muted); font-size: 0.8rem; line-height: 1.5; }
  .muted { color: var(--muted); }
  .small { font-size: 0.78rem; }
  .wrap { overflow-wrap: anywhere; }
  .good { color: var(--ok); }
  .bad { color: var(--err); }
  .error {
    background: color-mix(in srgb, var(--err) 15%, transparent); border: 1px solid var(--err);
    border-radius: 8px; padding: 0.5rem 0.8rem; margin-bottom: 0.8rem; overflow-wrap: anywhere;
  }
  .error.slim { margin: 0; padding: 0.4rem 0.7rem; font-size: 0.9rem; }
  .empty {
    background: var(--panel); border: 1px dashed var(--border); border-radius: 12px;
    padding: 1.5rem 1.25rem; color: var(--muted);
  }
  .panel {
    background: var(--panel); border: 1px solid var(--border); border-radius: 12px; padding: 1rem 1.25rem;
  }
  .titlerow { display: flex; align-items: flex-start; justify-content: space-between; gap: 1rem; }
  .titlerow .primary { flex: none; }
  .primary { background: var(--accent); color: var(--on-accent); padding: 0.5rem 1rem; border-radius: 8px; }
  .primary:disabled { opacity: 0.45; cursor: not-allowed; }
  .lib-btn {
    background: var(--bg); border: 1px solid var(--border); border-radius: 8px;
    color: var(--text); padding: 0.4rem 0.75rem; font: inherit; font-size: 0.875rem;
  }
  .lib-btn:hover { border-color: var(--accent); }
  .lib-btn:disabled { opacity: 0.5; cursor: default; }
  .lib-btn:disabled:hover { border-color: var(--border); }
  .lib-btn.danger { color: var(--err); border-color: color-mix(in srgb, var(--err) 45%, var(--border)); }
  .lib-btn.danger:hover { border-color: var(--err); }
  .lib-btn.danger.solid { background: var(--err); border-color: var(--err); color: var(--bg); }
  .lib-btn :global(.spin) { animation: spin 0.9s linear infinite; }
  @keyframes spin { to { transform: rotate(360deg); } }
  .ibtn { display: inline-flex; align-items: center; gap: 0.4rem; }
  .push-end { margin-right: auto; }
  .tag {
    background: var(--bg); border: 1px solid var(--border); border-radius: 999px;
    padding: 0.15rem 0.6rem; font-size: 0.78rem; display: inline-flex; align-items: center; gap: 0.3rem;
  }

  input:not([type='checkbox']) {
    background: var(--bg); border: 1px solid var(--border); border-radius: 8px;
    color: var(--text); padding: 0.5rem 0.7rem; font: inherit; min-width: 0; width: 100%;
  }

  /* The settings: stacked on a phone, the two side by side when wide. */
  .settings { display: grid; grid-template-columns: minmax(0, 1fr); gap: 0.9rem; max-width: 60rem; }
  .field { display: flex; flex-direction: column; gap: 0.3rem; font-size: 0.9rem; min-width: 0; }
  .field > span:first-child { color: var(--muted); }
  .inputrow { display: flex; flex-wrap: wrap; gap: 0.5rem; }
  .inputrow input { flex: 1 1 14rem; width: auto; }
  .inputrow .lib-btn { flex: none; }
  .setfoot { display: flex; align-items: center; justify-content: flex-end; gap: 0.75rem; }
  @media (min-width: 52rem) {
    .settings { grid-template-columns: minmax(0, 1fr) minmax(0, 1fr); align-items: start; }
    .setfoot, .settings .error { grid-column: 1 / -1; }
  }

  /* A card per destination, as on the Integrations page: name and kind,
     the switch, what it receives and how it last went, then the actions. */
  .grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(min(100%, 24rem), 1fr)); gap: 1rem; }
  .card { display: flex; flex-direction: column; gap: 0.6rem; }
  .card.off .headtext, .card.off .chips { opacity: 0.6; }
  .head { display: flex; align-items: flex-start; justify-content: space-between; gap: 0.75rem; }
  .headtext { display: flex; flex-direction: column; align-items: flex-start; gap: 0.35rem; min-width: 0; }
  .name { font-size: 1.05rem; font-weight: 600; overflow-wrap: anywhere; }
  .kindtag { color: var(--muted); }
  .chips { display: flex; flex-wrap: wrap; gap: 0.35rem; }
  .facts { display: grid; grid-template-columns: auto 1fr; gap: 0.3rem 0.9rem; }
  .facts dt { color: var(--muted); white-space: nowrap; }
  .facts dd { overflow-wrap: anywhere; }
  .testline { overflow-wrap: anywhere; }
  .actions {
    display: flex; flex-wrap: wrap; gap: 0.5rem;
    margin-top: auto; padding-top: 0.85rem; border-top: 1px solid var(--border);
  }
  .actions .push { margin-left: auto; }

  /* The Add dialogue's first step: one row per kind. */
  .kindlist { display: flex; flex-direction: column; gap: 0.5rem; }
  .kindopt {
    display: flex; align-items: center; gap: 0.85rem; text-align: left;
    padding: 0.8rem 0.9rem; border: 1px solid var(--border); border-radius: 10px; background: var(--bg);
  }
  .kindopt:hover, .kindopt:focus-visible { border-color: var(--accent); }
  .kindicon {
    display: grid; place-items: center; width: 2.4rem; height: 2.4rem; flex: none;
    border-radius: 8px; background: var(--panel); color: var(--accent-2);
  }
  .kindtext { display: flex; flex-direction: column; gap: 0.15rem; flex: 1; min-width: 0; }
  .kindopt :global(.kindgo) { flex: none; color: var(--muted); }

  .form { display: flex; flex-direction: column; gap: 0.6rem; }
  .form > label:not(.switch) { display: flex; flex-direction: column; gap: 0.3rem; font-size: 0.9rem; color: var(--muted); }
  .form .hint { margin-top: -0.3rem; }
  .group {
    border: 1px solid var(--border); border-radius: 8px; padding: 0.6rem 0.8rem;
    display: flex; flex-direction: column; gap: 0.4rem;
  }
  .group legend { color: var(--muted); font-size: 0.8rem; padding: 0 0.3rem; }
  .group .hint { margin-top: 0.2rem; }
  .check { display: flex; align-items: center; gap: 0.5rem; font-size: 0.9rem; cursor: pointer; }
  .check input { flex: none; }

  /* An on/off switch over a real checkbox, so it keeps the keyboard and
     the screen reader's "switch". */
  .switch { position: relative; display: inline-flex; align-items: center; gap: 0.5rem; font-size: 0.85rem; cursor: pointer; flex: none; }
  .switch input { position: absolute; opacity: 0; width: 2.4rem; height: 1.35rem; margin: 0; cursor: pointer; }
  .switch .track {
    position: relative; width: 2.4rem; height: 1.35rem; flex: none; border-radius: 999px;
    background: var(--border); transition: background 0.15s;
  }
  .switch .track::after {
    content: ''; position: absolute; top: 0.15rem; left: 0.15rem; width: 1.05rem; height: 1.05rem;
    border-radius: 50%; background: var(--text); transition: transform 0.15s;
  }
  .switch input:checked + .track { background: var(--accent); }
  .switch input:checked + .track::after { transform: translateX(1.05rem); }
  .switch input:focus-visible + .track { outline: 2px solid var(--accent-2); outline-offset: 2px; }
</style>
