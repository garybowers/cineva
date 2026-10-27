package com.garybowers.cineva.player

import com.garybowers.cineva.api.Item
import com.garybowers.cineva.api.QueueEntry
import com.garybowers.cineva.api.QueueMix
import com.garybowers.cineva.api.QueueRecord
import com.garybowers.cineva.api.QueueSource

/**
 * The play queue as plain data: the tracks in play order, where in them
 * we are, and the record the server keeps (server/docs/music.md, part 2).
 * Pure Kotlin, so it is tested on the JVM; the player mirrors it.
 *
 * Entries carry `ord`, their place in the unshuffled order, so turning
 * shuffle off restores it and turning it on shuffles only what has not
 * played.
 */
data class Queue(
    val tracks: List<Item> = emptyList(),
    val ords: List<Int> = emptyList(),
    val index: Int = 0,
    val shuffle: Boolean = false,
    val repeat: String = "off",
    val source: QueueSource? = null,
    /** The queue is, or ends in, a mix; entries from mix.from on are its (server/docs/mix.md). */
    val mix: QueueMix? = null,
) {
    val current: Item? get() = tracks.getOrNull(index)
    val isEmpty: Boolean get() = tracks.isEmpty()

    /** The index the mix's entries begin at; the end when there is no mix. */
    val mixFrom: Int get() = mix?.from?.coerceIn(0, tracks.size) ?: tracks.size

    /** The record the server keeps, with where in the current track we are. */
    fun record(positionSecs: Double): QueueRecord = QueueRecord(
        entries = tracks.mapIndexed { i, t -> QueueEntry(t.id, ords.getOrElse(i) { i }) },
        index = index.coerceIn(0, (tracks.size - 1).coerceAtLeast(0)),
        shuffle = shuffle,
        repeat = repeat,
        positionSecs = positionSecs,
        source = source,
        mix = mix,
    )

    fun jump(to: Int): Queue = if (to in tracks.indices) copy(index = to) else this

    fun remove(at: Int): Queue {
        if (at !in tracks.indices) return this
        val t = tracks.toMutableList().also { it.removeAt(at) }
        val o = ords.toMutableList().also { if (at < it.size) it.removeAt(at) }
        val i = when {
            at < index -> index - 1
            at == index -> index.coerceAtMost((t.size - 1).coerceAtLeast(0))
            else -> index
        }
        val m = mix?.let { if (at < it.from) it.copy(from = it.from - 1) else it }
        return copy(tracks = t, ords = o, index = i, mix = m)
    }

    /** A mix's next batch goes on the end; the first one marks where the mix begins. */
    fun appendMix(more: List<Item>, next: QueueMix): Queue {
        val from = mix?.from ?: tracks.size
        return append(more).copy(mix = next.copy(from = from))
    }

    /** Drops the mix's entries still to come and forgets it; what is playing keeps playing. */
    fun stopMix(): Queue {
        if (mix == null) return this
        val keep = maxOf(mixFrom, index + 1).coerceAtMost(tracks.size)
        return copy(tracks = tracks.take(keep), ords = ords.take(keep), index = index.coerceAtMost((keep - 1).coerceAtLeast(0)), mix = null)
    }

    /** Appends tracks to play after everything queued. */
    fun append(more: List<Item>): Queue {
        val start = ords.size
        return copy(tracks = tracks + more, ords = ords + more.indices.map { start + it })
    }

    /** Inserts tracks after the current one. */
    fun playNext(more: List<Item>): Queue {
        if (tracks.isEmpty()) return fromTracks(more, 0, source)
        val at = index + 1
        val t = tracks.toMutableList().also { it.addAll(at, more) }
        // The new entries take ords after everything, so an unshuffle lists
        // them where they were added relative to the rest.
        val start = (ords.maxOrNull() ?: -1) + 1
        val o = ords.toMutableList().also { it.addAll(at, more.indices.map { start + it }) }
        return copy(tracks = t, ords = o)
    }

    /**
     * Shuffle on keeps the current track where it is and shuffles what
     * follows; off restores the unshuffled order around the current track.
     */
    fun withShuffle(on: Boolean, random: kotlin.random.Random = kotlin.random.Random.Default): Queue {
        if (on == shuffle || tracks.isEmpty()) return copy(shuffle = on)
        val cur = current ?: return copy(shuffle = on)
        return if (on) {
            val played = tracks.take(index + 1)
            val playedOrds = ords.take(index + 1)
            val rest = tracks.drop(index + 1).zip(ords.drop(index + 1)).shuffled(random)
            copy(tracks = played + rest.map { it.first }, ords = playedOrds + rest.map { it.second }, shuffle = true)
        } else {
            val sorted = tracks.zip(ords).sortedBy { it.second }
            copy(tracks = sorted.map { it.first }, ords = sorted.map { it.second },
                index = sorted.indexOfFirst { it.first.id == cur.id }.coerceAtLeast(0), shuffle = false)
        }
    }

    fun cycleRepeat(): Queue = copy(repeat = when (repeat) { "off" -> "all"; "all" -> "one"; else -> "off" })

    companion object {
        fun fromTracks(tracks: List<Item>, startIndex: Int = 0, source: QueueSource? = null, shuffle: Boolean = false, mix: QueueMix? = null): Queue {
            val q = Queue(tracks = tracks, ords = tracks.indices.toList(), index = startIndex.coerceIn(0, (tracks.size - 1).coerceAtLeast(0)), source = source, mix = mix)
            return if (shuffle) q.withShuffle(true) else q
        }
    }
}
