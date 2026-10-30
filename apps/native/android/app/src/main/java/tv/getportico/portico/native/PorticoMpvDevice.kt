package tv.getportico.portico.native

import android.app.Activity
import android.app.ActivityManager
import android.app.Application
import android.content.Context
import android.hardware.display.DisplayManager
import android.media.AudioDeviceInfo
import android.media.AudioManager
import android.media.MediaCodecInfo
import android.media.MediaCodecInfo.CodecProfileLevel
import android.media.MediaCodecList
import android.os.Build
import android.os.Bundle
import android.system.Os
import android.view.Display
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleOwner
import java.io.File
import tv.getportico.portico.TelevisionDisplay

/**
 * What this device gives the mpv engine: which codecs its chip decodes (everything else mpv
 * decodes in software), what its screen and audio output take, and how much memory it has.
 * The engine's options and its capability profile are both built from this.
 */
internal object PorticoMpvDevice {
  /** A hardware decoder's own limits. */
  class Decoder(val maxWidth: Int, val maxHeight: Int, val maxFrameRate: Int, val tenBit: Boolean, val points: List<VideoPoint> = emptyList())

  /** mpv's codec names for the MediaCodec types FFmpeg in this build can drive. */
  private val MEDIACODEC_TYPES = mapOf(
      "video/avc" to "h264", "video/hevc" to "hevc", "video/x-vnd.on2.vp9" to "vp9", "video/av01" to "av1",
      "video/x-vnd.on2.vp8" to "vp8", "video/mpeg2" to "mpeg2video", "video/mp4v-es" to "mpeg4")

  private fun hardware(info: MediaCodecInfo): Boolean =
      if (Build.VERSION.SDK_INT >= 29) info.isHardwareAccelerated
      else info.name.lowercase().let { !it.startsWith("omx.google.") && !it.startsWith("c2.android.") && !it.startsWith("omx.ffmpeg.") && !it.contains(".sw.") }

  private fun tenBit(codec: String, level: CodecProfileLevel): Boolean = when (codec) {
    "h264" -> level.profile == CodecProfileLevel.AVCProfileHigh10
    "hevc" -> level.profile == CodecProfileLevel.HEVCProfileMain10 || level.profile == CodecProfileLevel.HEVCProfileMain10HDR10 || level.profile == CodecProfileLevel.HEVCProfileMain10HDR10Plus
    "vp9" -> level.profile == CodecProfileLevel.VP9Profile2 || level.profile == CodecProfileLevel.VP9Profile2HDR || level.profile == CodecProfileLevel.VP9Profile2HDR10Plus
    "av1" -> level.profile == CodecProfileLevel.AV1ProfileMain10 || level.profile == CodecProfileLevel.AV1ProfileMain10HDR10 || level.profile == CodecProfileLevel.AV1ProfileMain10HDR10Plus
    else -> false
  }

  /**
   * The codecs the chip decodes, by mpv's names, each with the largest picture and rate its
   * decoder reports. A codec with only a software decoder in the system is left out: mpv's own
   * software decoders are the better ones, so those codecs are never sent to MediaCodec.
   */
  val hardwareDecoders: Map<String, Decoder> by lazy {
    val found = LinkedHashMap<String, Decoder>()
    val codecs = try { MediaCodecList(MediaCodecList.REGULAR_CODECS).codecInfos.filter { !it.isEncoder && hardware(it) } } catch (_: Exception) { emptyList() }
    for (info in codecs) {
      for (type in info.supportedTypes) {
        val codec = MEDIACODEC_TYPES[type.lowercase()] ?: continue
        val capabilities = try { info.getCapabilitiesForType(type) } catch (_: Exception) { continue }
        val video = capabilities.videoCapabilities ?: continue
        val width = video.supportedWidths.upper
        val height = try { video.getSupportedHeightsFor(width).upper } catch (_: Exception) { video.supportedHeights.upper }
        val rate = try { video.getSupportedFrameRatesFor(width, height).upper.toInt() } catch (_: Exception) { video.supportedFrameRates.upper }
        val deep = capabilities.profileLevels.any { tenBit(codec, it) }
        val previous = found[codec]
        if (previous == null || width.toLong() * height > previous.maxWidth.toLong() * previous.maxHeight) {
          found[codec] = Decoder(width, height, rate.coerceIn(1, 960), deep || previous?.tenBit == true, videoPoints(video, width, height))
        } else if (deep && !previous.tenBit) {
          found[codec] = Decoder(previous.maxWidth, previous.maxHeight, previous.maxFrameRate, true, previous.points)
        }
      }
    }
    found
  }

