package com.garybowers.cineva.ui.screens

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.garybowers.cineva.R
import com.garybowers.cineva.api.CinevaApi
import com.garybowers.cineva.ui.theme.playingAccent
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.launch
import javax.inject.Inject

/** The fields live in the view model, so a rotation mid-typing keeps them. */
@HiltViewModel
class SignInViewModel @Inject constructor(private val api: CinevaApi) : ViewModel() {
    var server by mutableStateOf("")
    var username by mutableStateOf("")
    var secret by mutableStateOf("")
    var busy by mutableStateOf(false)
    var error by mutableStateOf<String?>(null)

    fun signIn(onDone: () -> Unit) {
        if (busy) return
        busy = true; error = null
        viewModelScope.launch {
            try {
                api.signIn(server, username.trim(), secret)
                onDone()
            } catch (e: Exception) {
                error = e.describe()
            } finally {
                busy = false
            }
        }
    }
}

@Composable
fun SignInScreen(onSignedIn: () -> Unit, vm: SignInViewModel = hiltViewModel()) {
    Column(
        Modifier.fillMaxSize().statusBarsPadding().verticalScroll(rememberScrollState()).imePadding().padding(horizontal = 28.dp, vertical = 40.dp),
        verticalArrangement = Arrangement.spacedBy(14.dp, alignment = Alignment.CenterVertically),
    ) {
        Text(stringResource(R.string.app_name), style = t(34, W800, playingAccent()))
        Text(stringResource(R.string.sign_in_lead), style = t(15, W400, tokens.muted))
        Spacer(Modifier.height(10.dp))
        Field(vm.server, { vm.server = it }, stringResource(R.string.server_hint), label = stringResource(R.string.server_address), keyboard = KeyboardOptions(keyboardType = KeyboardType.Uri))
        Field(vm.username, { vm.username = it }, "", label = stringResource(R.string.username))
        Field(vm.secret, { vm.secret = it }, "", label = stringResource(R.string.password_or_key), password = true, keyboard = KeyboardOptions(keyboardType = KeyboardType.Password))
        Text(stringResource(R.string.sign_in_help), style = t(12, W400, tokens.muted))
        vm.error?.let { Text(it, style = t(13, W500, tokens.err)) }
        Spacer(Modifier.height(4.dp))
        Column(Modifier.fillMaxWidth(), horizontalAlignment = Alignment.CenterHorizontally) {
            Pill(stringResource(if (vm.busy) R.string.signing_in else R.string.sign_in), filled = true, enabled = !vm.busy && vm.server.isNotBlank() && vm.username.isNotBlank() && vm.secret.isNotEmpty()) { vm.signIn(onSignedIn) }
        }
    }
}
