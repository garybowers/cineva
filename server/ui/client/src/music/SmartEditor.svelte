<script lang="ts">
  // Rules for a smart playlist: a few conditions on what the library holds
  // and what the listener has played, a sort and a limit. Evaluated by the
  // server each time the playlist is opened.
  import { untrack } from 'svelte'
  import { Plus, X, Sparkles } from '@lucide/svelte'
  import { listLibraries, libraryGenres, type SmartField, type SmartRules } from '../lib/api'
  import { fields, opLabel, sorts, describe } from './smart'

  let {
    name: initialName = '',
    rules: initialRules,
    onsave,
    onclose,
  }: {
    name?: string
    rules: SmartRules
    onsave: (name: string, rules: SmartRules) => Promise<void>
    onclose: () => void
  } = $props()

  // Edited as a copy, taken once: the dialogue opens over one playlist.
  let name = $state(untrack(() => initialName))
  let rules: SmartRules = $state(untrack(() => $state.snapshot(initialRules) as SmartRules))
  let error = $state('')
  let busy = $state(false)
  let genres: string[] = $state([])

  // Genre suggestions from the music libraries' own tags.
  $effect(() => {
    listLibraries()
      .then((libs) => Promise.all(libs.filter((l) => l.mediaType === 'music').map((l) => libraryGenres(l.id))))
      .then((lists) => (genres = [...new Set(lists.flat().map((g) => g.genre))].sort((a, b) => a.localeCompare(b))))
      .catch(() => (genres = []))
    const esc = (e: KeyboardEvent) => e.key === 'Escape' && onclose()
    document.addEventListener('keydown', esc)
    return () => document.removeEventListener('keydown', esc)
  })

  function setField(i: number, f: SmartField) {
    const r = rules.rules[i]
    r.field = f
    if (!fields[f].ops.includes(r.op)) r.op = fields[f].ops[0]
    r.value = fields[f].unit === 'days' ? '30' : fields[f].unit === 'plays' ? '0' : ''
  }
  function addRule() {
    rules.rules.push({ field: 'artist', op: 'is', value: '' })
  }

  async function save(e: SubmitEvent) {
    e.preventDefault()
    if (!name.trim()) {
      error = 'Give the playlist a name.'
      return
    }
    busy = true
    error = ''
    try {
      // Number inputs bind numbers; the rules carry their values as text.
      const plain = $state.snapshot(rules) as SmartRules
      await onsave(name.trim(), { ...plain, limit: Number(plain.limit) || 100, rules: plain.rules.map((r) => ({ ...r, value: String(r.value) })) })
    } catch (err) {
      error = err instanceof Error ? err.message : String(err)
    } finally {
      busy = false
    }
  }
</script>

