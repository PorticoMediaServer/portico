package tv.getportico.portico.native

import android.net.Uri
import android.util.Base64
import com.facebook.react.bridge.Arguments
import com.facebook.react.bridge.Promise
import com.facebook.react.bridge.ReactApplicationContext
import com.facebook.react.bridge.ReactMethod
import com.facebook.react.bridge.ReadableMap
import java.io.File
import java.io.FileOutputStream
import java.util.UUID

/**
 * `NativeModules.PorticoDownloads`: the methods and the event of the Apple module
 * (`PorticoDownloadChanged`). A download plays in Portico's own player (`localFile`). Phones and tablets only; a television has
 * no such module, and the app hides downloads there.
 */
class PorticoDownloads(context: ReactApplicationContext) : PorticoEventModule(context) {
  private val publications = PorticoPublicationReads()
  private val publicationState = PorticoDownloadCenter.PublicationStateRegistry()
  /** A cover-page temp file this very module created, filled in bounded chunks and then finished. */
  private class CoverStage(val file: File, val mime: String, val generation: Long) {
    var size = 0L
    var finished = false
    var busy = false
  }
  /** Opaque handle -> its private cache file. */
  private val coverImages = mutableMapOf<String, CoverStage>()
  private val coverLock = Any()
  private var coverRetired = false
  private var coverGeneration = 0L
  private val coverBase64 = Regex("^[A-Za-z0-9+/]*={0,2}$")
  private val maxCoverBytes = 10L * 1024 * 1024
  /** One chunk's bound (64 KiB of bytes); the app sends 24 KiB. */
  private val maxCoverChunkEncoded = ((64 * 1024 + 2) / 3) * 4
  @Volatile private var invalidated = false
  private val changed: (Map<String, Any?>) -> Unit = { entry ->
    if (!invalidated) emit("PorticoDownloadChanged", Arguments.makeNativeMap(entry))
  }

  override fun getName(): String = "PorticoDownloads"

  @ReactMethod fun addListener(eventName: String) {}

  @ReactMethod fun removeListeners(count: Double) {}

  override fun initialize() {
    super.initialize()
    PorticoDownloadCenter.post {
      if (invalidated) return@post
      PorticoDownloadCenter.open(reactApplicationContext)
      PorticoDownloadCenter.onChanged = changed
    }
  }

  override fun invalidate() {
    invalidated = true
    publications.invalidate()
    val retired = synchronized(coverLock) {
      coverRetired = true; coverGeneration++
      val files = coverImages.values.map { it.file }; coverImages.clear(); files
    }
    retired.forEach { it.delete() }
    // Transfers belong to the process and go on; only the listener goes.
    PorticoDownloadCenter.post {
      PorticoDownloadCenter.publicationStateInvalidate(publicationState)
      if (PorticoDownloadCenter.onChanged === changed) PorticoDownloadCenter.onChanged = null
    }
    super.invalidate()
  }

  @ReactMethod
  fun list(promise: Promise) = PorticoDownloadCenter.post {
    try { check(PorticoDownloadCenter.open(reactApplicationContext)); promise.resolve(Arguments.makeNativeArray(PorticoDownloadCenter.list())) }
    catch (_: Exception) { promise.reject("read_failed", "Downloads could not be read.") }
  }

  @ReactMethod
  fun start(request: ReadableMap?, promise: Promise) {
    val value = request?.toHashMap()
    PorticoDownloadCenter.post {
      val entry = value?.let { PorticoDownloadCenter.start(it) }
      if (entry != null) promise.resolve(Arguments.makeNativeMap(entry)) else promise.reject("invalid_download", "The download request is invalid.")
    }
  }

  @ReactMethod
  fun resume(request: ReadableMap?, promise: Promise) {
    val value = request?.toHashMap()
    PorticoDownloadCenter.post {
      val entry = value?.let { PorticoDownloadCenter.resume(it) }
      if (entry != null) promise.resolve(Arguments.makeNativeMap(entry))
      else promise.reject("generation_changed", "This download changed before it could be continued.")
    }
  }

  @ReactMethod
  fun pause(identifier: String?, promise: Promise) = PorticoDownloadCenter.post {
    when (val error = PorticoDownloadCenter.pause(identifier)) {
      null -> promise.resolve(null)
      "write_failed" -> promise.reject(error, "This download could not be paused because its state could not be saved.")
      else -> promise.reject(error, "The download could not be paused.")
    }
  }

  @ReactMethod
  fun remove(identifier: String?, promise: Promise) = PorticoDownloadCenter.post {
    when (val error = PorticoDownloadCenter.remove(identifier)) {
      null -> promise.resolve(null)
      "write_failed" -> promise.reject(error, "This download could not be removed because its state could not be saved.")
      else -> promise.reject(error, "The download could not be removed.")
    }
  }

