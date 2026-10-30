package tv.getportico.portico.native

import android.app.Activity
import android.content.Context
import android.content.Intent
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.graphics.Canvas
import android.graphics.Color
import android.graphics.ImageDecoder
import android.graphics.Rect
import android.net.Uri
import android.os.Build
import android.os.Handler
import android.os.Looper
import android.provider.OpenableColumns
import android.util.Base64
import com.facebook.react.bridge.ActivityEventListener
import com.facebook.react.bridge.Arguments
import com.facebook.react.bridge.BaseActivityEventListener
import com.facebook.react.bridge.Promise
import com.facebook.react.bridge.ReactApplicationContext
import com.facebook.react.bridge.ReactContextBaseJavaModule
import com.facebook.react.bridge.ReactMethod
import java.io.File
import java.io.ByteArrayOutputStream
import java.nio.ByteBuffer
import java.nio.charset.CharacterCodingException
import java.nio.charset.CodingErrorAction
import java.util.UUID
import java.util.concurrent.Executors
import tv.getportico.portico.TelevisionDisplay

private val pickerWork = Executors.newSingleThreadExecutor { Thread(it, "portico-pickers") }

private fun displayName(context: Context, uri: Uri): String {
  try {
    context.contentResolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME), null, null, null)?.use { cursor ->
      if (cursor.moveToFirst()) cursor.getString(0)?.let { return it }
    }
  } catch (_: Exception) {}
  return uri.lastPathSegment ?: ""
}

/**
 * `NativeModules.PorticoImagePicker.pick()`: one picture from the system picker, at most 1024
 * pixels on its long side, as JPEG. Answers `null` when nothing was chosen, and on a television.
 * Only the bytes cross the bridge; no address and no lasting permission.
 */
class PorticoImagePicker(context: ReactApplicationContext) : ReactContextBaseJavaModule(context) {
  private var pending: Promise? = null

  private val listener: ActivityEventListener = object : BaseActivityEventListener() {
    override fun onActivityResult(activity: Activity, requestCode: Int, resultCode: Int, data: Intent?) {
      if (requestCode != REQUEST) return
      val promise = pending ?: return
      pending = null
      val uri = if (resultCode == Activity.RESULT_OK) data?.data else null
      if (uri == null) { promise.resolve(null); return }
      pickerWork.execute { promise.resolve(read(uri)) }
    }
  }

  init { context.addActivityEventListener(listener) }

  override fun getName(): String = "PorticoImagePicker"

  override fun invalidate() {
    reactApplicationContext.removeActivityEventListener(listener)
    pending?.resolve(null)
    pending = null
    super.invalidate()
  }

  @ReactMethod
  fun pick(promise: Promise) {
    val activity = reactApplicationContext.currentActivity
    if (TelevisionDisplay.isTelevision || activity == null) { promise.resolve(null); return }
    pending?.resolve(null)
    pending = promise
    try {
      activity.startActivityForResult(Intent(Intent.ACTION_OPEN_DOCUMENT).addCategory(Intent.CATEGORY_OPENABLE).setType("image/*"), REQUEST)
    } catch (_: Exception) {
      pending = null
      promise.resolve(null)
    }
  }

  private fun read(uri: Uri): Any? = try {
    val resolver = reactApplicationContext.contentResolver
    val source: Bitmap? = if (Build.VERSION.SDK_INT >= 28) {
      // The decoder applies the picture's own rotation and never allocates more than it needs.
      ImageDecoder.decodeBitmap(ImageDecoder.createSource(resolver, uri)) { decoder, info, _ ->
        decoder.allocator = ImageDecoder.ALLOCATOR_SOFTWARE
        val longest = Math.max(info.size.width, info.size.height)
        if (longest > 1024) decoder.setTargetSize(Math.max(1, info.size.width * 1024 / longest), Math.max(1, info.size.height * 1024 / longest))
      }
    } else {
      // Read only dimensions first. Older phones must not allocate the original photograph
      // just to produce a 1024-pixel upload. Reopen the stream for the sampled decode.
      val bounds = BitmapFactory.Options().apply { inJustDecodeBounds = true }
      resolver.openInputStream(uri)?.use { BitmapFactory.decodeStream(it, null, bounds) }
      if (bounds.outWidth <= 0 || bounds.outHeight <= 0) null else {
        val options = BitmapFactory.Options().apply { inSampleSize = imagePickerSampleSize(bounds.outWidth, bounds.outHeight) }
        resolver.openInputStream(uri)?.use { BitmapFactory.decodeStream(it, null, options) }
      }
    }
    if (source == null) null else {
      try {
        val longest = Math.max(source.width, source.height)
        val scale = if (longest > 1024) 1024.0 / longest else 1.0
        val width = Math.max(1, Math.floor(source.width * scale).toInt())
        val height = Math.max(1, Math.floor(source.height * scale).toInt())
        // Opaque, on black: a transparent picture must not become a white or garbage background.
        val flat = Bitmap.createBitmap(width, height, Bitmap.Config.ARGB_8888)
        try {
          Canvas(flat).apply { drawColor(Color.BLACK); drawBitmap(source, null, Rect(0, 0, width, height), null) }
          val out = ByteArrayOutputStream()
          flat.compress(Bitmap.CompressFormat.JPEG, 90, out)
          val bytes = out.toByteArray()
          Arguments.createMap().apply {
            putString("base64", Base64.encodeToString(bytes, Base64.NO_WRAP))
            putString("contentType", "image/jpeg")
            putInt("bytes", bytes.size)
          }
        } finally { flat.recycle() }
      } finally { source.recycle() }
    }
  } catch (_: Exception) { null } catch (_: OutOfMemoryError) { null }

