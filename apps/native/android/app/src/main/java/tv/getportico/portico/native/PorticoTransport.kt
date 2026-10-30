package tv.getportico.portico.native

import android.util.Base64
import com.facebook.react.bridge.Arguments
import com.facebook.react.bridge.Promise
import com.facebook.react.bridge.ReadableMap
import com.facebook.react.bridge.ReadableType
import java.io.File
import java.io.IOException
import java.io.InterruptedIOException
import java.net.ConnectException
import java.net.NoRouteToHostException
import java.net.PortUnreachableException
import java.net.SocketTimeoutException
import java.net.UnknownHostException
import java.nio.ByteBuffer
import java.nio.charset.CharacterCodingException
import java.nio.charset.CodingErrorAction
import java.util.concurrent.TimeUnit
import java.util.concurrent.Executors
import java.util.concurrent.ScheduledFuture
import okhttp3.Call
import okhttp3.Callback
import okhttp3.Dispatcher
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import okhttp3.MediaType
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.MultipartBody
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody
import okhttp3.Response
import okio.Buffer
import okio.BufferedSink

/**
 * The bounded JSON control transport behind `PorticoRuntime`'s `queueRequest`, `networkRequest`
 * and `profileRequest`. These requests carry credentials and device proofs, so the transport
 * never follows a redirect, keeps no cookies and no cache, validates TLS against the system
 * trust store or the owner's pinned certificate ([PorticoServerTrust]), and never accumulates an
 * unbounded response.
 *
 * One shared client, so connections and TLS sessions are reused; at most 12 requests in flight
 * and 64 waiting in order, the same admission the Apple transport applies. The bounds are per
 * request: 1 MB responses (8 MB for API reads) and 15 seconds, except subtitle uploads
 * which have a 110-second total budget and 60-second read/write idle timeouts.
 */
internal object PorticoTransport {
  private const val MAX_ACTIVE = 12
  private const val MAX_WAITING = 64
  const val DEFAULT_LIMIT = 1L shl 20
  const val READ_LIMIT = 8L shl 20
  private object SubtitleUpload

  private class Entry(val identifier: String, val owner: Any, val limit: Long, var promise: Promise?, val critical: Boolean, val binary: Boolean, val streaming: Boolean) {
    var deadline: ScheduledFuture<*>? = null
    var call: Call? = null
    var completed = false
    var response: Response? = null
    var pull: Promise? = null
  }
  private data class StreamEnd(val owner: Any, val failure: String?, val code: String)

  private val entries = HashMap<String, Entry>()
  private val endedStreams = LinkedHashMap<String, StreamEnd>()
  private val deadlines = Executors.newSingleThreadScheduledExecutor { task -> Thread(task, "portico-request-deadlines").apply { isDaemon = true } }

  private val client: OkHttpClient by lazy {
    OkHttpClient.Builder().porticoTrust()
        .followRedirects(false)
        .followSslRedirects(false)
        .cache(null)
        .callTimeout(15, TimeUnit.SECONDS)
        .connectTimeout(15, TimeUnit.SECONDS)
        .readTimeout(15, TimeUnit.SECONDS)
        .writeTimeout(15, TimeUnit.SECONDS)
        .dispatcher(Dispatcher().apply { maxRequests = 10; maxRequestsPerHost = 10 })
        .build()
  }

  // Reads retain safe connection alternatives. Writes may only be retried by their app owner.
  private val writeClient: OkHttpClient by lazy { client.newBuilder().retryOnConnectionFailure(false)
      .dispatcher(Dispatcher().apply { maxRequests = 2; maxRequestsPerHost = 2 }).build() }
  // Same pool, dispatcher, TLS policy and deadline; a from-start source is never replayed.
  private val streamClient: OkHttpClient by lazy { client.newBuilder().retryOnConnectionFailure(false).build() }

  /** A request URL the transport will carry: http or https, no credentials in it, no fragment. */
  fun target(url: String?): HttpUrl? {
    if (url == null || url.length > 8192) return null
    val parsed = url.toHttpUrlOrNull() ?: return null
    if (parsed.username.isNotEmpty() || parsed.password.isNotEmpty() || parsed.fragment != null) return null
    return parsed
  }

  /** Builds the wire request, or null when a header or the method cannot be sent as given. */
  fun request(url: HttpUrl, method: String, headers: ReadableMap?, body: ByteArray?): Request? {
    val bytes = when (method) { "GET", "HEAD" -> null; else -> body ?: ByteArray(0) }
    val payload = bytes?.let { data ->
      object : RequestBody() {
        override fun contentType(): MediaType? = null
        override fun contentLength(): Long = data.size.toLong()
        override fun writeTo(sink: BufferedSink) { sink.write(data) }
      }
    }
    return wireRequest(url, method, headers, payload)
  }

