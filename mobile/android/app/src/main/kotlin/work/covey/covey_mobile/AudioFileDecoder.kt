package work.covey.covey_mobile

import android.media.AudioFormat
import android.media.MediaCodec
import android.media.MediaExtractor
import android.media.MediaFormat
import android.os.Handler
import android.os.HandlerThread
import android.os.Looper
import io.flutter.plugin.common.BinaryMessenger
import io.flutter.plugin.common.MethodCall
import io.flutter.plugin.common.MethodChannel
import java.io.ByteArrayOutputStream
import java.nio.ByteBuffer
import java.nio.ByteOrder

/**
 * Audio files decoded on the device for the recogniser (#634): MediaExtractor
 * and the platform's MediaCodec decoder turn the file into PCM, which is mixed
 * down to mono and resampled to 16 kHz PCM16 here — block by block as Dart
 * asks for it, so a recording of hours is never in memory whole. A file
 * without an audio track the system can decode is refused with the code
 * `unsupported`. The same channel as on the iPhone and the Mac
 * (mobile/darwin/AudioFileDecoder.swift); Dart's side is lib/audio_file.dart.
 */
class AudioFileDecoder(messenger: BinaryMessenger) {
    private val channel = MethodChannel(messenger, "covey/audio-file")
    private val thread = HandlerThread("covey-audio-file").apply { start() }
    private val worker = Handler(thread.looper)
    private val main = Handler(Looper.getMainLooper())

    // Touched on the worker thread only.
    private val readers = HashMap<Int, AudioFileReader>()
    private var next = 1

    init {
        channel.setMethodCallHandler(::handle)
    }

    private fun reply(f: () -> Unit) {
        main.post(f)
    }

    private fun handle(call: MethodCall, result: MethodChannel.Result) {
        worker.post {
            try {
                when (call.method) {
                    "open" -> {
                        val path = call.argument<String>("path")
                        if (path == null) {
                            reply { result.error("bad-arguments", "no path", null) }
                        } else {
                            val reader = try {
                                AudioFileReader.open(path)
                            } catch (e: Exception) {
                                reply { result.error("unsupported", e.message ?: e.toString(), null) }
                                null
                            }
                            if (reader != null) {
                                val id = next++
                                readers[id] = reader
                                reply { result.success(mapOf("id" to id, "durationMs" to reader.durationMs)) }
                            }
                        }
                    }
                    "read" -> {
                        val reader = readers[call.argument<Int>("id") ?: -1]
                        // Between 0.1 s and 30 s per block.
                        val frames = (call.argument<Int>("frames") ?: 32000).coerceIn(1600, 480_000)
                        val data = reader?.read(frames)
                        reply { result.success(data) }
                    }
                    "close" -> {
                        readers.remove(call.argument<Int>("id") ?: -1)?.close()
                        reply { result.success(null) }
                    }
                    else -> reply { result.notImplemented() }
                }
            } catch (e: Exception) {
                reply { result.error("failed", e.message ?: e.toString(), null) }
            }
        }
    }

    fun dispose() {
        channel.setMethodCallHandler(null)
        worker.post {
            readers.values.forEach { it.close() }
            readers.clear()
            thread.quitSafely()
        }
    }
}