  val isTelevision: Boolean get() = TelevisionDisplay.isTelevision

  @Volatile private var memory = 0L

  /** The device's memory. It does not change, so it is asked for once. */
  fun memoryBytes(context: Context): Long {
    if (memory > 0) return memory
    val info = ActivityManager.MemoryInfo()
    (context.getSystemService(Context.ACTIVITY_SERVICE) as? ActivityManager)?.getMemoryInfo(info)
    memory = info.totalMem
    return memory
  }

  fun display(context: Context): Display? =
      (context.getSystemService(Context.DISPLAY_SERVICE) as? DisplayManager)?.getDisplay(Display.DEFAULT_DISPLAY)

  /** The HDR formats the screen (or the television behind the box) shows: `hdr10`, `hlg`, `dolby_vision`, `hdr10plus`. */
  fun displayHdr(context: Context): Set<String> {
    val display = display(context) ?: return emptySet()
    val types: IntArray? = try {
      if (Build.VERSION.SDK_INT >= 34) display.mode.supportedHdrTypes
      else @Suppress("DEPRECATION") display.hdrCapabilities?.supportedHdrTypes
    } catch (_: Exception) {
      null
    }
    return (types ?: IntArray(0)).toList().mapNotNull { HDR_TYPES[it] }.toSet()
  }

  private val HDR_TYPES = mapOf(1 to "dolby_vision", 2 to "hdr10", 3 to "hlg", 4 to "hdr10plus")

  /** The panel's real size, long side first, and its highest refresh rate. */
  fun screen(context: Context): Triple<Int, Int, Int> {
    val display = display(context)
    val metrics = android.util.DisplayMetrics()
    @Suppress("DEPRECATION") display?.getRealMetrics(metrics)
    var width = maxOf(metrics.widthPixels, metrics.heightPixels)
    var height = minOf(metrics.widthPixels, metrics.heightPixels)
    var rate = display?.refreshRate ?: 0f
    // A television's interface is often 1080p on a 4K panel; the modes say what video may use.
    for (mode in display?.supportedModes ?: emptyArray()) {
      if (mode.physicalWidth.toLong() * mode.physicalHeight > width.toLong() * height) {
        width = maxOf(mode.physicalWidth, mode.physicalHeight)
        height = minOf(mode.physicalWidth, mode.physicalHeight)
      }
      rate = maxOf(rate, mode.refreshRate)
    }
    return Triple(width, height, Math.round(rate))
  }

  private val DIGITAL = setOf(AudioDeviceInfo.TYPE_HDMI, AudioDeviceInfo.TYPE_HDMI_ARC, AudioDeviceInfo.TYPE_HDMI_EARC, AudioDeviceInfo.TYPE_LINE_DIGITAL)
  private val PERSONAL = setOf(
      AudioDeviceInfo.TYPE_BLUETOOTH_A2DP, AudioDeviceInfo.TYPE_BLE_HEADSET, AudioDeviceInfo.TYPE_BLE_SPEAKER, AudioDeviceInfo.TYPE_HEARING_AID,
      AudioDeviceInfo.TYPE_WIRED_HEADPHONES, AudioDeviceInfo.TYPE_WIRED_HEADSET, AudioDeviceInfo.TYPE_USB_HEADSET)

  /**
   * How many channels the output a new playback would use takes. Headphones and Bluetooth come
   * first, as the system routes to them first. A device that lists no channel counts takes any
   * number: that reads as eight on a digital output and as two elsewhere.
   */
  fun outputChannels(context: Context): Int {
    val outputs = try { (context.getSystemService(Context.AUDIO_SERVICE) as AudioManager).getDevices(AudioManager.GET_DEVICES_OUTPUTS).toList() } catch (_: Exception) { emptyList() }
    if (outputs.any { it.type in PERSONAL }) return 2
    val digital = outputs.filter { it.type in DIGITAL }
    if (digital.isNotEmpty()) return digital.maxOf { device -> device.channelCounts.maxOrNull() ?: 8 }.coerceIn(2, 8)
    val other = outputs.maxOfOrNull { device -> device.channelCounts.maxOrNull() ?: 2 } ?: 2
    return other.coerceIn(2, 8)
  }

