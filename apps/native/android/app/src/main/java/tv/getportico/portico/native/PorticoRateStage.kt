package tv.getportico.portico.native

import java.nio.ByteBuffer
import java.nio.ByteOrder
import kotlin.math.ceil
import kotlin.math.PI
import kotlin.math.cos
import kotlin.math.sin

/**
 * The music sink's rate stage (Smart/Strict/Off, music-plus 14).
 *
 * Ownership: frames are copied out of the incoming buffer on the first call, so a sink retry
 * always sees the same retained piece — same buffer, time and count. Each stream gets its own
 * stage: when the next stream cannot continue this one, the pipeline keeps this stage alive only
 * to drain its remaining frames in this stage's own format, then starts a fresh one. Nothing is
 * ever reinterpreted in another format, and a rate is never changed under held audio.
 *
 * Clock: the outgoing timestamp comes from an output-frame clock that advances only when the
 * delegate accepts a piece, never from the input buffer's start. A refused buffer therefore keeps
 * its original timestamp, and the end-of-stream tail continues the same clock instead of jumping
 * back to the last input buffer's start.
 *
 * Rate conversion uses a normalized, phase-interpolated Blackman-windowed sinc. The cutoff
 * follows the lower Nyquist frequency and its support grows when downsampling; frequencies above
 * the output band are filtered before decimation. Unchanged rates still bypass every filter and
 * copy. Streaming waits for the filter's future support, retaining its original past frames; EOS
 * extends only the first/last sample and emits exactly the positions inside the finite input
 * duration. No extra audible frames, timestamp shift or artificial tail is added. This is a DSP
 * implementation, not a physical output, transparent-resampling or bit-perfect qualification.
 *
 * Modes: Off opens at this device's preferred rate; Strict at the file's rate (route refusal is a
 * caller fact); Smart at the file's rate unless the route is known to refuse it.
 */
internal class PorticoRateStage {
  /** A piece the delegate must accept. [buffer] is the delegate's own object: media3 requires a
   * retry to hand back the same buffer it last saw, and it may have partially consumed that buffer
   * before returning false, so its position is never rewound here. [frames] is captured at creation
   * because the position no longer describes the piece once the delegate has touched it. */
  data class Piece(val buffer: ByteBuffer, val timeUs: Long, val count: Int, val frames: Int,
                   val witness: PorticoGainWitness? = null)

  /** Source-frame runs survive interpolation, deferred formats and draining tails.
   * Unknown or unlike contributors suppress the whole output piece; they never acquire
   * the stamp of the next input, the current format or the binding at acceptance. */
  private class Run(var frames: Int, val witness: PorticoGainWitness?)
  private val provenance = java.util.ArrayDeque<Run>()

  private var sourceRate = 0
  private var targetRate = 0
  private var channels = 0
  private var encoding = 0
  private var frame = 0
  // Exact rational source position: no cumulative floating-point phase drift across chunks.
  private var position = 0L
  private var phase = 0L
  private var filter: SincFilter? = null
  private var pending = FloatArray(0)
  private var pendingCount = 0
  private var startTimeUs = -1L
  private var outputFrames = 0L
  private var lastCount = 0
  private var retained: Piece? = null
  private var tail: Piece? = null
  private var ended = false

  val resampling: Boolean get() = sourceRate > 0 && targetRate > 0 && sourceRate != targetRate

  companion object {
    /**
     * The rate this stream would open at, computed from the mode before anything is changed. The
     * pipeline compares it with the rate of a stage that still holds audio: only an identical
     * target may continue that stage, and any differing target is drained first.
     */
    fun target(
      sourceRate: Int,
      mode: String,
      preferredRate: Int,
      fileRate: Int,
      routeTakesFile: Boolean?,
      allowRateChange: Boolean,
    ): Int {
      if (!allowRateChange || sourceRate <= 0) return sourceRate
      return when (PorticoAudioSampleRate.parse(mode)) {
        PorticoAudioSampleRate.OFF -> if (preferredRate > 0) preferredRate else sourceRate
        PorticoAudioSampleRate.STRICT -> fileRate.takeIf { it > 0 } ?: sourceRate
        // Smart: the file's rate when the route takes it or the route is unknown (never a guess
        // that the preferred rate is supported); the preferred rate only on a known refusal.
        else -> when {
          fileRate <= 0 -> sourceRate
          routeTakesFile == false -> if (preferredRate > 0) preferredRate else fileRate
          else -> fileRate
        }
      }
    }
  }

