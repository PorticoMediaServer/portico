package tv.getportico.portico.native

import android.graphics.Color
import android.graphics.Matrix
import android.graphics.Outline
import android.graphics.Rect
import android.graphics.RectF
import android.graphics.drawable.ColorDrawable
import android.os.Build
import android.text.Spanned
import android.text.TextPaint
import android.text.style.CharacterStyle
import android.text.style.ReplacementSpan
import android.view.Gravity
import android.view.View
import android.view.ViewGroup
import android.widget.EditText
import android.widget.ImageView
import android.widget.TextView
import com.facebook.drawee.drawable.ScalingUtils
import com.facebook.drawee.view.GenericDraweeView
import com.facebook.react.bridge.Arguments
import com.facebook.react.bridge.Promise
import com.facebook.react.bridge.ReactApplicationContext
import com.facebook.react.bridge.ReactContextBaseJavaModule
import com.facebook.react.bridge.ReactMethod
import com.facebook.react.bridge.ReadableArray
import com.facebook.react.bridge.ReadableType
import com.facebook.react.bridge.UiThreadUtil
import com.facebook.react.uimanager.UIManagerHelper
import tv.getportico.portico.BuildConfig
import java.util.ArrayDeque

/** No app state, strings, props, assets or image bytes cross this DEBUG-only boundary. */
class PorticoLayoutObservation(context: ReactApplicationContext) : ReactContextBaseJavaModule(context) {
  @Volatile private var retired = false
  override fun getName() = "PorticoLayoutObservation"
  override fun invalidate() { retired = true; super.invalidate() }

