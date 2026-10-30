package tv.getportico.portico.native

import android.content.Context
import android.media.MediaCodec
import android.os.Handler
import androidx.media3.common.Format
import androidx.media3.common.Timeline
import androidx.media3.exoplayer.audio.AudioRendererEventListener
import androidx.media3.exoplayer.audio.MediaCodecAudioRenderer
import androidx.media3.exoplayer.mediacodec.MediaCodecAdapter
import androidx.media3.exoplayer.mediacodec.MediaCodecSelector
import androidx.media3.exoplayer.source.MediaSource
import java.nio.ByteBuffer
import java.util.ArrayDeque

/** Mirrors the *output stream* lifecycle of the pinned Media3 1.10.1 renderer.
 * Never infer a stream from timestamps, sink discontinuities or the player's current item.
 * The offset callback is an assignment notification, including equal numeric offsets and the
 * first stream. onProcessedStreamChange is unsuitable: its initial callback is optional.
 */
internal class PorticoGainAudioRenderer(
  context: Context, factory: MediaCodecAdapter.Factory, selector: MediaCodecSelector,
  fallback: Boolean, handler: Handler, listener: AudioRendererEventListener,
  private val musicSink: PorticoMusicSink, private val gain: PorticoAudioGain,
) : MediaCodecAudioRenderer(context, factory, selector, fallback, handler, listener, musicSink) {
  private class Origin(val periodUid: Any, val source: PorticoAudioSourceTag?) {
    fun stamp(): PorticoDecodedStreamStamp? = source?.let { PorticoDecodedStreamStamp(it, periodUid) }
  }
  private val pending = ArrayDeque<Origin>()
  private var output: Origin? = null

  private fun original(mediaPeriodId: MediaSource.MediaPeriodId): Origin {
    // Resolve exactly the renderer-supplied period in its own timeline. An unavailable period,
    // ad, untagged source or inconsistent mapping is unknown. No current-item/index fallback.
    val uid = mediaPeriodId.periodUid
    val ownTimeline = getTimeline()
    val index = ownTimeline.getIndexOfPeriod(uid)
    if (mediaPeriodId.isAd || index < 0) return Origin(uid, null)
    val period = ownTimeline.getPeriod(index, Timeline.Period(), true)
    if (period.uid != uid || period.windowIndex !in 0 until ownTimeline.windowCount) return Origin(uid, null)
    val item = ownTimeline.getWindow(period.windowIndex, Timeline.Window()).mediaItem
    return Origin(uid, item.localConfiguration?.tag as? PorticoAudioSourceTag)
  }

  override fun onStreamChanged(formats: Array<out Format>, startPositionUs: Long, offsetUs: Long,
                               mediaPeriodId: MediaSource.MediaPeriodId) {
    // super can synchronously assign an output stream, so custody must already be queued.
    pending.addLast(original(mediaPeriodId))
    super.onStreamChanged(formats, startPositionUs, offsetUs, mediaPeriodId)
  }

  override fun onOutputStreamOffsetUsChanged(outputStreamOffsetUs: Long) {
    // One call per actual setOutputStreamInfo, even if several changes are processed in one loop.
    // Do not compare offsets; different streams may have exactly the same value.
    output = pending.pollFirst()
    gain.outputStream = output?.stamp()
    gain.acceptedWitness = null
    super.onOutputStreamOffsetUsChanged(outputStreamOffsetUs)
  }

  override fun onPositionReset(positionUs: Long, joining: Boolean, sampleStreamIsResetToKeyFrame: Boolean) {
    // Upstream assigns LAST pending directly, without the offset callback, then clears its queue.
    // Keep that exact origin. A non-codec-reset seek cannot prove custody of retained codec output.
    val resetOrigin = pending.peekLast() ?: output
    pending.clear()
    output = resetOrigin
    gain.outputStream = null
    gain.acceptedWitness = null
    super.onPositionReset(positionUs, joining, sampleStreamIsResetToKeyFrame)
    if (sampleStreamIsResetToKeyFrame) gain.outputStream = resetOrigin?.stamp()
  }

  override fun processOutputBuffer(positionUs: Long, elapsedRealtimeUs: Long, codec: MediaCodecAdapter?,
    buffer: ByteBuffer?, bufferIndex: Int, bufferFlags: Int, sampleCount: Int,
    bufferPresentationTimeUs: Long, isDecodeOnlyBuffer: Boolean, isLastBuffer: Boolean, format: Format): Boolean {
    val submitted = buffer != null && !isDecodeOnlyBuffer &&
        (bufferFlags and MediaCodec.BUFFER_FLAG_CODEC_CONFIG) == 0
    if (submitted) {
      // Bypass batches / encoded passthrough / offload are deliberately unproven. The custom
      // sink itself only accepts PCM. Capture BEFORE gain.apply, once per original buffer.
      musicSink.decodedBuffer(buffer!!, gain.outputStream.takeIf { codec != null && sampleCount == 1 })
    }
    val accepted = super.processOutputBuffer(positionUs, elapsedRealtimeUs, codec, buffer, bufferIndex,
        bufferFlags, sampleCount, bufferPresentationTimeUs, isDecodeOnlyBuffer, isLastBuffer, format)
    if (submitted && accepted) musicSink.decodedAccepted()
    return accepted
  }

  private fun retire() {
    pending.clear(); output = null
    gain.outputStream = null; gain.acceptedWitness = null
    musicSink.decodedAccepted()
  }

  override fun onDisabled() {
    retire()
    try { super.onDisabled() } finally { retire() }
  }

  override fun onReset() {
    retire()
    try { super.onReset() } finally { retire() }
  }
}