  private companion object { const val REQUEST = 0x5049 }
}

/**
 * `NativeModules.PorticoSubtitleFiles.pick()`: one subtitle file (SRT, VTT, ASS/SSA, SUP) or a
 * matching IDX + SUB pair from the system picker, as private staged file URIs. Phones and tablets; a
 * television says the picker is unavailable, as tvOS does.
 */
class PorticoSubtitleFiles(context: ReactApplicationContext) : ReactContextBaseJavaModule(context) {
  private var pending: Promise? = null
  @Volatile private var closed = false
  private val staging = File(context.cacheDir, "portico-subtitle-uploads")

  private val listener: ActivityEventListener = object : BaseActivityEventListener() {
    override fun onActivityResult(activity: Activity, requestCode: Int, resultCode: Int, data: Intent?) {
      if (requestCode != REQUEST) return
      val promise = pending ?: return
      pending = null
      val chosen = ArrayList<Uri>()
      if (resultCode == Activity.RESULT_OK) {
        data?.clipData?.let { clip -> for (i in 0 until clip.itemCount) chosen.add(clip.getItemAt(i).uri) }
        if (chosen.isEmpty()) data?.data?.let { chosen.add(it) }
      }
      if (chosen.isEmpty()) { promise.reject("cancelled", "Subtitle file selection cancelled."); return }
      pickerWork.execute { read(chosen, promise) }
    }
  }

  init { staging.deleteRecursively(); context.addActivityEventListener(listener) }

  override fun getName(): String = "PorticoSubtitleFiles"

  override fun invalidate() {
    closed = true
    staging.deleteRecursively()
    reactApplicationContext.removeActivityEventListener(listener)
    pending?.reject("cancelled", "Subtitle file selection cancelled.")
    pending = null
    super.invalidate()
  }

  @ReactMethod
  fun discard(uris: com.facebook.react.bridge.ReadableArray, promise: Promise) {
    try {
      val root = staging.canonicalFile
      val files = (0 until uris.size()).map { index ->
        val uri = Uri.parse(uris.getString(index) ?: throw IllegalArgumentException())
        require(uri.scheme == "file" && uri.authority.isNullOrEmpty() && uri.query == null && uri.fragment == null)
        val file = File(uri.path ?: throw IllegalArgumentException()).canonicalFile
        require(file.path.startsWith(root.path + File.separator))
        file
      }
      var removed = true
      files.forEach { file ->
        if (file.exists() && !file.deleteRecursively()) removed = false
        file.parentFile?.let { parent -> if (parent != root && parent.list()?.isEmpty() == true) parent.delete() }
      }
      promise.resolve(removed)
    } catch (_: Exception) { promise.resolve(false) }
  }

  @ReactMethod
  fun pick(promise: Promise) {
    if (closed) { promise.reject("bridge_closed", "bridge_closed"); return }
    if (TelevisionDisplay.isTelevision) { promise.reject("subtitle_picker_unavailable", "Manage subtitle files from the web or a phone."); return }
    if (pending != null) { promise.reject("subtitle_picker_busy", "Another subtitle file is being read."); return }
    val activity = reactApplicationContext.currentActivity
    if (activity == null) { promise.reject("subtitle_picker_unavailable", "Open the player before choosing a subtitle."); return }
    pending = promise
    try {
      // Subtitle files have no dependable media type: the extension is checked once a file is chosen.
      activity.startActivityForResult(Intent(Intent.ACTION_OPEN_DOCUMENT).addCategory(Intent.CATEGORY_OPENABLE).setType("*/*").putExtra(Intent.EXTRA_ALLOW_MULTIPLE, true), REQUEST)
    } catch (_: Exception) {
      pending = null
      promise.reject("subtitle_picker_unavailable", "Open the player before choosing a subtitle.")
    }
  }

