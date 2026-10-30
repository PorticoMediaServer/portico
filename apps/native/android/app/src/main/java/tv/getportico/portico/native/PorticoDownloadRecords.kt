package tv.getportico.portico.native

import android.system.ErrnoException
import android.system.Os
import android.system.OsConstants
import java.io.File
import java.io.FileOutputStream
import java.nio.ByteBuffer
import java.nio.charset.CodingErrorAction

/** An absent record is empty; an unreadable or invalid record is never writable. */
internal class PorticoDownloadRecord(
    private val file: File,
    private val load: (File) -> ByteArray? = ::readDownloadRecord,
    private val store: (File, String) -> Boolean = ::writeDownloadRecord,
) {
  private var readable = false

  fun <T> read(decode: (String?) -> T): T {
    readable = false
    val bytes = load(file)
    val text = bytes?.let {
      Charsets.UTF_8.newDecoder().onMalformedInput(CodingErrorAction.REPORT)
          .onUnmappableCharacter(CodingErrorAction.REPORT).decode(ByteBuffer.wrap(it)).toString()
    }
    val value = decode(text)
    readable = true
    return value
  }

  fun write(text: String): Boolean = readable && store(file, text)
}

internal fun downloadPathExists(file: File): Boolean = try {
  Os.lstat(file.path)
  true
} catch (error: ErrnoException) {
  if (error.errno == OsConstants.ENOENT) false else throw error
}

private fun readDownloadRecord(file: File): ByteArray? {
  val stat = try { Os.lstat(file.path) } catch (error: ErrnoException) {
    if (error.errno == OsConstants.ENOENT) return null else throw error
  }
  check(OsConstants.S_ISREG(stat.st_mode))
  return file.readBytes()
}

private fun writeDownloadRecord(file: File, text: String): Boolean = try {
  val holding = File(file.path + ".tmp")
  FileOutputStream(holding).use { it.write(text.toByteArray(Charsets.UTF_8)); it.fd.sync() }
  holding.renameTo(file)
} catch (_: Exception) { false }

/** Validate every member before publishing any of a manifest. A bad row cannot be dropped
 * and then erased by the next save of the remaining rows. */
internal fun decodeDownloadManifest(value: Any?): HashMap<String, MutableMap<String, Any?>> {
  val stored = value as? Map<*, *> ?: error("read_failed")
  val result = HashMap<String, MutableMap<String, Any?>>()
  for ((key, raw) in stored) {
    check(key is String && key.matches(Regex("^[A-Za-z0-9_-]{1,160}$")))
    val entry = raw as? Map<*, *> ?: error("read_failed")
    check(entry.keys.all { it is String } && entry["id"] == key)
    check(entry["state"] in listOf("downloading", "paused", "failed", "ready", "removed"))
    check((entry["sha256"] as? String)?.lowercase()?.matches(Regex("^[a-f0-9]{64}$")) == true)
    val bytes = (entry["bytes"] as? Number)?.toDouble() ?: error("read_failed")
    check(bytes.isFinite() && bytes >= 1 && bytes <= 9007199254740991.0 && Math.floor(bytes) == bytes)
    @Suppress("UNCHECKED_CAST") val copy = HashMap(entry as Map<String, Any?>)
    if (copy["state"] == "downloading") { copy["state"] = "paused"; copy["error"] = ""; copy["waiting"] = "" }
    result[key] = copy
  }
  return result
}

/** A person can hold a restored/stopped run intent even when no transfer is active. */
internal fun pausedDownloadEntry(entry: Map<String, Any?>?, intent: String): MutableMap<String, Any?>? {
  if (entry == null || entry["state"] !in listOf("downloading", "paused", "failed")) return null
  return HashMap(entry).apply { put("state", "paused"); put("waiting", ""); put("error", ""); put("intent", intent) }
}

/** One row's change is accepted only after its manifest is durable. */
internal fun commitDownloadEntry(
    entries: MutableMap<String, MutableMap<String, Any?>>,
    id: String,
    next: MutableMap<String, Any?>,
    save: () -> Boolean,
): Boolean {
  val previous = entries.put(id, next)
  if (save()) return true
  if (previous == null) entries.remove(id) else entries[id] = previous
  return false
}
