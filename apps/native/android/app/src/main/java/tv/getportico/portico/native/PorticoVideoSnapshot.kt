package tv.getportico.portico.native

import android.graphics.Bitmap
import android.net.Uri
import android.os.Build
import android.os.Handler
import android.os.Looper
import android.view.PixelCopy
import android.view.SurfaceHolder
import android.view.SurfaceView
import android.view.View
import android.view.WindowManager
import com.facebook.react.bridge.Arguments
import com.facebook.react.bridge.LifecycleEventListener
import com.facebook.react.bridge.Promise
import com.facebook.react.bridge.ReactApplicationContext
import com.facebook.react.bridge.ReactContextBaseJavaModule
import com.facebook.react.bridge.ReactMethod
import com.facebook.react.bridge.ReadableMap
import com.facebook.react.uimanager.UIManagerHelper
import com.facebook.react.modules.core.DeviceEventManagerModule
import java.io.File
import java.util.concurrent.Executors
import java.util.concurrent.atomic.AtomicBoolean
import tv.getportico.portico.TelevisionDisplay
import kotlin.math.floor
import kotlin.math.min
import kotlin.math.sqrt

internal data class PorticoSnapshotTarget(val surface: SurfaceView, val current: () -> Boolean)

internal object PorticoSnapshotBinding {
  fun matches(expected: Map<String, Any?>, actual: Map<String, Any?>?): Boolean {
    if (actual == null) return false
    return listOf("lease", "sessionId", "itemId").all { key ->
      val value = expected[key] as? String
      value != null && value.isNotEmpty() && value.length <= 512 && value == actual[key]
    } &&
        listOf("intentId", "generation").all { key ->
          val value = (expected[key] as? Number)?.toDouble()
          value != null && value.isFinite() && value > 0 && value <= 9007199254740991.0 && value == floor(value) && value == (actual[key] as? Number)?.toDouble()
        }
  }
}

/** Only the real leased video surface, never a view drawing-cache or window/backdrop screenshot. */
class PorticoVideoSnapshot(context: ReactApplicationContext) : ReactContextBaseJavaModule(context), LifecycleEventListener {
  private val main = Handler(Looper.getMainLooper())
  private val encoder = Executors.newSingleThreadExecutor()
  private val directory = File(context.cacheDir, "portico-video-snapshots")
  private var flight: Capture? = null
  private var retained: Capture? = null
  private var invalidated = false
  private var foreground = true

  private class Capture(val id: String, var target: PorticoSnapshotTarget?, val promise: Promise, val bitmap: Bitmap, var source: Map<String, Any?>?) {
    var passive = false
    var policy: Runnable? = null
    val cancelled = AtomicBoolean(false)
    var settled = false
    var file: File? = null
    lateinit var timeout: Runnable
    lateinit var expiry: Runnable
    lateinit var detach: View.OnAttachStateChangeListener
    lateinit var surface: SurfaceHolder.Callback
  }

  init {
    // Private cache from an interrupted previous process is never reused as a video frame.
    directory.mkdirs()
    directory.listFiles()?.filter { it.isFile && it.name.startsWith("frame-") && it.extension == "png" }?.forEach { it.delete() }
    context.addLifecycleEventListener(this)
  }

  override fun getName() = "PorticoVideoSnapshot"

  private fun current(target: PorticoSnapshotTarget): Boolean = try { allowed() && target.current() } catch (_: Exception) { false }

  private fun allowed(): Boolean {
    val activity = reactApplicationContext.currentActivity ?: return false
    return !invalidated && foreground && Build.VERSION.SDK_INT >= 24 && !TelevisionDisplay.isTelevision &&
        activity.window.decorView.hasWindowFocus() && (activity.window.attributes.flags and WindowManager.LayoutParams.FLAG_SECURE) == 0 &&
        (Build.VERSION.SDK_INT < 26 || !activity.isInPictureInPictureMode)
  }

