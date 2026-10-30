package tv.getportico.portico.native

import android.net.Uri
import android.os.Handler
import android.os.Looper
import android.os.SystemClock
import java.io.ByteArrayOutputStream
import java.io.IOException
import java.io.OutputStream
import java.net.InetAddress
import java.net.ServerSocket
import java.net.Socket
import java.net.UnknownServiceException
import java.util.concurrent.Executors
import java.util.concurrent.SynchronousQueue
import java.util.concurrent.ThreadPoolExecutor
import java.util.concurrent.TimeUnit
import javax.net.ssl.SSLException
import okhttp3.Call
import okhttp3.OkHttpClient
import okhttp3.Request
import tv.getportico.portico.policy.PorticoMpvCompleteness

/**
 * The mpv engine's only way to the network: the Android counterpart of
 * `Native/PorticoMpvRelay.swift`.
 *
 * mpv (FFmpeg) has its own HTTP client. In the libmpv this app ships, that client cannot see the
 * system's trusted certificates, so it would either skip TLS validation or need a certificate
 * file of its own. Instead mpv is only ever given an address on this device
 * (`http://127.0.0.1:<port>/...`), and this relay fetches the real resource with the app's own
 * HTTP client: the same TLS validation, the same network security rules and the same
 * certificates the rest of the app trusts.
 *
 * The relay serves one playback at a time. It forwards a request only when its path lies under
 * the media grant of the source it was bound to (`/v1/media/<grant>/...`); a source that is not
 * under a grant is served alone, never its folder. That grant is the capability the server
 * issued for this playback, so another app on the device that does not already hold it gets
 * nothing here. Redirects are refused, as they are for the app's other media fetches. Only GET
 * and HEAD are served.
 *
 * An HLS playlist is not passed through as it is: every address in it is rewritten to come back
 * through the relay, so a playlist can never send mpv to fetch something by itself, outside
 * this validation. A playlist naming anything out of scope is refused whole.
 */
internal class PorticoMpvRelay {
  /** What the last failed upstream request said, for the engine's error classification. */
  class Failure(
      /** The upstream HTTP status (400..599), or 0 when there was no answer. */
      val httpStatus: Int,
      /** With no answer: a failure waiting will not change (transport security, cleartext refused). */
      val permanent: Boolean,
      /** `SystemClock.elapsedRealtime()` when it happened. */
      val at: Long,
  )

  private class Exchange(val socket: Socket) {
    @Volatile var call: Call? = null
    /** The request head arrived in time. */
    @Volatile var headRead = false
    var sourceEpoch = 0L
    var mediaResource: String? = null
    var requestRange: String? = null
    var rangeStart: Long? = null
    var rangeEnd: Long? = null
    var resourceEnd = false
    var delivered = 0L
  }

  private val main = Handler(Looper.getMainLooper())
  private val control = Executors.newSingleThreadExecutor { task -> Thread(task, "portico-mpv-relay").apply { isDaemon = true } }
  private val workers = ThreadPoolExecutor(0, MAXIMUM_EXCHANGES, 30, TimeUnit.SECONDS, SynchronousQueue()) { task -> Thread(task, "portico-mpv-relay-io").apply { isDaemon = true } }
  private val lock = Any()

  // Guarded by `lock`.
  private var server: ServerSocket? = null
  private var port = 0
  private var origin: String? = null
  private var originScheme = ""
  private var originHost = ""
  private var originPort = -1
  private var prefixes: List<String> = emptyList()
  private val exchanges = HashSet<Exchange>()
  private val completeness = PorticoMpvCompleteness<Failure>()
  private var sourceEpoch = 0L

  /** The most recent upstream failure, or null. Safe from any thread. */
  @Volatile var lastFailure: Failure? = null
    private set

  fun clearFailure() { lastFailure = null }

  /** A truncated body can remain buffered much longer than a recent-error classification window. */
  val unresolvedFailure: Failure? get() = synchronized(lock) { completeness.unresolved() }

