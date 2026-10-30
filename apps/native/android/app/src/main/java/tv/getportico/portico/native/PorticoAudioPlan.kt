package tv.getportico.portico.native

import android.net.Uri

/**
 * The version 2 audio plan (Playback Protocol §18.1) as this app reads it: the Android counterpart
 * of the plan checks in `Native/PorticoAudioRender.m`.
 *
 * The plan names one file the device decodes itself (the original, or the server's FLAC or Opus
 * conversion) with its exact length and gains. On Apple a Core Audio engine decodes it; here
 * ExoPlayer does, and joins two plans without a gap by playing them as one playlist.
 */
internal object PorticoAudioPlan {
  /** The (container, codec) pairs ExoPlayer decodes on every supported Android. ALAC is absent: the server converts it. */
  private val PAIRS = mapOf(
      "flac" to setOf("flac"), "mp4" to setOf("aac"), "m4a" to setOf("aac"), "adts" to setOf("aac"),
      "mp3" to setOf("mp3"), "ogg" to setOf("opus", "vorbis"))

  fun number(value: Any?): Double? = PorticoPlaybackController.number(value)

  private fun integer(value: Any?, min: Double, max: Double): Boolean {
    if (value is Boolean) return false
    val n = number(value) ?: return false
    return n >= min && n <= max && Math.floor(n) == n
  }

  private fun inRange(value: Any?, min: Double, max: Double): Boolean {
    if (value is Boolean) return false
    val n = number(value) ?: return false
    return n >= min && n <= max
  }

  fun decodes(plan: Map<*, *>): Boolean {
    val container = plan["container"] as? String ?: return false
    val codec = plan["codec"] as? String ?: return false
    if (container == "wav" || container == "aiff") return container == "wav" && codec.startsWith("pcm")
    return PAIRS[container]?.contains(codec) == true
  }

  /** A plan the engine can play at all. */
  fun playable(plan: Map<*, *>?): Boolean =
      plan != null && number(plan["version"]) == 2.0 && plan["mode"] in MODES && plan["url"] is String && decodes(plan) &&
          integer(plan["durationFrames"], 1.0, MAX_INTEGER) && integer(plan["sampleRate"], 8000.0, 768000.0)

  /** File rendering wins over a recipe codec. Missing/unmeasured facts keep the precision
   * pipeline; only a known encoded ordinary delivery delegates to the standard sink. */
  fun usesPCM(rendered: Map<*, *>?, seek: Map<*, *>?, deliveredCodec: Any?): Boolean {
    if (playable(rendered) || playable(seek)) return true
    return when (deliveredCodec) {
      "ac3", "eac3", "truehd", "dts", "dts_hd" -> false
      else -> true
    }
  }

  /** A plan with an absolute address on [origin], complete enough to load. */
  private fun valid(plan: Map<*, *>?, origin: Uri): Boolean {
    if (plan == null || !playable(plan)) return false
    val id = plan["id"] as? String ?: return false
    if (id.isEmpty() || id.length > 256 || !integer(plan["channels"], 1.0, 32.0)) return false
    val trim = plan["trim"] as? Map<*, *> ?: return false
    if (!integer(trim["startFrames"], 0.0, MAX_INTEGER) || !integer(trim["endFrames"], 0.0, MAX_INTEGER)) return false
    val url = try { Uri.parse(plan["url"] as String) } catch (_: Exception) { return false }
    if (url.scheme != "http" && url.scheme != "https") return false
    if (url.scheme != origin.scheme || url.host?.lowercase() != origin.host?.lowercase() || url.port != origin.port || url.userInfo != null) return false
    val gain = plan["gain"] as? Map<*, *>
    if (plan.containsKey("gain") && gain == null) return false
    for (key in listOf("trackDb", "albumDb")) if (gain?.get(key) != null && !inRange(gain[key], -60.0, 30.0)) return false
    for (key in listOf("trackPeak", "albumPeak")) if (gain?.get(key) != null && !inRange(gain[key], 0.000001, 64.0)) return false
    if (plan.containsKey("sourceBitrateBps") && !integer(plan["sourceBitrateBps"], 0.0, MAX_INTEGER)) return false
    for (key in listOf("conversionGainDb", "loudnessAppliedDb"))
      if (plan.containsKey(key) && !inRange(plan[key], -60.0, 30.0)) return false
    if (plan.containsKey("loudnessAppliedDb") && plan.containsKey("gain")) return false
    if (plan.containsKey("loudnessEnvelope") && envelope(plan["loudnessEnvelope"]) == null) return false
    return true
  }

