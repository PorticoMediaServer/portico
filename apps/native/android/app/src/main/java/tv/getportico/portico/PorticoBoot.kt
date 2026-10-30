package tv.getportico.portico

import android.content.Context
import android.graphics.Canvas
import android.graphics.ColorFilter
import android.graphics.Matrix
import android.graphics.Paint
import android.graphics.PixelFormat
import android.graphics.RadialGradient
import android.graphics.Shader
import android.graphics.drawable.Drawable
import android.os.Process
import android.os.SystemClock
import android.provider.Settings
import android.view.Gravity
import android.view.View
import android.view.ViewGroup
import android.view.ViewTreeObserver
import android.view.animation.LinearInterpolator
import android.view.animation.PathInterpolator
import android.widget.FrameLayout
import android.widget.ImageView
import androidx.core.splashscreen.SplashScreen
import com.facebook.react.bridge.UiThreadUtil
import kotlin.math.roundToInt

/**
 * The first frame, seamlessly (DESIGN "The first frame, seamlessly"). The system's first frame is
 * ink, with the white wordmark where the system can draw it (phones and tablets; a television's
 * splash is the colour alone). [PorticoBootView] then draws on its own first frame, beneath
 * nothing and before the JavaScript loads: the same ink, the standard backdrop fading in over it
 * in 320 ms, and the wordmark in the system's place (on a television, fading in with the
 * backdrop). React's Boot draws the same pixels; when it reports its first frame the view is
 * removed without animation.
 */
internal object PorticoBoot {
  private var view: PorticoBootView? = null
  private var drawn = false
  @Volatile private var handedOver = false

  /** When the person started the app: the process start on a cold launch, otherwise this activity. */
  @Volatile var startedAt: Long = System.currentTimeMillis()
    private set

  /** Before `super.onCreate`: the system splash stays until the boot view has drawn. */
  fun keep(splash: SplashScreen) {
    val sinceProcess = SystemClock.elapsedRealtime() - Process.getStartElapsedRealtime()
    if (view == null && !handedOver) startedAt = System.currentTimeMillis() - if (sinceProcess in 0..10_000) sinceProcess else 0
    splash.setKeepOnScreenCondition { !drawn && !handedOver }
    splash.setOnExitAnimationListener { it.remove() }
  }

  /** After `super.onCreate` has set React's root: the boot view goes over it, once per process. */
  fun attach(activity: MainActivity) {
    if (handedOver || view != null) return
    val systemDrewWordmark = activity.resources.getBoolean(R.bool.portico_splash_wordmark)
    val boot = PorticoBootView(activity, TelevisionDisplay.isTelevision, systemDrewWordmark)
    activity.addContentView(boot, ViewGroup.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT))
    view = boot
    boot.viewTreeObserver.addOnPreDrawListener(object : ViewTreeObserver.OnPreDrawListener {
      override fun onPreDraw(): Boolean {
        boot.viewTreeObserver.removeOnPreDrawListener(this)
        drawn = true
        boot.enter()
        return true
      }
    })
  }

  /** React's first frame is on screen: remove the boot view, without animation. */
  fun reactDrew() {
    handedOver = true
    UiThreadUtil.runOnUiThread {
      view?.let { (it.parent as? ViewGroup)?.removeView(it) }
      view = null
    }
  }
}

/** Ink, the standard backdrop and the wordmark, drawn natively at React Boot's sizes. */
internal class PorticoBootView(context: Context, television: Boolean, systemDrewWordmark: Boolean) : FrameLayout(context) {
  private val density = resources.displayMetrics.density
  // React's Boot uses the top band on a phone-width window and the full backdrop elsewhere.
  private val band = !television && resources.configuration.screenWidthDp < 600
  private val backdrop = View(context).apply { background = BootBackdrop(band); alpha = 0f }
  // A television's wordmark is sized for the room (640 on the 1920 design canvas) and can't be
  // the system's splash icon, so it arrives with the backdrop; elsewhere it is already drawn.
  private val fadesWordmark = television || !systemDrewWordmark
  private val wordmark = ImageView(context).apply {
    setImageResource(R.drawable.portico_wordmark)
    scaleType = ImageView.ScaleType.FIT_CENTER
    alpha = if (fadesWordmark) 0f else 1f
  }

