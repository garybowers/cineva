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
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.ArrowBackIos
import androidx.compose.material.icons.filled.PlayArrow
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
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.garybowers.cineva.R
import com.garybowers.cineva.api.CinevaApi
import com.garybowers.cineva.api.Item
import com.garybowers.cineva.api.PlaylistDetail
import com.garybowers.cineva.api.QueueSource
import com.garybowers.cineva.player.PlayerRepository
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.launch
import javax.inject.Inject

@HiltViewModel
class PlaylistViewModel @Inject constructor(val api: CinevaApi, val player: PlayerRepository) : ViewModel() {
    var detail by mutableStateOf<PlaylistDetail?>(null)
    var error by mutableStateOf<String?>(null)

    fun load(id: String) {
        viewModelScope.launch { try { detail = api.playlist(id); error = null } catch (e: Exception) { error = e.describe() } }
    }

    fun play(from: Int = 0, shuffle: Boolean = false) {
        val d = detail ?: return
        player.play(d.tracks, from, QueueSource("playlist", d.playlist.id, d.playlist.name), shuffle)
    }
}

@Composable
fun PlaylistScreen(id: String, onBack: () -> Unit, onHold: (Item) -> Unit, vm: PlaylistViewModel = hiltViewModel()) {
    LaunchedEffect(id) { vm.load(id) }
    val d = vm.detail
    val playing by vm.player.state.collectAsState()
    Box(Modifier.fillMaxSize().background(tokens.bg)) {
        LazyColumn(Modifier.fillMaxSize().statusBarsPadding(), contentPadding = PaddingValues(bottom = 24.dp)) {
            item {
                Row(Modifier.fillMaxWidth().padding(horizontal = 12.dp), verticalAlignment = Alignment.CenterVertically) {
                    Tap(Icons.AutoMirrored.Outlined.ArrowBackIos, stringResource(R.string.back), iconSize = 20.dp, onClick = onBack)
                }
            }
            vm.error?.let { e -> item { ErrorBox(e) { vm.load(id) } } }
            if (d == null) return@LazyColumn
            item {
                Row(Modifier.padding(horizontal = 20.dp, vertical = 6.dp), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(16.dp)) {
                    Mosaic(vm.api, d.playlist.covers, Modifier.size(132.dp), radius = 10.dp)
                    Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                        Text(d.playlist.name, style = t(22, W800, tokens.text), maxLines = 2, overflow = TextOverflow.Ellipsis)
                        Text(
                            stringResource(R.string.tracks_count, d.tracks.size) + " · " + totalTime(d.tracks.sumOf { it.runtimeSecs }) + if (d.playlist.isSmart) " · " + stringResource(R.string.smart_playlist) else "",
                            style = t(12, W400, tokens.tertiary),
                        )
                    }
                }
            }
            item {
                Row(Modifier.padding(start = 20.dp, top = 10.dp, bottom = 14.dp), horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                    Pill(stringResource(R.string.play), Icons.Filled.PlayArrow, filled = true, enabled = d.tracks.isNotEmpty()) { vm.play() }
                    Pill(stringResource(R.string.shuffle), Icons.Outlined.Shuffle, enabled = d.tracks.isNotEmpty()) { vm.play(0, true) }
                }
            }
            itemsIndexed(d.tracks, key = { i, t -> "$i-${t.id}" }) { i, t ->
                SongRow(vm.api, t, playing = playing.current?.id == t.id, onHold = onHold, onClick = { vm.play(i) })
            }
            item { Spacer(Modifier.height(8.dp)) }
        }
    }
}
