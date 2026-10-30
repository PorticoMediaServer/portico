package tv.getportico.portico.native

import androidx.media3.common.C
import androidx.media3.common.Format
import androidx.media3.common.MimeTypes
import java.nio.ByteBuffer
import java.nio.ByteOrder

/** Run in the Android unit-test runtime with Media3 1.10.1 on the classpath. Authored negatives. */
fun main() {
  // Unity in one encoding is the same object: no copy, no rounding, a byte-exact pass.
  val unity = PorticoAudioGain()
  val stereo = ByteBuffer.allocateDirect(12).order(ByteOrder.nativeOrder())
  for (sample in shortArrayOf(4000, -4000, 1, -1, 0, 0)) stereo.putShort(sample)
  stereo.flip()
  check(unity.apply(stereo, C.ENCODING_PCM_16BIT, C.ENCODING_PCM_16BIT, 2, 48000) === stereo)

  // Amplification on 24-bit packed audio keeps the source precision and clips at full scale.
  val loud = PorticoAudioGain()
  loud.target = 2f
  loud.reset()
  val packed = ByteBuffer.allocateDirect(12).order(ByteOrder.nativeOrder())
  for (sample in listOf(0x200000, -0x200000, 0x7FFFFF, -0x800000)) put24(packed, sample)
  packed.flip()
  val scaled = loud.apply(packed, C.ENCODING_PCM_24BIT, C.ENCODING_PCM_24BIT, 2, 48000).order(ByteOrder.nativeOrder())
  check(scaled.remaining() == 12)
  check(read24(scaled) == 0x400000 && read24(scaled) == -0x400000)
  check(read24(scaled) == 0x7FFFFF && read24(scaled) == -0x800000)
  check(packed.position() == 0) // the decoder's buffer is never consumed by gain

  // Float clips at full scale instead of wrapping.
  val floats = PorticoAudioGain()
  floats.target = 2f
  floats.reset()
  val stream = ByteBuffer.allocateDirect(8).order(ByteOrder.nativeOrder()).putFloat(0.25f).putFloat(0.75f)
  stream.flip()
  val copied = floats.apply(stream, C.ENCODING_PCM_FLOAT, C.ENCODING_PCM_FLOAT, 2, 48000).order(ByteOrder.nativeOrder())
  check(copied.float == 0.5f && copied.float == 1f)

  // A float profile is the only honest high-resolution target: configure tells the delegate the
  // format the gain actually produces, and a refused buffer is retried as the same object.
  val floatRoute = PorticoAudioOutput.Route(
      listOf(PorticoAudioOutput.Device("dac", listOf(PorticoAudioOutput.Profile(C.ENCODING_PCM_FLOAT, 96000, 2)))), true)
  val gain = PorticoAudioGain().also { it.target = 2f; it.reset() }
  val pipeline = PorticoMusicPipeline(gain, route = { floatRoute })
  val configured = pipeline.configure(format(C.ENCODING_PCM_24BIT, 96000, 2))
  check(configured.pcmEncoding == C.ENCODING_PCM_FLOAT)

  val handed = ByteBuffer.allocateDirect(18).order(ByteOrder.nativeOrder())
  for (sample in listOf(0x200000, -0x200000, 0x400000, -0x400000, 0, 0)) put24(handed, sample)
  handed.flip()
  val handedEnd = handed.limit()
  check(!pipeline.handle(handed, 1000L, 1) { _, _, _ -> false })
  val retried = pipeline.handle(handed, 1000L, 1) { outgoing, _, _ ->
    check(outgoing.position() == 0 && outgoing.limit() == 24)
    check(readFloat(outgoing) == 0.5f)
    true
  }
  check(retried)
  check(handed.position() == handedEnd)

  // A partial consume keeps the same object: the retry sees the delegate's own position, and a
  // gain change between the refusal and the retry cannot rebuild or rescale it.
  val partialGain = PorticoAudioGain().also { it.target = 2f; it.reset() }
  val partial = PorticoMusicPipeline(partialGain, route = { floatRoute })
  partial.configure(format(C.ENCODING_PCM_24BIT, 96000, 2))
  val partialInput = ByteBuffer.allocateDirect(18).order(ByteOrder.nativeOrder())
  for (sample in listOf(0x200000, -0x200000, 0x400000, -0x400000, 0, 0)) put24(partialInput, sample)
  partialInput.flip()
  var partialObject: ByteBuffer? = null
  check(!partial.handle(partialInput, 2000L, 1) { outgoing, _, _ ->
    partialObject = outgoing
    outgoing.position(outgoing.position() + 4)
    false
  })
  partialGain.target = 4f
  check(partial.handle(partialInput, 2000L, 1) { outgoing, _, _ ->
    check(outgoing === partialObject)
    check(outgoing.position() == 4)
    true
  })
  check(partialInput.position() == partialInput.limit())

  // Reconfiguring while the delegate still holds a buffer is refused before any source changes.
  val guardedConfigure = PorticoMusicPipeline(PorticoAudioGain(), route = { floatRoute })
  guardedConfigure.configure(format(C.ENCODING_PCM_24BIT, 96000, 2))
  val pendingInput2 = ByteBuffer.allocateDirect(6).order(ByteOrder.nativeOrder())
  put24(pendingInput2, 0x200000); put24(pendingInput2, -0x200000)
  pendingInput2.flip()
  check(!guardedConfigure.handle(pendingInput2, 5L, 1) { _, _, _ -> false })
  var refused = false
  try {
    guardedConfigure.configure(format(C.ENCODING_PCM_16BIT, 48000, 2))
  } catch (_: IllegalStateException) {
    refused = true
  }
  check(refused)
  check(guardedConfigure.handle(pendingInput2, 5L, 1) { outgoing, _, _ ->
    check(outgoing.position() == 0)
    true
  })

  // PCM8 and any unknown encoding are refused before a byte can reach a 16-bit delegate.
  val unsupported = PorticoMusicPipeline(PorticoAudioGain(), route = { floatRoute })
  check(unsupported.delegateFormat(format(C.ENCODING_PCM_8BIT, 48000, 2)) == null)
  check(unsupported.delegateFormat(format(12345, 48000, 2)) == null)
  var pcm8Refused = false
  try {
    unsupported.configure(format(C.ENCODING_PCM_8BIT, 48000, 2))
  } catch (_: IllegalArgumentException) {
    pcm8Refused = true
  }
  check(pcm8Refused)
  var gainRefused = false
  try {
    PorticoAudioGain().apply(ByteBuffer.allocateDirect(4).order(ByteOrder.nativeOrder()), C.ENCODING_PCM_8BIT, C.ENCODING_PCM_16BIT, 2, 48000)
  } catch (_: IllegalArgumentException) {
    gainRefused = true
  }
  check(gainRefused)

  // A different retry identity while a buffer is still pending is a broken contract, not a reset.
  val guarded = PorticoMusicPipeline(gain, route = { floatRoute })
  guarded.configure(format(C.ENCODING_PCM_24BIT, 96000, 2))
  val held = ByteBuffer.allocateDirect(6).order(ByteOrder.nativeOrder())
  put24(held, 0x200000)
  put24(held, -0x200000)
  held.flip()
  guarded.handle(held, 10L, 1) { _, _, _ -> false }
  var rejected = false
  try {
    guarded.handle(held, 11L, 1) { _, _, _ -> true }
  } catch (_: IllegalStateException) {
    rejected = true
  }
  check(rejected)

  // A 24-bit-only profile has no capable delegate path: the pipeline narrows to 16-bit honestly.
  val integerRoute = PorticoAudioOutput.Route(
      listOf(PorticoAudioOutput.Device("dac", listOf(PorticoAudioOutput.Profile(C.ENCODING_PCM_24BIT, 96000, 2)))), true)
  val narrowing = PorticoMusicPipeline(PorticoAudioGain()) { integerRoute }
  check(narrowing.configure(format(C.ENCODING_PCM_24BIT, 96000, 2)).pcmEncoding == C.ENCODING_PCM_16BIT)

  // The route is read per configure, so a new device is reflected without rebuilding the player.
  var route = integerRoute
  val reconfiguring = PorticoMusicPipeline(PorticoAudioGain()) { route }
  check(reconfiguring.configure(format(C.ENCODING_PCM_24BIT, 96000, 2)).pcmEncoding == C.ENCODING_PCM_16BIT)
  route = floatRoute
  check(reconfiguring.configure(format(C.ENCODING_PCM_24BIT, 96000, 2)).pcmEncoding == C.ENCODING_PCM_FLOAT)

  // Exact float passthrough at unity; integer high-resolution never claims 24-bit output.
  val exactFloat = PorticoAudioOutput.choose(floatRoute, 96000, 2, C.ENCODING_PCM_FLOAT, true)
  check(exactFloat.encoding == C.ENCODING_PCM_FLOAT && exactFloat.bytePassthrough && exactFloat.bitPerfect)
  val converted = PorticoAudioOutput.choose(floatRoute, 96000, 2, C.ENCODING_PCM_24BIT, true)
  check(converted.encoding == C.ENCODING_PCM_FLOAT && !converted.bitPerfect && converted.reason == "device-float")
  val integerOnly = PorticoAudioOutput.choose(integerRoute, 96000, 2, C.ENCODING_PCM_24BIT, true)
  check(integerOnly.encoding == C.ENCODING_PCM_16BIT && !integerOnly.bitPerfect && integerOnly.reason == "device-16bit")

  // Profiles are per combination: 16-bit at 192 kHz plus 24-bit at 48 kHz is not 24-bit at 192 kHz.
  val split = PorticoAudioOutput.Device("split",
      listOf(PorticoAudioOutput.Profile(C.ENCODING_PCM_16BIT, 192000, 2), PorticoAudioOutput.Profile(C.ENCODING_PCM_24BIT, 48000, 2)))
  val fabricated = PorticoAudioOutput.choose(PorticoAudioOutput.Route(listOf(split), true), 192000, 2, C.ENCODING_PCM_24BIT, true)
  check(fabricated.encoding == C.ENCODING_PCM_16BIT && !fabricated.bitPerfect && fabricated.reason == "device-16bit")

  // No unambiguous route is the conservative floor, never an inferred hardware claim.
  val two = PorticoAudioOutput.Route(listOf(floatRoute.devices.first(), split), false)
  val unknown = PorticoAudioOutput.choose(two, 96000, 2, C.ENCODING_PCM_24BIT, true)
  check(unknown.encoding == C.ENCODING_PCM_16BIT && unknown.reason == "route-unknown" && !unknown.bitPerfect)
  val unknown16 = PorticoAudioOutput.choose(two, 48000, 2, C.ENCODING_PCM_16BIT, true)
  check(unknown16.bytePassthrough && !unknown16.bitPerfect && unknown16.reason == "route-unknown")

  // A float-only device does not upgrade a 16-bit stream, and capability alone is not bit perfect.
  val sixteen = PorticoAudioOutput.choose(floatRoute, 96000, 2, C.ENCODING_PCM_16BIT, true)
  check(sixteen.encoding == C.ENCODING_PCM_16BIT && sixteen.bytePassthrough && !sixteen.bitPerfect)
  gainProvenanceFoundations()
  bandlimitedRateFoundations()
}

