package com.garybowers.cineva.player

import androidx.media3.common.C
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.audio.TeeAudioProcessor
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import java.nio.ByteBuffer
import java.nio.ByteOrder
import kotlin.math.PI
import kotlin.math.cos
import kotlin.math.ln
import kotlin.math.log10
import kotlin.math.max
import kotlin.math.min
import kotlin.math.sin
import kotlin.math.sqrt

/**
 * The spectrum behind the cover, Winamp's way: a tap on the player's own
 * output (so no microphone permission, and it works for local files too)
 * feeds a ring of samples; while now playing is open a ticker takes the
 * last 1024, windows them, runs a radix-2 FFT and bins the magnitudes
 * into log-spaced bands with a short attack, a slow decay and falling
 * peak caps. Closed, nothing runs and nothing is drawn.
 */
class Spectrum(val bandCount: Int = 24) {
    private val ring = FloatArray(RING)
    @Volatile private var write = 0
    @Volatile private var sampleRate = 44100
    @Volatile private var listening = false

    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    private var job: Job? = null
    private val _frame = MutableStateFlow(Frame(FloatArray(bandCount), FloatArray(bandCount)))
    /** The bands and their peak caps, each 0 to 1. */
    val frame: StateFlow<Frame> = _frame

    data class Frame(val bands: FloatArray, val peaks: FloatArray)

    /** What the player's audio sink calls with every buffer it writes. */
    @UnstableApi
    val sink: TeeAudioProcessor.AudioBufferSink = object : TeeAudioProcessor.AudioBufferSink {
        private var channels = 2
        private var encoding = C.ENCODING_PCM_16BIT
        override fun flush(sampleRateHz: Int, channelCount: Int, encoding: Int) {
            sampleRate = sampleRateHz; channels = max(1, channelCount); this.encoding = encoding
        }
        override fun handleBuffer(buffer: ByteBuffer) {
            if (!listening) return
            feed(buffer.duplicate().order(ByteOrder.nativeOrder()), channels, encoding)
        }
    }

    /** Mixes a PCM buffer to mono into the ring. 16-bit is what the sink carries by default; floats and 24 or 32 bits are read too. */
    fun feed(b: ByteBuffer, channels: Int, encoding: Int) {
        val bytesPer = when (encoding) {
            C.ENCODING_PCM_8BIT -> 1
            C.ENCODING_PCM_16BIT, C.ENCODING_PCM_16BIT_BIG_ENDIAN -> 2
            C.ENCODING_PCM_24BIT, C.ENCODING_PCM_24BIT_BIG_ENDIAN -> 3
            C.ENCODING_PCM_32BIT, C.ENCODING_PCM_32BIT_BIG_ENDIAN, C.ENCODING_PCM_FLOAT -> 4
            else -> return
        }
        val frameBytes = bytesPer * channels
        var w = write
        while (b.remaining() >= frameBytes) {
            var sum = 0f
            for (c in 0 until channels) {
                sum += when (encoding) {
                    C.ENCODING_PCM_8BIT -> (b.get().toInt() - 128) / 128f
                    C.ENCODING_PCM_16BIT -> b.short / 32768f
                    C.ENCODING_PCM_16BIT_BIG_ENDIAN -> java.lang.Short.reverseBytes(b.short) / 32768f
                    C.ENCODING_PCM_24BIT -> { val lo = b.get().toInt() and 0xff; val mid = b.get().toInt() and 0xff; val hi = b.get().toInt(); ((hi shl 16) or (mid shl 8) or lo) / 8388608f }
                    C.ENCODING_PCM_24BIT_BIG_ENDIAN -> { val hi = b.get().toInt(); val mid = b.get().toInt() and 0xff; val lo = b.get().toInt() and 0xff; ((hi shl 16) or (mid shl 8) or lo) / 8388608f }
                    C.ENCODING_PCM_32BIT -> b.int / 2147483648f
                    C.ENCODING_PCM_32BIT_BIG_ENDIAN -> Integer.reverseBytes(b.int) / 2147483648f
                    else -> b.float
                }
            }
            ring[w] = sum / channels
            w = (w + 1) and (RING - 1)
        }
        write = w
    }

