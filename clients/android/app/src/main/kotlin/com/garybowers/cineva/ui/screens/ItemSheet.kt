package com.garybowers.cineva.ui.screens

import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.BoxScope
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Album
import androidx.compose.material.icons.outlined.AutoAwesome
import androidx.compose.material.icons.outlined.Check
import androidx.compose.material.icons.outlined.Download
import androidx.compose.material.icons.outlined.Person
import androidx.compose.material.icons.outlined.PlaylistAdd
import androidx.compose.material.icons.outlined.QueueMusic
import androidx.compose.material.icons.outlined.SkipNext
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.garybowers.cineva.R
import com.garybowers.cineva.api.CinevaApi
import com.garybowers.cineva.api.Item
import com.garybowers.cineva.api.Playlist
import com.garybowers.cineva.data.AppPrefs
import com.garybowers.cineva.data.Ratings
import com.garybowers.cineva.data.Toasts
import com.garybowers.cineva.player.PlayerRepository
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.launch
import javax.inject.Inject

/** Holding a track, album or artist: what to do with it, with the cover and title at the top. */
@HiltViewModel
class ItemSheetViewModel @Inject constructor(
    val api: CinevaApi, val player: PlayerRepository, val ratings: Ratings, val prefs: AppPrefs, val toasts: Toasts,
) : ViewModel() {
    var item by mutableStateOf<Item?>(null)
    /** Opened from now playing: Add to playlist leads. */
    var fromNowPlaying by mutableStateOf(false)
    var pickingPlaylist by mutableStateOf(false)
    var playlists by mutableStateOf<List<Playlist>>(emptyList())
    var filter by mutableStateOf("")
    var newName by mutableStateOf("")
    var message by mutableStateOf<String?>(null)

    fun show(it: Item, nowPlaying: Boolean = false) { item = it; fromNowPlaying = nowPlaying; pickingPlaylist = false; message = null }
    fun dismiss() { item = null; pickingPlaylist = false; filter = "" }

    private suspend fun tracksOf(it: Item): List<Item> = if (it.kind == "track") listOf(it) else api.tracksUnder(it.id)

    fun playNext() = act { player.playNext(tracksOf(it)) }
    fun addToQueue() = act { player.addToQueue(tracksOf(it)) }

    /** A mix from the held track, album or artist (server/docs/mix.md). */
    fun mix() {
        val it = item ?: return
        dismiss()
        player.startMix(it) { err -> if (err != null) toasts.show(err) }
    }

    /** The playlists by use, each saying whether it holds the track; the one used last leads. */
    fun pickPlaylist() {
        pickingPlaylist = true
        filter = ""
        val it = item ?: return
        viewModelScope.launch {
            runCatching { if (it.kind == "track") api.playlistsFor(it.id) else api.playlists() }
                .onSuccess { all ->
                    val manual = all.filter { p -> !p.isSmart }
                    val last = prefs.current.lastPlaylistId
                    playlists = manual.sortedByDescending { p -> p.id == last }
                }
        }
    }

    /** The three to offer as chips: the last used, then by use. */
    val chips: List<Playlist> get() = playlists.take(3)
    val shown: List<Playlist> get() {
        val f = filter.trim()
        return if (f.isEmpty()) playlists else playlists.filter { it.name.contains(f, ignoreCase = true) }
    }

    fun addTo(p: Playlist) {
        val it = item ?: return
        viewModelScope.launch {
            runCatching { api.addToPlaylist(p.id, listOf(it.id)) }
                .onSuccess { d ->
                    prefs.update { s -> s.copy(lastPlaylistId = p.id) }
                    added(d.playlist.name, p.id)
                    dismiss()
                }
                .onFailure { e -> message = e.describe() }
        }
    }

    /** Enter in the filter adds to the first match. */
    fun addToFirstMatch() { shown.firstOrNull()?.let { addTo(it) } }

    fun addToNew() {
        val it = item ?: return
        val name = newName.trim().ifEmpty { return }
        viewModelScope.launch {
            runCatching { api.createPlaylist(name).let { p -> api.addToPlaylist(p.id, listOf(it.id)) } }
                .onSuccess { d ->
                    prefs.update { s -> s.copy(lastPlaylistId = d.playlist.id) }
                    newName = ""
                    added(d.playlist.name, d.playlist.id)
                    dismiss()
                }
                .onFailure { e -> message = e.describe() }
        }
    }

    /** "Added to Driving · Undo": the undo takes the last entry off again. */
    private fun added(name: String, playlistId: String) {
        toasts.show("Added to $name", "Undo") {
            viewModelScope.launch {
                runCatching { api.removeLastFromPlaylist(playlistId) }.onFailure { e -> toasts.show(e.describe()) }
            }
        }
    }

    /** The artist id of what is held: an artist itself, an album's parent, a track's album's parent. */
    fun artistId(then: (String) -> Unit) {
        val it = item ?: return
        viewModelScope.launch {
            val id = when (it.kind) {
                "artist" -> it.id
                "album" -> it.parentId
                else -> it.parentId?.let { a -> runCatching { api.item(a).item.parentId }.getOrNull() }
            }
            id?.let(then)
        }
    }

    private fun act(f: suspend (Item) -> Unit) {
        val it = item ?: return
        viewModelScope.launch { runCatching { f(it) }.onFailure { e -> message = e.describe() }; dismiss() }
    }
}

