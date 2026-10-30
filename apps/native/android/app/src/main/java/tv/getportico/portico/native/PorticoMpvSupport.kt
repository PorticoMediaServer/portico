package tv.getportico.portico.native

import android.content.Context
import android.net.Uri
import android.os.Handler
import android.os.Looper
import android.view.accessibility.CaptioningManager
import java.io.File
import java.io.IOException
import java.util.UUID
import java.util.concurrent.TimeUnit
import okhttp3.Call
import okhttp3.Callback
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import org.json.JSONArray
import org.json.JSONObject

/**
 * Pieces of the mpv engine that need no libmpv: command validation, track matching and the
 * address rules. The Android counterpart of `Native/PorticoMpvSupport.swift`, rule for rule.
 */
internal object PorticoMpvSupport {
  val RATES = listOf(0.5, 0.75, 1.0, 1.25, 1.5, 1.75, 2.0)
  const val MAX_SAFE_INTEGER = 9007199254740991.0

  // --- values from JavaScript -------------------------------------------------------------

  /** A JSON boolean, and nothing that merely converts to one. */
  fun boolean(value: Any?): Boolean? = value as? Boolean

  /** A finite JSON number. */
  fun number(value: Any?): Double? = (value as? Number)?.toDouble()?.takeIf { it.isFinite() }

  /** A whole number in `minimum..2^53-1`. */
  fun integer(value: Any?, minimum: Double = 1.0): Long? {
    val n = number(value) ?: return null
    return if (n >= minimum && n <= MAX_SAFE_INTEGER && Math.floor(n) == n) n.toLong() else null
  }

  @Suppress("UNCHECKED_CAST")
  fun map(value: Any?): Map<String, Any?>? = value as? Map<String, Any?>

  /**
   * [next] is a newer presentation of the playback [previous] is bound to: the same lease,
   * intent, session and title, a later generation. The server planned the session again in
   * place (a quality, version, audio or subtitle change, or a channel's next window), so the
   * command is the current one, not a stale one, and the engine opens its source.
   */
  fun replannedInPlace(previous: Map<*, *>, next: Map<*, *>): Boolean {
    val before = integer(previous["generation"]) ?: return false
    val after = integer(next["generation"]) ?: return false
    val intent = integer(next["intentId"]) ?: return false
    return after > before && intent == integer(previous["intentId"]) && previous["lease"] == next["lease"] &&
        next["sessionId"] is String && next["sessionId"] == previous["sessionId"] && next["itemId"] is String && next["itemId"] == previous["itemId"]
  }

  // --- mpv's JSON -------------------------------------------------------------------------

  private fun plain(value: Any?): Any? = when (value) {
    null, JSONObject.NULL -> null
    is JSONObject -> HashMap<String, Any?>().also { out -> for (key in value.keys()) out[key] = plain(value.opt(key)) }
    is JSONArray -> ArrayList<Any?>(value.length()).also { out -> for (i in 0 until value.length()) out.add(plain(value.opt(i))) }
    else -> value
  }

  /** The largest property document that is parsed. */
  private const val MAXIMUM_JSON = 2 * 1024 * 1024

  /** A list property (`track-list`, `chapter-list`) as mpv prints it. Empty when it is not one. */
  fun jsonList(text: String?): List<Map<String, Any?>> {
    if (text.isNullOrEmpty() || text.length > MAXIMUM_JSON) return emptyList()
    val array = try { JSONArray(text) } catch (_: Exception) { return emptyList() }
    val out = ArrayList<Map<String, Any?>>(array.length())
    for (i in 0 until array.length()) map(plain(array.opt(i)))?.let { out.add(it) }
    return out
  }

  /** A map property (`video-params`). Empty when it is not one. */
  fun jsonMap(text: String?): Map<String, Any?> {
    if (text.isNullOrEmpty() || text.length > MAXIMUM_JSON) return emptyMap()
    return try { map(plain(JSONObject(text))) ?: emptyMap() } catch (_: Exception) { emptyMap() }
  }

  // --- addresses --------------------------------------------------------------------------

  fun parse(value: String?): Uri? = try { value?.takeIf { it.isNotEmpty() }?.let { Uri.parse(it) }?.takeIf { it.scheme != null } } catch (_: Exception) { null }

