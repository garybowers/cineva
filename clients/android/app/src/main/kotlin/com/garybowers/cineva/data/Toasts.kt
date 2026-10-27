package com.garybowers.cineva.data

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow

/** A short message with one action: "Added to Driving · Undo". Shown for two seconds, over every screen. */
data class Toast(val text: String, val action: String? = null, val onAction: (() -> Unit)? = null, val stamp: Long = System.nanoTime())

class Toasts {
    private val _current = MutableStateFlow<Toast?>(null)
    val current: StateFlow<Toast?> = _current

    fun show(text: String, action: String? = null, onAction: (() -> Unit)? = null) {
        _current.value = Toast(text, action, onAction)
    }

    fun dismiss(t: Toast) { if (_current.value === t) _current.value = null }
}
