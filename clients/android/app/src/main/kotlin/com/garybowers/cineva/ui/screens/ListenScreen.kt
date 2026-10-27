package com.garybowers.cineva.ui.screens

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Pause
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.blur
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.shadow
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import coil.compose.AsyncImage
import com.garybowers.cineva.R
import com.garybowers.cineva.api.CinevaApi
import com.garybowers.cineva.api.Device
import com.garybowers.cineva.api.HandoffQueue
import com.garybowers.cineva.api.Item
import com.garybowers.cineva.api.Library
import com.garybowers.cineva.api.ListingQuery
import com.garybowers.cineva.api.Listening
import com.garybowers.cineva.api.Playlist
import com.garybowers.cineva.api.QueueSource
import com.garybowers.cineva.player.PlayerRepository
import com.garybowers.cineva.sync.EventsClient
import com.garybowers.cineva.sync.ServerEvent
import com.garybowers.cineva.ui.theme.onColour
import com.garybowers.cineva.ui.theme.playingAccent
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.launch
import javax.inject.Inject

/** The music libraries the user may see, shared by the tabs. */
@HiltViewModel
class LibrariesViewModel @Inject constructor(val api: CinevaApi, val player: PlayerRepository) : ViewModel() {
    var libraries by mutableStateOf<List<Library>>(emptyList())
    var current by mutableStateOf<Library?>(null)
    var error by mutableStateOf<String?>(null)
    var loaded by mutableStateOf(false)

    fun load() {
        if (loaded) return
        viewModelScope.launch {
            try {
                libraries = api.libraries().filter { it.mediaType == "music" }
                current = current ?: libraries.firstOrNull()
                loaded = true
            } catch (e: Exception) {
                error = e.describe()
            }
        }
    }

    fun playAlbum(album: Item, shuffle: Boolean = false) {
        viewModelScope.launch {
            runCatching { api.tracksUnder(album.id, shuffle) }.onSuccess {
                player.play(it, 0, QueueSource("album", album.id, album.title))
            }
        }
    }
}

@HiltViewModel
class ListenViewModel @Inject constructor(val api: CinevaApi, val player: PlayerRepository, private val events: EventsClient) : ViewModel() {
    var rows by mutableStateOf<Listening?>(null)
    var recentlyAdded by mutableStateOf<List<Item>>(emptyList())
    var playlists by mutableStateOf<List<Playlist>>(emptyList())
    /** The queue this device saved last, for carrying on. */
    var saved by mutableStateOf<HandoffQueue?>(null)
    /** Another device playing something now, for "Continue here". */
    var elsewhere by mutableStateOf<Device?>(null)
    var error by mutableStateOf<String?>(null)

    init {
        viewModelScope.launch {
            events.events.collect { e -> if (e is ServerEvent.Devices) loadDevices() }
        }
    }

    fun load(libraryId: String) {
        viewModelScope.launch {
            try {
                rows = api.listening(libraryId)
                recentlyAdded = api.albums(libraryId, ListingQuery(sort = "added", size = 20)).items
                error = null
            } catch (e: Exception) {
                error = e.describe()
            }
            runCatching { api.playlists() }.onSuccess { playlists = it }
            runCatching { api.myQueue() }.onSuccess { saved = it.takeIf { q -> q.items.isNotEmpty() } }
            loadDevices()
        }
    }

    fun loadDevices() {
        viewModelScope.launch {
            runCatching { api.devices() }.onSuccess { ds ->
                elsewhere = ds.firstOrNull { !it.current && it.online && it.playing != null && !it.playing.paused }
            }
        }
    }

    fun resume() { saved?.let { player.playHandoff(it) } }

    fun continueHere() {
        val d = elsewhere ?: return
        player.takeOver(d.id) { elsewhere = null }
    }
}

