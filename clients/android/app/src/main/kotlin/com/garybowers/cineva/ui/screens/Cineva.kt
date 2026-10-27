package com.garybowers.cineva.ui.screens

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.core.tween
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.slideInVertically
import androidx.compose.animation.slideOutVertically
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.gestures.detectHorizontalDragGestures
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.gestures.detectVerticalDragGestures
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxScope
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.RowScope
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Pause
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material.icons.filled.SkipNext
import androidx.compose.material.icons.outlined.Download
import androidx.compose.material.icons.outlined.Headphones
import androidx.compose.material.icons.outlined.LibraryMusic
import androidx.compose.material.icons.outlined.Search
import androidx.compose.material.icons.outlined.Sync
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.ui.layout.positionInParent
import androidx.compose.ui.layout.onGloballyPositioned
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.horizontalScroll
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.draw.shadow
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.hapticfeedback.HapticFeedbackType
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalHapticFeedback
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import coil.compose.AsyncImage
import com.garybowers.cineva.R
import com.garybowers.cineva.api.CinevaApi
import com.garybowers.cineva.api.Item
import com.garybowers.cineva.ui.theme.Inter
import com.garybowers.cineva.ui.theme.LocalTokens
import com.garybowers.cineva.ui.theme.Tokens
import com.garybowers.cineva.ui.theme.onColour
import com.garybowers.cineva.ui.theme.playingAccent

// Cineva's own pieces, drawn from Compose foundation to the agreed
// mockups (docs/mockups/*.dc.html), on the theme tokens. Nothing here is
// a Material component as it comes: the type sizes, weights, spacing and
// colours are the mockups' own.

val tokens: Tokens @Composable get() = LocalTokens.current

/** The raised strip (the mini player, sheets): #1b2029 on Midnight. */
val Tokens.strip: Color get() = Color(0xFF1B2029)

/** A hairline between regions: #1a1f28 on Midnight. */
val Tokens.hairline: Color get() = Color(0xFF1A1F28)

val Tokens.secondary: Color get() = Color(0xFFC9CEDA)
val Tokens.tertiary: Color get() = Color(0xFFA3ABBA)

fun t(size: Int, weight: FontWeight, colour: Color, tracking: Float = 0f) =
    TextStyle(fontFamily = Inter, fontSize = size.sp, fontWeight = weight, color = colour, letterSpacing = tracking.sp, lineHeight = (size * 1.25f).sp)

val W400 = FontWeight.Normal
val W500 = FontWeight.Medium
val W600 = FontWeight.SemiBold
val W700 = FontWeight.Bold
val W800 = FontWeight.ExtraBold

/** The small quiet label a page carries at the top ("LISTEN", "MUSIC"). */
@Composable
fun PageLabel(text: String, modifier: Modifier = Modifier) {
    Text(text.uppercase(), modifier, style = t(13, W600, tokens.muted, 1.0f))
}

/** A section label in a results list ("ARTISTS"). */
@Composable
fun SectionLabel(text: String, modifier: Modifier = Modifier) {
    Text(text.uppercase(), modifier.padding(horizontal = 20.dp), style = t(12, W600, tokens.muted, 1.0f))
}

/** A row heading with "All" on the right. */
@Composable
fun SectionHead(text: String, onAll: (() -> Unit)? = null) {
    Row(Modifier.fillMaxWidth().padding(horizontal = 20.dp), verticalAlignment = Alignment.Bottom) {
        Text(text, Modifier.weight(1f), style = t(17, W700, tokens.text))
        if (onAll != null) Text(stringResource(R.string.all), Modifier.clickable(onClick = onAll).padding(2.dp), style = t(13, W500, tokens.muted))
    }
}

/** The avatar at the top right: the user's initial on the accent; it opens Settings. */
@Composable
fun Avatar(initial: String, onClick: () -> Unit) {
    val accent = playingAccent()
    Box(
        Modifier.size(30.dp).clip(CircleShape).background(accent).clickable(onClick = onClick),
        contentAlignment = Alignment.Center,
    ) { Text(initial, style = t(13, W700, accent.onColour())) }
}

