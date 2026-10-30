package tv.getportico.portico.native

import android.content.Context
import androidx.media3.common.C
import androidx.media3.common.Format
import androidx.media3.common.MimeTypes
import androidx.media3.exoplayer.DefaultRenderersFactory
import androidx.media3.exoplayer.audio.AudioSink
import androidx.media3.exoplayer.audio.DefaultAudioSink
import androidx.media3.exoplayer.audio.ForwardingAudioSink
import java.nio.ByteBuffer
import java.nio.ByteOrder

/** Issued once when the bound source is installed, never filled in from a later commit. */
internal class PorticoAudioSourceTag(
  val sessionId: String, val generation: Long, val itemId: String, val planId: String,
  val lease: String, val intentId: Long,
) {
  fun matches(binding: Map<*, *>?, plan: Map<*, *>?): Boolean = binding != null &&
      sessionId == binding["sessionId"] && generation.toDouble() == (binding["generation"] as? Number)?.toDouble() &&
      itemId == binding["itemId"] && planId == plan?.get("id") && lease == binding["lease"] &&
      intentId.toDouble() == (binding["intentId"] as? Number)?.toDouble()
}

/** Only the renderer's actual decoded-output boundary originates a production stamp.
 * Keep the exact originating tag and period object, including across same-format boundaries. */
internal class PorticoDecodedStreamStamp(val source: PorticoAudioSourceTag, val periodUid: Any)

/** Complete, settled normalization on every contributing source sample, with unity fade. */
internal class PorticoGainWitness(
  val stream: PorticoDecodedStreamStamp, val coefficient: Float, val control: Any? = null,
) {
  fun sameAs(other: PorticoGainWitness?): Boolean = other != null && stream === other.stream &&
      coefficient == other.coefficient && control === other.control
}

/**
 * Music gain applied to the decoder's own PCM, before the sink converts anything.
 *
 * Media3's `DefaultAudioSink` cannot carry a user gain stage at high precision: with float output
 * it replaces the processor chain entirely, and without float output it converts to 16-bit before
 * the chain. So gain runs on the buffer the renderer hands the sink, in the decoder's encoding,
 * and converts to the encoding [PorticoAudioOutput] selected for this device and stream. An
 * unchanged unity stream at one encoding is returned untouched, keeping a bit-exact pass; ramped
 * or amplified audio is rounded per sample and clipped at the target's full scale. Nothing here
 * resamples, mixes or reorders channels, and [input] is never consumed.
 */
internal class PorticoAudioGain {
  private data class Target(val composite: Float, val normalization: Float?, val fade: Double?, val known: Boolean,
                            val source: PorticoAudioSourceTag? = null)
  @Volatile private var wantedTarget = Target(1f, null, null, false)
  /** A composite-only caller supplies no normalization proof. */
  var target: Float
    get() = wantedTarget.composite
    set(value) { wantedTarget = Target(value, null, null, false) }
  fun components(normalization: Float, fade: Double, known: Boolean, source: PorticoAudioSourceTag? = null) {
    val next = Target((normalization * fade).toFloat(), normalization, fade, known, source)
    // Repeating identical controls must not invalidate a genuine accepted witness.
    if (wantedTarget != next) wantedTarget = next
  }
  @Volatile var outputStream: PorticoDecodedStreamStamp? = null
  @Volatile var acceptedWitness: PorticoGainWitness? = null
  fun currentWitness(): PorticoGainWitness? {
    val proof = acceptedWitness ?: return null
    val controls = wantedTarget
    return proof.takeIf { it.stream === outputStream && it.control === controls && controls.source === it.stream.source }
  }
  fun witness(stream: PorticoDecodedStreamStamp?, applied: Applied): PorticoGainWitness? =
      if (stream != null && applied.normalizationCoefficient != null && applied.source === stream.source)
        PorticoGainWitness(stream, applied.normalizationCoefficient, applied.control) else null
  data class Applied(val buffer: ByteBuffer, val normalizationCoefficient: Float?,
                     val source: PorticoAudioSourceTag? = null, val control: Any? = null)
  private var level = 1f
  private var goal = 1f
  private var remaining = 0
  private var step = 0f

  /** Begins a new stream at the current target, without a ramp from the previous one. */
  fun reset() { level = target; goal = target; remaining = 0 }

