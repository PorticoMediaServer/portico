package tv.getportico.portico.native

import android.graphics.Color
import android.graphics.Typeface
import android.util.TypedValue
import android.view.Gravity
import android.view.SurfaceHolder
import android.view.SurfaceView
import android.view.View
import android.widget.FrameLayout
import android.widget.TextView
import com.facebook.react.bridge.Arguments
import com.facebook.react.uimanager.SimpleViewManager
import com.facebook.react.uimanager.ThemedReactContext
import com.facebook.react.uimanager.annotations.ReactProp

/**
 * The mpv engine's picture, for the lease it is given: the native view `PorticoMpvPlayer`
 * (`lease`, `pictureInPictureRequest`).
 *
 * The engine outlives its views. The view owns a surface and lends it to the engine only while
 * its lease is the engine's current one; it takes the surface back the moment the lease changes,
 * the view leaves the window or Android destroys the surface. The surface is laid out at the
 * picture's own shape inside the view, so the picture is never stretched whichever way it is
 * drawn, and the plain-text subtitle line (when the app, not mpv, draws the chosen track) sits
 * inside the picture exactly as the Media3 engine's does.
 */
internal class PorticoMpvPlayerView(private val reactContext: ThemedReactContext) : FrameLayout(reactContext), SurfaceHolder.Callback {
  private val surface = SurfaceView(reactContext)
  private val subtitle = TextView(reactContext)
  private var lease = ""
  private var surfaceActive = true
  private var request = 0
  private var listening = false
  /** The surface exists (between `surfaceCreated` and `surfaceDestroyed`). */
  private var surfaceReady = false
  /** The engine has this view's surface. */
  private var lent = false
  private var fixedWidth = 0
  private var fixedHeight = 0
  private val refresh: () -> Unit = { attach() }
  private val redraw: () -> Unit = { update() }

  init {
    setBackgroundColor(Color.TRANSPARENT)
    surface.visibility = View.GONE
    addView(surface)
    surface.holder.addCallback(this)
    subtitle.setTextColor(Color.WHITE)
    subtitle.setBackgroundColor(0xB3000000.toInt())
    subtitle.gravity = Gravity.CENTER
    subtitle.visibility = View.GONE
    subtitle.importantForAccessibility = View.IMPORTANT_FOR_ACCESSIBILITY_YES
    addView(subtitle)
  }

  fun setLease(value: String?) {
    lease = value ?: ""
    attach()
  }

  /** Producer visibility can change without remounting the view or its playback owner. */
  fun setSurfaceActive(value: Boolean) {
    if (surfaceActive == value) return
    surfaceActive = value
    attach()
  }

  fun snapshotTarget(expected: Map<String, Any?>): PorticoSnapshotTarget? {
    val controller = PorticoMpvController.shared ?: return null
    val token = controller.snapshotIdentity(this, surface.holder.surface, expected) ?: return null
    fun current() = PorticoMpvController.shared === controller && isAttachedToWindow && isShown && surfaceActive && lent && surfaceReady &&
        expected["lease"] == lease && leased(controller) && controller.snapshotIdentity(this, surface.holder.surface, expected) == token
    return if (current()) PorticoSnapshotTarget(surface, ::current) else null
  }

  /**
   * mpv has no Picture in Picture. A request is answered "unavailable", as the Media3 view
   * answers on a device without it, so the player hides the button.
   */
  fun setPictureInPictureRequest(value: Int) {
    if (value == request) return
    request = value
    PorticoMpvPlaybackModule.pictureInPicture(Arguments.createMap().apply { putString("lease", lease); putInt("request", value); putString("state", "unavailable") })
  }

  private fun leased(controller: PorticoMpvController?): Boolean = controller != null && lease.isNotEmpty() && lease == controller.lease

  private fun listen(controller: PorticoMpvController?) {
    if (controller == null || listening || !isAttachedToWindow) return
    listening = true
    controller.attachmentListeners.add(refresh)
    controller.overlayListeners.add(redraw)
  }

  private fun attach() {
    val controller = PorticoMpvController.shared
    listen(controller)
    val mine = leased(controller) && isAttachedToWindow
    val showing = mine && surfaceActive
    // SurfaceView composition must be retired directly; ancestor alpha is insufficient.
    surface.visibility = if (showing) View.VISIBLE else View.GONE
    setBackgroundColor(if (showing) Color.BLACK else Color.TRANSPARENT)
    if (showing && surfaceReady && surface.holder.surface.isValid) {
      val frame = surface.holder.surfaceFrame
      controller!!.surfaceAvailable(this, surface.holder.surface, frame.width(), frame.height())
      lent = true
    } else if (lent) {
      lent = false
      controller?.surfaceGone(this)
    }
    keepScreenOn = showing && controller?.paused == false
    update()
  }

  private fun update() {
    val controller = PorticoMpvController.shared
    val cue = if (surfaceActive && isAttachedToWindow && leased(controller)) controller?.overlayCue else null
    val text = cue?.text ?: ""
    subtitle.text = text
    subtitle.visibility = if (text.isEmpty()) View.GONE else View.VISIBLE
    place()
  }