  /** mpv's `audio-channels` for that many channels: the widest layout first, stereo always last. */
  fun channelLayouts(channels: Int): String = when {
    channels >= 8 -> "7.1,5.1,stereo"
    channels >= 6 -> "5.1,stereo"
    else -> "stereo"
  }
}

/**
 * Whether the app is on screen. A video stops when the app leaves the screen without Picture in
 * Picture (Home on a television, the screen switched off on a phone): both player engines listen
 * here. An activity in a Picture in Picture window is still on screen.
 *
 * Main thread only.
 */
internal object PorticoAppVisibility {
  val listeners = LinkedHashSet<(Boolean) -> Unit>()
  var visible = true
    private set
  private val started = HashSet<Activity>()
  private var installed = false

  fun install(context: Context, current: Activity?) {
    if (installed) return
    val application = context.applicationContext as? Application ?: return
    installed = true
    if (current != null && (current as? LifecycleOwner)?.lifecycle?.currentState?.isAtLeast(Lifecycle.State.STARTED) == true) started.add(current)
    application.registerActivityLifecycleCallbacks(object : Application.ActivityLifecycleCallbacks {
      override fun onActivityStarted(activity: Activity) { started.add(activity); update() }
      override fun onActivityStopped(activity: Activity) { started.remove(activity); update() }
      override fun onActivityDestroyed(activity: Activity) { started.remove(activity); update() }
      override fun onActivityCreated(activity: Activity, savedInstanceState: Bundle?) {}
      override fun onActivityResumed(activity: Activity) { started.add(activity); update() }
      override fun onActivityPaused(activity: Activity) {}
      override fun onActivitySaveInstanceState(activity: Activity, outState: Bundle) {}
    })
    update()
  }

  private fun update() {
    val next = started.isNotEmpty()
    if (next == visible) return
    visible = next
    listeners.toList().forEach { it(next) }
  }
}

/**
 * The font mpv falls back to for subtitles.
 *
 * mpv's subtitle renderer (libass) has no way to ask Android for fonts in this build, so it is
 * given one font file as its default, in its own configuration folder (`subfont.ttf`), as mpv
 * expects. That file is a link to a font the system already has, chosen by the language of the
 * subtitles about to be drawn: the system's ordinary text face (Latin, Greek, Cyrillic); its
 * Chinese, Japanese and Korean face; or its face for Arabic, Hebrew, Thai, the Indic scripts and
 * the other scripts Android carries a face for. Without this a line in one of those scripts is
 * drawn as empty boxes. A styled subtitle file's own attached fonts are used as usual and are not
 * affected.
 *
 * A line can mix scripts (Arabic with Latin names, say), so there is also a fallback chain. mpv's
 * fonts folder (`fallbackDirectory`, its own default `~~/fonts`) holds links to the ordinary face,
 * the face for the track's language and the faces for every script seen in its first lines, and
 * the subtitle font family (`sub-font`) is the ordinary face's own name. libass then draws each
 * letter in the first face that has it: the style's family, then the ordinary face, then (with the
 * media engine's libass patch, `third_party/media-engine/patches/libass`) any face in the folder,
 * then `subfont.ttf`. Faces over 8 MB (the Chinese, Japanese and Korean collections) are not put
 * in the folder, since libass reads every file there into memory; they stay `subfont.ttf`, and
 * they carry Latin letters of their own.
 */
internal object PorticoMpvFonts {
  private val ORDINARY = listOf("Roboto-Regular.ttf", "RobotoStatic-Regular.ttf", "NotoSans-Regular.ttf", "DroidSans.ttf")
  private val SERIF = listOf("NotoSerif-Regular.ttf", "NotoSerif-VF.ttf", "DroidSerif-Regular.ttf")
  private val MONO = listOf("DroidSansMono.ttf", "RobotoMono-Regular.ttf", "NotoSansMono-Regular.ttf")
  private val EAST_ASIAN = listOf("NotoSansCJK-Regular.ttc", "NotoSansCJKjp-Regular.otf", "NotoSansSC-Regular.otf", "DroidSansFallback.ttf")
  private val EAST_ASIAN_LANGUAGES = setOf("ja", "zh", "ko", "yue", "cmn")