  /**
   * A downloaded title: a file inside the app's own Downloads folder, checked after symbolic
   * links are resolved, and nowhere else on the device. The same rule as the Media3 engine.
   */
  fun allowedLocalSource(context: Context, url: Uri): Boolean {
    if (url.scheme?.lowercase() != "file" || url.encodedQuery != null || url.encodedFragment != null) return false
    return try {
      val root = File(context.noBackupFilesDir, "Downloads").canonicalPath
      val file = File(url.path ?: return false).canonicalFile
      file.path.startsWith("$root/") && file.isFile
    } catch (_: Exception) {
      false
    }
  }

  fun allowedSource(context: Context, url: Uri): Boolean {
    val scheme = url.scheme?.lowercase()
    if (scheme == "http" || scheme == "https") return !url.host.isNullOrEmpty() && url.userInfo == null
    return allowedLocalSource(context, url)
  }

  /**
   * An external subtitle or font file, or a resource an HLS playlist names, belongs to the
   * playback when it is on the media's own origin and under the media's own grant, beside the
   * media itself (`/v1/media/<grant>/...`) or where the server keeps subtitle files and their
   * fonts (`/v1/media/subtitles/<grant>/...`), with no credentials or fragment and a plain path. Any host is
   * accepted over HTTP or HTTPS: whether plain HTTP may be used at all is the app's network
   * policy, which the app's own HTTP client applies.
   */
  fun subtitleFileBelongs(url: Uri, media: Uri): Boolean {
    val scheme = url.scheme?.lowercase() ?: return false
    if (scheme != "https" && scheme != "http") return false
    if (scheme != media.scheme?.lowercase() || url.host?.lowercase() != media.host?.lowercase() || url.port != media.port) return false
    if (url.userInfo != null || url.encodedFragment != null) return false
    val parts = media.pathSegments
    if (parts.size < 3 || parts[0] != "v1" || parts[1] != "media") return false
    val path = url.path ?: return false
    if (path.contains('\\') || path.contains('%') || path.split('/').any { it == "." || it == ".." }) return false
    return path.startsWith("/v1/media/${parts[2]}/") || path.startsWith("/v1/media/subtitles/${parts[2]}/")
  }

  private val PLAYLIST_URI = Regex("URI=\"([^\"]*)\"")

  /**
   * An HLS playlist with every address in it (variants, segments, keys, maps and renditions)
   * rewritten to pass through the relay, so nothing it names is fetched by mpv itself, outside
   * the system's TLS validation. [local] answers the relay's address for an upstream one, or
   * null when it is out of scope. Null when the playlist is too large, is not a playlist, or
   * names anything that cannot be rewritten: such a playlist is refused whole.
   */
  fun relayPlaylist(data: ByteArray, source: Uri, local: (Uri) -> String?): ByteArray? {
    if (data.size > MAXIMUM_JSON) return null
    val text = String(data, Charsets.UTF_8)
    if (!text.startsWith("#EXTM3U")) return null
    fun address(value: String): String? {
      if (value.isEmpty() || value.toByteArray(Charsets.UTF_8).size > 8192 || value.contains("{$")) return null
      val remote = PorticoMpvFontFetcher.resolve(value, source) ?: return null
      if (!subtitleFileBelongs(remote, source)) return null
      return local(remote)
    }
    val lines = ArrayList<String>()
    for (line in text.split("\n")) {
      val clean = line.trim()
      if (clean.isEmpty()) { lines.add(""); continue }
      if (!clean.startsWith("#")) {
        lines.add(address(clean) ?: return null)
        continue
      }
      // Variable substitution could build an address this check never sees.
      if (clean.startsWith("#EXT-X-DEFINE:")) return null
      val matches = PLAYLIST_URI.findAll(clean).toList()
      // An unquoted or malformed URI attribute must never pass through untouched.
      if (clean.split("URI=").size - 1 != matches.size) return null
      var rewritten = clean
      for (match in matches.asReversed()) {
        val group = match.groups[1] ?: return null
        val value = address(group.value) ?: return null
        rewritten = rewritten.substring(0, group.range.first) + value + rewritten.substring(group.range.last + 1)
      }
      lines.add(rewritten)
    }
    return lines.joinToString("\n").toByteArray(Charsets.UTF_8)
  }

