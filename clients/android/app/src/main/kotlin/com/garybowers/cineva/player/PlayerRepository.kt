package com.garybowers.cineva.player

import android.content.ComponentName
import android.content.Context
import android.util.Log
import androidx.core.net.toUri
import androidx.media3.common.MediaItem
import androidx.media3.common.MediaMetadata
import androidx.media3.common.Player
import androidx.media3.session.MediaController
import androidx.media3.session.SessionToken
import com.garybowers.cineva.api.CinevaApi
import com.garybowers.cineva.api.HandoffQueue
import com.garybowers.cineva.api.Item
import com.garybowers.cineva.api.PlayInfo
import com.garybowers.cineva.api.QueueMix
import com.garybowers.cineva.api.QueueSource
import com.garybowers.cineva.api.SessionStore
import com.garybowers.cineva.data.Ratings
import com.garybowers.cineva.data.ThemeStore
import com.garybowers.cineva.sync.EventsClient
import com.garybowers.cineva.sync.ServerEvent
import com.garybowers.cineva.ui.theme.ArtColourCache
import com.google.common.util.concurrent.MoreExecutors
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch

/** What the screens show of playback: the queue, whether it is playing, and where. */
data class PlayerState(
    val queue: Queue = Queue(),
    val isPlaying: Boolean = false,
    val positionMs: Long = 0,
    val durationMs: Long = 0,
    /** A batch of the mix is being fetched. */
    val mixLoading: Boolean = false,
) {
    val current: Item? get() = queue.current
}

/**
 * The app's side of playback: holds the queue, drives the service's
 * player through a MediaController, keeps the server told (the 15-second
 * heartbeat, the queue record, a track's end), listens to the events
 * stream for takeovers and handoffs, and exposes state to the screens.
 * One per process; it outlives the activity, as the service does.
 */