/** One open file, decoded on demand. */
private class AudioFileReader private constructor(
    private val extractor: MediaExtractor,
    private val codec: MediaCodec,
    format: MediaFormat,
    val durationMs: Long,
) {
    private var channels = format.getInteger(MediaFormat.KEY_CHANNEL_COUNT)
    private var float = false
    private var resampler = Resampler(format.getInteger(MediaFormat.KEY_SAMPLE_RATE))
    private var inputDone = false
    private var outputDone = false

    /** 16 kHz mono PCM16 decoded but not yet handed out. */
    private val pending = ByteArrayOutputStream()
    private val info = MediaCodec.BufferInfo()

    companion object {
        fun open(path: String): AudioFileReader {
            val extractor = MediaExtractor()
            try {
                extractor.setDataSource(path)
                val track = (0 until extractor.trackCount).firstOrNull {
                    extractor.getTrackFormat(it).getString(MediaFormat.KEY_MIME)?.startsWith("audio/") == true
                } ?: throw IllegalArgumentException("the file holds no audio track")
                extractor.selectTrack(track)
                val format = extractor.getTrackFormat(track)
                val mime = format.getString(MediaFormat.KEY_MIME)!!
                val duration = if (format.containsKey(MediaFormat.KEY_DURATION)) format.getLong(MediaFormat.KEY_DURATION) / 1000 else 0L
                val codec = MediaCodec.createDecoderByType(mime)
                try {
                    codec.configure(format, null, null, 0)
                    codec.start()
                } catch (e: Exception) {
                    codec.release()
                    throw e
                }
                return AudioFileReader(extractor, codec, format, duration)
            } catch (e: Exception) {
                extractor.release()
                throw e
            }
        }
    }

    /** Up to [frames] samples of 16 kHz mono PCM16; null once the file is done. */
    fun read(frames: Int): ByteArray? {
        val want = frames * 2
        while (pending.size() < want && !outputDone) step()
        if (pending.size() == 0) return null
        val all = pending.toByteArray()
        val n = minOf(want, all.size)
        pending.reset()
        pending.write(all, n, all.size - n)
        return all.copyOf(n)
    }

    private fun step() {
        if (!inputDone) {
            val i = codec.dequeueInputBuffer(10_000)
            if (i >= 0) {
                val buf = codec.getInputBuffer(i)!!
                val n = extractor.readSampleData(buf, 0)
                if (n < 0) {
                    codec.queueInputBuffer(i, 0, 0, 0, MediaCodec.BUFFER_FLAG_END_OF_STREAM)
                    inputDone = true
                } else {
                    codec.queueInputBuffer(i, 0, n, extractor.sampleTime, 0)
                    extractor.advance()
                }
            }
        }
        val o = codec.dequeueOutputBuffer(info, 10_000)
        if (o >= 0) {
            val buf = codec.getOutputBuffer(o)!!
            buf.position(info.offset)
            buf.limit(info.offset + info.size)
            take(buf.slice().order(ByteOrder.nativeOrder()))
            codec.releaseOutputBuffer(o, false)
            if (info.flags and MediaCodec.BUFFER_FLAG_END_OF_STREAM != 0) outputDone = true
        } else if (o == MediaCodec.INFO_OUTPUT_FORMAT_CHANGED) {
            val f = codec.outputFormat
            channels = f.getInteger(MediaFormat.KEY_CHANNEL_COUNT)
            float = f.containsKey(MediaFormat.KEY_PCM_ENCODING) &&
                f.getInteger(MediaFormat.KEY_PCM_ENCODING) == AudioFormat.ENCODING_PCM_FLOAT
            resampler = Resampler(f.getInteger(MediaFormat.KEY_SAMPLE_RATE))
        }
    }

    /** One decoded buffer: the channels mixed into one, resampled, as PCM16. */
    private fun take(buf: ByteBuffer) {
        val c = maxOf(channels, 1)
        val frames = buf.remaining() / (c * if (float) 4 else 2)
        val out = ByteBuffer.allocate(resampler.capacity(frames) * 2).order(ByteOrder.LITTLE_ENDIAN)
        if (float) {
            val samples = buf.asFloatBuffer()
            while (samples.remaining() >= c) {
                var sum = 0f
                repeat(c) { sum += samples.get() }
                resampler.push(sum / c) { out.putSample(it) }
            }
        } else {
            val samples = buf.asShortBuffer()
            while (samples.remaining() >= c) {
                var sum = 0f
                repeat(c) { sum += samples.get() / 32768f }
                resampler.push(sum / c) { out.putSample(it) }
            }
        }
        pending.write(out.array(), 0, out.position())
    }

    private fun ByteBuffer.putSample(v: Float) {
        if (remaining() < 2) return
        putShort((v.coerceIn(-1f, 1f) * 32767f).toInt().toShort())
    }

    fun close() {
        try {
            codec.stop()
        } catch (_: Exception) {
        }
        codec.release()
        extractor.release()
    }
}

/**
 * From the file's rate to 16 kHz, one sample at a time: when downsampling,
 * each output sample is the mean of the input samples it covers — a box
 * filter that keeps what lies above 8 kHz from folding into the speech band;
 * when upsampling, linear interpolation.
 */
private class Resampler(rate: Int) {
    private val step = rate / 16000.0
    private var sum = 0.0
    private var count = 0
    private var pos = 0.0
    private var last = 0f
    private var frac = 0.0

    /** At most how many samples [frames] input samples become. */
    fun capacity(frames: Int): Int = (frames / step).toInt() + 2

    fun push(x: Float, out: (Float) -> Unit) {
        if (step >= 1.0) {
            sum += x
            count++
            pos += 1.0
            if (pos >= step) {
                out((sum / count).toFloat())
                sum = 0.0
                count = 0
                pos -= step
            }
        } else {
            while (frac < 1.0) {
                out(last + (x - last) * frac.toFloat())
                frac += step
            }
            frac -= 1.0
            last = x
        }
    }
}