/** Test-only stream stamps. Production must obtain them from the renderer, never these fixtures. */
private fun gainProvenanceFoundations() {
  val first = PorticoDecodedStreamStamp(PorticoAudioSourceTag("session-a", 1L, "item-a", "plan-a", "lease", 1L), Any())
  val next = PorticoDecodedStreamStamp(PorticoAudioSourceTag("session-b", 2L, "item-b", "plan-b", "lease", 2L), Any())
  val gain = PorticoAudioGain()
  gain.components(2f, 1.0, true, first.source); gain.reset()
  check(gain.applyObserved(provenancePcm(8), C.ENCODING_PCM_FLOAT, C.ENCODING_PCM_FLOAT, 1, 48000).normalizationCoefficient == 2f)
  gain.components(2f, 0.5, true); gain.reset()
  check(gain.applyObserved(provenancePcm(8), C.ENCODING_PCM_FLOAT, C.ENCODING_PCM_FLOAT, 1, 48000).normalizationCoefficient == null)
  gain.target = 2f; gain.reset()
  check(gain.applyObserved(provenancePcm(8), C.ENCODING_PCM_FLOAT, C.ENCODING_PCM_FLOAT, 1, 48000).normalizationCoefficient == null)
  gain.components(2f, 1.0, true, first.source); gain.reset()
  gain.components(3f, 1.0, true)
  check(gain.applyObserved(provenancePcm(1600), C.ENCODING_PCM_FLOAT, C.ENCODING_PCM_FLOAT, 1, 48000).normalizationCoefficient == null)
  check(gain.applyObserved(provenancePcm(8), C.ENCODING_PCM_FLOAT, C.ENCODING_PCM_FLOAT, 1, 48000).normalizationCoefficient == 3f)

  gain.components(2f, 1.0, true, first.source); gain.reset()
  val queue = PorticoGainQueue(gain)
  val input = provenancePcm(8)
  val held = queue.bufferFor(input, C.ENCODING_PCM_FLOAT, C.ENCODING_PCM_FLOAT, 1, 48000, 0L, 1, first)
  val proof = queue.witness
  check(proof != null && proof.stream === first && proof.coefficient == 2f)
  gain.components(4f, 1.0, true)
  check(queue.bufferFor(input, C.ENCODING_PCM_FLOAT, C.ENCODING_PCM_FLOAT, 1, 48000, 0L, 1, next) === held)
  check(queue.witness === proof) // neither today's control nor a later stamp retags a retry
  queue.accepted(); check(queue.witness == null)
  gain.acceptedWitness = proof
  gain.outputStream = first
  check(gain.currentWitness() == null) // controls changed while the original buffer was retained
  gain.components(2f, 1.0, true, next.source); gain.reset()
  queue.bufferFor(provenancePcm(8), C.ENCODING_PCM_FLOAT, C.ENCODING_PCM_FLOAT, 1, 48000, 0L, 1, first)
  check(queue.witness == null) // same coefficient with the wrong source is never proof
  queue.accepted()
  gain.components(2f, 1.0, true, first.source); gain.reset()
  val stable = gain.applyObserved(provenancePcm(8), C.ENCODING_PCM_FLOAT, C.ENCODING_PCM_FLOAT, 1, 48000)
  val currentProof = gain.witness(first, stable)!!
  gain.acceptedWitness = currentProof
  check(gain.currentWitness() === currentProof)
  gain.components(2f, 1.0, true, first.source)
  check(gain.currentWitness() === currentProof) // equal controls preserve an accepted observation
  gain.outputStream = PorticoDecodedStreamStamp(first.source, first.periodUid)
  check(gain.currentWitness() == null) // a reset/new decoded stream needs its own accepted piece
  gain.outputStream = null
  check(gain.currentWitness() == null)

  val stage = PorticoRateStage()
  stage.configure(44100, 1, C.ENCODING_PCM_FLOAT, 48000)
  val a = PorticoGainWitness(first, 2f)
  val b = PorticoGainWitness(next, 3f)
  val firstPiece = stage.process(provenancePcm(96), 0L, 1, a)
  check(firstPiece.witness?.sameAs(a) == true)
  check(stage.process(provenancePcm(96), 1000L, 1, b) === firstPiece) // retained delegate piece
  stage.accepted(firstPiece)
  val boundary = stage.process(provenancePcm(96), 2000L, 1, b)
  check(boundary.frames > 0 && boundary.witness == null) // the FIR support crosses original streams
  stage.accepted(boundary)
  val current = stage.process(provenancePcm(96), 3000L, 1, b)
  check(current.witness?.sameAs(b) == true)
  stage.accepted(current)
  val tail = stage.takeTail()!!
  check(tail.witness?.sameAs(b) == true)
  check(stage.takeTail() === tail)
  stage.accepted(tail)

  val changing = PorticoRateStage()
  changing.configure(44100, 1, C.ENCODING_PCM_FLOAT, 48000)
  val beforeChange = changing.process(provenancePcm(96), 0L, 1, a)
  changing.accepted(beforeChange)
  val changedGain = changing.process(provenancePcm(96), 1000L, 1, PorticoGainWitness(first, 3f))
  check(changedGain.witness == null) // one stream with unlike actual coefficients is also mixed
  changing.accepted(changedGain)
  check(changing.process(provenancePcm(96), 2000L, 1).witness == null) // unknown never inherits a prior proof

  val route = PorticoAudioOutput.Route(listOf(PorticoAudioOutput.Device("provenance-test", listOf(
      PorticoAudioOutput.Profile(C.ENCODING_PCM_FLOAT, 44100, 1),
      PorticoAudioOutput.Profile(C.ENCODING_PCM_FLOAT, 48000, 1)))), true)
  gain.components(2f, 1.0, true, first.source); gain.reset()
  val pipeline = PorticoMusicPipeline(gain, route = { route }, sampleRateMode = { "off" }, preferredSampleRate = { 48000 })
  pipeline.configure(format(C.ENCODING_PCM_FLOAT, 44100, 1))
  check(pipeline.handle(provenancePcm(96), 0L, 1, first) { _, _, _ -> true })
  check(pipeline.acceptedWitness?.stream === first)
  pipeline.configure(format(C.ENCODING_PCM_FLOAT, 32000, 1))
  check(pipeline.acceptedWitness == null)
  check(!pipeline.drain { _, _, _ -> false })
  check(pipeline.acceptedWitness == null)
  check(pipeline.drain { _, _, _ -> true })
  check(pipeline.acceptedWitness?.stream === first) // old tail survives the deferred format
  pipeline.drained(); pipeline.flush()
  check(pipeline.acceptedWitness == null)
  // Even a settled, separately known gain is not a stream witness without the actual renderer.
  pipeline.configure(format(C.ENCODING_PCM_FLOAT, 48000, 1))
  check(pipeline.handle(provenancePcm(8), 0L, 1) { _, _, _ -> true })
  check(pipeline.acceptedWitness == null)
}

