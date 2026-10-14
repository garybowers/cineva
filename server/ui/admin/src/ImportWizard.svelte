<script lang="ts">
  // Import from Plex or Jellyfin (docs/import.md), as a dialogue in three
  // steps: connect to the old server, map its users to Cineva's and choose
  // what to bring, then follow the run and read its result. A dry run
  // reports the same numbers without writing anything, and offers the real
  // import with the same settings. The token lives in this dialogue only;
  // the server uses it for the run and never stores it.
  import { onMount, untrack } from 'svelte'
  import {
    checkImportSource, startImport, currentImport,
    type ImportKind, type ImportCheck, type ImportRun, type ImportRequest, type ImportUserResult, type User,
  } from './lib/api'
  import Modal from './Modal.svelte'
  import { Server, ChevronRight, Copy, Check } from '@lucide/svelte'

  let { users, live = null, onclose }: {
    users: User[]
    /** The latest run from the event stream, for live progress. */
    live?: ImportRun | null
    onclose: () => void
  } = $props()

  const errText = (e: unknown) => (e instanceof Error ? e.message : String(e))

  const kinds: { id: ImportKind; label: string; blurb: string; url: string }[] = [
    { id: 'jellyfin', label: 'Jellyfin', blurb: 'API key from Dashboard, API Keys', url: 'http://jellyfin.lan:8096' },
    { id: 'plex', label: 'Plex', blurb: "The server owner's X-Plex-Token", url: 'http://plex.lan:32400' },
  ]
  const kindLabel = (k: ImportKind) => kinds.find((x) => x.id === k)?.label ?? k

  let step: 'connect' | 'map' | 'progress' = $state('connect')

  // ---- Connect ----
  let kind: ImportKind | null = $state(null)
  let url = $state('')
  let token = $state('')
  let checking = $state(false)
  let connectErr = $state('')
  let source: ImportCheck | null = $state(null)

  async function connect(e: Event) {
    e.preventDefault()
    if (!kind) return
    checking = true
    connectErr = ''
    try {
      source = await checkImportSource({ kind, url: url.trim(), token: token.trim() })
      mapping = Object.fromEntries(source.users.map((u) => [u.id, guess(u.name)]))
      step = 'map'
    } catch (x) {
      connectErr = errText(x)
    } finally {
      checking = false
    }
  }

  // ---- Map ----
  const CREATE = 'create'
  const SKIP = 'skip'
  /** Source user id to a Cineva user id, CREATE or SKIP. */
  let mapping: Record<string, string> = $state({})
  let watchState = $state(true)
  let playlists = $state(false)
  let startErr = $state('')
  let starting = $state(false)

  /** A Cineva user whose username or display name matches, ignoring case; else create one. */
  function guess(name: string): string {
    const n = name.trim().toLowerCase()
    const u = users.find((x) => x.username.toLowerCase() === n || x.displayName.toLowerCase() === n)
    return u ? u.id : CREATE
  }
  const mapped = $derived(Object.values(mapping).filter((v) => v !== SKIP).length)
  const canStart = $derived(mapped > 0 && (watchState || playlists))

  /** The last request sent, so a dry run can be followed by the real import. */
  let lastRequest: ImportRequest | null = $state(null)

  function request(dryRun: boolean): ImportRequest | null {
    if (!source || !kind) return null
    return {
      kind, url: url.trim(), token: token.trim(), dryRun,
      users: source.users.map((u) => {
        const to = mapping[u.id] ?? SKIP
        if (to === CREATE) return { sourceId: u.id, sourceName: u.name, create: true }
        if (to === SKIP) return { sourceId: u.id, sourceName: u.name }
        return { sourceId: u.id, sourceName: u.name, userId: to }
      }),
      options: { watchState, playlists },
    }
  }

  async function start(r: ImportRequest | null) {
    if (!r) return
    starting = true
    startErr = ''
    try {
      run = await startImport(r)
      lastRequest = r
      step = 'progress'
    } catch (x) {
      startErr = errText(x)
    } finally {
      starting = false
    }
  }

  // ---- Progress and result ----
  let run: ImportRun | null = $state(null)
  /** A finished run from before this dialogue opened, offered on the first step. */
  let previous: ImportRun | null = $state(null)

  onMount(() => {
    void currentImport().then((r) => {
      if (!r) return
      if (r.status === 'running') {
        run = r
        step = 'progress'
      } else if (!run) {
        previous = r
      }
    })
  })

  // The event stream carries each change; a run started elsewhere while
  // this is open takes over, since only one runs at a time.
  $effect(() => {
    const l = live
    if (!l) return
    untrack(() => {
      if (run && l.id === run.id) run = l
      else if (l.status === 'running' && (!run || run.status !== 'running')) {
        run = l
        step = 'progress'
      }
    })
  })

  // A fallback for a dropped event stream: ask now and then while it runs.
  const running = $derived(run?.status === 'running')
  $effect(() => {
    if (step !== 'progress' || !running) return
    const id = setInterval(() => {
      void currentImport().then((r) => {
        if (r && run && r.id === run.id) run = r
      })
    }, 4000)
    return () => clearInterval(id)
  })

  const pct = $derived(run && run.total > 0 ? Math.min(100, Math.round((run.done / run.total) * 100)) : 0)
  const showMusic = $derived(
    !!run && (!!lastRequest?.options.playlists || run.users.some((u) => u.playlists > 0 || u.tracks > 0)),
  )

  let copied = $state('')
  async function copy(u: ImportUserResult) {
    if (!u.created) return
    try {
      await navigator.clipboard.writeText(u.created.password)
      copied = u.created.username
      setTimeout(() => { if (copied === u.created?.username) copied = '' }, 2000)
    } catch {
      copied = ''
    }
  }

  function startOver() {
    run = null
    previous = null
    lastRequest = null
    source = null
    startErr = ''
    step = 'connect'
  }

  const title = $derived(
    step === 'connect' ? 'Import from Plex or Jellyfin'
      : step === 'map' ? `Import from ${source?.name || (kind ? kindLabel(kind) : 'the server')}`
        : run?.status === 'running' ? (run.dryRun ? 'Dry run' : 'Importing')
          : run?.dryRun ? 'Dry run result' : 'Import result',
  )