class PlayerRepository(
    private val context: Context,
    private val api: CinevaApi,
    private val decisions: PlayDecisions,
    private val events: EventsClient,
    private val sessions: SessionStore,
    private val artColours: ArtColourCache,
    private val ratings: Ratings,
    private val themes: ThemeStore,
) {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main)
    private val _state = MutableStateFlow(PlayerState())
    val state: StateFlow<PlayerState> = _state.asStateFlow()

    /** Short messages for the screens: "Continued on Living-room TV". */
    private val _notices = MutableSharedFlow<String>(extraBufferCapacity = 8)
    val notices: SharedFlow<String> = _notices

    private var controller: MediaController? = null
    private var queue = Queue()
    private var saveJob: Job? = null
    private var ticker: Job? = null
    private var pendingSeekMs: Long = 0
    private var extending = false
    private var mixLoading = false
    /** The mix request that came back empty or failed, not asked again until the queue moves on. */
    private var exhaustedFor = ""

    init {
        // The events stream runs while signed in: it is what makes this
        // phone "online" for Play on, and what carries takeovers here.
        scope.launch {
            sessions.session.collectLatest { s ->
                if (s == null) { events.stop(); return@collectLatest }
                // The theme follows the account: fetched at every sign-in
                // and start, kept from the last time until then.
                themes.refresh()
                events.start()
                events.events.collect { onEvent(it) }
            }
        }
    }

    /** The server's decision for the playing track, for the quality badge. */
    fun decision(itemId: String): PlayInfo? = decisions.forItem(itemId)

    private fun connect(then: (MediaController) -> Unit) {
        controller?.let { then(it); return }
        val token = SessionToken(context, ComponentName(context, PlaybackService::class.java))
        val future = MediaController.Builder(context, token).buildAsync()
        future.addListener({
            val c = future.get()
            controller = c
            c.addListener(listener)
            startTicker()
            then(c)
        }, MoreExecutors.directExecutor())
    }

    private val listener = object : Player.Listener {
        override fun onMediaItemTransition(mediaItem: MediaItem?, reason: Int) {
            val c = controller ?: return
            val from = queue.index
            queue = queue.jump(c.currentMediaItemIndex)
            publish()
            val old = queue.tracks.getOrNull(from)
            if (old != null && from != queue.index) scope.launch { api.stopPlayback(old.id) }
            colourFor(queue.current)
            scheduleSave()
            maybeExtend()
        }

        override fun onIsPlayingChanged(isPlaying: Boolean) {
            publish()
            heartbeat()
        }

        override fun onPlaybackStateChanged(playbackState: Int) {
            publish()
            if (playbackState == Player.STATE_READY && pendingSeekMs > 0) {
                controller?.seekTo(pendingSeekMs)
                pendingSeekMs = 0
            }
            if (playbackState == Player.STATE_ENDED) {
                queue.current?.let { t -> scope.launch { api.progress(t.id, t.runtimeSecs, true); api.stopPlayback(t.id) } }
            }
        }
    }

    // ---- what the screens call ----

    fun play(tracks: List<Item>, startIndex: Int = 0, source: QueueSource? = null, shuffle: Boolean = false, startSecs: Double = 0.0, mix: QueueMix? = null) {
        if (tracks.isEmpty()) return
        queue = Queue.fromTracks(tracks, startIndex, source, shuffle, mix)
        start(startSecs)
    }

    /** Plays a queue as another device handed it over: its order, shuffle, repeat, position and mix. */
    fun playHandoff(q: HandoffQueue) {
        if (q.items.isEmpty()) return
        queue = Queue(tracks = q.items, ords = q.items.indices.toList(), index = q.index.coerceIn(0, q.items.size - 1),
            shuffle = q.shuffle, repeat = q.repeat.ifEmpty { "off" }, source = q.source, mix = q.mix)
        start(q.positionSecs)
    }

    // ---- mixes (server/docs/mix.md) ----

    /** Starts a mix from a track, album or artist on purpose: the first batch replaces the queue and plays. */
    fun startMix(seed: Item, then: (String?) -> Unit = {}) {
        scope.launch {
            runCatching { api.mix(seed.id, start = true) }
                .onSuccess { res ->
                    if (res.tracks.isEmpty()) { then("Nothing to mix from yet"); return@onSuccess }
                    exhaustedFor = ""
                    val mix = QueueMix(seedId = seed.id, kind = seed.kind, title = seed.title, after = res.tracks.last().id, from = 0, basis = res.basis)
                    play(res.tracks, 0, QueueSource("mix", seed.id, seed.title), mix = mix)
                    then(null)
                }
                .onFailure { then(it.message) }
        }
    }

    /** Drops what the mix added and has not played, and forgets it; what is playing keeps playing. */
    fun stopMix() = connect { c ->
        val m = queue.mix ?: return@connect
        val keep = maxOf(queue.mixFrom, queue.index + 1).coerceAtMost(queue.tracks.size)
        queue = queue.stopMix()
        if (c.mediaItemCount > keep) c.removeMediaItems(keep, c.mediaItemCount)
        exhaustedFor = ""
        publish(); scheduleSave()
        Log.d(TAG, "mix stopped: ${m.title}")
    }

    /**
     * Keeps a mix fed, and carries an ordinary queue on when it is ending
     * and the person wants that (MixRules): the next batch is fetched and
     * added under the mix, so what is coming is visible before it plays.
     */
    private fun maybeExtend() {
        if (extending) return
        val req = MixRules.next(queue, themes.state.value.autoplay) ?: return
        if (req.key == exhaustedFor) return
        extending = true
        mixLoading = true
        publish()
        scope.launch {
            try {
                val res = api.mix(req.seedId, size = 20, exclude = queue.tracks.map { it.id }, after = req.after)
                // The queue changed meanwhile: this batch is for an ending that is gone.
                if (queue.tracks.lastOrNull()?.id != req.after) return@launch
                if (res.tracks.isEmpty()) { exhaustedFor = req.key; return@launch }
                val mix = QueueMix(seedId = req.seedId, kind = req.kind, title = req.title, after = res.tracks.last().id, basis = res.basis)
                queue = queue.appendMix(res.tracks, mix)
                connect { c -> c.addMediaItems(res.tracks.map(::mediaItem)); publish(); scheduleSave() }
            } catch (e: Exception) {
                Log.w(TAG, "extending the queue failed", e)
                exhaustedFor = req.key
            } finally {
                extending = false
                mixLoading = false
                publish()
            }
        }
    }

    private fun start(startSecs: Double) {
        connect { c ->
            c.setMediaItems(queue.tracks.map(::mediaItem), queue.index, (startSecs * 1000).toLong())
            c.shuffleModeEnabled = false // the queue itself is shuffled, so the player plays it in order
            c.repeatMode = repeatMode(queue.repeat)
            c.prepare()
            c.play()
            publish()
            colourFor(queue.current)
            scheduleSave()
            maybeExtend()
        }
    }

    fun playNext(tracks: List<Item>) {
        if (queue.isEmpty) return play(tracks)
        queue = queue.playNext(tracks)
        connect { c -> c.addMediaItems(c.currentMediaItemIndex + 1, tracks.map(::mediaItem)); publish(); scheduleSave() }
    }

    fun addToQueue(tracks: List<Item>) {
        if (queue.isEmpty) return play(tracks)
        queue = queue.append(tracks)
        connect { c -> c.addMediaItems(tracks.map(::mediaItem)); publish(); scheduleSave() }
    }

    fun togglePlay() = connect { c -> if (c.isPlaying) c.pause() else c.play() }
    fun next() = connect { c -> c.seekToNextMediaItem() }
    fun previous() = connect { c -> if (c.currentPosition > 3000) c.seekTo(0) else c.seekToPreviousMediaItem() }
    fun seekTo(ms: Long) = connect { c -> c.seekTo(ms) }

    fun jumpTo(index: Int) = connect { c -> if (index in queue.tracks.indices) { c.seekTo(index, 0); c.play() } }

    fun remove(index: Int) = connect { c ->
        if (index !in queue.tracks.indices) return@connect
        queue = queue.remove(index)
        c.removeMediaItem(index)
        publish(); scheduleSave()
        maybeExtend()
    }

    fun toggleShuffle() = connect { c ->
        queue = queue.withShuffle(!queue.shuffle)
        // The order changed after the current track: replace what follows.
        val after = queue.index + 1
        if (c.mediaItemCount > after) c.removeMediaItems(after, c.mediaItemCount)
        c.addMediaItems(queue.tracks.drop(after).map(::mediaItem))
        publish(); scheduleSave()
    }

    fun cycleRepeat() = connect { c ->
        queue = queue.cycleRepeat()
        c.repeatMode = repeatMode(queue.repeat)
        publish(); scheduleSave()
    }

    fun stop() = connect { c ->
        queue.current?.let { t -> scope.launch { api.stopPlayback(t.id) } }
        c.stop(); c.clearMediaItems()
        queue = Queue()
        artColours.clear()
        publish(); scheduleSave()
    }

    /** Continues here what another device plays: asks the server, which pauses it and answers with its queue. */
    fun takeOver(deviceId: String, then: (String?) -> Unit = {}) {
        scope.launch {
            runCatching { api.takeOver(deviceId) }
                .onSuccess { q -> playHandoff(q); then(null) }
                .onFailure { then(it.message) }
        }
    }

    /** Sends the queue to another device and stops here. */
    fun playOn(deviceId: String, then: (String?) -> Unit = {}) {
        val c = controller
        val pos = (c?.currentPosition ?: 0) / 1000.0
        scope.launch {
            runCatching { api.playOn(deviceId, queue.record(pos)) }
                .onSuccess { stop(); then(null) }
                .onFailure { then(it.message) }
        }
    }

    // ---- the events stream ----

    private fun onEvent(e: ServerEvent) {
        when (e) {
            is ServerEvent.Takeover -> {
                val t = queue.current ?: return
                if (t.id != e.itemId) return
                val c = controller
                val pos = ((c?.currentPosition ?: 0) / 1000).toInt()
                scope.launch { runCatching { api.progress(t.id, pos, true) } }
                c?.stop(); c?.clearMediaItems()
                queue = Queue()
                artColours.clear()
                publish()
                _notices.tryEmit("Continued on ${e.to.ifEmpty { "another device" }}")
            }
            is ServerEvent.Handoff -> {
                playHandoff(e.queue)
                if (e.queue.from.isNotEmpty()) _notices.tryEmit("From ${e.queue.from}")
            }
            is ServerEvent.Rated -> ratings.set(e.itemId, e.stars)
            is ServerEvent.Themes -> themes.refresh()
            is ServerEvent.Revoked -> { stop(); sessions.clear(); themes.clear() }
            else -> {}
        }
    }

    // ---- keeping the server told ----

    private fun publish() {
        val c = controller
        _state.value = PlayerState(
            queue = queue,
            isPlaying = c?.isPlaying == true,
            positionMs = c?.currentPosition?.coerceAtLeast(0) ?: 0,
            durationMs = c?.duration?.takeIf { it > 0 } ?: 0,
            mixLoading = mixLoading,
        )
    }

    private fun colourFor(track: Item?) {
        scope.launch { artColours.select(track?.poster?.takeIf { it.isNotEmpty() }?.let(api::absolute)) }
    }

    private fun startTicker() {
        ticker?.cancel()
        ticker = scope.launch {
            var beats = 0
            while (isActive) {
                delay(1000)
                publish()
                if (++beats % 15 == 0) heartbeat()
            }
        }
    }

    private fun heartbeat() {
        val c = controller ?: return
        val t = queue.current ?: return
        val pos = (c.currentPosition / 1000).toInt()
        val paused = !c.isPlaying
        scope.launch {
            runCatching { api.progress(t.id, pos, paused) }.onFailure { Log.w(TAG, "heartbeat failed", it) }
        }
    }

    private fun scheduleSave() {
        saveJob?.cancel()
        saveJob = scope.launch {
            delay(1000)
            val pos = (controller?.currentPosition ?: 0) / 1000.0
            runCatching { api.saveQueue(queue.record(pos)) }.onFailure { Log.w(TAG, "saving the queue failed", it) }
        }
    }

    private fun repeatMode(repeat: String) = when (repeat) { "all" -> Player.REPEAT_MODE_ALL; "one" -> Player.REPEAT_MODE_ONE; else -> Player.REPEAT_MODE_OFF }

    private fun mediaItem(t: Item): MediaItem = MediaItem.Builder()
        .setMediaId(t.id)
        .setUri(PlaybackService.uriFor(t.id))
        .setMediaMetadata(
            MediaMetadata.Builder()
                .setTitle(t.title)
                .setArtist(t.artist)
                .setAlbumTitle(t.album)
                .setAlbumArtist(t.albumArtist)
                .setTrackNumber(t.trackNum.takeIf { it > 0 })
                .setArtworkUri(t.poster.takeIf { it.isNotEmpty() }?.let { api.absolute(it).toUri() })
                .build(),
        )
        .build()

    companion object { private const val TAG = "cineva.player" }
}
