package tv.getportico.portico.native

import android.net.Uri
import android.os.Handler
import android.os.Looper
import android.os.SystemClock
import java.io.IOException
import java.util.concurrent.TimeUnit
import okhttp3.Call
import okhttp3.Callback
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import okio.Buffer
import org.json.JSONArray
import org.json.JSONObject

/**
 * The chosen subtitle track, drawn by the app: the Android counterpart of
 * `Native/PorticoSubtitleSelection.m`.
 *
 * The server publishes every subtitle track (embedded, sidecar, uploaded or from a provider) as
 * one cue document under the playback session's own media grant. This reads that document,
 * checks it again every 25 seconds (the answer is also the proof the session may still show it),
 * and answers which text is on screen at a position. Authorisation lapses 40 seconds after the
 * last good answer: a revoked session's subtitles go dark with its video.
 *
 * Main thread only.
 */
internal class PorticoSubtitleSelection(
  private val elapsedRealtime: () -> Long = { SystemClock.elapsedRealtime() },
  private val main: Scheduler = MainScheduler(),
  private val calls: Call.Factory? = null,
) {
  /** The owner still runs on the main thread; tests control only dispatch and elapsed time. */
  internal interface Scheduler {
    fun post(action: Runnable)
    fun postDelayed(action: Runnable, delayMs: Long)
    fun removeCallbacks(action: Runnable)
  }

  private class MainScheduler : Scheduler {
    private val handler = Handler(Looper.getMainLooper())
    override fun post(action: Runnable) { handler.post(action) }
    override fun postDelayed(action: Runnable, delayMs: Long) { handler.postDelayed(action, delayMs) }
    override fun removeCallbacks(action: Runnable) { handler.removeCallbacks(action) }
  }

  class Cue(val start: Long, val end: Long, val text: String, val italic: Boolean, val top: Boolean)
  data class Display(val text: String, val italic: Boolean, val top: Boolean)

  var onText: ((Display) -> Unit)? = null
  var onStatus: ((String) -> Unit)? = null

  /**
   * Whether a `file:` address is one this app may read (a file inside its own Downloads folder).
   * A downloaded title's subtitles are WebVTT files there; without this none is read.
   */
  var allowsLocalFile: ((Uri) -> Boolean)? = null

  /** A track is chosen, so the app is drawing text over the picture. */
  val overlayActive: Boolean get() = command != null

  private var command: Map<String, Any?>? = null
  private var url: Uri? = null
  private var cues: List<Cue>? = null
  private var prefixEnds = LongArray(0)
  private var etag: String? = null
  private var offsetUs = 0L
  private var revision = 0L
  private var authorizedUntil = 0L
  private var lastRequest = 0L
  private var call: Call? = null
  private var generation = 0
  private var positionUs = 0L
  private var display = Display("", italic = false, top = false)
  private var timer: Runnable? = null
  /** A download's subtitle file on this device: read once, never re-checked with a server. */
  private var local = false

  fun invalidate() {
    generation++
    local = false
    call?.cancel()
    call = null
    timer?.let { main.removeCallbacks(it) }
    timer = null
    command = null
    url = null
    cues = null
    prefixEnds = LongArray(0)
    etag = null
    revision = 0
    offsetUs = 0
    authorizedUntil = 0
    lastRequest = 0
    show("")
  }

  private fun show(text: String, italic: Boolean = false, top: Boolean = false) {
    val next = Display(text, italic && text.isNotEmpty(), top && text.isNotEmpty())
    if (next == display) return
    display = next
    onText?.invoke(next)
  }

  private fun status(error: String) { onStatus?.invoke(error) }

  /** [value] is the scoped `subtitles` part of a playback command, or null for no track. */
  fun apply(value: Map<String, Any?>?, source: String?) {
    if (value == null || value["mode"] != "track") {
      if (command != null || cues != null) invalidate()
      show("")
      return
    }
    val offset = decimalUs(value["offsetUs"], 600_000_000L, signed = true)
    val documentUrl = value["documentUrl"] as? String
    val resourceId = value["resourceId"] as? String
    if (!positiveInteger(value["revision"]) || !positiveInteger(value["resourceRevision"]) || offset == null || documentUrl == null || resourceId == null) {
      invalidate()
      status("The subtitle command is invalid.")
      return
    }
    // The document must be this playback session's own: the same origin as the media, under the
    // grant the media address carries, at exactly the resource and revision the command names.
    val target = try { Uri.parse(documentUrl) } catch (_: Exception) { null }
    val media = try { Uri.parse(source ?: "") } catch (_: Exception) { null }
    // A downloaded title: its subtitles are a WebVTT file beside it, inside the app's Downloads
    // folder. Only a download may name one, and only there.
    if (target?.scheme?.lowercase() == "file") {
      val allowed = allowsLocalFile
      if (media?.scheme?.lowercase() != "file" || allowed == null || !allowed(target) || !allowed(media) || target.path?.lowercase()?.endsWith(".vtt") != true) {
        invalidate()
        status("The subtitle address does not belong to this playback session.")
        return
      }
      val held = command
      if (local && url == target && held != null && held["binding"] == value["binding"]) {
        command = value
        revision = (value["revision"] as Number).toLong()
        offsetUs = offset
        tick(positionUs)
        return
      }
      invalidate()
      url = target
      local = true
      command = value
      revision = (value["revision"] as Number).toLong()
      offsetUs = offset
      readLocal(target)
      return
    }
    val grant = media?.pathSegments?.getOrNull(2) ?: ""
    // Subtitle files have a place of their own beside the media: /v1/media/subtitles/<grant>/<resource>/<revision>.
    val expected = "/v1/media/subtitles/$grant/$resourceId/${(value["resourceRevision"] as Number).toLong()}"
    // The document travels the way the media does: the same scheme, host and port, under the
    // media's own grant. A server reached over plain HTTP serves its subtitles over plain HTTP too.
    if (target == null || media == null || !(target.scheme == "https" || target.scheme == "http") || target.scheme != media.scheme ||
        target.host?.lowercase() != media.host?.lowercase() || target.port != media.port || target.userInfo != null ||
        target.encodedQuery != null || target.encodedFragment != null || target.path != expected || !ROUTE.matches(expected)) {
      invalidate()
      status("The subtitle address does not belong to this playback session.")
      return
    }
    val next = (value["revision"] as Number).toLong()
    val current = command
    if (current != null && current["binding"] == value["binding"] && next < revision) return
    val refreshRequested = current?.get("refreshRevision") != value["refreshRevision"]
    val sameResource = url == target && current?.get("binding") == value["binding"]
    if (!sameResource) {
      invalidate()
      url = target
    }
    command = value
    revision = next
    offsetUs = offset
    if (timer == null) {
      val tick = object : Runnable {
        override fun run() {
          val now = elapsedRealtime()
          if (now > authorizedUntil) show("")
          if (call == null && now - lastRequest >= 25_000) fetch()
          main.postDelayed(this, 1000)
        }
      }
      timer = tick
      main.postDelayed(tick, 1000)
    }
    if (!sameResource || refreshRequested) fetch() else tick(positionUs)
  }

  /**
   * Reads a download's WebVTT file off the main thread and keeps its cues. Tags are dropped (the
   * line is plain text, italic when the cue is), and a cue that cannot be read costs only itself.
   */
  private fun readLocal(target: Uri) {
    val attempt = generation
    val path = target.path ?: return
    Thread({
      val parsed = try {
        val file = java.io.File(path)
        if (file.length() > BYTE_LIMIT) null else parseWebVtt(file.readText(Charsets.UTF_8))
      } catch (_: Exception) { null }
      main.post {
        if (attempt != generation || !local) return@post
        if (parsed == null) { status("Subtitles could not be read from this download."); return@post }
        cues = parsed
        var maxEnd = 0L
        prefixEnds = LongArray(parsed.size) { index -> maxOf(maxEnd, parsed[index].end).also { maxEnd = it } }
        authorizedUntil = Long.MAX_VALUE
        status("")
        tick(positionUs)
      }
    }, "portico-subtitle-file").start()
  }

  private fun fetch() {
    if (local) return
    val target = url ?: return
    if (call != null) return
    lastRequest = elapsedRealtime()
    val request = Request.Builder().url(target.toString()).header("Accept", "application/json")
    // The validator of the document already held, so "still authorised, unchanged" is one header.
    val validator = etag
    if (!validator.isNullOrEmpty() && cues != null) request.header("If-None-Match", validator)
    val attempt = generation
    val started = (calls ?: client).newCall(request.build())
    call = started
    started.enqueue(object : Callback {
      override fun onFailure(call: Call, e: IOException) { main.post { finished(attempt, started, null) } }

      override fun onResponse(call: Call, response: Response) {
        var body: ByteArray? = null
        var failure: String? = null
        var transient = false
        val code = response.code
        val tag = response.header("ETag")?.takeIf { it.length <= 256 }
        response.use {
          if (code == 200) {
            val json = it.header("Content-Type")?.substringBefore(';')?.trim()?.lowercase() == "application/json"
            val source = it.body?.source()
            if (!json || source == null || (it.body?.contentLength() ?: 0) > BYTE_LIMIT) failure = "Subtitles are unavailable or access was revoked."
            else try {
              val buffer = Buffer()
              while (source.read(buffer, 16384) != -1L) if (buffer.size > BYTE_LIMIT) { failure = "Subtitle data exceeded the supported limit."; break }
              if (failure == null) body = buffer.readByteArray()
            } catch (_: IOException) {
              transient = true
              failure = "Subtitles could not be read. Check the server connection."
            }
          } else if (code in 500..599) {
            transient = true
            failure = "Subtitles could not be read. Check the server connection."
          } else if (code != 304) {
            failure = "Subtitles are unavailable or access was revoked."
          }
        }
        main.post { finished(attempt, started, Answer(code, tag, body, failure, transient)) }
      }
    })
  }

  private class Answer(val code: Int, val etag: String?, val body: ByteArray?, val failure: String?, val transient: Boolean)

  private fun finished(attempt: Int, started: Call, answer: Answer?) {
    if (attempt != generation || call !== started) return
    call = null
    if (answer == null || answer.transient) {
      // Retry in five seconds, while a previous grant may still authorize the held cues.
      lastRequest = elapsedRealtime() - 20_000
      // Keep the previous grant's deadline: a failed check cannot extend authorization.
      tick(positionUs)
      status("Subtitles could not be read. Check the server connection.")
      return
    }
    // 304 is the server saying: checked in full, still yours, unchanged.
    if (answer.code == 304 && !etag.isNullOrEmpty() && cues != null) {
      authorizedUntil = elapsedRealtime() + 40_000
      status("")
      tick(positionUs)
      return
    }
    if (answer.failure != null || answer.body == null) {
      cues = null
      authorizedUntil = 0
      show("")
      status(answer.failure ?: "Subtitles are unavailable or access was revoked.")
      return
    }
    val parsed = parse(answer.body)
    if (parsed == null) {
      etag = null
      cues = null
      authorizedUntil = 0
      show("")
      status("The server returned invalid subtitle timing or text.")
      return
    }
    etag = answer.etag
    cues = parsed
    var maxEnd = 0L
    prefixEnds = LongArray(parsed.size) { index -> maxOf(maxEnd, parsed[index].end).also { maxEnd = it } }
    authorizedUntil = elapsedRealtime() + 40_000
    status("")
    tick(positionUs)
  }

  /** Strict about the envelope, forgiving about cues: one bad cue costs only itself. */
  private fun parse(bytes: ByteArray): List<Cue>? {
    val document = try { JSONObject(bytes.toString(Charsets.UTF_8)) } catch (_: Exception) { return null }
    val list = document.opt("cues") as? JSONArray ?: return null
    if (PorticoProfileTrust.number(document.opt("version")) != 1.0 || document.opt("timeDomain") != "source-relative" || list.length() > 20000) return null
    val out = ArrayList<Cue>(list.length())
    var previous = -1L
    var textBytes = 0L
    for (i in 0 until list.length()) {
      val cue = list.opt(i) as? JSONObject ?: continue
      val start = decimalUs(cue.opt("startUs"), DAY_US, signed = false) ?: continue
      val end = decimalUs(cue.opt("endUs"), DAY_US, signed = false) ?: continue
      val text = cue.opt("text") as? String ?: continue
      if (end <= start || start < previous || !plainText(text)) continue
      textBytes += text.toByteArray(Charsets.UTF_8).size
      if (textBytes > 4_194_304) return null
      previous = start
      out.add(Cue(start, end, text, cue.opt("italic") == true, cue.opt("top") == true))
    }
    if (out.isEmpty() && list.length() > 0) return null
    return out
  }

  /** The text on screen at [positionUs] of the media's own timeline. */
  fun tick(positionUs: Long) {
    this.positionUs = positionUs
    val list = cues
    if (command == null || list == null || elapsedRealtime() > authorizedUntil) { show(""); return }
    val now = positionUs - offsetUs
    var low = 0
    var high = list.size
    while (low < high) { val mid = (low + high) ushr 1; if (list[mid].start <= now) low = mid + 1 else high = mid }
    var italic = false
    var top = false
    val active = ArrayList<String>(2)
    var i = low - 1
    while (i >= 0) {
      if (prefixEnds[i] <= now) break
      val cue = list[i]
      if (cue.end > now) { active.add(0, cue.text); italic = italic || cue.italic; top = top || cue.top }
      i--
    }
    show(active.joinToString("\n"), italic, top)
  }

  companion object {
    private val VTT_TIMING = Regex("^\\s*(?:(\\d{1,3}):)?(\\d{2}):(\\d{2})[.,](\\d{3})\\s+-->\\s+(?:(\\d{1,3}):)?(\\d{2}):(\\d{2})[.,](\\d{3})(.*)$")
    private val VTT_TAG = Regex("<[^>]{0,64}>")
    private val VTT_LINE = Regex("\\bline:(-?\\d{1,3})(%?)")

    /** The cues of a WebVTT file, in order. Bounded like the server's cue document. */
    internal fun parseWebVtt(text: String): List<Cue> {
      val lines = text.replace("\r\n", "\n").replace('\r', '\n').split('\n')
      val out = ArrayList<Cue>()
      var textBytes = 0L
      var i = 0
      while (i < lines.size && out.size < 20000) {
        val match = VTT_TIMING.find(lines[i])
        i++
        if (match == null) continue
        val g = match.groupValues
        fun part(index: Int) = g[index].toLongOrNull() ?: 0L
        val start = ((part(1) * 60 + part(2)) * 60 + part(3)) * 1_000_000 + part(4) * 1000
        val end = ((part(5) * 60 + part(6)) * 60 + part(7)) * 1_000_000 + part(8) * 1000
        val settings = g[9]
        val body = ArrayList<String>()
        while (i < lines.size && lines[i].isNotEmpty()) { body.add(lines[i]); i++ }
        val raw = body.joinToString("\n")
        val italic = raw.contains("<i>") || raw.contains("<i.")
        val plain = VTT_TAG.replace(raw, "").replace("&lt;", "<").replace("&gt;", ">").replace("&nbsp;", " ").replace("&lrm;", "").replace("&rlm;", "").replace("&amp;", "&").trim()
        if (end <= start || end > DAY_US || !plainText(plain)) continue
        textBytes += plain.toByteArray(Charsets.UTF_8).size
        if (textBytes > 4_194_304) break
        // A cue placed in the upper half of the picture is drawn at the top.
        val position = VTT_LINE.find(settings)
        val top = position?.let { val value = it.groupValues[1].toIntOrNull() ?: -1; if (it.groupValues[2].isNotEmpty()) value in 0..49 else value in 0..7 } ?: false
        out.add(Cue(start, end, plain, italic, top))
      }
      out.sortBy { it.start }
      return out
    }

    private const val BYTE_LIMIT = 8L * 1024 * 1024
    private const val DAY_US = 86_400_000_000L
    private val ROUTE = Regex("^/v1/media/subtitles/[A-Za-z0-9_-]{1,128}/[A-Za-z0-9_-]{1,128}/[1-9][0-9]*$")

    /** A scoped media grant, not a bearer credential; a redirect is never followed. */
    private val client: OkHttpClient by lazy {
      OkHttpClient.Builder().porticoTrust().followRedirects(false).followSslRedirects(false).cache(null)
          .callTimeout(20, TimeUnit.SECONDS).connectTimeout(15, TimeUnit.SECONDS).readTimeout(15, TimeUnit.SECONDS).build()
    }

    fun positiveInteger(value: Any?): Boolean {
      val n = (value as? Number)?.toDouble() ?: return false
      return n.isFinite() && n >= 1 && n <= 9007199254740991.0 && Math.floor(n) == n
    }

    /** A decimal string of microseconds, in its one canonical spelling, within [maximum]. */
    fun decimalUs(value: Any?, maximum: Long, signed: Boolean): Long? {
      val s = value as? String ?: return null
      if (s.isEmpty() || s.length > 19) return null
      val negative = s.startsWith("-")
      if (negative && !signed) return null
      val start = if (negative) 1 else 0
      if (start == s.length || (s.length - start > 1 && s[start] == '0')) return null
      var n = 0L
      for (i in start until s.length) {
        val c = s[i]
        if (c < '0' || c > '9' || n > (maximum - (c - '0')) / 10) return null
        n = n * 10 + (c - '0')
      }
      if (negative && n == 0L) return null
      return if (negative) -n else n
    }

    private fun plainText(value: String): Boolean {
      if (value.isEmpty() || value.toByteArray(Charsets.UTF_8).size > 8192 || value.contains("{\\")) return false
      return value.none { (it.code < 32 && it.code != 9 && it.code != 10) || it.code == 127 }
    }
  }
}