</script>

<Modal {title} {onclose}>
  {#if step === 'connect'}
    <form id="import-connect" class="form" onsubmit={connect}>
      <div class="kindlist" role="radiogroup" aria-label="The server to import from">
        {#each kinds as k (k.id)}
          <button type="button" class="kindopt" class:sel={kind === k.id} role="radio" aria-checked={kind === k.id}
            onclick={() => { kind = k.id; connectErr = '' }}>
            <span class="kindicon"><Server size={20} /></span>
            <span class="kindtext">
              <strong>{k.label}</strong>
              <span class="muted small">{k.blurb}</span>
            </span>
            {#if kind === k.id}<Check size={18} class="kindgo" />{:else}<ChevronRight size={18} class="kindgo" />{/if}
          </button>
        {/each}
      </div>
      {#if kind}
        <label>Server address
          <input type="url" bind:value={url} required placeholder={kinds.find((k) => k.id === kind)?.url}
            spellcheck="false" autocapitalize="off" />
        </label>
        <label>{kind === 'plex' ? 'X-Plex-Token' : 'API key'}
          <input type="password" bind:value={token} required autocomplete="off" spellcheck="false" autocapitalize="off" />
        </label>
        <p class="hint">Used for this import only and never stored.</p>
      {/if}
      {#if connectErr}<div class="error">{connectErr}</div>{/if}
      {#if previous}
        <p class="hint">
          The last {previous.dryRun ? 'dry run' : 'import'}{previous.source ? ` from ${previous.source}` : ''}
          {previous.status === 'failed' ? 'failed' : 'finished'}.
          <button type="button" class="linkbtn" onclick={() => { run = previous; step = 'progress' }}>Show its result</button>
        </p>
      {/if}
    </form>
  {:else if step === 'map' && source}
    <div class="form">
      <p class="small muted">{source.name || kindLabel(source.kind)}{source.version ? ` · ${kindLabel(source.kind)} ${source.version}` : ''}</p>
      {#each source.caveats ?? [] as c (c)}<p class="note small">{c}</p>{/each}

      <fieldset class="group">
        <legend>People</legend>
        {#if source.users.length === 0}
          <p class="muted small">{source.name || 'The server'} has no users to import.</p>
        {/if}
        {#each source.users as u (u.id)}
          <div class="maprow">
            <span class="mapname">{u.name}{#if u.admin}<span class="tag">admin</span>{/if}</span>
            <select bind:value={mapping[u.id]} aria-label="Who {u.name} becomes in Cineva">
              {#each users as cu (cu.id)}<option value={cu.id}>{cu.displayName || cu.username}{cu.displayName && cu.displayName !== cu.username ? ` (${cu.username})` : ''}</option>{/each}
              <option value={CREATE}>Create a user</option>
              <option value={SKIP}>Skip</option>
            </select>
          </div>
        {/each}
      </fieldset>

      <fieldset class="group">
        <legend>What to bring</legend>
        <label class="check">
          <input type="checkbox" bind:checked={watchState} />
          <span>Watch state
            <span class="hint block">What each person has watched and where they stopped. Nothing newer in Cineva is overwritten.</span>
          </span>
        </label>
        <label class="check">
          <input type="checkbox" bind:checked={playlists} />
          <span>Music playlists and favourites
            <span class="hint block">Music playlists, and Jellyfin's favourite tracks as a playlist called Favourites. Video playlists have nowhere to go in Cineva.</span>
          </span>
        </label>
      </fieldset>
      <p class="hint">
        {#if mapped === 0}Map at least one person to a Cineva user, or to Create a user.
        {:else if !watchState && !playlists}Choose something to bring.
        {:else}A dry run shows what would come across and what did not match, without changing anything.{/if}
      </p>
      {#if startErr}<div class="error">{startErr}</div>{/if}
    </div>
  {:else if step === 'progress' && run}
    {@const r = run}
    <div class="form">
      {#if r.status === 'running'}
        <p role="status">{r.step || 'Working'}{r.total > 0 ? ` · ${r.done} of ${r.total}` : '…'}</p>
        <div class="bar" class:busy={r.total === 0} role="progressbar" aria-valuemin="0" aria-valuemax="100"
          aria-valuenow={r.total > 0 ? pct : undefined} aria-label="Import progress">
          <span style:width={r.total > 0 ? `${pct}%` : undefined}></span>
        </div>
        <p class="hint">This can be closed: the {r.dryRun ? 'dry run' : 'import'} carries on, and opening this again shows it.</p>
      {:else}
        <p class="small muted">
          {r.source || kindLabel(r.kind)} · {r.dryRun ? 'dry run: nothing was written' : 'import'}{r.status === 'failed' ? ' · failed' : ''}
        </p>
        {#if r.error}<div class="error">{r.error}</div>{/if}
      {/if}

      {#each r.users.filter((u) => u.created) as u (u.source)}
        <div class="created">
          <p><strong>{u.source}</strong> is now <strong>{u.created?.username}</strong> in Cineva.</p>
          <div class="pwrow">
            <code class="pw">{u.created?.password}</code>
            <button type="button" class="lib-btn ibtn" onclick={() => copy(u)}>
              {#if copied === u.created?.username}<Check size={15} /> Copied{:else}<Copy size={15} /> Copy{/if}
            </button>
          </div>
          <p class="hint">A temporary password, shown only this once: copy it now and give it to them. They should change it when they first sign in.</p>
        </div>
      {/each}

      {#if r.users.length > 0}
        <ul class="results">
          {#each r.users as u (u.source)}
            <li>
              <div class="rhead">
                <strong>{u.source}</strong>
                <span class="muted small">{u.user ? `to ${u.user}` : ''}</span>
              </div>
              {#if u.error}<p class="small bad">{u.error}</p>{/if}
              <dl class="facts small">
                <dt>Watched</dt><dd>{u.watched}</dd>
                <dt>In progress</dt><dd>{u.inProgress}</dd>
                {#if !r.dryRun}<dt>Written</dt><dd>{u.written}</dd>{/if}
                <dt>Not visible to them</dt><dd>{u.notVisible}</dd>
                {#if showMusic}
                  <dt>Playlists</dt><dd>{u.playlists} ({u.tracks} track{u.tracks === 1 ? '' : 's'})</dd>
                {/if}
                <dt>Unmatched</dt>
                <dd>
                  {#if u.unmatched > 0 && (u.unmatchedTitles?.length ?? 0) > 0}
                    <details>
                      <summary>{u.unmatched}</summary>
                      <ul class="titles">
                        {#each u.unmatchedTitles ?? [] as t, i (i)}<li>{t}</li>{/each}
                        {#if u.unmatched > (u.unmatchedTitles?.length ?? 0)}
                          <li class="muted">and {u.unmatched - (u.unmatchedTitles?.length ?? 0)} more</li>
                        {/if}
                      </ul>
                    </details>
                  {:else}
                    {u.unmatched}
                  {/if}
                </dd>
              </dl>
            </li>
          {/each}
        </ul>
        {#if r.status !== 'running'}
          <p class="hint">
            Unmatched titles are not in Cineva's libraries, or could not be told apart, {r.dryRun ? 'would be' : 'were'} left
            alone. Titles not visible are in libraries that person cannot see.
          </p>
        {/if}
      {/if}
      {#if startErr}<div class="error">{startErr}</div>{/if}
    </div>
  {/if}

  {#snippet foot()}
    {#if step === 'connect'}
      <button type="button" class="lib-btn" onclick={onclose}>Cancel</button>
      <button class="primary" type="submit" form="import-connect" disabled={!kind || checking}>{checking ? 'Connecting…' : 'Connect'}</button>
    {:else if step === 'map'}
      <button type="button" class="lib-btn push-end" onclick={() => { step = 'connect'; startErr = '' }}>Back</button>
      <button type="button" class="lib-btn" disabled={!canStart || starting} onclick={() => start(request(true))}>Dry run</button>
      <button type="button" class="primary" disabled={!canStart || starting} onclick={() => start(request(false))}>{starting ? 'Starting…' : 'Import'}</button>
    {:else if run?.status === 'running'}
      <button type="button" class="lib-btn" onclick={onclose}>Close</button>
    {:else}
      <button type="button" class="lib-btn push-end" onclick={startOver}>Start again</button>
      {#if run?.dryRun && run.status === 'done' && lastRequest}
        <button type="button" class="lib-btn" onclick={() => { step = 'map'; startErr = '' }}>Change settings</button>
        <button type="button" class="primary" disabled={starting}
          onclick={() => lastRequest && start({ ...lastRequest, dryRun: false })}>{starting ? 'Starting…' : 'Import now'}</button>
      {:else}
        <button type="button" class="primary" onclick={onclose}>Done</button>
      {/if}
    {/if}
  {/snippet}
</Modal>

<style>
  .hint { color: var(--muted); font-size: 0.8rem; line-height: 1.5; }
  .hint.block { display: block; }
  .muted { color: var(--muted); }
  .small { font-size: 0.78rem; }
  .bad { color: var(--err); overflow-wrap: anywhere; }
  .error {
    background: color-mix(in srgb, var(--err) 15%, transparent); border: 1px solid var(--err);
    border-radius: 8px; padding: 0.4rem 0.7rem; font-size: 0.9rem; overflow-wrap: anywhere;
  }
  .note {
    border-left: 3px solid var(--accent-2); background: var(--bg); border-radius: 0 8px 8px 0;
    padding: 0.45rem 0.7rem; color: var(--text); line-height: 1.5;
  }
  .primary { background: var(--accent); color: var(--on-accent); padding: 0.5rem 1rem; border-radius: 8px; }
  .primary:disabled { opacity: 0.45; cursor: not-allowed; }
  .lib-btn {
    background: var(--bg); border: 1px solid var(--border); border-radius: 8px;
    color: var(--text); padding: 0.4rem 0.75rem; font: inherit; font-size: 0.875rem;
  }
  .lib-btn:hover { border-color: var(--accent); }
  .lib-btn:disabled { opacity: 0.5; cursor: default; }
  .lib-btn:disabled:hover { border-color: var(--border); }
  .ibtn { display: inline-flex; align-items: center; gap: 0.4rem; }
  .push-end { margin-right: auto; }
  .linkbtn { color: var(--accent-2); text-decoration: underline; font-size: inherit; }
  .tag {
    background: var(--bg); border: 1px solid var(--border); border-radius: 999px;
    padding: 0.05rem 0.5rem; font-size: 0.72rem; color: var(--muted); margin-left: 0.45rem;
  }

  .form { display: flex; flex-direction: column; gap: 0.7rem; }
  .form > label { display: flex; flex-direction: column; gap: 0.3rem; font-size: 0.9rem; color: var(--muted); }
  .form > .hint { margin-top: -0.3rem; }
  input:not([type='checkbox']), select {
    background: var(--bg); border: 1px solid var(--border); border-radius: 8px;
    color: var(--text); padding: 0.5rem 0.7rem; font: inherit; min-width: 0; width: 100%;
  }

  /* The kinds, as the other Add dialogues show them. */
  .kindlist { display: flex; flex-direction: column; gap: 0.5rem; }
  .kindopt {
    display: flex; align-items: center; gap: 0.85rem; text-align: left;
    padding: 0.8rem 0.9rem; border: 1px solid var(--border); border-radius: 10px; background: var(--bg);
  }
  .kindopt:hover, .kindopt:focus-visible, .kindopt.sel { border-color: var(--accent); }
  .kindopt.sel { background: color-mix(in srgb, var(--accent) 10%, var(--bg)); }
  .kindicon {
    display: grid; place-items: center; width: 2.4rem; height: 2.4rem; flex: none;
    border-radius: 8px; background: var(--panel); color: var(--accent-2);
  }
  .kindtext { display: flex; flex-direction: column; gap: 0.15rem; flex: 1; min-width: 0; }
  .kindopt :global(.kindgo) { flex: none; color: var(--muted); }

  .group {
    border: 1px solid var(--border); border-radius: 8px; padding: 0.6rem 0.8rem;
    display: flex; flex-direction: column; gap: 0.55rem; min-width: 0;
  }
  .group legend { color: var(--muted); font-size: 0.8rem; padding: 0 0.3rem; }
  .check { display: flex; align-items: flex-start; gap: 0.55rem; font-size: 0.9rem; cursor: pointer; }
  .check input { flex: none; margin-top: 0.2rem; }

  /* A source user and who they become: stacked on a phone, side by side when there is room. */
  .maprow { display: flex; flex-wrap: wrap; align-items: center; gap: 0.35rem 0.75rem; }
  .mapname { flex: 1 1 9rem; min-width: 0; overflow-wrap: anywhere; font-size: 0.9rem; }
  .maprow select { flex: 1 1 12rem; width: auto; }

  .bar { height: 0.5rem; border-radius: 999px; background: var(--bg); border: 1px solid var(--border); overflow: hidden; }
  .bar span { display: block; height: 100%; background: var(--accent); transition: width 0.3s; }
  .bar.busy span { width: 35%; animation: slide 1.4s ease-in-out infinite; }
  @keyframes slide { from { transform: translateX(-100%); } to { transform: translateX(300%); } }

  .created {
    border: 1px solid var(--accent); border-radius: 10px; padding: 0.75rem 0.9rem;
    background: color-mix(in srgb, var(--accent) 8%, var(--bg)); display: flex; flex-direction: column; gap: 0.5rem;
  }
  .created p { overflow-wrap: anywhere; }
  .pwrow { display: flex; flex-wrap: wrap; align-items: center; gap: 0.5rem; }
  .pw {
    flex: 1 1 10rem; min-width: 0; overflow-wrap: anywhere; font-size: 1.05rem; letter-spacing: 0.03em;
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace; color: var(--text);
    background: var(--panel); border: 1px solid var(--border); border-radius: 8px; padding: 0.4rem 0.65rem;
    user-select: all;
  }

  .results { list-style: none; border: 1px solid var(--border); border-radius: 10px; }
  .results > li { padding: 0.7rem 0.9rem; display: flex; flex-direction: column; gap: 0.4rem; }
  .results > li + li { border-top: 1px solid var(--border); }
  .rhead { display: flex; flex-wrap: wrap; align-items: baseline; gap: 0.2rem 0.6rem; overflow-wrap: anywhere; }
  .facts { display: grid; grid-template-columns: auto 1fr; gap: 0.25rem 0.9rem; }
  .facts dt { color: var(--muted); }
  .facts dd { overflow-wrap: anywhere; }
  summary { cursor: pointer; color: var(--accent-2); }
  .titles { margin: 0.35rem 0 0 1rem; display: flex; flex-direction: column; gap: 0.15rem; max-height: 14rem; overflow-y: auto; }
</style>
