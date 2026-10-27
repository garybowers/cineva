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
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.ArrowBackIos
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material.icons.outlined.Download
import androidx.compose.material.icons.outlined.MoreHoriz
import androidx.compose.material.icons.outlined.AutoAwesome
import androidx.compose.material.icons.outlined.Shuffle
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.draw.blur
import androidx.compose.ui.draw.shadow
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import coil.compose.AsyncImage
import com.garybowers.cineva.R
import com.garybowers.cineva.api.CinevaApi
import com.garybowers.cineva.api.Item
import com.garybowers.cineva.api.QueueSource
import com.garybowers.cineva.player.PlayerRepository
import com.garybowers.cineva.ui.theme.playingAccent
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.launch
import javax.inject.Inject

@HiltViewModel
class AlbumViewModel @Inject constructor(val api: CinevaApi, val player: PlayerRepository) : ViewModel() {
    var album by mutableStateOf<Item?>(null)
    var tracks by mutableStateOf<List<Item>>(emptyList())
    var more by mutableStateOf<List<Item>>(emptyList())
    var error by mutableStateOf<String?>(null)
    var note by mutableStateOf<String?>(null)

    fun load(id: String) {
        viewModelScope.launch {
            try {
                val d = api.item(id)
                album = d.item
                tracks = api.tracksUnder(id)
                error = null
                d.item.parentId?.let { a -> runCatching { api.artist(a) }.onSuccess { p -> more = p.albums.filter { it.id != id } } }
            } catch (e: Exception) {
                error = e.describe()
            }
        }
    }

    fun play(from: Int = 0, shuffle: Boolean = false) {
        val a = album ?: return
        player.play(tracks, from, QueueSource("album", a.id, a.title), shuffle)
    }
}

/** The information line: year, tracks, length, what the files are. */
fun albumInfo(album: Item, tracks: List<Item>): String {
    val parts = mutableListOf<String>()
    if (album.year > 0) parts += album.year.toString()
    parts += "${tracks.size} tracks"
    val secs = tracks.sumOf { it.runtimeSecs }
    if (secs > 0) parts += totalTime(secs)
    val first = tracks.firstOrNull()
    if (first != null && first.audioCodec.isNotEmpty()) {
        var f = first.audioCodec.uppercase()
        if (first.bitDepth > 0) f += " ${first.bitDepth}-bit"
        if (first.sampleRate > 0) f += " ${"%.1f".format(first.sampleRate / 1000.0).removeSuffix(".0")} kHz"
        else if (first.bitrate > 0) f += " ${first.bitrate} kbit/s"
        parts += f
    }
    return parts.joinToString(" · ")
}

/** The cover bleeding behind a page's header, blurred and darkened, fading into the ground. */
@Composable
fun Bleed(api: CinevaApi, item: Item?, height: Dp) {
    val bg = tokens.bg
    Box(Modifier.fillMaxWidth().height(height)) {
        if (item?.poster?.isNotEmpty() == true) {
            AsyncImage(
                model = api.absolute(item.poster), contentDescription = null, contentScale = ContentScale.Crop,
                modifier = Modifier.fillMaxSize().blur(30.dp).alpha(0.55f),
            )
        }
        Box(Modifier.fillMaxSize().background(Brush.verticalGradient(0f to bg.copy(alpha = 0.2f), 0.7f to bg.copy(alpha = 0.75f), 1f to bg)))
    }
}

