package com.garybowers.cineva.player

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import kotlin.math.PI
import kotlin.math.sin

class SpectrumTest {
    private fun tone(hz: Double, rate: Int = 44100, n: Int = Spectrum.N): FloatArray =
        FloatArray(n) { i -> (0.8 * sin(2 * PI * hz * i / rate)).toFloat() }

    @Test
    fun theFftFindsATone() {
        val rate = 44100
        val mags = Spectrum.magnitudes(tone(1000.0, rate))
        val binHz = rate.toDouble() / Spectrum.N
        val peak = mags.indices.maxByOrNull { mags[it] }!!
        assertEquals(1000.0 / binHz, peak.toDouble(), 1.0)
        assertTrue("the peak reads near full scale, got ${mags[peak]}", mags[peak] > 0.5f)
    }

    @Test
    fun theBandsAreLogSpacedFrom40To16k() {
        val e = Spectrum.edges(24)
        assertEquals(25, e.size)
        assertEquals(40.0, e.first(), 0.01)
        assertEquals(16000.0, e.last(), 0.01)
        // Each band is the same ratio wider than the last.
        val ratio = e[1] / e[0]
        for (i in 1 until 24) assertEquals(ratio, e[i + 1] / e[i], 1e-6)
    }

    @Test
    fun aToneLandsInItsBandAndNowhereLoud() {
        val rate = 44100
        val bands = Spectrum.bin(Spectrum.magnitudes(tone(1000.0, rate)), rate, 24)
        val e = Spectrum.edges(24)
        val expected = (0 until 24).first { 1000.0 >= e[it] && 1000.0 < e[it + 1] }
        val loudest = bands.indices.maxByOrNull { bands[it] }!!
        assertEquals(expected, loudest)
        assertTrue(bands[loudest] > 0.6f)
        assertTrue("a distant band stays quiet", bands[0] < 0.25f)
    }

    @Test
    fun silenceIsZero() {
        val bands = Spectrum.bin(Spectrum.magnitudes(FloatArray(Spectrum.N)), 44100, 24)
        assertTrue(bands.all { it == 0f })
    }

    @Test
    fun feedMixesStereo16BitToMono() {
        val s = Spectrum()
        val buf = java.nio.ByteBuffer.allocate(8).order(java.nio.ByteOrder.nativeOrder())
        buf.putShort(16384).putShort(-16384).putShort(32767).putShort(32767)
        buf.flip()
        s.feed(buf, 2, androidx.media3.common.C.ENCODING_PCM_16BIT)
        // Two frames: (0.5 + -0.5) / 2 = 0, and about 1.
        val f = Spectrum::class.java.getDeclaredField("ring").apply { isAccessible = true }.get(s) as FloatArray
        assertEquals(0f, f[0], 1e-4f)
        assertEquals(1f, f[1], 1e-3f)
    }
}