/** The page's top row: the label on the left, the avatar on the right. */
@Composable
fun PageTop(label: String, onSettings: () -> Unit, initial: String) {
    Row(Modifier.fillMaxWidth().padding(start = 20.dp, end = 20.dp, top = 12.dp), verticalAlignment = Alignment.CenterVertically) {
        PageLabel(label, Modifier.weight(1f))
        Avatar(initial, onSettings)
    }
}

/** Covers and pictures, or a letter on a soft gradient where there is none. */
@Composable
fun Art(api: CinevaApi, item: Item?, modifier: Modifier = Modifier, radius: Dp = 8.dp, round: Boolean = false, letterSize: Int = 30) {
    val shape = if (round) CircleShape else RoundedCornerShape(radius)
    val poster = item?.poster.orEmpty()
    if (poster.isEmpty()) {
        Placeholder(item?.title?.trim()?.firstOrNull()?.uppercaseChar()?.toString() ?: "", modifier.clip(shape), letterSize)
    } else {
        AsyncImage(model = api.absolute(poster), contentDescription = item?.title, modifier = modifier.clip(shape), contentScale = ContentScale.Crop)
    }
}

@Composable
fun Placeholder(letter: String, modifier: Modifier = Modifier, letterSize: Int = 30) {
    Box(
        modifier.background(Brush.linearGradient(listOf(Color(0xFF2A3140), Color(0xFF161B23)))),
        contentAlignment = Alignment.Center,
    ) { Text(letter, style = t(letterSize, W800, Color(0xFF4A5366))) }
}

/** A round target of at least 44 dp around an icon. */
@Composable
fun Tap(icon: ImageVector, description: String?, modifier: Modifier = Modifier, size: Dp = 44.dp, iconSize: Dp = 24.dp, tint: Color = tokens.text, onClick: () -> Unit) {
    Box(
        modifier.size(size).clip(CircleShape).clickable(onClick = onClick),
        contentAlignment = Alignment.Center,
    ) { Icon(icon, contentDescription = description, tint = tint, modifier = Modifier.size(iconSize)) }
}

/** The 46-high pill: filled in the accent for Play, quiet for Shuffle. */
@Composable
fun Pill(text: String, icon: ImageVector? = null, filled: Boolean = false, enabled: Boolean = true, onClick: () -> Unit) {
    val accent = playingAccent()
    val bg = if (filled) accent else tokens.text.copy(alpha = 0.1f)
    val fg = if (filled) accent.onColour() else tokens.text
    Row(
        Modifier.height(46.dp).clip(RoundedCornerShape(23.dp)).background(bg).clickable(enabled = enabled, onClick = onClick)
            .padding(horizontal = if (filled) 26.dp else 20.dp),
        verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        if (icon != null) Icon(icon, null, Modifier.size(18.dp), tint = fg)
        Text(text, style = t(15, if (filled) W700 else W600, fg), maxLines = 1, softWrap = false)
    }
}

/** A quiet round button beside the pills (Download). */
@Composable
fun RoundQuiet(icon: ImageVector, description: String, onClick: () -> Unit) {
    Box(
        Modifier.size(46.dp).clip(CircleShape).background(tokens.text.copy(alpha = 0.1f)).clickable(onClick = onClick),
        contentAlignment = Alignment.Center,
    ) { Icon(icon, description, Modifier.size(20.dp), tint = tokens.text) }
}

/** Tabs as words: the active one bold and white, the rest muted. */
@Composable
fun WordTabs(labels: List<String>, selected: Int, onSelect: (Int) -> Unit) {
    // Five words do not fit a phone at this size, so the row scrolls and
    // the chosen word is brought into view.
    val scroll = rememberScrollState()
    var selectedX by remember { mutableIntStateOf(0) }
    LaunchedEffect(selected, selectedX) { scroll.animateScrollTo(maxOf(0, selectedX - 20)) }
    Row(
        Modifier.fillMaxWidth().horizontalScroll(scroll).padding(start = 20.dp, end = 20.dp, top = 18.dp),
        horizontalArrangement = Arrangement.spacedBy(22.dp), verticalAlignment = Alignment.Bottom,
    ) {
        labels.forEachIndexed { i, label ->
            val on = i == selected
            Text(
                label,
                Modifier
                    .onGloballyPositioned { if (on) selectedX = it.positionInParent().x.toInt() }
                    .clickable(interactionSource = remember { MutableInteractionSource() }, indication = null) { onSelect(i) }
                    .padding(vertical = 4.dp),
                style = t(22, if (on) W800 else W600, if (on) tokens.text else tokens.muted),
            )
        }
    }
}

