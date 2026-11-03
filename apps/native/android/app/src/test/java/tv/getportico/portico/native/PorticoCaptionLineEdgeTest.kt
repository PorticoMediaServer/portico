package tv.getportico.portico.native

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class PorticoCaptionLineEdgeTest {
  private fun edge(background: String?, box: Int, systemBox: Boolean = false) =
      PorticoMpvSupport.captionLineEdge(background?.let { mapOf("background" to it, "useSystemCaptionStyle" to false) }, systemBox, box, 40f)

  @Test fun outlineAndDropShadowDrawNoBox() {
    val outline = edge("outline", 0xB3000000.toInt())
    assertEquals(0, outline.box); assertTrue(outline.radius > 0f); assertEquals(0f, outline.dx); assertEquals(0f, outline.dy)
    val shadow = edge("shadow", 0xB3000000.toInt())
    assertEquals(0, shadow.box); assertTrue(shadow.radius > 0f && shadow.dx > 0f && shadow.dy > 0f)
    assertEquals(0xff000000.toInt(), shadow.color)
  }

  @Test fun translucentAndSolidBoxesKeepTheirBoxAndNoEdge() {
    for (box in listOf(0xB3000000.toInt(), 0xff000000.toInt())) {
      val value = edge("translucent", box)
      assertEquals(box, value.box); assertEquals(0f, value.radius)
    }
  }

  @Test fun noBackgroundStillGetsAnEdgeToStayReadable() {
    val value = edge("none", 0)
    assertEquals(0, value.box); assertTrue(value.radius > 0f); assertEquals(0f, value.dx)
  }

  @Test fun aSystemCaptionStyleKeepsItsOwnBox() {
    val value = edge("shadow", 0x80112233.toInt(), systemBox = true)
    assertEquals(0x80112233.toInt(), value.box)
  }
}
