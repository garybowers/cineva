<script lang="ts">
  // The household's defaults: one table per Settings section, a column for
  // the server and one for each group in priority order, each cell a value
  // and a lock. Users start from these and may change them, except where a
  // cell is locked. On a phone each preference becomes a card with a row
  // per column, since a table of several groups cannot fit.
  import { onMount } from 'svelte'
  import {
    getPrefDefaults, putGroupDefaults, putServerDefaults, setGroupOrder,
    type PrefDefaultChanges, type PrefDefaults, type PrefEntry, type PrefSetting,
  } from './lib/api'
  import type { ThemeInfo } from './lib/theme'

  let { themes = [] }: { themes?: ThemeInfo[] } = $props()

  let data: PrefDefaults | null = $state(null)
  let err = $state('')
  let busy = $state(false)
  const errText = (e: unknown) => (e instanceof Error ? e.message : String(e))

  async function load() {
    try {
      data = await getPrefDefaults()
      err = ''
    } catch (e) {
      err = errText(e)
    }
  }
  onMount(load)

  // A column: the server, or a group.
  type Column = { id: string; name: string; values: Record<string, PrefSetting> }
  const columns = $derived<Column[]>(
    data
      ? [
          { id: '', name: 'Server', values: data.server },
          ...data.groups.map((g) => ({ id: g.id, name: g.name, values: g.defaults })),
        ]
      : [],
  )

  // "Not set" needs a value no preference can have: some choices are the
  // empty string ("The file's default").
  const UNSET = '\u0000unset'

  async function change(col: Column, key: string, next: PrefSetting | null) {
    if (!data) return
    const before = data
    // Shown at once; put back if the server refuses it.
    const values = { ...col.values }
    if (next) values[key] = next
    else delete values[key]
    data = col.id === ''
      ? { ...data, server: values }
      : { ...data, groups: data.groups.map((g) => (g.id === col.id ? { ...g, defaults: values } : g)) }
    const changes: PrefDefaultChanges = { [key]: next }
    try {
      if (col.id === '') await putServerDefaults(changes)
      else await putGroupDefaults(col.id, changes)
      err = ''
    } catch (e) {
      data = before
      err = `${col.name}: ${errText(e)}`
    }
  }

  function setValue(col: Column, p: PrefEntry, raw: string) {
    if (raw === UNSET || (raw === '' && p.type !== 'enum')) {
      change(col, p.key, null)
      return
    }
    change(col, p.key, { value: raw, locked: col.values[p.key]?.locked ?? false })
  }

  function toggleLock(col: Column, p: PrefEntry) {
    const v = col.values[p.key]
    if (v) change(col, p.key, { value: v.value, locked: !v.locked })
  }

  // ---- Group priority ----
  async function move(i: number, by: number) {
    if (!data || busy) return
    const groups = [...data.groups]
    const j = i + by
    if (j < 0 || j >= groups.length) return
    ;[groups[i], groups[j]] = [groups[j], groups[i]]
    const before = data
    data = { ...data, groups }
    busy = true
    try {
      await setGroupOrder(groups.map((g) => g.id))
      err = ''
    } catch (e) {
      data = before
      err = errText(e)
    } finally {
      busy = false
    }
  }

  const choices = (p: PrefEntry) =>
    p.type === 'bool'
      ? [{ value: 'on', label: 'On' }, { value: 'off', label: 'Off' }]
      : p.key === 'theme' && themes.length > 0
        ? themes.map((t) => ({ value: t.id, label: t.name }))
        : (p.options ?? [])
  const builtIn = (p: PrefEntry) => choices(p).find((o) => o.value === p.default)?.label ?? (p.default || 'none')
</script>

<div class="titlerow"><h1>Defaults</h1></div>
<p class="hint intro">
  What people get until they choose otherwise. A group's default applies to its members over the server's, and where
  someone is in several groups the first group in the order below wins. Lock a default to stop people changing it:
  they still see the setting, marked as set by their administrator.
