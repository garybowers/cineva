package com.garybowers.cineva.player

import android.content.Context
import android.net.ConnectivityManager
import android.net.NetworkCapabilities
import android.net.Uri
import androidx.annotation.OptIn
import androidx.media3.common.AudioAttributes
import androidx.media3.common.C
import androidx.media3.common.util.UnstableApi
import androidx.media3.datasource.DataSpec
import androidx.media3.datasource.ResolvingDataSource
import androidx.media3.datasource.okhttp.OkHttpDataSource
import androidx.media3.exoplayer.DefaultRenderersFactory
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.audio.AudioSink
import androidx.media3.exoplayer.audio.DefaultAudioSink
import androidx.media3.exoplayer.audio.TeeAudioProcessor
import androidx.media3.exoplayer.source.DefaultMediaSourceFactory
import androidx.media3.session.MediaSession
import androidx.media3.session.MediaSessionService
import com.garybowers.cineva.api.Capabilities
import com.garybowers.cineva.api.CinevaApi
import com.garybowers.cineva.api.PlayInfo
import com.garybowers.cineva.data.AppPrefs
import dagger.hilt.android.AndroidEntryPoint
import okhttp3.OkHttpClient
import java.util.concurrent.ConcurrentHashMap
import javax.inject.Inject

/**
 * The player, in a media session service so playback carries on in the
 * background with the notification, lock screen, Bluetooth buttons and
 * audio focus handled by Media3.
 *
 * Every queued track has the URI cineva://item/<id>; the data source
 * resolves it when the player opens it, by asking the server what to
 * play (POST /items/{id}/play with this phone's capabilities), so the
 * server's quality preference and any admin lock decide the stream, and
 * the play is recorded at the moment it starts rather than when it was
 * queued.
 *
 * The audio sink carries a tee for the spectrum behind the cover; audio
 * offload stays off so the tee sees every buffer.
 */
@AndroidEntryPoint
class PlaybackService : MediaSessionService() {

    @Inject lateinit var api: CinevaApi
    @Inject lateinit var http: OkHttpClient
    @Inject lateinit var decisions: PlayDecisions
    @Inject lateinit var prefs: AppPrefs
    @Inject lateinit var spectrum: Spectrum

    private var session: MediaSession? = null

    @OptIn(UnstableApi::class)
    override fun onCreate() {
        super.onCreate()
        val upstream = OkHttpDataSource.Factory(http)
        val resolving = ResolvingDataSource.Factory(upstream, Resolver())
        val renderers = object : DefaultRenderersFactory(this) {
            override fun buildAudioSink(context: Context, enableFloatOutput: Boolean, enableAudioTrackPlaybackParams: Boolean): AudioSink =
                DefaultAudioSink.Builder(context)
                    .setEnableFloatOutput(enableFloatOutput)
                    .setEnableAudioTrackPlaybackParams(enableAudioTrackPlaybackParams)
                    .setAudioProcessors(arrayOf(TeeAudioProcessor(spectrum.sink)))
                    .build()
        }
        val player = ExoPlayer.Builder(this, renderers)
            .setMediaSourceFactory(DefaultMediaSourceFactory(this).setDataSourceFactory(resolving))
            .setAudioAttributes(
                AudioAttributes.Builder().setUsage(C.USAGE_MEDIA).setContentType(C.AUDIO_CONTENT_TYPE_MUSIC).build(),
                true,
            )
            .setHandleAudioBecomingNoisy(true)
            .build()
        session = MediaSession.Builder(this, player).build()
    }

    override fun onGetSession(controllerInfo: MediaSession.ControllerInfo): MediaSession? = session

    override fun onDestroy() {
        session?.run {
            player.release()
            release()
            session = null
        }
        super.onDestroy()
    }

    @OptIn(UnstableApi::class)
    private inner class Resolver : ResolvingDataSource.Resolver {
        override fun resolveDataSpec(dataSpec: DataSpec): DataSpec {
            val uri = dataSpec.uri
            if (uri.scheme != SCHEME) return dataSpec
            val id = uri.lastPathSegment ?: return dataSpec
            val info = api.playBlocking(id, Capabilities(quality = qualityForNetwork()))
            decisions.remember(id, info)
            return dataSpec.withUri(Uri.parse(api.absolute(info.url)))
        }
    }

    /** The quality the phone asks for on the network it is on now. */
    private fun qualityForNetwork(): String {
        val cm = getSystemService(Context.CONNECTIVITY_SERVICE) as? ConnectivityManager ?: return prefs.current.wifiQuality
        val caps = cm.getNetworkCapabilities(cm.activeNetwork)
        val mobile = caps != null && caps.hasTransport(NetworkCapabilities.TRANSPORT_CELLULAR) && !caps.hasTransport(NetworkCapabilities.TRANSPORT_WIFI)
        return if (mobile) prefs.current.mobileQuality else prefs.current.wifiQuality
    }

    companion object {
        const val SCHEME = "cineva"
        fun uriFor(itemId: String): Uri = Uri.parse("$SCHEME://item/$itemId")
    }
}

/** What the server decided for each track played, for the quality badge. */
class PlayDecisions {
    private val byItem = ConcurrentHashMap<String, PlayInfo>()
    fun remember(id: String, info: PlayInfo) { byItem[id] = info }
    fun forItem(id: String): PlayInfo? = byItem[id]
}
