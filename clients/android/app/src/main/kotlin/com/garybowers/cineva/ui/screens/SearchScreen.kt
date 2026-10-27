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
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Close
import androidx.compose.material.icons.outlined.Search
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
import androidx.compose.ui.draw.clip
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.garybowers.cineva.R
import com.garybowers.cineva.api.CinevaApi
import com.garybowers.cineva.api.Item
import com.garybowers.cineva.api.LibrarySearch
import com.garybowers.cineva.api.QueueSource
import com.garybowers.cineva.player.PlayerRepository
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import javax.inject.Inject

@HiltViewModel
class SearchViewModel @Inject constructor(val api: CinevaApi, val player: PlayerRepository) : ViewModel() {
    var query by mutableStateOf("")
    var results by mutableStateOf<LibrarySearch?>(null)
    var error by mutableStateOf<String?>(null)
    private var job: Job? = null

    /** Searches as typed, a moment after the last keystroke. */
    fun search(libraryId: String, q: String) {
        query = q
        job?.cancel()
        if (q.trim().length < 2) { results = null; return }
        job = viewModelScope.launch {
            delay(250)
            try { results = api.search(libraryId, q.trim()); error = null } catch (e: Exception) { error = e.describe() }
        }
    }
}

/** Search as its mockup draws it: one field with Cancel, results grouped as typed. */
@Composable
fun SearchScreen(onArtist: (String) -> Unit, onAlbum: (String) -> Unit, onHold: (Item) -> Unit, onCancel: () -> Unit, libs: LibrariesViewModel = hiltViewModel(), vm: SearchViewModel = hiltViewModel()) {
    LaunchedEffect(Unit) { libs.load() }
    val lib = libs.current
    val playing by vm.player.state.collectAsState()
    val api = vm.api
    Column(Modifier.fillMaxSize().statusBarsPadding()) {
        Row(Modifier.fillMaxWidth().padding(start = 20.dp, end = 14.dp, top = 12.dp), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(6.dp)) {
            Field(
                value = vm.query, onChange = { q -> lib?.let { vm.search(it.id, q) } }, placeholder = stringResource(R.string.search_hint), modifier = Modifier.weight(1f),
                leading = { Icon(Icons.Outlined.Search, null, Modifier.size(20.dp), tint = tokens.muted) },
                trailing = {
                    if (vm.query.isNotEmpty()) {
                        Box(Modifier.size(28.dp).clip(CircleShape).background(tokens.border).clickable { lib?.let { vm.search(it.id, "") } }, contentAlignment = Alignment.Center) {
                            Icon(Icons.Outlined.Close, stringResource(R.string.clear), Modifier.size(14.dp), tint = tokens.text)
                        }
                    }
                },
            )
            Word(stringResource(R.string.cancel)) { lib?.let { vm.search(it.id, "") }; onCancel() }
        }
        val r = vm.results
        vm.error?.let { ErrorBox(it) }
        if (r == null) {
            EmptyNote(stringResource(R.string.search_note))
        } else {
            LazyColumn(contentPadding = PaddingValues(top = 22.dp, bottom = 24.dp), verticalArrangement = Arrangement.spacedBy(22.dp)) {
                if (r.artists.isNotEmpty()) {
                    item {
                        Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
                            SectionLabel(stringResource(R.string.tab_artists))
                            LazyRow(contentPadding = PaddingValues(horizontal = 20.dp), horizontalArrangement = Arrangement.spacedBy(16.dp)) {
                                items(r.artists, key = { it.id }) { a ->
                                    Column(Modifier.width(84.dp).clickable { onArtist(a.id) }, horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(8.dp)) {
                                        Art(api, a, Modifier.size(84.dp), round = true, letterSize = 26)
                                        Text(a.title, maxLines = 1, overflow = androidx.compose.ui.text.style.TextOverflow.Ellipsis, style = t(13, W600, tokens.text))
                                    }
                                }
                            }
                        }
                    }
                }
                if (r.albums.isNotEmpty()) {
                    item {
                        Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
                            SectionLabel(stringResource(R.string.tab_albums))
                            CoverRail(api, r.albums, width = 112.dp, sub = { it.year.takeIf { y -> y > 0 }?.toString() ?: it.artist }, onHold = onHold) { onAlbum(it.id) }
                        }
                    }
                }
                if (r.tracks.isNotEmpty()) {
                    item {
                        Column(verticalArrangement = Arrangement.spacedBy(2.dp)) {
                            SectionLabel(stringResource(R.string.tab_songs))
                            Spacer(Modifier.height(6.dp))
                            for (t in r.tracks) {
                                SongRow(api, t, playing = playing.current?.id == t.id, onHold = onHold, onClick = { vm.player.play(r.tracks, r.tracks.indexOf(t), QueueSource("search", "", vm.query)) })
                            }
                        }
                    }
                }
                if (r.artists.isEmpty() && r.albums.isEmpty() && r.tracks.isEmpty()) {
                    item { EmptyNote(stringResource(R.string.no_results)) }
                }
            }
        }
    }
}
