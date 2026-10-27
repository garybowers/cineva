package com.garybowers.cineva.ui.screens

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.ArrowBackIos
import androidx.compose.material.icons.outlined.Check
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.garybowers.cineva.R
import com.garybowers.cineva.api.CinevaApi
import com.garybowers.cineva.api.SessionStore
import com.garybowers.cineva.data.AppPrefs
import com.garybowers.cineva.data.ThemeStore
import com.garybowers.cineva.player.PlayerRepository
import com.garybowers.cineva.ui.theme.onColour
import com.garybowers.cineva.ui.theme.parseHex
import com.garybowers.cineva.ui.theme.playingAccent
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.launch
import javax.inject.Inject

@HiltViewModel
class SettingsViewModel @Inject constructor(val api: CinevaApi, val sessions: SessionStore, val prefs: AppPrefs, val player: PlayerRepository, val themes: ThemeStore) : ViewModel() {
    fun signOut(then: () -> Unit) { viewModelScope.launch { player.stop(); api.signOut(); themes.clear(); then() } }
}

private val qualities = listOf("original" to R.string.quality_original, "opus-128" to R.string.quality_high, "opus-64" to R.string.quality_saver)

/** Settings, behind the avatar: quality by network, the look from the server's themes, the spectrum, the account. */
@Composable
fun SettingsScreen(onBack: () -> Unit, onSignedOut: () -> Unit, vm: SettingsViewModel = hiltViewModel()) {
    val p by vm.prefs.state.collectAsState()
    val session by vm.sessions.session.collectAsState()
    val theme by vm.themes.state.collectAsState()
    Column(Modifier.fillMaxSize().statusBarsPadding().verticalScroll(rememberScrollState()).padding(bottom = 32.dp)) {
        Row(Modifier.fillMaxWidth().padding(horizontal = 12.dp), verticalAlignment = Alignment.CenterVertically) {
            Tap(Icons.AutoMirrored.Outlined.ArrowBackIos, stringResource(R.string.back), iconSize = 20.dp, onClick = onBack)
            PageLabel(stringResource(R.string.settings), Modifier.padding(start = 4.dp))
        }

        Group(stringResource(R.string.quality_wifi), stringResource(R.string.quality_note)) {
            Choices(qualities.map { it.first to stringResource(it.second) }, p.wifiQuality) { v -> vm.prefs.update { it.copy(wifiQuality = v) } }
        }
        Group(stringResource(R.string.quality_mobile), null) {
            Choices(qualities.map { it.first to stringResource(it.second) }, p.mobileQuality) { v -> vm.prefs.update { it.copy(mobileQuality = v) } }
        }
        Group(stringResource(R.string.theme), stringResource(R.string.theme_note)) {
            if (theme.themes.isEmpty()) Text(stringResource(R.string.theme_loading), style = t(13, W400, tokens.muted))
            for (th in theme.themes) {
                val on = th.id == theme.chosen
                Row(Modifier.fillMaxWidth().clickable { vm.themes.choose(th.id) }.height(44.dp), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                    Box(Modifier.size(22.dp).clip(CircleShape).background(parseHex(th.tokens["bg"]) ?: tokens.bg), contentAlignment = Alignment.Center) {
                        Box(Modifier.size(10.dp).clip(CircleShape).background(parseHex(th.tokens["accent"]) ?: tokens.accent))
                    }
                    Text(th.name.ifEmpty { th.id }, Modifier.weight(1f), style = t(15, if (on) W600 else W500, tokens.text))
                    if (th.id == theme.occasion?.themeId && theme.effective?.id == th.id) Text(stringResource(R.string.theme_holiday_now), style = t(12, W400, tokens.muted))
                    if (on) Icon(Icons.Outlined.Check, null, Modifier.size(18.dp), tint = playingAccent())
                }
            }
        }
        Group(stringResource(R.string.spectrum), stringResource(R.string.spectrum_note)) {
            Toggle(p.spectrum) { vm.prefs.update { it.copy(spectrum = !it.spectrum) } }
        }
        Group(stringResource(R.string.autoplay), stringResource(R.string.autoplay_note)) {
            Toggle(theme.autoplay) { vm.themes.setAutoplay(!theme.autoplay) }
        }
        Group(stringResource(R.string.gapless), stringResource(R.string.gapless_note)) {}

        Text(stringResource(R.string.account).uppercase(), Modifier.padding(start = 20.dp, top = 28.dp, bottom = 8.dp), style = t(12, W600, tokens.muted, 1.0f))
        Text(session?.username ?: "", Modifier.padding(horizontal = 20.dp), style = t(16, W600, tokens.text))
        Text(session?.server ?: "", Modifier.padding(horizontal = 20.dp), style = t(13, W400, tokens.muted))
        Text(vm.sessions.deviceName, Modifier.padding(horizontal = 20.dp), style = t(13, W400, tokens.muted))
        Box(Modifier.padding(start = 20.dp, top = 14.dp)) { Pill(stringResource(R.string.sign_out)) { vm.signOut(onSignedOut) } }
    }
}

@Composable
private fun Group(title: String, note: String?, content: @Composable () -> Unit) {
    Column(Modifier.fillMaxWidth().padding(horizontal = 20.dp, vertical = 12.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
        Text(title, style = t(15, W700, tokens.text))
        if (note != null) Text(note, style = t(12, W400, tokens.muted))
        Spacer(Modifier.height(2.dp))
        content()
    }
}

/** Choices as small words in a pill track, the chosen one filled. */
@Composable
private fun Choices(options: List<Pair<String, String>>, chosen: String, onChoose: (String) -> Unit) {
    val accent = playingAccent()
    Row(Modifier.clip(RoundedCornerShape(999.dp)).background(tokens.panel).padding(3.dp), horizontalArrangement = Arrangement.spacedBy(2.dp)) {
        for ((value, label) in options) {
            val on = value == chosen
            Text(
                label,
                Modifier.clip(RoundedCornerShape(999.dp)).background(if (on) accent else androidx.compose.ui.graphics.Color.Transparent)
                    .clickable(interactionSource = remember { MutableInteractionSource() }, indication = null) { onChoose(value) }
                    .padding(horizontal = 12.dp, vertical = 6.dp),
                style = t(12, if (on) W700 else W600, if (on) accent.onColour() else tokens.muted),
            )
        }
    }
}

/** A switch as the mockups draw one: 44 by 26, the knob on the accent. */
@Composable
fun Toggle(on: Boolean, onToggle: () -> Unit) {
    val accent = playingAccent()
    Box(
        Modifier.width(44.dp).height(26.dp).clip(RoundedCornerShape(13.dp)).background(if (on) accent else tokens.border)
            .clickable(interactionSource = remember { MutableInteractionSource() }, indication = null, onClick = onToggle),
        contentAlignment = if (on) Alignment.CenterEnd else Alignment.CenterStart,
    ) {
        Box(Modifier.padding(3.dp).size(20.dp).clip(CircleShape).background(if (on) tokens.bg else tokens.muted))
    }
}
