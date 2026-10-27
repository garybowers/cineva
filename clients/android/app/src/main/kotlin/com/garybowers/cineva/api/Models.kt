package com.garybowers.cineva.api

import kotlinx.serialization.Serializable

// The wire shapes the app reads, as server/docs/openapi.yaml defines them.
// Only the fields the music screens use are declared; the decoder ignores
// the rest, so a server that gains a field changes nothing here.

@Serializable
data class Item(
    val id: String,
    val libraryId: String = "",
    val kind: String = "",
    val parentId: String? = null,
    val title: String = "",
    val year: Int = 0,
    val discNum: Int = 0,
    val trackNum: Int = 0,
    val artist: String = "",
    val album: String = "",
    val albumArtist: String = "",
    val runtimeSecs: Int = 0,
    val container: String = "",
    val audioCodec: String = "",
    val bitrate: Int = 0,
    val sampleRate: Int = 0,
    val bitDepth: Int = 0,
    val poster: String = "",
    val overview: String = "",
    val compilation: Boolean = false,
    val playCount: Int = 0,
    val userRating: Int = 0,
    val childCount: Int = 0,
    val genres: List<String> = emptyList(),
)

@Serializable
data class MusicPage(
    val items: List<Item> = emptyList(),
    val total: Int = 0,
    val page: Int = 1,
    val size: Int = 0,
    val index: Map<String, Int>? = null,
)

@Serializable
data class Library(
    val id: String,
    val name: String = "",
    val mediaType: String = "",
    val itemCount: Int = 0,
)

@Serializable
data class ArtistStats(val tracks: Int = 0, val secs: Int = 0, val plays: Int = 0)

@Serializable
data class ArtistPage(
    val artist: Item,
    val albums: List<Item> = emptyList(),
    val appearsOn: List<Item> = emptyList(),
    val topTracks: List<Item> = emptyList(),
    val stats: ArtistStats = ArtistStats(),
    val bioUrl: String? = null,
    val lookingUp: Boolean = false,
)

@Serializable
data class ItemDetail(val item: Item, val children: List<Item> = emptyList())

@Serializable
data class Listening(
    val recentlyPlayed: List<Item> = emptyList(),
    val mostPlayed: List<Item> = emptyList(),
    val mostPlayedAlbums: List<Item> = emptyList(),
)

@Serializable
data class LibrarySearch(
    val artists: List<Item> = emptyList(),
    val albums: List<Item> = emptyList(),
    val tracks: List<Item> = emptyList(),
)

@Serializable
data class GenreCount(val genre: String, val count: Int = 0, val tracks: Int = 0)

@Serializable
data class Me(
    val id: String,
    val username: String = "",
    val displayName: String = "",
    val role: String = "",
)

/** What the server answers when a track is played: where to fetch it, and what will sound. */
@Serializable
data class PlayInfo(
    val mode: String = "",
    val url: String = "",
    val durationSecs: Int = 0,
    val positionSecs: Int = 0,
    val source: PlaySource? = null,
)

@Serializable
data class PlaySource(
    val container: String = "",
    val audio: String = "",
    val audioCodec: String = "",
    val bitrate: Int = 0,
    val sampleRate: Int = 0,
    val bitDepth: Int = 0,
)

/** The device capabilities sent with a play request: what ExoPlayer decodes. */
@Serializable
data class Capabilities(
    val containers: List<String> = listOf("flac", "mp3", "m4a", "mp4", "ogg", "opus", "wav", "aac"),
    val audioCodecs: List<String> = listOf("flac", "mp3", "aac", "opus", "vorbis"),
    val videoCodecs: List<String> = emptyList(),
    /** What the phone asks for on this network: original, opus-128 or opus-64; the server's preference and lock still decide. */
    val quality: String = "",
)

@Serializable
data class Progress(val positionSecs: Int, val paused: Boolean)

// The session queue record (server/docs/music.md, part 2).

@Serializable
data class QueueEntry(val id: String, val ord: Int)

@Serializable
data class QueueSource(val kind: String, val id: String = "", val title: String = "")