<div class="se-back" role="presentation" onclick={(e) => e.target === e.currentTarget && onclose()}>
  <div class="se-box" role="dialog" aria-modal="true" aria-labelledby="se-title">
  <form class="se" onsubmit={save}>
    <header>
      <h2 id="se-title"><Sparkles size={18} /> {initialName ? 'Edit smart playlist' : 'New smart playlist'}</h2>
      <button type="button" class="x" onclick={onclose} aria-label="Close"><X size={18} /></button>
    </header>

    <label class="field">
      <span>Name</span>
      <input bind:value={name} placeholder="Jazz I haven't heard lately" maxlength="120" />
    </label>

    <div class="match">
      Tracks matching
      <select bind:value={rules.match} aria-label="Match">
        <option value="all">all</option>
        <option value="any">any</option>
      </select>
      of these rules
    </div>

    <ul class="rules">
      {#each rules.rules as r, i (i)}
        <li>
          <select value={r.field} onchange={(e) => setField(i, (e.target as HTMLSelectElement).value as SmartField)} aria-label="Field">
            {#each Object.entries(fields) as [id, f] (id)}<option value={id}>{f.label}</option>{/each}
          </select>
          <select bind:value={r.op} aria-label="Condition">
            {#each fields[r.field].ops as op (op)}<option value={op}>{opLabel(r.field, op)}</option>{/each}
          </select>
          {#if fields[r.field].unit}
            <input class="num" type="number" min="0" inputmode="numeric" bind:value={r.value} aria-label="Value" required />
            {#if fields[r.field].unit === 'days'}<span class="unit">days</span>{:else if fields[r.field].unit === 'plays'}<span class="unit">times</span>{/if}
          {:else}
            <input class="text" bind:value={r.value} list={r.field === 'genre' ? 'se-genres' : undefined}
              placeholder={r.field === 'format' ? 'flac' : r.field === 'genre' ? 'Jazz' : ''} aria-label="Value" required />
          {/if}
          <button type="button" class="x" onclick={() => rules.rules.splice(i, 1)} aria-label="Remove rule"><X size={16} /></button>
        </li>
      {/each}
    </ul>
    <datalist id="se-genres">{#each genres as g (g)}<option value={g}></option>{/each}</datalist>
    <button type="button" class="add" onclick={addRule}><Plus size={15} /> Add rule</button>

    <div class="row">
      <label class="field"><span>Order</span>
        <select bind:value={rules.sort}>{#each sorts as s (s.id)}<option value={s.id}>{s.label}</option>{/each}</select>
      </label>
      <label class="field"><span>Up to</span>
        <input class="num" type="number" min="1" max="1000" bind:value={rules.limit} />
      </label>
    </div>

    <p class="summary">{describe({ ...rules, rules: rules.rules.filter((r) => r.value !== '') })}</p>
    {#if error}<p class="err">{error}</p>{/if}
    <footer>
      <button type="button" class="btn" onclick={onclose}>Cancel</button>
      <button type="submit" class="primary" disabled={busy}>{busy ? 'Saving…' : 'Save'}</button>
    </footer>
  </form>
  </div>
</div>

<style>
  .se-back {
    position: fixed; inset: 0; z-index: 200; display: grid; place-items: center; padding: 1rem;
    background: color-mix(in srgb, var(--bg) 70%, transparent);
  }
  .se-box { width: min(38rem, 100%); display: flex; }
  .se {
    width: 100%; max-height: 92vh; overflow-y: auto; display: flex; flex-direction: column; gap: 0.8rem;
    background: var(--panel); border: 1px solid var(--border); border-radius: 14px; padding: 1.1rem;
    box-shadow: 0 16px 48px color-mix(in srgb, var(--bg) 80%, transparent);
  }
  header { display: flex; align-items: center; justify-content: space-between; }
  h2 { display: flex; align-items: center; gap: 0.5rem; font-size: 1.05rem; margin: 0; }
  .x { display: grid; place-items: center; width: 2rem; height: 2rem; border-radius: 50%; color: var(--muted); flex: none; }
  .x:hover { color: var(--text); background: var(--bg); }
  .field { display: flex; flex-direction: column; gap: 0.3rem; font-size: 0.82rem; color: var(--muted); }
  input, select {
    background: var(--bg); color: var(--text); border: 1px solid var(--border); border-radius: 8px;
    padding: 0.45rem 0.6rem; font: inherit; font-size: 0.9rem; min-width: 0;
  }
  input:focus, select:focus { outline: none; border-color: var(--accent); }
  .match { display: flex; align-items: center; gap: 0.5rem; flex-wrap: wrap; font-size: 0.9rem; }
  .rules { list-style: none; display: flex; flex-direction: column; gap: 0.5rem; }
  .rules li { display: flex; flex-wrap: wrap; align-items: center; gap: 0.4rem; padding: 0.5rem; border: 1px solid var(--border); border-radius: 10px; }
  .rules .text { flex: 1 1 8rem; }
  .num { width: 5.5rem; }
  .unit { color: var(--muted); font-size: 0.85rem; }
  .rules li .x { margin-left: auto; }
  .add { align-self: flex-start; display: inline-flex; align-items: center; gap: 0.35rem; color: var(--accent-2); font-size: 0.88rem; }
  .row { display: flex; gap: 1rem; flex-wrap: wrap; }
  .summary { color: var(--muted); font-size: 0.82rem; }
  .err { color: var(--err); font-size: 0.86rem; }
  footer { display: flex; justify-content: flex-end; gap: 0.5rem; }
  .primary, .btn { border-radius: 999px; padding: 0.5rem 1.2rem; font-size: 0.9rem; border: 1px solid var(--border); }
  .primary { border-color: var(--accent); background: color-mix(in srgb, var(--accent) 22%, var(--panel)); font-weight: 600; }
  .primary:disabled { opacity: 0.6; }
  .btn:hover { border-color: var(--accent); }
  @media (max-width: 30rem) {
    .se-back { padding: 0; place-items: end stretch; }
    .se-box { width: 100%; }
    .se { border-radius: 14px 14px 0 0; }
  }
</style>
