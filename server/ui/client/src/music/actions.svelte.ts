/**
 * What the music views can ask the app to do.
 *
 * The artist page, playlists and track menus are their own components, but
 * playing, queueing and navigating belong to App (the player and the routes
 * live there). App registers its handlers once with setMusicActions, and the
 * components call them through music(), rather than each being handed the
 * same half-dozen callbacks through every layer.
 */

import type { Item } from '../lib/api'

export interface MusicActions {
  /** Start these tracks from index and open the listening view. */
  play(tracks: Item[], index?: number, shuffle?: boolean): void
  /** Add to the end of the music queue, starting it if nothing is playing. */
  queue(tracks: Item[]): void
  /** An album or artist, by item, or by id when only the id is to hand. */
  openAlbum(album: Item | ItemRef): void
  openArtist(artist: Item | ItemRef): void
  openPlaylist(id: string): void
  notify(kind: 'info' | 'error', text: string): void
  /** A ZIP of an album or artist: "original", "mp3-320", "mp3-192", "ogg-192". */
  download(id: string, format: string): void
}

/** An item known only by id, and the library it is in. */
export type ItemRef = { id: string; libraryId: string }

let registered: MusicActions | null = null

export function setMusicActions(a: MusicActions): void {
  registered = a
}

export function music(): MusicActions {
  if (!registered) throw new Error('music actions used before App registered them')
  return registered
}

/**
 * The "Add to playlist" picker, mounted once by App. Setting ids opens it;
 * label names what is being added ("Blue Lines", "3 tracks").
 */
export const picker = $state({ ids: [] as string[], label: '' })

export function addToPlaylist(ids: string[], label: string): void {
  picker.ids = ids
  picker.label = label
}

/** "3:07" for a track. */
export function trackTime(secs?: number): string {
  if (!secs) return ''
  return `${Math.floor(secs / 60)}:${String(secs % 60).padStart(2, '0')}`
}

/** "1 h 12 min" for an album or playlist. */
export function totalTime(secs: number): string {
  const m = Math.round(secs / 60)
  if (m < 60) return `${m} min`
  return `${Math.floor(m / 60)} h ${m % 60} min`
}

/** "3 plays", "1 play". */
export function plays(n?: number): string {
  return n === 1 ? '1 play' : `${n ?? 0} plays`
}

/** "today", "yesterday", "12 Mar", "Mar 2024" for a server timestamp (UTC). */
export function playedWhen(at?: string): string {
  if (!at) return ''
  const d = new Date(at.replace(' ', 'T') + (at.endsWith('Z') ? '' : 'Z'))
  if (Number.isNaN(d.getTime())) return ''
  const days = Math.floor((Date.now() - d.getTime()) / 86400000)
  if (days <= 0) return 'today'
  if (days === 1) return 'yesterday'
  if (days < 7) return `${days} days ago`
  const sameYear = d.getFullYear() === new Date().getFullYear()
  return d.toLocaleDateString(undefined, sameYear ? { day: 'numeric', month: 'short' } : { month: 'short', year: 'numeric' })
}