  init {
    setBackgroundResource(R.color.portico_ink)
    isFocusable = false
    descendantFocusability = FOCUS_BLOCK_DESCENDANTS
    contentDescription = context.getString(R.string.app_name)
    addView(backdrop, LayoutParams(LayoutParams.MATCH_PARENT, if (band) (560 * density).roundToInt() else LayoutParams.MATCH_PARENT, Gravity.TOP))
    val width = (if (television) 640 else 176) * density
    addView(wordmark, LayoutParams(width.roundToInt(), (width * 405 / 1774).roundToInt(), Gravity.CENTER))
  }

  /** The entrance: 320 ms `out` (motion.html), or 160 ms linear when motion is reduced. */
  fun enter() {
    val reduced = Settings.Global.getFloat(context.contentResolver, Settings.Global.TRANSITION_ANIMATION_SCALE, 1f) == 0f
    val targets = if (fadesWordmark) listOf(backdrop, wordmark) else listOf(backdrop)
    for (target in targets) target.animate().alpha(1f)
        .setDuration(if (reduced) 160L else 320L)
        .setInterpolator(if (reduced) LinearInterpolator() else PathInterpolator(0.2f, 0.8f, 0.2f, 1f))
        .start()
  }
}

/**
 * The standard backdrop, the same glows as `ColourBackdrop` (src-next/kit/artwork/Surfaces.tsx):
 * elliptical radial gradients placed in fractions of the drawn area, the first glow on top.
 */
private class BootBackdrop(band: Boolean) : Drawable() {
  private class Glow(val x: Float, val y: Float, val rx: Float, val ry: Float, val colour: Int, val stops: List<Pair<Float, Float>>)

  private val glows = if (band) listOf(
      Glow(0.96f, -0.06f, 0.9f, 0.54f, ACCENT_DEEP, listOf(0f to 0.5f, 0.48f to 0.15f, 0.76f to 0f)),
      Glow(-0.08f, 0.3f, 0.84f, 0.44f, GENERATED_GREEN, listOf(0f to 0.5f, 0.52f to 0.13f, 0.78f to 0f)),
      Glow(0.3f, 0f, 0.7f, 0.4f, AMBIENT, listOf(0f to 0.8f, 0.72f to 0f)),
  ) else listOf(
      Glow(0.86f, 0.04f, 0.44f, 0.66f, ACCENT_DEEP, listOf(0f to 0.46f, 0.46f to 0.15f, 0.74f to 0f)),
      Glow(0.64f, 1.04f, 0.42f, 0.58f, GENERATED_GREEN, listOf(0f to 0.6f, 0.5f to 0.18f, 0.76f to 0f)),
      Glow(0.04f, 0f, 0.38f, 0.5f, AMBIENT, listOf(0f to 0.75f, 0.72f to 0f)),
  )
  private val paint = Paint(Paint.ANTI_ALIAS_FLAG).apply { isDither = true }

  override fun draw(canvas: Canvas) {
    val area = bounds
    val width = area.width().toFloat()
    val height = area.height().toFloat()
    if (width <= 0f || height <= 0f) return
    for (glow in glows.asReversed()) {
      val colours = IntArray(glow.stops.size) { (glow.stops[it].second * 255).roundToInt() shl 24 or (glow.colour and 0xFFFFFF) }
      val positions = FloatArray(glow.stops.size) { glow.stops[it].first }
      paint.shader = RadialGradient(0f, 0f, 1f, colours, positions, Shader.TileMode.CLAMP).apply {
        setLocalMatrix(Matrix().apply {
          setScale(glow.rx * width, glow.ry * height)
          postTranslate(area.left + glow.x * width, area.top + glow.y * height)
        })
      }
      canvas.drawRect(area, paint)
    }
  }

  override fun setAlpha(alpha: Int) {}
  override fun setColorFilter(colorFilter: ColorFilter?) {}
  @Deprecated("Deprecated in Java")
  override fun getOpacity(): Int = PixelFormat.TRANSLUCENT

  private companion object {
    // packages/design: accentDeep, generatedGreen, ambient.
    const val ACCENT_DEEP = 0x2F86BB
    const val GENERATED_GREEN = 0x1E5B52
    const val AMBIENT = 0x153749
  }
}