  fun apply(input: ByteBuffer, sourceEncoding: Int, targetEncoding: Int, channelCount: Int, sampleRate: Int): ByteBuffer =
      applyObserved(input, sourceEncoding, targetEncoding, channelCount, sampleRate).buffer

  fun applyObserved(input: ByteBuffer, sourceEncoding: Int, targetEncoding: Int, channelCount: Int, sampleRate: Int): Applied {
    require(PorticoAudioOutput.supports(sourceEncoding) && PorticoAudioOutput.supports(targetEncoding)) {
      "unsupported pcm encoding"
    }
    val sourceFrame = PorticoAudioOutput.bytesPerSample(sourceEncoding) * channelCount
    val targetFrame = PorticoAudioOutput.bytesPerSample(targetEncoding) * channelCount
    if (sourceFrame <= 0 || targetFrame <= 0 || input.remaining() < sourceFrame) return Applied(input, null)
    val controls = wantedTarget
    val wanted = controls.composite.takeIf { it.isFinite() }?.coerceIn(0f, 32f) ?: 1f
    if (wanted != goal) {
      goal = wanted
      remaining = (sampleRate / 50).coerceAtLeast(1)
      step = (goal - level) / remaining
    }
    // Capture the coefficient this call actually uses, never the control target at acceptance.
    // A ramp that settles inside this buffer still leaves the entire buffer unproven.
    val observed = level.takeIf { remaining == 0 && it == goal && it.isFinite() && it > 0f
      && controls.known && controls.fade == 1.0 && controls.normalization == it
      && input.remaining() % sourceFrame == 0 }
    if (level == 1f && goal == 1f && sourceEncoding == targetEncoding) return Applied(input, observed, controls.source, controls)
    val source = input.duplicate().order(ByteOrder.nativeOrder())
    val frames = source.remaining() / sourceFrame
    if (frames <= 0) return Applied(input, null)
    val out = ByteBuffer.allocateDirect(frames * targetFrame).order(ByteOrder.nativeOrder())
    repeat(frames) {
      if (remaining > 0) { level += step; if (--remaining == 0) level = goal }
      repeat(channelCount) { writeSample(out, targetEncoding, readSample(source, sourceEncoding) * level) }
    }
    out.flip()
    return Applied(out, observed, controls.source, controls)
  }

  private fun readSample(input: ByteBuffer, encoding: Int): Float = when (encoding) {
    C.ENCODING_PCM_16BIT -> input.short / 32768f
    C.ENCODING_PCM_24BIT -> read24(input) / 8388608f
    C.ENCODING_PCM_32BIT -> (input.int / 2147483648.0).toFloat()
    else -> input.float
  }

  private fun read24(input: ByteBuffer): Int {
    val raw = (input.get().toInt() and 0xFF) or ((input.get().toInt() and 0xFF) shl 8) or ((input.get().toInt() and 0xFF) shl 16)
    return if (raw and 0x800000 != 0) raw - 0x1000000 else raw
  }

  private fun writeSample(output: ByteBuffer, encoding: Int, scaled: Float) {
    val value = if (scaled.isFinite()) scaled.coerceIn(-1f, 1f) else 0f
    when (encoding) {
      C.ENCODING_PCM_16BIT -> output.putShort((value * 32768f).toInt().coerceIn(-32768, 32767).toShort())
      C.ENCODING_PCM_24BIT -> {
        val sample = (value * 8388608f).toInt().coerceIn(-8388608, 8388607)
        output.put((sample and 0xFF).toByte())
        output.put((sample shr 8 and 0xFF).toByte())
        output.put((sample shr 16 and 0xFF).toByte())
      }
      C.ENCODING_PCM_32BIT -> output.putInt(
          (value.toDouble() * 2147483648.0).toLong().coerceIn(Int.MIN_VALUE.toLong(), Int.MAX_VALUE.toLong()).toInt())
      else -> output.putFloat(value)
    }
  }
}

/**
 * Holds one transformed buffer across sink retries.
 *
 * `AudioSink.handleBuffer` may return false without consuming the buffer, and the next attempt
 * must pass the same buffer with the same timestamp and access count. A different identity while
 * one is still held is a broken contract, so it is rejected rather than silently replacing a
 * buffer the delegate still owns; only [clear] (flush or reset) retires one. On acceptance the
 * original decoder buffer is advanced to the end it had when it was handed over.
 */
