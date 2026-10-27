package com.garybowers.cineva.sync

import android.util.Log
import com.garybowers.cineva.api.CinevaApi
import com.garybowers.cineva.api.HandoffQueue
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import okhttp3.sse.EventSource
import okhttp3.sse.EventSourceListener
import okhttp3.sse.EventSources
import java.util.concurrent.TimeUnit

/** What the server tells this device over its events stream (server-sent events). */
sealed class ServerEvent {
    /** Another of the user's devices is continuing what this one plays. */
    data class Takeover(val to: String, val itemId: String) : ServerEvent()
    /** Another device sent its queue here: play it. */
    data class Handoff(val queue: HandoffQueue) : ServerEvent()
    /** The viewer rated something elsewhere. */
    data class Rated(val itemId: String, val stars: Int) : ServerEvent()
    /** The user's devices changed: one signed in or out, or saved a queue. */
    data object Devices : ServerEvent()
    /** A library changed: listings are worth fetching again. */
    data class Items(val libraryId: String) : ServerEvent()
    /** This device was signed out elsewhere. */
    data object Revoked : ServerEvent()
    /** The server's themes changed, or the viewer's preferences did. */
    data object Themes : ServerEvent()
}

@Serializable private data class TakeoverData(val to: String = "", val itemId: String = "")
@Serializable private data class RatedData(val itemId: String = "", val stars: Int = 0)
@Serializable private data class ItemsData(val libraryId: String = "")

/** Turns an event's name and JSON into a ServerEvent; null for events the app does not use. Pure, so tested on the JVM. */
object EventParser {
    private val json = Json { ignoreUnknownKeys = true; explicitNulls = false }

    fun parse(type: String, data: String?): ServerEvent? = try {
        val d = data?.takeIf { it.isNotBlank() && it != "null" }
        when (type) {
            "takeover" -> d?.let { json.decodeFromString(TakeoverData.serializer(), it) }?.takeIf { it.itemId.isNotEmpty() }?.let { ServerEvent.Takeover(it.to, it.itemId) }
            "handoff" -> d?.let { ServerEvent.Handoff(json.decodeFromString(HandoffQueue.serializer(), it)) }
            "rating" -> d?.let { json.decodeFromString(RatedData.serializer(), it) }?.takeIf { it.itemId.isNotEmpty() }?.let { ServerEvent.Rated(it.itemId, it.stars) }
            "devices" -> ServerEvent.Devices
            "items" -> ServerEvent.Items(d?.let { json.decodeFromString(ItemsData.serializer(), it).libraryId } ?: "")
            "revoked" -> ServerEvent.Revoked
            "themes", "prefs" -> ServerEvent.Themes
            else -> null
        }
    } catch (e: Exception) {
        null
    }
}

/**
 * Keeps the events stream open while the app is signed in, so the server
 * counts this phone as online (Play on... needs that) and the phone hears
 * takeovers, handoffs and ratings as they happen. Reconnects with a
 * growing wait; the server's own heartbeat keeps the connection alive.
 */
class EventsClient(private val api: CinevaApi, http: OkHttpClient) {
    private val client = http.newBuilder().readTimeout(0, TimeUnit.SECONDS).build()
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private val _events = MutableSharedFlow<ServerEvent>(extraBufferCapacity = 32)
    val events: SharedFlow<ServerEvent> = _events
    private val _connected = MutableStateFlow(false)
    val connected: StateFlow<Boolean> = _connected
    private var source: EventSource? = null
    private var job: Job? = null

    fun start() {
        if (job?.isActive == true) return
        job = scope.launch {
            var wait = 1000L
            while (true) {
                val closed = kotlinx.coroutines.CompletableDeferred<Unit>()
                val url = runCatching { api.eventsUrl() }.getOrNull()
                if (url == null) { delay(5000); continue }
                source = EventSources.createFactory(client).newEventSource(
                    Request.Builder().url(url).header("Accept", "text/event-stream").build(),
                    object : EventSourceListener() {
                        override fun onOpen(eventSource: EventSource, response: Response) { _connected.value = true; wait = 1000L }
                        override fun onEvent(eventSource: EventSource, id: String?, type: String?, data: String) {
                            EventParser.parse(type ?: "message", data)?.let { _events.tryEmit(it) }
                        }
                        override fun onClosed(eventSource: EventSource) { _connected.value = false; closed.complete(Unit) }
                        override fun onFailure(eventSource: EventSource, t: Throwable?, response: Response?) {
                            _connected.value = false
                            if (t != null) Log.d(TAG, "events stream dropped: ${t.message}")
                            closed.complete(Unit)
                        }
                    },
                )
                closed.await()
                delay(wait)
                wait = (wait * 2).coerceAtMost(30_000L)
            }
        }
    }

    fun stop() {
        job?.cancel(); job = null
        source?.cancel(); source = null
        _connected.value = false
    }

    companion object { private const val TAG = "cineva.events" }
}
