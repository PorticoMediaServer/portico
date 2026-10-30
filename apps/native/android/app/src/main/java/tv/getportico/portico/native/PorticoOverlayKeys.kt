package tv.getportico.portico.native

import android.os.Handler
import android.os.Looper
import android.view.InputDevice
import android.view.KeyEvent
import com.facebook.react.bridge.Arguments
import com.facebook.react.bridge.ReactApplicationContext
import com.facebook.react.bridge.ReactContextBaseJavaModule
import com.facebook.react.bridge.ReactMethod
import java.lang.ref.WeakReference
import java.util.concurrent.atomic.AtomicLong

/** Escape/Tab in the Activity window, enabled only while the renderer retains an overlay. */
class PorticoOverlayKeys(context: ReactApplicationContext) : ReactContextBaseJavaModule(context) {
  private data class StrokeKey(val device: Int, val code: Int, val downTime: Long)
  private class Stroke(val id: String, val key: StrokeKey, val replay: (KeyEvent) -> Unit) {
    val events = mutableListOf<KeyEvent>()
    var claimed = false
    var handled = false
    var ended = false
    lateinit var timeout: Runnable
  }

  private val main = Handler(Looper.getMainLooper())
  private val strokes = linkedMapOf<String, Stroke>()
  private val lock = Any()
  private var enabled = false

  init { current = WeakReference(this) }

  override fun getName(): String = "PorticoOverlayKeys"

  @ReactMethod fun addListener(event: String) {}
  @ReactMethod fun removeListeners(count: Double) {}

  @ReactMethod fun setEnabled(value: Boolean) {
    main.post {
      enabled = value
      if (!value) flush()
    }
  }

  /** Claim synchronously before invoking JS: a timed-out/replayed key must never dismiss later. */
  @ReactMethod(isBlockingSynchronousMethod = true)
  fun claimKeyEvent(id: String): Boolean = synchronized(lock) {
    val stroke = strokes[id]
    if (stroke == null || stroke.claimed || stroke.handled) false
    else {stroke.claimed = true; true}
  }

  @ReactMethod fun resolveKeyEvent(id: String, handled: Boolean) {
    main.post { finish(id, handled) }
  }

  override fun invalidate() {
    main.post {
      enabled = false
      flush()
      if (current?.get() === this) current = null
    }
    super.invalidate()
  }

  private fun flush() {
    val pending = synchronized(lock) { strokes.values.filter { !it.handled }.map { it.id } }
    pending.forEach { finish(it, false) }
  }

  private fun finish(id: String, handled: Boolean, onlyUnclaimed: Boolean = false) {
    val stroke: Stroke
    val events: List<KeyEvent>
    synchronized(lock) {
      stroke = strokes[id] ?: return
      if (stroke.handled || (onlyUnclaimed && stroke.claimed)) return
      main.removeCallbacks(stroke.timeout)
      stroke.handled = handled
      events = stroke.events.toList()
      stroke.events.clear()
      if (!handled || stroke.ended) strokes.remove(id)
    }
    for (event in events) if (!handled) stroke.replay(event)
  }

  private fun capture(event: KeyEvent, replay: (KeyEvent) -> Unit): Boolean {
    if (event.keyCode != KeyEvent.KEYCODE_ESCAPE && event.keyCode != KeyEvent.KEYCODE_TAB) return false
    if (event.action != KeyEvent.ACTION_DOWN && event.action != KeyEvent.ACTION_UP) return false
    val key = StrokeKey(event.deviceId, event.keyCode, event.downTime)
    synchronized(lock) {
      val stroke = strokes.values.firstOrNull { it.key == key }
      if (stroke != null) {
        if (!stroke.handled) stroke.events.add(KeyEvent(event))
        if (event.action == KeyEvent.ACTION_UP) {
          stroke.ended = true
          if (stroke.handled) strokes.remove(stroke.id)
        }
        return true
      }
    }
    // Never claim a release/repeat without its down, modified shortcuts or keys from another input.
    if (!enabled || !reactApplicationContext.hasActiveReactInstance() || event.action != KeyEvent.ACTION_DOWN || event.repeatCount != 0 || event.isCanceled ||
        !event.isFromSource(InputDevice.SOURCE_KEYBOARD) || event.isCtrlPressed || event.isAltPressed || event.isMetaPressed) return false

    val stroke = Stroke(nextId.incrementAndGet().toString(), key, replay)
    stroke.events.add(KeyEvent(event))
    stroke.timeout = Runnable { finish(stroke.id, false, onlyUnclaimed = true) }
    synchronized(lock) {
      // A lost release cannot retain an already handled stroke when that key is pressed anew.
      val previous = strokes.values.filter { it.handled && it.key.device == key.device && it.key.code == key.code }.map { it.id }
      previous.forEach { strokes.remove(it) }
      if (strokes.size >= 16) return false
      strokes[stroke.id] = stroke
    }
    main.postDelayed(stroke.timeout, 250)
    try {
      reactApplicationContext.emitDeviceEvent("PorticoOverlayKey", Arguments.createMap().apply {
        putString("id", stroke.id)
        putString("key", if (event.keyCode == KeyEvent.KEYCODE_ESCAPE) "escape" else "tab")
        putBoolean("shift", event.isShiftPressed)
      })
    } catch (_: RuntimeException) {
      // Returning false lets the original event take the ordinary path exactly once.
      main.removeCallbacks(stroke.timeout)
      synchronized(lock) { strokes.remove(stroke.id) }
      stroke.events.clear()
      return false
    }
    return true
  }

  companion object {
    @Volatile private var current: WeakReference<PorticoOverlayKeys>? = null
    private val nextId = AtomicLong()

    /** The caller supplies ordinary Activity dispatch, bypassing this interception on replay. */
    fun dispatch(event: KeyEvent, replay: (KeyEvent) -> Unit): Boolean = current?.get()?.capture(event, replay) ?: false
  }
}