  /** A script, the word its system font files carry in their names, and older names tried first. */
  private class Script(val word: String, val known: List<String> = emptyList())
  private val ARABIC = Script("Arabic", listOf("NotoNaskhArabic-Regular.ttf", "NotoNaskhArabicUI-Regular.ttf", "NotoSansArabic-Regular.ttf", "DroidNaskh-Regular.ttf", "DroidKufi-Regular.ttf"))
  private val HEBREW = Script("Hebrew", listOf("NotoSansHebrew-Regular.ttf", "DroidSansHebrew-Regular.ttf"))
  private val THAI = Script("Thai", listOf("NotoSansThai-Regular.ttf", "NotoSansThaiUI-Regular.ttf", "DroidSansThai.ttf"))
  private val DEVANAGARI = Script("Devanagari", listOf("NotoSansDevanagari-VF.ttf", "NotoSansDevanagari-Regular.otf", "NotoSansDevanagari-Regular.ttf", "DroidSansDevanagari-Regular.ttf"))
  private val BENGALI = Script("Bengali")
  private val TAMIL = Script("Tamil")
  private val TELUGU = Script("Telugu")
  private val KANNADA = Script("Kannada")
  private val MALAYALAM = Script("Malayalam")
  private val GUJARATI = Script("Gujarati")
  private val GURMUKHI = Script("Gurmukhi")
  /** By language, in two-letter codes where the language has one (`PorticoMpvSupport.baseLanguage`). */
  private val SCRIPTS: Map<String, Script> = mapOf(
      "ar" to ARABIC, "ara" to ARABIC, "fa" to ARABIC, "fas" to ARABIC, "per" to ARABIC, "ur" to ARABIC, "urd" to ARABIC, "ps" to ARABIC, "pus" to ARABIC, "sd" to ARABIC, "ug" to ARABIC, "ckb" to ARABIC,
      "he" to HEBREW, "heb" to HEBREW, "iw" to HEBREW, "yi" to HEBREW, "yid" to HEBREW,
      "th" to THAI, "tha" to THAI,
      "hi" to DEVANAGARI, "hin" to DEVANAGARI, "mr" to DEVANAGARI, "mar" to DEVANAGARI, "ne" to DEVANAGARI, "nep" to DEVANAGARI, "sa" to DEVANAGARI, "san" to DEVANAGARI,
      "bn" to BENGALI, "ben" to BENGALI, "as" to BENGALI, "asm" to BENGALI,
      "ta" to TAMIL, "tam" to TAMIL, "te" to TELUGU, "tel" to TELUGU, "kn" to KANNADA, "kan" to KANNADA, "ml" to MALAYALAM, "mal" to MALAYALAM,
      "gu" to GUJARATI, "guj" to GUJARATI, "pa" to GURMUKHI, "pan" to GURMUKHI,
      "si" to Script("Sinhala"), "sin" to Script("Sinhala"), "km" to Script("Khmer"), "khm" to Script("Khmer"), "lo" to Script("Lao"), "lao" to Script("Lao"),
      "my" to Script("Myanmar"), "mya" to Script("Myanmar"), "bur" to Script("Myanmar"), "hy" to Script("Armenian"), "hye" to Script("Armenian"), "arm" to Script("Armenian"),
      "ka" to Script("Georgian"), "kat" to Script("Georgian"), "geo" to Script("Georgian"), "am" to Script("Ethiopic"), "amh" to Script("Ethiopic"), "ti" to Script("Ethiopic"))
  private var linked: String? = null
  private var listing: List<File>? = null
  /** The faces in the fallback folder now, by path. */
  private val fallbacks = LinkedHashSet<String>()
  /** A styled track's attached fonts folder in use, which gets the same fallback faces. */
  private var attached: File? = null
  /** libass reads each file of the fonts folder into memory when a track opens. */
  private const val FALLBACK_BYTES = 8_000_000L

