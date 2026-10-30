package tv.getportico.portico.native

import android.app.Activity
import android.content.Intent
import com.facebook.react.bridge.*
import tv.getportico.portico.TelevisionDisplay

/** One explicit in-app camera operation. Raw text only: no URL opening or authority parsing. */
class PorticoQRScanner(context: ReactApplicationContext) : ReactContextBaseJavaModule(context), LifecycleEventListener {
  private data class Pending(val id: String, val promise: Promise)
  @Volatile private var pending: Pending? = null
  @Volatile private var closed = false
  private val listener = object : BaseActivityEventListener() {
    override fun onActivityResult(activity: Activity, requestCode: Int, resultCode: Int, data: Intent?) {
      if (requestCode != REQUEST_CODE) return
      val owned = pending ?: return
      if (closed) { retireOriginal(owned); return }
      // A late result from a cancelled scan cannot settle its successor.
      val answer = data ?: return
      if (answer.getStringExtra("requestId") != owned.id) return
      pending = null
      val failure = answer.getStringExtra("failure")
      val raw = answer.getStringExtra("raw")
      if (closed) owned.promise.resolve(null)
      else if (failure != null) owned.promise.reject(if (failure in FAILURES) failure else "qr_camera_unavailable", "QR scan did not complete.")
      else if (resultCode != Activity.RESULT_OK) owned.promise.resolve(null)
      else if (raw == null || raw.toByteArray(Charsets.UTF_8).size !in 1..8192) owned.promise.reject("qr_too_large", "QR result is outside the supported size.")
      else owned.promise.resolve(raw)
    }
  }
  init { context.addActivityEventListener(listener); context.addLifecycleEventListener(this) }
  override fun getName() = "PorticoQRScanner"
  @ReactMethod fun scan(requestId: String, title: String, cancel: String, promise: Promise) = UiThreadUtil.runOnUiThread {
    if (closed) { promise.reject("qr_camera_unavailable", "Scanner module is closed."); return@runOnUiThread }
    if (!requestId.matches(Regex("[a-f0-9]{32}")) || title.length !in 1..200 || cancel.length !in 1..100) {
      promise.reject("qr_invalid_request", "Invalid scanner opening."); return@runOnUiThread
    }
    if (pending != null) { promise.reject("qr_busy", "A QR scan is already open."); return@runOnUiThread }
    val activity = reactApplicationContext.currentActivity
    if (TelevisionDisplay.isTelevision || activity == null || activity.isFinishing) {
      promise.reject("qr_camera_unavailable", "Camera scanning is unavailable."); return@runOnUiThread
    }
    if (!PorticoQRScanActivity.admit(requestId)) { promise.reject("qr_busy", "A QR scan is already open."); return@runOnUiThread }
    val owned = Pending(requestId, promise); pending = owned
    if (closed) { retireOriginal(owned); return@runOnUiThread }
    try {
      if (closed || pending !== owned) { retireOriginal(owned); return@runOnUiThread }
      activity.startActivityForResult(Intent(activity, PorticoQRScanActivity::class.java)
        .putExtra("requestId", requestId).putExtra("title", title).putExtra("cancel", cancel), REQUEST_CODE)
    } catch (_: Exception) { PorticoQRScanActivity.cancel(requestId); if (pending === owned) pending = null; promise.reject("qr_camera_unavailable", "Camera scanning is unavailable.") }
  }
  @ReactMethod fun cancel(requestId: String) = UiThreadUtil.runOnUiThread {
    val owned = pending
    if (owned?.id != requestId) return@runOnUiThread
    pending = null; PorticoQRScanActivity.cancel(requestId); owned.promise.resolve(null)
  }
  override fun onHostResume() {}
  // The app's own scan Activity intentionally pauses the React host. That Activity
  // stops its camera on Pause and cancels on Stop, including real backgrounding.
  override fun onHostPause() {}
  private fun retireOriginal(owned: Pending?) {
    if (owned == null) return
    PorticoQRScanActivity.cancel(owned.id)
    if (pending === owned) { pending = null; owned.promise.resolve(null) }
  }
  override fun onHostDestroy() {
    val original = pending
    original?.let { PorticoQRScanActivity.retireAdmission(it.id) }
    UiThreadUtil.runOnUiThread { retireOriginal(original) }
  }
  override fun invalidate() {
    // Terminal before dispatch: a scan already queued on main cannot reopen us.
    closed = true
    val original = pending
    original?.let { PorticoQRScanActivity.retireAdmission(it.id) }
    UiThreadUtil.runOnUiThread { retireOriginal(original) }
    reactApplicationContext.removeActivityEventListener(listener)
    reactApplicationContext.removeLifecycleEventListener(this); super.invalidate()
  }
  companion object {
    const val REQUEST_CODE = 6083
    val FAILURES = setOf("qr_permission_denied", "qr_camera_unavailable", "qr_too_large")
  }
}