  /** Start a stream at [targetRate]; the pipeline decides that, and never changes it under audio. */
  fun configure(sourceRate: Int, channels: Int, encoding: Int, targetRate: Int): Int {
    val outputRate = if (targetRate > 0) targetRate else sourceRate
    require(sourceRate > 0 && channels > 0 && PorticoAudioOutput.supports(encoding)) {
      "unsupported rate stage format"
    }
    val nextFilter = if (sourceRate == outputRate) null else SincFilter(sourceRate, outputRate)
    clear()
    this.sourceRate = sourceRate
    this.channels = channels
    this.encoding = encoding
    frame = PorticoAudioOutput.bytesPerSample(encoding) * channels
    this.targetRate = outputRate
    filter = nextFilter
    return this.targetRate
  }

  /**
   * True when the next stream runs at this stage's exact source format and output rate **and** this
   * stage is still a contiguous streaming stage. A stage that has entered end of stream — a tail
   * offered to the delegate, or an end already requested or drained — is never continued, even when
   * the formats match: its output is still owed and must be delivered as a drain first, or the
   * delegate would be reconfigured while it still owns that buffer.
   */
  fun continuesWith(sourceRate: Int, channels: Int, encoding: Int, targetRate: Int): Boolean =
      !ended && tail == null && retained == null &&
          sourceRate == this.sourceRate && channels == this.channels && encoding == this.encoding &&
          targetRate == this.targetRate

  /** One decoder buffer in; the piece the delegate must accept (same identity on a retry). */
  fun process(input: ByteBuffer, presentationTimeUs: Long, count: Int, witness: PorticoGainWitness? = null): Piece {
    retained?.let { return it }
    lastCount = count
    if (startTimeUs < 0) startTimeUs = presentationTimeUs
    if (!resampling) {
      val frames = if (frame > 0) input.remaining() / frame else 0
      return Piece(input, presentationTimeUs, count, frames,
          witness.takeIf { frames > 0 && input.remaining() % frame == 0 }).also { retained = it }
    }
    copy(input, witness)
    return produce(eos = false).also { retained = it }
  }

  /** The delegate accepted [piece]; retire it and advance the output clock by its own frames. */
  fun accepted(piece: Piece?) {
    if (piece == null) return
    if (piece === retained) {
      outputFrames += piece.frames
      retained = null
    }
    if (piece === tail) {
      tail = null
      ended = true
    }
  }

  /** The end-of-stream tail, or null. Retained until [accepted]; [ended] waits for that. */
  fun takeTail(): Piece? {
    if (!resampling) {
      ended = true
      return null
    }
    tail?.let { return it }
    if (ended) return null
    if (pendingCount <= 0) {
      ended = true
      return null
    }
    val piece = produce(eos = true)
    if (piece.frames <= 0) {
      ended = true
      return null
    }
    return piece.also { tail = it }
  }

  /** True once the retained tail has been accepted, so the delegate may finish. */
  val tailAccepted: Boolean get() = ended

  /** True while a piece the delegate has not accepted is still held, tail included. */
  val hasPendingOutput: Boolean get() = retained != null || tail != null

  /** True while this stage still holds audio of the stream it was last configured for. */
  val hasUnfinishedAudio: Boolean get() = pendingCount > 0 || retained != null || tail != null

  fun flush() = clear()

  fun reset() {
    clear()
    sourceRate = 0
    targetRate = 0
    channels = 0
    encoding = 0
    frame = 0
    filter = null
  }

  /** Retire every retained piece and the clock with it; nothing of this stream survives. */
  private fun clear() {
    pending = FloatArray(0)
    provenance.clear()
    pendingCount = 0
    position = 0L
    phase = 0L
    retained = null
    tail = null
    ended = false
    startTimeUs = -1L
    outputFrames = 0
    lastCount = 0
  }

