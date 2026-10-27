package com.garybowers.cineva.api

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.serialization.KSerializer
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.Json
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import okhttp3.Interceptor
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import java.io.IOException

/** A failed call: the status and the server's message, if it gave one. */
class ApiException(val status: Int, message: String) : IOException(message)

/**
 * The /api/v1 client, hand-written against server/docs/openapi.yaml (see
 * ApiPaths). The server decides and the app shows, so every call maps
 * onto one route and returns the wire shape as it is.
 */
class CinevaApi(private val sessions: SessionStore, private val client: OkHttpClient) {

    val json: Json = Json { ignoreUnknownKeys = true; explicitNulls = false; encodeDefaults = true }

    private val base: HttpUrl?
        get() = sessions.current?.server?.trimEnd('/')?.let { "$it/api/v1/".toHttpUrlOrNull() }

    /** Turns a server-relative path such as a poster or a stream URL into an absolute one. */
    fun absolute(path: String): String {
        val server = sessions.current?.server?.trimEnd('/') ?: return path
        return if (path.startsWith("http://") || path.startsWith("https://")) path else server + path
    }

    // ---- account ----

    suspend fun me(): Me = get(ApiPaths.ME, Me.serializer())

    /**
     * Signs in and keeps the session. An API key (cnv_...) is used as the
     * bearer as it is; a password goes to POST /auth/login asking for a
     * bearer token, which the server is gaining (server/docs/offline.md).
     * Switching every sign-in to that is this one function.
     */
    suspend fun signIn(server: String, username: String, secret: String) {
        val trimmed = server.trim().trimEnd('/')
        val url = trimmed.toHttpUrlOrNull() ?: throw ApiException(0, "That is not a server address")
        if (secret.startsWith("cnv_")) {
            sessions.save(Session(url.toString().trimEnd('/'), secret, username))
            try {
                val me = me()
                sessions.save(Session(url.toString().trimEnd('/'), secret, me.username))
            } catch (e: IOException) {
                sessions.clear()
                throw e
            }
            return
        }
        val body = LoginRequest(
            username = username, password = secret,
            device = LoginDevice(sessions.deviceId, sessions.deviceName, "Cineva Android " + com.garybowers.cineva.BuildConfig.VERSION_NAME),
        )
        val res = withContext(Dispatchers.IO) {
            val req = Request.Builder()
                .url(url.newBuilder().addPathSegments("api/v1" + ApiPaths.LOGIN).build())
                .post(json.encodeToString(LoginRequest.serializer(), body).toRequestBody(JSON))
                .build()
            client.newCall(req).execute().use { r ->
                val text = r.body?.string() ?: ""
                if (!r.isSuccessful) throw ApiException(r.code, messageOf(text, r.code))
                json.decodeFromString(LoginResponse.serializer(), text)
            }
        }
        val token = res.token ?: throw ApiException(
            0, "This server does not sign apps in with a password yet: use an API key made in Settings",
        )
        sessions.save(Session(url.toString().trimEnd('/'), token, res.username.ifEmpty { username }))
    }

    suspend fun signOut() {
        val s = sessions.current ?: return
        if (!s.token.startsWith("cnv_")) {
            runCatching { post(ApiPaths.LOGOUT, "{}") }
        }
        sessions.clear()
    }

    // ---- libraries and listings ----

    suspend fun libraries(): List<Library> = get(ApiPaths.LIBRARIES, ListSerializer(Library.serializer()))

    suspend fun artists(libraryId: String, q: ListingQuery) =
        get(ApiPaths.fill(ApiPaths.ARTISTS, libraryId), MusicPage.serializer(), q.params())

    suspend fun albums(libraryId: String, q: ListingQuery) =
        get(ApiPaths.fill(ApiPaths.ALBUMS, libraryId), MusicPage.serializer(), q.params())

    suspend fun tracks(libraryId: String, q: ListingQuery) =
        get(ApiPaths.fill(ApiPaths.TRACKS, libraryId), MusicPage.serializer(), q.params())

    suspend fun genres(libraryId: String): List<GenreCount> =
        get(ApiPaths.fill(ApiPaths.GENRES, libraryId), ListSerializer(GenreCount.serializer()))

    suspend fun search(libraryId: String, query: String): LibrarySearch =
        get(ApiPaths.fill(ApiPaths.LIBRARY_SEARCH, libraryId), LibrarySearch.serializer(), mapOf("q" to query))

    suspend fun listening(libraryId: String): Listening =
        get(ApiPaths.fill(ApiPaths.LISTENING, libraryId), Listening.serializer())

    suspend fun item(id: String): ItemDetail = get(ApiPaths.fill(ApiPaths.ITEM, id), ItemDetail.serializer())

    suspend fun artist(id: String): ArtistPage = get(ApiPaths.fill(ApiPaths.ITEM_ARTIST, id), ArtistPage.serializer())

