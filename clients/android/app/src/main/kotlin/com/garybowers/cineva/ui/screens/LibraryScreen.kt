package com.garybowers.cineva.ui.screens

import androidx.compose.foundation.clickable
import kotlinx.coroutines.launch
import com.garybowers.cineva.api.Playlist
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.itemsIndexed
import androidx.compose.foundation.lazy.grid.rememberLazyGridState
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import com.garybowers.cineva.R
import com.garybowers.cineva.api.CinevaApi
import com.garybowers.cineva.api.GenreCount
import com.garybowers.cineva.api.Item
import com.garybowers.cineva.api.ListingQuery
import com.garybowers.cineva.api.QueueSource

/** The music library as the Library mockup draws it: word tabs, a sort row, covers in thirds. */
@Composable
fun LibraryScreen(onArtist: (String) -> Unit, onAlbum: (String) -> Unit, onPlaylist: (String) -> Unit, onHold: (Item) -> Unit, onSettings: () -> Unit, libs: LibrariesViewModel = hiltViewModel()) {
    LaunchedEffect(Unit) { libs.load() }
    var tab by rememberSaveable { mutableIntStateOf(1) }
    val labels = listOf(R.string.tab_artists, R.string.tab_albums, R.string.tab_songs, R.string.tab_genres, R.string.tab_playlists).map { stringResource(it) }
    ThemeBackdrop(libs.api) {
        Column(Modifier.fillMaxSize().statusBarsPadding()) {
            val lib = libs.current
            TopRow(lib?.name ?: stringResource(R.string.nav_library), onSettings)
            if (lib == null) {
                if (libs.error != null) ErrorBox(libs.error!!) { libs.loaded = false; libs.load() }
                else if (libs.loaded) EmptyNote(stringResource(R.string.no_music_libraries))
                return@Column
            }
            if (libs.libraries.size > 1) LibraryWords(libs)
            WordTabs(labels, tab) { tab = it }
            when (tab) {
                0 -> ArtistsTab(libs.api, lib.id, onArtist, onHold)
                1 -> AlbumsTab(libs.api, lib.id, onAlbum, onHold)
                2 -> SongsTab(libs, lib.id, onHold)
                3 -> GenresTab(libs.api, lib.id, onAlbum, onHold)
                else -> PlaylistsTab(libs.api, onPlaylist)
            }
        }
    }
}

/** Several music libraries: their names as words, the current one lit. */
@Composable
private fun LibraryWords(libs: LibrariesViewModel) {
    Row(Modifier.fillMaxWidth().padding(start = 20.dp, end = 20.dp, top = 12.dp), horizontalArrangement = Arrangement.spacedBy(14.dp)) {
        for (l in libs.libraries) {
            val on = l.id == libs.current?.id
            Text(l.name, Modifier.clickable { libs.current = l }, style = t(13, if (on) W600 else W500, if (on) tokens.text else tokens.muted))
        }
    }
}

/** Covers in thirds, edge to edge inside the 20 dp gutter, 16 down and 12 across. */
@Composable
private fun CoverGrid(api: CinevaApi, paged: Paged, round: Boolean = false, onHold: (Item) -> Unit, onClick: (Item) -> Unit) {
    val grid = rememberLazyGridState()
    LoadMore(grid.firstVisibleItemIndex + 12 >= paged.items.size, paged)
    LazyVerticalGrid(
        GridCells.Fixed(3), Modifier.fillMaxSize(), state = grid,
        contentPadding = PaddingValues(start = 20.dp, end = 20.dp, top = 16.dp, bottom = 24.dp),
        horizontalArrangement = Arrangement.spacedBy(12.dp), verticalArrangement = Arrangement.spacedBy(16.dp),
    ) {
        itemsIndexed(paged.items, key = { _, it -> it.id }) { _, a -> CoverCell(api, a, round = round, centred = round, onHold = onHold) { onClick(a) } }
    }
    paged.error?.let { ErrorBox(it) }
}