  private fun timeFor(framesBefore: Long): Long =
    if (startTimeUs < 0 || targetRate <= 0) startTimeUs.coerceAtLeast(0)
    else startTimeUs + Math.round(framesBefore * 1_000_000.0 / targetRate)

  private fun copy(input: ByteBuffer, witness: PorticoGainWitness?) {
    val source = input.duplicate().order(ByteOrder.nativeOrder())
    val frames = if (frame > 0) source.remaining() / frame else 0
    if (frames <= 0) return
    val needed = (pendingCount + frames) * channels
    if (needed > pending.size) {
      val grown = FloatArray(maxOf(needed, pending.size * 2, 1024))
      System.arraycopy(pending, 0, grown, 0, pendingCount * channels)
      pending = grown
    }
    repeat(frames) { index ->
      for (channel in 0 until channels) {
        pending[(pendingCount + index) * channels + channel] = read(source)
      }
    }
    val proof = witness.takeIf { source.remaining() == 0 }
    val last = provenance.peekLast()
    if (last != null && (last.witness?.sameAs(proof) == true || last.witness == null && proof == null)) last.frames += frames
    else provenance.addLast(Run(frames, proof))
    pendingCount += frames
  }

  /** Every real source frame in the filter support must have the same original witness. */
  private fun witnessFor(first: Int, last: Int): PorticoGainWitness? {
    if (first < 0 || last < first) return null
    var offset = 0
    var proof: PorticoGainWitness? = null
    for (run in provenance) {
      val end = offset + run.frames
      if (end > first && offset <= last) {
        val contributor = run.witness ?: return null
        if (proof != null && !proof.sameAs(contributor)) return null
        proof = contributor
      }
      offset = end
      if (offset > last) return proof
    }
    return null
  }

  private fun consumeProvenance(frames: Int) {
    var remaining = frames
    while (remaining > 0 && provenance.isNotEmpty()) {
      val first = provenance.peekFirst()
      val consumed = minOf(remaining, first.frames)
      first.frames -= consumed
      remaining -= consumed
      if (first.frames == 0) provenance.removeFirst()
    }
  }

  /** Emit every target position whose filter support is available; flush the finite EOS once. */
  private fun produce(eos: Boolean): Piece {
    val kernel = checkNotNull(filter)
    val last = pendingCount - 1
    val capacity = maxOf(1, ceil(pendingCount.toDouble() * targetRate / sourceRate).toInt() + 2)
    val out = FloatArray(capacity * channels)
    var produced = 0
    var firstContributor = pendingCount
    var lastContributor = -1
    while (position < pendingCount && produced < capacity) {
      val at = position.toInt()
      // A future decoder buffer must supply the right wing during streaming. Only EOS may
      // extend the final sample. Keeping the left wing also preserves cross-buffer provenance.
      if (!eos && position + kernel.radius >= pendingCount) break
      val weights = kernel.weights(phase.toDouble() / targetRate)
      firstContributor = minOf(firstContributor, maxOf(0, at - kernel.radius))
      lastContributor = maxOf(lastContributor, minOf(last, at + kernel.radius))
      for (channel in 0 until channels) {
        var value = 0.0
        for (tap in weights.indices) {
          val sourceFrame = (at + tap - kernel.radius).coerceIn(0, last)
          value += pending[sourceFrame * channels + channel] * weights[tap]
        }
        out[produced * channels + channel] = value.toFloat()
      }
      produced++
      phase += sourceRate
      position += phase / targetRate
      phase %= targetRate
    }
    // Resolve proof BEFORE removing history. Unlike/unknown frames anywhere in the FIR support
    // suppress this entire piece, including an old source's contribution to a new source's edge.
    val proof = if (produced > 0) witnessFor(firstContributor, lastContributor) else null
    val consumed = minOf(maxOf(0L, position - kernel.radius), pendingCount.toLong()).toInt()
    if (consumed > 0) {
      consumeProvenance(consumed)
      System.arraycopy(pending, consumed * channels, pending, 0, (pendingCount - consumed) * channels)
      pendingCount -= consumed
      position -= consumed
    }
    return Piece(encode(produced, out), timeFor(outputFrames), lastCount, produced, proof)
  }

