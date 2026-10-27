package com.garybowers.cineva

import android.Manifest
import android.content.pm.PackageManager
import android.os.Build
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.graphics.Color
import androidx.core.content.ContextCompat
import com.garybowers.cineva.data.ThemeStore
import com.garybowers.cineva.ui.CinevaNavHost
import com.garybowers.cineva.ui.theme.ArtColourCache
import com.garybowers.cineva.ui.theme.CinevaTheme
import com.garybowers.cineva.ui.theme.Midnight
import com.garybowers.cineva.ui.theme.toTokens
import dagger.hilt.android.AndroidEntryPoint
import javax.inject.Inject

@AndroidEntryPoint
class MainActivity : ComponentActivity() {
    private val askNotifications = registerForActivityResult(ActivityResultContracts.RequestPermission()) {}
    @Inject lateinit var themes: ThemeStore
    @Inject lateinit var artColours: ArtColourCache

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        // The playback notification is a media notification, which Android
        // shows anyway; asking makes the controls appear on every phone.
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU &&
            ContextCompat.checkSelfPermission(this, Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED
        ) {
            askNotifications.launch(Manifest.permission.POST_NOTIFICATIONS)
        }
        setContent {
            val theme by themes.state.collectAsState()
            val art by artColours.current.collectAsState()
            val tokens = remember(theme) { theme.effective?.toTokens() ?: Midnight }
            CinevaTheme(tokens = tokens, artColour = art?.let { Color(it) }) {
                CinevaNavHost()
            }
        }
    }
}
