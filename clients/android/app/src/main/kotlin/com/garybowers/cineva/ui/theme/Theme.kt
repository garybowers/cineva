package com.garybowers.cineva.ui.theme

import androidx.compose.material3.ColorScheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Typography
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.compositionLocalOf
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.luminance
import androidx.compose.ui.text.ExperimentalTextApi
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.Font
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontVariation
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.sp
import com.garybowers.cineva.R
import com.garybowers.cineva.api.ThemeInfo

// The app's colours are Cineva's own theme tokens, fetched from the server
// (data/Themes.kt) and mapped onto a Material colour scheme as plumbing,
// so the phone and the web client are one Cineva. Midnight is the fallback
// before the first fetch. The art colour (the playing cover's accent) sits
// beside them in a composition local and replaces the accent only while
// music plays.

/** The token set every Cineva theme defines (server/docs/themes.md). */
data class Tokens(
    val bg: Color, val panel: Color, val border: Color, val text: Color, val muted: Color,
    val accent: Color, val accent2: Color, val onAccent: Color, val ok: Color, val err: Color,
    val dark: Boolean = true,
    /** The theme's background picture, server-relative, and how strongly bg covers it (0 to 1). */
    val background: String = "", val dim: Float = 0.7f,
)

val Midnight = Tokens(
    bg = Color(0xFF0E1116), panel = Color(0xFF161B23), border = Color(0xFF232A35), text = Color(0xFFE8EBF0),
    muted = Color(0xFF8A93A3), accent = Color(0xFF7C5CFF), accent2 = Color(0xFF4DC3FF), onAccent = Color.White,
    ok = Color(0xFF3ECF8E), err = Color(0xFFFF6B6B),
)

/** Reads "#rrggbb" or "#rgb"; null for anything else. */
fun parseHex(s: String?): Color? {
    val h = s?.trim()?.removePrefix("#") ?: return null
    val hex = when (h.length) { 3 -> h.map { "$it$it" }.joinToString(""); 6 -> h; 8 -> h.drop(2); else -> return null }
    val v = hex.toLongOrNull(16) ?: return null
    return Color(0xFF000000 or v)
}

/** A theme's tokens, Midnight's where one is missing. */
fun ThemeInfo.toTokens(): Tokens {
    val t = tokens
    fun c(key: String, fallback: Color) = parseHex(t[key]) ?: fallback
    return Tokens(
        bg = c("bg", Midnight.bg), panel = c("panel", Midnight.panel), border = c("border", Midnight.border),
        text = c("text", Midnight.text), muted = c("muted", Midnight.muted), accent = c("accent", Midnight.accent),
        accent2 = c("accent-2", Midnight.accent2), onAccent = c("on-accent", Midnight.onAccent),
        ok = c("ok", Midnight.ok), err = c("err", Midnight.err),
        dark = appearance != "light",
        background = background.orEmpty(),
        dim = if (dim > 0) dim / 100f else (t["bg-dim"]?.toFloatOrNull() ?: 0.7f),
    )
}

fun Tokens.scheme(): ColorScheme {
    val base = if (dark) darkColorScheme() else lightColorScheme()
    return base.copy(
        primary = accent, onPrimary = onAccent, primaryContainer = accent.copy(alpha = 0.18f), onPrimaryContainer = text,
        secondary = accent2, onSecondary = onAccent, secondaryContainer = accent2.copy(alpha = 0.18f), onSecondaryContainer = text,
        tertiary = ok, onTertiary = onAccent,
        background = bg, onBackground = text,
        surface = bg, onSurface = text,
        surfaceVariant = panel, onSurfaceVariant = muted,
        surfaceContainer = panel, surfaceContainerLow = panel, surfaceContainerHigh = border, surfaceContainerHighest = border,
        surfaceContainerLowest = bg,
        outline = border, outlineVariant = border,
        error = err, onError = onAccent,
        inverseSurface = text, inverseOnSurface = bg,
    )
}

val LocalTokens = compositionLocalOf { Midnight }

/** The playing cover's accent, or null when nothing plays or the cover has none. */
val LocalArtColour = compositionLocalOf<Color?> { null }

// Inter, bundled as its variable font (res/font/inter.ttf, SIL OFL), the
// typeface the web client uses, at the weights the mockups draw.
@OptIn(ExperimentalTextApi::class)
private fun inter(weight: FontWeight) = Font(R.font.inter, weight, variationSettings = FontVariation.Settings(FontVariation.weight(weight.weight)))

val Inter = FontFamily(inter(FontWeight.Normal), inter(FontWeight.Medium), inter(FontWeight.SemiBold), inter(FontWeight.Bold), inter(FontWeight.ExtraBold))

/** Material's scale, in Inter, for the few places plumbing still reads it. */
val CinevaType = Typography(
    headlineSmall = TextStyle(fontFamily = Inter, fontWeight = FontWeight.ExtraBold, fontSize = 22.sp, lineHeight = 26.sp),
    titleMedium = TextStyle(fontFamily = Inter, fontWeight = FontWeight.SemiBold, fontSize = 16.sp, lineHeight = 20.sp),
    bodyLarge = TextStyle(fontFamily = Inter, fontWeight = FontWeight.Normal, fontSize = 16.sp, lineHeight = 22.sp),
    bodyMedium = TextStyle(fontFamily = Inter, fontWeight = FontWeight.Normal, fontSize = 14.sp, lineHeight = 19.sp),
    bodySmall = TextStyle(fontFamily = Inter, fontWeight = FontWeight.Normal, fontSize = 12.sp, lineHeight = 16.sp),
    labelLarge = TextStyle(fontFamily = Inter, fontWeight = FontWeight.SemiBold, fontSize = 14.sp, lineHeight = 18.sp),
    labelSmall = TextStyle(fontFamily = Inter, fontWeight = FontWeight.Medium, fontSize = 11.sp, lineHeight = 14.sp),
)

@Composable
fun CinevaTheme(tokens: Tokens, artColour: Color?, content: @Composable () -> Unit) {
    CompositionLocalProvider(LocalTokens provides tokens, LocalArtColour provides artColour) {
        MaterialTheme(colorScheme = tokens.scheme(), typography = CinevaType, content = content)
    }
}

/** The accent to use where music is playing: the cover's, else the theme's. */
@Composable
fun playingAccent(): Color = LocalArtColour.current ?: LocalTokens.current.accent

/** Text that reads on the playing accent. */
fun Color.onColour(): Color = if (luminance() > 0.55f) Color(0xFF0E1116) else Color.White