  private fun record(exchange: Exchange, status: Int, permanent: Boolean) {
    synchronized(lock) {
      if (exchange in exchanges && exchange.call?.isCanceled() != true) {
        val failure = Failure(status, permanent, SystemClock.elapsedRealtime())
        lastFailure = failure
        // 416 is also used to probe a resource's natural end; it proves no missing media bytes.
        if (status != 416) exchange.mediaResource?.let { resource ->
          completeness.failed(exchange.sourceEpoch, resource, exchange.requestRange,
              byteOffset(exchange.rangeStart, exchange.delivered), exchange.rangeEnd, failure)
        }
      }
    }
  }

  private fun completed(exchange: Exchange, bytes: Long) {
    synchronized(lock) {
      if (exchange !in exchanges || exchange.call?.isCanceled() == true) return
      exchange.mediaResource?.let { resource ->
        completeness.completed(exchange.sourceEpoch, resource, exchange.requestRange, exchange.rangeStart,
            byteOffset(exchange.rangeStart, bytes), exchange.resourceEnd)
      }
    }
  }

  private fun byteOffset(start: Long?, bytes: Long): Long? = start?.takeIf { it <= Long.MAX_VALUE - bytes }?.plus(bytes)

  /**
   * Binds the relay to [source] and answers with the address mpv should open instead, or null
   * when the relay could not listen. [completion] runs on the main thread.
   */
  fun bind(source: Uri, completion: (String?) -> Unit) {
    control.execute {
      val scheme = source.scheme?.lowercase()
      val host = source.host
      if ((scheme != "http" && scheme != "https") || host.isNullOrEmpty() || source.userInfo != null) {
        main.post { completion(null) }
        return@execute
      }
      val listening = synchronized(lock) {
        originScheme = scheme
        originHost = host.lowercase()
        originPort = source.port
        // An IPv6 literal keeps its brackets in an address.
        val name = if (host.contains(':') && !host.startsWith("[")) "[$host]" else host
        origin = "$scheme://$name" + (if (source.port >= 0) ":${source.port}" else "")
        prefixes = prefixesFor(source.path ?: "/")
        cancelAllLocked()
        lastFailure = null
        sourceEpoch = completeness.reset()
        ensureListeningLocked()
      }
      val local = if (listening == 0) null else local(source, listening)
      main.post { completion(local) }
    }
  }

  /**
   * The loopback address for another resource of the bound playback (an external subtitle
   * file), or null when it is outside the bound scope or the relay is not listening.
   */
  fun localUrl(remote: Uri): String? = synchronized(lock) {
    if (origin == null || port == 0) return null
    if (remote.scheme?.lowercase() != originScheme || remote.host?.lowercase() != originHost || remote.port != originPort) return null
    if (remote.userInfo != null || remote.encodedFragment != null || !allowedLocked(remote.encodedPath ?: "")) return null
    local(remote, port)
  }

  /** Ends every transfer and forgets the bound source. The listener stays for the next playback. */
  fun unbind() {
    control.execute {
      synchronized(lock) {
        origin = null
        prefixes = emptyList()
        cancelAllLocked()
        sourceEpoch = completeness.reset()
        lastFailure = null
      }
    }
  }

  /** Stops listening and ends every transfer. */
  fun stop() {
    control.execute {
      synchronized(lock) {
        origin = null
        prefixes = emptyList()
        cancelAllLocked()
        sourceEpoch = completeness.reset()
        lastFailure = null
        try { server?.close() } catch (_: IOException) {}
        server = null
        port = 0
      }
    }
  }

  /** A retired bridge has no next playback: release its executors as well as its sockets. */
  fun destroy() {
    stop()
    control.shutdown()
    workers.shutdownNow()
  }

  // --- scope ------------------------------------------------------------------------------

  private fun prefixesFor(path: String): List<String> {
    val parts = path.split('/').filter { it.isNotEmpty() }
    // The media's own files, and the subtitle files and fonts the server keeps under the same grant.
    if (parts.size >= 3 && parts[0] == "v1" && parts[1] == "media") return listOf("/v1/media/" + parts[2], "/v1/media/subtitles/" + parts[2])
    // A non-grant address grants only the source itself, never a whole server folder.
    return listOf(path)
  }

  private fun local(remote: Uri, port: Int): String {
    val query = remote.encodedQuery
    return "http://127.0.0.1:$port" + (remote.encodedPath ?: "/") + (if (query != null) "?$query" else "")
  }

