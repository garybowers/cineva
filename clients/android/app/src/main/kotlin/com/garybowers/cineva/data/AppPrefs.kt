package com.garybowers.cineva.data

import android.content.Context
import android.content.SharedPreferences
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow

/** What the phone keeps for itself: the stream quality on each kind of network, and the spectrum. */
data class Prefs(
    /** original, opus-128 or opus-64, used when the phone is on Wi-Fi */
    val wifiQuality: String = "original",
    /** the same on mobile data */
    val mobileQuality: String = "opus-128",
    /** the spectrum behind the cover on now playing */
    val spectrum: Boolean = true,
    /** the playlist a track was last added to, first in the picker next time */
    val lastPlaylistId: String = "",
)

/**
 * Plain preferences, read synchronously at start. Quality here is what
 * the phone asks for; the server's own `music-quality` preference and an
 * admin's lock still decide (server/docs/music.md, part 3). The theme is
 * not here: it comes from the server (Themes.kt).
 */
class AppPrefs(context: Context) {
    private val prefs: SharedPreferences = context.getSharedPreferences("cineva-prefs", Context.MODE_PRIVATE)
    private val _state = MutableStateFlow(read())
    val state: StateFlow<Prefs> = _state
    val current: Prefs get() = _state.value

    fun update(change: (Prefs) -> Prefs) {
        val next = change(_state.value)
        prefs.edit()
            .putString("wifiQuality", next.wifiQuality)
            .putString("mobileQuality", next.mobileQuality)
            .putBoolean("spectrum", next.spectrum)
            .putString("lastPlaylistId", next.lastPlaylistId)
            .apply()
        _state.value = next
    }

    private fun read() = Prefs(
        wifiQuality = prefs.getString("wifiQuality", "original") ?: "original",
        mobileQuality = prefs.getString("mobileQuality", "opus-128") ?: "opus-128",
        spectrum = prefs.getBoolean("spectrum", true),
        lastPlaylistId = prefs.getString("lastPlaylistId", "") ?: "",
    )
}
