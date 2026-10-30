package tv.getportico.portico.native

import org.junit.Assert.*
import org.junit.Test

class PorticoPostureStateTest {
  private val metrics = PostureMetrics(PostureBounds(200.0, 100.0, 1880.0, 1860.0), 2.0)
  private val hinge = PostureFold(PostureBounds(830.0, 0.0, 850.0, 1760.0), "half-opened", "vertical", true, "full")
  @Test fun windowAndFoldUseLogicalDpWithoutSubtractingScreenOrigin() {
    val state = PorticoPostureState(); val token = state.begin(metrics)
    assertEquals("pending",state.snapshot.status)
    assertTrue(state.available(token,metrics,listOf(hinge)))
    assertEquals(PostureWindow(840.0,880.0),state.snapshot.window)
    assertEquals(PostureBounds(415.0,0.0,425.0,880.0),state.snapshot.folds.single().bounds)
  }
  @Test fun resizeRetiresOldWindowFactsAndPauseRetiresItsCallbacks() {
    val state = PorticoPostureState(); val old = state.begin(metrics)
    val resized = metrics.copy(bounds = PostureBounds(0.0,0.0,1760.0,1680.0))
    val current = state.begin(resized)
    assertFalse(state.available(old,metrics,listOf(hinge)))
    assertEquals(emptyList<PostureFold>(),state.snapshot.folds)
    assertTrue(state.available(current,resized,emptyList()))
    assertTrue(state.clear()); assertEquals("unavailable",state.snapshot.status)
    assertFalse(state.available(current,resized,listOf(hinge)))
  }
  @Test fun repeatedFactsRetainRevisionButNewDensityChangesLogicalWindow() {
    val state = PorticoPostureState(); val token = state.begin(metrics)
    state.available(token,metrics,emptyList()); val revision = state.revision
    assertFalse(state.available(token,metrics,emptyList())); assertEquals(revision,state.revision)
    val changed = metrics.copy(density=1.0); val next = state.begin(changed)
    assertTrue(state.available(next,changed,emptyList())); assertEquals(PostureWindow(1680.0,1760.0),state.snapshot.window)
  }
  @Test fun invalidMetricsAreRefusedAndZeroWidthCreaseIsPreserved() {
    assertThrows(IllegalArgumentException::class.java) { metrics.copy(density=0.0) }
    assertThrows(IllegalArgumentException::class.java) { metrics.copy(bounds=PostureBounds(0.0,0.0,Double.NaN,10.0)) }
    val state=PorticoPostureState(); val token=state.begin(metrics)
    state.available(token,metrics,listOf(hinge.copy(bounds=PostureBounds(840.0,0.0,840.0,1760.0),occlusion="none")))
    assertEquals(420.0,state.snapshot.folds.single().bounds.left,0.0)
    assertEquals(420.0,state.snapshot.folds.single().bounds.right,0.0)
  }
}
