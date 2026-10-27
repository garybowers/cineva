package com.garybowers.cineva

import android.app.Application
import coil.ImageLoader
import coil.ImageLoaderFactory
import dagger.hilt.android.HiltAndroidApp
import javax.inject.Inject

@HiltAndroidApp
class CinevaApp : Application(), ImageLoaderFactory {
    @Inject lateinit var loader: ImageLoader

    /** Coil fetches artwork through the app's HTTP client, so the server's bearer rides along. */
    override fun newImageLoader(): ImageLoader = loader
}
