package com.garybowers.cineva.ui.screens

import android.provider.Settings
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.detectHorizontalDragGestures
import androidx.compose.foundation.gestures.detectVerticalDragGestures
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Favorite
import androidx.compose.material.icons.filled.Pause
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material.icons.filled.SkipNext
import androidx.compose.material.icons.filled.SkipPrevious
import androidx.compose.material.icons.outlined.Close
import androidx.compose.material.icons.outlined.Devices
import androidx.compose.material.icons.outlined.FavoriteBorder
import androidx.compose.material.icons.outlined.KeyboardArrowDown
import androidx.compose.material.icons.outlined.MoreHoriz
import androidx.compose.material.icons.outlined.QueueMusic
import androidx.compose.material.icons.outlined.Repeat
import androidx.compose.material.icons.outlined.RepeatOne
import androidx.compose.material.icons.outlined.Shuffle
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.draw.blur
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.scale
import androidx.compose.ui.draw.shadow
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import coil.compose.AsyncImage
import com.garybowers.cineva.R
import com.garybowers.cineva.api.CinevaApi
import com.garybowers.cineva.api.Device
import com.garybowers.cineva.api.Item
import com.garybowers.cineva.api.Lyrics
import com.garybowers.cineva.data.AppPrefs
import com.garybowers.cineva.data.Ratings
import com.garybowers.cineva.player.PlayerRepository
import com.garybowers.cineva.player.PlayerState
import com.garybowers.cineva.player.MixRules
import com.garybowers.cineva.player.Spectrum
import com.garybowers.cineva.ui.theme.playingAccent
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.launch
import javax.inject.Inject
import kotlin.math.abs

@HiltViewModel
class NowPlayingViewModel @Inject constructor(val api: CinevaApi, val player: PlayerRepository, val ratings: Ratings, val spectrum: Spectrum, val prefs: AppPrefs) : ViewModel() {
    var lyrics by mutableStateOf<Lyrics?>(null)
    private var lyricsFor: String? = null
    var devices by mutableStateOf<List<Device>>(emptyList())
    var notice by mutableStateOf<String?>(null)
    var pickingDevice by mutableStateOf(false)
    /** Saving the mix as a playlist: the name being typed, null when not. */
    var savingName by mutableStateOf<String?>(null)

    /** Saves the queue's mix entries so far as a manual playlist. */
    fun saveMix(tracks: List<Item>) {
        val name = savingName?.trim()?.ifEmpty { null } ?: return
        savingName = null
        viewModelScope.launch {
            runCatching { api.createPlaylist(name).let { p -> api.addToPlaylist(p.id, tracks.map { it.id }) } }
                .onSuccess { notice = "Saved as $name" }
                .onFailure { notice = it.message }
        }
    }

    fun loadLyrics(trackId: String) {
        if (lyricsFor == trackId) return
        lyricsFor = trackId
        lyrics = null
        viewModelScope.launch { lyrics = runCatching { api.lyrics(trackId) }.getOrNull() }
    }

    fun loadDevices() {
        viewModelScope.launch { runCatching { api.devices() }.onSuccess { devices = it.filter { d -> !d.current && d.online } } }
    }

    fun playOn(d: Device) {
        player.playOn(d.id) { err -> notice = err ?: "Playing on ${d.label}" }
    }
}

/**
 * Now playing as its mockup draws it: the cover blurred and darkened
 * behind, the spectrum moving behind and beside the sharp cover, the
 * title and artist, a thin seek bar, the controls with a white play
 * button, and at the bottom Up next and Lyrics as words with Play on and
 * the queue either side. Swipe down to close; swipe the cover sideways
 * for next and previous. Up next and Lyrics open as panes in place of
 * the cover.
 */