  /** Already validated private files; OkHttp reads them on its worker, with exact size bounds. */
  fun uploadRequest(url: HttpUrl, method: String, headers: ReadableMap?, metadata: String, files: List<Pair<File, String>>): Request? {
    val sizes = files.map { it.first.length() }
    if (sizes.any { it <= 0 || it > 33554432L } || sizes.sum() > 33554432L) return null
    val multipart = MultipartBody.Builder().setType(MultipartBody.FORM).addFormDataPart("metadata", metadata)
    for ((index, entry) in files.withIndex()) {
      val (file, name) = entry
      val size = sizes[index]
      val payload = object : RequestBody() {
        override fun contentType(): MediaType = "application/octet-stream".toMediaType()
        override fun contentLength(): Long = size
        override fun writeTo(sink: BufferedSink) {
          if (file.canonicalFile != file || !file.isFile || file.length() != size) throw IOException("Staged subtitle changed.")
          file.inputStream().use { input ->
            val buffer = ByteArray(65536)
            var remaining = size
            while (remaining > 0) {
              if (Thread.currentThread().isInterrupted) throw InterruptedIOException("Subtitle upload cancelled.")
              val count = input.read(buffer, 0, minOf(buffer.size.toLong(), remaining).toInt())
              if (count < 1) throw IOException("Staged subtitle changed.")
              sink.write(buffer, 0, count); remaining -= count
            }
            if (input.read() != -1) throw IOException("Staged subtitle changed.")
          }
        }
      }
      multipart.addFormDataPart(if (index == 0) "file" else "companion", name, payload)
    }
    val payload = multipart.build()
    return wireRequest(url, method, headers, payload)?.newBuilder()
        ?.tag(SubtitleUpload::class.java, SubtitleUpload)
        ?.removeHeader("Content-Length")?.header("Content-Type", payload.contentType().toString())?.build()
  }

  /** Shared headers and one-shot policy for JSON and multipart bodies. */
  private fun wireRequest(url: HttpUrl, method: String, headers: ReadableMap?, body: RequestBody?): Request? = try {
    val builder = Request.Builder().url(url)
    if (headers != null) {
      val keys = headers.keySetIterator()
      while (keys.hasNextKey()) {
        val name = keys.nextKey()
        if (headers.getType(name) == ReadableType.String) builder.header(name, headers.getString(name) ?: "")
      }
    }
    // One-shot prevents HTTP follow-ups (including retryable 503s) from replaying a write.
    val payload = body?.let { data ->
      object : RequestBody() {
        override fun contentType(): MediaType? = data.contentType()
        override fun contentLength(): Long = data.contentLength()
        override fun isOneShot(): Boolean = true
        override fun writeTo(sink: BufferedSink) { data.writeTo(sink) }
      }
    }
    builder.method(method, payload).build()
  } catch (_: IllegalArgumentException) { null }

  internal fun bulkMetadataPreview(request: Request) = request.method == "POST" &&
      request.url.encodedPath == "/v1/metadata/bulk/preview" && request.url.encodedQuery == null

  /** The selected production policy is also exercised by the JVM transport tests. */
  internal fun clientFor(request: Request, streaming: Boolean = false): OkHttpClient {
    val preview = bulkMetadataPreview(request)
    val base = if (streaming) streamClient else if (!preview && request.method !in setOf("GET", "HEAD")) writeClient else client
    // A paged owner read may span millions of candidates. Keep connect/write
    // bounds and caller cancellation, without a whole-scan/read-idle timer.
    if (preview) return base.newBuilder().callTimeout(0, TimeUnit.MILLISECONDS).readTimeout(0, TimeUnit.MILLISECONDS).build()
    // Derive from the trusted client, retaining its pool, dispatcher and one-shot write policy.
    return if (request.tag(SubtitleUpload::class.java) != null) base.newBuilder()
        .callTimeout(110, TimeUnit.SECONDS)
        .readTimeout(60, TimeUnit.SECONDS)
        .writeTimeout(60, TimeUnit.SECONDS)
        .build() else base
  }

