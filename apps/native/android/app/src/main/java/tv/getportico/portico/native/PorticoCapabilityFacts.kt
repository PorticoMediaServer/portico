package tv.getportico.portico.native

import android.media.AudioDeviceInfo
import android.media.AudioFormat

/** Keep every published video limit and profile on the decoder that reported it. */
internal class PorticoVideoDecoder(val codec: String) {
  var hardware = false
  var maxWidth = 0
  var maxHeight = 0
  var maxFrameRate = 0
  var maxBitrate = 0
  var maxLevel = 0
  var tenBit = false
  var points: List<VideoPoint> = emptyList()
  val profiles = LinkedHashSet<String>()
  val hdr = LinkedHashSet<String>()
  val dolbyVision = LinkedHashSet<Int>()

  /** Dolby Vision profiles retain their base codec; profile 10 is AV1, never HEVC. */
  fun addDolbyVisionProfiles(reported: Set<Int>) {
    dolbyVision.addAll(reported.filter { profile ->
      when (codec) {
        "hevc" -> profile in setOf(4, 5, 7, 8)
        "h264" -> profile == 9
        "av1" -> profile == 10
        else -> false
      }
    })
  }

  /** Prefer the chip, then its largest operating point; never union another decoder's facts. */
  fun preferredOver(previous: PorticoVideoDecoder?): Boolean {
    if (maxWidth <= 0 || maxHeight <= 0) return false
    if (previous == null) return true
    if (hardware != previous.hardware) return hardware
    val pixels = maxWidth.toLong() * maxHeight
    val previousPixels = previous.maxWidth.toLong() * previous.maxHeight
    if (pixels != previousPixels) return pixels > previousPixels
    if (maxFrameRate != previous.maxFrameRate) return maxFrameRate > previous.maxFrameRate
    return maxBitrate > previous.maxBitrate
  }

  /** Decoding stays true on an SDR output; presentation is checked against displayHdr separately. */
  fun decodedRanges(): List<String> = (hdr + if (dolbyVision.isEmpty()) emptySet() else setOf("dolby_vision")).toList()
}

internal data class PorticoOutputDevice(val type: Int, val channels: List<Int>, val encodings: List<Int>)
internal data class PorticoHdmiPlug(val channels: Int, val encodings: List<Int>)
internal data class PorticoEffectiveOutput(val route: String, val channels: Int, val encodings: List<Int>) {
  val digital: Boolean get() = route == "hdmi" || route == "optical"
}
internal data class PorticoBitstream(val codec: String, val channels: Int, val variants: List<String> = emptyList(), val objectAudio: List<String> = emptyList())

/** One effective route feeds both the output facts and the encoded formats it can carry. */
internal object PorticoCapabilityAudio {
  private val routes = listOf(
      setOf(AudioDeviceInfo.TYPE_BLUETOOTH_A2DP, AudioDeviceInfo.TYPE_BLE_HEADSET, AudioDeviceInfo.TYPE_BLE_SPEAKER, AudioDeviceInfo.TYPE_HEARING_AID) to "bluetooth",
      setOf(AudioDeviceInfo.TYPE_WIRED_HEADPHONES, AudioDeviceInfo.TYPE_WIRED_HEADSET) to "headphones",
      setOf(AudioDeviceInfo.TYPE_USB_DEVICE, AudioDeviceInfo.TYPE_USB_HEADSET, AudioDeviceInfo.TYPE_USB_ACCESSORY) to "usb",
      setOf(AudioDeviceInfo.TYPE_HDMI, AudioDeviceInfo.TYPE_HDMI_ARC, AudioDeviceInfo.TYPE_HDMI_EARC) to "hdmi",
      setOf(AudioDeviceInfo.TYPE_LINE_DIGITAL) to "optical",
      setOf(AudioDeviceInfo.TYPE_BUILTIN_SPEAKER, AudioDeviceInfo.TYPE_BUILTIN_EARPIECE) to "speaker")

  fun output(devices: List<PorticoOutputDevice>, television: Boolean, hdmi: PorticoHdmiPlug?): PorticoEffectiveOutput {
    val active = routes.firstNotNullOfOrNull { (types, route) -> devices.firstOrNull { it.type in types }?.let { it to route } }
    val route = active?.second ?: if (devices.isEmpty()) "" else "other"
    // Some Fire TV devices enumerate the speaker even while the HDMI plug names the receiver.
    if (television && hdmi != null && route !in setOf("hdmi", "optical", "bluetooth", "headphones", "usb"))
      return PorticoEffectiveOutput("hdmi", (if (hdmi.channels > 0) hdmi.channels else 8).coerceIn(2, 16), hdmi.encodings)
    val device = active?.first ?: devices.firstOrNull()
    val channels = device?.channels?.maxOrNull()?.takeIf { it > 0 } ?: if (route == "hdmi" || route == "optical") 8 else 2
    return PorticoEffectiveOutput(route, channels.coerceIn(2, 16), device?.encodings ?: emptyList())
  }

  // DTS:X is a separate object-audio claim, made only for an explicitly reported UHD encoding.
  val bitstreams = mapOf(
      AudioFormat.ENCODING_AC3 to PorticoBitstream("ac3", 6),
      AudioFormat.ENCODING_E_AC3 to PorticoBitstream("eac3", 8),
      AudioFormat.ENCODING_E_AC3_JOC to PorticoBitstream("eac3", 8, objectAudio = listOf("atmos")),
      AudioFormat.ENCODING_DOLBY_TRUEHD to PorticoBitstream("truehd", 8),
      AudioFormat.ENCODING_DTS to PorticoBitstream("dts", 6, variants = listOf("dts_core")),
      AudioFormat.ENCODING_DTS_HD to PorticoBitstream("dts", 8, variants = listOf("dts_core", "dts_hd_hra", "dts_hd_ma")),
      // An MA-only report (API 34 constant) still carries the core and MA, never HRA by implication.
      AudioFormat.ENCODING_DTS_HD_MA to PorticoBitstream("dts", 8, variants = listOf("dts_core", "dts_hd_ma")),
      AudioFormat.ENCODING_DTS_UHD_P1 to PorticoBitstream("dts", 8, variants = listOf("dtsx"), objectAudio = listOf("dtsx")),
      AudioFormat.ENCODING_DTS_UHD_P2 to PorticoBitstream("dts", 8, variants = listOf("dtsx"), objectAudio = listOf("dtsx")))

  fun passthrough(encodings: List<Int>, channels: Int): List<PorticoBitstream> {
    val found = LinkedHashMap<String, PorticoBitstream>()
    for (encoding in encodings) {
      val entry = bitstreams[encoding] ?: continue
      val previous = found[entry.codec]
      found[entry.codec] = PorticoBitstream(entry.codec,
          maxOf(previous?.channels ?: 0, if (channels > 0) minOf(channels, entry.channels) else entry.channels),
          ((previous?.variants ?: emptyList()) + entry.variants).distinct(),
          ((previous?.objectAudio ?: emptyList()) + entry.objectAudio).distinct())
    }
    return found.values.toList()
  }
}