@Composable
fun NowPlayingScreen(
    open: Boolean, onClose: () -> Unit, onAlbum: (String) -> Unit, onArtist: (String) -> Unit, onHold: (Item) -> Unit,
    onMore: (Item) -> Unit = onHold,
    vm: NowPlayingViewModel = hiltViewModel(),
) {
    val state by vm.player.state.collectAsState()
    val t = state.current
    val grow by animateFloatAsState(if (open) 1f else 0.6f, tween(320), label = "cover")
    val bg = tokens.bg
    val accent = playingAccent()
    var pane by remember { mutableIntStateOf(0) } // 0 none, 1 up next, 2 lyrics
    var dragY by remember { mutableStateOf(0f) }
    val prefs by vm.prefs.state.collectAsState()
    val context = LocalContext.current
    val reducedMotion = remember { runCatching { Settings.Global.getFloat(context.contentResolver, Settings.Global.ANIMATOR_DURATION_SCALE, 1f) == 0f }.getOrDefault(false) }

    // The spectrum runs only while this screen is open and wanted.
    DisposableEffect(open, prefs.spectrum) {
        if (open && prefs.spectrum) vm.spectrum.start(still = reducedMotion)
        onDispose { vm.spectrum.stop() }
    }

    Box(
        Modifier.fillMaxSize().background(bg)
            .pointerInput(Unit) {
                detectVerticalDragGestures(onDragStart = { dragY = 0f }, onDragEnd = { if (dragY > 120f) onClose(); dragY = 0f }) { _, d -> dragY += d }
            },
    ) {
        if (t?.poster?.isNotEmpty() == true) {
            AsyncImage(
                model = vm.api.absolute(t.poster), contentDescription = null, contentScale = ContentScale.Crop,
                modifier = Modifier.fillMaxSize().scale(1.15f).blur(44.dp).alpha(0.7f),
            )
        }
        Box(Modifier.fillMaxSize().background(Brush.verticalGradient(0f to bg.copy(alpha = 0.35f), 0.55f to bg.copy(alpha = 0.55f), 1f to bg.copy(alpha = 0.9f))))
        if (prefs.spectrum && pane == 0) SpectrumCanvas(vm.spectrum, accent, Modifier.fillMaxSize())
        if (t == null) {
            Column(Modifier.fillMaxSize().statusBarsPadding().padding(16.dp)) {
                Tap(Icons.Outlined.KeyboardArrowDown, stringResource(R.string.close), onClick = onClose)
                EmptyNote(stringResource(R.string.nothing_playing))
            }
            return@Box
        }
        LaunchedEffect(pane, t.id) { if (pane == 2) vm.loadLyrics(t.id) }

        Column(Modifier.fillMaxSize().statusBarsPadding().navigationBarsPadding()) {
            // Header: close, "Playing from", more.
            Row(Modifier.fillMaxWidth().padding(horizontal = 12.dp), verticalAlignment = Alignment.CenterVertically) {
                Tap(Icons.Outlined.KeyboardArrowDown, stringResource(R.string.close), iconSize = 26.dp, onClick = onClose)
                Column(Modifier.weight(1f), horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(2.dp)) {
                    Text(stringResource(R.string.playing_from).uppercase(), style = t(10, W600, tokens.secondary, 1.0f))
                    Text(state.queue.source?.title?.ifEmpty { null } ?: t.album.ifEmpty { stringResource(R.string.up_next) }, maxLines = 1, overflow = TextOverflow.Ellipsis, style = t(13, W600, tokens.text))
                }
                Tap(Icons.Outlined.MoreHoriz, stringResource(R.string.more)) { onMore(t) }
            }

            if (pane == 0) {
                // The cover, 28 in from each side, grown into place.
                Box(
                    Modifier.fillMaxWidth().padding(start = 28.dp, end = 28.dp, top = 34.dp)
                        .pointerInput(Unit) {
                            var dx = 0f
                            detectHorizontalDragGestures(onDragStart = { dx = 0f }, onDragEnd = { if (dx < -90f) vm.player.next() else if (dx > 90f) vm.player.previous() }) { _, d -> dx += d }
                        },
                ) {
                    Box(Modifier.fillMaxWidth().aspectRatio(1f).scale(grow).shadow(30.dp, RoundedCornerShape(16.dp), clip = false, ambientColor = Color.Black.copy(alpha = 0.6f), spotColor = Color.Black.copy(alpha = 0.6f))) {
                        Art(vm.api, t, Modifier.fillMaxSize(), radius = 16.dp, letterSize = 72)
                    }
                }
                Spacer(Modifier.weight(1f))
            } else {
                // A pane: the track as one row at the top, the list under it.
                Row(Modifier.fillMaxWidth().padding(horizontal = 28.dp, vertical = 14.dp), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                    Art(vm.api, t, Modifier.size(48.dp), radius = 8.dp, letterSize = 18)
                    Column(Modifier.weight(1f)) {
                        Text(t.title, maxLines = 1, overflow = TextOverflow.Ellipsis, style = t(15, W700, tokens.text))
                        Text(t.artist, maxLines = 1, overflow = TextOverflow.Ellipsis, style = t(13, W500, accent))
                    }
                }
                Box(Modifier.weight(1f).fillMaxWidth()) {
                    if (pane == 1) UpNextPane(vm, state, onHold) else LyricsPane(vm.lyrics, state.positionMs)
                }
            }

            if (pane == 0) {
                // Title, artist, heart.
                Row(Modifier.fillMaxWidth().padding(start = 28.dp, end = 20.dp, top = 30.dp), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                        Text(t.title, maxLines = 1, overflow = TextOverflow.Ellipsis, style = t(22, W800, tokens.text))
                        Text(t.artist, maxLines = 1, overflow = TextOverflow.Ellipsis, style = t(15, W500, accent),
                            modifier = Modifier.clickable(interactionSource = remember { MutableInteractionSource() }, indication = null) { onHold(t) })
                    }
                    Heart(vm, t)
                }
                // Seek, times, the quality badge between them.
                Column(Modifier.fillMaxWidth().padding(horizontal = 28.dp).padding(top = 16.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                    SeekBar(if (state.durationMs > 0) state.positionMs.toFloat() / state.durationMs else 0f, onSeek = { f -> vm.player.seekTo((f * state.durationMs).toLong()) })
                    Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                        Text(trackTime((state.positionMs / 1000).toInt()).ifEmpty { "0:00" }, style = t(12, W400, tokens.secondary))
                        Box(Modifier.weight(1f), contentAlignment = Alignment.Center) { QualityBadge(vm, t) }
                        Text(trackTime((state.durationMs / 1000).toInt()), style = t(12, W400, tokens.secondary))
                    }
                }
                // Controls.
                Row(Modifier.fillMaxWidth().padding(start = 28.dp, end = 28.dp, top = 14.dp), horizontalArrangement = Arrangement.SpaceBetween, verticalAlignment = Alignment.CenterVertically) {
                    Tap(Icons.Outlined.Shuffle, stringResource(R.string.shuffle), size = 48.dp, iconSize = 22.dp, tint = if (state.queue.shuffle) accent else tokens.secondary) { vm.player.toggleShuffle() }
                    Tap(Icons.Filled.SkipPrevious, stringResource(R.string.previous), size = 56.dp, iconSize = 34.dp) { vm.player.previous() }
                    Box(
                        Modifier.size(76.dp).shadow(14.dp, CircleShape, clip = false, ambientColor = Color.Black.copy(alpha = 0.45f), spotColor = Color.Black.copy(alpha = 0.45f))
                            .clip(CircleShape).background(Color(0xFFE8EBF0)).clickable { vm.player.togglePlay() },
                        contentAlignment = Alignment.Center,
                    ) {
                        Icon(if (state.isPlaying) Icons.Filled.Pause else Icons.Filled.PlayArrow, contentDescription = stringResource(if (state.isPlaying) R.string.pause else R.string.play), Modifier.size(34.dp), tint = Color(0xFF141413))
                    }
                    Tap(Icons.Filled.SkipNext, stringResource(R.string.next), size = 56.dp, iconSize = 34.dp) { vm.player.next() }
                    Tap(if (state.queue.repeat == "one") Icons.Outlined.RepeatOne else Icons.Outlined.Repeat, stringResource(R.string.repeat), size = 48.dp, iconSize = 22.dp,
                        tint = if (state.queue.repeat != "off") accent else tokens.secondary) { vm.player.cycleRepeat() }
                }
                Spacer(Modifier.weight(1f))
            }

            // The bottom row: Play on, the words, the queue.
            Row(Modifier.fillMaxWidth().padding(start = 28.dp, end = 28.dp, bottom = 12.dp), verticalAlignment = Alignment.CenterVertically) {
                Tap(Icons.Outlined.Devices, stringResource(R.string.play_on), iconSize = 22.dp, tint = tokens.secondary) { vm.pickingDevice = true; vm.loadDevices() }
                Row(Modifier.weight(1f), horizontalArrangement = Arrangement.spacedBy(22.dp, Alignment.CenterHorizontally)) {
                    BottomWord(stringResource(R.string.up_next), pane == 1) { pane = if (pane == 1) 0 else 1 }
                    BottomWord(stringResource(R.string.lyrics), pane == 2) { pane = if (pane == 2) 0 else 2 }
                }
                Tap(Icons.Outlined.QueueMusic, stringResource(R.string.queue), iconSize = 22.dp, tint = tokens.secondary) { pane = if (pane == 1) 0 else 1 }
            }
        }
        vm.notice?.let { n ->
            LaunchedEffect(n) { kotlinx.coroutines.delay(3000); vm.notice = null }
            Text(n, Modifier.align(Alignment.TopCenter).statusBarsPadding().padding(top = 60.dp).clip(RoundedCornerShape(10.dp)).background(tokens.strip).padding(horizontal = 14.dp, vertical = 9.dp), style = t(13, W500, tokens.text))
        }
        PlayOnSheet(vm)
    }
}