/** The long-press sheet: the cover and title at its top, then the actions, and stars to rate a track. */
@Composable
fun BoxScope.ItemSheet(vm: ItemSheetViewModel, onArtist: (String) -> Unit, onAlbum: (String) -> Unit) {
    val item = vm.item
    Sheet(item != null, onDismiss = { vm.dismiss() }) {
        if (item == null) return@Sheet
        Column(Modifier.padding(bottom = 8.dp)) {
            Row(Modifier.padding(horizontal = 20.dp, vertical = 8.dp), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(14.dp)) {
                Art(vm.api, item, Modifier.size(56.dp), radius = 8.dp, round = item.kind == "artist", letterSize = 20)
                Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(2.dp)) {
                    Text(item.title, maxLines = 2, overflow = TextOverflow.Ellipsis, style = t(16, W700, tokens.text))
                    val sub = listOf(item.artist, item.album).filter { it.isNotEmpty() && it != item.title }.joinToString(" · ")
                    if (sub.isNotEmpty()) Text(sub, maxLines = 1, overflow = TextOverflow.Ellipsis, style = t(12, W400, tokens.muted))
                }
            }
            if (item.kind == "track") {
                Row(Modifier.padding(start = 14.dp), verticalAlignment = Alignment.CenterVertically) {
                    Stars(item.id, item.userRating, vm.ratings, size = 24.dp)
                }
            }
            vm.message?.let { Text(it, Modifier.padding(horizontal = 20.dp, vertical = 6.dp), style = t(13, W500, playingAccentColour())) }
            if (vm.pickingPlaylist) {
                PlaylistPicker(vm)
            } else {
                // From now playing, adding the playing song to a playlist is what the sheet is for.
                if (vm.fromNowPlaying) SheetAction(Icons.Outlined.PlaylistAdd, stringResource(R.string.add_to_playlist)) { vm.pickPlaylist() }
                SheetAction(Icons.Outlined.AutoAwesome, stringResource(R.string.mix_from_this)) { vm.mix() }
                SheetAction(Icons.Outlined.SkipNext, stringResource(R.string.play_next)) { vm.playNext() }
                SheetAction(Icons.Outlined.QueueMusic, stringResource(R.string.add_to_queue)) { vm.addToQueue() }
                if (!vm.fromNowPlaying) SheetAction(Icons.Outlined.PlaylistAdd, stringResource(R.string.add_to_playlist)) { vm.pickPlaylist() }
                SheetAction(Icons.Outlined.Download, stringResource(R.string.download), enabled = false, note = stringResource(R.string.download_sync_note)) {}
                if (item.kind != "artist") SheetAction(Icons.Outlined.Person, stringResource(R.string.go_to_artist)) { vm.artistId { id -> vm.dismiss(); onArtist(id) } }
                if (item.kind == "track" && !item.parentId.isNullOrEmpty()) SheetAction(Icons.Outlined.Album, stringResource(R.string.go_to_album)) { vm.dismiss(); onAlbum(item.parentId) }
            }
        }
    }
}

@Composable
private fun SheetAction(icon: ImageVector, label: String, enabled: Boolean = true, note: String? = null, onClick: () -> Unit) {
    val colour = if (enabled) tokens.text else tokens.muted
    Row(
        Modifier.fillMaxWidth().height(54.dp).clickable(enabled = enabled, onClick = onClick).padding(horizontal = 24.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Icon(icon, contentDescription = null, tint = colour, modifier = Modifier.size(22.dp))
        Spacer(Modifier.width(18.dp))
        Column {
            Text(label, style = t(15, W500, colour))
            if (note != null) Text(note, style = t(12, W400, tokens.muted))
        }
    }
}

/**
 * The picker, by use: the last three as chips (the one used last first),
 * then the rest; a tick where the track is already in, with "Add again";
 * a filter above eight, Enter adding to the first match.
 */
@Composable
private fun PlaylistPicker(vm: ItemSheetViewModel) {
    val accent = playingAccentColour()
    Column(Modifier.padding(horizontal = 20.dp)) {
        Text(stringResource(R.string.add_to_playlist), style = t(15, W700, tokens.text), modifier = Modifier.padding(vertical = 8.dp))
        if (vm.chips.isNotEmpty() && vm.filter.isEmpty()) {
            Row(Modifier.fillMaxWidth().horizontalScroll(rememberScrollState()).padding(bottom = 10.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                for (p in vm.chips) {
                    Pill(p.name, icon = if (p.holds) Icons.Outlined.Check else Icons.Outlined.PlaylistAdd, filled = !p.holds) { vm.addTo(p) }
                }
            }
        }
        if (vm.playlists.size > 8) {
            Field(vm.filter, { vm.filter = it }, stringResource(R.string.filter_playlists), Modifier.fillMaxWidth().padding(bottom = 6.dp), onDone = { vm.addToFirstMatch() })
        }
        for (p in vm.shown) {
            Row(Modifier.fillMaxWidth().height(48.dp).clickable { vm.addTo(p) }, verticalAlignment = Alignment.CenterVertically) {
                Icon(if (p.holds) Icons.Outlined.Check else Icons.Outlined.QueueMusic, null, Modifier.size(20.dp), tint = if (p.holds) accent else tokens.muted)
                Spacer(Modifier.width(14.dp))
                Text(p.name, style = t(15, W500, tokens.text), modifier = Modifier.weight(1f))
                Text(if (p.holds) stringResource(R.string.add_again) else stringResource(R.string.tracks_count, p.trackCount), style = t(12, W400, if (p.holds) accent else tokens.muted))
            }
        }
        Row(Modifier.fillMaxWidth().padding(top = 8.dp), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(10.dp)) {
            Field(vm.newName, { vm.newName = it }, stringResource(R.string.new_playlist), Modifier.weight(1f))
            Pill(stringResource(R.string.create), filled = true, enabled = vm.newName.isNotBlank()) { vm.addToNew() }
        }
        Word(stringResource(R.string.back)) { vm.pickingPlaylist = false }
    }
}