  private fun read(chosen: List<Uri>, promise: Promise) {
    val context = reactApplicationContext
    val named = chosen.map { it to displayName(context, it) }
    val companion = named.firstOrNull { it.second.lowercase().endsWith(".sub") }
    val main = named.firstOrNull { !it.second.lowercase().endsWith(".sub") }
    val format = main?.second?.substringAfterLast('.', "")?.lowercase() ?: ""
    val pair = format == "idx"
    var valid = main != null && chosen.size == (if (pair) 2 else 1) && format in FORMATS
    if (pair) valid = valid && companion != null && companion.second.lowercase() == main!!.second.substringBeforeLast('.').lowercase() + ".sub"
    if (!valid) { promise.reject("subtitle_format", "Choose one SRT, VTT, ASS/SSA, SUP file, or a matching IDX + SUB pair."); return }
    val directory = File(staging, UUID.randomUUID().toString())
    var delivered = false
    try {
      check(!closed && directory.mkdirs())
      fun name(value: String, extension: String): String {
        val safe = value.substringBeforeLast('.').replace(Regex("[^A-Za-z0-9._-]"), "_").take(150)
        return if (safe.isEmpty() || safe == "." || safe == "..") "subtitle.$extension" else "$safe.$extension"
      }
      val file = File(directory, name(main!!.second, format))
      val limit = if (format == "sup") 33554432 else 4194304
      val size = bounded(context, main.first, file, limit)
      val second = if (pair) File(directory, file.nameWithoutExtension + ".sub") else null
      if (second != null) bounded(context, companion!!.first, second, 33554432 - size)
      check(!closed)
      val result = Arguments.createMap()
      // Provider addresses and lasting access never escape; only private staged copies do.
      result.putString("fileUri", Uri.fromFile(file).toString())
      result.putString("format", format)
      result.putString("title", main.second.take(160))
      if (second != null) result.putString("companionUri", Uri.fromFile(second).toString())
      promise.resolve(result)
      delivered = true
    } catch (_: Exception) {
      promise.reject("subtitle_file", "Read failed or size limit exceeded. Text/index: 4 MiB; binary total: 32 MiB.")
    } finally { if (!delivered || closed) directory.deleteRecursively() }
  }

  /** Copy at most [limit] bytes with bounded memory; refuse empty or oversized input. */
  private fun bounded(context: Context, uri: Uri, file: File, limit: Int): Int {
    val input = context.contentResolver.openInputStream(uri) ?: throw IllegalArgumentException()
    return input.use {
      file.outputStream().use { output ->
        val chunk = ByteArray(65536)
        var size = 0
        while (!closed && size <= limit) {
          val read = input.read(chunk, 0, Math.min(chunk.size, limit + 1 - size))
          if (read < 0) break
          check(read > 0)
          size += read
          check(size <= limit)
          output.write(chunk, 0, read)
        }
        check(!closed && size in 1..limit)
        size
      }
    }
  }

  private companion object {
    const val REQUEST = 0x5053
    val FORMATS = setOf("srt", "vtt", "ass", "ssa", "sup", "idx")
  }
}

/**
 * `NativeModules.PorticoDocumentFiles.pick(requestId, kind)`: one M3U/M3U8 or XML
 * file from the system picker as bounded, strictly decoded UTF-8 text. Phones and
 * tablets only; no path or lasting permission crosses the bridge.
 */
class PorticoDocumentFiles(context: ReactApplicationContext) : ReactContextBaseJavaModule(context) {
  private class Request(val code: Int, val id: String, val kind: String, val activity: Activity, val promise: Promise) {
    @Volatile var cancelled = false
    @Volatile var reading = false
  }
  private var active: Request? = null
  @Volatile private var closed = false
  private val admission = Any()
  private companion object {
    // Process-wide, never reused across React module replacement. This range is
    // disjoint from the image/subtitle pickers and the highlights writer.
    val codeLock = Any()
    var nextCode = 0x6000
    fun allocateCode(): Int? = synchronized(codeLock) {
      if (nextCode > 0x7fff) null else nextCode++
    }
  }
  private val main = Handler(Looper.getMainLooper())

  private val listener: ActivityEventListener = object : BaseActivityEventListener() {
    override fun onActivityResult(activity: Activity, requestCode: Int, resultCode: Int, data: Intent?) {
      var request: Request? = null
      synchronized(admission) {
        if (closed) return
        request = active ?: return
        if (request!!.activity !== activity || request!!.code != requestCode || request!!.reading) return
        request!!.reading = true
      }
      val chosen = request ?: return
      val uri = if (resultCode == Activity.RESULT_OK) data?.data else null
      if (uri == null) { finish(chosen, null, "cancelled", "File selection cancelled."); return }
      pickerWork.execute { read(uri, chosen) }
    }
  }

  init { context.addActivityEventListener(listener) }

  override fun getName(): String = "PorticoDocumentFiles"

