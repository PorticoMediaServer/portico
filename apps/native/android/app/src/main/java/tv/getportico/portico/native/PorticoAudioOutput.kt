package tv.getportico.portico.native

import android.content.Context
import android.media.AudioDeviceInfo
import android.media.AudioManager
import android.os.Build
import androidx.annotation.RequiresApi
import androidx.media3.common.C

/**
 * What one routed output device actually advertises, matched against the decoded music format.
 *
 * Android reports one [android.media.AudioProfile] per format: an encoding, the sample rates and
 * the channel counts that encoding supports. Those stay attached to their own profile instead of
 * being flattened into combinations the device never advertised.
 *
 * `DefaultAudioSink` can only preserve more than 16 bits through its float output; its integer
 * path always converts to 16-bit. So float is the only high-resolution target this sink can
 * honestly offer, and an integer-only high-resolution profile falls back to 16-bit rather than
 * claiming precision the sink would discard. The current route counts as known only when exactly
 * one sink is connected. [Choice.bytePassthrough] describes bytes (unity, same encoding);
 * [Choice.bitPerfect] additionally needs this exact combination advertised, and neither is a
 * claim about the DAC.
 */
internal object PorticoAudioOutput {
  /** One advertised format: encoding with one of its rates and channel counts. */
  data class Profile(val encoding: Int, val sampleRate: Int, val channelCount: Int)

  /** One output device's advertised profiles, copied out of [AudioDeviceInfo]. */
  data class Device(val label: String, val profiles: List<Profile>)

  data class Choice(
      val encoding: Int,
      val sourceEncoding: Int,
      val sampleRate: Int,
      val channels: Int,
      val unity: Boolean,
      val route: String,
      val routeKnown: Boolean,
      val bytePassthrough: Boolean,
      val bitPerfect: Boolean,
      val reason: String,
  )

  /** The route: connected sinks, and whether there is exactly one so the route is unambiguous. */
  data class Route(val devices: List<Device>, val known: Boolean)

  private val PRECISION = mapOf(
      C.ENCODING_PCM_16BIT to 2,
      C.ENCODING_PCM_24BIT to 3,
      C.ENCODING_PCM_32BIT to 4,
      C.ENCODING_PCM_FLOAT to 4,
  )

  fun bytesPerSample(encoding: Int): Int = PRECISION[encoding] ?: 0

  /** Exactly the PCM encodings a listening player may hand to the sink. */
  fun supports(encoding: Int): Boolean = encoding in PRECISION

  /**
   * Intersect one decoded format with the advertised device. Pure so the decision is testable
   * without a real route: the device list is data, not a query.
   */
  fun choose(route: Route, sampleRate: Int, channels: Int, sourceEncoding: Int, unity: Boolean): Choice {
    val source = if (sourceEncoding in PRECISION) sourceEncoding else C.ENCODING_PCM_16BIT
    val floorBytes = bytesPerSample(C.ENCODING_PCM_16BIT)
    val sourceIsFloor = bytesPerSample(source) <= floorBytes
    val label = route.devices.firstOrNull()?.label ?: "none"
    if (sampleRate <= 0 || channels <= 0) {
      return Choice(C.ENCODING_PCM_16BIT, source, 0, 0, unity, label, route.known, false, false, "no-plan")
    }
    if (!route.known) {
      val passthrough = unity && sourceIsFloor
      return Choice(C.ENCODING_PCM_16BIT, source, sampleRate, channels, unity, label, false, passthrough, false, "route-unknown")
    }
    val exact = { encoding: Int ->
      route.devices.firstOrNull { device ->
        device.profiles.any { it.encoding == encoding && it.sampleRate == sampleRate && it.channelCount == channels }
      }
    }
    val floatDevice = exact(C.ENCODING_PCM_FLOAT)
    if (floatDevice != null && !sourceIsFloor) {
      // The sink's float output is the only path that keeps more than 16 bits; feeding it a
      // 24/32/float stream converts there and keeps the decoded precision.
      val passthrough = unity && source == C.ENCODING_PCM_FLOAT
      return Choice(C.ENCODING_PCM_FLOAT, source, sampleRate, channels, unity, floatDevice.label, true, passthrough, passthrough,
          if (passthrough) "unity-passthrough" else if (source == C.ENCODING_PCM_FLOAT) "gain-applied" else "device-float")
    }
    // No float at this exact format: 16-bit is the honest target, whatever integer precision the
    // profile advertises, because the sink would narrow it anyway.
    val sixteen = exact(C.ENCODING_PCM_16BIT)
    val passthrough = unity && source == C.ENCODING_PCM_16BIT
    return Choice(C.ENCODING_PCM_16BIT, source, sampleRate, channels, unity, label, true, passthrough,
        sixteen != null && passthrough,
        if (source == C.ENCODING_PCM_16BIT) (if (passthrough) "unity-passthrough" else "gain-applied") else "device-16bit")
  }