  // --- tracks -----------------------------------------------------------------------------

  fun tracks(all: List<Map<String, Any?>>, type: String): List<Map<String, Any?>> = all.filter { it["type"] == type }

  fun trackId(track: Map<String, Any?>): Int? = (track["id"] as? Number)?.toInt()

  fun streamIndex(track: Map<String, Any?>): Int? = (track["ff-index"] as? Number)?.toInt()

  /** A real video track, not cover art. */
  fun hasVideo(all: List<Map<String, Any?>>): Boolean = tracks(all, "video").any { it["albumart"] != true && it["image"] != true }

  /**
   * The name an HLS audio rendition was given in its playlist, which FFmpeg files as the
   * stream's comment; otherwise the track's own title.
   */
  fun names(track: Map<String, Any?>): List<String> {
    val out = ArrayList<String>()
    (track["title"] as? String)?.takeIf { it.isNotEmpty() }?.let { out.add(it) }
    map(track["metadata"])?.forEach { (key, value) ->
      val lowered = key.lowercase()
      if ((lowered == "comment" || lowered == "name" || lowered == "title") && value is String && value.isNotEmpty()) out.add(value)
    }
    return out
  }

  /**
   * Maps each server rendition (`id`, `name`, optional `streamIndex`) to exactly one mpv audio
   * track. Every rendition must find its own track, or nothing is mapped: by the file's stream
   * index when the server gives one, else by name, else by position when the counts agree.
   */
  fun mapRenditions(renditions: List<Map<String, Any?>>, audio: List<Map<String, Any?>>): Map<String, Int>? {
    if (renditions.isEmpty() || renditions.size > 64) return null
    val ids = ArrayList<String>()
    for (rendition in renditions) {
      val id = rendition["id"] as? String
      if (id.isNullOrEmpty() || ids.contains(id)) return null
      ids.add(id)
    }
    val mapped = LinkedHashMap<String, Int>()
    val used = HashSet<Int>()
    var exact = true
    for ((position, rendition) in renditions.withIndex()) {
      var match: Int? = null
      integer(rendition["streamIndex"], 0.0)?.let { index ->
        val found = audio.filter { streamIndex(it)?.toLong() == index }
        if (found.size == 1) match = trackId(found[0])
      }
      if (match == null) {
        val name = rendition["name"] as? String
        if (!name.isNullOrEmpty()) {
          val found = audio.filter { names(it).contains(name) }
          if (found.size == 1) match = trackId(found[0])
        }
      }
      val track = match
      if (track == null || used.contains(track)) { exact = false; break }
      mapped[ids[position]] = track
      used.add(track)
    }
    if (exact) return mapped
    if (audio.size != renditions.size) return null
    mapped.clear()
    for ((position, id) in ids.withIndex()) mapped[id] = trackId(audio[position]) ?: return null
    return mapped
  }

  /** The codecs FFmpeg reports for each subtitle kind of the capability document. */
  val SUBTITLE_CODECS: Map<String, Set<String>> = mapOf(
      "text" to setOf("subrip", "srt", "webvtt", "mov_text", "text", "microdvd", "sami", "ttml"),
      "ass" to setOf("ass", "ssa"),
      "pgs" to setOf("hdmv_pgs_subtitle"),
      "vobsub" to setOf("dvd_subtitle"),
      "dvb" to setOf("dvb_subtitle", "dvb_teletext"))

  /** The subtitle kinds mpv draws itself, by the names the capability document uses. */
  val RENDERED_SUBTITLE_KINDS = listOf("text", "ass", "pgs", "vobsub", "dvb")

  private fun codecOf(track: Map<String, Any?>): String = ((track["codec"] as? String) ?: "").lowercase()

