package tv.getportico.portico.native

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/** Executes the same lifecycle used by PorticoVideoStage; no Android/compositor pixel claim. */
class PorticoSurfaceBindingTest {
  private class Display {
    val events = mutableListOf<String>()
    var visible = true
    var output: Any? = null
    val binding = PorticoSurfaceBinding<Any>(
      visible = { visible = it; events.add("visible:$it") },
      detach = { assertTrue(output === it); output = null; events.add("detach") },
      attach = { assertTrue(visible); assertEquals(null, output); output = it; events.add("attach") },
      compositionChanged = { events.add("composition") },
    )
  }

  @Test fun idleDisplayHasNoProducerOrBlackStageAndRepeatedIdleIsInert() {
    val d = Display()
    assertFalse(d.visible)
    assertFalse(d.binding.shows(null))
    d.binding.show(null)
    assertEquals(listOf("visible:false"), d.events)
  }

  @Test fun expandedToDockWithTheSamePlayerDoesNotRetireOrReattachTheProducer() {
    val d = Display()
    val player = Any()
    d.binding.show(player)
    val events = d.events.toList()
    repeat(4) { d.binding.show(player) }
    assertEquals(events, d.events)
    assertTrue(d.binding.shows(player))
    assertTrue(d.output === player)
    assertTrue(d.visible)
  }

  @Test fun closeRetiresCompositionBeforeDisconnectingAndCannotBeRevivedByRepeatedNull() {
    val d = Display()
    val player = Any()
    d.binding.show(player)
    d.events.clear()
    d.binding.show(null)
    assertEquals(listOf("visible:false", "detach", "composition"), d.events)
    assertFalse(d.binding.shows(player))
    assertFalse(d.visible)
    assertEquals(null, d.output)
    d.binding.show(null)
    assertEquals(3, d.events.size)
  }

  @Test fun reentryRebindsOnlyTheAdmittedPlayer() {
    val d = Display()
    val first = Any()
    val next = Any()
    d.binding.show(first)
    d.binding.show(null)
    d.events.clear()
    d.binding.show(next)
    assertEquals(listOf("visible:true", "attach", "composition"), d.events)
    assertFalse(d.binding.shows(first))
    assertTrue(d.binding.shows(next))
    assertTrue(d.output === next)
  }

  @Test fun replacementDisconnectsThePreviousPlayerBeforeBindingTheNext() {
    val d = Display()
    val first = Any()
    val next = Any()
    d.binding.show(first)
    d.events.clear()
    d.binding.show(next)
    assertEquals(listOf("detach", "visible:true", "attach", "composition"), d.events)
    assertFalse(d.binding.shows(first))
    assertTrue(d.binding.shows(next))
  }
}