  /** mpv's configuration folder. It holds nothing but the font link and the fonts folder; mpv reads no settings from it. */
  fun configDirectory(context: Context): File = File(context.noBackupFilesDir, "mpv")

  /** mpv's fonts folder (`sub-fonts-dir`): links to the fallback faces, every one of which libass loads. */
  fun fallbackDirectory(context: Context): File = File(configDirectory(context), "fonts")

  /**
   * The family name the ordinary face is known by, for `sub-font`: letters it has (Latin, Greek,
   * Cyrillic, digits, punctuation) are drawn in it whatever else a line holds. `sans-serif` when
   * it cannot be read, which is mpv's own default.
   */
  fun ordinaryFamily(): String = ordinary()?.takeIf { it.length() <= FALLBACK_BYTES }?.let { familyName(it) } ?: "sans-serif"

  data class CaptionFace(val file: File, val family: String)

  /** libass's request family comes from an actual bounded installed file's name
   * table. This is not an observed/canonical typography identity or a CSS name. */
  fun captionFace(font: String, find: (List<String>) -> File? = ::systemFont): CaptionFace? = try {
    val names = when (font) { "serif" -> SERIF; "mono" -> MONO; else -> emptyList() }
    val file = if (names.isEmpty()) null else find(names)
    if (file == null || !file.isFile || !file.canRead() || file.length() !in 1..FALLBACK_BYTES) null
    else familyName(file)?.let { CaptionFace(file, it) }
  } catch (_: Exception) { null }

  // Two fixed public OS face choices, resolved once; no media-derived paths.
  private val captionFaces by lazy { listOf("serif", "mono").associateWith { captionFace(it) } }
  private val captionOrdinary by lazy { ordinaryFamily() }

  fun captionFamily(style: Map<*, *>?): String =
      if (style?.get("useSystemCaptionStyle") == false) captionFaces[(style["font"] as? String) ?: ""]?.family ?: captionOrdinary else captionOrdinary

  private fun systemFont(names: List<String>): File? {
    for (name in names) File("/system/fonts", name).let { if (it.isFile && it.canRead()) return it }
    return null
  }

  private fun systemFonts(): List<File> = listing ?: (try {
    File("/system/fonts").listFiles()?.filter { it.isFile && it.canRead() && (it.name.endsWith(".ttf") || it.name.endsWith(".otf") || it.name.endsWith(".ttc")) }?.sortedBy { it.name }
  } catch (_: Exception) { null } ?: emptyList()).also { listing = it }

  private fun ordinary(): File? = systemFont(ORDINARY)
      ?: systemFonts().filter { it.name.endsWith(".ttf") && it.length() in 1..8_000_000 }.minByOrNull { it.name }

  /**
   * The system's face for a script: a name known from Android's releases, else the plainest file
   * whose name carries the script's word (a regular or variable weight; a sans face before a
   * serif one; the reading face before the one drawn for interface labels).
   */
  private fun scriptFont(script: Script): File? = systemFont(script.known) ?: systemFonts()
      .filter { it.name.contains(script.word) && (it.name.contains("Regular") || it.name.contains("-VF")) }
      .minByOrNull { (if (it.name.contains("Serif")) 4 else 0) + (if (it.name.contains("UI")) 2 else 0) + (if (it.name.contains("-VF")) 1 else 0) }

  /**
   * Points the default font at the face for [language] (a subtitle track's language tag, or
   * null). Call before mpv starts drawing a track: the file is read when the track is opened.
   */
  fun select(context: Context, language: String?) {
    val base = PorticoMpvSupport.baseLanguage((language ?: "").lowercase())
    val face = if (base in EAST_ASIAN_LANGUAGES) systemFont(EAST_ASIAN) else SCRIPTS[base]?.let { scriptFont(it) }
    // A new track starts the chain again: the ordinary face and the language's.
    resetFallbacks(context)
    // libass reads a font directory when it opens a track. Preload these two
    // bounded faces too, so a later style change need not reopen the source.
    (listOfNotNull(ordinary(), face) + captionFaces.values.mapNotNull { it?.file }).forEach { addFallback(context, it) }
    link(context, face ?: ordinary() ?: return)
  }