  /** React Native lays this view out but not its children: they are placed here. */
  private fun place() {
    val width = width
    val height = height
    if (width <= 0 || height <= 0) return
    val controller = PorticoMpvController.shared
    val aspect = if (leased(controller)) controller?.videoAspect else null
    var w = width
    var h = height
    if (aspect != null && aspect > 0) {
      if (width.toDouble() / height > aspect) w = Math.round(height * aspect).toInt() else h = Math.round(width / aspect).toInt()
    }
    w = w.coerceAtLeast(2)
    h = h.coerceAtLeast(2)
    val left = (width - w) / 2
    val top = (height - h) / 2
    // A 4K title on a 4K television whose interface is 1080p is drawn at the panel's size.
    val larger = if (leased(controller)) controller?.renderSize(w, h) else null
    if (larger != null) {
      if (larger.first != fixedWidth || larger.second != fixedHeight) {
        fixedWidth = larger.first
        fixedHeight = larger.second
        surface.holder.setFixedSize(fixedWidth, fixedHeight)
      }
    } else if (fixedWidth != 0) {
      fixedWidth = 0
      fixedHeight = 0
      surface.holder.setSizeFromLayout()
    }
    surface.measure(MeasureSpec.makeMeasureSpec(w, MeasureSpec.EXACTLY), MeasureSpec.makeMeasureSpec(h, MeasureSpec.EXACTLY))
    surface.layout(left, top, left + w, top + h)
    if (subtitle.visibility != View.VISIBLE) return
    val density = resources.displayMetrics.density
    val line = (w * 0.9f).toInt()
    // Portico's caption style (size and background) on top of the system's own caption settings.
    PorticoMpvSupport.styleCaptionLine(subtitle, context, controller?.captionStyle, Math.max(12 * density, Math.min(40 * density, w * 0.035f)), controller?.overlayCue?.italic == true)
    subtitle.measure(MeasureSpec.makeMeasureSpec(line, MeasureSpec.EXACTLY), MeasureSpec.makeMeasureSpec((h * 0.6f).toInt(), MeasureSpec.AT_MOST))
    val textHeight = subtitle.measuredHeight
    val x = left + (w - line) / 2
    val y = if (PorticoMpvSupport.captionAtTop(controller?.captionStyle, controller?.overlayCue?.top == true)) top + (8 * density).toInt()
      else top + h - Math.max((8 * density).toInt(), (h * PorticoMpvSupport.captionBottomInset(controller?.captionStyle)).toInt()) - textHeight
    subtitle.layout(x, y, x + line, y + textHeight)
  }

  // --- the surface -----------------------------------------------------------------------

  override fun surfaceCreated(holder: SurfaceHolder) {
    surfaceReady = true
    attach()
  }

  override fun surfaceChanged(holder: SurfaceHolder, format: Int, width: Int, height: Int) {
    if (lent) PorticoMpvController.shared?.surfaceChanged(this, width, height)
  }

  /** Nothing may draw on the surface once this returns: the engine gives it back first. */
  override fun surfaceDestroyed(holder: SurfaceHolder) {
    surfaceReady = false
    if (lent) {
      lent = false
      PorticoMpvController.shared?.surfaceGone(this)
    }
  }

  // --- the view --------------------------------------------------------------------------

  override fun onAttachedToWindow() {
    super.onAttachedToWindow()
    attach()
  }

  override fun onDetachedFromWindow() {
    if (listening) {
      listening = false
      PorticoMpvController.shared?.let { it.attachmentListeners.remove(refresh); it.overlayListeners.remove(redraw) }
    }
    if (lent) {
      lent = false
      PorticoMpvController.shared?.surfaceGone(this)
    }
    surface.visibility = View.GONE
    setBackgroundColor(Color.TRANSPARENT)
    subtitle.text = ""
    subtitle.visibility = View.GONE
    keepScreenOn = false
    super.onDetachedFromWindow()
  }

  override fun onLayout(changed: Boolean, left: Int, top: Int, right: Int, bottom: Int) = place()

  override fun requestLayout() {
    super.requestLayout()
    if (surfaceActive) post {
      if (isAttachedToWindow && surfaceActive) {
        measure(MeasureSpec.makeMeasureSpec(width, MeasureSpec.EXACTLY), MeasureSpec.makeMeasureSpec(height, MeasureSpec.EXACTLY))
        layout(left, top, right, bottom)
      }
    }
  }
}

class PorticoMpvPlayerManager : SimpleViewManager<FrameLayout>() {
  override fun getName(): String = "PorticoMpvPlayer"

  override fun createViewInstance(context: ThemedReactContext): FrameLayout = PorticoMpvPlayerView(context)

  @ReactProp(name = "lease")
  fun setLease(view: FrameLayout, lease: String?) = (view as PorticoMpvPlayerView).setLease(lease)

  @ReactProp(name = "surfaceActive", defaultBoolean = true)
  fun setSurfaceActive(view: FrameLayout, active: Boolean) = (view as PorticoMpvPlayerView).setSurfaceActive(active)

  @ReactProp(name = "pictureInPictureRequest", defaultInt = 0)
  fun setPictureInPictureRequest(view: FrameLayout, request: Int) = (view as PorticoMpvPlayerView).setPictureInPictureRequest(request)
}