  /**
   * The track inside the media itself that an `embedded` description names: of the wanted kind,
   * by stream index, else by its position among the file's own subtitle tracks, else by language
   * (and title when several share the language). Null when nothing, or more than one track, fits.
   */
  fun embeddedSubtitle(embedded: Map<String, Any?>, kind: String, subtitles: List<Map<String, Any?>>): Int? {
    val codecs = SUBTITLE_CODECS[kind] ?: emptySet()
    val own = subtitles.filter { it["external"] != true }
    val ofKind = own.filter { codecs.contains(codecOf(it)) }
    integer(embedded["streamIndex"], 0.0)?.let { index ->
      val found = ofKind.filter { streamIndex(it)?.toLong() == index }
      if (found.size == 1) return trackId(found[0])
    }
    integer(embedded["ordinal"], 0.0)?.let { ordinal ->
      if (ordinal < own.size && codecs.contains(codecOf(own[ordinal.toInt()]))) return trackId(own[ordinal.toInt()])
    }
    val language = (embedded["language"] as? String)?.lowercase()
    if (language.isNullOrEmpty() || language.length > 35) return null
    val wanted = baseLanguage(language)
    var matches = ofKind.filter { baseLanguage(((it["lang"] as? String) ?: "").lowercase()) == wanted }
    val title = embedded["title"] as? String
    if (matches.size > 1 && !title.isNullOrEmpty()) {
      val titled = matches.filter { ((it["title"] as? String) ?: "").equals(title, ignoreCase = true) }
      if (titled.size == 1) matches = titled
    }
    return if (matches.size == 1) trackId(matches[0]) else null
  }

  private val THREE_LETTER = mapOf(
      "eng" to "en", "fra" to "fr", "fre" to "fr", "deu" to "de", "ger" to "de", "spa" to "es", "ita" to "it", "por" to "pt",
      "nld" to "nl", "dut" to "nl", "jpn" to "ja", "kor" to "ko", "zho" to "zh", "chi" to "zh", "rus" to "ru", "swe" to "sv",
      "nor" to "no", "dan" to "da", "fin" to "fi", "pol" to "pl", "tur" to "tr", "ara" to "ar", "heb" to "he", "hin" to "hi",
      "ces" to "cs", "cze" to "cs", "ell" to "el", "gre" to "el", "hun" to "hu", "ron" to "ro", "rum" to "ro", "ukr" to "uk",
      "vie" to "vi", "tha" to "th", "ind" to "id")

  /** Two- and three-letter codes for the same language compare equal for the common cases. */
  fun baseLanguage(tag: String): String {
    val base = tag.split('-', '_').firstOrNull() ?: tag
    return THREE_LETTER[base] ?: base
  }

  // --- device -----------------------------------------------------------------------------

  /**
   * The viewer's caption size from the system's Accessibility settings, as a scale on the
   * engine's own subtitle size (1 when the viewer has not set one).
   */
  fun captionScale(context: Context, style: Map<*, *>? = null): Double {
    if (style?.get("useSystemCaptionStyle") == false) return 1.0
    val scale = try { (context.getSystemService(Context.CAPTIONING_SERVICE) as? CaptioningManager)?.fontScale?.toDouble() } catch (_: Exception) { null } ?: 1.0
    return if (scale.isFinite() && scale > 0) scale.coerceIn(0.5, 3.0) else 1.0
  }

  /** The size of Portico's own caption style (the subtitle command's `style.scale`), 1 when absent. */
  fun styleScale(style: Map<*, *>?): Double {
    val scale = (style?.get("scale") as? Number)?.toDouble() ?: return 1.0
    return if (scale.isFinite() && scale in 0.5..3.0) scale else 1.0
  }

  /** Only the actual ASS/SSA render kind opts into file-style policy. The
   * pinned mpv's scale mode keeps authored styles while honoring caption size;
   * strip mode renders its text with the existing subtitle style.
   * no bitmap or plain-text track inherits an earlier ASS override. */
  fun captionAssOverride(style: Map<*, *>?, kind: Any?): String =
      if (kind != "ass") "scale" else when (style?.get("keepStyling")) {
        true -> "scale"
        false -> "strip"
        else -> "scale"
      }

  /** An original file's offset is the complete source-relative correction,
   * including explicit zero and fractions. Never add it to the plan, clamp an
   * unsupported clock, or replace an invalid explicit value with the plan. */
  fun originalSubtitleDelay(command: Map<*, *>, render: Map<*, *>): String? {
    if (render.containsKey("timeDomain") && render["timeDomain"] != "source-relative") return null
    if (render.containsKey("offsetMs")) {
      val ms = (render["offsetMs"] as? Number)?.toDouble() ?: return null
      return if (ms.isFinite() && ms in -86_400_000.0..86_400_000.0) (ms / 1000).toString() else null
    }
    val raw = command["offsetUs"] as? String ?: return null
    if (raw.length > 10 || !Regex("-?(?:0|[1-9][0-9]*)").matches(raw)) return null
    val us = raw.toLongOrNull() ?: return null
    return if (us in -600_000_000L..600_000_000L) (us.toDouble() / 1_000_000).toString() else null
  }

