package com.garybowers.cineva.ui

import androidx.activity.compose.BackHandler
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.core.tween
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.slideInVertically
import androidx.compose.animation.slideOutVertically
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.navigation.NavGraph.Companion.findStartDestination
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.currentBackStackEntryAsState
import androidx.navigation.compose.rememberNavController
import com.garybowers.cineva.api.CinevaApi
import com.garybowers.cineva.api.SessionStore
import com.garybowers.cineva.player.PlayerRepository
import com.garybowers.cineva.ui.screens.AlbumScreen
import com.garybowers.cineva.ui.screens.ArtistScreen
import com.garybowers.cineva.ui.screens.BottomBar
import com.garybowers.cineva.ui.screens.ItemSheet
import com.garybowers.cineva.ui.screens.ItemSheetViewModel
import com.garybowers.cineva.ui.screens.LibraryScreen
import com.garybowers.cineva.ui.screens.ListenScreen
import com.garybowers.cineva.ui.screens.MiniBar
import com.garybowers.cineva.ui.screens.NowPlayingScreen
import com.garybowers.cineva.ui.screens.PlaylistScreen
import com.garybowers.cineva.ui.screens.SearchScreen
import com.garybowers.cineva.ui.screens.SettingsScreen
import com.garybowers.cineva.ui.screens.SignInScreen
import com.garybowers.cineva.ui.screens.SyncScreen
import com.garybowers.cineva.ui.screens.W500
import com.garybowers.cineva.ui.screens.strip
import androidx.compose.foundation.layout.Row
import com.garybowers.cineva.ui.screens.Word
import com.garybowers.cineva.ui.screens.t
import com.garybowers.cineva.ui.screens.tokens
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.delay
import javax.inject.Inject

/** The routes; items are addressed by id, as the server addresses them. */
object Routes {
    const val SIGN_IN = "signin"
    const val LISTEN = "listen"
    const val LIBRARY = "library"
    const val SEARCH = "search"
    const val SYNC = "sync"
    const val SETTINGS = "settings"
    const val ARTIST = "artist/{id}"
    const val ALBUM = "album/{id}"
    const val PLAYLIST = "playlist/{id}"
    fun artist(id: String) = "artist/$id"
    fun album(id: String) = "album/$id"
    fun playlist(id: String) = "playlist/$id"
    val tabs = listOf(LISTEN, LIBRARY, SEARCH, SYNC)
}

@HiltViewModel
class ShellViewModel @Inject constructor(val sessions: SessionStore, val player: PlayerRepository, val api: CinevaApi) : ViewModel() {
    /** The now-playing sheet, over whatever page is open. */
    var nowPlayingOpen by mutableStateOf(false)
}

/**
 * The app: sign-in, then four tabs with the mini player floating above a
 * slim bar, the now-playing sheet sliding up over everything, and the
 * item sheet for a held track, album or artist. All Cineva's own pieces.
 */