  @ReactMethod
  fun removeGeneration(identifier: String?, generation: String?, promise: Promise) = PorticoDownloadCenter.post {
    when (PorticoDownloadCenter.removeGeneration(identifier, generation)) {
      null -> promise.resolve(null)
      "generation_changed" -> promise.reject("generation_changed", "This download changed before it could be removed.")
      "write_failed" -> promise.reject("write_failed", "This download could not be removed because its state could not be saved.")
      else -> promise.reject("invalid_download", "The download is invalid.")
    }
  }

  @ReactMethod
  fun setMeta(identifier: String?, meta: String?, promise: Promise) = PorticoDownloadCenter.post {
    when (PorticoDownloadCenter.setMeta(identifier, meta)) {
      null -> promise.resolve(null)
      "not_found" -> promise.reject("not_found", "This download no longer exists.")
      "too_large" -> promise.reject("too_large", "This download's details are too large to keep.")
      else -> promise.reject("write_failed", "This download's details could not be saved.")
    }
  }

  @ReactMethod
  fun setMetas(metas: ReadableMap?, promise: Promise) {
    val value = metas?.toHashMap()
    PorticoDownloadCenter.post {
      when (if (value == null) "too_large" else PorticoDownloadCenter.setMetas(value)) {
        null -> promise.resolve(null)
        "too_large" -> promise.reject("too_large", "This download's details are too large to keep.")
        else -> promise.reject("write_failed", "This download's details could not be saved.")
      }
    }
  }

  @ReactMethod
  fun publicationStateOpen(handle: String?, identifier: String?, generation: String?, expected: String?, promise: Promise) = PorticoDownloadCenter.post {
    if (!invalidated && PorticoDownloadCenter.publicationStateOpen(publicationState, handle, identifier, generation, expected)) promise.resolve(null)
    else promise.reject("reading_retired", "reading_retired")
  }
  @ReactMethod
  fun publicationStateRead(handle: String, name: String, promise: Promise) = PorticoDownloadCenter.post {
    try { check(!invalidated); promise.resolve(PorticoDownloadCenter.publicationStateRead(publicationState, handle, name)) }
    catch (_: Exception) { promise.reject("reading_retired", "reading_retired") }
  }
  @ReactMethod
  fun publicationStateWrite(handle: String, name: String, json: String, promise: Promise) = PorticoDownloadCenter.post {
    try { check(!invalidated); PorticoDownloadCenter.publicationStateWrite(publicationState, handle, name, json); promise.resolve(null) }
    catch (_: Exception) { promise.reject("reading_state_write_failed", "reading_state_write_failed") }
  }
  @ReactMethod
  fun publicationStateClose(handle: String?) = PorticoDownloadCenter.post { PorticoDownloadCenter.publicationStateClose(publicationState, handle) }

  @ReactMethod
  fun writeExtra(identifier: String?, name: String?, text: String?, promise: Promise) = PorticoDownloadCenter.post {
    if (PorticoDownloadCenter.writeExtra(identifier, name, text)) promise.resolve(null) else promise.reject("write_failed", "This download's details could not be saved.")
  }

  @ReactMethod
  fun readExtra(identifier: String?, name: String?, promise: Promise) = PorticoDownloadCenter.post { promise.resolve(PorticoDownloadCenter.readExtra(identifier, name)) }

  @ReactMethod
  fun fetchExtra(identifier: String?, name: String?, url: String?, promise: Promise) = PorticoDownloadCenter.post {
    PorticoDownloadCenter.fetchExtra(identifier, name, url) { outcome -> promise.resolve(outcome) }
  }

  @ReactMethod
  fun storage(promise: Promise) = PorticoDownloadCenter.post {
    PorticoDownloadCenter.storage { result ->
      // A result belongs to the module that requested it, even if a new bridge opened meanwhile.
      if (invalidated) promise.reject("cancelled", "cancelled")
      else result.fold(
          onSuccess = { promise.resolve(Arguments.makeNativeMap(it)) },
          onFailure = { promise.reject("storage_unavailable", "Download storage could not be measured.") })
    }
  }

  @ReactMethod
  fun setCellular(allowed: Boolean, promise: Promise) = PorticoDownloadCenter.post { PorticoDownloadCenter.setCellular(allowed); promise.resolve(null) }