  /** Bounded polyphase FIR. Adjacent normalized phases are interpolated, not rounded to a phase.
   * The 32 zero crossings at the lower rate give a fixed transition width in the output band.
   * Tables belong to this stage/format; no process-global cache or current-source lookup. */
  private class SincFilter(sourceRate: Int, targetRate: Int) {
    private val scale = minOf(1.0, targetRate.toDouble() / sourceRate)
    // Covers every ratio in the existing 8–768 kHz playable-plan range (at most 96:1).
    // Refuse a larger FIR before allocation; unchanged rates never enter this bound.
    val radius = ceil(32.0 / scale).also { require(it <= 3072.0) { "unsupported rate ratio" } }.toInt()
    private val taps = radius * 2 + 1
    private val phases = 256
    private val table = FloatArray((phases + 1) * taps)
    private val interpolated = DoubleArray(taps)

    init {
      val cutoff = 0.94 * scale
      for (phase in 0..phases) {
        val fraction = phase.toDouble() / phases
        var sum = 0.0
        for (tap in 0 until taps) {
          val distance = tap - radius - fraction
          val windowAt = distance / radius
          val window = if (kotlin.math.abs(windowAt) >= 1.0) 0.0 else
            0.42 + 0.5 * cos(PI * windowAt) + 0.08 * cos(2 * PI * windowAt)
          val x = PI * cutoff * distance
          val value = cutoff * (if (kotlin.math.abs(x) < 1e-12) 1.0 else sin(x) / x) * window
          table[phase * taps + tap] = value.toFloat()
          sum += value
        }
        check(sum.isFinite() && sum > 0)
        for (tap in 0 until taps) table[phase * taps + tap] =
            (table[phase * taps + tap] / sum).toFloat()
      }
    }

    fun weights(fraction: Double): DoubleArray {
      val at = (fraction * phases).coerceIn(0.0, phases.toDouble())
      val lower = at.toInt().coerceAtMost(phases - 1)
      val mix = at - lower
      for (tap in 0 until taps) {
        val a = table[lower * taps + tap].toDouble()
        val b = table[(lower + 1) * taps + tap].toDouble()
        interpolated[tap] = a + (b - a) * mix
      }
      return interpolated
    }
  }

  private fun encode(frames: Int, source: FloatArray): ByteBuffer {
    val out = ByteBuffer.allocateDirect(frames * frame).order(ByteOrder.nativeOrder())
    for (index in 0 until frames * channels) write(out, source[index])
    out.flip()
    return out
  }

  private fun read(input: ByteBuffer): Float = when (encoding) {
    android.media.AudioFormat.ENCODING_PCM_16BIT -> input.short / 32768f
    android.media.AudioFormat.ENCODING_PCM_24BIT_PACKED -> read24(input) / 8388608f
    android.media.AudioFormat.ENCODING_PCM_32BIT -> (input.int / 2147483648.0).toFloat()
    else -> input.float
  }

  private fun read24(input: ByteBuffer): Int {
    val raw = (input.get().toInt() and 0xFF) or
        ((input.get().toInt() and 0xFF) shl 8) or
        ((input.get().toInt() and 0xFF) shl 16)
    return if (raw and 0x800000 != 0) raw - 0x1000000 else raw
  }

  private fun write(output: ByteBuffer, value: Float) {
    val sample = if (value.isFinite()) value.coerceIn(-1f, 1f) else 0f
    when (encoding) {
      android.media.AudioFormat.ENCODING_PCM_16BIT ->
        output.putShort((sample * 32768f).toInt().coerceIn(-32768, 32767).toShort())
      android.media.AudioFormat.ENCODING_PCM_24BIT_PACKED -> {
        val bits = (sample * 8388608f).toInt().coerceIn(-8388608, 8388607)
        output.put((bits and 0xFF).toByte())
        output.put((bits shr 8 and 0xFF).toByte())
        output.put((bits shr 16 and 0xFF).toByte())
      }
      android.media.AudioFormat.ENCODING_PCM_32BIT ->
        output.putInt((sample.toDouble() * 2147483648.0).toLong()
          .coerceIn(Int.MIN_VALUE.toLong(), Int.MAX_VALUE.toLong()).toInt())
      else -> output.putFloat(sample)
    }
  }
}