    suspend fun tracksUnder(id: String, shuffle: Boolean = false): List<Item> =
        get(ApiPaths.fill(ApiPaths.ITEM_TRACKS, id), ListSerializer(Item.serializer()),
            if (shuffle) mapOf("shuffle" to "true") else emptyMap())

    // ---- playback ----

    /** Asks the server what to play for a track, blocking: called from the player's loading thread. */
    fun playBlocking(id: String, caps: Capabilities): PlayInfo {
        val text = call(
            Request.Builder().url(url(ApiPaths.fill(ApiPaths.PLAY, id)))
                .post(json.encodeToString(Capabilities.serializer(), caps).toRequestBody(JSON)).build(),
        )
        return json.decodeFromString(PlayInfo.serializer(), text)
    }

    suspend fun progress(id: String, positionSecs: Int, paused: Boolean) {
        post(ApiPaths.fill(ApiPaths.PROGRESS, id), json.encodeToString(Progress.serializer(), Progress(positionSecs, paused)))
    }

    suspend fun stopPlayback(id: String) {
        withContext(Dispatchers.IO) {
            runCatching { call(Request.Builder().url(url(ApiPaths.fill(ApiPaths.PLAYBACK, id))).delete().build()) }
        }
    }

    suspend fun saveQueue(record: QueueRecord) {
        withContext(Dispatchers.IO) {
            call(
                Request.Builder().url(url(ApiPaths.MY_QUEUE))
                    .put(json.encodeToString(QueueRecord.serializer(), record).toRequestBody(JSON)).build(),
            )
        }
    }

    suspend fun myQueue(): HandoffQueue = get(ApiPaths.MY_QUEUE, HandoffQueue.serializer())

    // ---- devices ----

    suspend fun devices(): List<Device> = get(ApiPaths.DEVICES, ListSerializer(Device.serializer()))

    /** Continues what another device plays here: it pauses, and the answer is its queue and position. */
    suspend fun takeOver(deviceId: String): HandoffQueue = withContext(Dispatchers.IO) {
        json.decodeFromString(HandoffQueue.serializer(),
            call(Request.Builder().url(url(ApiPaths.fill(ApiPaths.DEVICE_TAKEOVER, deviceId))).post("{}".toRequestBody(JSON)).build()))
    }

    /** Sends the queue to another device, which starts playing it. */
    suspend fun playOn(deviceId: String, record: QueueRecord) {
        post(ApiPaths.fill(ApiPaths.DEVICE_PLAY, deviceId), json.encodeToString(QueueRecord.serializer(), record))
    }

    /** The events stream's address, for the SSE client. */
    fun eventsUrl(): HttpUrl = url(ApiPaths.EVENTS)

    // ---- playlists, ratings, lyrics ----

    suspend fun playlists(): List<Playlist> = get(ApiPaths.PLAYLISTS, ListSerializer(Playlist.serializer()))

    suspend fun playlist(id: String): PlaylistDetail = get(ApiPaths.fill(ApiPaths.PLAYLIST, id), PlaylistDetail.serializer())

    suspend fun createPlaylist(name: String): Playlist = withContext(Dispatchers.IO) {
        json.decodeFromString(Playlist.serializer(), call(Request.Builder().url(url(ApiPaths.PLAYLISTS))
            .post(json.encodeToString(PlaylistRequest.serializer(), PlaylistRequest(name)).toRequestBody(JSON)).build()))
    }

    /** Adds tracks, albums or artists (the server expands them) to a playlist. */
    suspend fun addToPlaylist(id: String, itemIds: List<String>): PlaylistDetail = withContext(Dispatchers.IO) {
        json.decodeFromString(PlaylistDetail.serializer(), call(Request.Builder().url(url(ApiPaths.fill(ApiPaths.PLAYLIST_ITEMS, id)))
            .post(json.encodeToString(PlaylistItems.serializer(), PlaylistItems(itemIds)).toRequestBody(JSON)).build()))
    }

    /** 1 to 5 stars; 0 clears. */
    suspend fun rate(id: String, stars: Int) {
        withContext(Dispatchers.IO) {
            call(Request.Builder().url(url(ApiPaths.fill(ApiPaths.RATING, id)))
                .put(json.encodeToString(Rating.serializer(), Rating(stars)).toRequestBody(JSON)).build())
        }
    }

    /**
     * A batch of a mix from a track, album or artist (server/docs/mix.md):
     * start for the first batch of one started on purpose, after the last
     * track the listener reached, exclude what is already queued.
     */
    suspend fun mix(seedId: String, size: Int = 20, exclude: List<String> = emptyList(), after: String = "", start: Boolean = false): MixResult =
        get(ApiPaths.fill(ApiPaths.ITEM_MIX, seedId), MixResult.serializer(), mapOf(
            "size" to size.toString(), "exclude" to exclude.take(1000).joinToString(","), "after" to after,
            "start" to if (start) "true" else "",
        ))