/** Sorts as small words; the chosen one underlined in the accent. A count sits on the right. */
@Composable
fun SortWords(options: List<Pair<String, String>>, chosen: String, count: String? = null, onChoose: (String) -> Unit) {
    val accent = playingAccent()
    Row(Modifier.fillMaxWidth().padding(start = 20.dp, end = 20.dp, top = 14.dp), verticalAlignment = Alignment.CenterVertically) {
        Row(Modifier.weight(1f), horizontalArrangement = Arrangement.spacedBy(14.dp)) {
            for ((value, label) in options) {
                val on = value == chosen
                Text(
                    label,
                    Modifier.clickable(interactionSource = remember { MutableInteractionSource() }, indication = null) { onChoose(value) }
                        .drawBehind { if (on) drawRect(accent, topLeft = Offset(0f, size.height - 2.dp.toPx()), size = androidx.compose.ui.geometry.Size(size.width, 2.dp.toPx())) }
                        .padding(bottom = 4.dp, top = 4.dp),
                    style = t(13, if (on) W600 else W500, if (on) tokens.text else tokens.muted),
                )
            }
        }
        if (count != null) Text(count, style = t(12, W400, tokens.muted))
    }
}

/** One cover in a rail or grid: the picture, the name, a line under it. A hold opens its sheet. */
@OptIn(ExperimentalFoundationApi::class)
@Composable
fun CoverCell(api: CinevaApi, item: Item, modifier: Modifier = Modifier, sub: String? = null, round: Boolean = false, centred: Boolean = false, onHold: ((Item) -> Unit)? = null, onClick: () -> Unit) {
    val haptics = LocalHapticFeedback.current
    Column(
        modifier.combinedClickable(interactionSource = remember { MutableInteractionSource() }, indication = null, onClick = onClick,
            onLongClick = onHold?.let { { haptics.performHapticFeedback(HapticFeedbackType.LongPress); it(item) } }),
        horizontalAlignment = if (centred) Alignment.CenterHorizontally else Alignment.Start,
    ) {
        Art(api, item, Modifier.fillMaxWidth().aspectRatio(1f), radius = 8.dp, round = round)
        Spacer(Modifier.height(7.dp))
        Text(item.title, maxLines = 1, overflow = TextOverflow.Ellipsis, style = t(13, W600, tokens.text), textAlign = if (centred) TextAlign.Center else null, modifier = Modifier.fillMaxWidth())
        val line = sub ?: when (item.kind) {
            "artist" -> stringResource(R.string.albums_count, item.childCount)
            else -> item.artist
        }
        if (line.isNotEmpty()) Text(line, maxLines = 1, overflow = TextOverflow.Ellipsis, style = t(12, W400, tokens.muted), textAlign = if (centred) TextAlign.Center else null, modifier = Modifier.fillMaxWidth())
    }
}

/** A row of 132 dp covers, 12 apart, inset 20. */
@Composable
fun CoverRail(api: CinevaApi, items: List<Item>, width: Dp = 132.dp, sub: ((Item) -> String)? = null, onHold: ((Item) -> Unit)? = null, onClick: (Item) -> Unit) {
    LazyRow(contentPadding = PaddingValues(horizontal = 20.dp), horizontalArrangement = Arrangement.spacedBy(12.dp)) {
        items(items, key = { it.id }) { it -> CoverCell(api, it, Modifier.width(width), sub = sub?.invoke(it), onHold = onHold) { onClick(it) } }
    }
}

/** A song as a row with its cover: title, "Artist · Album", time. Tap plays, hold opens the sheet. */
@OptIn(ExperimentalFoundationApi::class)
@Composable
fun SongRow(api: CinevaApi, track: Item, sub: String = listOf(track.artist, track.album).filter { it.isNotEmpty() }.joinToString(" · "), playing: Boolean = false, onHold: ((Item) -> Unit)? = null, trailing: (@Composable RowScope.() -> Unit)? = null, onClick: () -> Unit) {
    val haptics = LocalHapticFeedback.current
    val accent = playingAccent()
    Row(
        Modifier.fillMaxWidth()
            .combinedClickable(onClick = onClick, onLongClick = onHold?.let { { haptics.performHapticFeedback(HapticFeedbackType.LongPress); it(track) } })
            .padding(horizontal = 20.dp, vertical = 7.dp),
        verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        Art(api, track, Modifier.size(44.dp), radius = 6.dp, letterSize = 16)
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(2.dp)) {
            Text(track.title, maxLines = 1, overflow = TextOverflow.Ellipsis, style = t(14, W600, if (playing) accent else tokens.text))
            if (sub.isNotEmpty()) Text(sub, maxLines = 1, overflow = TextOverflow.Ellipsis, style = t(12, W400, tokens.muted))
        }
        if (trailing != null) trailing() else Text(trackTime(track.runtimeSecs), style = t(12, W400, tokens.muted))
    }
}