private val artistSorts = listOf("name" to R.string.sort_name, "plays" to R.string.sort_plays, "added" to R.string.sort_recent)
private val albumSorts = listOf("name" to R.string.sort_name, "artist" to R.string.sort_artist, "year" to R.string.sort_year, "added" to R.string.sort_recent)
private val songSorts = listOf("title" to R.string.sort_name, "artist" to R.string.sort_artist, "added" to R.string.sort_recent, "plays" to R.string.sort_plays)

@Composable
private fun ArtistsTab(api: CinevaApi, libraryId: String, onArtist: (String) -> Unit, onHold: (Item) -> Unit) {
    var sort by rememberSaveable { mutableStateOf("name") }
    val paged = rememberPaged(libraryId to sort) { page -> api.artists(libraryId, ListingQuery(sort = sort, page = page)) }
    LaunchedEffect(paged) { paged.reset() }
    Column(Modifier.fillMaxSize()) {
        SortWords(artistSorts.map { it.first to stringResource(it.second) }, sort, count = stringResource(R.string.artists_count, paged.total).takeIf { paged.total > 0 }) { sort = it }
        CoverGrid(api, paged, round = true, onHold = onHold) { onArtist(it.id) }
    }
}

@Composable
private fun AlbumsTab(api: CinevaApi, libraryId: String, onAlbum: (String) -> Unit, onHold: (Item) -> Unit) {
    var sort by rememberSaveable { mutableStateOf("name") }
    val paged = rememberPaged(libraryId to sort) { page -> api.albums(libraryId, ListingQuery(sort = sort, page = page)) }
    LaunchedEffect(paged) { paged.reset() }
    Column(Modifier.fillMaxSize()) {
        SortWords(albumSorts.map { it.first to stringResource(it.second) }, sort, count = stringResource(R.string.albums_count, paged.total).takeIf { paged.total > 0 }) { sort = it }
        CoverGrid(api, paged, onHold = onHold) { onAlbum(it.id) }
    }
}

@Composable
private fun SongsTab(libs: LibrariesViewModel, libraryId: String, onHold: (Item) -> Unit) {
    var sort by rememberSaveable { mutableStateOf("title") }
    val paged = rememberPaged(libraryId to sort) { page -> libs.api.tracks(libraryId, ListingQuery(sort = sort, page = page, size = 100)) }
    LaunchedEffect(paged) { paged.reset() }
    val playing by libs.player.state.collectAsState()
    Column(Modifier.fillMaxSize()) {
        SortWords(songSorts.map { it.first to stringResource(it.second) }, sort, count = stringResource(R.string.songs_count, paged.total).takeIf { paged.total > 0 }) { sort = it }
        val list = rememberLazyListState()
        LoadMore(list.firstVisibleItemIndex + 30 >= paged.items.size, paged)
        LazyColumn(Modifier.fillMaxSize(), state = list, contentPadding = PaddingValues(top = 10.dp, bottom = 24.dp)) {
            item { Text(stringResource(R.string.songs_note, paged.items.size, paged.total), Modifier.padding(horizontal = 20.dp, vertical = 4.dp), style = t(12, W400, tokens.muted)) }
            itemsIndexed(paged.items, key = { _, it -> it.id }) { i, t ->
                // Plays what is loaded so far from this track: the whole
                // library would be the queue's thousand-entry cap many
                // times over.
                SongRow(libs.api, t, playing = playing.current?.id == t.id, onHold = onHold,
                    onClick = { libs.player.play(paged.items, i, QueueSource("library", libraryId, "Songs")) })
            }
        }
        paged.error?.let { ErrorBox(it) }
    }
}

