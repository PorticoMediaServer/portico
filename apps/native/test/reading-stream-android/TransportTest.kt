package tv.getportico.portico.native

import com.facebook.react.bridge.BridgeMap
import com.facebook.react.bridge.HeaderMap
import com.facebook.react.bridge.Promise
import java.io.IOException
import java.net.InetAddress
import java.net.ServerSocket
import java.net.Socket
import java.security.MessageDigest
import java.util.concurrent.CompletableFuture
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger
import okhttp3.OkHttpClient

// The JVM test replaces Android/RN value conversion and TLS delegation only.
// Production PorticoTransport itself performs all real loopback HTTP and IO.
internal object PorticoServerTrust {fun refusedCertificate(error: IOException) = false}
internal fun OkHttpClient.Builder.porticoTrust(): OkHttpClient.Builder = this
private data class Failure(val code: String): RuntimeException(code)
private class Result: Promise {
  val future = CompletableFuture<Any?>()
  override fun resolve(value: Any?) {future.complete(value)}
  override fun reject(code: String, message: String?) {future.completeExceptionally(Failure(code))}
  fun value(): BridgeMap = future.get(18, TimeUnit.SECONDS) as BridgeMap
  fun failure(code: String) {
    try {value(); error("Expected transport failure: $code")} catch (error: java.util.concurrent.ExecutionException) {check((error.cause as? Failure)?.code == code)}
  }
}

private class Server: AutoCloseable {
  val data = ByteArray((2 shl 20) + 17) {(it % 251).toByte()}
  val headersPublished = CountDownLatch(1)
  val bodyAllowed = CountDownLatch(1)
  val requests = AtomicInteger()
  private val sockets = CopyOnWriteArrayList<Socket>()
  private val executor = Executors.newCachedThreadPool {task -> Thread(task, "reading-stream-fixture").apply {isDaemon = true}}
  private val listener = ServerSocket(0, 16, InetAddress.getByName("127.0.0.1"))
  val origin = "http://127.0.0.1:${listener.localPort}"
  init {executor.execute {
    while (!listener.isClosed) try {val socket = listener.accept(); sockets.add(socket); executor.execute {serve(socket)}} catch (_: IOException) {break}
  }}
  private fun serve(socket: Socket) {
    socket.use {try {
      socket.soTimeout = 20000
      val input = socket.getInputStream().bufferedReader(Charsets.ISO_8859_1)
      val line = input.readLine() ?: return
      val path = line.split(' ')[1]; var total = 0; val fields = mutableMapOf<String, String>()
      while (true) {val field = input.readLine() ?: return; if (field.isEmpty()) break; total += field.length; check(total <= 16384)
        fields[field.substringBefore(':').lowercase()] = field.substringAfter(':').trim()}
      check(fields["authorization"] == "Bearer throwaway-native-test"); check(fields["range"] == null)
      requests.incrementAndGet(); val output = socket.getOutputStream()
      if (path.contains("redirect")) {
        output.write("HTTP/1.1 302 Found\r\nLocation: $origin/never-follow\r\nContent-Length: 0\r\nConnection: close\r\n\r\n".toByteArray()); output.flush(); return
      }
      val chunked = path.contains("chunked")
      val lengthHeader = if (chunked) "Transfer-Encoding: chunked" else "Content-Length: ${data.size}"
      output.write("HTTP/1.1 200 OK\r\nContent-Type: application/pdf\r\n$lengthHeader\r\nAccept-Ranges: none\r\nConnection: close\r\n\r\n".toByteArray()); output.flush(); headersPublished.countDown()
      if (path.contains("held")) bodyAllowed.await(20, TimeUnit.SECONDS)
      val length = if (path.contains("short")) 17 else data.size
      var offset = 0
      while (offset < length) {val count = minOf(8192, length - offset)
        if (chunked) output.write("${count.toString(16)}\r\n".toByteArray())
        output.write(data, offset, count); offset += count
        if (chunked) output.write("\r\n".toByteArray())}
      if (chunked) output.write("0\r\n\r\n".toByteArray())
      output.flush()
    } catch (_: IOException) { /* the tested owner/caller may cancel the socket */ }
    catch (_: InterruptedException) {Thread.currentThread().interrupt()}}
  }
  override fun close() {bodyAllowed.countDown(); listener.close(); for (socket in sockets) socket.close(); executor.shutdownNow()}
}