  /** Validate only supplied original pairs. Constructing a sibling request from
   * an absent companion is never permission to open it. Local artifact custody
   * is additionally checked by the controller before either file can be opened. */
  fun vobSubPair(index: String, companion: String?, media: String, resource: String?, revision: Long?): Boolean = try {
    if (companion == null || listOf(index, companion, media).any { it.length > 8192 }) false else {
      val idx = java.net.URI(index); val sub = java.net.URI(companion); val source = java.net.URI(media)
      val scheme = idx.scheme?.lowercase()
      val paths = listOf(idx.path ?: "", sub.path ?: "")
      val clean = paths.all { path -> path.startsWith('/') && !path.contains('\\') && !path.contains('%') &&
          path.none { it.code < 0x20 || it.code == 0x7f } && path.split('/').none { it == "." || it == ".." } }
      val bare = listOf(idx, sub).all { it.rawUserInfo == null && it.rawQuery == null && it.rawFragment == null }
      if (!clean || !bare || scheme != sub.scheme?.lowercase() || scheme != source.scheme?.lowercase()
          || !paths[0].endsWith(".idx") || paths[1] != paths[0].removeSuffix(".idx") + ".sub") false
      else if (scheme == "file") source.path?.startsWith('/') == true && listOf(idx, sub, source).all { it.rawAuthority.isNullOrEmpty() }
      else if (scheme != "http" && scheme != "https") false
      else {
        val sameOrigin = listOf(idx, sub).all { it.host != null && it.host.equals(source.host, ignoreCase = true) && it.port == source.port }
        val sourceParts = source.rawPath?.split('/') ?: emptyList()
        val grant = sourceParts.getOrNull(3)
        val word = Regex("[A-Za-z0-9_-]+")
        sameOrigin && source.rawUserInfo == null && sourceParts.size >= 4 && sourceParts[1] == "v1" && sourceParts[2] == "media"
            && grant != null && word.matches(grant) && resource != null && resource.length <= 8192 && word.matches(resource)
            && revision != null && revision in 1..9_007_199_254_740_991L
            && idx.rawPath == "/v1/media/subtitles/$grant/$resource/$revision/original.idx"
            && sub.rawPath == "/v1/media/subtitles/$grant/$resource/$revision/original.sub"
      }
    }
  } catch (_: Exception) { false }

  fun originalSubtitleKey(command: Map<*, *>, render: Map<*, *>): String {
    val embedded = render["embedded"] as? Map<*, *>
    return listOf(command["resourceId"], command["resourceRevision"], render["kind"], embedded?.get("streamIndex"),
        embedded?.get("ordinal"), embedded?.get("language"), render["url"], embedded?.get("title"), render["fonts"],
        if (render["kind"] == "vobsub") render["companionUrl"] else null).joinToString("|") { it?.toString() ?: "" }
  }

  enum class OriginalSubtitleAction { INVALID, REFUSED, REUSE, LOAD }

  /** Validate the current external pair before either cache can admit it. The
   * controller retains the only active/refused state; this decision owns none. */
  fun originalSubtitleAction(key: String, active: String?, refused: Set<String>, externalVobSub: Boolean, pairValid: Boolean): OriginalSubtitleAction = when {
    externalVobSub && !pairValid -> OriginalSubtitleAction.INVALID
    key in refused -> OriginalSubtitleAction.REFUSED
    key == active -> OriginalSubtitleAction.REUSE
    else -> OriginalSubtitleAction.LOAD
  }

  /** The background of Portico's own caption style: "none", "translucent" (the default) or "opaque". */
  fun styleBackground(style: Map<*, *>?): String {
    val value = style?.get("background") as? String
    return if (value == "none" || value == "opaque") value else "translucent"
  }