@Composable
private fun BottomWord(text: String, on: Boolean, onClick: () -> Unit) {
    val accent = playingAccent()
    Column(Modifier.clickable(interactionSource = remember { MutableInteractionSource() }, indication = null, onClick = onClick), horizontalAlignment = Alignment.CenterHorizontally) {
        Text(text, style = t(13, W600, if (on) tokens.text else tokens.tertiary))
        Box(Modifier.padding(top = 3.dp).width(if (on) 30.dp else 0.dp).height(2.dp).background(accent))
    }
}

/** The heart: five stars and clear, until favourites exist. */
@Composable
private fun Heart(vm: NowPlayingViewModel, t: Item) {
    val overrides by vm.ratings.overrides.collectAsState()
    val stars = overrides[t.id] ?: t.userRating
    val loved = stars >= 5
    Tap(if (loved) Icons.Filled.Favorite else Icons.Outlined.FavoriteBorder, stringResource(R.string.love), tint = if (loved) playingAccent() else tokens.text) {
        vm.ratings.rate(t.id, if (loved) 0 else 5, stars)
    }
}

/** The spectrum: bars rising from the bottom in the art colour, quietly, with Winamp's falling caps. */
@Composable
fun SpectrumCanvas(spectrum: Spectrum, colour: Color, modifier: Modifier = Modifier) {
    val frame by spectrum.frame.collectAsState()
    Canvas(modifier) {
        val n = frame.bands.size
        if (n == 0) return@Canvas
        val gap = 3.dp.toPx()
        val w = (size.width - gap * (n + 1)) / n
        // Up to just under half the screen, so the bars live behind and
        // beside the cover and under the controls, never over the title.
        val maxH = size.height * 0.46f
        for (i in 0 until n) {
            val x = gap + i * (w + gap)
            val h = frame.bands[i] * maxH
            if (h > 0f) drawRect(colour.copy(alpha = 0.22f), Offset(x, size.height - h), Size(w, h))
            val p = frame.peaks[i] * maxH
            if (p > 2f) drawRect(colour.copy(alpha = 0.38f), Offset(x, size.height - p - 2.dp.toPx()), Size(w, 2.dp.toPx()))
        }
    }
}