  /** Why a command's `audioRender` is refused, or null. [source] is the command's media address. */
  fun validate(render: Map<*, *>, source: String?): String? {
    val origin = try { Uri.parse(source ?: "").takeIf { it.host != null } } catch (_: Exception) { null } ?: return "Invalid audio render plan."
    if (!valid(render["plan"] as? Map<*, *>, origin)) return "Invalid audio render plan."
    val settings = render["settings"] as? Map<*, *>
    if (settings == null || settings["gapless"] !is Boolean || !inRange(settings["crossfadeSeconds"], 0.0, 12.0) || settings["normalization"] !in NORMALIZATION ||
        !integer(render["epoch"], 0.0, MAX_INTEGER) || !inRange(render["startSeconds"], 0.0, 1e9)) return "Invalid audio render settings."
    for (key in listOf("neverBoost", "smoothTransitions"))
      if (settings.containsKey(key) && settings[key] !is Boolean) return "Invalid audio render settings."
    for (key in listOf("prepared", "committed")) {
      val value = render[key] ?: continue
      val next = value as? Map<*, *> ?: return "Invalid prepared audio."
      val token = next["token"] as? String
      if (!valid(next["plan"] as? Map<*, *>, origin) || token == null || !TOKEN.matches(token)) return "Invalid prepared audio binding."
    }
    return null
  }

  /** `10^((dB + preamp)/20)`, limited so the peak stays at or under full scale. Amplification is applied by the PCM processor, never by player volume. */
  fun gain(plan: Map<*, *>?, settings: Map<*, *>?): Float {
    // A measured server-applied amount is already in the received samples.
    if (plan?.containsKey("loudnessAppliedDb") == true) return 1f
    val mode = settings?.get("normalization") as? String ?: "off"
    val gain = plan?.get("gain") as? Map<*, *>
    if (gain == null || mode !in setOf("track", "album")) return 1f
    val album = mode == "album" && gain["albumDb"] != null
    val db = number(if (album) gain["albumDb"] else gain["trackDb"])?.takeIf { it in -60.0..30.0 } ?: return 1f
    val peak = number(if (album) (gain["albumPeak"] ?: gain["trackPeak"]) else gain["trackPeak"])?.takeIf { it in 0.000001..64.0 }
    var linear = Math.pow(10.0, (db + (number(settings?.get("preampDb")) ?: 0.0)) / 20)
    if (peak != null && peak * linear > 1) linear = 1 / peak
    return linear.coerceIn(0.0, if (peak == null || settings?.get("neverBoost") == true) 1.0 else 32.0).toFloat()
  }

  /** Actual measured head/tail contrast shapes the overlap; an absent envelope
   * keeps the fixed fade. This is the same rule as Apple and the canonical core. */
  fun transitionSeconds(current: Map<*, *>?, next: Map<*, *>?, maximum: Double, sameAlbum: Boolean, smooth: Boolean): Double {
    if (sameAlbum) return 0.0
    if (!maximum.isFinite() || maximum < 0.0 || maximum > 12.0) return Double.NaN
    if (!smooth) return maximum
    val tail = envelope(current?.get("loudnessEnvelope"))?.second ?: return maximum
    val head = envelope(next?.get("loudnessEnvelope"))?.first ?: return maximum
    if (tail.isEmpty() || head.isEmpty()) return maximum
    fun mean(values: List<Double>): Double = 10.0 * Math.log10(values.sumOf { Math.pow(10.0, it / 10.0) } / values.size)
    val contrast = Math.abs(mean(tail) - mean(head))
    return Math.min(maximum, Math.max(0.25, maximum / (1.0 + contrast / 12.0)))
  }

  private fun envelope(value: Any?): Pair<List<Double>, List<Double>>? {
    val map = value as? Map<*, *> ?: return null
    if (!integer(map["intervalMs"], 100.0, 100.0)) return null
    fun points(key: String): List<Double>? {
      val values = map[key] as? List<*> ?: return null
      if (values.size > 30) return null
      val out = ArrayList<Double>(values.size)
      for (point in values) {
        if (!inRange(point, -120.0, 10.0)) return null
        out.add(number(point) ?: return null)
      }
      return out
    }
    return Pair(points("startLufs") ?: return null, points("endLufs") ?: return null)
  }

  fun durationSeconds(plan: Map<*, *>?): Double {
    val frames = number(plan?.get("durationFrames")) ?: return 0.0
    val rate = number(plan?.get("sampleRate")) ?: return 0.0
    return if (rate > 0 && frames > 0) frames / rate else 0.0
  }

  private const val MAX_INTEGER = 9007199254740991.0
  private val MODES = setOf("direct", "converted")
  private val NORMALIZATION = setOf("off", "track", "album")
  private val TOKEN = Regex("^[A-Za-z0-9_.-]{1,128}$")
}
