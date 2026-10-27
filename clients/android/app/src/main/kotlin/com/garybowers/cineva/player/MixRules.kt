package com.garybowers.cineva.player

/**
 * When a queue asks for more (server/docs/mix.md), as the web client's
 * queue store decides it: pure, so it is tested on the JVM.
 *
 * With two tracks still to come, a mix asks for its next batch after its
 * last entry; an ordinary queue, if the person wants the music to keep
 * playing, starts a mix from its last track. Repeat means the queue is
 * going round, so nothing is asked. A batch that came back empty, or
 * failed, is not asked for again until the queue's last entry changes.
 */
data class ExtendRequest(val seedId: String, val kind: String, val title: String, val after: String) {
    /** What an exhausted request is remembered by. */
    val key: String get() = "$seedId/$after"
}

object MixRules {
    /** Tracks still to come when the next batch is asked for. */
    const val EXTEND_WHEN = 2

    fun next(q: Queue, autoplay: Boolean): ExtendRequest? {
        if (q.isEmpty || q.repeat != "off") return null
        val remaining = q.tracks.size - q.index - 1
        if (remaining > EXTEND_WHEN) return null
        val last = q.tracks.last()
        if (last.kind != "track") return null
        val mix = q.mix
        return if (mix != null) {
            ExtendRequest(mix.seedId, mix.kind, mix.title, last.id)
        } else {
            if (!autoplay) null else ExtendRequest(last.id, "track", last.title, last.id)
        }
    }

    /** Words for a batch's basis: "Similar artists and what you play". */
    fun basisWords(basis: List<String>): String {
        val words = mapOf(
            "similar-artists" to "similar artists", "top-songs" to "best-known songs", "household" to "what you play",
            "co-listening" to "what you play alongside it", "genre" to "the same genre",
        )
        val parts = basis.mapNotNull { words[it] }
        if (parts.isEmpty()) return "From your library"
        val text = if (parts.size == 1) parts[0] else parts.dropLast(1).joinToString(", ") + " and " + parts.last()
        return text.replaceFirstChar { it.uppercase() }
    }
}