/** A track on an album: number, title, time; the playing one tinted with the bars; a tick where it is downloaded. */
@OptIn(ExperimentalFoundationApi::class)
@Composable
fun AlbumTrackRow(track: Item, number: Int, playing: Boolean, downloaded: Boolean = false, albumArtist: String = "", onHold: ((Item) -> Unit)? = null, onClick: () -> Unit) {
    val haptics = LocalHapticFeedback.current
    val accent = playingAccent()
    Row(
        Modifier.fillMaxWidth().padding(horizontal = 8.dp).clip(RoundedCornerShape(10.dp))
            .background(if (playing) accent.copy(alpha = 0.1f) else Color.Transparent)
            .combinedClickable(onClick = onClick, onLongClick = onHold?.let { { haptics.performHapticFeedback(HapticFeedbackType.LongPress); it(track) } })
            .padding(horizontal = 12.dp, vertical = 10.dp),
        verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(14.dp),
    ) {
        Text(number.toString(), Modifier.width(20.dp), textAlign = TextAlign.Center, style = t(13, if (playing) W600 else W400, if (playing) accent else tokens.muted))
        Column(Modifier.weight(1f)) {
            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                Text(track.title, maxLines = 1, overflow = TextOverflow.Ellipsis, style = t(15, if (playing) W600 else W500, if (playing) accent else tokens.text), modifier = Modifier.weight(1f, fill = false))
                if (downloaded) Icon(Icons.Outlined.Download, stringResource(R.string.downloaded), Modifier.size(12.dp), tint = tokens.ok)
            }
            if (track.artist.isNotEmpty() && albumArtist.isNotEmpty() && track.artist != albumArtist) {
                Text(track.artist, maxLines = 1, overflow = TextOverflow.Ellipsis, style = t(12, W400, tokens.muted))
            }
        }
        if (playing) Bars(accent)
        Text(trackTime(track.runtimeSecs), Modifier.width(36.dp), textAlign = TextAlign.End, style = t(13, W400, if (playing) tokens.tertiary else tokens.muted))
    }
}

/** The three bars that mark the playing row. */
@Composable
fun Bars(colour: Color) {
    Row(Modifier.height(14.dp), horizontalArrangement = Arrangement.spacedBy(2.5.dp), verticalAlignment = Alignment.Bottom) {
        for (h in listOf(8, 13, 10)) Box(Modifier.width(3.dp).height(h.dp).clip(RoundedCornerShape(1.dp)).background(colour))
    }
}

/** A 1 px line in the hairline colour. */
@Composable
fun Hairline(modifier: Modifier = Modifier) {
    Box(modifier.fillMaxWidth().height(1.dp).background(tokens.hairline))
}

/** The slim bar: four icons over small labels, the active one in the accent, no pill. */
@Composable
fun BottomBar(active: String, onSelect: (String) -> Unit) {
    val accent = playingAccent()
    val tabs = listOf(
        Triple("listen", R.string.nav_listen, Icons.Outlined.Headphones),
        Triple("library", R.string.nav_library, Icons.Outlined.LibraryMusic),
        Triple("search", R.string.nav_search, Icons.Outlined.Search),
        Triple("sync", R.string.nav_sync, Icons.Outlined.Sync),
    )
    Column(Modifier.fillMaxWidth().background(tokens.bg)) {
        Hairline()
        Row(Modifier.fillMaxWidth().navigationBarsPadding().padding(top = 10.dp, bottom = 10.dp, start = 8.dp, end = 8.dp), horizontalArrangement = Arrangement.SpaceAround) {
            for ((route, label, icon) in tabs) {
                val on = route == active
                Column(
                    Modifier.width(64.dp).clickable(interactionSource = remember { MutableInteractionSource() }, indication = null) { onSelect(route) },
                    horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(4.dp),
                ) {
                    Icon(icon, null, Modifier.size(24.dp), tint = if (on) accent else tokens.muted)
                    Text(stringResource(label), style = t(11, if (on) W600 else W500, if (on) accent else tokens.muted))
                }
            }
        }
    }
}