  /** An explicit canonical choice wins; old two-field commands retain the old
   * CaptioningManager-enabled behavior until their producer is upgraded. */
  fun usesSystemCaptionStyle(style: Map<*, *>?, enabled: Boolean): Boolean =
      (style?.get("useSystemCaptionStyle") as? Boolean) ?: enabled

  fun captionForeground(style: Map<*, *>?): Int = when (style?.get("color")) {
    "yellow" -> android.graphics.Color.YELLOW
    "cyan" -> android.graphics.Color.CYAN
    "green" -> android.graphics.Color.GREEN
    "pink" -> 0xffff80bf.toInt()
    else -> android.graphics.Color.WHITE
  }

  fun captionAtTop(style: Map<*, *>?, authoredTop: Boolean): Boolean =
      authoredTop || style?.get("position") == "top"

  fun captionBottomInset(style: Map<*, *>?): Float = if (style?.get("position") == "raised") .20f else .08f

  /** mpv 0.41 sub-margin-y uses scaled pixels at a 720-high window, not native
   * view pixels or dp (DOCS/man/options.rst, sub-font-size/sub-margin-y). */
  fun mpvCaptionMargin(style: Map<*, *>?): Int = Math.round(720 * captionBottomInset(style))

  /** Select only actual supplied platform faces. A configured system face wins;
   * no rounded face or font-resource identity is invented from a family label. */
  fun <T : Any> captionTypeface(style: Map<*, *>?, system: T?, ordinary: T, serif: T, mono: T): T =
      system ?: when (style?.get("font")) { "serif" -> serif; "mono" -> mono; else -> ordinary }

  private fun systemCaptionStyle(context: Context, style: Map<*, *>?): CaptioningManager.CaptionStyle? {
    val manager = try { context.getSystemService(Context.CAPTIONING_SERVICE) as? CaptioningManager } catch (_: Exception) { null }
    return try { if (manager != null && usesSystemCaptionStyle(style, manager.isEnabled)) manager.userStyle else null } catch (_: Exception) { null }
  }

  fun captionColors(context: Context, style: Map<*, *>?, applyProfileColour: Boolean = true): Pair<Int, Int> {
    val system = systemCaptionStyle(context, style)
    val text = if (system != null && system.hasForegroundColor()) system.foregroundColor
      else if (applyProfileColour) captionForeground(style) else android.graphics.Color.WHITE
    val box = if (system != null && system.hasBackgroundColor()) system.backgroundColor
      else when (styleBackground(style)) { "none" -> android.graphics.Color.TRANSPARENT; "opaque" -> android.graphics.Color.BLACK; else -> 0xB3000000.toInt() }
    return text to box
  }

  data class CaptionEdges(val backColor: Int, val boxed: Boolean, val outline: Int, val shadow: Int)

  /** Existing mpv edge primitives, only for explicit app appearance. A system
   * style or old command keeps its already-resolved background and edge rule. */
  fun mpvCaptionEdges(style: Map<*, *>?, background: Int): CaptionEdges {
    if (style?.get("useSystemCaptionStyle") == false) {
      when (style["background"]) {
        "outline" -> return CaptionEdges(0, false, 3, 0)
        // sub-back-color also supplies sub-shadow-color: black paint without
        // background-box draws a shadow, while transparent paint cannot.
        "shadow" -> return CaptionEdges(0xff000000.toInt(), false, 0, 2)
      }
    }
    val boxed = (background ushr 24) >= 77
    return CaptionEdges(background, boxed, if (boxed) 0 else 3, 0)
  }

  /** mpv's floating point red/green/blue/alpha syntax. */
  fun captionColor(color: Int): String = listOf(
      android.graphics.Color.red(color), android.graphics.Color.green(color),
      android.graphics.Color.blue(color), android.graphics.Color.alpha(color))
      .joinToString("/") { decimal(it / 255.0, 3) }