@Composable
fun CinevaNavHost(vm: ShellViewModel = hiltViewModel(), sheet: ItemSheetViewModel = hiltViewModel()) {
    val session by vm.sessions.session.collectAsState()
    val nav = rememberNavController()
    val entry by nav.currentBackStackEntryAsState()
    val route = entry?.destination?.route
    val player by vm.player.state.collectAsState()
    var notice by remember { mutableStateOf<String?>(null) }
    LaunchedEffect(Unit) { vm.player.notices.collect { notice = it } }
    LaunchedEffect(notice) { if (notice != null) { delay(3500); notice = null } }
    val toast by sheet.toasts.current.collectAsState()
    LaunchedEffect(toast) { toast?.let { t -> delay(2000); sheet.toasts.dismiss(t) } }
    // Signed out elsewhere, or here: back to the sign-in screen.
    LaunchedEffect(session) { if (session == null && route != null && route != Routes.SIGN_IN) nav.navigate(Routes.SIGN_IN) { popUpTo(0) } }
    // Nothing left to play: the sheet has nothing to show.
    LaunchedEffect(player.current) { if (player.current == null) vm.nowPlayingOpen = false }

    val chrome = session != null && route != Routes.SIGN_IN
    val toArtist: (String) -> Unit = { vm.nowPlayingOpen = false; nav.navigate(Routes.artist(it)) }
    val toAlbum: (String) -> Unit = { vm.nowPlayingOpen = false; nav.navigate(Routes.album(it)) }
    val toSettings: () -> Unit = { nav.navigate(Routes.SETTINGS) }
    val activeTab = Routes.tabs.firstOrNull { it == route } ?: when {
        route?.startsWith("artist") == true || route?.startsWith("album") == true || route?.startsWith("playlist") == true -> Routes.LIBRARY
        else -> ""
    }

    Box(Modifier.fillMaxSize().background(tokens.bg)) {
        Column(Modifier.fillMaxSize()) {
            Box(Modifier.weight(1f).fillMaxWidth()) {
                NavHost(
                    navController = nav,
                    startDestination = if (session == null) Routes.SIGN_IN else Routes.LISTEN,
                    modifier = Modifier.fillMaxSize(),
                ) {
                    composable(Routes.SIGN_IN) {
                        SignInScreen(onSignedIn = { nav.navigate(Routes.LISTEN) { popUpTo(Routes.SIGN_IN) { inclusive = true } } })
                    }
                    composable(Routes.LISTEN) {
                        ListenScreen(onAlbum = toAlbum, onPlaylist = { nav.navigate(Routes.playlist(it)) }, onHold = sheet::show, onSettings = toSettings, onOpenNowPlaying = { vm.nowPlayingOpen = true })
                    }
                    composable(Routes.LIBRARY) { LibraryScreen(onArtist = toArtist, onAlbum = toAlbum, onPlaylist = { nav.navigate(Routes.playlist(it)) }, onHold = sheet::show, onSettings = toSettings) }
                    composable(Routes.SEARCH) { SearchScreen(onArtist = toArtist, onAlbum = toAlbum, onHold = sheet::show, onCancel = { nav.navigate(Routes.LISTEN) { popUpTo(nav.graph.findStartDestination().id); launchSingleTop = true } }) }
                    composable(Routes.SYNC) { SyncScreen(onSettings = toSettings) }
                    composable(Routes.SETTINGS) {
                        SettingsScreen(onBack = { nav.popBackStack() }, onSignedOut = { nav.navigate(Routes.SIGN_IN) { popUpTo(0) } })
                    }
                    composable(Routes.ARTIST) { back ->
                        ArtistScreen(id = back.arguments?.getString("id") ?: "", onAlbum = toAlbum, onBack = { nav.popBackStack() }, onHold = sheet::show)
                    }
                    composable(Routes.ALBUM) { back ->
                        AlbumScreen(id = back.arguments?.getString("id") ?: "", onArtist = toArtist, onAlbum = toAlbum, onBack = { nav.popBackStack() }, onHold = sheet::show)
                    }
                    composable(Routes.PLAYLIST) { back ->
                        PlaylistScreen(id = back.arguments?.getString("id") ?: "", onBack = { nav.popBackStack() }, onHold = sheet::show)
                    }
                }
            }
            if (chrome) {
                val t = player.current
                if (t != null) {
                    MiniBar(
                        api = vm.api, track = t, playing = player.isPlaying,
                        fraction = if (player.durationMs > 0) player.positionMs.toFloat() / player.durationMs else 0f,
                        onOpen = { vm.nowPlayingOpen = true }, onToggle = { vm.player.togglePlay() }, onNext = { vm.player.next() }, onPrevious = { vm.player.previous() },
                    )
                }
                BottomBar(active = activeTab) { r ->
                    nav.navigate(r) {
                        popUpTo(nav.graph.findStartDestination().id) { saveState = true }
                        launchSingleTop = true
                        restoreState = true
                    }
                }
            } else {
                Box(Modifier.navigationBarsPadding())
            }
        }

        // The now-playing sheet rises from the mini player and the cover grows into place.
        AnimatedVisibility(
            visible = vm.nowPlayingOpen && player.current != null,
            enter = slideInVertically(tween(320)) { it } + fadeIn(tween(200)),
            exit = slideOutVertically(tween(260)) { it } + fadeOut(tween(200)),
        ) {
            BackHandler { vm.nowPlayingOpen = false }
            NowPlayingScreen(open = vm.nowPlayingOpen, onClose = { vm.nowPlayingOpen = false }, onAlbum = toAlbum, onArtist = toArtist, onHold = sheet::show, onMore = { sheet.show(it, nowPlaying = true) })
        }
        ItemSheet(sheet, onArtist = toArtist, onAlbum = toAlbum)
        // "Added to Driving · Undo": two seconds, with its one action.
        AnimatedVisibility(toast != null, Modifier.align(Alignment.BottomCenter).padding(bottom = 150.dp), enter = fadeIn(), exit = fadeOut()) {
            val tst = toast
            Row(Modifier.clip(RoundedCornerShape(10.dp)).background(tokens.strip).padding(start = 14.dp, end = 6.dp, top = 4.dp, bottom = 4.dp), verticalAlignment = Alignment.CenterVertically) {
                Text(tst?.text ?: "", Modifier.padding(vertical = 5.dp), style = t(13, W500, tokens.text))
                if (tst?.action != null) {
                    Text(" · ", style = t(13, W500, tokens.muted))
                    Word(tst.action) { tst.onAction?.invoke(); sheet.toasts.dismiss(tst) }
                }
            }
        }
        // A short notice: "Continued on Living-room TV".
        AnimatedVisibility(notice != null, Modifier.align(Alignment.BottomCenter).padding(bottom = 150.dp), enter = fadeIn(), exit = fadeOut()) {
            Text(notice ?: "", Modifier.clip(RoundedCornerShape(10.dp)).background(tokens.strip).padding(horizontal = 14.dp, vertical = 9.dp), style = t(13, W500, tokens.text))
        }
    }
}