/** Home: carry on with one tap, then the rows, as the Home mockup draws them. */
@Composable
fun ListenScreen(
    onAlbum: (String) -> Unit, onPlaylist: (String) -> Unit, onHold: (Item) -> Unit, onSettings: () -> Unit, onOpenNowPlaying: () -> Unit,
    libs: LibrariesViewModel = hiltViewModel(), vm: ListenViewModel = hiltViewModel(),
) {
    LaunchedEffect(Unit) { libs.load() }
    val lib = libs.current
    LaunchedEffect(lib?.id) { lib?.let { vm.load(it.id) } }
    val playing by vm.player.state.collectAsState()
    val api = libs.api

    ThemeBackdrop(api) {
        LazyColumn(Modifier.fillMaxSize().statusBarsPadding(), contentPadding = PaddingValues(bottom = 24.dp), verticalArrangement = Arrangement.spacedBy(26.dp)) {
            item { TopRow(stringResource(R.string.nav_listen), onSettings) }
            if (libs.error != null) item { ErrorBox(libs.error!!) { libs.loaded = false; libs.load() } }
            if (libs.loaded && libs.libraries.isEmpty()) item { EmptyNote(stringResource(R.string.no_music_libraries)) }
            vm.elsewhere?.let { d -> item { ContinueHereStrip(d) { vm.continueHere() } } }

            // The card: what is playing, else the saved queue, else the last album.
            val saved = vm.saved
            val lastAlbum = vm.rows?.recentlyPlayed?.firstOrNull()
            val now = playing.current
            when {
                now != null -> item {
                    CarryOnCard(api, now, now.album.ifEmpty { playing.queue.source?.title.orEmpty() }, stringResource(R.string.now_playing),
                        stringResource(R.string.track_of, playing.queue.index + 1, playing.queue.tracks.size) + " · " + trackTime((playing.positionMs / 1000).toInt()).ifEmpty { "0:00" } + " " + stringResource(R.string.in_word),
                        playing = playing.isPlaying, onOpen = onOpenNowPlaying) { vm.player.togglePlay() }
                }
                saved != null -> item {
                    val t = saved.items[saved.index.coerceIn(0, saved.items.size - 1)]
                    CarryOnCard(api, t, t.album.ifEmpty { saved.source?.title.orEmpty() }, stringResource(R.string.carry_on),
                        stringResource(R.string.track_of, saved.index + 1, saved.items.size) + " · " + trackTime(saved.positionSecs.toInt()).ifEmpty { "0:00" } + " " + stringResource(R.string.in_word),
                        onOpen = { t.parentId?.let(onAlbum) }) { vm.resume() }
                }
                lastAlbum != null -> item {
                    CarryOnCard(api, lastAlbum, lastAlbum.artist, stringResource(R.string.play_again), stringResource(R.string.tracks_count, lastAlbum.childCount).takeIf { lastAlbum.childCount > 0 } ?: "",
                        onOpen = { onAlbum(lastAlbum.id) }) { libs.playAlbum(lastAlbum) }
                }
            }

            val rows = vm.rows
            if (vm.error != null) item { ErrorBox(vm.error!!) { lib?.let { vm.load(it.id) } } }
            if (rows != null && rows.recentlyPlayed.isNotEmpty()) {
                item {
                    Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
                        SectionHead(stringResource(R.string.recently_played))
                        CoverRail(api, rows.recentlyPlayed, onHold = onHold) { onAlbum(it.id) }
                    }
                }
            }
            if (vm.recentlyAdded.isNotEmpty()) {
                item {
                    Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
                        SectionHead(stringResource(R.string.recently_added))
                        CoverRail(api, vm.recentlyAdded, onHold = onHold) { onAlbum(it.id) }
                    }
                }
            }
            if (rows != null && rows.mostPlayed.isNotEmpty()) {
                item {
                    Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                        SectionHead(stringResource(R.string.most_played))
                        Spacer(Modifier.height(4.dp))
                        for (t in rows.mostPlayed.take(5)) {
                            SongRow(api, t, sub = listOf(t.artist, stringResource(R.string.plays_count, t.playCount)).filter { it.isNotEmpty() }.joinToString(" · "),
                                playing = playing.current?.id == t.id, onHold = onHold,
                                onClick = { vm.player.play(rows.mostPlayed, rows.mostPlayed.indexOf(t), QueueSource("library", lib?.id ?: "", "Most played")) })
                        }
                    }
                }
            }
            item {
                Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
                    SectionHead(stringResource(R.string.playlists))
                    if (vm.playlists.isEmpty()) EmptyNote(stringResource(R.string.no_playlists_yet))
                    else PlaylistRail(api, vm.playlists, onPlaylist)
                }
            }
        }
    }
}

