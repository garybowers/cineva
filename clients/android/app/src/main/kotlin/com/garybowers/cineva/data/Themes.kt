package com.garybowers.cineva.data

import android.content.Context
import android.content.SharedPreferences
import android.util.Log
import com.garybowers.cineva.api.CinevaApi
import com.garybowers.cineva.api.ThemeInfo
import com.garybowers.cineva.api.Occasion
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch
import kotlinx.serialization.Serializable
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.Json

/**
 * The theme comes from the server, so a person's Cineva theme follows
 * them to the phone (server/docs/themes.md): the list from GET /themes,
 * their choice from the `theme` preference, and a holiday's theme while
 * one is on and `holiday-themes` is on and this year's run has not been
 * set aside, as the web client decides it. What was painted last is
 * cached, so the first frame after a cold start is already the right
 * look; Midnight's tokens are only the fallback before any fetch.
 */
@Serializable
data class ThemeState(
    val themes: List<ThemeInfo> = emptyList(),
    /** The user's own choice, the `theme` preference. */
    val chosen: String = "midnight",
    val occasion: Occasion? = null,
    val holidayThemes: Boolean = true,
    val holidaySkip: String = "",
    /** The `music-autoplay` preference: keep playing when the queue ends (server/docs/mix.md). Kept here with the other server preferences the app reads. */
    val autoplay: Boolean = true,
) {
    /** The theme to paint now. */
    val effective: ThemeInfo? get() = effectiveTheme(themes, chosen, occasion, holidayThemes, holidaySkip)
}

/** This year's run of a holiday, as the web client stores a skip: "halloween-2026". */
fun holidayKey(o: Occasion): String = o.id + "-" + o.until.take(4)

/** The web client's rule, kept in step: the holiday's theme while it is on and not set aside, else the choice, else the default. */
fun effectiveTheme(themes: List<ThemeInfo>, chosen: String, occasion: Occasion?, holidayThemes: Boolean, holidaySkip: String): ThemeInfo? {
    if (occasion != null && occasion.themeId.isNotEmpty() && holidayThemes && holidaySkip != holidayKey(occasion)) {
        themes.firstOrNull { it.id == occasion.themeId }?.let { return it }
    }
    return themes.firstOrNull { it.id == chosen } ?: themes.firstOrNull { it.id == "midnight" } ?: themes.firstOrNull()
}

class ThemeStore(context: Context, private val api: CinevaApi) {
    private val prefs: SharedPreferences = context.getSharedPreferences("cineva-theme", Context.MODE_PRIVATE)
    private val json = Json { ignoreUnknownKeys = true; explicitNulls = false }
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main)
    private val _state = MutableStateFlow(read())
    val state: StateFlow<ThemeState> = _state

    /** Fetches the themes, the holiday and the user's choice, and keeps the result for the next start. */
    fun refresh() {
        scope.launch {
            val themes = runCatching { api.themes() }.getOrElse { e -> Log.w(TAG, "themes failed", e); return@launch }
            val occasion = runCatching { api.occasion() }.getOrNull()
            val p = runCatching { api.myPrefs() }.getOrNull()
            val next = _state.value.copy(
                themes = themes, occasion = occasion,
                chosen = p?.value("theme") ?: _state.value.chosen,
                holidayThemes = (p?.value("holiday-themes") ?: "on") != "off",
                holidaySkip = p?.value("holiday-skip") ?: "",
                autoplay = (p?.value("music-autoplay") ?: "on") != "off",
            )
            _state.value = next
            write(next)
        }
    }

    /** Remembers a theme as the user's choice, on the server as the web client does. */
    fun choose(id: String) {
        val next = _state.value.copy(chosen = id)
        _state.value = next
        write(next)
        scope.launch { runCatching { api.setPref("theme", id) }.onFailure { Log.w(TAG, "saving the theme failed", it) } }
    }

    /** Keep playing when the queue ends: saved on the server as the user's preference. */
    fun setAutoplay(on: Boolean) {
        val next = _state.value.copy(autoplay = on)
        _state.value = next
        write(next)
        scope.launch { runCatching { api.setPref("music-autoplay", if (on) "on" else "off") }.onFailure { Log.w(TAG, "saving autoplay failed", it) } }
    }

    /** Forgets the server's themes at sign-out; Midnight until the next sign-in. */
    fun clear() {
        _state.value = ThemeState()
        prefs.edit().remove("state").apply()
    }

    private fun read(): ThemeState = runCatching {
        prefs.getString("state", null)?.let { json.decodeFromString(ThemeState.serializer(), it) }
    }.getOrNull() ?: ThemeState()

    private fun write(s: ThemeState) {
        runCatching { prefs.edit().putString("state", json.encodeToString(ThemeState.serializer(), s)).apply() }
    }

    companion object {
        private const val TAG = "cineva.themes"
        /** Only what matters is kept; the list could hold a household's custom themes. */
        @Suppress("unused") private val listSerializer = ListSerializer(ThemeInfo.serializer())
    }
}
