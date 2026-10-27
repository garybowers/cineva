package com.garybowers.cineva.player

import com.garybowers.cineva.api.Item
import com.garybowers.cineva.api.QueueMix
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class MixRulesTest {
    private fun tracks(n: Int) = (1..n).map { Item(id = "t$it", kind = "track", title = "Track $it") }
    private val mix = QueueMix(seedId = "seed", kind = "album", title = "AM", after = "t3", from = 0, basis = listOf("similar-artists"))

    @Test
    fun asksForMoreOnlyNearTheEnd() {
        assertNull(MixRules.next(Queue.fromTracks(tracks(6), 0, mix = mix), autoplay = true))
        val near = MixRules.next(Queue.fromTracks(tracks(6), 3, mix = mix), autoplay = true)
        assertEquals("seed", near?.seedId)
        assertEquals("album", near?.kind)
        assertEquals("t6", near?.after)
    }

    @Test
    fun aMixCarriesOnWhateverAutoplaySays() {
        assertEquals("seed", MixRules.next(Queue.fromTracks(tracks(3), 2, mix = mix), autoplay = false)?.seedId)
    }

    @Test
    fun anOrdinaryQueueStartsAMixFromItsLastTrackWhenWanted() {
        val q = Queue.fromTracks(tracks(3), 1)
        assertNull(MixRules.next(q, autoplay = false))
        val req = MixRules.next(q, autoplay = true)
        assertEquals("t3", req?.seedId)
        assertEquals("track", req?.kind)
        assertEquals("Track 3", req?.title)
        assertEquals("t3/t3", req?.key)
    }

    @Test
    fun repeatAndEmptyAndNonTracksAskNothing() {
        assertNull(MixRules.next(Queue.fromTracks(tracks(3), 2).cycleRepeat(), autoplay = true))
        assertNull(MixRules.next(Queue(), autoplay = true))
        val video = Queue.fromTracks(listOf(Item(id = "v", kind = "episode")), 0)
        assertNull(MixRules.next(video, autoplay = true))
    }

    @Test
    fun basisInWords() {
        assertEquals("Similar artists and what you play", MixRules.basisWords(listOf("similar-artists", "household")))
        assertEquals("Similar artists, best-known songs and the same genre", MixRules.basisWords(listOf("similar-artists", "top-songs", "genre")))
        assertEquals("From your library", MixRules.basisWords(emptyList()))
        assertEquals("The same genre", MixRules.basisWords(listOf("genre", "unknown")))
    }
}
