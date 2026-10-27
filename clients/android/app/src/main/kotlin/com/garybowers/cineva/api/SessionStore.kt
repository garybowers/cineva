package com.garybowers.cineva.api

import android.content.Context
import android.content.SharedPreferences
import android.os.Build
import androidx.security.crypto.EncryptedSharedPreferences
import androidx.security.crypto.MasterKey
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import java.util.UUID

/** Where the app is signed in: the server, the bearer token and this phone's device identity. */
data class Session(val server: String, val token: String, val username: String)

/**
 * Keeps the session in encrypted preferences (the Android keystore holds
 * the master key), and this phone's stable device id, which the server
 * uses to recognise the same phone across sign-ins.
 */
class SessionStore(context: Context) {
    private val prefs: SharedPreferences = EncryptedSharedPreferences.create(
        context,
        "cineva-session",
        MasterKey.Builder(context).setKeyScheme(MasterKey.KeyScheme.AES256_GCM).build(),
        EncryptedSharedPreferences.PrefKeyEncryptionScheme.AES256_SIV,
        EncryptedSharedPreferences.PrefValueEncryptionScheme.AES256_GCM,
    )

    private val _session = MutableStateFlow(read())
    val session: StateFlow<Session?> = _session

    val current: Session? get() = _session.value

    /** A random id made once per install, as a browser's device cookie is. */
    val deviceId: String
        get() = prefs.getString("deviceId", null) ?: UUID.randomUUID().toString().also {
            prefs.edit().putString("deviceId", it).apply()
        }

    /** What the phone calls itself on the Devices page. */
    val deviceName: String get() = "${Build.MANUFACTURER} ${Build.MODEL}".trim().replaceFirstChar { it.uppercase() }

    fun save(session: Session) {
        prefs.edit()
            .putString("server", session.server)
            .putString("token", session.token)
            .putString("username", session.username)
            .apply()
        _session.value = session
    }

    fun clear() {
        prefs.edit().remove("server").remove("token").remove("username").apply()
        _session.value = null
    }

    private fun read(): Session? {
        val server = prefs.getString("server", null) ?: return null
        val token = prefs.getString("token", null) ?: return null
        return Session(server, token, prefs.getString("username", "") ?: "")
    }
}
