package tv.getportico.portico.native

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class PorticoLocalAudioSelectionTest {
  private val command = mapOf<String, Any?>("revision" to 1.0, "catalogRevision" to 2.0, "trackId" to "local-audio-2-1")

  @Test
  fun failedOrTimedOutRestorationStopsBeforeReleasingTheHoldAndReporting() {
    assertEquals(15_000L, PorticoLocalAudioSelection.SELECTION_TIMEOUT_MS)
    for (timedOut in listOf(false, true)) {
      val events = ArrayList<String>()
      var held = true
      val restoring = PorticoLocalAudioSelection.Restoration(PorticoLocalAudioSelection.Retained(null, 7),
          { events.add("cancel timeout") },
          { assertTrue("stop while playback is still held", held); events.add("stop") },
          { held = false; events.add("local-track error") })
      if (timedOut) restoring.timeout.run() else restoring.fail()
      assertFalse(held)
      assertFalse(restoring.active)
      assertEquals(listOf("cancel timeout", "stop", "local-track error"), events)
      restoring.timeout.run()
      restoring.fail()
      assertEquals("late callbacks cannot stop a replacement or duplicate its error", 3, events.size)
    }
  }

  @Test
  fun completedOrRetiredRestorationIgnoresQueuedTimeouts() {
    val events = ArrayList<String>()
    val restoring = PorticoLocalAudioSelection.Restoration(PorticoLocalAudioSelection.Retained(null, 7),
        { events.add("cancel timeout") }, { events.add("stop") }, { events.add("local-track error") })
    restoring.cancel()
    restoring.timeout.run()
    assertFalse(restoring.active)
    assertEquals(listOf("cancel timeout"), events)
  }

  @Test
  fun localChoiceRequiresItsExactCatalogueAndCommandRevisions() {
    assertTrue(PorticoLocalAudioSelection.valid(null))
    assertTrue(PorticoLocalAudioSelection.valid(command))
    assertTrue(PorticoLocalAudioSelection.valid(command + mapOf("revision" to 9007199254740991.0)))
    for (key in command.keys) assertFalse(PorticoLocalAudioSelection.valid(command - key))
    assertFalse(PorticoLocalAudioSelection.valid(command + mapOf("streamIndex" to 1)))
    assertFalse(PorticoLocalAudioSelection.valid(emptyList<Any>()))
  }

  @Test
  fun malformedOrUnsafeValuesCannotReachTrackSelection() {
    for (key in listOf("revision", "catalogRevision")) for (value in listOf(null, true, "1", 0, -1, 1.5, Double.NaN, Double.POSITIVE_INFINITY, 9007199254740992.0))
      assertFalse(PorticoLocalAudioSelection.valid(command + mapOf(key to value)))
    for (id in listOf(null, 1, "", "a".repeat(257)))
      assertFalse(PorticoLocalAudioSelection.valid(command + mapOf("trackId" to id)))
  }
}