internal class PorticoGainQueue(private val gain: PorticoAudioGain) {
  private var pending: ByteBuffer? = null
  private var pendingInput: ByteBuffer? = null
  private var pendingTime = 0L
  private var pendingCount = 0
  private var pendingEnd = 0
  var witness: PorticoGainWitness? = null
    private set

  fun hasPending(): Boolean = pending != null

  fun bufferFor(input: ByteBuffer, sourceEncoding: Int, targetEncoding: Int, channelCount: Int, sampleRate: Int,
                presentationTimeUs: Long, encodedAccessUnitCount: Int, stream: PorticoDecodedStreamStamp? = null): ByteBuffer {
    val held = pending
    if (held != null) {
      check(pendingInput === input && pendingTime == presentationTimeUs && pendingCount == encodedAccessUnitCount) {
        "audio sink retried a different buffer while one was still pending"
      }
      return held
    }
    // Cache every submission, including a unity buffer that is handed on unchanged: the delegate
    // must see the same object again if it refuses it, whatever the gain target becomes meanwhile.
    val applied = gain.applyObserved(input, sourceEncoding, targetEncoding, channelCount, sampleRate)
    val scaled = applied.buffer
    witness = gain.witness(stream, applied)
    pending = scaled
    pendingInput = input
    pendingTime = presentationTimeUs
    pendingCount = encodedAccessUnitCount
    pendingEnd = input.limit()
    return scaled
  }

  fun accepted() {
    pendingInput?.position(pendingEnd)
    clear()
  }

  fun clear() {
    pending = null
    pendingInput = null
    pendingTime = 0
    pendingCount = 0
    pendingEnd = 0
    witness = null
  }
}

/**
 * The listening pipeline: gain plus the device format decision, kept separate from the sink
 * adapter so the retry and reconfigure behaviour is testable without a Media3 delegate.
 */