@Composable
private fun UpNextPane(vm: NowPlayingViewModel, state: PlayerState, onHold: (Item) -> Unit) {
    val list = rememberLazyListState()
    val q = state.queue
    val mix = q.mix
    val from = q.mixFrom
    LaunchedEffect(q.index) { if (q.index > 0) list.scrollToItem((q.index - 1).coerceAtLeast(0)) }
    LazyColumn(Modifier.fillMaxSize(), state = list, contentPadding = PaddingValues(bottom = 8.dp)) {
        // The queue's own entries, if any come before the mix.
        if (mix == null || from > 0) {
            item {
                Row(Modifier.fillMaxWidth().padding(start = 28.dp, end = 20.dp, bottom = 6.dp), verticalAlignment = Alignment.CenterVertically) {
                    Text(stringResource(R.string.up_next) + (q.source?.title?.takeIf { it.isNotEmpty() && q.source.kind != "mix" }?.let { " · $it" } ?: ""), Modifier.weight(1f), style = t(17, W700, tokens.text), maxLines = 1, overflow = TextOverflow.Ellipsis)
                    Word(stringResource(R.string.clear), tokens.muted) { vm.player.stop() }
                }
            }
        }
        itemsIndexed(q.tracks.take(from), key = { i, it -> "$i-${it.id}" }) { i, t -> QueueRow(vm, t, i, state, onHold) }
        // The mix: its heading, the basis in words, and the batches so far (server/docs/mix.md).
        if (mix != null) {
            item {
                Column(Modifier.fillMaxWidth().padding(start = 28.dp, end = 20.dp, top = if (from > 0) 18.dp else 0.dp, bottom = 6.dp)) {
                    Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                        Text(stringResource(R.string.mix_heading, mix.title.ifEmpty { stringResource(R.string.up_next) }), Modifier.weight(1f), style = t(17, W700, tokens.text), maxLines = 1, overflow = TextOverflow.Ellipsis)
                        Word(stringResource(R.string.stop_mix), tokens.muted) { vm.player.stopMix() }
                    }
                    Row(Modifier.fillMaxWidth().padding(top = 2.dp), verticalAlignment = Alignment.CenterVertically) {
                        Text(MixRules.basisWords(mix.basis), Modifier.weight(1f), style = t(12, W400, tokens.muted), maxLines = 1, overflow = TextOverflow.Ellipsis)
                        if (vm.savingName == null) Word(stringResource(R.string.save_as_playlist)) { vm.savingName = "Mix from ${mix.title}" }
                    }
                    vm.savingName?.let { name ->
                        Row(Modifier.fillMaxWidth().padding(top = 8.dp), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                            Field(name, { vm.savingName = it }, stringResource(R.string.mix_name, mix.title), Modifier.weight(1f), onDone = { vm.saveMix(q.tracks.drop(from)) })
                            Pill(stringResource(R.string.save), filled = true, enabled = name.isNotBlank()) { vm.saveMix(q.tracks.drop(from)) }
                        }
                    }
                }
            }
            itemsIndexed(q.tracks.drop(from), key = { i, it -> "m${i + from}-${it.id}" }) { i, t -> QueueRow(vm, t, i + from, state, onHold) }
        }
        if (state.mixLoading) {
            item { Text(stringResource(R.string.mix_finding), Modifier.padding(start = 28.dp, top = 10.dp, bottom = 10.dp), style = t(13, W500, tokens.muted)) }
        }
    }
}