  @ReactMethod
  fun readState(promise: Promise) = PorticoDownloadCenter.post {
    try { check(PorticoDownloadCenter.open(reactApplicationContext)); promise.resolve(PorticoDownloadCenter.readState()) }
    catch (_: Exception) { promise.reject("read_failed", "Download settings could not be read.") }
  }

  @ReactMethod
  fun writeState(json: String?, promise: Promise) = PorticoDownloadCenter.post {
    if (json == null || json.length >= 1048576) promise.reject("too_large", "Download settings are too large to keep.")
    else if (PorticoDownloadCenter.writeState(json)) promise.resolve(null)
    else promise.reject("write_failed", "Download settings could not be saved.")
  }

  private fun fileOr(identifier: String?, promise: Promise): File? = when (val found = PorticoDownloadCenter.file(identifier)) {
    is File -> found
    "missing_file" -> { promise.reject("missing_file", "This download’s file is missing. Remove it and download it again."); null }
    else -> { promise.reject("not_ready", "This download is not ready to play."); null }
  }

  /** The file of a finished download, for Portico's own player (it accepts a file only inside the downloads folder). */
  @ReactMethod
  fun localFile(identifier: String?, promise: Promise) = PorticoDownloadCenter.post {
    val file = fileOr(identifier, promise) ?: return@post
    val result = Arguments.createMap()
    result.putString("url", Uri.fromFile(file).toString())
    result.putDouble("durationSeconds", PorticoDownloadCenter.durationSeconds(file))
    promise.resolve(result)
  }
  @ReactMethod fun publicationOpen(handle: String, identifier: String, owner: String, digest: String, promise: Promise) = PorticoDownloadCenter.post {
    val found = if (!invalidated) PorticoDownloadCenter.publication(identifier, owner, digest) else null
    if (found == null) promise.reject("reading_unavailable", "reading_unavailable")
    else publications.open(handle, found.first, found.first.length(), digest, found.second, promise)
  }
  @ReactMethod fun publicationRead(handle: String, operation: Double, offset: Double, length: Double, promise: Promise) = publications.range(handle, operation, offset, length, promise)
  @ReactMethod fun publicationMember(handle: String, operation: Double, offset: Double, compressed: Double, size: Double, method: Double, digest: String, promise: Promise) = publications.member(handle, operation, offset, compressed, size, method, digest, promise)
  @ReactMethod fun publicationMemberRead(handle: String, operation: Double, offset: Double, length: Double, promise: Promise) = publications.memberRange(handle, operation, offset, length, promise)
  @ReactMethod fun publicationCancel(handle: String, operation: Double) {publications.cancel(handle, operation)}
  @ReactMethod fun publicationClose(handle: String) {publications.close(handle)}

  private fun coverDirectory(): File {
    val directory = File(reactApplicationContext.cacheDir, "portico-cover-uploads")
    if (!directory.exists() && !directory.mkdirs()) throw java.io.IOException("cover_directory")
    return directory.canonicalFile
  }
  private fun coverMime(data: ByteArray): String? {
    fun at(index: Int, value: Int) = index < data.size && (data[index].toInt() and 0xFF) == value
    return when {
      at(0, 0xFF) && at(1, 0xD8) && at(2, 0xFF) -> "image/jpeg"
      at(0, 0x89) && at(1, 0x50) && at(2, 0x4E) && at(3, 0x47) && at(4, 0x0D) && at(5, 0x0A) && at(6, 0x1A) && at(7, 0x0A) -> "image/png"
      at(0, 0x52) && at(1, 0x49) && at(2, 0x46) && at(3, 0x46) && at(8, 0x57) && at(9, 0x45) && at(10, 0x42) && at(11, 0x50) -> "image/webp"
      else -> null
    }
  }
  private fun coverExtension(mime: String) = when (mime) { "image/jpeg" -> ".jpg"; "image/png" -> ".png"; else -> ".webp" }

  /**
   * One bounded image-only private temp file, staged in chunks so the app never holds the whole
   * image as one string (IM01). The caller sends only bytes; the filename, directory and handle are
   * generated here. A stage that loses its module generation never finishes and is deleted.
   */
  @ReactMethod
  fun beginCoverImage(mime: String?, promise: Promise) {
    val type = mime?.takeIf { it == "image/jpeg" || it == "image/png" || it == "image/webp" }
        ?: run { promise.reject("unsupported_image", "unsupported_image"); return }
    var file: File? = null
    try {
      val target = File(coverDirectory(), UUID.randomUUID().toString() + coverExtension(type))
      file = target
      if (!target.createNewFile()) throw java.io.IOException("cover_write")
      val handle = UUID.randomUUID().toString()
      val registered = synchronized(coverLock) {
        if (coverRetired) false else { coverImages[handle] = CoverStage(target, type, coverGeneration); true }
      }
      if (!registered) { target.delete(); promise.reject("cancelled", "cancelled"); return }
      promise.resolve(Arguments.createMap().apply { putString("handle", handle) })
    } catch (_: Exception) {
      file?.delete()
      promise.reject("write_failed", "write_failed")
    }
  }