  private fun allowedLocked(path: String): Boolean {
    if (!path.startsWith("/") || path.contains('\\')) return false
    val lowered = path.lowercase()
    if (lowered.contains("%2e") || lowered.contains("%2f") || lowered.contains("%5c")) return false
    if (path.split('/').any { it == ".." || it == "." }) return false
    for (prefix in prefixes) {
      if (path == prefix) return true
      if (prefix.startsWith("/v1/media/") && path.startsWith("$prefix/")) return true
    }
    return false
  }

  /** [url], built from a request, is still on the bound origin and in scope. */
  private fun inScopeLocked(url: Uri): Boolean =
      origin != null && url.scheme?.lowercase() == originScheme && url.host?.lowercase() == originHost && url.port == originPort &&
          url.userInfo == null && url.encodedFragment == null && allowedLocked(url.encodedPath ?: "")

  // --- listener ---------------------------------------------------------------------------

  /** The port being listened on, or 0. */
  private fun ensureListeningLocked(): Int {
    server?.let { if (!it.isClosed && port != 0) return port }
    val made = try { ServerSocket(0, 16, InetAddress.getByAddress(byteArrayOf(127, 0, 0, 1))) } catch (_: IOException) { return 0 }
    server = made
    port = made.localPort
    Thread({ acceptLoop(made) }, "portico-mpv-relay-accept").apply { isDaemon = true }.start()
    return port
  }

  private fun acceptLoop(listener: ServerSocket) {
    while (true) {
      val socket = try { listener.accept() } catch (_: IOException) { break }
      val exchange = Exchange(socket)
      synchronized(lock) { exchanges.add(exchange) }
      // A connection that sends no complete request is not kept.
      timers.schedule({ if (!exchange.headRead) try { socket.close() } catch (_: IOException) {} }, HEAD_SECONDS, TimeUnit.SECONDS)
      try {
        workers.execute {
          try { serve(exchange) } catch (_: Exception) {} finally { end(exchange) }
        }
      } catch (_: Exception) {
        // Too many transfers at once: this one is refused.
        end(exchange)
      }
    }
    synchronized(lock) {
      if (server === listener) { server = null; port = 0 }
    }
  }

  private fun end(exchange: Exchange) {
    synchronized(lock) { exchanges.remove(exchange) }
    exchange.call?.cancel()
    try { exchange.socket.close() } catch (_: IOException) {}
  }

  private fun cancelAllLocked() {
    for (exchange in exchanges.toList()) {
      exchange.call?.cancel()
      try { exchange.socket.close() } catch (_: IOException) {}
    }
    exchanges.clear()
  }

  // --- one request ------------------------------------------------------------------------

  /** A short answer the relay makes itself (a refusal or an upstream failure). */
  private fun reply(out: OutputStream, status: Int) {
    out.write("HTTP/1.1 $status Relay\r\nContent-Length: 0\r\nConnection: close\r\n\r\n".toByteArray(Charsets.US_ASCII))
    out.flush()
  }

  /** The request head, without its final blank line; null when it is too long or the peer left. */
  private fun readHead(socket: Socket): String? {
    // Nothing after the head is read from this connection, so reading ahead loses nothing.
    val input = socket.getInputStream().buffered(4096)
    val head = ByteArrayOutputStream(1024)
    var matched = 0
    while (head.size() <= MAXIMUM_HEAD) {
      val byte = input.read()
      if (byte < 0) return null
      head.write(byte)
      matched = when {
        byte == '\r'.code && (matched == 0 || matched == 2) -> matched + 1
        byte == '\n'.code && (matched == 1 || matched == 3) -> matched + 1
        byte == '\r'.code -> 1
        else -> 0
      }
      if (matched == 4) return String(head.toByteArray(), 0, head.size() - 4, Charsets.UTF_8)
    }
    return null
  }