@Composable
private fun QueueRow(vm: NowPlayingViewModel, q: Item, i: Int, state: PlayerState, onHold: (Item) -> Unit) {
    var dx by remember { mutableStateOf(0f) }
    val gone = i < state.queue.index
    val accent = playingAccent()
    Box(
        Modifier.fillMaxWidth().alpha(if (gone) 0.45f else 1f)
            .pointerInput(i) {
                detectHorizontalDragGestures(onDragStart = { dx = 0f }, onDragEnd = { if (abs(dx) > 140f && i != state.queue.index) vm.player.remove(i); dx = 0f }) { _, d -> dx += d }
            },
    ) {
        Row(
            Modifier.fillMaxWidth().clickable { vm.player.jumpTo(i) }.padding(start = 28.dp, end = 12.dp, top = 9.dp, bottom = 9.dp),
            verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(14.dp),
        ) {
            Text((i + 1).toString(), Modifier.width(22.dp), style = t(13, W500, if (i == state.queue.index) accent else tokens.muted))
            Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(2.dp)) {
                Text(q.title, maxLines = 1, overflow = TextOverflow.Ellipsis, style = t(15, W600, if (i == state.queue.index) accent else tokens.text))
                Text(listOf(q.artist, q.album).filter { it.isNotEmpty() }.joinToString(" · "), maxLines = 1, overflow = TextOverflow.Ellipsis, style = t(12, W400, tokens.muted))
            }
            Text(trackTime(q.runtimeSecs), style = t(13, W400, tokens.muted))
            Tap(Icons.Outlined.Close, stringResource(R.string.remove), size = 40.dp, iconSize = 18.dp, tint = tokens.muted) { vm.player.remove(i) }
        }
    }
}

