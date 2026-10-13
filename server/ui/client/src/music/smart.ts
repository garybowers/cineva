// Smart playlist rules as the editor offers them and as a sentence reads
// them. The server checks the same combinations (store/playlists.go).

import type { SmartField, SmartOp, SmartRule, SmartRules, SmartSort } from '../lib/api'

export interface FieldSpec {
  label: string
  ops: SmartOp[]
  /** Whole numbers (and what they count), or free text. */
  unit?: 'days' | 'plays' | 'year'
}

export const fields: Record<SmartField, FieldSpec> = {
  artist: { label: 'Artist', ops: ['is', 'isNot', 'contains'] },
  album: { label: 'Album', ops: ['is', 'contains'] },
  title: { label: 'Title', ops: ['contains'] },
  genre: { label: 'Genre', ops: ['is', 'isNot'] },
  year: { label: 'Year', ops: ['is', 'gt', 'lt'], unit: 'year' },
  format: { label: 'Format', ops: ['is'] },
  added: { label: 'Added', ops: ['inLast', 'notInLast'], unit: 'days' },
  plays: { label: 'Plays', ops: ['is', 'gt', 'lt'], unit: 'plays' },
  lastPlayed: { label: 'Last played', ops: ['inLast', 'notInLast'], unit: 'days' },
}

/** An operator's wording in the editor, which depends on the field. */
export function opLabel(field: SmartField, op: SmartOp): string {
  const unit = fields[field].unit
  switch (op) {
    case 'is': return 'is'
    case 'isNot': return 'is not'
    case 'contains': return 'contains'
    case 'gt': return unit === 'year' ? 'after' : 'more than'
    case 'lt': return unit === 'year' ? 'before' : 'fewer than'
    case 'inLast': return 'in the last'
    case 'notInLast': return 'not in the last'
  }
}

export const sorts: { id: SmartSort; label: string }[] = [
  { id: 'random', label: 'Random' },
  { id: 'plays', label: 'Most played' },
  { id: 'lastPlayed', label: 'Recently played' },
  { id: 'leastRecent', label: 'Longest unplayed' },
  { id: 'added', label: 'Recently added' },
  { id: 'year', label: 'Newest first' },
  { id: 'artist', label: 'Artist' },
  { id: 'album', label: 'Album' },
  { id: 'title', label: 'Title' },
]

/** "Genre is Jazz", "Played more than 5 times", "Not played in the last 30 days". */
export function describeRule(r: SmartRule): string {
  const n = Number(r.value)
  const days = n === 1 ? 'day' : `${n} days`
  switch (r.field) {
    case 'plays':
      if (r.op === 'is') return n === 0 ? 'Never played' : `Played ${n === 1 ? 'once' : `${n} times`}`
      return `Played ${r.op === 'gt' ? 'more' : 'fewer'} than ${n === 1 ? 'once' : `${n} times`}`
    case 'lastPlayed':
      return r.op === 'inLast' ? `Played in the last ${days}` : `Not played in the last ${days}`
    case 'added':
      return r.op === 'inLast' ? `Added in the last ${days}` : `Added more than ${days} ago`
    case 'year':
      return `From ${r.op === 'is' ? '' : opLabel('year', r.op) + ' '}${r.value}`
    default:
      return `${fields[r.field].label} ${opLabel(r.field, r.op)} ${r.value}`
  }
}

export function describe(rules: SmartRules): string {
  const parts = rules.rules.map(describeRule)
  const joined = parts.length ? parts.join(rules.match === 'any' ? ', or ' : ', ') : 'Every track'
  const sort = sorts.find((s) => s.id === rules.sort)?.label.toLowerCase() ?? rules.sort
  return `${joined} · ${sort} · up to ${rules.limit}`
}

export function blankRules(): SmartRules {
  return { match: 'all', rules: [{ field: 'genre', op: 'is', value: '' }], sort: 'random', limit: 100 }
}
