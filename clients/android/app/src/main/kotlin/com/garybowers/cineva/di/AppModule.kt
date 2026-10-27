package com.garybowers.cineva.di

import android.content.Context
import coil.ImageLoader
import coil.decode.SvgDecoder
import com.garybowers.cineva.api.CinevaApi
import com.garybowers.cineva.api.SessionStore
import com.garybowers.cineva.data.AppPrefs
import com.garybowers.cineva.data.Ratings
import com.garybowers.cineva.data.ThemeStore
import com.garybowers.cineva.data.Toasts
import com.garybowers.cineva.player.PlayDecisions
import com.garybowers.cineva.player.PlayerRepository
import com.garybowers.cineva.player.Spectrum
import com.garybowers.cineva.sync.EventsClient
import com.garybowers.cineva.ui.theme.ArtColourCache
import com.garybowers.cineva.ui.theme.artColourCache
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.android.qualifiers.ApplicationContext
import dagger.hilt.components.SingletonComponent
import okhttp3.OkHttpClient
import java.util.concurrent.TimeUnit
import javax.inject.Singleton

/** The app's singletons: one session, one HTTP client with its bearer, one API, one player. */
@Module
@InstallIn(SingletonComponent::class)
object AppModule {

    @Provides @Singleton
    fun sessionStore(@ApplicationContext context: Context): SessionStore = SessionStore(context)

    @Provides @Singleton
    fun httpClient(sessions: SessionStore): OkHttpClient = OkHttpClient.Builder()
        .addInterceptor(CinevaApi.authInterceptor(sessions))
        .connectTimeout(15, TimeUnit.SECONDS)
        .readTimeout(60, TimeUnit.SECONDS)
        .build()

    @Provides @Singleton
    fun api(sessions: SessionStore, client: OkHttpClient): CinevaApi = CinevaApi(sessions, client)

    @Provides @Singleton
    fun decisions(): PlayDecisions = PlayDecisions()

    @Provides @Singleton
    fun prefs(@ApplicationContext context: Context): AppPrefs = AppPrefs(context)

    @Provides @Singleton
    fun themes(@ApplicationContext context: Context, api: CinevaApi): ThemeStore = ThemeStore(context, api)

    @Provides @Singleton
    fun spectrum(): Spectrum = Spectrum()

    @Provides @Singleton
    fun toasts(): Toasts = Toasts()

    @Provides @Singleton
    fun ratings(api: CinevaApi): Ratings = Ratings(api)

    @Provides @Singleton
    fun events(api: CinevaApi, client: OkHttpClient): EventsClient = EventsClient(api, client)

    @Provides @Singleton
    fun artColours(@ApplicationContext context: Context, loader: ImageLoader): ArtColourCache = artColourCache(context, loader)

    @Provides @Singleton
    fun player(
        @ApplicationContext context: Context, api: CinevaApi, decisions: PlayDecisions, events: EventsClient,
        sessions: SessionStore, colours: ArtColourCache, ratings: Ratings, themes: ThemeStore,
    ): PlayerRepository = PlayerRepository(context, api, decisions, events, sessions, colours, ratings, themes)

    /** Artwork through the app's HTTP client (so the bearer rides along), with SVG for the themes' background pictures. */
    @Provides @Singleton
    fun imageLoader(@ApplicationContext context: Context, client: OkHttpClient): ImageLoader =
        ImageLoader.Builder(context).okHttpClient(client).crossfade(true).components { add(SvgDecoder.Factory()) }.build()
}