/** Exercise the actual stage, including split input, backpressure, finite tail and channel identity.
 * These are authored sources only; no signal, decoder, output device or audible pass is inferred. */
private fun bandlimitedRateFoundations() {
  fun render(sourceRate: Int, targetRate: Int, channels: Int, samples: FloatArray,
             chunks: IntArray): FloatArray {
    val stage = PorticoRateStage()
    stage.configure(sourceRate, channels, C.ENCODING_PCM_FLOAT, targetRate)
    val output = ArrayList<Float>()
    var acceptedFrames = 0
    fun accept(piece: PorticoRateStage.Piece) {
      check(piece.timeUs == 1234L + Math.round(acceptedFrames * 1_000_000.0 / targetRate))
      val copy = piece.buffer.duplicate().order(ByteOrder.nativeOrder())
      check(copy.remaining() == piece.frames * channels * 4)
      while (copy.hasRemaining()) output.add(copy.float)
      acceptedFrames += piece.frames
      stage.accepted(piece)
    }
    var offset = 0
    var chunk = 0
    val frames = samples.size / channels
    while (offset < frames) {
      val count = minOf(chunks[chunk++ % chunks.size], frames - offset)
      val input = ByteBuffer.allocateDirect(count * channels * 4).order(ByteOrder.nativeOrder())
      repeat(count * channels) { input.putFloat(samples[offset * channels + it]) }
      input.flip()
      val piece = stage.process(input, 1234L + Math.round(offset * 1_000_000.0 / sourceRate), 1)
      check(stage.process(input, piece.timeUs, 1) === piece) // one retained piece across backpressure
      check(input.position() == 0) // the decoder's input is not consumed by this stage
      accept(piece)
      offset += count
    }
    val tail = stage.takeTail()
    if (tail != null) {
      check(stage.takeTail() === tail)
      accept(tail)
    }
    check(stage.takeTail() == null && stage.tailAccepted)
    val expectedFrames = (frames.toLong() * targetRate + sourceRate - 1) / sourceRate
    check(acceptedFrames.toLong() == expectedFrames)
    return output.toFloatArray()
  }

  // The right channel is above the target Nyquist: integer-position linear decimation aliases it
  // loudly to 12 kHz. The left channel's independent 3 kHz tone must retain its level and channel.
  val tone = FloatArray(8192 * 2) { index ->
    val frame = index / 2
    val hz = if (index % 2 == 0) 3000.0 else 36000.0
    (0.25 * kotlin.math.sin(2 * Math.PI * hz * frame / 96000)).toFloat()
  }
  val decimated = render(96000, 48000, 2, tone, intArrayOf(97, 13, 1024, 1))
  fun rms(channel: Int): Double {
    var energy = 0.0
    val frames = decimated.size / 2
    for (frame in 256 until frames - 256) {
      val value = decimated[frame * 2 + channel].toDouble()
      energy += value * value
    }
    return kotlin.math.sqrt(energy / (frames - 512))
  }
  check(kotlin.math.abs(rms(0) - 0.25 / kotlin.math.sqrt(2.0)) < 0.002)
  check(rms(1) < 0.0002) // actual stopband energy, not just a selected filter coefficient

  for ((sourceRate, targetRate) in listOf(44100 to 48000, 96000 to 44100, 48000 to 32000)) {
    val source = FloatArray(4099) { (0.2 * kotlin.math.sin(2 * Math.PI * 1100 * it / sourceRate)).toFloat() }
    val whole = render(sourceRate, targetRate, 1, source, intArrayOf(source.size))
    val split = render(sourceRate, targetRate, 1, source, intArrayOf(1, 7, 127, 509))
    check(whole.contentEquals(split)) // phase/history and EOS do not depend on decoder chunking
  }
  // Tiny inputs flush their whole finite duration, with no artificial filter-delay padding.
  for (size in listOf(1, 2, 3, 31)) {
    val constant = render(44100, 48000, 1, FloatArray(size) { 0.25f }, intArrayOf(1))
    check(constant.all { kotlin.math.abs(it - 0.25f) < 0.00001f })
  }
  // An unchanged rate bypasses even allocation/filtering; unity precision remains bit-exact.
  val exact = PorticoRateStage()
  exact.configure(96000, 1, C.ENCODING_PCM_FLOAT, 96000)
  val original = provenancePcm(17)
  val pass = exact.process(original, 0L, 1)
  check(pass.buffer === original && pass.frames == 17)
  exact.accepted(pass)
  check(exact.takeTail() == null)
  exact.configure(7350, 1, C.ENCODING_PCM_FLOAT, 7350)
  val lowRate = provenancePcm(7)
  check(exact.process(lowRate, 0L, 1).buffer === lowRate) // no new global rate ceiling/floor

  // Flush retires retained frames, phase, provenance and clock, without borrowing the old tail.
  val reset = PorticoRateStage()
  reset.configure(44100, 1, C.ENCODING_PCM_FLOAT, 48000)
  reset.accepted(reset.process(provenancePcm(3), 0L, 1))
  reset.flush()
  val fresh = reset.process(provenancePcm(96), 50000L, 1)
  check(fresh.timeUs == 50000L)
  reset.accepted(fresh)
  check(reset.takeTail() != null)
}

private fun provenancePcm(frames: Int): ByteBuffer {
  val buffer = ByteBuffer.allocateDirect(frames * 4).order(ByteOrder.nativeOrder())
  repeat(frames) { buffer.putFloat(0.25f) }
  buffer.flip()
  return buffer
}

private fun format(encoding: Int, rate: Int, channels: Int): Format =
    Format.Builder().setSampleMimeType(MimeTypes.AUDIO_RAW).setPcmEncoding(encoding)
        .setSampleRate(rate).setChannelCount(channels).build()

private fun put24(buffer: ByteBuffer, sample: Int) {
  buffer.put((sample and 0xFF).toByte())
  buffer.put((sample shr 8 and 0xFF).toByte())
  buffer.put((sample shr 16 and 0xFF).toByte())
}

private fun read24(input: ByteBuffer): Int {
  val raw = (input.get().toInt() and 0xFF) or ((input.get().toInt() and 0xFF) shl 8) or ((input.get().toInt() and 0xFF) shl 16)
  return if (raw and 0x800000 != 0) raw - 0x1000000 else raw
}

private fun readFloat(input: ByteBuffer): Float = input.order(ByteOrder.nativeOrder()).float
