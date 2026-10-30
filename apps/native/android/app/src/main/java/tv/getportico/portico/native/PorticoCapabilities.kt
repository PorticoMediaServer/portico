package tv.getportico.portico.native

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.database.ContentObserver
import android.hardware.display.DisplayManager
import android.media.AudioDeviceCallback
import android.media.AudioDeviceInfo
import android.media.AudioManager
import android.media.MediaCodecInfo
import android.media.MediaCodecInfo.CodecProfileLevel
import android.media.MediaCodecList
import android.os.Build
import android.os.Handler
import android.os.Looper
import android.provider.Settings
import android.util.DisplayMetrics
import android.view.Display
import com.facebook.react.bridge.Arguments
import com.facebook.react.bridge.Promise
import com.facebook.react.bridge.ReactApplicationContext
import com.facebook.react.bridge.ReactMethod
import com.facebook.react.bridge.WritableArray
import com.facebook.react.bridge.WritableMap

/**
 * `NativeModules.PorticoCapabilities`: what only the system knows about this device right now —
 * which codecs it decodes (`MediaCodecList`), which HDR formats the panel or the television
 * behind it accepts (`Display`), and what the current audio output carries (`AudioManager`). The
 * answer is client-core's `AndroidNativeFacts`, which `androidProfile` merges into the capability
 * profile the server plans delivery against. Everything here is a read.
 */
class PorticoCapabilities(context: ReactApplicationContext) : PorticoEventModule(context) {
  private val main = Handler(Looper.getMainLooper())
  private val audio by lazy { context.getSystemService(Context.AUDIO_SERVICE) as AudioManager }
  private val displays by lazy { context.getSystemService(Context.DISPLAY_SERVICE) as DisplayManager }
  private var watching = false

  private val audioChanged = object : AudioDeviceCallback() {
    override fun onAudioDevicesAdded(added: Array<out AudioDeviceInfo>) = changed()
    override fun onAudioDevicesRemoved(removed: Array<out AudioDeviceInfo>) = changed()
  }
  // Fire OS (and some televisions) say what the receiver takes through the HDMI plug broadcast
  // and the "surround sound" system setting rather than the audio device list: either changing
  // asks for the profile again too.
  private val hdmiChanged = object : BroadcastReceiver() {
    override fun onReceive(context: Context?, intent: Intent?) = changed()
  }
  private val surroundChanged = object : ContentObserver(Handler(Looper.getMainLooper())) {
    override fun onChange(selfChange: Boolean) = changed()
  }
  private val displayChanged = object : DisplayManager.DisplayListener {
    override fun onDisplayAdded(displayId: Int) {}
    override fun onDisplayRemoved(displayId: Int) {}
    override fun onDisplayChanged(displayId: Int) { if (displayId == Display.DEFAULT_DISPLAY) changed() }
  }

  override fun getName(): String = "PorticoCapabilities"

  override fun initialize() {
    super.initialize()
    synchronized(this) {
      if (watching) return
      watching = true
    }
    audio.registerAudioDeviceCallback(audioChanged, main)
    displays.registerDisplayListener(displayChanged, main)
    try {
      val filter = IntentFilter(AudioManager.ACTION_HDMI_AUDIO_PLUG)
      if (Build.VERSION.SDK_INT >= 33) reactApplicationContext.registerReceiver(hdmiChanged, filter, Context.RECEIVER_NOT_EXPORTED)
      else reactApplicationContext.registerReceiver(hdmiChanged, filter)
    } catch (_: Exception) {}
    try { reactApplicationContext.contentResolver.registerContentObserver(Settings.Global.getUriFor(SURROUND_SETTING), false, surroundChanged) } catch (_: Exception) {}
  }

  override fun invalidate() {
    val was = synchronized(this) { watching.also { watching = false } }
    if (was) {
      audio.unregisterAudioDeviceCallback(audioChanged)
      displays.unregisterDisplayListener(displayChanged)
      try { reactApplicationContext.unregisterReceiver(hdmiChanged) } catch (_: Exception) {}
      try { reactApplicationContext.contentResolver.unregisterContentObserver(surroundChanged) } catch (_: Exception) {}
    }
    super.invalidate()
  }

  @ReactMethod fun addListener(eventName: String) {}

  @ReactMethod fun removeListeners(count: Double) {}

  /** The profile is asked for again; the JavaScript side waits for a burst to settle. */
  private fun changed() = emit("portico.capabilities", Arguments.createMap())

