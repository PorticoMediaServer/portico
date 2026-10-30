package tv.getportico.portico.native

import java.util.concurrent.Executor
import java.io.File
import java.io.FileOutputStream
import java.security.MessageDigest

/** Only the newest progress waits in the bookkeeping queue, at most one drain per transfer. */
internal class PorticoDownloadProgress(private val post: (() -> Unit) -> Unit, private val publish: (Long) -> Unit) {
  private var latest = 0L
  private var queued = false

  fun offer(bytes: Long) {
    val enqueue = synchronized(this) {
      latest = bytes
      if (queued) false else { queued = true; true }
    }
    if (enqueue) post {
      val reached = synchronized(this) { queued = false; latest }
      publish(reached)
    }
  }
}

/** File work for one download stays in order; unrelated downloads may run together. */
internal class PorticoDownloadWork(private val executor: Executor) {
  private val pending = HashMap<String, ArrayDeque<() -> Unit>>()

  fun execute(id: String, work: () -> Unit) {
    synchronized(pending) {
      val queue = pending.getOrPut(id) { ArrayDeque() }
      queue.addLast(work)
      if (queue.size == 1) dispatch(id, work)
    }
  }

  private fun dispatch(id: String, work: () -> Unit) {
    executor.execute {
      try { work() } finally {
        synchronized(pending) {
          val queue = checkNotNull(pending[id])
          queue.removeFirst()
          if (queue.isEmpty()) pending.remove(id) else dispatch(id, queue.first())
        }
      }
    }
  }
}

/** The bytes' identity stays beside them, independent of queued requests in the manifest. */
internal fun prepareDownloadPartial(directory: File, digest: String): Boolean = try {
  val part = File(directory, "partial.part")
  val identity = File(directory, "partial.sha256")
  val held = if (identity.isFile && identity.length() == 64L) identity.readText() else ""
  if (held != digest && part.exists() && !part.delete()) false
  else {
    val pending = File(directory, "partial.sha256.tmp")
    FileOutputStream(pending).use { it.write(digest.toByteArray(Charsets.US_ASCII)); it.fd.sync() }
    pending.renameTo(identity)
  }
} catch (_: Exception) { false }

/** A crash can leave the verified final file ahead of its ready manifest commit. It is
 * adopted only after checking both its complete size and digest on the file worker. */
internal fun verifiedDownloadFile(file: File, bytes: Long, digest: String, cancelled: () -> Boolean): Boolean =
    file.isFile && file.length() == bytes && downloadFileDigest(file, cancelled) == digest && !cancelled()

internal enum class DownloadStart { ADOPT, WAIT_FOR_WIFI, TRANSFER }

/** Only a transfer needs network admission. A verified local file can be adopted offline. */
internal fun downloadStart(verified: Boolean, cellularAllowed: Boolean, onCellular: () -> Boolean): DownloadStart =
    if (verified) DownloadStart.ADOPT
    else if (!cellularAllowed && onCellular()) DownloadStart.WAIT_FOR_WIFI
    else DownloadStart.TRANSFER

internal fun downloadFileDigest(file: File, cancelled: () -> Boolean): String = try {
  val digest = MessageDigest.getInstance("SHA-256")
  file.inputStream().use { input ->
    val buffer = ByteArray(1 shl 20)
    while (true) {
      if (cancelled()) return ""
      val read = input.read(buffer)
      if (read == -1) break
      digest.update(buffer, 0, read)
    }
  }
  val hex = "0123456789abcdef"
  StringBuilder(64).also { out -> for (byte in digest.digest()) { val value = byte.toInt() and 255; out.append(hex[value ushr 4]); out.append(hex[value and 15]) } }.toString()
} catch (_: Exception) { "" }