  @ReactMethod
  fun observe(rootReactTag: Double, fixtureReactTag: Double, hostReactTags: ReadableArray, promise: Promise) {
    val tags = try {
      if (hostReactTags.size() > LayoutObservationPolicy.MAX_HOSTS) null else (0 until hostReactTags.size()).map {
        if (hostReactTags.getType(it) != ReadableType.Number) return@map Double.NaN
        hostReactTags.getDouble(it)
      }
    } catch (_: Exception) { null }
    val activity = reactApplicationContext.currentActivity
    val intent = activity?.intent
    if (retired || activity == null || intent == null || tags == null || !LayoutObservationPolicy.admitted(
        BuildConfig.DEBUG, intent.action, intent.dataString, rootReactTag, fixtureReactTag, tags)) {
      rejectScope(promise, LayoutObservationPolicy.Stage.ADMISSION); return
    }
    UiThreadUtil.runOnUiThread {
      var stage = LayoutObservationPolicy.Stage.UI_CURRENT
      try {
        fun intentCurrent() = !retired && reactApplicationContext.currentActivity === activity && activity.intent === intent &&
          LayoutObservationPolicy.publicIntent(BuildConfig.DEBUG, intent.action, intent.dataString) && !activity.isFinishing && !activity.isDestroyed
        if (!intentCurrent()) { rejectScope(promise, stage); return@runOnUiThread }
        // Only this captured root is resolved by RN. All other IDs are found below it.
        stage = LayoutObservationPolicy.Stage.ROOT_RESOLVE
        val root = UIManagerHelper.getUIManagerForReactTag(reactApplicationContext, rootReactTag.toInt())?.resolveView(rootReactTag.toInt())
        if (root == null) { rejectScope(promise, stage); return@runOnUiThread }
        stage = LayoutObservationPolicy.Stage.ROOT_ATTACHMENT
        val decor = activity.window?.decorView
        if (root.id != rootReactTag.toInt() || decor == null || !root.isAttachedToWindow || root.windowToken == null ||
            root.windowToken != decor.windowToken || !descendant(root, decor)) {
          rejectScope(promise, stage); return@runOnUiThread
        }
        stage = LayoutObservationPolicy.Stage.ROOT_WALK
        val window = root.windowToken
        val requested = tags.map { it.toInt() }
        val wanted = (requested + fixtureReactTag.toInt()).toSet()
        val found = LayoutObservationPolicy.collect(root, wanted, { it.id }, { if (it is ViewGroup) it.childCount else 0 },
          { view, index -> (view as ViewGroup).getChildAt(index) })
        if (found == null) { rejectScope(promise, stage); return@runOnUiThread }
        stage = LayoutObservationPolicy.Stage.PRE_OBSERVATION_SCOPE
        val fixture = found[fixtureReactTag.toInt()]
        fun scopeFailure() = LayoutObservationPolicy.scopeFailure { check -> when (check) {
          LayoutObservationPolicy.ScopeCheck.ACTIVITY_INTENT_CURRENT -> intentCurrent()
          LayoutObservationPolicy.ScopeCheck.ROOT_TAG_CURRENT -> root.id == rootReactTag.toInt()
          LayoutObservationPolicy.ScopeCheck.ROOT_ATTACHED -> root.isAttachedToWindow
          LayoutObservationPolicy.ScopeCheck.DECOR_ATTACHED -> decor.isAttachedToWindow
          LayoutObservationPolicy.ScopeCheck.DECOR_CURRENT -> activity.window?.decorView === decor
          LayoutObservationPolicy.ScopeCheck.ROOT_WINDOW_CURRENT -> root.windowToken == window
          LayoutObservationPolicy.ScopeCheck.DECOR_WINDOW_CURRENT -> decor.windowToken == window
          LayoutObservationPolicy.ScopeCheck.ROOT_IN_DECOR -> descendant(root, decor)
          LayoutObservationPolicy.ScopeCheck.FIXTURE_FOUND -> fixture != null
          LayoutObservationPolicy.ScopeCheck.FIXTURE_TAG_CURRENT -> fixture != null && fixture.id == fixtureReactTag.toInt()
          LayoutObservationPolicy.ScopeCheck.FIXTURE_ATTACHED -> fixture != null && fixture.isAttachedToWindow
          LayoutObservationPolicy.ScopeCheck.FIXTURE_WINDOW_CURRENT -> fixture != null && fixture.windowToken == window
          LayoutObservationPolicy.ScopeCheck.FIXTURE_IN_ROOT -> fixture != null && descendant(fixture, root)
        } }
        val before = scopeFailure()
        if (before != null) { rejectScope(promise, stage, check = before); return@runOnUiThread }
        stage = LayoutObservationPolicy.Stage.OBSERVATION
        val budget = LayoutObservationPolicy.TextBudget()
        val hosts = requested.map { tag ->
          val view = found[tag]
          val reason = when {
            view == null -> "host-not-found"
            !view.isAttachedToWindow -> "host-detached"
            !descendant(view, root) -> "host-outside-root"
            view.windowToken != window -> "host-window-mismatch"
            input(view, root) -> "input-opaque"
            else -> null
          }
          val observed = if (reason != null) unavailableFacts(reason) else try { facts(view!!, decor, budget) }
            catch (_: Exception) { unavailableFacts("paint-unprovable") }
          mapOf("reactTag" to tag, "facts" to if (view != null && (view.id != tag || !view.isAttachedToWindow ||
            view.windowToken != window || !descendant(view, root))) unavailableFacts("root-changed") else observed)
        }
        stage = LayoutObservationPolicy.Stage.POST_OBSERVATION_SCOPE
        val after = scopeFailure()
        if (after != null) { rejectScope(promise, stage, check = after); return@runOnUiThread }
        stage = LayoutObservationPolicy.Stage.RESPONSE
        promise.resolve(Arguments.makeNativeMap(mapOf("version" to 1, "unit" to "dp", "rootReactTag" to rootReactTag.toInt(),
          "fixtureReactTag" to fixtureReactTag.toInt(), "hosts" to hosts)))
      } catch (_: Exception) {
        // Neither target exception strings nor native paths are diagnostics for this public fixture.
        rejectScope(promise, stage, exception = true)
      }
    }
  }

  private fun rejectScope(promise: Promise, stage: LayoutObservationPolicy.Stage, exception: Boolean = false, check: LayoutObservationPolicy.ScopeCheck? = null) {
    promise.reject("layout_observation_scope", "layout_observation_scope", Arguments.makeNativeMap(LayoutObservationPolicy.failure(stage, exception, check)))
  }

