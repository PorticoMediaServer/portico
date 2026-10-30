package tv.getportico.portico.native

import android.content.Context
import android.media.AudioDeviceInfo
import android.media.AudioFormat
import androidx.media3.common.AudioAttributes
import androidx.media3.common.Format
import androidx.media3.common.MimeTypes
import androidx.media3.exoplayer.DefaultRenderersFactory
import androidx.media3.exoplayer.audio.AudioCapabilities
import androidx.media3.exoplayer.audio.AudioOffloadSupport
import androidx.media3.exoplayer.audio.AudioSink
import androidx.media3.exoplayer.audio.ForwardingAudioSink

/** Device preferences restrict encoded output; they never grant support that the sink lacks. */
internal data class PorticoPassthroughPolicy(val disabled: Boolean = false, val off: Set<String> = emptySet()) {
  fun allowsEncoding(encoding: Int): Boolean = !disabled && when (encoding) {
    AudioFormat.ENCODING_AC3 -> "dd" !in off
    AudioFormat.ENCODING_E_AC3, AudioFormat.ENCODING_E_AC3_JOC -> "ddp" !in off
    AudioFormat.ENCODING_DOLBY_TRUEHD -> "truehd" !in off
    AudioFormat.ENCODING_DTS -> "dts" !in off
    AudioFormat.ENCODING_DTS_HD, AudioFormat.ENCODING_DTS_HD_MA -> "dtshd" !in off
    AudioFormat.ENCODING_DTS_UHD_P1, AudioFormat.ENCODING_DTS_UHD_P2 -> "dtsx" !in off
    else -> true
  }

  fun allowsMime(mime: String?): Boolean {
    if (mime == MimeTypes.AUDIO_RAW) return true
    if (disabled) return false
    return when (mime) {
      MimeTypes.AUDIO_AC3 -> "dd" !in off
      MimeTypes.AUDIO_E_AC3, MimeTypes.AUDIO_E_AC3_JOC -> "ddp" !in off
      MimeTypes.AUDIO_TRUEHD -> "truehd" !in off
      MimeTypes.AUDIO_DTS -> "dts" !in off
      MimeTypes.AUDIO_DTS_HD -> "dtshd" !in off
      MimeTypes.AUDIO_DTS_X -> "dtsx" !in off
      else -> true
    }
  }

  companion object {
    private val formats = setOf("dd", "ddp", "truehd", "dts", "dtshd", "dtsx")

    fun valid(value: Any?): Boolean {
      if (value == null) return true
      val policy = value as? Map<*, *> ?: return false
      val choices = policy["formats"] as? Map<*, *> ?: return false
      return policy["disabled"] is Boolean && policy.keys.all { it == "disabled" || it == "formats" } &&
          choices.all { (key, mode) -> key is String && key in formats && (mode == "auto" || mode == "off") }
    }

    /** Called only after the whole native command has been validated. */
    fun read(value: Map<*, *>): PorticoPassthroughPolicy {
      val choices = value["formats"] as Map<*, *>
      return PorticoPassthroughPolicy(value["disabled"] == true, formats.filter { choices[it] == "off" }.toSet())
    }
  }
}

/** Check both the source format and Media3's chosen fallback encoding (DTS-HD may become core). */
internal class PorticoPassthroughSink(
  sink: AudioSink,
  private val policy: PorticoPassthroughPolicy,
  private val encoding: (Format, AudioAttributes, AudioDeviceInfo?) -> Int?,
) : ForwardingAudioSink(sink) {
  private var attributes = AudioAttributes.DEFAULT
  private var device: AudioDeviceInfo? = null

  private fun allowed(format: Format): Boolean {
    if (!policy.allowsMime(format.sampleMimeType)) return false
    if (format.sampleMimeType == MimeTypes.AUDIO_RAW || policy.off.isEmpty()) return true
    // Null means the route offers no passthrough for this format; the delegate still decides
    // whether it can decode/offload it. A disabled fallback may never bypass decoding.
    return encoding(format, attributes, device)?.let { policy.allowsEncoding(it) } ?: true
  }

  override fun setAudioAttributes(audioAttributes: AudioAttributes) {
    attributes = audioAttributes
    super.setAudioAttributes(audioAttributes)
  }

  override fun setPreferredDevice(audioDeviceInfo: AudioDeviceInfo?) {
    device = audioDeviceInfo
    super.setPreferredDevice(audioDeviceInfo)
  }

  override fun supportsFormat(format: Format): Boolean = allowed(format) && super.supportsFormat(format)

  override fun getFormatSupport(format: Format): Int =
      if (allowed(format)) super.getFormatSupport(format) else AudioSink.SINK_FORMAT_UNSUPPORTED

  override fun getFormatOffloadSupport(format: Format): AudioOffloadSupport =
      if (allowed(format)) super.getFormatOffloadSupport(format) else AudioOffloadSupport.DEFAULT_UNSUPPORTED

  override fun configure(format: Format, specifiedBufferSize: Int, outputChannels: IntArray?) {
    // Recheck after route changes as well as during codec selection. Tunneling/offload must
    // not configure an encoding that the current policy disallows.
    if (!allowed(format)) throw AudioSink.ConfigurationException("Encoded output is disabled for this format.", format)
    super.configure(format, specifiedBufferSize, outputChannels)
  }
}

internal class PorticoPassthroughRenderers(context: Context, private val policy: PorticoPassthroughPolicy) : DefaultRenderersFactory(context) {
  override fun buildAudioSink(context: Context, enableFloatOutput: Boolean, enableAudioOutputPlaybackParams: Boolean): AudioSink? =
      super.buildAudioSink(context, enableFloatOutput, enableAudioOutputPlaybackParams)?.let { sink ->
        PorticoPassthroughSink(sink, policy) { format, attributes, device ->
          AudioCapabilities.getCapabilities(context, attributes, device).getEncodingAndChannelConfigForPassthrough(format, attributes)?.first
        }
      }
}