/** The big card at the top of Home: the cover, the track, Play, in the art colour over the blurred cover. */
@Composable
fun CarryOnCard(api: CinevaApi, item: Item, line: String, label: String, detail: String, playing: Boolean = false, onOpen: () -> Unit, onPlay: () -> Unit) {
    val accent = playingAccent()
    val bg = tokens.bg
    Box(
        Modifier.fillMaxWidth().padding(horizontal = 20.dp).height(196.dp).clip(RoundedCornerShape(18.dp)).background(tokens.panel).clickable(onClick = onOpen),
    ) {
        if (item.poster.isNotEmpty()) {
            AsyncImage(model = api.absolute(item.poster), contentDescription = null, contentScale = ContentScale.Crop, modifier = Modifier.fillMaxSize().blur(28.dp))
        }
        Box(Modifier.fillMaxSize().background(Brush.verticalGradient(0f to bg.copy(alpha = 0.45f), 1f to bg.copy(alpha = 0.86f))))
        Row(Modifier.fillMaxSize().padding(16.dp), verticalAlignment = Alignment.Bottom, horizontalArrangement = Arrangement.spacedBy(16.dp)) {
            Box(Modifier.size(104.dp).shadow(14.dp, RoundedCornerShape(10.dp), clip = false, ambientColor = Color.Black.copy(alpha = 0.45f), spotColor = Color.Black.copy(alpha = 0.45f))) {
                Art(api, item, Modifier.size(104.dp), radius = 10.dp)
            }
            Column(Modifier.weight(1f).padding(bottom = 2.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                Text(label.uppercase(), style = t(11, W600, accent, 1.0f))
                Text(item.title, maxLines = 1, overflow = TextOverflow.Ellipsis, style = t(17, W700, tokens.text))
                Text(listOf(item.artist, line).filter { it.isNotEmpty() }.joinToString(" · "), maxLines = 1, overflow = TextOverflow.Ellipsis, style = t(13, W400, tokens.secondary))
                if (detail.isNotEmpty()) Text(detail, maxLines = 1, overflow = TextOverflow.Ellipsis, style = t(12, W400, tokens.muted))
            }
            Box(
                Modifier.size(52.dp).shadow(12.dp, CircleShape, clip = false, ambientColor = accent.copy(alpha = 0.35f), spotColor = accent.copy(alpha = 0.35f))
                    .clip(CircleShape).background(accent).clickable(onClick = onPlay),
                contentAlignment = Alignment.Center,
            ) {
                Icon(if (playing) Icons.Filled.Pause else Icons.Filled.PlayArrow, contentDescription = stringResource(if (playing) R.string.pause else R.string.play), tint = accent.onColour(), modifier = Modifier.size(24.dp))
            }
        }
    }
}

/** "Playing on Living-room TV · Continue here". */
@Composable
private fun ContinueHereStrip(d: Device, onContinue: () -> Unit) {
    val p = d.playing ?: return
    val accent = playingAccent()
    Row(
        Modifier.fillMaxWidth().padding(horizontal = 20.dp).clip(RoundedCornerShape(12.dp)).background(accent.copy(alpha = 0.14f)).clickable(onClick = onContinue).padding(horizontal = 14.dp, vertical = 12.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(2.dp)) {
            Text(stringResource(R.string.playing_on, d.label), style = t(11, W600, tokens.muted, 1.0f))
            Text(p.item.title + (p.item.artist.takeIf { it.isNotEmpty() }?.let { " · $it" } ?: ""), maxLines = 1, overflow = TextOverflow.Ellipsis, style = t(14, W600, tokens.text))
        }
        Text(stringResource(R.string.continue_here), style = t(13, W700, accent))
    }
}

/** Playlists as a row of mosaics. */
@Composable
fun PlaylistRail(api: CinevaApi, playlists: List<Playlist>, onPlaylist: (String) -> Unit) {
    LazyRow(contentPadding = PaddingValues(horizontal = 20.dp), horizontalArrangement = Arrangement.spacedBy(12.dp)) {
        items(playlists, key = { it.id }) { p ->
            Column(Modifier.width(132.dp).clickable { onPlaylist(p.id) }) {
                Mosaic(api, p.covers, Modifier.size(132.dp))
                Spacer(Modifier.height(7.dp))
                Text(p.name, maxLines = 1, overflow = TextOverflow.Ellipsis, style = t(13, W600, tokens.text))
                Text(stringResource(R.string.tracks_count, p.trackCount), style = t(12, W400, tokens.muted))
            }
        }
    }
}
