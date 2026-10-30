package tv.getportico.portico.native

import android.system.Os
import android.system.OsConstants
import android.util.Base64
import com.facebook.react.bridge.Promise
import java.io.File
import java.io.FileInputStream
import java.nio.ByteBuffer
import java.nio.channels.FileChannel
import java.security.MessageDigest
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.Executors
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicLong
import java.util.zip.Inflater

/** Private download handles only: no filesystem URL, server URL or guest fetch.
 * Each extent comes from the receipt-bound descriptor; delivery is <=48KiB.
 * One member (<=32MiB) is expanded at a time, with cancellation between chunks. */
internal class PorticoPublicationReads {
  private class Ticket(val current: () -> Boolean) {
    val closed = AtomicBoolean(false)
    val busy = AtomicBoolean(false)
    val operation = AtomicLong(0)
    val cancelledThrough = AtomicLong(0)
    var channel: FileChannel? = null
    var bytes = 0L
    var member: ByteArray? = null
  }
  private val tickets = ConcurrentHashMap<String, Ticket>()
  private val retired = HashSet<String>()
  private val io = Executors.newSingleThreadExecutor()
  @Volatile private var closed = false
  private fun requireLive(t: Ticket) {check(!closed && !t.closed.get() && t.cancelledThrough.get() < t.operation.get())}
  private fun release(t: Ticket) {t.member = null; runCatching {t.channel?.close()}; t.channel = null}
  @Synchronized private fun enqueue(work: () -> Unit): Boolean {
    if (io.isShutdown) return false
    io.execute {work()}
    return true
  }
  @Synchronized private fun retire(handle: String, t: Ticket) {
    if (!tickets.remove(handle, t)) return
    if (retired.size >= 4096) closed = true
    retired.add(handle); t.closed.set(true)
    enqueue {release(t)}
  }
  @Synchronized fun close(handle: String) {
    if (retired.size >= 4096) closed = true
    retired.add(handle)
    tickets[handle]?.let {retire(handle, it)}
  }
  fun cancel(handle: String, operation: Double) {
    if (!operation.isFinite() || operation < 1 || operation > 9007199254740991.0 || operation != operation.toLong().toDouble()) return
    tickets[handle]?.cancelledThrough?.updateAndGet {maxOf(it, operation.toLong())}
  }
  @Synchronized fun invalidate() {
    closed = true
    tickets.entries.toList().forEach {retire(it.key, it.value)}
    // Exact-ticket closes are queued before shutdown; already queued work drains.
    io.shutdown()
  }
  private fun run(handle: String, operation: Double, promise: Promise, work: (Ticket) -> Any?) {
    val t = tickets[handle]
    if (t == null || closed || t.closed.get() || !operation.isFinite() || operation < 1 || operation > 9007199254740991.0 || operation != operation.toLong().toDouble() || operation < t.operation.get() || operation <= t.cancelledThrough.get() || !t.busy.compareAndSet(false, true)) {promise.reject("reading_unavailable", "reading_unavailable"); return}
    t.operation.set(operation.toLong())
    PorticoDownloadCenter.post {
      if (closed || !t.current() || t.closed.get()) {retire(handle, t); t.busy.set(false); promise.reject("reading_unavailable", "reading_unavailable"); return@post}
      val queued = enqueue {
        try {
          requireLive(t); val result = work(t); requireLive(t)
          PorticoDownloadCenter.post {
            t.busy.set(false)
            val ownerCurrent = t.current()
            if (tickets[handle] === t && !t.closed.get() && !closed && t.cancelledThrough.get() < operation.toLong() && ownerCurrent) promise.resolve(result)
            else {
              if (!ownerCurrent || closed || t.closed.get()) retire(handle, t)
              promise.reject("reading_retired", "reading_retired")
            }
          }
        } catch (_: Exception) {t.busy.set(false); if (t.cancelledThrough.get() < operation.toLong()) retire(handle, t); promise.reject("reading_unavailable", "reading_unavailable")}
      }
      if (!queued) {t.busy.set(false); promise.reject("reading_unavailable", "reading_unavailable")}
    }
  }
  @Synchronized fun open(handle: String, file: File, bytes: Long, digest: String, current: () -> Boolean, promise: Promise) {
    if (closed || retired.contains(handle) || tickets.size >= 2 || !handle.matches(Regex("[A-Za-z0-9_-]{8,128}")) || !digest.matches(Regex("[0-9a-f]{64}"))) {promise.reject("reading_unavailable", "reading_unavailable"); return}
    val t = Ticket(current)
    if (tickets.putIfAbsent(handle, t) != null) {promise.reject("reading_unavailable", "reading_unavailable"); return}
    run(handle, 1.0, promise) {
      val fd = Os.open(file.path, OsConstants.O_RDONLY or OsConstants.O_NOFOLLOW, 0)
      var input: FileInputStream? = null
      var transferred = false
      try {
        val stat = Os.fstat(fd)
        check(OsConstants.S_ISREG(stat.st_mode) && stat.st_size == bytes && bytes >= 1 && bytes <= 2147483648L)
        val stream = FileInputStream(fd); input = stream
        val channel = stream.channel; t.channel = channel; t.bytes = bytes; transferred = true
        val hash = MessageDigest.getInstance("SHA-256"); val buffer = ByteBuffer.allocate(65536)
        var seen = 0L
        while (seen < bytes) {
          requireLive(t); buffer.clear(); buffer.limit(minOf(buffer.capacity().toLong(), bytes - seen).toInt())
          val n = channel.read(buffer); check(n > 0)
          hash.update(buffer.array(), 0, n); seen += n
        }
        check(seen == bytes && channel.size() == bytes && hash.digest().joinToString("") {"%02x".format(it)} == digest)
        bytes.toDouble()
      } finally {
        // Until channel ownership transfers to this ticket, every failed fstat,
        // stream construction or channel acquisition still owns the raw fd here.
        if (!transferred) {
          val stream = input
          if (stream != null) runCatching {stream.close()} else runCatching {Os.close(fd)}
        }
      }
    }
  }
  private fun read(t: Ticket, offset: Long, count: Int): ByteArray {
    requireLive(t); val channel = t.channel ?: error("reading_unavailable")
    check(offset >= 0 && count >= 0 && channel.size() == t.bytes && offset <= t.bytes - count)
    val out = ByteArray(count); val buffer = ByteBuffer.wrap(out); var position = offset
    while (buffer.hasRemaining()) {requireLive(t); val n = channel.read(buffer, position); check(n > 0); position += n}
    return out
  }
  fun range(handle: String, operation: Double, offset: Double, length: Double, promise: Promise) = run(handle, operation, promise) {t ->
    check(offset.isFinite() && offset == offset.toLong().toDouble() && length.isFinite() && length == length.toInt().toDouble() && length >= 0 && length <= 49152)
    Base64.encodeToString(read(t, offset.toLong(), length.toInt()), Base64.NO_WRAP)
  }
  fun member(handle: String, operation: Double, offset: Double, compressed: Double, size: Double, method: Double, digest: String, promise: Promise) = run(handle, operation, promise) {t ->
    check(offset.isFinite() && offset == offset.toLong().toDouble() && compressed.isFinite() && compressed == compressed.toLong().toDouble() && compressed >= 0 && compressed <= 2147483648.0 && size.isFinite() && size == size.toInt().toDouble() && size >= 0 && size <= 33554432 && (method == 0.0 || method == 8.0) && digest.matches(Regex("[0-9a-f]{64}")))
    t.member = null
    val out = ByteArray(size.toInt()); var written = 0; var consumed = 0L
    val inflater = if (method == 8.0) Inflater(true) else null
    try {
      while (consumed < compressed.toLong()) {
        requireLive(t); val input = read(t, offset.toLong() + consumed, minOf(65536L, compressed.toLong() - consumed).toInt()); consumed += input.size
        if (inflater == null) {check(written <= out.size - input.size); input.copyInto(out, written); written += input.size}
        else {
          inflater.setInput(input)
          while (!inflater.needsInput() && !inflater.finished()) {
            requireLive(t)
            if (written == out.size) {val extra = ByteArray(1); check(inflater.inflate(extra) == 0 && inflater.finished()); break}
            val n = inflater.inflate(out, written, minOf(65536, out.size - written)); check(n > 0 || inflater.finished()); written += n
          }
          if (inflater.finished()) {check(consumed == compressed.toLong() && inflater.remaining == 0); break}
        }
      }
      if (inflater != null) while (!inflater.finished()) {
        requireLive(t)
        if (written == out.size) {val extra = ByteArray(1); check(inflater.inflate(extra) == 0 && inflater.finished()); break}
        val n = inflater.inflate(out, written, minOf(65536, out.size - written)); check(n > 0 || inflater.finished()); written += n
      }
      check(written == out.size && (inflater == null || inflater.finished() && inflater.remaining == 0 && inflater.bytesRead == compressed.toLong()))
      check(MessageDigest.getInstance("SHA-256").digest(out).joinToString("") {"%02x".format(it)} == digest)
      requireLive(t); t.member = out; out.size
    } finally {inflater?.end()}
  }
  fun memberRange(handle: String, operation: Double, offset: Double, length: Double, promise: Promise) = run(handle, operation, promise) {t ->
    val data = t.member ?: error("reading_unavailable")
    check(offset.isFinite() && offset == offset.toInt().toDouble() && length.isFinite() && length == length.toInt().toDouble() && offset >= 0 && length >= 0 && length <= 49152 && offset <= data.size - length)
    Base64.encodeToString(data.copyOfRange(offset.toInt(), offset.toInt() + length.toInt()), Base64.NO_WRAP)
  }
}