</p>
{#if err}<div class="error" role="alert">{err}</div>{/if}

{#if !data}
  <div class="panel muted">Loading…</div>
{:else}
  {#if data.groups.length > 1}
    <h2>Group order</h2>
    <ol class="panel order">
      {#each data.groups as g, i (g.id)}
        <li>
          <span class="rank">{i + 1}</span>
          <span class="gname">{g.name}</span>
          <button class="mv" disabled={busy || i === 0} onclick={() => move(i, -1)} aria-label="Move {g.name} up" title="Move up">↑</button>
          <button class="mv" disabled={busy || i === data.groups.length - 1} onclick={() => move(i, 1)} aria-label="Move {g.name} down" title="Move down">↓</button>
        </li>
      {/each}
    </ol>
  {:else if data.groups.length === 0}
    <p class="hint">Make groups on the Users page to give some people different defaults, such as subtitles for Kids.</p>
  {/if}

  {#each data.sections as sec (sec.id)}
    {@const rows = data.registry.filter((p) => p.section === sec.id && !p.hidden)}
    {#if rows.length > 0}
      <h2>{sec.label}</h2>
      <div class="tablewrap">
        <table>
          <thead>
            <tr>
              <th>Preference</th>
              {#each columns as col (col.id)}<th>{col.name}</th>{/each}
            </tr>
          </thead>
          <tbody>
            {#each rows as p (p.key)}
              <tr>
                <td class="what">
                  <strong>{p.label}</strong>
                  <span class="muted small">
                    Built in: {builtIn(p)}{#if p.scope === 'device'} · each device can differ{/if}
                  </span>
                </td>
                {#each columns as col (col.id)}
                  {@const v = col.values[p.key]}
                  {@const opts = choices(p)}
                  <td class="cell">
                    <span class="colname">{col.name}</span>
                    <div class="ctl">
                      {#if opts.length > 0}
                        <select
                          value={v ? v.value : UNSET}
                          aria-label="{p.label}, {col.name}"
                          onchange={(e) => setValue(col, p, (e.currentTarget as HTMLSelectElement).value)}
                        >
                          <option value={UNSET}>{col.id === '' ? 'Built in' : 'Not set'}</option>
                          {#each opts as o (o.value)}<option value={o.value}>{o.label}</option>{/each}
                        </select>
                      {:else}
                        <input
                          type={p.type === 'int' ? 'number' : 'text'}
                          min={p.min}
                          max={p.max}
                          value={v?.value ?? ''}
                          placeholder={col.id === '' ? 'Built in' : 'Not set'}
                          aria-label="{p.label}, {col.name}"
                          onchange={(e) => setValue(col, p, (e.currentTarget as HTMLInputElement).value.trim())}
                        />
                      {/if}
                      {#if p.lockable}
                        <label class="lock" class:on={v?.locked} class:off={!v} title={v ? 'Stop people changing it' : 'Choose a value to lock'}>
                          <input type="checkbox" checked={!!v?.locked} disabled={!v} onchange={() => toggleLock(col, p)} />
                          Lock
                        </label>
                      {/if}
                    </div>
                  </td>
                {/each}
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  {/each}
{/if}

<style>
  .titlerow { display: flex; align-items: center; justify-content: space-between; gap: 1rem; }
  h1 { font-size: 1.35rem; margin-bottom: 1rem; }
  h2 { font-size: 0.95rem; color: var(--muted); margin: 1.25rem 0 0.6rem; }
  .hint { color: var(--muted); font-size: 0.8rem; line-height: 1.5; }
  .intro { max-width: 52rem; margin-bottom: 1rem; }
  .muted { color: var(--muted); }
  .small { font-size: 0.78rem; }
  .error {
    background: color-mix(in srgb, var(--err) 15%, transparent);
    border: 1px solid var(--err); border-radius: 8px;
    padding: 0.6rem 1rem; margin-bottom: 1rem;
  }
  .panel {
    background: var(--panel); border: 1px solid var(--border); border-radius: 12px;
    padding: 1rem 1.25rem; margin-bottom: 1rem;
  }

  .order { list-style: none; display: flex; flex-direction: column; gap: 0.35rem; max-width: 28rem; padding: 0.6rem 0.8rem; }
  .order li { display: flex; align-items: center; gap: 0.6rem; }
  .rank { color: var(--muted); width: 1.4rem; text-align: right; font-variant-numeric: tabular-nums; }
  .gname { flex: 1; min-width: 0; overflow-wrap: anywhere; }
  .mv {
    width: 2rem; height: 2rem; border-radius: 8px; border: 1px solid var(--border);
    background: var(--bg); color: var(--text);
  }
  .mv:disabled { opacity: 0.35; cursor: default; }

  .tablewrap { overflow-x: auto; background: var(--panel); border: 1px solid var(--border); border-radius: 12px; }
  table { width: 100%; border-collapse: collapse; font-size: 0.9rem; }
  th, td { text-align: left; padding: 0.7rem 1rem; border-bottom: 1px solid var(--border); vertical-align: top; }
  th { color: var(--muted); font-weight: 500; font-size: 0.8rem; white-space: nowrap; }
  tr:last-child td { border-bottom: none; }
  .what { min-width: 12rem; }
  .what strong { display: block; font-weight: 500; }
  .colname { display: none; }
  .ctl { display: flex; align-items: center; gap: 0.5rem; }
  select, input:not([type='checkbox']) {
    background: var(--bg); border: 1px solid var(--border); border-radius: 8px;
    color: var(--text); padding: 0.4rem 0.55rem; font: inherit; font-size: 0.85rem;
    width: 11rem; max-width: 100%;
  }
  .lock { display: inline-flex; align-items: center; gap: 0.3rem; font-size: 0.8rem; color: var(--muted); white-space: nowrap; cursor: pointer; }
  .lock.on { color: var(--accent-2); }
  .lock.off { opacity: 0.45; cursor: default; }

  /* Phone: a card per preference, a labelled row per column. */
  @media (max-width: 48rem) {
    .tablewrap { overflow: visible; background: none; border: none; }
    table, tbody, tr, td { display: block; width: 100%; }
    thead { display: none; }
    tr { background: var(--panel); border: 1px solid var(--border); border-radius: 12px; margin-bottom: 0.75rem; }
    td { border-bottom: 1px solid var(--border); padding: 0.6rem 0.9rem; }
    tr:last-child td { border-bottom: 1px solid var(--border); }
    td:last-child, tr:last-child td:last-child { border-bottom: none; }
    .cell { display: flex; align-items: center; gap: 0.75rem; flex-wrap: wrap; }
    .colname { display: block; flex: 1; min-width: 6rem; color: var(--muted); font-size: 0.85rem; }
    select, input:not([type='checkbox']) { width: 12rem; }
  }
</style>