/** "FLAC 16/44" or "Opus 128k": what the server decided for this track. */
@Composable
private fun QualityBadge(vm: NowPlayingViewModel, t: Item) {
    val src = vm.player.decision(t.id)?.source ?: return
    val badge = buildString {
        append(src.audioCodec.ifEmpty { src.container }.uppercase())
        if (src.audio == "transcode" && src.bitrate > 0) append(" ${src.bitrate}k")
        else if (src.bitDepth > 0 && src.sampleRate > 0) append(" ${src.bitDepth}/${src.sampleRate / 1000}")
    }
    Text(badge, style = t(11, W600, tokens.tertiary, 0.6f))
}

/** Play on...: the user's other devices that have Cineva open, as a sheet. */
@Composable
private fun androidx.compose.foundation.layout.BoxScope.PlayOnSheet(vm: NowPlayingViewModel) {
    Sheet(vm.pickingDevice, onDismiss = { vm.pickingDevice = false }) {
        Column(Modifier.padding(horizontal = 20.dp, vertical = 8.dp)) {
            Text(stringResource(R.string.play_on), style = t(17, W700, tokens.text))
            Spacer(Modifier.height(8.dp))
            if (vm.devices.isEmpty()) Text(stringResource(R.string.no_other_devices), Modifier.padding(vertical = 12.dp), style = t(14, W400, tokens.muted))
            for (d in vm.devices) {
                Row(Modifier.fillMaxWidth().clickable { vm.pickingDevice = false; vm.playOn(d) }.height(50.dp), verticalAlignment = Alignment.CenterVertically) {
                    Icon(Icons.Outlined.Devices, null, Modifier.size(20.dp), tint = tokens.muted)
                    Spacer(Modifier.width(14.dp))
                    Text(d.label, style = t(15, W500, tokens.text))
                }
            }
        }
    }
}

/** A timed line of lyrics. */
data class LyricLine(val atMs: Long, val text: String)

private val lrcLine = Regex("""^\[(\d+):(\d{2})(?:[.:](\d{1,3}))?]\s?(.*)$""")

/** Parses LRC: "[mm:ss.xx] words". Lines without a time keep the last one. */
fun parseLyrics(l: Lyrics): List<LyricLine> {
    if (!l.synced) return l.text.lines().map { LyricLine(-1, it) }
    val out = ArrayList<LyricLine>()
    for (raw in l.text.lines()) {
        val m = lrcLine.find(raw.trim()) ?: continue
        val (min, sec, frac, words) = m.destructured
        val ms = min.toLong() * 60_000 + sec.toLong() * 1000 + (frac.padEnd(3, '0').take(3).toLongOrNull() ?: 0)
        out += LyricLine(ms, words)
    }
    return out
}

@Composable
private fun LyricsPane(lyrics: Lyrics?, positionMs: Long) {
    val accent = playingAccent()
    if (lyrics == null) { EmptyNote(stringResource(R.string.no_lyrics)); return }
    val lines = remember(lyrics) { parseLyrics(lyrics) }
    val current = if (lyrics.synced) lines.indexOfLast { it.atMs in 0..positionMs } else -1
    val scroll = rememberScrollState()
    LaunchedEffect(current) { if (current > 2) scroll.animateScrollTo(((current - 2) * 44.dp.value * 3).toInt()) }
    Column(Modifier.fillMaxSize().verticalScroll(scroll).padding(horizontal = 28.dp, vertical = 8.dp)) {
        lines.forEachIndexed { i, line ->
            val on = i == current
            Text(
                line.text.ifEmpty { " " },
                style = if (lyrics.synced) t(20, if (on) W700 else W500, if (on) accent else tokens.muted) else t(16, W400, tokens.text),
                modifier = Modifier.padding(vertical = if (lyrics.synced) 6.dp else 2.dp).alpha(if (lyrics.synced && !on && i < current) 0.5f else 1f),
                textAlign = TextAlign.Start,
            )
        }
        Spacer(Modifier.height(80.dp))
    }
}