    /** The playlists for adding a track to: most recently added to first, each saying whether it holds the track. */
    suspend fun playlistsFor(trackId: String): List<Playlist> =
        get(ApiPaths.PLAYLISTS, ListSerializer(Playlist.serializer()), mapOf("for" to trackId))

    /** Undoes an append: the last count entries go. */
    suspend fun removeLastFromPlaylist(id: String, count: Int = 1): Int = withContext(Dispatchers.IO) {
        val u = url(ApiPaths.fill(ApiPaths.PLAYLIST_ITEMS_LAST, id), mapOf("count" to count.toString()))
        json.decodeFromString(Removed.serializer(), call(Request.Builder().url(u).delete().build())).removed
    }

    /** The track's lyrics, or null when it has none. */
    suspend fun lyrics(id: String): Lyrics? = try {
        get(ApiPaths.fill(ApiPaths.ITEM_LYRICS, id), Lyrics.serializer())
    } catch (e: ApiException) {
        if (e.status == 404) null else throw e
    }

    // ---- themes and preferences ----

    suspend fun themes(): List<ThemeInfo> = get(ApiPaths.THEMES, ListSerializer(ThemeInfo.serializer()))

    /** The holiday on now, or null when none is. */
    suspend fun occasion(): Occasion? = withContext(Dispatchers.IO) {
        client.newCall(Request.Builder().url(url(ApiPaths.OCCASION)).build()).execute().use { r ->
            if (r.code == 204 || !r.isSuccessful) null
            else runCatching { json.decodeFromString(Occasion.serializer(), r.body?.string() ?: "") }.getOrNull()
        }
    }

    suspend fun myPrefs(): MyPrefs = get(ApiPaths.MY_PREFS, MyPrefs.serializer())

    /** Sets one of the user's preferences, as the web client's Settings does. */
    suspend fun setPref(key: String, value: String) {
        withContext(Dispatchers.IO) {
            val body = kotlinx.serialization.json.buildJsonObject { put(key, kotlinx.serialization.json.JsonPrimitive(value)) }.toString()
            call(Request.Builder().url(url(ApiPaths.MY_PREFS)).put(body.toRequestBody(JSON)).build())
        }
    }

    // ---- plumbing ----

    private fun url(path: String, params: Map<String, String> = emptyMap()): HttpUrl {
        val b = (base ?: throw ApiException(0, "Not signed in")).newBuilder().addPathSegments(path.trimStart('/'))
        for ((k, v) in params) if (v.isNotEmpty()) b.addQueryParameter(k, v)
        return b.build()
    }

    private suspend fun <T> get(path: String, serializer: KSerializer<T>, params: Map<String, String> = emptyMap()): T =
        withContext(Dispatchers.IO) {
            json.decodeFromString(serializer, call(Request.Builder().url(url(path, params)).build()))
        }

    private suspend fun post(path: String, body: String): String = withContext(Dispatchers.IO) {
        call(Request.Builder().url(url(path)).post(body.toRequestBody(JSON)).build())
    }

    private fun call(req: Request): String = client.newCall(req).execute().use { r ->
        val text = r.body?.string() ?: ""
        // A dead session (signed out elsewhere, expired) means the sign-in
        // screen, not an error on every page.
        if (r.code == 401 && sessions.current != null && !req.url.encodedPath.endsWith(ApiPaths.LOGIN)) sessions.clear()
        if (!r.isSuccessful) throw ApiException(r.code, messageOf(text, r.code))
        text
    }

    private fun messageOf(text: String, code: Int): String =
        runCatching { json.parseToJsonElement(text).let { it.toString() } }
            .getOrNull()?.takeIf { it.contains("message") }
            ?.let { Regex("\"message\"\\s*:\\s*\"([^\"]*)\"").find(it)?.groupValues?.get(1) }
            ?: "HTTP $code"

    companion object {
        private val JSON = "application/json".toMediaType()

        /** Adds the session's bearer to every request to the server. */
        fun authInterceptor(sessions: SessionStore) = Interceptor { chain ->
            val token = sessions.current?.token
            val req = if (token != null && chain.request().header("Authorization") == null) {
                chain.request().newBuilder().header("Authorization", "Bearer $token").build()
            } else chain.request()
            chain.proceed(req)
        }
    }
}

/** A page of a listing: the filters the paged endpoints take. */
data class ListingQuery(
    val q: String = "",
    val sort: String = "",
    val letter: String = "",
    val genre: String = "",
    val artist: String = "",
    val page: Int = 1,
    val size: Int = 60,
) {
    fun params(): Map<String, String> = mapOf(
        "q" to q, "sort" to sort, "letter" to letter, "genre" to genre, "artist" to artist,
        "page" to page.toString(), "size" to size.toString(),
    )
}