/** The album page as its mockup draws it. */
@Composable
fun AlbumScreen(id: String, onArtist: (String) -> Unit, onAlbum: (String) -> Unit, onBack: () -> Unit, onHold: (Item) -> Unit, vm: AlbumViewModel = hiltViewModel()) {
    LaunchedEffect(id) { vm.load(id) }
    val album = vm.album
    val playing by vm.player.state.collectAsState()
    val accent = playingAccent()
    val onDeviceNote = stringResource(R.string.download_sync_note)
    Box(Modifier.fillMaxSize().background(tokens.bg)) {
        Bleed(vm.api, album, 420.dp)
        LazyColumn(Modifier.fillMaxSize().statusBarsPadding(), contentPadding = PaddingValues(bottom = 24.dp)) {
            item {
                Row(Modifier.fillMaxWidth().padding(horizontal = 12.dp), verticalAlignment = Alignment.CenterVertically) {
                    Tap(Icons.AutoMirrored.Outlined.ArrowBackIos, stringResource(R.string.back), iconSize = 20.dp, onClick = onBack)
                    Spacer(Modifier.weight(1f))
                    Tap(Icons.Outlined.MoreHoriz, stringResource(R.string.more)) { album?.let(onHold) }
                }
            }
            vm.error?.let { e -> item { ErrorBox(e) { vm.load(id) } } }
            if (album != null) {
                item {
                    Column(Modifier.fillMaxWidth().padding(start = 20.dp, end = 20.dp, top = 6.dp), horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(14.dp)) {
                        Box(Modifier.size(214.dp).shadow(22.dp, RoundedCornerShape(12.dp), clip = false, ambientColor = Color.Black.copy(alpha = 0.55f), spotColor = Color.Black.copy(alpha = 0.55f))) {
                            Art(vm.api, album, Modifier.size(214.dp), radius = 12.dp, letterSize = 48)
                        }
                        Column(horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(4.dp)) {
                            Text(album.title, style = t(22, W800, tokens.text), textAlign = TextAlign.Center, maxLines = 2, overflow = TextOverflow.Ellipsis)
                            Text(album.artist, style = t(15, W600, accent), modifier = Modifier.clickable(enabled = !album.parentId.isNullOrEmpty()) { album.parentId?.let(onArtist) })
                            Text(albumInfo(album, vm.tracks), style = t(12, W400, tokens.tertiary), textAlign = TextAlign.Center)
                        }
                        Row(Modifier.padding(top = 4.dp), horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                            Pill(stringResource(R.string.play), Icons.Filled.PlayArrow, filled = true, enabled = vm.tracks.isNotEmpty()) { vm.play() }
                            Pill(stringResource(R.string.shuffle), Icons.Outlined.Shuffle, enabled = vm.tracks.isNotEmpty()) { vm.play(0, true) }
                            Pill(stringResource(R.string.mix), Icons.Outlined.AutoAwesome, enabled = vm.tracks.isNotEmpty()) { vm.player.startMix(album) { err -> vm.note = err } }
                            RoundQuiet(Icons.Outlined.Download, stringResource(R.string.download)) { vm.note = onDeviceNote }
                        }
                        vm.note?.let { Text(it, style = t(12, W400, tokens.muted), textAlign = TextAlign.Center) }
                    }
                    Spacer(Modifier.height(18.dp))
                }
                val discs = vm.tracks.map { it.discNum }.distinct().filter { it > 0 }
                var lastDisc = 0
                vm.tracks.forEachIndexed { i, t ->
                    if (discs.size > 1 && t.discNum != lastDisc) {
                        lastDisc = t.discNum
                        item(key = "disc-$lastDisc") { Text(stringResource(R.string.disc_n, lastDisc), Modifier.padding(start = 20.dp, top = 10.dp, bottom = 4.dp), style = t(12, W600, tokens.muted, 1.0f)) }
                    }
                    item(key = t.id) {
                        AlbumTrackRow(t, number = t.trackNum.takeIf { it > 0 } ?: (i + 1), playing = playing.current?.id == t.id, albumArtist = album.artist, onHold = onHold) { vm.play(i) }
                    }
                }
                if (vm.more.isNotEmpty()) {
                    item {
                        Column(Modifier.padding(top = 26.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                            SectionHead(stringResource(R.string.more_by, album.artist))
                            CoverRail(vm.api, vm.more, sub = { it.year.takeIf { y -> y > 0 }?.toString() ?: "" }, onHold = onHold) { onAlbum(it.id) }
                        }
                    }
                }
            }
        }
    }
}