  override fun invalidate() {
    var request: Request? = null
    synchronized(admission) {
      closed = true
      request = active; active = null
      request?.cancelled = true
    }
    reactApplicationContext.removeActivityEventListener(listener)
    request?.promise?.resolve(null)
    super.invalidate()
  }

  @ReactMethod
  fun pick(requestId: String, kind: String, promise: Promise) {
    if (TelevisionDisplay.isTelevision) { promise.reject("document_picker_unavailable", "Choose playlist files on a phone or tablet."); return }
    if (!requestId.matches(Regex("^[A-Za-z0-9_-]{1,128}$")) || (kind != "playlist" && kind != "guide")) { promise.reject("document_request_invalid", "Invalid document request."); return }
    val activity = reactApplicationContext.currentActivity
    if (activity == null) { promise.reject("document_picker_unavailable", "Open the app before choosing a file."); return }
    synchronized(admission) {
      if (closed) { promise.reject("bridge_closed", "bridge_closed"); return }
      if (active != null) { promise.reject("document_picker_busy", "Another file is being read."); return }
      val code = allocateCode()
      if (code == null) { promise.reject("document_picker_exhausted", "Restart the app before choosing another file."); return }
      val request = Request(code, requestId, kind, activity, promise); active = request
      activity.runOnUiThread {
        synchronized(admission) {
          // The captured operation alone may register on the captured activity.
          if (closed || active !== request || request.cancelled) return@runOnUiThread
          try {
            activity.startActivityForResult(Intent(Intent.ACTION_OPEN_DOCUMENT).addCategory(Intent.CATEGORY_OPENABLE).setType("*/*"), request.code)
          } catch (_: Exception) {
            active = null; request.cancelled = true
            promise.reject("document_picker_unavailable", "Open the app before choosing a file.")
          }
        }
      }
    }
  }

  @ReactMethod
  fun cancel(requestId: String, promise: Promise) {
    if (closed) { promise.resolve(false); return }
    var request: Request? = null
    var current = false
    synchronized(admission) {
      request = active
      current = request != null && request!!.id == requestId
      if (current) { request!!.cancelled = true; active = null }
    }
    if (current) request!!.promise.resolve(null)
    promise.resolve(current)
  }

  private fun finish(request: Request, result: com.facebook.react.bridge.WritableMap?, code: String?, message: String?) {
    val accepted = synchronized(admission) {
      if (closed || active !== request || request.cancelled) false
      else { active = null; true }
    }
    if (!accepted) return
    if (result != null) request.promise.resolve(result)
    else request.promise.reject(code ?: "document_file", message ?: "Read failed.")
  }

  private fun read(uri: Uri, request: Request) {
    if (closed || request.cancelled) return
    val context = reactApplicationContext
    val name = displayName(context, uri)
    val extension = name.substringAfterLast('.', "").lowercase()
    val allowed = if (request.kind == "playlist") setOf("m3u", "m3u8") else setOf("xml")
    if (extension !in allowed) { main.post { finish(request, null, "document_format", if (request.kind == "playlist") "Choose an M3U playlist file." else "Choose an XMLTV guide file.") }; return }
    val bytes = boundedDocument(context, uri, 16 * 1024 * 1024, request)
    if (bytes == null || closed || request.cancelled) { if (!closed && !request.cancelled) main.post { finish(request, null, "document_file", "Read failed, the file is empty, or it is larger than 16 MB.") }; return }
    val text = try {
      Charsets.UTF_8.newDecoder().onMalformedInput(CodingErrorAction.REPORT).onUnmappableCharacter(CodingErrorAction.REPORT)
        .decode(ByteBuffer.wrap(bytes)).toString()
    } catch (_: CharacterCodingException) {
      main.post { finish(request, null, "document_encoding", "The file is not valid UTF-8 text.") }; return
    }
    if (text.isEmpty()) { main.post { finish(request, null, "document_file", "Read failed, the file is empty, or it is larger than 16 MB.") }; return }
    val result = Arguments.createMap()
    result.putString("data", text)
    result.putString("title", name.take(160))
    result.putInt("bytes", bytes.size)
    main.post { finish(request, result, null, null) }
  }

  private fun boundedDocument(context: Context, uri: Uri, limit: Int, request: Request): ByteArray? = try {
    context.contentResolver.openInputStream(uri)?.use { input ->
      val out = ByteArrayOutputStream()
      val chunk = ByteArray(65536)
      while (!closed && !request.cancelled && out.size() <= limit) {
        val read = input.read(chunk, 0, Math.min(chunk.size, limit + 1 - out.size()))
        if (read <= 0) break
        out.write(chunk, 0, read)
      }
      if (!closed && !request.cancelled && out.size() in 1..limit) out.toByteArray() else null
    }
  } catch (_: Exception) { null }

}