  private fun facts(view: View, decor: View, budget: LayoutObservationPolicy.TextBudget): Map<String, Any> {
    val result = unavailableFacts("paint-unprovable").toMutableMap()
    val density = view.resources.displayMetrics.density.toDouble()
    if (!density.isFinite() || density <= 0) return result
    result["isFocused"] = value(view.isFocused, "android.View.isFocused")
    val coordinates = windowCoordinates(view, decor)
    result["windowRect"] = coordinates?.rect(RectF(0f, 0f, view.width.toFloat(), view.height.toFloat()), density)?.let {
      value(it, "android.View.transformMatrixToGlobal+getLocationInWindow.boundsDp")
    } ?: missing("unsupported-transform")
    val chain = ancestry(view, decor) ?: return result
    val alpha = chain.map { if (Build.VERSION.SDK_INT >= 29 && it.layerType == View.LAYER_TYPE_NONE &&
      (it.animation == null || it.animation.hasEnded())) it.alpha.toDouble() * it.transitionAlpha.toDouble() else Double.NaN }
    LayoutObservationPolicy.opacity(alpha)?.let { result["effectiveOpacity"] = value(it, "android.View.alpha+transitionAlpha.decorAncestry") }
    val backgrounds = chain.map(::background)
    backgrounds.first()?.let { result["backgroundColor"] = value(LayoutObservationPolicy.rgba(it), if (view.background == null) "android.View.background.none" else "android.ColorDrawable.color+alpha") }
      ?: run { result["backgroundColor"] = missing("unsupported-background") }
    if (backgrounds.all { it != null } && LayoutObservationPolicy.opacity(alpha) != null) {
      val composite = LayoutObservationPolicy.background(backgrounds.map { it!! }, alpha)
      result["effectiveBackgroundColor"] = value(LayoutObservationPolicy.rgba(composite), "android.ColorDrawable+View.alpha.decorAncestry")
    } else result["effectiveBackgroundColor"] = missing("unsupported-background")
    result["borderRadius"] = radius(view, density)
    // Elevation/light geometry and RN shadows have no public exact CSS-equivalent paint getter.
    result["boxShadow"] = missing("unsupported-shadow")
    image(view).forEach { (key, observation) -> result[key] = observation }
    if (view !is TextView) {
      TEXT_FIELDS.forEach { result[it] = missing("not-text") }; return result
    }
    text(view, decor, density, budget).forEach { (key, observation) -> result[key] = observation }
    return result
  }

  private fun radius(view: View, density: Double): Map<String, Any> {
    if (!view.clipToOutline || view.outlineProvider == null) return missing("unsupported-outline")
    val outline = Outline(); view.outlineProvider.getOutline(view, outline)
    val rect = Rect()
    if (!outline.getRect(rect) || rect != Rect(0, 0, view.width, view.height) || !outline.radius.isFinite() || outline.radius < 0) return missing("unsupported-outline")
    return value(outline.radius / density, "android.ViewOutlineProvider+Outline.getRadius")
  }

  private fun image(view: View): Map<String, Any> {
    if (view !is ImageView) return IMAGE_FIELDS.associateWith { missing("not-image") }
    val result = IMAGE_FIELDS.associateWith { missing("image-state-unavailable") }.toMutableMap()
    try {
      if (view is GenericDraweeView) {
        if (!view.hasHierarchy()) return result
        val hierarchy = view.hierarchy
        // Presence only: intermediate/retained/fading actual images can satisfy hasImage.
        // It is NOT request completion, source identity, visibility, or canonical loaded.
        result["imageHasImage"] = value(hierarchy.hasImage(), "android.fresco.GenericDraweeHierarchy.hasImage")
        val mechanism = LayoutObservationPolicy.imageMechanism(hierarchy.actualImageScaleType, FRESCO_SCALES)
        result["imageScaleType"] = mechanism?.let { value(it, "android.fresco.GenericDraweeHierarchy.getActualImageScaleType") }
          ?: missing("unsupported-image-scale")
      } else {
        // A standard ImageView's non-null drawable may be a placeholder/transition;
        // its public scale mechanism is observable without claiming image presence.
        val mechanism = LayoutObservationPolicy.imageMechanism(view.scaleType, IMAGE_VIEW_SCALES)
        result["imageScaleType"] = mechanism?.let { value(it, "android.widget.ImageView.getScaleType") }
          ?: missing("unsupported-image-scale")
      }
    } catch (_: Exception) { /* Preserve only already observed fields; no error details. */ }
    return result
  }