  /** The encoding this route can take for one stream. Independent of gain, so support and
   * configure cannot disagree when the gain target changes between the two calls. */
  fun targetEncoding(route: Route, sampleRate: Int, channels: Int, sourceEncoding: Int): Int =
      choose(route, sampleRate, channels, sourceEncoding, true).encoding

  /** The connected outputs, read only where Android can report profiles at all (API 31+). */
  fun route(context: Context): Route {
    if (Build.VERSION.SDK_INT < 31) return Route(emptyList(), false)
    val manager = context.getSystemService(Context.AUDIO_SERVICE) as? AudioManager ?: return Route(emptyList(), false)
    val outputs = try {
      manager.getDevices(AudioManager.GET_DEVICES_OUTPUTS)
    } catch (_: Exception) {
      return Route(emptyList(), false)
    }
    val sinks = outputs.filter { it.isSink && it.type != AudioDeviceInfo.TYPE_TELEPHONY }
    if (sinks.size != 1) return Route(sinks.map { describe(it) }, false)
    return Route(listOf(describe(sinks[0])), true)
  }

  @RequiresApi(Build.VERSION_CODES.S)
  private fun describe(info: AudioDeviceInfo): Device {
    val profiles = try {
      info.audioProfiles
    } catch (_: Exception) {
      emptyList()
    }
    val combinations = ArrayList<Profile>()
    for (profile in profiles) {
      // `format` is the encoding this profile describes; its rates and masks belong to that same
      // encoding only. Nothing is combined across profiles.
      val encoding = profile.format
      if (bytesPerSample(encoding) == 0) continue
      val counts = LinkedHashSet<Int>()
      for (mask in profile.channelMasks) if (mask > 0) counts.add(Integer.bitCount(mask))
      for (mask in profile.channelIndexMasks) if (mask > 0) counts.add(Integer.bitCount(mask))
      if (counts.isEmpty()) continue
      for (rate in profile.sampleRates) {
        if (rate <= 0) continue
        for (channels in counts) combinations.add(Profile(encoding, rate, channels))
      }
    }
    return Device(label(info), combinations)
  }

  /** A readable route label without relying on APIs the public SDK does not expose. */
  @RequiresApi(Build.VERSION_CODES.S)
  private fun label(info: AudioDeviceInfo): String {
    val kind = when (info.type) {
      AudioDeviceInfo.TYPE_BUILTIN_SPEAKER -> "speaker"
      AudioDeviceInfo.TYPE_BUILTIN_EARPIECE -> "earpiece"
      AudioDeviceInfo.TYPE_WIRED_HEADPHONES, AudioDeviceInfo.TYPE_WIRED_HEADSET -> "headphones"
      AudioDeviceInfo.TYPE_BLUETOOTH_A2DP, AudioDeviceInfo.TYPE_BLUETOOTH_SCO -> "bluetooth"
      AudioDeviceInfo.TYPE_HDMI, AudioDeviceInfo.TYPE_HDMI_ARC, AudioDeviceInfo.TYPE_HDMI_EARC -> "hdmi"
      AudioDeviceInfo.TYPE_USB_DEVICE, AudioDeviceInfo.TYPE_USB_HEADSET, AudioDeviceInfo.TYPE_USB_ACCESSORY -> "usb"
      AudioDeviceInfo.TYPE_LINE_ANALOG, AudioDeviceInfo.TYPE_LINE_DIGITAL -> "line"
      else -> "output"
    }
    return listOf(kind, info.productName.toString()).filter { it.isNotBlank() }.joinToString(" ")
  }

}
