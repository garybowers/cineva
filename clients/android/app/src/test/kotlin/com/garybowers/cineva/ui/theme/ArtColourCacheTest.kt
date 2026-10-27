package com.garybowers.cineva.ui.theme

import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.async
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class ArtColourCacheTest {
    @Test
    fun `a cover is worked out once however often it is asked for`() = runBlocking {
        val cache = ArtColourCache { url -> url.length }
        assertEquals(10, cache.colourFor("/art/a.jpg"))
        assertEquals(10, cache.colourFor("/art/a.jpg"))
        assertEquals(1, cache.extractions)
        assertEquals(12, cache.colourFor("/art/abc.jpg"))
        assertEquals(2, cache.extractions)
    }

    @Test
    fun `two askers at once share one extraction`() = runBlocking {
        val gate = CompletableDeferred<Unit>()
        val cache = ArtColourCache { gate.await(); 7 }
        val a = async { cache.colourFor("/x") }
        val b = async { cache.colourFor("/x") }
        gate.complete(Unit)
        assertEquals(7, a.await())
        assertEquals(7, b.await())
        assertEquals(1, cache.extractions)
    }

    @Test
    fun `no cover, a failed extraction and a cover without colour all give null and are remembered`() = runBlocking {
        var calls = 0
        val cache = ArtColourCache { url -> calls++; if (url == "/bad") throw IllegalStateException("no") else null }
        assertNull(cache.colourFor(null))
        assertNull(cache.colourFor(""))
        assertNull(cache.colourFor("/bad"))
        assertNull(cache.colourFor("/bad"))
        assertNull(cache.colourFor("/grey"))
        assertEquals(2, calls)
    }

    @Test
    fun `select makes the colour current and clear forgets it`() = runBlocking {
        val cache = ArtColourCache { 0xFF112233.toInt() }
        cache.select("/a")
        assertEquals(0xFF112233.toInt(), cache.current.value)
        cache.clear()
        assertNull(cache.current.value)
    }
}