  private fun serve(exchange: Exchange) {
    val socket = exchange.socket
    socket.soTimeout = (HEAD_SECONDS * 1000).toInt()
    val out = socket.getOutputStream()
    val head = readHead(socket) ?: return
    exchange.headRead = true
    val lines = head.split("\r\n")
    val request = lines.first().split(' ').filter { it.isNotEmpty() }
    if (request.size != 3 || (request[0] != "GET" && request[0] != "HEAD")) { reply(out, 405); return }
    val target = request[1]
    val path = target.substringBefore('?')
    // The address is checked as asked for, and again as built: scheme, host, port and path.
    val upstream = if (!target.startsWith("/") || target.toByteArray(Charsets.UTF_8).size > 8192) null else synchronized(lock) {
      val base = origin
      val built = if (base != null && allowedLocked(path)) try { Uri.parse(base + target) } catch (_: Exception) { null } else null
      if (built != null && inScopeLocked(built)) {
        exchange.sourceEpoch = sourceEpoch
        // The second prefix contains subtitles/fonts. Their failure cannot truncate the media.
        val mediaPrefix = prefixes.firstOrNull()
        if (request[0] == "GET" && mediaPrefix != null && (path == mediaPrefix || path.startsWith("$mediaPrefix/"))) exchange.mediaResource = built.toString()
        built
      } else null
    }
    if (upstream == null) { reply(out, 404); return }
    val playlistPath = (upstream.path ?: "").lowercase().endsWith(".m3u8")
    var range: String? = null
    for (line in lines.drop(1)) {
      val colon = line.indexOf(':')
      if (colon <= 0) continue
      if (line.substring(0, colon).trim().lowercase() == "range") {
        val value = line.substring(colon + 1).trim()
        if (value.length <= 128 && RANGE.matches(value)) range = value
      }
    }
    exchange.requestRange = if (playlistPath) null else range
    exchange.rangeStart = if (exchange.requestRange == null) 0L else exchange.requestRange!!.substringAfter("bytes=").substringBefore('-').toLongOrNull()
    exchange.rangeEnd = exchange.requestRange?.substringAfter('-')?.toLongOrNull()?.takeIf { it < Long.MAX_VALUE }?.plus(1)
    val wire = try {
      Request.Builder().url(upstream.toString()).method(request[0], null)
          .header("Accept-Encoding", "identity").header("Accept", "*/*")
          // A rewritten playlist has different byte offsets: it is always fetched whole.
          .apply { if (range != null && !playlistPath) header("Range", range) }
          .build()
    } catch (_: IllegalArgumentException) {
      reply(out, 404)
      return
    }
    val call = client.newCall(wire)
    synchronized(lock) {
      if (exchange !in exchanges || socket.isClosed) return
      exchange.call = call
    }
    val response = try {
      call.execute()
    } catch (error: IOException) {
      if (!call.isCanceled()) {
        record(exchange, 0, permanent(error))
        try { reply(out, 502) } catch (_: IOException) {}
      }
      return
    }
    response.use { answer ->
      val status = answer.code
      if (status in 300..399) {
        // A redirect is never followed, and mpv must not follow it on its own either.
        record(exchange, 502, false)
        reply(out, 502)
        return
      }
      if (status >= 400) record(exchange, status, false)
      if (status == 200) {
        exchange.rangeStart = 0L
        exchange.rangeEnd = answer.body?.contentLength()?.takeIf { it >= 0 }
        exchange.resourceEnd = true
      } else if (status == 206) {
        val match = CONTENT_RANGE.matchEntire(answer.header("Content-Range") ?: "")
        val start = match?.groupValues?.get(1)?.toLongOrNull()
        val end = match?.groupValues?.get(2)?.toLongOrNull()?.takeIf { it < Long.MAX_VALUE }?.plus(1)
        if (start != null && end != null && end > start) {
          exchange.rangeStart = start
          exchange.rangeEnd = end
          exchange.resourceEnd = match?.groupValues?.get(3)?.toLongOrNull() == end
        }
      }
      val type = (answer.header("Content-Type") ?: "").lowercase()
      if ((status == 200 || status == 206) && request[0] != "HEAD" && (playlistPath || type.contains("mpegurl"))) {
        servePlaylist(exchange, answer, upstream, out)
        return
      }
      val text = StringBuilder("HTTP/1.1 $status Relay\r\n")
      for (name in FORWARDED) {
        val value = answer.header(name) ?: continue
        if (value.toByteArray(Charsets.UTF_8).size <= 1024 && !value.contains('\r') && !value.contains('\n')) text.append(name).append(": ").append(value).append("\r\n")
      }
      text.append("Connection: close\r\n\r\n")
      out.write(text.toString().toByteArray(Charsets.ISO_8859_1))
      out.flush()
      if (request[0] == "HEAD") return
      val source = answer.body?.byteStream() ?: return
      val buffer = ByteArray(64 * 1024)
      // mpv stops reading when its cache is full; the write then waits, and the upstream
      // transfer waits with it.
      socket.soTimeout = 0
      while (true) {
        val count = try {
          source.read(buffer)
        } catch (error: IOException) {
          // The body was cut short: closing here lets mpv ask again from where it stopped.
          if (!call.isCanceled()) record(exchange, 0, permanent(error))
          return
        }
        if (count < 0) break
        out.write(buffer, 0, count)
        exchange.delivered += count
      }
      out.flush()
      if (status == 200 || status == 206) {
        val expected = exchange.rangeEnd?.let { end -> exchange.rangeStart?.let { end - it } }
        if (expected != null && exchange.delivered < expected) record(exchange, 0, false)
        else completed(exchange, exchange.delivered)
      }
    }
  }