  private fun text(view: TextView, decor: View, density: Double, budget: LayoutObservationPolicy.TextBudget): Map<String, Any> {
    val result = TEXT_FIELDS.associateWith { missing("paint-unprovable") }.toMutableMap()
    val layout = view.layout ?: return TEXT_FIELDS.associateWith { missing("unsupported-text-layout") }
    val boundedLines = layout.lineCount <= LayoutObservationPolicy.MAX_LINES && budget.lines(layout.lineCount)
    val ellipsized = boundedLines && (0 until layout.lineCount).any { layout.getEllipsisCount(it) != 0 }
    val geometryReady = !view.isLayoutRequested && boundedLines && !ellipsized
    // Layout's public span metadata is consulted without reading/subsequencing/serializing characters.
    // Reapply the same draw-state spans to detached TextPaints; base TextView defaults alone are not run paint.
    val content = layout.text
    val spanned = content as? Spanned
    val paints = mutableListOf<TextPaint>()
    val basePaint = TextPaint(layout.paint); basePaint.color = view.currentTextColor; basePaint.drawableState = view.drawableState
    view.linkTextColors?.let { basePaint.linkColor = it.getColorForState(view.drawableState, it.defaultColor) }
    var start = 0
    while (start < content.length) {
      if (paints.size >= LayoutObservationPolicy.MAX_RUNS || !budget.run()) return TEXT_FIELDS.associateWith { missing("unsupported-text-layout") }
      val end = spanned?.nextSpanTransition(start, content.length, CharacterStyle::class.java) ?: content.length
      if (end <= start) return result
      val paint = TextPaint(basePaint)
      val spans = if (spanned == null) emptyList() else spanned.getSpans(start, end, CharacterStyle::class.java).filter {
        spanned.getSpanStart(it) < end && spanned.getSpanEnd(it) > start
      }
      if (spans.any { it is ReplacementSpan }) return TEXT_FIELDS.associateWith { missing("unsupported-text-layout") }
      for (span in spans) span.updateDrawState(paint)
      paints.add(paint); start = end
    }
    // Geometry can be unsupported while actual run paint is uniformly provable.
    // Where ellipsis is possible, require the native base paint to agree too;
    // never guess which run/base style the synthesized glyph would use.
    if (paints.isNotEmpty() && (!boundedLines || ellipsized)) paints.add(basePaint)
    LayoutObservationPolicy.uniform(paints.map { if (it.shader == null && it.colorFilter == null) it.color else null })?.let {
      result["textColor"] = value(LayoutObservationPolicy.rgba(it), "android.Layout.TextPaint+CharacterStyle")
    }
    LayoutObservationPolicy.uniform(paints.map { it.textSize.toDouble().takeIf { size -> size.isFinite() && size > 0 } })?.let {
      result["fontSize"] = value(it / density, "android.Layout.TextPaint.textSize+MetricAffectingSpan")
    }
    if (Build.VERSION.SDK_INT >= 28) {
      LayoutObservationPolicy.uniform(paints.map { paint -> paint.typeface?.weight?.takeUnless { paint.isFakeBoldText } })?.let {
        result["fontWeight"] = value(it, "android.Layout.TextPaint.Typeface.getWeight")
      }
    }
    LayoutObservationPolicy.uniform(paints.map { LayoutObservationPolicy.fontStyle(it.typeface?.isItalic, it.textSkewX.toDouble()) })?.let {
      result["fontStyle"] = value(it, "android.graphics.Typeface.isItalic")
    }
    LayoutObservationPolicy.uniform(paints.map { LayoutObservationPolicy.letterSpacing(it.letterSpacing.toDouble(), it.textSize.toDouble(), density) })?.let {
      result["letterSpacing"] = value(it, "android.graphics.Paint.getLetterSpacing")
    }
    LayoutObservationPolicy.uniform(paints.map { LayoutObservationPolicy.decoration(it.isUnderlineText, it.isStrikeThruText) })?.let {
      result["textDecorationLine"] = value(it, "android.graphics.Paint.getFlags")
    }
    result["textShadow"] = missing("unsupported-shadow")
    if (Build.VERSION.SDK_INT >= 29 && paints.isNotEmpty() && (paints + basePaint + layout.paint + view.paint).all {
        LayoutObservationPolicy.noTextShadow(it.shadowLayerRadius.toDouble(), it.maskFilter == null, it.colorFilter == null)
      }) result["textShadow"] = value("none", "android.graphics.Paint.getShadowLayerRadius")
    // No public run-paint mechanism proves text transformation. Do not inspect
    // characters, requested styles, or transformation methods to guess one.
    result["textTransform"] = missing("paint-unprovable")
    // A Typeface system label does not establish the associated shaped resource's
    // typographic name (name ID 16, else ID 1). No resource/name-table association
    // is currently observable here, so neither labels nor requested families qualify.
    result["fontFamily"] = missing("font-family-unavailable")
    if (!geometryReady) {
      result["lineHeight"] = missing("unsupported-text-layout")
      result["textLineRects"] = missing("unsupported-text-layout")
      return result
    }
    val heights = (0 until layout.lineCount).map { layout.getLineBottom(it) - layout.getLineTop(it) }.distinct()
    if (heights.size == 1 && heights.single() > 0) result["lineHeight"] = value(heights.single() / density, "android.Layout.getLineTop+getLineBottom")
    result["textLineRects"] = missing("unsupported-transform")
    if (Build.VERSION.SDK_INT < 29 || ancestry(view, decor)?.any { it.animation != null && !it.animation.hasEnded() } != false) return result
    val matrix = Matrix(); view.transformMatrixToGlobal(matrix)
    val decorMatrix = Matrix(); decor.transformMatrixToGlobal(decorMatrix)
    val globalOrigin = floatArrayOf(0f, 0f); decorMatrix.mapPoints(globalOrigin)
    val windowOrigin = IntArray(2); decor.getLocationInWindow(windowOrigin)
    val dx = windowOrigin[0] - globalOrigin[0]; val dy = windowOrigin[1] - globalOrigin[1]
    val available = view.height - view.extendedPaddingTop - view.extendedPaddingBottom
    if (view.scrollX != 0 || view.scrollY != 0 || layout.lineCount > view.maxLines || layout.height > available)
      return result.apply { this["textLineRects"] = missing("unsupported-text-layout") }
    val vertical = when (view.gravity and Gravity.VERTICAL_GRAVITY_MASK) {
      Gravity.BOTTOM -> (available - layout.height).coerceAtLeast(0)
      Gravity.CENTER_VERTICAL -> (available - layout.height).coerceAtLeast(0) / 2
      else -> 0
    }
    val x = view.compoundPaddingLeft - view.scrollX
    val y = view.extendedPaddingTop + vertical - view.scrollY
    val rects = (0 until layout.lineCount).map { line ->
      val rect = RectF(layout.getLineLeft(line) + x, (layout.getLineTop(line) + y).toFloat(), layout.getLineRight(line) + x, (layout.getLineBottom(line) + y).toFloat())
      matrix.mapRect(rect); rect.offset(dx, dy)
      mapOf("x" to rect.left / density, "y" to rect.top / density, "width" to rect.width() / density, "height" to rect.height() / density)
    }
    if (rects.all { rect -> rect.values.all { it.isFinite() } && rect.getValue("width") >= 0 && rect.getValue("height") >= 0 })
      result["textLineRects"] = value(rects, "android.Layout.lineBounds+View.transformMatrixToGlobal.windowDp")
    return result
  }