/** The queue is, or ends in, a mix (server/docs/mix.md): what it grew from and where it has got to. */
@Serializable
data class QueueMix(
    val seedId: String,
    val kind: String = "track",
    val title: String = "",
    /** The last track the mix was asked to continue from. */
    val after: String = "",
    /** The queue index the mix's entries begin at. */
    val from: Int = 0,
    /** The signals the last batch had, for the sub-line. */
    val basis: List<String> = emptyList(),
)

@Serializable
data class QueueRecord(
    val entries: List<QueueEntry> = emptyList(),
    val index: Int = 0,
    val shuffle: Boolean = false,
    val repeat: String = "off",
    val positionSecs: Double = 0.0,
    val source: QueueSource? = null,
    val mix: QueueMix? = null,
)

/** A batch of a mix: GET /items/{id}/mix. */
@Serializable
data class MixResult(val seed: Item, val basis: List<String> = emptyList(), val tracks: List<Item> = emptyList())

/** Signing in with a password: the bearer form the server is gaining (server/docs/offline.md). */
@Serializable
data class LoginRequest(
    val username: String,
    val password: String,
    val bearer: Boolean = true,
    val device: LoginDevice,
)

@Serializable
data class LoginDevice(val id: String, val name: String, val client: String)

@Serializable
data class LoginResponse(val token: String? = null, val sessionId: String = "", val id: String = "", val username: String = "")

// ---- playlists, ratings, lyrics ----

@Serializable
data class Playlist(
    val id: String,
    val name: String = "",
    val smart: kotlinx.serialization.json.JsonElement? = null,
    val trackCount: Int = 0,
    val durationSecs: Int = 0,
    val covers: List<String> = emptyList(),
    /** When a track was last added; absent when never. */
    val lastAddedAt: String? = null,
    /** With ?for=, whether the playlist already holds that track. */
    val holds: Boolean = false,
) {
    val isSmart: Boolean get() = smart != null && smart !is kotlinx.serialization.json.JsonNull
}

@Serializable
data class PlaylistDetail(val playlist: Playlist, val tracks: List<Item> = emptyList())

@Serializable
data class PlaylistItems(val itemIds: List<String>)

@Serializable
data class PlaylistRequest(val name: String)

/** DELETE /playlists/{id}/items/last: how many entries went. */
@Serializable
data class Removed(val removed: Int = 0)

@Serializable
data class Rating(val stars: Int)

@Serializable
data class Lyrics(val synced: Boolean = false, val text: String = "")

// ---- devices: Continue here and Play on ----

@Serializable
data class NowPlayingOn(val item: Item, val positionSecs: Int = 0, val durationSecs: Int = 0, val paused: Boolean = false)

@Serializable
data class Device(
    val id: String,
    val name: String = "",
    val client: String = "",
    val current: Boolean = false,
    val online: Boolean = false,
    val playing: NowPlayingOn? = null,
) {
    val label: String get() = name.ifEmpty { client.ifEmpty { "Another device" } }
}

/** A whole queue as the server hands it between devices, items resolved. */
@Serializable
data class HandoffQueue(
    val items: List<Item> = emptyList(),
    val index: Int = 0,
    val shuffle: Boolean = false,
    val repeat: String = "off",
    val positionSecs: Double = 0.0,
    val source: QueueSource? = null,
    val mix: QueueMix? = null,
    val from: String = "",
)

// ---- themes and preferences (server/docs/themes.md, personalisation.md) ----

/** A theme as GET /themes lists it: tokens by name, a background picture and how strongly bg dims it. */
@Serializable
data class ThemeInfo(
    val id: String,
    val name: String = "",
    val appearance: String = "dark",
    val tokens: Map<String, String> = emptyMap(),
    val background: String? = null,
    val dim: Int = 0,
    val effect: String = "",
    val occasion: String = "",
    val custom: Boolean = false,
)

/** The holiday on now, and the theme for it. */
@Serializable
data class Occasion(val id: String, val name: String = "", val until: String = "", val themeId: String = "")

@Serializable
data class PrefValue(val value: String = "", val source: String = "", val locked: Boolean = false)

/** GET /auth/me/prefs: each preference resolved through its layers. */
@Serializable
data class MyPrefs(val prefs: Map<String, PrefValue> = emptyMap()) {
    fun value(key: String): String? = prefs[key]?.value
}
