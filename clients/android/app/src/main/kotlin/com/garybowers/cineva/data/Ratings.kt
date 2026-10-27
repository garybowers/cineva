package com.garybowers.cineva.data

import android.util.Log
import com.garybowers.cineva.api.CinevaApi
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch

/**
 * The viewer's star ratings as the screens show them: what the server
 * said when an item was fetched, overridden by what the viewer set since
 * (sent to the server, rolled back if that fails) and by ratings made on
 * other devices that arrive over the events stream.
 */
class Ratings(private val api: CinevaApi) {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main)
    private val _overrides = MutableStateFlow<Map<String, Int>>(emptyMap())
    val overrides: StateFlow<Map<String, Int>> = _overrides

    fun of(itemId: String, fromServer: Int): Int = _overrides.value[itemId] ?: fromServer

    fun rate(itemId: String, stars: Int, was: Int) {
        set(itemId, stars)
        scope.launch {
            runCatching { api.rate(itemId, stars) }.onFailure { e ->
                Log.w("cineva.ratings", "rating failed", e)
                set(itemId, was)
            }
        }
    }

    /** A rating made elsewhere. */
    fun set(itemId: String, stars: Int) { _overrides.value = _overrides.value + (itemId to stars) }
}
