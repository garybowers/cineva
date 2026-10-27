package com.garybowers.cineva.player

import com.garybowers.cineva.api.Item
import com.garybowers.cineva.api.QueueMix
import com.garybowers.cineva.api.QueueSource
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import kotlin.random.Random

class QueueTest {
    private fun tracks(n: Int) = (1..n).map { Item(id = "t$it", title = "Track $it", trackNum = it) }

    @Test
    fun recordCarriesOrderIndexAndSource() {
        val q = Queue.fromTracks(tracks(3), 1, QueueSource("album", "a1", "AM"))
        val r = q.record(12.5)
        assertEquals(listOf("t1", "t2", "t3"), r.entries.map { it.id })
        assertEquals(listOf(0, 1, 2), r.entries.map { it.ord })
        assertEquals(1, r.index)
        assertEquals(12.5, r.positionSecs, 0.0)
        assertEquals("AM", r.source?.title)
        assertEquals("off", r.repeat)
    }

    @Test
    fun removeKeepsTheCurrentTrack() {
        val q = Queue.fromTracks(tracks(4), 2)
        assertEquals("t3", q.remove(0).current?.id) // before: index moves down
        assertEquals("t3", q.remove(3).current?.id) // after: unchanged
        assertEquals("t4", q.remove(2).current?.id) // the current one: the next plays
        assertEquals(1, q.remove(0).index)
        assertTrue(Queue().remove(0).isEmpty)
    }

    @Test
    fun jumpStaysInRange() {
        val q = Queue.fromTracks(tracks(2))
        assertEquals(1, q.jump(1).index)
        assertEquals(0, q.jump(9).index)
    }

    @Test
    fun shuffleKeepsPlayedAndRestoresOrder() {
        val q = Queue.fromTracks(tracks(6), 1)
        val s = q.withShuffle(true, Random(7))
        assertTrue(s.shuffle)
        assertEquals(listOf("t1", "t2"), s.tracks.take(2).map { it.id })
        assertEquals(setOf("t3", "t4", "t5", "t6"), s.tracks.drop(2).map { it.id }.toSet())
        assertEquals("t2", s.current?.id)
        val back = s.jump(4).withShuffle(false)
        assertFalse(back.shuffle)
        assertEquals(listOf("t1", "t2", "t3", "t4", "t5", "t6"), back.tracks.map { it.id })
        assertEquals(s.tracks[4].id, back.current?.id)
    }

    @Test
    fun playNextInsertsAfterTheCurrentAndAppendGoesLast() {
        val q = Queue.fromTracks(tracks(3), 0)
        val more = listOf(Item(id = "x", title = "X"))
        assertEquals(listOf("t1", "x", "t2", "t3"), q.playNext(more).tracks.map { it.id })
        assertEquals(listOf("t1", "t2", "t3", "x"), q.append(more).tracks.map { it.id })
        assertEquals(listOf(0, 1, 2, 3), q.append(more).ords)
        assertEquals("x", Queue().playNext(more).current?.id)
    }

    @Test
    fun repeatCycles() {
        val q = Queue.fromTracks(tracks(1))
        assertEquals("all", q.cycleRepeat().repeat)
        assertEquals("one", q.cycleRepeat().cycleRepeat().repeat)
        assertEquals("off", q.cycleRepeat().cycleRepeat().cycleRepeat().repeat)
    }

    @Test
    fun aMixMarksWhereItBeginsAndStopsCleanly() {
        val mix = QueueMix(seedId = "s", kind = "track", title = "Seed", after = "t5", basis = listOf("genre"))
        // An ordinary queue of three carries on: the mix begins at 3.
        val q = Queue.fromTracks(tracks(3), 1).appendMix(tracks(5).drop(3), mix)
        assertEquals(3, q.mix?.from)
        assertEquals(5, q.tracks.size)
        assertEquals("s", q.record(0.0).mix?.seedId)
        // The next batch keeps the same beginning.
        val more = q.appendMix(listOf(Item(id = "t6", kind = "track")), mix.copy(after = "t6"))
        assertEquals(3, more.mix?.from)
        assertEquals("t6", more.mix?.after)
        // Removing an entry before the mix moves its beginning; after it does not.
        assertEquals(2, more.remove(0).mix?.from)
        assertEquals(3, more.remove(4).mix?.from)
        // Stopping drops what the mix added and has not played, and keeps what plays.
        val playing = more.jump(3)
        val stopped = playing.stopMix()
        assertEquals(listOf("t1", "t2", "t3", "t4"), stopped.tracks.map { it.id })
        assertEquals(3, stopped.index)
        assertEquals(null, stopped.mix)
        // A mix started on purpose begins at 0 and stopping keeps only the playing track.
        val own = Queue.fromTracks(tracks(4), 1, mix = mix.copy(from = 0)).stopMix()
        assertEquals(listOf("t1", "t2"), own.tracks.map { it.id })
        assertEquals(1, own.index)
    }
}