  @ReactMethod
  fun facts(promise: Promise) {
    try {
      val context = reactApplicationContext
      val result = Arguments.createMap()
      result.putString("model", Build.MODEL ?: "")
      result.putString("osVersion", Build.VERSION.RELEASE ?: "")
      result.putString("manufacturer", Build.MANUFACTURER ?: "")
      // The name the owner gave this device ("Living Room"), for sign-in approvals, and the app's own version.
      result.putString("deviceName", try { Settings.Global.getString(context.contentResolver, Settings.Global.DEVICE_NAME) ?: "" } catch (_: Exception) { "" })
      result.putString("appVersion", try { context.packageManager.getPackageInfo(context.packageName, 0).versionName ?: "" } catch (_: Exception) { "" })
      val codecs = try { MediaCodecList(MediaCodecList.REGULAR_CODECS).codecInfos.filter { !it.isEncoder } } catch (_: Exception) { emptyList() }
      result.putArray("decoders", videoDecoders(codecs))
      result.putArray("audioDecoders", audioDecoders(codecs))
      val display = displays.getDisplay(Display.DEFAULT_DISPLAY)
      result.putArray("displayHdr", displayHdr(display))
      result.putMap("screen", screen(display))
      val outputs = try { audio.getDevices(AudioManager.GET_DEVICES_OUTPUTS).map { PorticoOutputDevice(it.type, it.channelCounts.toList(), it.encodings.toList()) } } catch (_: Exception) { emptyList() }
      val output = PorticoCapabilityAudio.output(outputs, PorticoMpvDevice.isTelevision, hdmiPlug())
      result.putArray("passthrough", passthrough(output))
      result.putMap("audio", Arguments.createMap().apply {
        putInt("maxOutputChannels", output.channels)
        putString("route", output.route)
      })
      promise.resolve(result)
    } catch (e: Exception) {
      promise.reject("capabilities", "This device's playback capabilities could not be read.", e)
    }
  }

  // --- video -----------------------------------------------------------------------------

  private fun videoDecoders(codecs: List<MediaCodecInfo>): WritableArray {
    val found = LinkedHashMap<String, PorticoVideoDecoder>()
    var dolbyVision: Set<Int> = emptySet()
    for (info in codecs) {
      for (type in info.supportedTypes) {
        val mime = type.lowercase()
        val capabilities = try { info.getCapabilitiesForType(type) } catch (_: Exception) { continue }
        if (mime == "video/dolby-vision") {
          dolbyVision = dolbyVision + capabilities.profileLevels.mapNotNull { DOLBY_VISION[it.profile] }
          continue
        }
        val codec = VIDEO[mime] ?: continue
        val video = capabilities.videoCapabilities ?: continue
        val entry = PorticoVideoDecoder(codec)
        entry.hardware = hardware(info)
        val width = video.supportedWidths.upper
        val height = try { video.getSupportedHeightsFor(width).upper } catch (_: Exception) { video.supportedHeights.upper }
        entry.maxWidth = width
        entry.maxHeight = height
        entry.maxFrameRate = try { video.getSupportedFrameRatesFor(width, height).upper.toInt() } catch (_: Exception) { video.supportedFrameRates.upper }
        entry.points = videoPoints(video, width, height)
        entry.maxBitrate = video.bitrateRange.upper
        for (level in capabilities.profileLevels) describe(codec, level, entry)
        if (entry.preferredOver(found[codec])) found[codec] = entry
      }
    }
    for (entry in found.values) entry.addDolbyVisionProfiles(dolbyVision)
    val out = Arguments.createArray()
    for (entry in found.values) {
      if (entry.maxWidth <= 0 || entry.maxHeight <= 0) continue
      out.pushMap(Arguments.createMap().apply {
        putString("codec", entry.codec)
        putBoolean("hardware", entry.hardware)
        putInt("maxWidth", entry.maxWidth)
        putInt("maxHeight", entry.maxHeight)
        if (entry.maxFrameRate in 1..960) putInt("maxFrameRate", entry.maxFrameRate)
        if (entry.points.isNotEmpty()) putArray("limits", videoPointsArray(entry.points))
        if (entry.maxBitrate > 0) putInt("maxBitrateBps", entry.maxBitrate)
        if (entry.profiles.isNotEmpty()) putArray("profiles", Arguments.fromList(entry.profiles.toList()))
        if (entry.maxLevel > 0) putInt("maxLevel", entry.maxLevel)
        putBoolean("tenBit", entry.tenBit)
        putArray("hdr", Arguments.fromList(entry.decodedRanges()))
        if (entry.dolbyVision.isNotEmpty()) putArray("dolbyVisionProfiles", Arguments.fromList(entry.dolbyVision.sorted()))
      })
    }
    return out
  }

