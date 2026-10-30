package tv.getportico.portico.native

import android.app.Activity
import android.content.pm.ActivityInfo
import android.os.Handler
import android.os.Looper
import com.facebook.react.bridge.Promise
import com.facebook.react.bridge.ReactApplicationContext
import com.facebook.react.bridge.ReactContextBaseJavaModule
import com.facebook.react.bridge.ReactMethod
import tv.getportico.portico.TelevisionDisplay

/**
 * The one sideways presentation the player may ask this Activity for. It owns no engine,
 * surface, lease or PiP view: the manifest's own `configChanges` keeps the Activity and the
 * React view mounted across the turn.
 *
 * Admission, capture and restore are all one main-queue turn each, so a successor's capture can
 * only ever read the value a predecessor restored. One owner at a time; a second request is
 * refused instead of capturing a forced orientation. An owner retired before its posted enter
 * runs is never turned, and a module invalidation retires only that instance's own request.
 */
class PorticoFullWindow(private val context: ReactApplicationContext) : ReactContextBaseJavaModule(context) {
  /** The one request; read and written only on the main thread. */
  private object Turn {
    var module: PorticoFullWindow? = null
    var owner: String? = null
    var activity: Activity? = null
    var previous = ActivityInfo.SCREEN_ORIENTATION_UNSPECIFIED
  }

  /** Owners retired before their posted turn ran; the only cross-thread state. */
  private val retiredOwners = HashSet<String>()
  private val retiredLock = Any()

  @Volatile private var retired = false
  private val ui = Handler(Looper.getMainLooper())

  override fun getName() = "PorticoFullWindow"

  /** OS/module support. Never a snapshot of whether an Activity happens to exist right now. */
  override fun getConstants(): Map<String, Any> = mapOf("orientationSupported" to !TelevisionDisplay.isTelevision)

  // The JS contract constructs one emitter for the failure event; Android never emits it.
  @ReactMethod fun addListener(@Suppress("UNUSED_PARAMETER") event: String) {}
  @ReactMethod fun removeListeners(@Suppress("UNUSED_PARAMETER") count: Double) {}

  private fun retireOwner(owner: String) {synchronized(retiredLock) {retiredOwners.add(owner)}}

  private fun consumeRetired(owner: String): Boolean = synchronized(retiredLock) {retiredOwners.remove(owner)}

  @ReactMethod fun enter(owner: String, promise: Promise) {
    ui.post {
      if (retired || TelevisionDisplay.isTelevision || consumeRetired(owner) || Turn.owner != null) {promise.resolve(false); return@post}
      val activity = context.currentActivity
      if (activity == null || activity.isFinishing || activity.isDestroyed) {promise.resolve(false); return@post}
      // Captured only in this turn, so no predecessor's forced value can become this request's.
      Turn.module = this
      Turn.owner = owner
      Turn.activity = activity
      Turn.previous = activity.requestedOrientation
      try {
        activity.requestedOrientation = ActivityInfo.SCREEN_ORIENTATION_SENSOR_LANDSCAPE
      } catch (failure: RuntimeException) {
        // Clear only this owner's slot and settle: a refused assignment must not strand the
        // owner or leave the native call unresolved.
        if (Turn.module === this && Turn.owner == owner) {Turn.module = null; Turn.owner = null; Turn.activity = null}
        promise.resolve(false)
        return@post
      }
      promise.resolve(true)
    }
  }

  @ReactMethod fun leave(owner: String) {
    // Synchronous, so a close that lands before the posted enter cancels it before it turns.
    retireOwner(owner)
    ui.post {
      consumeRetired(owner)
      if (Turn.module !== this || Turn.owner != owner) return@post
      val activity = Turn.activity
      val previous = Turn.previous
      Turn.module = null; Turn.owner = null; Turn.activity = null
      // Restored inside this same turn; a successor's enter runs after it and captures this value.
      restore(activity, previous)
    }
  }

  override fun invalidate() {
    retired = true
    ui.post {
      if (Turn.module !== this) return@post
      val activity = Turn.activity
      val previous = Turn.previous
      Turn.module = null; Turn.owner = null; Turn.activity = null
      restore(activity, previous)
    }
    super.invalidate()
  }

  /** Restore only the Activity we captured and only the request we made. */
  private fun restore(activity: Activity?, previous: Int) {
    if (activity == null || activity !== context.currentActivity || activity.isFinishing || activity.isDestroyed) return
    if (activity.requestedOrientation != ActivityInfo.SCREEN_ORIENTATION_SENSOR_LANDSCAPE) return
    try {activity.requestedOrientation = previous} catch (failure: RuntimeException) {}
  }
}