internal class PorticoMusicPipeline(
  private val gain: PorticoAudioGain,
  private val route: () -> PorticoAudioOutput.Route,
  private val sampleRateMode: () -> String = { PorticoAudioSampleRate.mode },
  private val preferredSampleRate: () -> Int = { 0 },
) {
  private val queue = PorticoGainQueue(gain)
  private var stage = PorticoRateStage()
  /** A stage whose remaining frames are still owed to the delegate, in its own format. */
  private var draining: PorticoRateStage? = null
  /** The delegate format that must not be applied until [draining] has been delivered. */
  private var deferred: Format? = null
  private var sourceEncoding = C.ENCODING_PCM_16BIT
  private var targetEncoding = C.ENCODING_PCM_16BIT
  private var channels = 0
  private var sampleRate = 0
  /** Internal only: the accepted piece's original proof, never today's playback binding.
   * Production remains unknown until the renderer supplies an actual decoded stream stamp. */
  @Volatile var acceptedWitness: PorticoGainWitness? = null
    private set(value) { field = value; gain.acceptedWitness = value }

  /**
   * What the delegate must be configured with for [format]. media3 may call this without a flush —
   * `DefaultAudioSink` keeps the old configuration live and queues the new one — so the previous
   * stream may still owe frames here. Those are never discarded into another format: this returns
   * the new format and keeps [draining] alive, and the sink delivers them under the old
   * configuration before applying it ([drain], [deferredFormat], [drained]).
   *
   * A stage continues only when the newly computed target and the source format are exactly the
   * ones it already holds, and it has not entered end of stream — the genuine contiguous streaming
   * case. Any differing target or format, or an offered/refused tail or a requested end, is drained
   * first, so the delegate never gets a new configuration while it still owns an old buffer.
   */
  fun configure(format: Format): Format {
    // A pending gain buffer belongs to the previous stream; configure must not discard it.
    check(!queue.hasPending()) { "configure while a buffer is still pending" }
    acceptedWitness = null
    val source = if (format.pcmEncoding != Format.NO_VALUE) format.pcmEncoding else C.ENCODING_PCM_16BIT
    // Only the four paths this stage converts are accepted; anything else is refused before a
    // single byte reaches the delegate, which would otherwise misread it at the target encoding.
    require(PorticoAudioOutput.supports(source)) { "unsupported pcm encoding" }
    sourceEncoding = source
    channels = format.channelCount
    sampleRate = format.sampleRate
    gain.reset()
    targetEncoding = PorticoAudioOutput.targetEncoding(route(), sampleRate, channels, sourceEncoding)
    // media3 applies encoderDelay/encoderPadding as input PCM frame counts at the delegate's rate,
    // so changing that rate would make a stream trim the wrong duration. Until trimming is done in
    // the source domain ahead of this stage, a stream that declares either keeps its own rate.
    val declaresTrimming = format.encoderDelay > 0 || format.encoderPadding > 0
    // The desired rate is computed before anything is changed; the routed device is not readable
    // from the media3 sink, so Smart stays unknown rather than assuming support.
    val target = PorticoRateStage.target(sampleRate, sampleRateMode(), preferredSampleRate(),
        sampleRate, null, allowRateChange = !declaresTrimming)
    val delegate = converted(format, sourceEncoding)
    val wanted = if (target > 0 && target != sampleRate) delegate.buildUpon().setSampleRate(target).build() else delegate

    if (stage.hasUnfinishedAudio &&
        stage.continuesWith(sampleRate, channels, targetEncoding, target)) {
      // Same source format, same output rate, nothing flushed and no end of stream in flight:
      // continue the held frames and clock.
      return wanted
    }
    if (stage.hasUnfinishedAudio) {
      // Its frames belong to the format it was configured for; they are delivered before the new
      // configuration is applied, never reinterpreted through it.
      if (draining == null) draining = stage
      deferred = wanted
    } else if (draining != null) {
      // An earlier drain is still owed; keep it and wait for this format as well.
      deferred = wanted
    }
    stage = PorticoRateStage()
    stage.configure(sampleRate, channels, targetEncoding, target)
    return wanted
  }

  /** True while a previous stream still owes frames before the delegate may be reconfigured. */
  fun drainPending(): Boolean = deferred != null

  /** The delegate format waiting for the drain, or null when there is nothing deferred. */
  fun deferredFormat(): Format? = deferred

  /**
   * Deliver the draining stage's remaining frames in its own format. Returns false while the
   * delegate cannot accept them, keeping the same piece (and its buffer identity) for the retry.
   */
  fun drain(deliver: (ByteBuffer, Long, Int) -> Boolean): Boolean {
    val old = draining
    if (old == null) return true
    val piece = old.takeTail()
    if (piece == null) {
      draining = null
      return true
    }
    if (!deliver(piece.buffer, piece.timeUs, piece.count)) return false
    acceptedWitness = piece.witness.takeIf { piece.frames > 0 }
    old.accepted(piece)
    draining = null
    return true
  }

  /** The sink applied the deferred format after a successful [drain]. */
  fun drained() { deferred = null }

  fun handle(buffer: ByteBuffer, presentationTimeUs: Long, encodedAccessUnitCount: Int,
             stream: PorticoDecodedStreamStamp? = null,
             deliver: (ByteBuffer, Long, Int) -> Boolean): Boolean {
    val scaled = queue.bufferFor(buffer, sourceEncoding, targetEncoding, channels, sampleRate,
        presentationTimeUs, encodedAccessUnitCount, stream)
    // The stage owns time and count from here: a retried buffer returns the same piece, so the
    // delegate sees one stable timestamp rather than the input buffer's start each attempt.
    val piece = stage.process(scaled, presentationTimeUs, encodedAccessUnitCount, queue.witness)
    // The delegate's own buffer object, every time: media3 requires a retry to pass the same buffer
    // it last saw, and it may have partially consumed it before returning false. Its position is
    // never reset here; the piece keeps the clock and count independently of the buffer's position.
    if (!deliver(piece.buffer, piece.timeUs, piece.count)) return false
    acceptedWitness = piece.witness.takeIf { piece.frames > 0 }
    queue.accepted()
    stage.accepted(piece)
    return true
  }

  /** The format the delegate would actually receive for [format], or null when it is unsupported. */
  fun delegateFormat(format: Format): Format? {
    val source = if (format.pcmEncoding != Format.NO_VALUE) format.pcmEncoding else C.ENCODING_PCM_16BIT
    if (!PorticoAudioOutput.supports(source)) return null
    val target = PorticoAudioOutput.targetEncoding(route(), format.sampleRate, format.channelCount, source)
    return if (target != source) format.buildUpon().setPcmEncoding(target).build() else format
  }

  /** The retained end-of-stream tail, or null when there is none. */
  fun takeTail(): PorticoRateStage.Piece? = stage.takeTail()

  fun acceptedTail(piece: PorticoRateStage.Piece, accepted: Boolean) {
    if (accepted) {
      acceptedWitness = piece.witness.takeIf { piece.frames > 0 }
      stage.accepted(piece)
    }
  }

  /** True once the tail has been accepted, so the delegate may be told the stream ended. */
  fun tailFinished(): Boolean = stage.tailAccepted

  /** True while the wrapper holds output the delegate has not accepted yet, including a previous
   * stream's frames still owed after a configuration change. */
  fun hasPendingOutput(): Boolean = stage.hasPendingOutput || draining != null

  fun flush() {
    acceptedWitness = null
    queue.clear(); gain.reset(); stage.flush()
    draining = null; deferred = null
  }

  fun reset() {
    acceptedWitness = null
    queue.clear(); gain.reset(); stage.reset()
    draining = null; deferred = null
  }

  private fun converted(format: Format, source: Int, target: Int = targetEncoding): Format =
      if (target != source) format.buildUpon().setPcmEncoding(target).build() else format
}

