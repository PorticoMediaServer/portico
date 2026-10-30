package tv.getportico.portico.native

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/** Capture admission must name the exact lease/session/generation, independent of JS number boxing. */
class PorticoSnapshotBindingTest {
  private val current = mapOf<String, Any?>("lease" to "lease-A", "sessionId" to "session-A", "itemId" to "item-A", "intentId" to 3L, "generation" to 7L)

  @Test fun matchesOnlyTheCompleteCurrentBinding() {
    assertTrue(PorticoSnapshotBinding.matches(current, current))
    assertTrue(PorticoSnapshotBinding.matches(current + mapOf("intentId" to 3.0, "generation" to 7.0), current))
    assertFalse(PorticoSnapshotBinding.matches(current, null))
    for (key in current.keys) assertFalse(key, PorticoSnapshotBinding.matches(current - key, current))
  }

  @Test fun replacementLeaseSessionItemIntentOrGenerationCannotCaptureOldPixels() {
    for (key in listOf("lease", "sessionId", "itemId")) {
      assertFalse(key, PorticoSnapshotBinding.matches(current + (key to "replacement"), current))
      assertFalse(key, PorticoSnapshotBinding.matches(current + (key to ""), current))
    }
    for (key in listOf("intentId", "generation")) assertFalse(key, PorticoSnapshotBinding.matches(current + (key to 8), current))
  }

  @Test fun numericCoercionFractionsAndNonfiniteValuesAreRejected() {
    for (value in listOf<Any?>(null, "3", true, 0, -1, 3.5, Double.NaN, Double.POSITIVE_INFINITY, 9007199254740992.0)) {
      assertFalse(value.toString(), PorticoSnapshotBinding.matches(current + ("intentId" to value), current + ("intentId" to value)))
    }
  }
}