  /** The stage, held for one call; null when unknown, finished, busy or from a retired generation. */
  private fun claimCover(handle: String?): CoverStage? = synchronized(coverLock) {
    handle?.let { coverImages[it] }?.takeIf { !it.finished && !it.busy && !coverRetired && it.generation == coverGeneration }?.also { it.busy = true }
  }

  /** Appends one chunk; answers the bytes staged so far. */
  @ReactMethod
  fun appendCoverImage(handle: String?, base64: String?, promise: Promise) {
    val encoded = base64 ?: run { promise.reject("invalid_image", "invalid_image"); return }
    // Bound the bridge string before any decode allocation; strict canonical base64, so no
    // whitespace, ignored character or malformed tail is accepted.
    if (encoded.isEmpty() || encoded.length > maxCoverChunkEncoded || encoded.length % 4 != 0 || !coverBase64.matches(encoded)) {
      promise.reject("invalid_image", "invalid_image"); return
    }
    val data = try { Base64.decode(encoded, Base64.NO_WRAP) } catch (_: IllegalArgumentException) { promise.reject("invalid_image", "invalid_image"); return }
    // Canonical equality also rejects non-zero unused tail bits in the last sextet.
    if (data.isEmpty() || Base64.encodeToString(data, Base64.NO_WRAP) != encoded) { promise.reject("invalid_image", "invalid_image"); return }
    val stage = claimCover(handle) ?: run { promise.reject("invalid_handle", "invalid_handle"); return }
    try {
      if (stage.size + data.size > maxCoverBytes) { promise.reject("invalid_image", "invalid_image"); return }
      FileOutputStream(stage.file, true).use { it.write(data) }
      stage.size += data.size
      promise.resolve(stage.size.toDouble())
    } catch (_: Exception) {
      promise.reject("write_failed", "write_failed")
    } finally {
      synchronized(coverLock) { stage.busy = false }
    }
  }

  /** The staged file is exactly [size] bytes of the declared image type: it becomes the lease. */
  @ReactMethod
  fun finishCoverImage(handle: String?, size: Double, promise: Promise) {
    val stage = claimCover(handle) ?: run { promise.reject("invalid_handle", "invalid_handle"); return }
    try {
      if (size < 1 || size != Math.floor(size) || size.toLong() != stage.size || stage.file.length() != stage.size) {
        promise.reject("invalid_image", "invalid_image"); return
      }
      val head = ByteArray(12)
      val read = java.io.FileInputStream(stage.file).use { input -> var total = 0; while (total < head.size) { val n = input.read(head, total, head.size - total); if (n < 0) break; total += n }; total }
      if (coverMime(head.copyOf(read)) != stage.mime) { promise.reject("unsupported_image", "unsupported_image"); return }
      FileOutputStream(stage.file, true).use { it.fd.sync() }
      val finished = synchronized(coverLock) {
        if (coverRetired || stage.generation != coverGeneration) false else { stage.finished = true; true }
      }
      if (!finished) { stage.file.delete(); promise.reject("cancelled", "cancelled"); return }
      val result = Arguments.createMap()
      result.putString("handle", handle)
      result.putString("uri", Uri.fromFile(stage.file).toString())
      result.putDouble("size", stage.size.toDouble())
      promise.resolve(result)
    } catch (_: Exception) {
      promise.reject("write_failed", "write_failed")
    } finally {
      synchronized(coverLock) { stage.busy = false }
    }
  }
  /** Idempotent. A handle is removed only after its private file is gone; a
   * failed delete keeps the exact bookkeeping so teardown can retry. */
  @ReactMethod
  fun deleteCoverImage(handle: String?, promise: Promise) {
    val file = handle?.let { synchronized(coverLock) { coverImages[it]?.file } }
    if (file == null) { promise.resolve(null); return }
    val directory = reactApplicationContext.cacheDir.resolve("portico-cover-uploads").canonicalFile
    if (file.canonicalFile.parentFile != directory) {
      synchronized(coverLock) { coverImages.remove(handle) }
      promise.reject("invalid_handle", "invalid_handle"); return
    }
    if (file.delete() || !file.exists()) {
      synchronized(coverLock) { coverImages.remove(handle) }
      promise.resolve(null)
    } else promise.reject("delete_failed", "delete_failed")
  }
}