  /**
   * A decoder's profile and level, in the words and numbers the server reads from a file: the
   * profile as `ffprobe` names it, lowercased with underscores ("High 10" is `high_10`). The
   * server refuses a file whose profile is not listed, so the vocabulary has to be the file's.
   */
  private fun describe(codec: String, level: CodecProfileLevel, entry: PorticoVideoDecoder) {
    when (codec) {
      "h264" -> {
        when (level.profile) {
          CodecProfileLevel.AVCProfileBaseline -> { entry.profiles.add("baseline"); entry.profiles.add("constrained_baseline") }
          CodecProfileLevel.AVCProfileConstrainedBaseline -> entry.profiles.add("constrained_baseline")
          CodecProfileLevel.AVCProfileMain -> entry.profiles.add("main")
          CodecProfileLevel.AVCProfileExtended -> entry.profiles.add("extended")
          CodecProfileLevel.AVCProfileHigh -> entry.profiles.add("high")
          CodecProfileLevel.AVCProfileConstrainedHigh -> entry.profiles.add("constrained_high")
          CodecProfileLevel.AVCProfileHigh10 -> { entry.profiles.add("high_10"); entry.tenBit = true }
        }
        AVC_LEVELS[level.level]?.let { entry.maxLevel = maxOf(entry.maxLevel, it) }
      }
      "hevc" -> {
        when (level.profile) {
          CodecProfileLevel.HEVCProfileMain -> entry.profiles.add("main")
          CodecProfileLevel.HEVCProfileMainStill -> entry.profiles.add("main_still_picture")
          CodecProfileLevel.HEVCProfileMain10 -> { entry.profiles.add("main_10"); entry.tenBit = true; entry.hdr.add("hlg") }
          CodecProfileLevel.HEVCProfileMain10HDR10 -> { entry.profiles.add("main_10"); entry.tenBit = true; entry.hdr.add("hdr10"); entry.hdr.add("hlg") }
          CodecProfileLevel.HEVCProfileMain10HDR10Plus -> { entry.profiles.add("main_10"); entry.tenBit = true; entry.hdr.add("hdr10"); entry.hdr.add("hdr10plus") }
        }
        HEVC_LEVELS[level.level]?.let { entry.maxLevel = maxOf(entry.maxLevel, it) }
      }
      "vp9" -> when (level.profile) {
        CodecProfileLevel.VP9Profile0 -> entry.profiles.add("profile_0")
        CodecProfileLevel.VP9Profile1 -> entry.profiles.add("profile_1")
        CodecProfileLevel.VP9Profile2 -> { entry.profiles.add("profile_2"); entry.tenBit = true }
        CodecProfileLevel.VP9Profile3 -> { entry.profiles.add("profile_3"); entry.tenBit = true }
        CodecProfileLevel.VP9Profile2HDR -> { entry.profiles.add("profile_2"); entry.tenBit = true; entry.hdr.add("hdr10") }
        CodecProfileLevel.VP9Profile2HDR10Plus -> { entry.profiles.add("profile_2"); entry.tenBit = true; entry.hdr.add("hdr10"); entry.hdr.add("hdr10plus") }
      }
      "av1" -> when (level.profile) {
        CodecProfileLevel.AV1ProfileMain8 -> entry.profiles.add("main")
        CodecProfileLevel.AV1ProfileMain10 -> { entry.profiles.add("main"); entry.tenBit = true; entry.hdr.add("hlg") }
        CodecProfileLevel.AV1ProfileMain10HDR10 -> { entry.profiles.add("main"); entry.tenBit = true; entry.hdr.add("hdr10"); entry.hdr.add("hlg") }
        CodecProfileLevel.AV1ProfileMain10HDR10Plus -> { entry.profiles.add("main"); entry.tenBit = true; entry.hdr.add("hdr10"); entry.hdr.add("hdr10plus") }
      }
    }
  }

  private fun hardware(info: MediaCodecInfo): Boolean =
      if (Build.VERSION.SDK_INT >= 29) info.isHardwareAccelerated
      else info.name.lowercase().let { !it.startsWith("omx.google.") && !it.startsWith("c2.android.") }

  // --- audio -----------------------------------------------------------------------------

