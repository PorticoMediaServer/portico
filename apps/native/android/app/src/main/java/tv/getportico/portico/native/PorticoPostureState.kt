package tv.getportico.portico.native

internal data class PostureBounds(val left: Double, val top: Double, val right: Double, val bottom: Double) {
  fun scaled(density: Double) = PostureBounds(left / density, top / density, right / density, bottom / density)
}
internal data class PostureWindow(val width: Double, val height: Double)
internal data class PostureMetrics(val bounds: PostureBounds, val density: Double) {
  init {
    require(density.isFinite() && density > 0)
    require(listOf(bounds.left, bounds.top, bounds.right, bounds.bottom).all { it.isFinite() })
    require(bounds.right > bounds.left && bounds.bottom > bounds.top)
  }
  fun window() = PostureWindow((bounds.right - bounds.left) / density, (bounds.bottom - bounds.top) / density)
}
internal data class PostureFold(val bounds: PostureBounds, val state: String, val orientation: String, val isSeparating: Boolean, val occlusion: String)
internal data class PostureSnapshot(val status: String = "unavailable", val window: PostureWindow? = null, val folds: List<PostureFold> = emptyList())

/** UI-thread facts only. A new attachment retires every callback from the previous window. */
internal class PorticoPostureState {
  var snapshot = PostureSnapshot(); private set
  var revision = 0L; private set
  private var attachment = 0L
  fun current(token: Long) = token == attachment
  private fun publish(next: PostureSnapshot): Boolean {
    if (next == snapshot) return false
    snapshot = next; revision++
    return true
  }
  fun begin(metrics: PostureMetrics): Long {
    attachment++
    publish(PostureSnapshot("pending", metrics.window()))
    return attachment
  }
  fun available(token: Long, metrics: PostureMetrics, pixelFolds: List<PostureFold>): Boolean {
    if (!current(token)) return false
    // FoldingFeature coordinates already start at the window origin. WindowMetrics' screen
    // origin must never be subtracted from them, including in split-screen and freeform mode.
    return publish(PostureSnapshot("available", metrics.window(), pixelFolds.map { it.copy(bounds = it.bounds.scaled(metrics.density)) }))
  }
  fun clear(): Boolean { attachment++; return publish(PostureSnapshot()) }
}
