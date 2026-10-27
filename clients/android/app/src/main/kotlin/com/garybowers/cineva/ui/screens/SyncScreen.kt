package com.garybowers.cineva.ui.screens

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.background
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Sync
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import com.garybowers.cineva.R

/**
 * Sync, the iPod way: a plan for what comes with you, kept in step on its
 * own. Its own stage with its own mockup (docs/mockups/Sync.dc.html);
 * until then the tab says what is coming.
 */
@Composable
fun SyncScreen(onSettings: () -> Unit, header: ShellHeaderViewModel = hiltViewModel()) {
    ThemeBackdrop(header.api) {
        Column(Modifier.fillMaxSize().statusBarsPadding()) {
            TopRow(stringResource(R.string.nav_sync), onSettings)
            Row(Modifier.fillMaxWidth().padding(start = 20.dp, end = 20.dp, top = 18.dp), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(16.dp)) {
                Box(Modifier.size(72.dp).clip(CircleShape).background(tokens.panel), contentAlignment = Alignment.Center) {
                    Icon(Icons.Outlined.Sync, null, Modifier.size(28.dp), tint = tokens.text)
                }
                Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(3.dp)) {
                    Text(stringResource(R.string.sync_title), style = t(22, W800, tokens.text))
                    Text(stringResource(R.string.sync_lead), style = t(13, W400, tokens.secondary))
                }
            }
            Text(stringResource(R.string.sync_coming), Modifier.padding(start = 20.dp, end = 20.dp, top = 24.dp), style = t(14, W400, tokens.muted))
        }
    }
}
