package com.garybowers.cineva.ui.theme

import android.content.Context
import android.graphics.Bitmap
import androidx.palette.graphics.Palette
import coil.ImageLoader
import coil.request.ImageRequest
import coil.request.SuccessResult
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

/** Reads a cover through the app's image loader and picks its accent with the palette library. */
fun artColourCache(context: Context, loader: ImageLoader): ArtColourCache = ArtColourCache { url ->
    withContext(Dispatchers.IO) {
        val req = ImageRequest.Builder(context).data(url).allowHardware(false).size(160).build()
        val res = loader.execute(req) as? SuccessResult ?: return@withContext null
        val bmp = (res.drawable as? android.graphics.drawable.BitmapDrawable)?.bitmap ?: return@withContext null
        pickAccent(bmp)
    }
}

/**
 * The swatch that reads as the cover's colour: vibrant first, then muted,
 * then whatever dominates, skipping anything too dark or too washed out
 * to carry controls on a dark screen.
 */
fun pickAccent(bitmap: Bitmap): Int? {
    val p = Palette.from(bitmap).maximumColorCount(24).generate()
    val candidates = listOfNotNull(p.vibrantSwatch, p.lightVibrantSwatch, p.darkVibrantSwatch, p.mutedSwatch, p.lightMutedSwatch, p.dominantSwatch)
    val chosen = candidates.firstOrNull { sw ->
        val hsl = sw.hsl
        hsl[1] > 0.25f && hsl[2] in 0.3f..0.75f
    } ?: candidates.firstOrNull() ?: return null
    return readable(chosen.hsl)
}

/**
 * The cover's hue, lifted until it reads on the dark ground: a Play pill,
 * an artist name and a playing row are drawn in this colour, so a dark
 * brown picked from a brown cover (AC/DC) must come out as a light brown,
 * not vanish. Lightness at least 0.55 and saturation at least 0.35, the
 * hue untouched so it still looks like the cover.
 */
fun readable(hsl: FloatArray): Int {
    val fixed = floatArrayOf(hsl[0], maxOf(hsl[1], 0.35f), hsl[2].coerceIn(0.55f, 0.78f))
    return androidx.core.graphics.ColorUtils.HSLToColor(fixed)
}
