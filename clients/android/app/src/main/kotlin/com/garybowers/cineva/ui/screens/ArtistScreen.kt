package com.garybowers.cineva.ui.screens

import androidx.compose.foundation.background
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
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.ArrowBackIos
import androidx.compose.material.icons.filled.PlayArrow
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
import androidx.compose.ui.draw.shadow
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.garybowers.cineva.R
import com.garybowers.cineva.api.ArtistPage
import com.garybowers.cineva.api.CinevaApi
import com.garybowers.cineva.api.Item
import com.garybowers.cineva.api.QueueSource
import com.garybowers.cineva.player.PlayerRepository
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.launch
import javax.inject.Inject

@HiltViewModel
class ArtistViewModel @Inject constructor(val api: CinevaApi, val player: PlayerRepository) : ViewModel() {
    var page by mutableStateOf<ArtistPage?>(null)
    var error by mutableStateOf<String?>(null)

    fun load(id: String) {
        viewModelScope.launch {
            try { page = api.artist(id); error = null } catch (e: Exception) { error = e.describe() }
        }
    }

    /** Everything by the artist in one request, shuffled or not, into the queue. */
    fun playAll(shuffle: Boolean) {
        val a = page?.artist ?: return
        viewModelScope.launch {
            runCatching { api.tracksUnder(a.id, shuffle) }.onSuccess { player.play(it, 0, QueueSource("artist", a.id, a.title)) }
        }
    }
}

/** The artist page, in the album page's manner: the picture bleeding behind, round, the name, Play and Shuffle. */
@Composable
fun ArtistScreen(id: String, onAlbum: (String) -> Unit, onBack: () -> Unit, onHold: (Item) -> Unit, vm: ArtistViewModel = hiltViewModel()) {
    LaunchedEffect(id) { vm.load(id) }
    val page = vm.page
    val playing by vm.player.state.collectAsState()
    val picture = page?.artist?.takeIf { it.poster.isNotEmpty() } ?: page?.albums?.firstOrNull { it.poster.isNotEmpty() }
    Box(Modifier.fillMaxSize().background(tokens.bg)) {
        Bleed(vm.api, picture, 360.dp)
        LazyColumn(Modifier.fillMaxSize().statusBarsPadding(), contentPadding = PaddingValues(bottom = 24.dp)) {
            item {
                Row(Modifier.fillMaxWidth().padding(horizontal = 12.dp), verticalAlignment = Alignment.CenterVertically) {
                    Tap(Icons.AutoMirrored.Outlined.ArrowBackIos, stringResource(R.string.back), iconSize = 20.dp, onClick = onBack)
                    Spacer(Modifier.weight(1f))
                    Tap(Icons.Outlined.MoreHoriz, stringResource(R.string.more)) { page?.artist?.let(onHold) }
                }
            }
            vm.error?.let { e -> item { ErrorBox(e) { vm.load(id) } } }
            if (page != null) {
                item {
                    Column(Modifier.fillMaxWidth().padding(horizontal = 20.dp), horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(14.dp)) {
                        Box(Modifier.size(150.dp).shadow(22.dp, CircleShape, clip = false, ambientColor = Color.Black.copy(alpha = 0.5f), spotColor = Color.Black.copy(alpha = 0.5f))) {
                            Art(vm.api, picture ?: page.artist, Modifier.size(150.dp), round = true, letterSize = 48)
                        }
                        Column(horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(4.dp)) {
                            Text(page.artist.title, style = t(22, W800, tokens.text), textAlign = TextAlign.Center)
                            Text(
                                stringResource(R.string.albums_count, page.albums.size) + " · " + stringResource(R.string.tracks_count, page.stats.tracks) + " · " + totalTime(page.stats.secs),
                                style = t(12, W400, tokens.tertiary),
                            )
                        }
                        Row(Modifier.padding(top = 4.dp), horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                            Pill(stringResource(R.string.play), Icons.Filled.PlayArrow, filled = true) { vm.playAll(false) }
                            Pill(stringResource(R.string.shuffle), Icons.Outlined.Shuffle) { vm.playAll(true) }
                            Pill(stringResource(R.string.mix), Icons.Outlined.AutoAwesome) { vm.player.startMix(page.artist) {} }
                        }
                    }
                    Spacer(Modifier.height(10.dp))
                }
                if (page.artist.overview.isNotEmpty()) {
                    item { Text(page.artist.overview, Modifier.padding(horizontal = 20.dp, vertical = 8.dp), maxLines = 4, overflow = TextOverflow.Ellipsis, style = t(14, W400, tokens.secondary)) }
                }
                if (page.topTracks.isNotEmpty()) {
                    item { Spacer(Modifier.height(12.dp)); SectionHead(stringResource(R.string.top_tracks)); Spacer(Modifier.height(6.dp)) }
                    items(page.topTracks, key = { it.id }) { t ->
                        SongRow(vm.api, t, sub = listOf(t.album, stringResource(R.string.plays_count, t.playCount)).filter { it.isNotEmpty() }.joinToString(" · "),
                            playing = playing.current?.id == t.id, onHold = onHold,
                            onClick = { vm.player.play(page.topTracks, page.topTracks.indexOf(t), QueueSource("artist", page.artist.id, page.artist.title)) })
                    }
                }
                if (page.albums.isNotEmpty()) {
                    item {
                        Column(Modifier.padding(top = 22.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                            SectionHead(stringResource(R.string.tab_albums))
                            CoverRail(vm.api, page.albums, sub = { it.year.takeIf { y -> y > 0 }?.toString() ?: "" }, onHold = onHold) { onAlbum(it.id) }
                        }
                    }
                }
                if (page.appearsOn.isNotEmpty()) {
                    item {
                        Column(Modifier.padding(top = 22.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                            SectionHead(stringResource(R.string.appears_on))
                            CoverRail(vm.api, page.appearsOn, onHold = onHold) { onAlbum(it.id) }
                        }
                    }
                }
            }
        }
    }
}