  private class WindowCoordinates(val matrix: Matrix, val mappedOrigin: FloatArray, val nativeWindowOrigin: IntArray) {
    fun rect(local: RectF, density: Double): Map<String, Double>? {
      matrix.mapRect(local)
      return LayoutObservationPolicy.windowRect(doubleArrayOf(local.left.toDouble(), local.top.toDouble(), local.right.toDouble(), local.bottom.toDouble()),
        doubleArrayOf(mappedOrigin[0].toDouble(), mappedOrigin[1].toDouble()),
        doubleArrayOf(nativeWindowOrigin[0].toDouble(), nativeWindowOrigin[1].toDouble()), density)
    }
  }

  private fun windowCoordinates(view: View, decor: View): WindowCoordinates? {
    if (Build.VERSION.SDK_INT < 29 || view.isLayoutRequested || ancestry(view, decor)?.any {
        it.animation != null && !it.animation.hasEnded() || it.animationMatrix?.isIdentity == false
      } != false) return null
    val matrix = Matrix(); view.transformMatrixToGlobal(matrix)
    val mappedOrigin = floatArrayOf(0f, 0f); matrix.mapPoints(mappedOrigin)
    // SAME-view public window origin supplies the native coordinate-space translation.
    // Its integer physical-pixel precision is retained; no zero/reference-fitting origin is chosen.
    val nativeWindowOrigin = IntArray(2); view.getLocationInWindow(nativeWindowOrigin)
    return WindowCoordinates(matrix, mappedOrigin, nativeWindowOrigin)
  }