  /** Admits a request: start it, queue it, or refuse it when 76 are already pending. */
  fun submit(owner: Any, identifier: String, request: Request, limit: Long, domain: String, busy: String, promise: Promise, binary: Boolean = false, streaming: Boolean = false) {
    val critical = request.method !in setOf("GET", "HEAD") && !bulkMetadataPreview(request)
    val entry = Entry(identifier, owner, limit, promise, critical, binary, streaming)
    val call: Call
    synchronized(entries) {
      if (entries.containsKey(identifier) || endedStreams.containsKey(identifier) || entries.size >= MAX_ACTIVE + MAX_WAITING || (!critical && entries.values.count { !it.critical } >= 66) ||
          streaming && entries.values.any { it.streaming && it.owner === owner }) {
        promise.reject(domain, busy)
        return
      }
      entries[identifier] = entry
      val requestClient = clientFor(request, streaming)
      call = requestClient.newCall(request)
      entry.call = call
      if (requestClient.callTimeoutMillis > 0) entry.deadline = deadlines.schedule({ finish(entry, "The request took too long.", null, TIMEOUT) }, requestClient.callTimeoutMillis.toLong(), TimeUnit.MILLISECONDS)
    }
    call.enqueue(object : Callback {
      override fun onFailure(call: Call, e: IOException) =
          if (PorticoServerTrust.refusedCertificate(e)) finish(entry, "The server's certificate is not trusted yet.", null, CERTIFICATE)
          else when (failureCode(e)) {
            TIMEOUT -> finish(entry, "The request took too long.", null, TIMEOUT)
            UNREACHABLE -> finish(entry, "The server could not be reached.", null, UNREACHABLE)
            else -> finish(entry, "The queue request could not be confirmed.", null)
          }

      override fun onResponse(call: Call, response: Response) {
        if (entry.streaming) openStream(entry, response) else response.use { read(entry, it) }
      }
    })
  }

  /** Retain the SAME response after headers; the body is touched only by a pull. */
  private fun openStream(entry: Entry, response: Response) {
    if (response.code in intArrayOf(301, 302, 303, 307, 308) && response.header("Location") != null) {
      response.close(); finish(entry, "Queue control redirects are not allowed.", null); return
    }
    val headers = Arguments.createMap()
    for (name in response.headers.names()) headers.putString(name, response.headers.values(name).joinToString(", "))
    val result = Arguments.createMap().apply { putInt("status", response.code); putMap("headers", headers) }
    synchronized(entry) {
      if (entry.completed) {response.close(); return}
      entry.response = response
      entry.promise?.resolve(result)
      entry.promise = null
    }
  }

  /** One pending bounded read of the original HTTP response, never a second request. */
  fun readStream(owner: Any, identifier: String, promise: Promise) {
    val entry: Entry?
    val terminal: StreamEnd?
    synchronized(entries) {entry = entries[identifier]; terminal = endedStreams[identifier]}
    if (entry == null) {
      if (terminal != null && terminal.owner === owner) {
        if (terminal.failure == null) promise.resolve(streamResult(true, ByteArray(0))) else promise.reject(terminal.code, terminal.failure)
      } else promise.reject(CANCELLED, "Reading stream cancelled.")
      return
    }
    synchronized(entry) {
      if (entry.owner !== owner || !entry.streaming || entry.completed || entry.response == null) {promise.reject(CANCELLED, "Reading stream cancelled."); return}
      if (entry.pull != null) {promise.reject("queue_transport", "A Reading pull is already pending."); return}
      entry.pull = promise
    }
    client.dispatcher.executorService.execute {
      try {
        val response = synchronized(entry) {if (entry.completed) null else entry.response}
        if (response == null) return@execute
        val buffer = Buffer()
        val count = response.body?.source()?.read(buffer, 49152) ?: -1L
        if (count == -1L) {finish(entry, null, streamResult(true, ByteArray(0))); return@execute}
        if (count < 1 || count > 49152 || buffer.size != count) {finish(entry, "Invalid Reading stream response.", null); return@execute}
        val result = streamResult(false, buffer.readByteArray())
        synchronized(entry) {if (!entry.completed) {entry.pull?.resolve(result); entry.pull = null}}
      } catch (error: IOException) {
        val code = if (PorticoServerTrust.refusedCertificate(error)) CERTIFICATE else failureCode(error) ?: "queue_transport"
        finish(entry, "The Reading stream could not be confirmed.", null, code)
      } catch (_: RuntimeException) {finish(entry, "The Reading stream could not be confirmed.", null)}
    }
  }

  private fun streamResult(done: Boolean, bytes: ByteArray) = Arguments.createMap().apply {
    putBoolean("done", done); putString("bodyBase64", Base64.encodeToString(bytes, Base64.NO_WRAP))
  }