/**
 * The listening sink: the pipeline above, then the standard sink. The decoder's encoding is
 * remembered from [configure], so gain works on real high-resolution samples before media3
 * converts them, and the delegate is configured with the format the gain actually produces.
 */
internal class PorticoMusicSink(
  delegate: AudioSink,
  gain: PorticoAudioGain,
  route: () -> PorticoAudioOutput.Route,
  preferredSampleRate: () -> Int = { 0 },
) : ForwardingAudioSink(delegate) {
  private val pipeline = PorticoMusicPipeline(gain, route, { PorticoAudioSampleRate.mode }, preferredSampleRate)
  private var decodedInput: ByteBuffer? = null
  private var decodedStream: PorticoDecodedStreamStamp? = null

  /** Renderer thread only. Keep the original buffer's stamp while the sink refuses it. */
  fun decodedBuffer(buffer: ByteBuffer, stream: PorticoDecodedStreamStamp?) {
    if (decodedInput == null) { decodedInput = buffer; decodedStream = stream }
    else check(decodedInput === buffer) { "renderer replaced a pending decoded buffer" }
  }
  fun decodedAccepted() { decodedInput = null; decodedStream = null }

  /** Size and channel map of a configuration whose delegate switch is waiting on the drain. */
  private var deferredSize = 0
  private var deferredChannels: IntArray? = null

  override fun configure(format: Format, specifiedBufferSize: Int, outputChannels: IntArray?) {
    val delegate = pipeline.configure(format)
    if (pipeline.drainPending()) {
      // The previous stream still owes frames in its own format. Keep the delegate on its old
      // configuration and let the drain complete under backpressure before switching.
      deferredSize = specifiedBufferSize
      deferredChannels = outputChannels
      return
    }
    super.configure(delegate, specifiedBufferSize, outputChannels)
  }

  /** One buffer to the real sink; kept as a method so the drain can pass it as a function. */
  private fun deliverToDelegate(buffer: ByteBuffer, timeUs: Long, count: Int): Boolean =
      super.handleBuffer(buffer, timeUs, count)

  /** Deliver what the previous stream still owes, then apply the configuration waiting behind it. */
  private fun settleDeferred(): Boolean {
    val deferred = pipeline.deferredFormat() ?: return true
    if (!pipeline.drain(::deliverToDelegate)) return false
    super.configure(deferred, deferredSize, deferredChannels)
    pipeline.drained()
    return true
  }

  override fun handleBuffer(buffer: ByteBuffer, presentationTimeUs: Long, encodedAccessUnitCount: Int): Boolean {
    if (!settleDeferred()) return false
    return pipeline.handle(buffer, presentationTimeUs, encodedAccessUnitCount,
        decodedStream.takeIf { decodedInput === buffer }, deliver = ::deliverToDelegate)
  }

  override fun flush() {
    super.flush()
    val deferred = pipeline.deferredFormat()
    val size = deferredSize
    val channels = deferredChannels
    pipeline.flush()
    decodedAccepted()
    // A flush is a seek: the owed frames are correctly discarded, but the stream media3 already
    // announced still has to reach the delegate in its own format.
    if (deferred != null) super.configure(deferred, size, channels)
  }

  override fun playToEndOfStream() {
    if (!settleDeferred()) return
    // Deliver the resampler's retained tail. The delegate is only told the stream ended after it
    // accepts that tail; a refusal keeps the same piece (buffer, time, count) for the next call.
    val tail = pipeline.takeTail()
    if (tail != null) {
      val accepted = deliverToDelegate(tail.buffer, tail.timeUs, tail.count)
      pipeline.acceptedTail(tail, accepted)
      if (!accepted || !pipeline.tailFinished()) return
    }
    super.playToEndOfStream()
  }

  override fun reset() {
    super.reset()
    pipeline.reset()
    decodedAccepted()
  }

  /** The delegate reports ended once its output is torn down, which it may do while this wrapper
   * still holds owed frames. Only claim the end once nothing is retained or deferred. */
  override fun isEnded(): Boolean =
      !pipeline.drainPending() && !pipeline.hasPendingOutput() && super.isEnded()

  override fun hasPendingData(): Boolean =
      pipeline.drainPending() || pipeline.hasPendingOutput() || super.hasPendingData()

  override fun getFormatSupport(format: Format): Int {
    if (format.sampleMimeType != MimeTypes.AUDIO_RAW) return AudioSink.SINK_FORMAT_UNSUPPORTED
    val delegate = pipeline.delegateFormat(format) ?: return AudioSink.SINK_FORMAT_UNSUPPORTED
    return super.getFormatSupport(delegate)
  }

  override fun supportsFormat(format: Format): Boolean {
    if (format.sampleMimeType != MimeTypes.AUDIO_RAW) return false
    val delegate = pipeline.delegateFormat(format) ?: return false
    return super.supportsFormat(delegate)
  }
}