  /**
   * The existing plain-text line. Canonical colour and the explicit system-style
   * preference are applied on the same retained view; no output fact is inferred.
   * `base` is the size in pixels before either scale.
   */
  fun styleCaptionLine(line: android.widget.TextView, context: Context, style: Map<*, *>?, base: Float, italic: Boolean) {
    val system = systemCaptionStyle(context, style)
    val size = base * captionScale(context, style).toFloat() * styleScale(style).toFloat()
    line.setTextSize(android.util.TypedValue.COMPLEX_UNIT_PX, size)
    val systemFace = try { system?.typeface } catch (_: Exception) { null }
    val face = captionTypeface(style, systemFace, android.graphics.Typeface.DEFAULT, android.graphics.Typeface.SERIF, android.graphics.Typeface.MONOSPACE)
    line.setTypeface(face, if (italic) android.graphics.Typeface.ITALIC else android.graphics.Typeface.NORMAL)
    val (text, box) = captionColors(context, style)
    line.setTextColor(text)
    line.setBackgroundColor(box)
    // Without a box behind it the line needs an edge of its own to stay readable over a bright picture.
    if (android.graphics.Color.alpha(box) < 77) line.setShadowLayer(Math.max(2f, size * 0.08f), 0f, 0f, android.graphics.Color.BLACK) else line.setShadowLayer(0f, 0f, 0f, 0)
  }

  /** Text for mpv, which takes numbers in the C locale whatever the device's language. */
  fun decimal(value: Double, digits: Int = 3): String = String.format(java.util.Locale.ROOT, "%.${digits}f", value)
}

/**
 * Fetches the fonts attached to a styled subtitle file into a folder mpv can read them from.
 * Bounded in number, total size and time; never follows a redirect; a font that cannot be
 * fetched is left out, and the subtitles are then drawn with the nearest system font. Fonts are
 * written to disk as they arrive, never held in memory, and a fetch that was cancelled can
 * neither store a file nor finish a newer one.
 *
 * Main thread only.
 */
internal class PorticoMpvFontFetcher(private val context: Context) {
  private val main = Handler(Looper.getMainLooper())
  private var calls = ArrayList<Call>()
  @Volatile private var generation = 0
  var folder: File? = null
    private set

  /**
   * [fonts] are `{name, url}` pairs on the media's own origin and under its grant. [completion]
   * runs on the main thread with the folder, or null when nothing was fetched.
   */
  fun fetch(fonts: List<Map<String, Any?>>, media: Uri, completion: (File?) -> Unit) {
    cancel()
    val wanted = ArrayList<Pair<String, Uri>>()
    for (font in fonts.take(MAXIMUM_FONTS)) {
      val text = font["url"] as? String ?: continue
      if (text.toByteArray(Charsets.UTF_8).size > 2048) continue
      val url = resolve(text, media) ?: continue
      if (!PorticoMpvSupport.subtitleFileBelongs(url, media)) continue
      val raw = (font["name"] as? String) ?: "font"
      val safe = raw.filter { it.isLetterOrDigit() || it == '.' || it == '-' || it == '_' }.take(80)
      wanted.add(Pair(safe.ifEmpty { "font" }, url))
    }
    if (wanted.isEmpty()) { completion(null); return }
    val directory = File(File(context.cacheDir, "portico-mpv-fonts"), UUID.randomUUID().toString())
    if (!directory.mkdirs()) { completion(null); return }
    folder = directory
    val attempt = ++generation
    // Every font of this fetch draws on one budget.
    val received = java.util.concurrent.atomic.AtomicLong(0)
    val started = java.util.concurrent.CopyOnWriteArrayList<Call>()
    var remaining = wanted.size
    var stored = 0
    val done = { ok: Boolean ->
      main.post {
        if (attempt == generation) {
          if (ok) stored++
          remaining--
          if (remaining == 0) completion(if (stored > 0) directory else null)
        }
      }
      Unit
    }
    for ((position, entry) in wanted.withIndex()) {
      val call = try { client.newCall(Request.Builder().url(entry.second.toString()).build()) } catch (_: Exception) { null }
      if (call == null) {
        remaining--
        continue
      }
      started.add(call)
      call.enqueue(object : Callback {
        override fun onFailure(call: Call, e: IOException) { done(false) }

        override fun onResponse(call: Call, response: Response) {
          val partial = File(directory, "$position-${entry.first}.part")
          val ok = response.use { answer ->
            val source = answer.body?.source()
            if (answer.code != 200 || source == null || (answer.body?.contentLength() ?: 0) > MAXIMUM_BYTES) return@use false
            try {
              var size = 0L
              partial.outputStream().use { out ->
                val buffer = ByteArray(65536)
                while (true) {
                  val count = source.read(buffer)
                  if (count < 0) break
                  size += count
                  // Over the budget, or cancelled: this font and the ones still arriving stop.
                  if (received.addAndGet(count.toLong()) > MAXIMUM_BYTES || attempt != generation) {
                    started.forEach { it.cancel() }
                    return@use false
                  }
                  out.write(buffer, 0, count)
                }
              }
              size > 0 && attempt == generation && partial.renameTo(File(directory, "$position-${entry.first}"))
            } catch (_: IOException) {
              false
            }
          }
          if (!ok) try { partial.delete() } catch (_: Exception) {}
          done(ok)
        }
      })
    }
    calls = ArrayList(started)
    if (remaining == 0) completion(null)
  }

