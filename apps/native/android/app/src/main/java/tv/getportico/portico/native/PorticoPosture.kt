package tv.getportico.portico.native

import android.app.Activity
import android.content.ComponentCallbacks
import android.content.res.Configuration
import android.graphics.Rect
import android.os.Handler
import android.os.Looper
import android.view.View
import androidx.core.content.ContextCompat
import androidx.core.util.Consumer
import androidx.window.java.layout.WindowInfoTrackerCallbackAdapter
import androidx.window.layout.FoldingFeature
import androidx.window.layout.WindowInfoTracker
import androidx.window.layout.WindowLayoutInfo
import androidx.window.layout.WindowMetricsCalculator
import com.facebook.react.bridge.Arguments
import com.facebook.react.bridge.LifecycleEventListener
import com.facebook.react.bridge.Promise
import com.facebook.react.bridge.ReactApplicationContext
import com.facebook.react.bridge.ReactMethod
import com.facebook.react.common.LifecycleState

/** Read-only OS posture publisher; it owns no surface, navigation or playback intent. */
class PorticoPosture(context: ReactApplicationContext) : PorticoEventModule(context), LifecycleEventListener, ComponentCallbacks {
  private val ui = Handler(Looper.getMainLooper())
  private val facts = PorticoPostureState()
  private var count = 0
  private var resumed = false
  private var closed = false
  private var host: Activity? = null
  private var metrics: PostureMetrics? = null
  private var tracker: WindowInfoTrackerCallbackAdapter? = null
  private var callback: Consumer<WindowLayoutInfo>? = null
  private var decor: View? = null
  private val layout = View.OnLayoutChangeListener { _, _, _, _, _, _, _, _, _ -> reconcile() }

  override fun getName() = "PorticoPosture"
  override fun initialize() {
    super.initialize()
    reactApplicationContext.addLifecycleEventListener(this)
    reactApplicationContext.registerComponentCallbacks(this)
    ui.post { if (!closed) { resumed = reactApplicationContext.lifecycleState == LifecycleState.RESUMED; reconcile() } }
  }
  @ReactMethod fun addListener(event: String) { if (event == EVENT) ui.post { if (!closed) { count++; reconcile() } } }
  @ReactMethod fun removeListeners(number: Double) {
    ui.post {
      if (!closed && number.isFinite() && number > 0) {
        count = (count - number.coerceAtMost(Int.MAX_VALUE.toDouble()).toInt()).coerceAtLeast(0)
        if (count == 0) retire()
      }
    }
  }
  @ReactMethod fun getSnapshot(promise: Promise) { ui.post { if (!closed) reconcile(); promise.resolve(wire()) } }

  private fun bounds(rect: Rect) = PostureBounds(rect.left.toDouble(), rect.top.toDouble(), rect.right.toDouble(), rect.bottom.toDouble())
  private fun measure(activity: Activity): PostureMetrics {
    val value = WindowMetricsCalculator.getOrCreate().computeCurrentWindowMetrics(activity)
    return PostureMetrics(bounds(value.bounds), value.density.toDouble())
  }
  private fun feature(value: FoldingFeature): PostureFold? {
    val state = when (value.state) { FoldingFeature.State.FLAT -> "flat"; FoldingFeature.State.HALF_OPENED -> "half-opened"; else -> return null }
    val orientation = when (value.orientation) { FoldingFeature.Orientation.VERTICAL -> "vertical"; FoldingFeature.Orientation.HORIZONTAL -> "horizontal"; else -> return null }
    val occlusion = when (value.occlusionType) { FoldingFeature.OcclusionType.NONE -> "none"; FoldingFeature.OcclusionType.FULL -> "full"; else -> return null }
    return PostureFold(bounds(value.bounds), state, orientation, value.isSeparating, occlusion)
  }
  private fun reconcile() {
    val next = reactApplicationContext.currentActivity
    if (closed || !resumed || count == 0 || next == null || next.isFinishing || next.isDestroyed) { retire(); return }
    try {
      val measured = measure(next)
      if (next === host && measured == metrics && callback != null) return
      detach()
      val token = facts.begin(measured)
      host = next; metrics = measured
      changed()
      val adapter = WindowInfoTrackerCallbackAdapter(WindowInfoTracker.getOrCreate(next))
      val consumer = Consumer<WindowLayoutInfo> { info ->
        if (closed || !resumed || count == 0 || !facts.current(token) || next !== host) return@Consumer
        try {
          if (reactApplicationContext.currentActivity !== next || measured != measure(next)) { reconcile(); return@Consumer }
          if (facts.available(token, measured, info.displayFeatures.filterIsInstance<FoldingFeature>().mapNotNull(::feature))) changed()
        } catch (_: Exception) { retire() }
      }
      tracker = adapter; callback = consumer
      decor = next.window.decorView.also { it.addOnLayoutChangeListener(layout) }
      adapter.addWindowLayoutInfoListener(next, ContextCompat.getMainExecutor(next), consumer)
    } catch (_: Exception) { retire() }
  }
  private fun detach() {
    val old = callback; val oldTracker = tracker; val oldDecor = decor
    tracker = null; decor = null; host = null; metrics = null
    callback = null
    // Vendor window extensions may fail during Activity teardown. Retire our ownership first
    // and still remove the decor listener even if removing the vendor callback throws.
    if (old != null) runCatching { oldTracker?.removeWindowLayoutInfoListener(old) }
    oldDecor?.removeOnLayoutChangeListener(layout)
  }
  private fun retire() { detach(); if (facts.clear()) changed() }
  private fun changed() { if (!closed && count > 0) emit(EVENT, wire()) }
  private fun wire() = Arguments.createMap().apply {
    putDouble("revision", facts.revision.toDouble()); putString("status", facts.snapshot.status)
    val window = facts.snapshot.window
    if (window == null) putNull("window") else putMap("window", Arguments.createMap().apply { putDouble("width", window.width); putDouble("height", window.height) })
    putArray("folds", Arguments.createArray().apply {
      facts.snapshot.folds.forEach { fold -> pushMap(Arguments.createMap().apply {
        putString("state", fold.state); putString("orientation", fold.orientation); putBoolean("isSeparating", fold.isSeparating); putString("occlusion", fold.occlusion)
        putMap("bounds", Arguments.createMap().apply { putDouble("left", fold.bounds.left); putDouble("top", fold.bounds.top); putDouble("right", fold.bounds.right); putDouble("bottom", fold.bounds.bottom) })
      }) }
    })
  }
  override fun onHostResume() { ui.post { if (!closed) { resumed = true; reconcile() } } }
  override fun onHostPause() { ui.post { resumed = false; retire() } }
  override fun onHostDestroy() { ui.post { resumed = false; retire() } }
  override fun onConfigurationChanged(configuration: Configuration) { ui.post { if (!closed) reconcile() } }
  override fun onLowMemory() {}
  override fun invalidate() {
    reactApplicationContext.removeLifecycleEventListener(this)
    reactApplicationContext.unregisterComponentCallbacks(this)
    ui.post { closed = true; count = 0; retire() }
    super.invalidate()
  }
  private companion object { const val EVENT = "PorticoPostureChanged" }
}
