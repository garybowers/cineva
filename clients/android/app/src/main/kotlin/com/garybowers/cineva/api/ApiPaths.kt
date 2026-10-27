package com.garybowers.cineva.api

/**
 * Every /api/v1 path the app uses, in the spec's own spelling, so a JVM
 * test can check each one exists in server/docs/openapi.yaml: the spec is
 * the contract, and a path that drifts fails the build rather than the
 * phone.
 */
object ApiPaths {
    const val ME = "/auth/me"
    const val LOGIN = "/auth/login"
    const val LOGOUT = "/auth/logout"
    const val LIBRARIES = "/libraries"
    const val ARTISTS = "/libraries/{id}/artists"
    const val ALBUMS = "/libraries/{id}/albums"
    const val TRACKS = "/libraries/{id}/tracks"
    const val GENRES = "/libraries/{id}/genres"
    const val LIBRARY_SEARCH = "/libraries/{id}/search"
    const val LISTENING = "/libraries/{id}/listening"
    const val ITEM = "/items/{id}"
    const val ITEM_ARTIST = "/items/{id}/artist"
    const val ITEM_TRACKS = "/items/{id}/tracks"
    const val ITEM_LYRICS = "/items/{id}/lyrics"
    const val ITEM_MIX = "/items/{id}/mix"
    const val PLAY = "/items/{id}/play"
    const val PROGRESS = "/items/{id}/progress"
    const val PLAYBACK = "/items/{id}/playback"
    const val RATING = "/items/{id}/rating"
    const val MY_QUEUE = "/auth/sessions/current/queue"
    const val DEVICES = "/auth/sessions"
    const val DEVICE_TAKEOVER = "/auth/sessions/{id}/takeover"
    const val DEVICE_PLAY = "/auth/sessions/{id}/play"
    const val EVENTS = "/events"
    const val PLAYLISTS = "/playlists"
    const val PLAYLIST = "/playlists/{id}"
    const val PLAYLIST_ITEMS = "/playlists/{id}/items"
    const val PLAYLIST_ITEMS_LAST = "/playlists/{id}/items/last"
    const val THEMES = "/themes"
    const val OCCASION = "/themes/occasion"
    const val MY_PREFS = "/auth/me/prefs"

    val all = listOf(
        ME, LOGIN, LOGOUT, LIBRARIES, ARTISTS, ALBUMS, TRACKS, GENRES, LIBRARY_SEARCH, LISTENING,
        ITEM, ITEM_ARTIST, ITEM_TRACKS, ITEM_LYRICS, ITEM_MIX, PLAY, PROGRESS, PLAYBACK, RATING, MY_QUEUE,
        DEVICES, DEVICE_TAKEOVER, DEVICE_PLAY, EVENTS, PLAYLISTS, PLAYLIST, PLAYLIST_ITEMS, PLAYLIST_ITEMS_LAST,
        THEMES, OCCASION, MY_PREFS,
    )

    /** Fills {id} in a path template. */
    fun fill(template: String, id: String): String = template.replace("{id}", id)
}