  private fun background(view: View): Int? {
    val drawable = view.background ?: return Color.TRANSPARENT
    // RN 0.83 CompositeBackgroundDrawable/BackgroundDrawable are intentionally unsupported:
    // actual backgroundPaint is private. backgroundColor is only backing state; setAlpha
    // changes the paint independently without exposing its resolved alpha via getAlpha.
    // Unwrapping props or reflecting that private Paint would not be an observation API.
    if (drawable !is ColorDrawable || drawable.colorFilter != null) return null
    return (drawable.color and 0x00ffffff) or (drawable.alpha shl 24)
  }

  private fun ancestry(view: View, root: View): List<View>? {
    val result = mutableListOf<View>(); var current: View? = view
    while (current != null && result.size < LayoutObservationPolicy.MAX_ANCESTORS) {
      if (!current.isAttachedToWindow || current.windowToken != root.windowToken) return null
      result.add(current); if (current === root) return result
      current = current.parent as? View
    }
    return null
  }
  private fun descendant(view: View, root: View) = ancestry(view, root) != null
  private fun input(view: View, root: View) = ancestry(view, root)?.any { it is EditText } != false
  private fun value(value: Any, source: String): Map<String, Any> = mapOf("value" to value, "source" to source)
  private fun missing(reason: String): Map<String, Any> = mapOf("unavailable" to reason)
  private fun unavailableFacts(reason: String): Map<String, Any> = FIELDS.associateWith { missing(reason) }

  companion object {
    private val TEXT_FIELDS = listOf("textColor", "fontFamily", "fontSize", "fontWeight", "lineHeight", "textLineRects", "fontStyle", "letterSpacing", "textDecorationLine", "textShadow", "textTransform")
    private val IMAGE_FIELDS = listOf("imageHasImage", "imageScaleType")
    private val FIELDS = listOf("isFocused", "effectiveOpacity", "backgroundColor", "effectiveBackgroundColor", "borderRadius", "boxShadow", "windowRect") + TEXT_FIELDS + IMAGE_FIELDS
    private val FRESCO_SCALES = listOf(
      ScalingUtils.ScaleType.FIT_XY to LayoutObservationPolicy.ImageMechanism.FIT_XY,
      ScalingUtils.ScaleType.FIT_X to LayoutObservationPolicy.ImageMechanism.FIT_X,
      ScalingUtils.ScaleType.FIT_Y to LayoutObservationPolicy.ImageMechanism.FIT_Y,
      ScalingUtils.ScaleType.FIT_START to LayoutObservationPolicy.ImageMechanism.FIT_START,
      ScalingUtils.ScaleType.FIT_CENTER to LayoutObservationPolicy.ImageMechanism.FIT_CENTER,
      ScalingUtils.ScaleType.FIT_END to LayoutObservationPolicy.ImageMechanism.FIT_END,
      ScalingUtils.ScaleType.CENTER to LayoutObservationPolicy.ImageMechanism.CENTER,
      ScalingUtils.ScaleType.CENTER_INSIDE to LayoutObservationPolicy.ImageMechanism.CENTER_INSIDE,
      ScalingUtils.ScaleType.CENTER_CROP to LayoutObservationPolicy.ImageMechanism.CENTER_CROP,
      ScalingUtils.ScaleType.FOCUS_CROP to LayoutObservationPolicy.ImageMechanism.FOCUS_CROP,
      ScalingUtils.ScaleType.FIT_BOTTOM_START to LayoutObservationPolicy.ImageMechanism.FIT_BOTTOM_START)
    private val IMAGE_VIEW_SCALES = listOf(
      ImageView.ScaleType.FIT_XY to LayoutObservationPolicy.ImageMechanism.FIT_XY,
      ImageView.ScaleType.FIT_START to LayoutObservationPolicy.ImageMechanism.FIT_START,
      ImageView.ScaleType.FIT_CENTER to LayoutObservationPolicy.ImageMechanism.FIT_CENTER,
      ImageView.ScaleType.FIT_END to LayoutObservationPolicy.ImageMechanism.FIT_END,
      ImageView.ScaleType.CENTER to LayoutObservationPolicy.ImageMechanism.CENTER,
      ImageView.ScaleType.CENTER_INSIDE to LayoutObservationPolicy.ImageMechanism.CENTER_INSIDE,
      ImageView.ScaleType.CENTER_CROP to LayoutObservationPolicy.ImageMechanism.CENTER_CROP,
      ImageView.ScaleType.MATRIX to LayoutObservationPolicy.ImageMechanism.MATRIX)
  }
}