  @ReactMethod fun capture(id: String, raw: ReadableMap, promise: Promise) {
    val source = try { raw.toHashMap() } catch (_: Exception) { null }
    main.post {
      if (flight != null || retained != null) { promise.reject("snapshot_busy", "A video frame is already retained or being copied."); return@post }
      if (!allowed() || !id.matches(Regex("[A-Za-z0-9_-]{1,96}")) || source == null) {
        promise.reject("snapshot_unavailable", "Video frame capture is unavailable."); return@post
      }
      val tag = (source["reactTag"] as? Number)?.toDouble()
      val lease = source["lease"] as? String
      @Suppress("UNCHECKED_CAST") val bound = source["binding"] as? Map<String, Any?>
      if (tag == null || tag <= 0 || tag > Int.MAX_VALUE || tag != floor(tag) || lease.isNullOrEmpty() || bound == null) {
        promise.reject("snapshot_unavailable", "Invalid video capture source."); return@post
      }
      val expected = bound.toMutableMap().apply { put("lease", lease) }
      val view = try { UIManagerHelper.getUIManagerForReactTag(reactApplicationContext, tag.toInt())?.resolveView(tag.toInt()) } catch (_: Exception) { null }
      val target = try { when {
        source["engine"] == "native" && view is PorticoPlayerView -> view.snapshotTarget(expected)
        source["engine"] == "mpv" && view is PorticoMpvPlayerView -> view.snapshotTarget(expected)
        else -> null
      } } catch (_: Exception) { null }
      if (target == null || !current(target) || !target.surface.holder.surface.isValid) {
        promise.reject("snapshot_unavailable", "The leased video surface has no capturable current frame."); return@post
      }
      val frame = target.surface.holder.surfaceFrame
      val w = frame.width(); val h = frame.height()
      if (w <= 0 || h <= 0) { promise.reject("snapshot_unavailable", "The video surface has no size."); return@post }
      val scale = min(1.0, min(1280.0 / maxOf(w, h), sqrt(921600.0 / (w.toDouble() * h))))
      val bitmap = try { Bitmap.createBitmap(maxOf(1, floor(w * scale).toInt()), maxOf(1, floor(h * scale).toInt()), Bitmap.Config.ARGB_8888) }
      catch (_: Exception) { null } catch (_: OutOfMemoryError) { null }
      if (bitmap == null) { promise.reject("snapshot_unavailable", "Video frame allocation failed."); return@post }
      val work = Capture(id, target, promise, bitmap, source)
      flight = work
      work.timeout = Runnable { cancel(work, "snapshot_timeout") }
      work.expiry = Runnable { cancel(work, "snapshot_expired") }
      work.detach = object : View.OnAttachStateChangeListener {
        override fun onViewAttachedToWindow(view: View) {}
        override fun onViewDetachedFromWindow(view: View) { if (!work.passive) cancel(work, "snapshot_stale") }
      }
      work.surface = object : SurfaceHolder.Callback {
        override fun surfaceCreated(holder: SurfaceHolder) { if (!work.passive) cancel(work, "snapshot_stale") }
        override fun surfaceChanged(holder: SurfaceHolder, format: Int, width: Int, height: Int) { if (!work.passive) cancel(work, "snapshot_stale") }
        override fun surfaceDestroyed(holder: SurfaceHolder) { if (!work.passive) cancel(work, "snapshot_stale") }
      }
      target.surface.addOnAttachStateChangeListener(work.detach)
      target.surface.holder.addCallback(work.surface)
      main.postDelayed(work.timeout, 1500)
      try {
        PixelCopy.request(target.surface, bitmap, { result ->
          if (work.cancelled.get() || !current(target)) { finish(work, null, "snapshot_stale"); return@request }
          if (result != PixelCopy.SUCCESS) { finish(work, null, "snapshot_unavailable"); return@request }
          // Encoding never blocks the UI. Only this one bitmap can exist until its callback/encode completes.
          encoder.execute {
            var file: File? = null
            try {
              if (!work.cancelled.get()) {
                file = File.createTempFile("frame-", ".png", directory)
                file.outputStream().use { stream -> check(bitmap.compress(Bitmap.CompressFormat.PNG, 100, stream)) }
              }
            } catch (_: Exception) { file?.delete(); file = null }
            val encoded = file
            main.post { finish(work, encoded, if (encoded == null) "snapshot_unavailable" else null) }
          }
        }, main)
      } catch (_: Exception) { finish(work, null, "snapshot_unavailable") }
    }
  }

  /** Always on the UI thread after PixelCopy/encoding have stopped writing the bitmap. */
  private fun finish(work: Capture, file: File?, error: String?) {
    main.removeCallbacks(work.timeout)
    if (flight === work) flight = null
    val width = work.bitmap.width; val height = work.bitmap.height
    work.bitmap.recycle()
    if (error != null || work.cancelled.get() || (work.target?.let { !current(it) } ?: true)) {
      file?.delete()
      cancel(work, error ?: "snapshot_stale")
      unlisten(work)
      return
    }
    work.file = file
    retained = work
    work.settled = true
    main.postDelayed(work.expiry, 15000)
    work.promise.resolve(Arguments.createMap().apply {
      putString("id", work.id); putString("uri", Uri.fromFile(file!!).toString())
      putInt("width", width); putInt("height", height)
    })
  }