  /** By the script the letters are written in (`scriptOf`), for the text's own face. */
  private val BY_SCRIPT: Map<Character.UnicodeScript, Script> = mapOf(
      Character.UnicodeScript.ARABIC to ARABIC, Character.UnicodeScript.HEBREW to HEBREW, Character.UnicodeScript.THAI to THAI,
      Character.UnicodeScript.DEVANAGARI to DEVANAGARI, Character.UnicodeScript.BENGALI to BENGALI, Character.UnicodeScript.TAMIL to TAMIL,
      Character.UnicodeScript.TELUGU to TELUGU, Character.UnicodeScript.KANNADA to KANNADA, Character.UnicodeScript.MALAYALAM to MALAYALAM,
      Character.UnicodeScript.GUJARATI to GUJARATI, Character.UnicodeScript.GURMUKHI to GURMUKHI, Character.UnicodeScript.SINHALA to Script("Sinhala"),
      Character.UnicodeScript.KHMER to Script("Khmer"), Character.UnicodeScript.LAO to Script("Lao"), Character.UnicodeScript.MYANMAR to Script("Myanmar"),
      Character.UnicodeScript.ARMENIAN to Script("Armenian"), Character.UnicodeScript.GEORGIAN to Script("Georgian"), Character.UnicodeScript.ETHIOPIC to Script("Ethiopic"))
  private val EAST_ASIAN_SCRIPTS = setOf(Character.UnicodeScript.HAN, Character.UnicodeScript.HIRAGANA, Character.UnicodeScript.KATAKANA, Character.UnicodeScript.HANGUL, Character.UnicodeScript.BOPOMOFO)
  private val PLAIN_SCRIPTS = setOf(Character.UnicodeScript.LATIN, Character.UnicodeScript.GREEK, Character.UnicodeScript.CYRILLIC, Character.UnicodeScript.COMMON, Character.UnicodeScript.INHERITED, Character.UnicodeScript.UNKNOWN)

  /**
   * The script most of the letters in [text] are written in, when it is one the ordinary face
   * does not draw (Latin, Greek and Cyrillic are the ordinary face's); null otherwise. East Asian
   * scripts count as one (their face draws all of them, and Latin too).
   */
  fun scriptOf(text: String): Character.UnicodeScript? = scriptCounts(text).maxByOrNull { it.value }?.key

  /** Every script in [text] the ordinary face does not draw, most letters first. */
  fun scriptsOf(text: String): List<Character.UnicodeScript> = scriptCounts(text).entries.sortedByDescending { it.value }.map { it.key }

  private fun scriptCounts(text: String): Map<Character.UnicodeScript, Int> {
    val counts = LinkedHashMap<Character.UnicodeScript, Int>()
    var i = 0
    while (i < text.length && i < 2000) {
      val cp = text.codePointAt(i)
      i += Character.charCount(cp)
      val script = try { Character.UnicodeScript.of(cp) } catch (_: IllegalArgumentException) { continue }
      if (script in PLAIN_SCRIPTS) continue
      val key = if (script in EAST_ASIAN_SCRIPTS) Character.UnicodeScript.HAN else script
      counts[key] = (counts[key] ?: 0) + 1
    }
    return counts
  }

  private fun faceFor(script: Character.UnicodeScript): File? =
      if (script == Character.UnicodeScript.HAN) systemFont(EAST_ASIAN) else BY_SCRIPT[script]?.let { scriptFont(it) }

  /**
   * Points the default font at the face for the script [text] is written in, when the language
   * tag chose another (an untagged or mistagged track, or a line in a second script). True when
   * the face changed: the track must be opened again to use it.
   */
  fun selectFor(context: Context, text: String): Boolean {
    val scripts = scriptsOf(text)
    if (scripts.isEmpty()) return false
    // Every script of the line joins the fallback chain (a few at most: one line rarely has more than two).
    var changed = false
    for (script in scripts.take(4)) faceFor(script)?.let { if (addFallback(context, it)) changed = true }
    // The default face follows the line's main script, as before: a face too large for the folder is reached this way.
    val wanted = faceFor(scripts.first())
    if (wanted != null && linked != wanted.path) {
      link(context, wanted)
      if (linked == wanted.path) changed = true
    }
    return changed
  }