    /** Starts analysing at about 30 frames a second; `still` keeps the bars where they are (reduced motion). */
    fun start(still: Boolean = false) {
        listening = true
        if (job?.isActive == true) return
        val bands = FloatArray(bandCount)
        val peaks = FloatArray(bandCount)
        val hold = IntArray(bandCount)
        val window = FloatArray(N)
        val re = FloatArray(N)
        val im = FloatArray(N)
        job = scope.launch {
            while (isActive) {
                if (!still) {
                    val start = (write - N) and (RING - 1)
                    for (i in 0 until N) window[i] = ring[(start + i) and (RING - 1)]
                    val mags = magnitudes(window, re, im)
                    val fresh = bin(mags, sampleRate, bandCount)
                    for (i in 0 until bandCount) {
                        bands[i] = if (fresh[i] > bands[i]) bands[i] + (fresh[i] - bands[i]) * 0.6f else bands[i] * 0.86f
                        if (bands[i] >= peaks[i]) { peaks[i] = bands[i]; hold[i] = 12 }
                        else if (hold[i] > 0) hold[i]-- else peaks[i] = max(0f, peaks[i] - 0.015f)
                    }
                    _frame.value = Frame(bands.copyOf(), peaks.copyOf())
                }
                delay(33)
            }
        }
    }

    fun stop() {
        listening = false
        job?.cancel(); job = null
    }

    companion object {
        const val N = 1024
        private const val RING = 4096

        /** Hann-windowed magnitudes of the first N/2 bins, in place through a radix-2 FFT. */
        fun magnitudes(samples: FloatArray, re: FloatArray = FloatArray(N), im: FloatArray = FloatArray(N)): FloatArray {
            for (i in 0 until N) {
                re[i] = samples[i] * (0.5f - 0.5f * cos(2.0 * PI * i / (N - 1)).toFloat())
                im[i] = 0f
            }
            fft(re, im)
            val out = FloatArray(N / 2)
            for (i in 0 until N / 2) out[i] = sqrt(re[i] * re[i] + im[i] * im[i]) / (N / 4)
            return out
        }

        /** In-place iterative radix-2 Cooley-Tukey. */
        fun fft(re: FloatArray, im: FloatArray) {
            val n = re.size
            var j = 0
            for (i in 1 until n) {
                var bit = n shr 1
                while (j and bit != 0) { j = j xor bit; bit = bit shr 1 }
                j = j xor bit
                if (i < j) { var t = re[i]; re[i] = re[j]; re[j] = t; t = im[i]; im[i] = im[j]; im[j] = t }
            }
            var len = 2
            while (len <= n) {
                val ang = -2.0 * PI / len
                val wr = cos(ang).toFloat(); val wi = sin(ang).toFloat()
                var i = 0
                while (i < n) {
                    var cr = 1f; var ci = 0f
                    for (k in 0 until len / 2) {
                        val ur = re[i + k]; val ui = im[i + k]
                        val vr = re[i + k + len / 2] * cr - im[i + k + len / 2] * ci
                        val vi = re[i + k + len / 2] * ci + im[i + k + len / 2] * cr
                        re[i + k] = ur + vr; im[i + k] = ui + vi
                        re[i + k + len / 2] = ur - vr; im[i + k + len / 2] = ui - vi
                        val nr = cr * wr - ci * wi; ci = cr * wi + ci * wr; cr = nr
                    }
                    i += len
                }
                len = len shl 1
            }
        }

        /** The band edges, log-spaced from 40 Hz to 16 kHz. */
        fun edges(bands: Int, lo: Double = 40.0, hi: Double = 16000.0): DoubleArray =
            DoubleArray(bands + 1) { i -> lo * Math.pow(hi / lo, i.toDouble() / bands) }

        /** Bins the magnitudes into bands on a decibel scale, each 0 to 1. */
        fun bin(mags: FloatArray, sampleRate: Int, bands: Int): FloatArray {
            val e = edges(bands)
            val out = FloatArray(bands)
            val binHz = sampleRate.toDouble() / (2 * mags.size)
            for (b in 0 until bands) {
                val from = max(1, (e[b] / binHz).toInt())
                val to = max(from, min(mags.size - 1, (e[b + 1] / binHz).toInt()))
                var peak = 0f
                for (i in from..to) peak = max(peak, mags[i])
                // 66 dB of range above the floor reads as 0 to 1, bent so
                // the quiet bands stay low and only the loud ones stand up.
                val db = 20.0 * log10(max(peak.toDouble(), 1e-6))
                val lin = ((db + 66.0) / 66.0).coerceIn(0.0, 1.0)
                out[b] = Math.pow(lin, 1.8).toFloat()
            }
            return out
        }

        @Suppress("unused") private val keepLn = ln(1.0)
    }
}
