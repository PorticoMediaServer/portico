package tv.getportico.portico.native

import org.junit.Test

class PorticoLocalPartsTest {
  @Test fun aDownloadedTitleSeeksAcrossFilesOnOneClock() {
    val rows = listOf(
      mapOf("url" to "file:///Downloads/copy/one.mp4", "offsetSeconds" to 0, "durationSeconds" to 10.01, "startSeconds" to 3),
      mapOf("url" to "file:///Downloads/copy/two.mp4", "offsetSeconds" to 10.01, "durationSeconds" to 20.02, "startSeconds" to 0),
    )
    val parts = PorticoLocalParts.read(rows, rows[0]["url"] as String) { it.startsWith("file:///Downloads/copy/") }
    check(PorticoLocalParts.target(parts, 9.0) == (0 to 9000L))
    check(PorticoLocalParts.target(parts, 10.01) == (1 to 0L))
    check(PorticoLocalParts.target(parts, 21.01) == (1 to 11000L))
    check(PorticoLocalParts.target(parts, 40.0) == (1 to 20020L))
    val edl = PorticoLocalParts.edl(parts) { it.removePrefix("file://") }
    check(edl == "edl://%23%/Downloads/copy/one.mp4,3.0,10.01;%23%/Downloads/copy/two.mp4,0.0,20.02")
    var refused = false
    try { PorticoLocalParts.read(rows.mapIndexed { n, row -> if(n == 1) row + ("url" to "file:///private/other.mp4") else row }, rows[0]["url"] as String) { it.startsWith("file:///Downloads/copy/") } }
    catch (_: IllegalStateException) { refused = true }
    check(refused)
  }
  @Test fun convertedPartsKeepTheCaptionSourceClockAndTheMeasuredPlaybackClock() {
    val rows=listOf(
      mapOf("url" to "file:///one", "offsetSeconds" to 0, "durationSeconds" to 10, "startSeconds" to 0, "sourceOffsetSeconds" to 0),
      mapOf("url" to "file:///two", "offsetSeconds" to 10, "durationSeconds" to 10, "startSeconds" to 0, "sourceOffsetSeconds" to 10.042),
    )
    val parts=PorticoLocalParts.read(rows, "file:///one") { true }
    check(PorticoLocalParts.target(parts, 10.5) == (1 to 500L))
    check(kotlin.math.abs(PorticoLocalParts.sourceTime(parts, 10.5) - 10.542) < 0.000001)
  }

}