  /**
   * A styled track's attached fonts are in a folder of their own, which mpv then reads instead of
   * the fonts folder: the fallback faces are linked there too. Null when the track has none.
   */
  fun useAttachedFolder(folder: File?) {
    attached = folder
    if (folder != null) fallbacks.forEach { linkInto(folder, File(it)) }
  }

  private fun resetFallbacks(context: Context) {
    fallbacks.clear()
    try { fallbackDirectory(context).listFiles()?.forEach { it.delete() } } catch (_: Exception) {}
  }

  /** Puts [face] in the fallback folder (and the attached fonts folder in use). True when it was not there. */
  private fun addFallback(context: Context, face: File): Boolean {
    if (face.path in fallbacks || face.length() !in 1..FALLBACK_BYTES) return false
    val directory = fallbackDirectory(context)
    try { directory.mkdirs() } catch (_: Exception) { return false }
    if (!linkInto(directory, face)) return false
    fallbacks.add(face.path)
    attached?.let { linkInto(it, face) }
    return true
  }

  /** A link to [face] in [directory] under the face's own file name, or a copy where links cannot be made. */
  private fun linkInto(directory: File, face: File): Boolean {
    val target = File(directory, "fallback-" + face.name)
    if (target.exists()) return true
    return try {
      Os.symlink(face.path, target.path)
      true
    } catch (_: Exception) {
      try { face.copyTo(target, overwrite = true); true } catch (_: Exception) { false }
    }
  }

  /**
   * The family name (name ID 1, Windows platform, as libass reads it) of the first face in a
   * TrueType or OpenType file or collection; null when the file cannot be read as one.
   */
  fun familyName(file: File): String? = try {
    java.io.RandomAccessFile(file, "r").use { familyName(it) }
  } catch (_: Exception) {
    null
  }

  internal fun familyName(f: java.io.RandomAccessFile): String? {
    fun u16(at: Long): Int { f.seek(at); return f.readUnsignedShort() }
    fun u32(at: Long): Long { f.seek(at); return f.readInt().toLong() and 0xffffffffL }
    val size = f.length()
    if (size < 12) return null
    // A collection ('ttcf') lists its faces; the first one's table directory is read.
    val base = if (u32(0) == 0x74746366L) u32(12) else 0L
    if (base + 12 > size) return null
    val tables = u16(base + 4)
    if (tables > 256 || base + 12 + tables * 16L > size) return null
    var name = -1L
    for (i in 0 until tables) {
      val record = base + 12 + i * 16L
      if (u32(record) == 0x6e616d65L) { name = u32(record + 8); break } // 'name'
    }
    if (name < 0 || name + 6 > size) return null
    val count = u16(name + 2)
    val strings = name + u16(name + 4)
    var best: String? = null
    for (i in 0 until minOf(count, 1024)) {
      val record = name + 6 + i * 12L
      if (record + 12 > size) break
      if (u16(record) != 3 || u16(record + 6) != 1) continue
      val language = u16(record + 4)
      val length = u16(record + 8)
      val at = strings + u16(record + 10)
      if (length == 0 || length > 512 || at + length > size) continue
      val bytes = ByteArray(length)
      f.seek(at)
      f.readFully(bytes)
      val text = String(bytes, Charsets.UTF_16BE).trim()
      if (text.isEmpty()) continue
      // English (United States) is the name every font has; another language's is kept only until it is found.
      if (language == 0x409) return text
      if (best == null) best = text
    }
    return best
  }

  private fun link(context: Context, wanted: File) {
    if (linked == wanted.path) return
    val directory = configDirectory(context)
    val link = File(directory, "subfont.ttf")
    try {
      directory.mkdirs()
      link.delete()
      Os.symlink(wanted.path, link.path)
      linked = wanted.path
    } catch (_: Exception) {
      // No link could be made: a copy of the face does (a large font is not copied).
      try {
        val source = if (wanted.length() <= 8_000_000) wanted else ordinary()
        if (source != null) {
          link.delete()
          source.copyTo(link, overwrite = true)
          linked = source.path
        }
      } catch (_: Exception) {}
    }
  }
}