@Composable
private fun GenresTab(api: CinevaApi, libraryId: String, onAlbum: (String) -> Unit, onHold: (Item) -> Unit) {
    var genres by remember(libraryId) { mutableStateOf<List<GenreCount>>(emptyList()) }
    var chosen by rememberSaveable { mutableStateOf("") }
    var error by remember { mutableStateOf<String?>(null) }
    LaunchedEffect(libraryId) { runCatching { api.genres(libraryId) }.onSuccess { genres = it }.onFailure { error = it.describe() } }
    error?.let { ErrorBox(it); return }
    if (chosen.isEmpty()) {
        LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(top = 10.dp, bottom = 24.dp)) {
            itemsIndexed(genres, key = { _, it -> it.genre }) { i, g ->
                Column {
                    if (i > 0) Hairline(Modifier.padding(horizontal = 20.dp))
                    Row(Modifier.fillMaxWidth().clickable { chosen = g.genre }.padding(horizontal = 20.dp).height(50.dp), verticalAlignment = Alignment.CenterVertically) {
                        Text(g.genre, Modifier.weight(1f), style = t(15, W500, tokens.text))
                        Text(stringResource(R.string.albums_count, g.count), style = t(14, W400, tokens.muted))
                    }
                }
            }
        }
    } else {
        val paged = rememberPaged(libraryId to chosen) { page -> api.albums(libraryId, ListingQuery(genre = chosen, sort = "artist", page = page)) }
        LaunchedEffect(paged) { paged.reset() }
        Column(Modifier.fillMaxSize()) {
            Row(Modifier.fillMaxWidth().padding(start = 20.dp, end = 20.dp, top = 14.dp), verticalAlignment = Alignment.CenterVertically) {
                Text(chosen, Modifier.weight(1f), style = t(15, W700, tokens.text))
                Word(stringResource(R.string.all_genres)) { chosen = "" }
            }
            Box(Modifier.weight(1f)) { CoverGrid(api, paged, onHold = onHold) { onAlbum(it.id) } }
        }
    }
}


/**
 * The person's playlists as mosaics in thirds, with a way to make one:
 * the playlists are a first-class place in the library, not a row that
 * hides when there are none.
 */
@Composable
fun PlaylistsTab(api: CinevaApi, onPlaylist: (String) -> Unit) {
    var lists by remember { mutableStateOf<List<Playlist>?>(null) }
    var name by rememberSaveable { mutableStateOf("") }
    var error by remember { mutableStateOf<String?>(null) }
    val scope = rememberCoroutineScope()
    fun reload() {
        scope.launch { runCatching { api.playlists() }.onSuccess { lists = it }.onFailure { error = it.message } }
    }
    LaunchedEffect(Unit) { reload() }
    fun create() {
        val n = name.trim()
        if (n.isEmpty()) return
        scope.launch {
            runCatching { api.createPlaylist(n) }
                .onSuccess { name = ""; reload(); onPlaylist(it.id) }
                .onFailure { error = it.message }
        }
    }
    Column(Modifier.fillMaxSize()) {
        Row(Modifier.padding(horizontal = 20.dp, vertical = 10.dp), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(10.dp)) {
            Field(name, { name = it }, stringResource(R.string.playlist_name), Modifier.weight(1f), onDone = { create() })
            Pill(stringResource(R.string.new_playlist), filled = true, enabled = name.isNotBlank()) { create() }
        }
        error?.let { ErrorBox(it) { error = null; reload() } }
        val all = lists ?: return@Column
        if (all.isEmpty()) {
            EmptyNote(stringResource(R.string.no_playlists_yet))
            return@Column
        }
        LazyVerticalGrid(GridCells.Fixed(3), Modifier.fillMaxSize(), contentPadding = PaddingValues(horizontal = 20.dp, vertical = 6.dp),
            horizontalArrangement = Arrangement.spacedBy(12.dp), verticalArrangement = Arrangement.spacedBy(16.dp)) {
            itemsIndexed(all, key = { _, p -> p.id }) { _, p ->
                Column(Modifier.clickable { onPlaylist(p.id) }) {
                    Mosaic(api, p.covers, Modifier.fillMaxWidth().aspectRatio(1f))
                    Spacer(Modifier.height(7.dp))
                    Text(p.name, maxLines = 1, overflow = TextOverflow.Ellipsis, style = t(13, W600, tokens.text))
                    Text(stringResource(R.string.tracks_count, p.trackCount), style = t(12, W400, tokens.muted))
                }
            }
        }
    }
}