  private fun read(entry: Entry, response: Response) {
    // A redirect is never followed: the next request would carry the caller's credentials to an
    // address the caller did not name.
    if (response.code in intArrayOf(301, 302, 303, 307, 308) && response.header("Location") != null) {
      finish(entry, "Queue control redirects are not allowed.", null)
      return
    }
    val bytes: ByteArray
    try {
      val body = response.body
      val buffer = Buffer()
      if (body != null && response.request.method != "HEAD") {
        if (body.contentLength() > entry.limit) {
          finish(entry, "Queue response exceeded its limit.", null)
          return
        }
        val source = body.source()
        while (source.read(buffer, 16384) != -1L) {
          if (buffer.size > entry.limit) {
            finish(entry, "Queue response exceeded its limit.", null)
            return
          }
        }
      }
      bytes = buffer.readByteArray()
    } catch (_: IOException) {
      finish(entry, "The queue request could not be confirmed.", null)
      return
    }
    val text = if (entry.binary) Base64.encodeToString(bytes, Base64.NO_WRAP) else try {
      Charsets.UTF_8.newDecoder()
          .onMalformedInput(CodingErrorAction.REPORT)
          .onUnmappableCharacter(CodingErrorAction.REPORT)
          .decode(ByteBuffer.wrap(bytes))
          .toString()
    } catch (_: CharacterCodingException) {
      finish(entry, "Invalid queue response.", null)
      return
    }
    val headers = Arguments.createMap()
    for (name in response.headers.names()) headers.putString(name, response.headers.values(name).joinToString(", "))
    val result = Arguments.createMap()
    result.putInt("status", response.code)
    result.putMap("headers", headers)
    result.putString(if (entry.binary) "bodyBase64" else "body", text)
    finish(entry, null, result)
  }

  /** The failure code for a refused certificate: the app can then ask the server to prove itself over it. */
  const val CERTIFICATE = "certificate_untrusted"
  /** No answer in time: the app says the server took too long. A write's outcome is unknown. */
  const val TIMEOUT = "timeout"
  /** Nothing reached the server (refused, no route, unknown name): the app says it can't be reached. */
  const val UNREACHABLE = "network"
  /** The app abandoned the request. */
  const val CANCELLED = "cancelled"

  /**
   * What a transport failure means to the person, by its kind. Only failures before anything
   * was sent count as unreachable; anything else keeps the transport's own code.
   */
  fun failureCode(e: IOException): String? = when (e) {
    is SocketTimeoutException -> TIMEOUT
    is InterruptedIOException -> if (e.message == "timeout") TIMEOUT else null
    is ConnectException, is NoRouteToHostException, is UnknownHostException, is PortUnreachableException -> UNREACHABLE
    else -> null
  }

  private fun finish(entry: Entry, failure: String?, result: Any?, code: String = "queue_transport") {
    val promise: Promise?
    val pull: Promise?
    val response: Response?
    synchronized(entry) {
      if (entry.completed) return
      entry.completed = true
      entry.deadline?.cancel(false)
      entry.deadline = null
      promise = entry.promise
      entry.promise = null
      pull = entry.pull
      entry.pull = null
      response = entry.response
      entry.response = null
    }
    synchronized(entries) {
      if (entries[entry.identifier] === entry) entries.remove(entry.identifier)
      if (entry.streaming) {
        endedStreams[entry.identifier] = StreamEnd(entry.owner, failure, code)
        while (endedStreams.size > MAX_ACTIVE + MAX_WAITING) endedStreams.remove(endedStreams.keys.first())
      }
    }
    if (failure != null) {
      entry.call?.cancel()
      promise?.reject(code, failure)
      pull?.reject(code, failure)
    } else {
      promise?.resolve(result)
      pull?.resolve(result)
    }
    try {response?.close()} catch (_: RuntimeException) { /* already terminal; all pending promises settled */ }
  }

  fun cancel(identifier: String) {
    val entry = synchronized(entries) { entries[identifier].also {if (it == null) endedStreams.remove(identifier)} } ?: return
    finish(entry, "Queue request cancelled.", null, CANCELLED)
  }

  /**
   * A JavaScript reload abandons its promises; release their slots at once instead of leaving
   * them to time out while the new JavaScript's first requests wait behind them.
   */
  fun cancelOwnedBy(owner: Any) {
    val owned = synchronized(entries) { entries.values.filter { it.owner === owner } }
    for (entry in owned) finish(entry, "The app reloaded.", null)
    synchronized(entries) {endedStreams.entries.removeAll {it.value.owner === owner}}
  }
}