/** Pure admission/composition math; tests do not claim native paint/device coverage. */
internal object LayoutObservationPolicy {
  enum class Stage(val code: String) {
    ADMISSION("admission"), UI_CURRENT("ui-current"), ROOT_RESOLVE("root-resolve"), ROOT_ATTACHMENT("root-attachment"),
    ROOT_WALK("root-walk"), PRE_OBSERVATION_SCOPE("pre-observation-scope"), OBSERVATION("observation"),
    POST_OBSERVATION_SCOPE("post-observation-scope"), RESPONSE("response")
  }
  enum class ScopeCheck(val code: String) {
    ACTIVITY_INTENT_CURRENT("activity-intent-current"), ROOT_TAG_CURRENT("root-tag-current"), ROOT_ATTACHED("root-attached"),
    DECOR_ATTACHED("decor-attached"), DECOR_CURRENT("decor-current"), ROOT_WINDOW_CURRENT("root-window-current"),
    DECOR_WINDOW_CURRENT("decor-window-current"), ROOT_IN_DECOR("root-in-decor"), FIXTURE_FOUND("fixture-found"),
    FIXTURE_TAG_CURRENT("fixture-tag-current"), FIXTURE_ATTACHED("fixture-attached"), FIXTURE_WINDOW_CURRENT("fixture-window-current"),
    FIXTURE_IN_ROOT("fixture-in-root")
  }
  fun scopeFailure(passes: (ScopeCheck) -> Boolean): ScopeCheck? = ScopeCheck.entries.firstOrNull { !passes(it) }
  // Typed finite phase/check only: never a Throwable, class, target value, tag or diagnostic sentence.
  fun failure(stage: Stage, exception: Boolean, check: ScopeCheck? = null): Map<String, String> =
    mapOf("nativeObservationStage" to (stage.code + if (exception) "-exception" else "")) +
      if (!exception && check != null && (stage == Stage.PRE_OBSERVATION_SCOPE || stage == Stage.POST_OBSERVATION_SCOPE))
        mapOf("nativeObservationScopeCheck" to check.code) else emptyMap()
  const val MAX_HOSTS = 2000
  const val MAX_NODES = 20000
  const val MAX_LINES = 512
  const val MAX_RUNS = 512
  const val MAX_ANCESTORS = 256
  class TextBudget {
    private var lines = 4096
    private var runs = 4096
    fun lines(count: Int): Boolean { if (count < 0 || count > lines) return false; lines -= count; return true }
    fun run(): Boolean { if (runs == 0) return false; runs--; return true }
  }
  private const val PUBLIC_URI = "portico://native-layout?specimen=signed-out-welcome"
  fun publicIntent(debug: Boolean, action: String?, uri: String?) = debug && action == "android.intent.action.VIEW" && uri == PUBLIC_URI
  private fun tag(value: Double) = value.isFinite() && value > 0 && value <= Int.MAX_VALUE && value == value.toInt().toDouble()
  fun admitted(debug: Boolean, action: String?, uri: String?, root: Double, fixture: Double, tags: List<Double>) =
    publicIntent(debug, action, uri) && tag(root) && tag(fixture) && root != fixture && tags.size <= MAX_HOSTS && tags.all(::tag) && tags.distinct().size == tags.size
  fun <T : Any> collect(root: T, wanted: Set<Int>, id: (T) -> Int, count: (T) -> Int, child: (T, Int) -> T): Map<Int, T>? {
    val found = linkedMapOf<Int, T>(); val pending = ArrayDeque<T>(); pending.add(root)
    var visited = 0
    while (pending.isNotEmpty()) {
      val view = pending.removeLast(); if (++visited > MAX_NODES) return null
      val tag = id(view)
      if (tag in wanted && found.put(tag, view) != null) return null
      val size = count(view)
      if (size < 0 || size.toLong() + pending.size + visited > MAX_NODES) return null
      for (index in 0 until size) pending.add(child(view, index))
    }
    return found
  }
  fun opacity(alpha: List<Double>): Double? = if (alpha.isEmpty() || alpha.any { !it.isFinite() || it < 0 || it > 1 }) null else alpha.fold(1.0) { a, b -> a * b }
  fun <T : Any> uniform(values: List<T?>): T? {
    if (values.isEmpty() || values.any { it == null }) return null
    val first = values.first()!!
    return first.takeIf { values.all { it == first } }
  }
  fun fontStyle(italic: Boolean?, skew: Double): String? = if (italic == null || !skew.isFinite() || skew != 0.0) null else if (italic) "italic" else "normal"
  fun letterSpacing(ems: Double, textSizePx: Double, density: Double): Double? =
    if (!ems.isFinite() || !textSizePx.isFinite() || textSizePx <= 0 || !density.isFinite() || density <= 0) null
    else (ems * textSizePx / density).takeIf(Double::isFinite)
  fun decoration(underline: Boolean, strike: Boolean): String = when {
    underline && strike -> "underline line-through"
    underline -> "underline"
    strike -> "line-through"
    else -> "none"
  }
  fun noTextShadow(radius: Double, noMaskFilter: Boolean, noColorFilter: Boolean) = radius.isFinite() && radius == 0.0 && noMaskFilter && noColorFilter
  enum class ImageMechanism(val code: String) {
    FIT_XY("fit-xy"), FIT_X("fit-x"), FIT_Y("fit-y"), FIT_START("fit-start"), FIT_CENTER("fit-center"), FIT_END("fit-end"),
    CENTER("center"), CENTER_INSIDE("center-inside"), CENTER_CROP("center-crop"), FOCUS_CROP("focus-crop"), FIT_BOTTOM_START("fit-bottom-start"), MATRIX("matrix")
  }
  fun <T : Any> imageMechanism(actual: T?, known: List<Pair<T, ImageMechanism>>): String? =
    if (actual == null) null else known.firstOrNull { it.first === actual }?.second?.code
  fun windowRect(bounds: DoubleArray, mappedOrigin: DoubleArray, nativeWindowOrigin: DoubleArray, density: Double): Map<String, Double>? {
    if (bounds.size != 4 || mappedOrigin.size != 2 || nativeWindowOrigin.size != 2 || !density.isFinite() || density <= 0 ||
        bounds.any { !it.isFinite() } || mappedOrigin.any { !it.isFinite() } || nativeWindowOrigin.any { !it.isFinite() } ||
        bounds[2] < bounds[0] || bounds[3] < bounds[1]) return null
    val rect = mapOf("x" to (bounds[0] - mappedOrigin[0] + nativeWindowOrigin[0]) / density,
      "y" to (bounds[1] - mappedOrigin[1] + nativeWindowOrigin[1]) / density,
      "width" to (bounds[2] - bounds[0]) / density, "height" to (bounds[3] - bounds[1]) / density)
    return rect.takeIf { it.values.all(Double::isFinite) }
  }
  data class Rgba(val red: Double, val green: Double, val blue: Double, val alpha: Double)
  fun rgba(color: Int) = rgba(Rgba((color ushr 16 and 255).toDouble(), (color ushr 8 and 255).toDouble(), (color and 255).toDouble(), (color ushr 24 and 255) / 255.0))
  fun rgba(color: Rgba): String {
    fun number(value: Double) = java.math.BigDecimal.valueOf(value).setScale(8, java.math.RoundingMode.HALF_UP).stripTrailingZeros().toPlainString()
    return "rgba(${number(color.red)}, ${number(color.green)}, ${number(color.blue)}, ${number(color.alpha)})"
  }
  fun background(colors: List<Int>, alpha: List<Double>): Rgba {
    var a = 0.0; var r = 0.0; var g = 0.0; var b = 0.0
    for (index in colors.indices) {
      val color = colors[index]; val ca = (color ushr 24 and 255) / 255.0
      r += (color ushr 16 and 255) * ca * (1 - a); g += (color ushr 8 and 255) * ca * (1 - a); b += (color and 255) * ca * (1 - a)
      a += ca * (1 - a)
      r *= alpha[index]; g *= alpha[index]; b *= alpha[index]; a *= alpha[index]
    }
    if (a == 0.0) return Rgba(0.0, 0.0, 0.0, 0.0)
    return Rgba(r / a, g / a, b / a, a)
  }
}