private val owner = RuntimeSurface()
private val headers = HeaderMap(mapOf("Authorization" to "Bearer throwaway-native-test", "If-Match" to "\"${"a".repeat(64)}\""))
// A book's address as the server signs it: `b.` and 118 base64url characters (server-next reading.go sign). The test
// server reads its behaviour (held, short, chunked, redirect) from the word the grant starts with.
private fun grant(word: String) = "b." + word + "A".repeat(118 - word.length)
private fun open(server: Server, id: String, source: String = "normal"): Result {
  return Result().also {owner.networkStreamOpen(id, "${server.origin}/v1/media/${grant(source)}/source", headers, it)}
}
private fun pull(id: String) = Result().also {owner.networkStreamRead(id, it)}
private fun bytes(result: BridgeMap): ByteArray = java.util.Base64.getDecoder().decode(result.values["bodyBase64"] as String)
private fun done(result: BridgeMap) = result.values["done"] as Boolean

fun main() {
  var cases = 0
  try {
    Server().use {server ->
      val base = "${server.origin}/v1/media/${grant("source")}/source"
      for ((id, url) in listOf("" to base, "x".repeat(129) to base, "part" to base.replace("/source", "/parts/x.xhtml"), "page" to base.replace("/source", "/pages/0"),
          "old" to "${server.origin}/v1/reading/sessions/source/source",
          "query" to "$base?path=other", "fragment" to "$base#other", "credentials" to base.replace("http://", "http://user:password@"))) {
        Result().also {owner.networkStreamOpen(id, url, headers, it)}.failure("queue_transport")
      }
      Result().also {owner.networkStreamOpen("range", base, HeaderMap(mapOf("rAnGe" to "bytes=0-9")), it)}.failure("queue_transport")
      check(server.requests.get() == 0); cases++
    }
    // The byte gate: a book's exact source, page or part, and nothing wider.
    Server().use {server ->
      val book = "${server.origin}/v1/media/${grant("normal")}"
      fun ask(id: String, url: String, method: String = "GET", with: Map<String, String> = mapOf()) =
        // The fixture answers only the test's own authorization; a range is never sent to it (it is the stream fixture),
        // so a range is proven only where the gate refuses it, before any request.
        Result().also {owner.networkBytesRequest(id, url, method, HeaderMap(mapOf("Authorization" to "Bearer throwaway-native-test") + with), 33554432.0, it)}
      for ((id, call) in listOf("source" to {ask("ok-source", "$book/source")},
          "head" to {ask("ok-head", "$book/source", "HEAD")}, "page" to {ask("ok-page", "$book/pages/12?w=640")},
          "part" to {ask("ok-part", "$book/parts/OEBPS/chapter%201.xhtml")})) {call().value(); PorticoTransport.cancel("ok-$id")}
      val accepted = server.requests.get(); check(accepted == 4)
      val refused = listOf("$book/source?x=1", "$book/audio", "$book/audio-index", "$book/progress", "$book/%73ource",
        "$book/pages/-1", "$book/pages/01", "$book/pages/1?width=2", "$book/pages/1?w=x", "$book/pages/12345678901",
        "$book/parts", "$book/parts/", "$book/parts//x", "$book/parts/x/", "$book/parts/../x", "$book/parts/a/../x", "$book/parts/./x",
        "$book/parts/a%2Fb", "$book/parts/a%2fb", "$book/parts/%2E%2E/x", "$book/parts/%2e%2e", "$book/parts/a%252Fb", "$book/parts/%252E%252E/x",
        "$book/parts/a%5Cb", "$book/parts/a\\b", "$book/parts/a%00b", "$book/parts/a%0Ab", "$book/parts/a%7Fb", "$book/parts/a%ZZ", "$book/parts/a%-1",
        "$book/parts/x?y=1", "$book/parts/x#f", "${server.origin}/v1/reading/sessions/s/source", "${server.origin}/v1/media/b.short/source",
        "${server.origin}/v1/media/${grant("normal")}x/source", "${server.origin}/v1/media/c.${grant("normal").substring(2)}/source",
        "${server.origin}/v1/media/${grant("normal").replaceRange(10, 11, ".")}/source", "${server.origin}/v1/media/${grant("normal").replaceRange(10, 11, "%41")}/source",
        book.replace("http://", "http://user:password@") + "/source", "file:///v1/media/${grant("normal")}/source")
      for ((index, url) in refused.withIndex()) ask("bad-$index", url).failure("queue_transport")
      ask("head-part", "$book/parts/x.xhtml", "HEAD").failure("queue_transport"); ask("head-page", "$book/pages/0", "HEAD").failure("queue_transport")
      ask("head-range", "$book/source", "HEAD", mapOf("Range" to "bytes=0-1")).failure("queue_transport")
      ask("part-range", "$book/parts/x.xhtml", with = mapOf("rAnGe" to "bytes=0-1")).failure("queue_transport")
      for (method in listOf("POST", "PUT", "DELETE", "get")) ask("method-$method", "$book/source", method).failure("queue_transport")
      check(server.requests.get() == accepted); cases++
    }
    for (source in listOf("normal", "chunked")) Server().use {server ->
      val opened = open(server, "complete-$source", source).value(); check(opened.values["status"] == 200)
      val digest = MessageDigest.getInstance("SHA-256"); var count = 0
      while (true) {val next = pull("complete-$source").value(); val data = bytes(next)
        if (done(next)) {check(data.isEmpty()); break}
        check(data.isNotEmpty() && data.size <= 49152); count += data.size; digest.update(data)}
      check(count == server.data.size); check(digest.digest().contentEquals(MessageDigest.getInstance("SHA-256").digest(server.data))); check(server.requests.get() == 1)
      check(done(pull("complete-$source").value())); PorticoTransport.cancel("complete-$source"); cases++
    }
    Server().use {server ->
      open(server, "cancel", "held").value(); check(server.headersPublished.await(1, TimeUnit.SECONDS))
      open(server, "overlap").failure("queue_transport")
      Result().also {RuntimeSurface().networkStreamRead("cancel", it)}.failure("cancelled")
      val pending = pull("cancel"); pull("cancel").failure("queue_transport")
      PorticoTransport.cancel("cancel"); pending.failure("cancelled"); pull("cancel").failure("cancelled")
      open(server, "recovered").value(); PorticoTransport.cancel("recovered"); check(server.requests.get() == 2); cases++
    }
    Server().use {server ->
      open(server, "reload", "held").value(); val pending = pull("reload")
      PorticoTransport.cancelOwnedBy(owner); pending.failure("queue_transport"); pull("reload").failure("cancelled"); cases++
    }
    Server().use {server ->
      open(server, "short", "short").value(); var count = 0
      try {while (true) {val next = pull("short").value(); check(!done(next)); count += bytes(next).size}}
      catch (error: java.util.concurrent.ExecutionException) {check((error.cause as? Failure)?.code == "queue_transport")}
      check(count == 17); pull("short").failure("queue_transport"); PorticoTransport.cancel("short"); cases++
    }
    Server().use {server ->open(server, "redirect", "redirect").failure("queue_transport"); check(server.requests.get() == 1); PorticoTransport.cancel("redirect"); cases++}
    Server().use {server ->
      open(server, "deadline", "held").value(); val pending = pull("deadline"); pending.failure("timeout"); pull("deadline").failure("timeout"); PorticoTransport.cancel("deadline"); cases++
    }
    println("PASS Android production transport loopback: $cases cases; exact 2MiB+17B, headers/pull/busy/cancel/owner/error/redirect/deadline; TLS delegation stubbed, not device proof")
  } finally {
    PorticoTransport.cancelOwnedBy(owner)
    // Only this isolated JVM's own clients; no app/runtime or shared daemon.
    for (method in PorticoTransport.javaClass.declaredMethods.filter {it.parameterCount == 0 && it.returnType == OkHttpClient::class.java}) {
      method.isAccessible = true; val client = method.invoke(PorticoTransport) as OkHttpClient
      client.dispatcher.executorService.shutdownNow(); client.connectionPool.evictAll()
    }
  }
}