  /**
   * An HLS playlist: read whole (bounded), every address in it rewritten to this relay, and
   * answered as a complete document. Anything that cannot be rewritten refuses the playlist.
   */
  private fun servePlaylist(exchange: Exchange, answer: okhttp3.Response, upstream: Uri, out: OutputStream) {
    val refuse = {
      record(exchange, 502, false)
      reply(out, 502)
    }
    val body = answer.body
    if (body == null || body.contentLength() > MAXIMUM_PLAYLIST) { refuse(); return }
    val data = try {
      val buffer = ByteArrayOutputStream()
      val chunk = ByteArray(16 * 1024)
      val source = body.byteStream()
      var over = false
      while (true) {
        val count = source.read(chunk)
        if (count < 0) break
        if (buffer.size() + count > MAXIMUM_PLAYLIST) { over = true; break }
        buffer.write(chunk, 0, count)
      }
      if (over) null else buffer.toByteArray()
    } catch (error: IOException) {
      record(exchange, 0, permanent(error))
      try { reply(out, 502) } catch (_: IOException) {}
      return
    }
    val rewritten = if (data == null) null else PorticoMpvSupport.relayPlaylist(data, upstream) { remote ->
      synchronized(lock) { if (port != 0 && inScopeLocked(remote)) local(remote, port) else null }
    }
    if (rewritten == null) { refuse(); return }
    out.write("HTTP/1.1 200 Relay\r\nContent-Type: application/vnd.apple.mpegurl\r\nContent-Length: ${rewritten.size}\r\nConnection: close\r\n\r\n".toByteArray(Charsets.US_ASCII))
    out.write(rewritten)
    out.flush()
    completed(exchange, data!!.size.toLong())
  }

  companion object {
    private const val MAXIMUM_EXCHANGES = 16
    private const val MAXIMUM_PLAYLIST = 2 * 1024 * 1024
    /** How long a connection has to send its request. */
    private const val HEAD_SECONDS = 10L
    private val timers = java.util.concurrent.ScheduledThreadPoolExecutor(1) { task -> Thread(task, "portico-mpv-relay-timer").apply { isDaemon = true } }.apply { removeOnCancelPolicy = true }
    private const val MAXIMUM_HEAD = 16 * 1024
    private val RANGE = Regex("^bytes=[0-9]*-[0-9]*$")
    private val CONTENT_RANGE = Regex("^bytes ([0-9]+)-([0-9]+)/([0-9]+|\\*)$", RegexOption.IGNORE_CASE)
    private val FORWARDED = listOf("Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "ETag", "Last-Modified")

    /** Media addresses are capability URLs: no cookies, no cache, no redirects. */
    private val client: OkHttpClient by lazy {
      OkHttpClient.Builder().porticoTrust().followRedirects(false).followSslRedirects(false).cache(null)
          .connectTimeout(15, TimeUnit.SECONDS).readTimeout(30, TimeUnit.SECONDS).build()
    }

    /** A failure that waiting will not change: the connection is refused on security grounds. */
    private fun permanent(error: IOException): Boolean =
        generateSequence<Throwable>(error) { it.cause }.take(4).any { it is SSLException || it is UnknownServiceException || it is java.security.cert.CertificateException }
  }
}