/** The floating strip above the bar: cover, title, artist, pause and next, a hairline of progress. Tap or swipe up opens the screen; sideways is next and previous. */
@Composable
fun MiniBar(api: CinevaApi, track: Item, playing: Boolean, fraction: Float, onOpen: () -> Unit, onToggle: () -> Unit, onNext: () -> Unit, onPrevious: () -> Unit) {
    val accent = playingAccent()
    Box(
        Modifier.fillMaxWidth().padding(start = 10.dp, end = 10.dp, bottom = 8.dp)
            .shadow(12.dp, RoundedCornerShape(12.dp), clip = false, ambientColor = Color.Black.copy(alpha = 0.4f), spotColor = Color.Black.copy(alpha = 0.4f))
            .clip(RoundedCornerShape(12.dp)).background(tokens.strip)
            .clickable(onClick = onOpen)
            .pointerInput(Unit) {
                var dy = 0f
                detectVerticalDragGestures(onDragStart = { dy = 0f }, onDragEnd = { if (dy < -50f) onOpen() }) { _, d -> dy += d }
            }
            .pointerInput(Unit) {
                var dx = 0f
                detectHorizontalDragGestures(onDragStart = { dx = 0f }, onDragEnd = { if (dx < -70f) onNext() else if (dx > 70f) onPrevious() }) { _, d -> dx += d }
            },
    ) {
        Row(Modifier.fillMaxWidth().padding(start = 10.dp, end = 10.dp, top = 8.dp, bottom = 10.dp), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
            Art(api, track, Modifier.size(40.dp), radius = 6.dp, letterSize = 14)
            Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(1.dp)) {
                Text(track.title, maxLines = 1, overflow = TextOverflow.Ellipsis, style = t(13, W600, tokens.text))
                Text(track.artist, maxLines = 1, overflow = TextOverflow.Ellipsis, style = t(12, W400, tokens.muted))
            }
            Row(horizontalArrangement = Arrangement.spacedBy(4.dp)) {
                Tap(if (playing) Icons.Filled.Pause else Icons.Filled.PlayArrow, stringResource(if (playing) R.string.pause else R.string.play), size = 40.dp, iconSize = 22.dp, onClick = onToggle)
                Tap(Icons.Filled.SkipNext, stringResource(R.string.next), size = 40.dp, iconSize = 22.dp, onClick = onNext)
            }
        }
        Box(Modifier.align(Alignment.BottomStart).fillMaxWidth().height(2.dp).background(Color(0xFF2A3140))) {
            Box(Modifier.fillMaxWidth(fraction.coerceIn(0f, 1f)).height(2.dp).background(accent))
        }
    }
}

/** A thin seek bar: 4 dp track, the played part in the accent, a 12 dp white thumb. Drag to seek. */
@Composable
fun SeekBar(fraction: Float, onSeek: (Float) -> Unit, modifier: Modifier = Modifier) {
    val accent = playingAccent()
    var dragging by remember { mutableStateOf(false) }
    var dragFraction by remember { mutableFloatStateOf(0f) }
    val shown = if (dragging) dragFraction else fraction.coerceIn(0f, 1f)
    val track = tokens.text.copy(alpha = 0.18f)
    val thumb = Color(0xFFE8EBF0)
    Box(
        modifier.fillMaxWidth().height(24.dp)
            .pointerInput(Unit) {
                detectTapGestures { pos -> onSeek((pos.x / size.width).coerceIn(0f, 1f)) }
            }
            .pointerInput(Unit) {
                detectHorizontalDragGestures(
                    onDragStart = { pos -> dragging = true; dragFraction = (pos.x / size.width).coerceIn(0f, 1f) },
                    onDragEnd = { dragging = false; onSeek(dragFraction) },
                    onDragCancel = { dragging = false },
                ) { change, _ -> dragFraction = (change.position.x / size.width).coerceIn(0f, 1f) }
            }
            .drawBehind {
                val y = size.height / 2
                val h = 4.dp.toPx()
                drawLine(track, Offset(0f, y), Offset(size.width, y), h, StrokeCap.Round)
                val x = size.width * shown
                if (x > 0f) drawLine(accent, Offset(0f, y), Offset(x, y), h, StrokeCap.Round)
                drawCircle(Color.Black.copy(alpha = 0.3f), 7.dp.toPx(), Offset(x, y + 1.dp.toPx()))
                drawCircle(thumb, 6.dp.toPx(), Offset(x, y))
            },
    )
}

