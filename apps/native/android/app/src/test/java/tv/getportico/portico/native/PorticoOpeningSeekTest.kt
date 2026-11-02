package tv.getportico.portico.native

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class PorticoOpeningSeekTest {
  @Test fun aResumeOpensTheMediaAtItsPlaceWhateverTheSource() {
    assertEquals(PorticoOpeningSeek.Start(3, 754.5), PorticoOpeningSeek.start(mapOf("positionSeconds" to 754.5, "revision" to 3.0), routeResume = false, linear = false))
  }

  @Test fun theStartRouteResumesAndLiveChannelsAreNotOpeningSeeks() {
    assertNull(PorticoOpeningSeek.start(null, routeResume = false, linear = false))
    assertNull(PorticoOpeningSeek.start(mapOf("positionSeconds" to 0.0, "revision" to 1.0), routeResume = false, linear = false))
    assertNull(PorticoOpeningSeek.start(mapOf("positionSeconds" to 30.0, "revision" to 1.0), routeResume = true, linear = false))
    assertNull(PorticoOpeningSeek.start(mapOf("positionSeconds" to 30.0, "revision" to 1.0), routeResume = false, linear = true))
    assertNull(PorticoOpeningSeek.start(mapOf("positionSeconds" to Double.NaN, "revision" to 1.0), routeResume = false, linear = false))
    assertNull(PorticoOpeningSeek.start(mapOf("positionSeconds" to 30.0), routeResume = false, linear = false))
  }
}