  /**
   * A download's styled track: its attached fonts are files beside the download (`file:` URLs
   * inside Downloads). mpv reads every file of the folder it is given as a font, so they are
   * copied, alone, into a fresh folder of their own, on the same budget as a fetch; the
   * download's folder itself is never handed over. [completion] runs on the main thread with the
   * folder, or null when no font could be copied.
   */
  fun gather(fonts: List<Map<String, Any?>>, completion: (File?) -> Unit) {
    cancel()
    val wanted = ArrayList<Pair<String, File>>()
    for (font in fonts.take(MAXIMUM_FONTS)) {
      val text = font["url"] as? String ?: continue
      if (text.toByteArray(Charsets.UTF_8).size > 2048) continue
      val url = try { Uri.parse(text) } catch (_: Exception) { null } ?: continue
      if (!PorticoMpvSupport.allowedLocalSource(context, url)) continue
      val path = url.path ?: continue
      val raw = (font["name"] as? String) ?: "font"
      val safe = raw.filter { it.isLetterOrDigit() || it == '.' || it == '-' || it == '_' }.take(80)
      wanted.add(Pair(safe.ifEmpty { "font" }, File(path)))
    }
    if (wanted.isEmpty()) { completion(null); return }
    val directory = File(File(context.cacheDir, "portico-mpv-fonts"), UUID.randomUUID().toString())
    if (!directory.mkdirs()) { completion(null); return }
    folder = directory
    val attempt = ++generation
    io.execute {
      var total = 0L
      var stored = 0
      for ((position, entry) in wanted.withIndex()) {
        if (attempt != generation) break
        try {
          val size = entry.second.length()
          if (size <= 0 || total + size > MAXIMUM_BYTES) continue
          entry.second.inputStream().use { input -> File(directory, "$position-${entry.first}").outputStream().use { input.copyTo(it) } }
          total += size
          stored++
        } catch (_: IOException) {
        }
      }
      main.post { if (attempt == generation) completion(if (stored > 0) directory else null) }
    }
  }

  /** Stops any fetch and removes the folder. */
  fun cancel() {
    generation++
    calls.forEach { it.cancel() }
    calls = ArrayList()
    val old = folder
    folder = null
    if (old != null) io.execute { try { old.deleteRecursively() } catch (_: Exception) {} }
  }

  companion object {
    private const val MAXIMUM_FONTS = 16
    /** For all the fonts of one subtitle file together. */
    private const val MAXIMUM_BYTES = 24L * 1024 * 1024
    private val io = java.util.concurrent.Executors.newSingleThreadExecutor { task -> Thread(task, "portico-mpv-fonts").apply { isDaemon = true } }

    /** A scoped media grant, not a bearer credential; a redirect is never followed. */
    private val client: OkHttpClient by lazy {
      OkHttpClient.Builder().porticoTrust().followRedirects(false).followSslRedirects(false).cache(null)
          .callTimeout(20, TimeUnit.SECONDS).connectTimeout(10, TimeUnit.SECONDS).readTimeout(10, TimeUnit.SECONDS).build()
    }

    /** [text] as an absolute address, read against [base] when it is relative. */
    fun resolve(text: String, base: Uri): Uri? = try {
      java.net.URI(base.toString()).resolve(text).toString().let { Uri.parse(it) }.takeIf { it.scheme != null }
    } catch (_: Exception) {
      null
    }
  }
}