  private fun audioDecoders(codecs: List<MediaCodecInfo>): WritableArray {
    val found = LinkedHashMap<String, Int>()
    for (info in codecs) {
      for (type in info.supportedTypes) {
        val codec = AUDIO[type.lowercase()] ?: continue
        val channels = try { info.getCapabilitiesForType(type).audioCapabilities?.maxInputChannelCount ?: 2 } catch (_: Exception) { continue }
        found[codec] = maxOf(found[codec] ?: 0, channels.coerceIn(1, 16))
      }
    }
    val out = Arguments.createArray()
    for ((codec, channels) in found) out.pushMap(Arguments.createMap().apply { putString("codec", codec); putInt("maxChannels", channels) })
    return out
  }

  /**
   * What the receiver or television takes as a bitstream. Only when the sound is going there:
   * with headphones or a Bluetooth speaker connected the system plays through those, and a
   * bitstream the receiver could have taken has nowhere to go.
   */
  private fun passthrough(output: PorticoEffectiveOutput): WritableArray {
    val out = Arguments.createArray()
    if (!output.digital) return out
    val encodings = output.encodings.toMutableList()
    var channels = output.channels
    // The player's own answer as well: what its audio output would hand on untouched. It reads
    // what the system says can be played directly, what the HDMI plug reported, and the surround
    // setting some televisions and Fire TV keep instead, so a receiver the device list above says
    // nothing about is still found. An optical route uses its own encodings; the global HDMI
    // broadcast must not lend an optical output the receiver's lossless formats.
    if (output.route == "hdmi") {
      val player = try { androidx.media3.exoplayer.audio.AudioCapabilities.getCapabilities(reactApplicationContext, androidx.media3.common.AudioAttributes.DEFAULT, null) } catch (_: Throwable) { null }
      if (player != null) {
        channels = maxOf(channels, player.maxChannelCount)
        encodings.addAll(PorticoCapabilityAudio.bitstreams.keys.filter { player.supportsEncoding(it) })
      }
    }
    for (value in PorticoCapabilityAudio.passthrough(encodings, channels)) out.pushMap(Arguments.createMap().apply {
      putString("codec", value.codec)
      putInt("maxChannels", value.channels)
      if (value.variants.isNotEmpty()) putArray("variants", Arguments.fromList(value.variants))
      if (value.objectAudio.isNotEmpty()) putArray("objectAudio", Arguments.fromList(value.objectAudio))
    })
    return out
  }

  /** The HDMI sink's facts from the plug broadcast when one is connected, else null. */
  private fun hdmiPlug(): PorticoHdmiPlug? = try {
    val sticky = if (Build.VERSION.SDK_INT >= 33) reactApplicationContext.registerReceiver(null, IntentFilter(AudioManager.ACTION_HDMI_AUDIO_PLUG), Context.RECEIVER_NOT_EXPORTED)
        else reactApplicationContext.registerReceiver(null, IntentFilter(AudioManager.ACTION_HDMI_AUDIO_PLUG))
    if (sticky?.getIntExtra(AudioManager.EXTRA_AUDIO_PLUG_STATE, 0) == 1)
      PorticoHdmiPlug(sticky.getIntExtra(AudioManager.EXTRA_MAX_CHANNEL_COUNT, 0), sticky.getIntArrayExtra(AudioManager.EXTRA_ENCODINGS)?.toList() ?: emptyList())
    else null
  } catch (_: Exception) { null }

  // --- display ---------------------------------------------------------------------------

  private fun displayHdr(display: Display?): WritableArray {
    val out = Arguments.createArray()
    @Suppress("DEPRECATION")
    val types = try { display?.hdrCapabilities?.supportedHdrTypes } catch (_: Exception) { null } ?: return out
    for (type in types) HDR_TYPES[type]?.let { out.pushString(it) }
    return out
  }

  private fun screen(display: Display?): WritableMap {
    val metrics = DisplayMetrics()
    @Suppress("DEPRECATION") display?.getRealMetrics(metrics)
    var width = metrics.widthPixels
    var height = metrics.heightPixels
    var rate = display?.refreshRate ?: 0f
    // A television's interface is often 1080p on a 4K panel; the modes say what video may use.
    for (mode in display?.supportedModes ?: emptyArray()) {
      if (mode.physicalWidth.toLong() * mode.physicalHeight > width.toLong() * height) { width = mode.physicalWidth; height = mode.physicalHeight }
      rate = maxOf(rate, mode.refreshRate)
    }
    return Arguments.createMap().apply {
      putInt("width", maxOf(width, height))
      putInt("height", minOf(width, height))
      if (rate > 0) putInt("maxFrameRate", Math.round(rate))
    }
  }

