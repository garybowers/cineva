package com.garybowers.cineva.ui.theme

import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow

/**
 * The accent colour of each cover, remembered by its address so an album
 * is worked out once however many of its tracks play, and never worked
 * out twice at once. `extract` turns a cover's address into an ARGB
 * colour, or null when it has none; the Android side supplies it from
 * the palette library, and tests supply their own.
 */
class ArtColourCache(private val extract: suspend (String) -> Int?) {
    private val known = HashMap<String, Int?>()
    private val inFlight = HashMap<String, CompletableDeferred<Int?>>()
    private val lock = Any()
    private val _current = MutableStateFlow<Int?>(null)

    /** The colour of whatever cover was asked for last, for the screens. */
    val current: StateFlow<Int?> = _current

    var extractions = 0
        private set

    suspend fun colourFor(cover: String?): Int? {
        if (cover.isNullOrEmpty()) return null
        val waiting: CompletableDeferred<Int?>
        var other: CompletableDeferred<Int?>? = null
        synchronized(lock) {
            if (known.containsKey(cover)) return known[cover]
            other = inFlight[cover]
            waiting = other ?: CompletableDeferred<Int?>().also { inFlight[cover] = it }
        }
        other?.let { return it.await() }
        val colour = try {
            extractions++
            extract(cover)
        } catch (e: Exception) {
            null
        }
        synchronized(lock) {
            known[cover] = colour
            inFlight.remove(cover)
        }
        waiting.complete(colour)
        return colour
    }

    /** Works out the cover's colour and makes it the current one. */
    suspend fun select(cover: String?) {
        _current.value = colourFor(cover)
    }

    fun clear() { _current.value = null }
}