/** A field as the mockups draw one: 44 high, rounded 12, on the panel colour. */
@Composable
fun Field(
    value: String, onChange: (String) -> Unit, placeholder: String, modifier: Modifier = Modifier, leading: (@Composable () -> Unit)? = null,
    trailing: (@Composable () -> Unit)? = null, password: Boolean = false, keyboard: KeyboardOptions = KeyboardOptions.Default, label: String? = null,
    /** Enter, when the field means a choice: adds to the first match, saves the name. */
    onDone: (() -> Unit)? = null,
) {
    Column(modifier) {
        if (label != null) Text(label, Modifier.padding(start = 4.dp, bottom = 6.dp), style = t(12, W600, tokens.muted))
        Row(
            Modifier.fillMaxWidth().height(44.dp).clip(RoundedCornerShape(12.dp)).background(tokens.panel).padding(horizontal = 14.dp),
            verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(10.dp),
        ) {
            leading?.invoke()
            Box(Modifier.weight(1f), contentAlignment = Alignment.CenterStart) {
                if (value.isEmpty()) Text(placeholder, style = t(16, W400, tokens.muted), maxLines = 1)
                BasicTextField(
                    value = value, onValueChange = onChange, singleLine = true,
                    textStyle = t(16, W400, tokens.text), cursorBrush = SolidColor(playingAccent()),
                    visualTransformation = if (password) PasswordVisualTransformation() else VisualTransformation.None,
                    keyboardOptions = if (onDone != null) keyboard.copy(imeAction = ImeAction.Done) else keyboard,
                    keyboardActions = KeyboardActions(onDone = { onDone?.invoke() }),
                    modifier = Modifier.fillMaxWidth(),
                )
            }
            trailing?.invoke()
        }
    }
}

/** A sheet rising from the bottom over a scrim, Cineva's own rather than Material's. */
@Composable
fun BoxScope.Sheet(visible: Boolean, onDismiss: () -> Unit, content: @Composable () -> Unit) {
    AnimatedVisibility(visible, Modifier.matchParentSize(), enter = fadeIn(tween(160)), exit = fadeOut(tween(160))) {
        Box(Modifier.fillMaxSize().background(Color.Black.copy(alpha = 0.55f)).clickable(interactionSource = remember { MutableInteractionSource() }, indication = null, onClick = onDismiss))
    }
    AnimatedVisibility(
        visible, Modifier.align(Alignment.BottomCenter),
        enter = slideInVertically(tween(260)) { it }, exit = slideOutVertically(tween(220)) { it },
    ) {
        Column(
            Modifier.fillMaxWidth().clip(RoundedCornerShape(topStart = 18.dp, topEnd = 18.dp)).background(tokens.strip)
                .clickable(interactionSource = remember { MutableInteractionSource() }, indication = null) {}
                .navigationBarsPadding().padding(bottom = 12.dp),
        ) {
            Box(Modifier.align(Alignment.CenterHorizontally).padding(top = 10.dp, bottom = 6.dp).size(36.dp, 4.dp).clip(RoundedCornerShape(2.dp)).background(tokens.text.copy(alpha = 0.25f)))
            content()
        }
    }
}

/** A plain word that acts: "Cancel", "All", "Back". */
@Composable
fun Word(text: String, colour: Color = playingAccent(), onClick: () -> Unit) {
    Text(text, Modifier.clickable(onClick = onClick).padding(6.dp), style = t(15, W600, colour))
}

@Composable
fun stringResource(id: Int): String = androidx.compose.ui.res.stringResource(id)

@Composable
fun stringResource(id: Int, vararg args: Any): String = androidx.compose.ui.res.stringResource(id, *args)