  private fun detachSource(work: Capture) {
    work.target?.surface?.removeOnAttachStateChangeListener(work.detach)
    work.target?.surface?.holder?.removeCallback(work.surface)
  }
  private fun unlisten(work: Capture) {
    detachSource(work)
    main.removeCallbacks(work.expiry)
    work.policy?.let { main.removeCallbacks(it) }; work.policy = null
  }

  @ReactMethod fun addListener(eventName: String) { /* RN event subscription bookkeeping. */ }
  @ReactMethod fun removeListeners(count: Double) { /* No capture authority comes from listeners. */ }

  /** Transfer only already-encoded pixels. Original expiry is never restarted. */
  @ReactMethod fun retainForExit(id: String, raw: ReadableMap, promise: Promise) {
    val expected = try { raw.toHashMap() } catch (_: Exception) { null }
    main.post {
      val work = retained
      val target = work?.target
      @Suppress("UNCHECKED_CAST") val binding = expected?.get("binding") as? Map<String, Any?>
      val tag = (expected?.get("reactTag") as? Number)?.toDouble()
      val full = binding?.toMutableMap()?.apply { put("lease", expected?.get("lease")) }
      // Resolve the same view and ask its ORIGINAL current-source predicate again.
      val view = if (tag != null && tag > 0 && tag <= Int.MAX_VALUE && tag == floor(tag))
        try { UIManagerHelper.getUIManagerForReactTag(reactApplicationContext, tag.toInt())?.resolveView(tag.toInt()) } catch (_: Exception) { null } else null
      val offered = try { when {
        expected?.get("engine") == "native" && view is PorticoPlayerView && full != null -> view.snapshotTarget(full)
        expected?.get("engine") == "mpv" && view is PorticoMpvPlayerView && full != null -> view.snapshotTarget(full)
        else -> null
      } } catch (_: Exception) { null }
      if (work == null || work.id != id || expected != work.source || !work.settled || work.passive || work.cancelled.get() || work.file?.isFile != true || target == null ||
          offered == null || offered.surface !== target.surface || !current(target) || !current(offered)) { promise.resolve(false); return@post }
      work.passive = true
      detachSource(work); work.target = null; work.source = null // No renderer/player/lease survives this transfer.
      val policy = object : Runnable {
        override fun run() {
          if (retained !== work || work.cancelled.get()) return
          if (!allowed()) { cancel(work, "snapshot_stale"); return }
          main.postDelayed(this, 50) // Resource-policy revocation, not playback or animation timing.
        }
      }
      work.policy = policy; main.post(policy)
      promise.resolve(true)
    }
  }

  private fun cancel(work: Capture, code: String) {
    if (work.cancelled.getAndSet(true)) return
    main.removeCallbacks(work.timeout)
    unlisten(work)
    work.file?.delete(); work.file = null
    if (retained === work) retained = null
    if (work.passive) try {
      reactApplicationContext.getJSModule(DeviceEventManagerModule.RCTDeviceEventEmitter::class.java)
        .emit("PorticoVideoSnapshotRetired", Arguments.createMap().apply { putString("id", work.id) })
    } catch (_: Exception) { /* File cleanup does not depend on a live JS bridge. */ }
    if (!work.settled) { work.settled = true; work.promise.reject(code, "The video capture is no longer available.") }
    // PixelCopy has no cancellation API. Keep flight occupied and its bitmap alive until callback/encode completion.
  }

  @ReactMethod fun release(id: String, promise: Promise) {
    main.post {
      val work = flight?.takeIf { it.id == id } ?: retained?.takeIf { it.id == id }
      if (work != null) cancel(work, "snapshot_cancelled")
      promise.resolve(work != null)
    }
  }

  private fun clear() { flight?.let { cancel(it, "snapshot_cancelled") }; retained?.let { cancel(it, "snapshot_cancelled") } }
  override fun onHostResume() { main.post { foreground = true } }
  override fun onHostPause() { main.post { foreground = false; clear() } }
  override fun onHostDestroy() { main.post { foreground = false; clear() } }
  override fun invalidate() {
    reactApplicationContext.removeLifecycleEventListener(this)
    main.post { invalidated = true; clear(); encoder.shutdown() }
    super.invalidate()
  }
}
