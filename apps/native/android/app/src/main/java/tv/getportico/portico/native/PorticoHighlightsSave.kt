package tv.getportico.portico.native

import android.app.Activity
import android.content.Intent
import android.net.Uri
import com.facebook.react.bridge.*
import java.io.OutputStream
import java.util.concurrent.atomic.AtomicBoolean

/** Only a bounded Markdown export; no caller-controlled path or generic writer. */
class PorticoHighlightsSave(private val context: ReactApplicationContext) : ReactContextBaseJavaModule(context) {
  private class Operation(val id: String, val code: Int, val activity: Activity, val bytes: ByteArray, val promise: Promise, val kind: String = "highlights", val mime: String = "text/markdown") {
    val settled = AtomicBoolean(false)
    // Every custody field below is protected by the process-wide gate.
    var cancelled = false
    var writing = false
    var resultClaimed = false
    var stream: OutputStream? = null
    fun finish(value: String) { if (settled.compareAndSet(false, true)) promise.resolve(value) }
    fun fail() { if (settled.compareAndSet(false, true)) promise.reject("highlights_save_failed", "Could not save highlights") }
  }
  companion object {
    private val gate = Any()
    // Never restart this namespace when React recreates a module. Exhaustion
    // refuses admission until process restart; old activities cannot survive it.
    private var nextCode = 46203
    private var custody: Operation? = null
    private val diagnosticsIds = HashSet<String>()
  }
  private var closed = false
  private var operation: Operation? = null
  private fun owns(value: Operation) = !closed && operation === value && custody === value && !value.cancelled
  private fun release(value: Operation) = synchronized(gate) {
    value.stream = null
    value.bytes.fill(0)
    if (operation === value) operation = null
    if (custody === value) custody = null
  }
  private fun cancel(value: Operation) {
    val stream: OutputStream?
    synchronized(gate) {
      if (operation !== value || value.cancelled) return
      value.cancelled = true
      stream = value.stream
      value.finish("cancelled")
      // A blocked provider writer retains process-wide custody until its own
      // physical finally. Never zero a buffer while that writer still uses it.
      if (!value.writing) release(value)
    }
    value.activity.runOnUiThread { value.activity.finishActivity(value.code) }
    try { stream?.close() } catch (_: Exception) { /* writer finally owns retirement */ }
  }
  private val listener = object : BaseActivityEventListener() {
    override fun onActivityResult(activity: Activity, code: Int, result: Int, data: Intent?) {
      val original = synchronized(gate) {
        val value = operation ?: return
        if (!owns(value) || activity !== value.activity || code != value.code || value.resultClaimed) return
        value.resultClaimed = true
        value
      }
      val uri: Uri? = data?.data
      if (result != Activity.RESULT_OK || uri == null) { cancel(original); return }
      synchronized(gate) {
        if (!owns(original)) return
        if (uri.scheme != "content") { original.fail(); release(original); return }
        original.writing = true
      }
      try { Thread {
        try {
          if (!synchronized(gate) { owns(original) }) return@Thread
          val output = context.contentResolver.openOutputStream(uri, "w") ?: throw java.io.IOException()
          try {
            synchronized(gate) {
              if (!owns(original)) throw java.io.InterruptedIOException()
              original.stream = output
            }
            var offset = 0
            while (offset < original.bytes.size) {
              if (!synchronized(gate) { owns(original) }) throw java.io.InterruptedIOException()
              val count = minOf(16384, original.bytes.size - offset)
              output.write(original.bytes, offset, count); offset += count
            }
            if (!synchronized(gate) { owns(original) }) throw java.io.InterruptedIOException()
            output.flush()
          } finally { output.close() }
          synchronized(gate) { if (owns(original)) original.finish("saved") }
        } catch (_: Exception) { synchronized(gate) { if (owns(original)) original.fail() } }
        finally { release(original) }
      }.start() } catch (_: Exception) { synchronized(gate) { if (owns(original)) original.fail(); release(original) } }
    }
  }
  init { context.addActivityEventListener(listener) }
  override fun getName() = "PorticoHighlightsSave"
  @ReactMethod fun saveHighlights(id: String, name: String, markdown: String, promise: Promise) {
    if (!Regex("^[A-Za-z0-9_-]{32}$").matches(id) || !Regex("^[A-Za-z0-9 -]{1,64}-highlights\\.md$").matches(name) || markdown.length > 4194304 || !markdown.startsWith("# Highlights\n")) { promise.reject("invalid_highlights", "Invalid highlights export"); return }
    synchronized(gate) {
      if (closed || custody != null || nextCode > 65534) { promise.reject("highlights_busy", "Save unavailable"); return }
      val activity = context.currentActivity
      if (activity == null) { promise.reject("highlights_busy", "Save unavailable"); return }
      val bytes = markdown.toByteArray(Charsets.UTF_8)
      if (bytes.size > 4194304) { bytes.fill(0); promise.reject("invalid_highlights", "Invalid highlights export"); return }
      val original = Operation(id, nextCode++, activity, bytes, promise)
      operation = original; custody = original
      activity.runOnUiThread {
        synchronized(gate) {
          if (!owns(original)) return@runOnUiThread
          try { activity.startActivityForResult(Intent(Intent.ACTION_CREATE_DOCUMENT).addCategory(Intent.CATEGORY_OPENABLE).setType(original.mime).putExtra(Intent.EXTRA_TITLE, name), original.code) }
          catch (_: Exception) { original.fail(); release(original) }
        }
      }
    }
  }
  /** Fixed JSON diagnostics only, sharing the existing physical writer custody. */
  @ReactMethod fun saveDiagnostics(id: String, kind: String, json: String, promise: Promise) {
    if (!Regex("^[A-Za-z0-9_-]{32}$").matches(id) || kind !in listOf("report", "support") || json.length > 8388608) { promise.reject("invalid_diagnostics", "Invalid diagnostics export"); return }
    try {
      val parser = org.json.JSONTokener(json)
      val document = parser.nextValue() as? org.json.JSONObject ?: throw IllegalArgumentException()
      if (parser.nextClean() != 0.toChar() || document.opt("id") !is String || (kind == "report" && document.opt("body") !is String) || (kind == "support" && document.opt("manifest") !is org.json.JSONObject)) throw IllegalArgumentException()
    } catch (_: Exception) { promise.reject("invalid_diagnostics", "Invalid diagnostics export"); return }
    synchronized(gate) {
      if (closed || custody != null || nextCode > 65534 || diagnosticsIds.size >= 4096 || diagnosticsIds.contains(id)) { promise.reject("diagnostics_busy", "Save unavailable"); return }
      val activity = context.currentActivity
      if (activity == null) { promise.reject("diagnostics_busy", "Save unavailable"); return }
      val bytes = json.toByteArray(Charsets.UTF_8)
      if (bytes.size > 8388608) { bytes.fill(0); promise.reject("invalid_diagnostics", "Invalid diagnostics export"); return }
      diagnosticsIds.add(id)
      val original = Operation(id, nextCode++, activity, bytes, promise, "diagnostics", "application/json")
      operation = original; custody = original
      activity.runOnUiThread {
        synchronized(gate) {
          if (!owns(original)) return@runOnUiThread
          try { activity.startActivityForResult(Intent(Intent.ACTION_CREATE_DOCUMENT).addCategory(Intent.CATEGORY_OPENABLE).setType(original.mime).putExtra(Intent.EXTRA_TITLE, "portico-$kind.json"), original.code) }
          catch (_: Exception) { original.fail(); release(original) }
        }
      }
    }
  }
  /** The invitation card as a PNG, at most 4 MB, under one fixed name. */
  @ReactMethod fun saveInvitationPicture(id: String, png: String, promise: Promise) {
    if (!Regex("^[A-Za-z0-9_-]{32}$").matches(id) || png.length > 5600000) { promise.reject("invalid_invitation_picture", "Invalid invitation picture"); return }
    val bytes = try { android.util.Base64.decode(png, android.util.Base64.NO_WRAP) } catch (_: Exception) { null }
    val signature = byteArrayOf(0x89.toByte(), 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A)
    if (bytes == null || bytes.size < 8 || bytes.size > 4194304 || !bytes.copyOfRange(0, 8).contentEquals(signature)) { bytes?.fill(0); promise.reject("invalid_invitation_picture", "Invalid invitation picture"); return }
    synchronized(gate) {
      if (closed || custody != null || nextCode > 65534) { bytes.fill(0); promise.reject("invitation_picture_busy", "Save unavailable"); return }
      val activity = context.currentActivity
      if (activity == null) { bytes.fill(0); promise.reject("invitation_picture_busy", "Save unavailable"); return }
      val original = Operation(id, nextCode++, activity, bytes, promise, "invitation", "image/png")
      operation = original; custody = original
      activity.runOnUiThread {
        synchronized(gate) {
          if (!owns(original)) return@runOnUiThread
          try { activity.startActivityForResult(Intent(Intent.ACTION_CREATE_DOCUMENT).addCategory(Intent.CATEGORY_OPENABLE).setType(original.mime).putExtra(Intent.EXTRA_TITLE, "portico-invitation.png"), original.code) }
          catch (_: Exception) { original.fail(); release(original) }
        }
      }
    }
  }
  @ReactMethod fun cancelInvitationPicture(id: String) {
    val original = synchronized(gate) { operation?.takeIf { it.id == id && it.kind == "invitation" } } ?: return
    cancel(original)
  }
  @ReactMethod fun cancelDiagnostics(id: String) {
    val original = synchronized(gate) { operation?.takeIf { it.id == id && it.kind == "diagnostics" } } ?: return
    cancel(original)
  }
  @ReactMethod fun cancelHighlights(id: String) {
    val original = synchronized(gate) { operation?.takeIf { it.id == id && it.kind == "highlights" } } ?: return
    cancel(original)
    // The selected provider destination belongs to the user; never delete it.
  }
  override fun invalidate() {
    val original = synchronized(gate) { closed = true; operation }
    original?.let { cancel(it) }
    context.removeActivityEventListener(listener)
    super.invalidate()
  }
}