/** Only listening players use this sink; video keeps its normal output format and passthrough. */
internal class PorticoAudioRenderers(context: Context, private val gain: PorticoAudioGain) : DefaultRenderersFactory(context) {
  override fun buildAudioRenderers(
    context: Context, extensionRendererMode: Int, mediaCodecSelector: androidx.media3.exoplayer.mediacodec.MediaCodecSelector,
    enableDecoderFallback: Boolean, audioSink: AudioSink, eventHandler: android.os.Handler,
    eventListener: androidx.media3.exoplayer.audio.AudioRendererEventListener,
    out: java.util.ArrayList<androidx.media3.exoplayer.Renderer>,
  ) {
    val start = out.size
    super.buildAudioRenderers(context, extensionRendererMode, mediaCodecSelector, enableDecoderFallback,
        audioSink, eventHandler, eventListener, out)
    if (audioSink !is PorticoMusicSink) return
    for (index in start until out.size) {
      if (out[index].javaClass == androidx.media3.exoplayer.audio.MediaCodecAudioRenderer::class.java) {
        out[index] = PorticoGainAudioRenderer(context, getCodecAdapterFactory(), mediaCodecSelector,
            enableDecoderFallback, eventHandler, eventListener, audioSink, gain)
        break
      }
    }
  }

  override fun buildAudioSink(context: Context, enableFloatOutput: Boolean, enableAudioOutputPlaybackParams: Boolean): AudioSink {
    val route = { PorticoAudioOutput.route(context) }
    // The device's own preferred output rate, as the configured fallback — never reported as the
    // routed device's support.
    val preferred = {
      val manager = context.getSystemService(Context.AUDIO_SERVICE) as? android.media.AudioManager
      manager?.getProperty(android.media.AudioManager.PROPERTY_OUTPUT_SAMPLE_RATE)?.toIntOrNull() ?: 0
    }
    // Float is the sink's only high-resolution output and the decision is per stream, not per
    // player: with float enabled the delegate uses it exactly when PorticoAudioOutput selects
    // float for this stream, and a 16-bit selection never enters the float path. A build-time
    // route snapshot therefore cannot make a later float selection narrow to 16-bit.
    val sink = DefaultAudioSink.Builder(context).setEnableFloatOutput(true).build()
    return PorticoMusicSink(sink, gain, route, preferred)
  }
}
