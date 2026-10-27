package com.garybowers.cineva.ui.screens

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Star
import androidx.compose.material.icons.outlined.QueueMusic
import androidx.compose.material.icons.outlined.StarBorder
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.MutableState
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshotFlow
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import coil.compose.AsyncImage
import com.garybowers.cineva.R
import com.garybowers.cineva.api.ApiException
import com.garybowers.cineva.api.CinevaApi
import com.garybowers.cineva.api.Item
import com.garybowers.cineva.api.MusicPage
import com.garybowers.cineva.api.SessionStore
import com.garybowers.cineva.data.Ratings
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.distinctUntilChanged
import java.io.IOException
import javax.inject.Inject

@HiltViewModel
class ShellHeaderViewModel @Inject constructor(val sessions: SessionStore, val api: CinevaApi) : ViewModel()

/** A page's top row: its label and the avatar that opens Settings. */
@Composable
fun TopRow(label: String, onSettings: () -> Unit, vm: ShellHeaderViewModel = hiltViewModel()) {
    val session by vm.sessions.session.collectAsState()
    PageTop(label, onSettings, session?.username?.take(1)?.uppercase() ?: "?")
}

/**
 * A theme's background picture under the page, dimmed by the theme's own
 * bg colour as the web client dims it, so text and covers stay readable.
 * Nothing is drawn for a theme without one.
 */
@Composable
fun ThemeBackdrop(api: CinevaApi, content: @Composable () -> Unit) {
    val tk = tokens
    Box(Modifier.fillMaxSize().background(tk.bg)) {
        if (tk.background.isNotEmpty()) {
            AsyncImage(model = api.absolute(tk.background), contentDescription = null, contentScale = ContentScale.Crop, modifier = Modifier.fillMaxSize())
            Box(Modifier.fillMaxSize().background(tk.bg.copy(alpha = tk.dim.coerceIn(0f, 1f))))
        }
        content()
    }
}

/** Five stars; tapping the lit one clears. */
@Composable
fun Stars(itemId: String, fromServer: Int, ratings: Ratings, size: Dp = 22.dp, tint: Color = playingAccentColour()) {
    val overrides by ratings.overrides.collectAsState()
    val stars = overrides[itemId] ?: fromServer
    Row {
        for (i in 1..5) {
            Box(Modifier.size(size + 14.dp).clickable(interactionSource = remember { MutableInteractionSource() }, indication = null) { ratings.rate(itemId, if (i == stars) 0 else i, stars) }, contentAlignment = Alignment.Center) {
                Icon(if (i <= stars) Icons.Filled.Star else Icons.Outlined.StarBorder, contentDescription = null, tint = if (i <= stars) tint else tokens.muted, modifier = Modifier.size(size))
            }
        }
    }
}

@Composable
fun playingAccentColour(): Color = com.garybowers.cineva.ui.theme.playingAccent()

@Composable
fun ErrorBox(message: String, onRetry: (() -> Unit)? = null) {
    Column(Modifier.fillMaxWidth().padding(24.dp), horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(14.dp)) {
        Text(stringResource(R.string.error_generic, message), style = t(14, W500, tokens.err), textAlign = TextAlign.Center)
        if (onRetry != null) Pill(stringResource(R.string.retry), filled = true, onClick = onRetry)
    }
}

@Composable
fun EmptyNote(text: String) {
    Box(Modifier.fillMaxSize().padding(32.dp), contentAlignment = Alignment.Center) {
        Text(text, style = t(14, W400, tokens.muted), textAlign = TextAlign.Center)
    }
}

/** Up to four covers in a square, as the web client draws a playlist. */
@Composable
fun Mosaic(api: CinevaApi, covers: List<String>, modifier: Modifier = Modifier, radius: Dp = 8.dp) {
    val shape = RoundedCornerShape(radius)
    Box(modifier.clip(shape).background(Brush.linearGradient(listOf(Color(0xFF2A3140), Color(0xFF161B23))))) {
        when {
            covers.isEmpty() -> Icon(Icons.Outlined.QueueMusic, null, Modifier.align(Alignment.Center).size(32.dp), tint = Color(0xFF4A5366))
            covers.size < 4 -> AsyncImage(model = api.absolute(covers[0]), contentDescription = null, modifier = Modifier.fillMaxSize(), contentScale = ContentScale.Crop)
            else -> Column(Modifier.fillMaxSize()) {
                for (r in 0..1) Row(Modifier.weight(1f)) {
                    for (c in 0..1) AsyncImage(model = api.absolute(covers[r * 2 + c]), contentDescription = null, modifier = Modifier.weight(1f).fillMaxSize(), contentScale = ContentScale.Crop)
                }
            }
        }
    }
}

fun trackTime(secs: Int): String {
    if (secs <= 0) return ""
    val m = secs / 60
    val s = secs % 60
    return if (m >= 60) "%d:%02d:%02d".format(m / 60, m % 60, s) else "%d:%02d".format(m, s)
}

fun totalTime(secs: Int): String {
    val m = secs / 60
    return if (m >= 60) "${m / 60} h ${m % 60} min" else "$m min"
}

/** The message a failure shows. */
fun Throwable.describe(): String = when (this) {
    is ApiException -> message ?: "HTTP $status"
    is IOException -> message ?: "network error"
    else -> message ?: toString()
}

/**
 * A listing fetched a page at a time as it scrolls. `load` fetches one
 * page; `more()` is called when the last rows come into view.
 */
class Paged(private val load: suspend (page: Int) -> MusicPage) {
    var items: List<Item> by mutableStateOf(emptyList())
    var total: Int by mutableStateOf(0)
    var loading: Boolean by mutableStateOf(false)
    var error: String? by mutableStateOf(null)
    private var page = 0
    private var done = false

    suspend fun reset() {
        items = emptyList(); total = 0; page = 0; done = false; error = null
        more()
    }

    suspend fun more() {
        if (loading || done) return
        loading = true
        try {
            val p = load(page + 1)
            page = p.page
            items = items + p.items
            total = p.total
            done = items.size >= p.total || p.items.isEmpty()
        } catch (e: Exception) {
            error = e.describe()
        } finally {
            loading = false
        }
    }
}

@Composable
fun rememberPaged(key: Any?, load: suspend (page: Int) -> MusicPage): Paged = remember(key) { Paged(load) }

/** Asks the listing for its next page when the end comes into view. */
@Composable
fun LoadMore(nearEnd: Boolean, paged: Paged) {
    LaunchedEffect(paged) {
        snapshotFlow { nearEnd }.distinctUntilChanged().collect { if (it) paged.more() }
    }
    LaunchedEffect(nearEnd, paged.items.size) { if (nearEnd) paged.more() }
}

/** Remembered text state for a search box. */
@Composable
fun rememberText(initial: String = ""): MutableState<String> = remember { mutableStateOf(initial) }

/** Dims what is not available. */
fun Modifier.dim(on: Boolean): Modifier = if (on) alpha(0.45f) else this