  companion object {
    /** The Fire OS and Android TV "surround sound" setting Media3 reads for an external receiver. */
    private const val SURROUND_SETTING = "external_surround_sound_enabled"
    private val VIDEO = mapOf(
        "video/avc" to "h264", "video/hevc" to "hevc", "video/av01" to "av1", "video/x-vnd.on2.vp9" to "vp9",
        "video/x-vnd.on2.vp8" to "vp8", "video/mp4v-es" to "mpeg4", "video/mpeg2" to "mpeg2video",
        "video/wvc1" to "vc1")
    private val AUDIO = mapOf(
        "audio/mp4a-latm" to "aac", "audio/mpeg" to "mp3", "audio/opus" to "opus", "audio/vorbis" to "vorbis",
        "audio/flac" to "flac", "audio/raw" to "pcm", "audio/ac3" to "ac3", "audio/eac3" to "eac3",
        "audio/true-hd" to "truehd", "audio/vnd.dts" to "dts", "audio/vnd.dts.hd" to "dtshd")

    /** `Display.HdrCapabilities` types. */
    private val HDR_TYPES = mapOf(1 to "dolby_vision", 2 to "hdr10", 3 to "hlg", 4 to "hdr10plus")

    /** `CodecProfileLevel.DolbyVisionProfile*` to the profile number a file names. */
    private val DOLBY_VISION = mapOf(
        CodecProfileLevel.DolbyVisionProfileDvheDtr to 4, CodecProfileLevel.DolbyVisionProfileDvheStn to 5,
        CodecProfileLevel.DolbyVisionProfileDvheDtb to 7, CodecProfileLevel.DolbyVisionProfileDvheSt to 8,
        CodecProfileLevel.DolbyVisionProfileDvavSe to 9, CodecProfileLevel.DolbyVisionProfileDvav110 to 10)

    /** H.264 levels as `ffprobe` writes them (4.1 is 41). */
    private val AVC_LEVELS = mapOf(
        CodecProfileLevel.AVCLevel1 to 10, CodecProfileLevel.AVCLevel11 to 11, CodecProfileLevel.AVCLevel12 to 12, CodecProfileLevel.AVCLevel13 to 13,
        CodecProfileLevel.AVCLevel2 to 20, CodecProfileLevel.AVCLevel21 to 21, CodecProfileLevel.AVCLevel22 to 22,
        CodecProfileLevel.AVCLevel3 to 30, CodecProfileLevel.AVCLevel31 to 31, CodecProfileLevel.AVCLevel32 to 32,
        CodecProfileLevel.AVCLevel4 to 40, CodecProfileLevel.AVCLevel41 to 41, CodecProfileLevel.AVCLevel42 to 42,
        CodecProfileLevel.AVCLevel5 to 50, CodecProfileLevel.AVCLevel51 to 51, CodecProfileLevel.AVCLevel52 to 52,
        CodecProfileLevel.AVCLevel6 to 60, CodecProfileLevel.AVCLevel61 to 61, CodecProfileLevel.AVCLevel62 to 62)

    /** HEVC levels as `ffprobe` writes them (5.1 is 153: thirty times the level). */
    private val HEVC_LEVELS = mapOf(
        CodecProfileLevel.HEVCMainTierLevel1 to 30, CodecProfileLevel.HEVCMainTierLevel2 to 60, CodecProfileLevel.HEVCMainTierLevel21 to 63,
        CodecProfileLevel.HEVCMainTierLevel3 to 90, CodecProfileLevel.HEVCMainTierLevel31 to 93,
        CodecProfileLevel.HEVCMainTierLevel4 to 120, CodecProfileLevel.HEVCHighTierLevel4 to 120,
        CodecProfileLevel.HEVCMainTierLevel41 to 123, CodecProfileLevel.HEVCHighTierLevel41 to 123,
        CodecProfileLevel.HEVCMainTierLevel5 to 150, CodecProfileLevel.HEVCHighTierLevel5 to 150,
        CodecProfileLevel.HEVCMainTierLevel51 to 153, CodecProfileLevel.HEVCHighTierLevel51 to 153,
        CodecProfileLevel.HEVCMainTierLevel52 to 156, CodecProfileLevel.HEVCHighTierLevel52 to 156,
        CodecProfileLevel.HEVCMainTierLevel6 to 180, CodecProfileLevel.HEVCHighTierLevel6 to 180,
        CodecProfileLevel.HEVCMainTierLevel61 to 183, CodecProfileLevel.HEVCHighTierLevel61 to 183,
        CodecProfileLevel.HEVCMainTierLevel62 to 186, CodecProfileLevel.HEVCHighTierLevel62 to 186)

  }
}
