package tv.getportico.portico.native

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/** The version 2 audio plan as Android reads it (Playback Protocol §18.1): what decodes, what plays, and its gain. */
class PorticoAudioPlanTest {
  private val plan = mapOf(
      "version" to 2.0, "mode" to "direct", "url" to "https://server.example/v1/media/g/a.flac",
      "container" to "flac", "codec" to "flac", "durationFrames" to 441000.0, "sampleRate" to 44100.0)

  @Test
  fun onlyPairsEveryAndroidDecodesAreDecodedHere() {
    for ((container, codec) in listOf("flac" to "flac", "mp4" to "aac", "m4a" to "aac", "adts" to "aac", "mp3" to "mp3", "ogg" to "opus", "ogg" to "vorbis", "wav" to "pcm_s16le"))
      assertTrue("$container/$codec", PorticoAudioPlan.decodes(mapOf("container" to container, "codec" to codec)))
    for ((container, codec) in listOf("mp4" to "alac", "aiff" to "pcm_s16be", "mkv" to "flac", "wav" to "flac"))
      assertFalse("$container/$codec", PorticoAudioPlan.decodes(mapOf("container" to container, "codec" to codec)))
    assertFalse(PorticoAudioPlan.decodes(mapOf("container" to "flac")))
  }

  @Test
  fun aPlanPlaysOnlyWhenItIsCompleteAndInRange() {
    assertTrue(PorticoAudioPlan.playable(plan))
    assertTrue(PorticoAudioPlan.playable(plan + ("mode" to "converted")))
    assertFalse(PorticoAudioPlan.playable(null))
    assertFalse("another version", PorticoAudioPlan.playable(plan + ("version" to 1.0)))
    assertFalse("an unknown mode", PorticoAudioPlan.playable(plan + ("mode" to "stream")))
    assertFalse("no address", PorticoAudioPlan.playable(plan - "url"))
    assertFalse("a rate below 8 kHz", PorticoAudioPlan.playable(plan + ("sampleRate" to 4000.0)))
    assertFalse("no frames", PorticoAudioPlan.playable(plan + ("durationFrames" to 0.0)))
    assertFalse("part of a frame", PorticoAudioPlan.playable(plan + ("durationFrames" to 1.5)))
    assertFalse("a boolean is not a number", PorticoAudioPlan.playable(plan + ("durationFrames" to true)))
  }

  @Test
  fun gainFollowsTheViewersNormalizationAndNeverClipsThePeak() {
    val track = mapOf("normalization" to "track")
    assertEquals("normalization off", 1f, PorticoAudioPlan.gain(plan + ("gain" to mapOf("trackDb" to -6.0)), mapOf("normalization" to "off")), 0f)
    assertEquals(0.5012f, PorticoAudioPlan.gain(plan + ("gain" to mapOf("trackDb" to -6.0)), track), 0.0001f)
    assertEquals("the preamp adds", 1f, PorticoAudioPlan.gain(plan + ("gain" to mapOf("trackDb" to -6.0)), track + ("preampDb" to 6.0)), 0.0001f)
    assertEquals("amplified only up to the peak", 1f / 0.9f, PorticoAudioPlan.gain(plan + ("gain" to mapOf("trackDb" to 6.0, "trackPeak" to 0.9)), track), 0.0001f)
    assertEquals("no peak known: never above full scale", 1f, PorticoAudioPlan.gain(plan + ("gain" to mapOf("trackDb" to 6.0)), track), 0f)
    val album = mapOf("normalization" to "album")
    assertEquals("album gain, with the track's peak when the album has none", 0.7079f, PorticoAudioPlan.gain(plan + ("gain" to mapOf("trackDb" to -9.0, "albumDb" to -3.0, "trackPeak" to 0.5)), album), 0.0001f)
    assertEquals("album asked, none known: the track's", 0.5012f, PorticoAudioPlan.gain(plan + ("gain" to mapOf("trackDb" to -6.0)), album), 0.0001f)
    assertEquals("no gain in the plan", 1f, PorticoAudioPlan.gain(plan, track), 0f)
  }

  @Test
  fun durationComesFromFramesAndRate() {
    assertEquals(10.0, PorticoAudioPlan.durationSeconds(plan), 0.0)
    assertEquals(0.0, PorticoAudioPlan.durationSeconds(plan - "sampleRate"), 0.0)
    assertEquals(0.0, PorticoAudioPlan.durationSeconds(null), 0.0)
  }

  @Test
  fun neverBoostCapsOnlyAnAllowedPeakBoost() {
    val track = mapOf("normalization" to "track")
    val boost = plan + ("gain" to mapOf("trackDb" to 6.0, "trackPeak" to 0.5))
    assertEquals("a measured peak leaves room for +6 dB", 1.9953f, PorticoAudioPlan.gain(boost, track), 0.0001f)
    assertEquals("false Never boost allows it", 1.9953f, PorticoAudioPlan.gain(boost, track + ("neverBoost" to false)), 0.0001f)
    assertEquals("Never boost caps it at unity", 1f, PorticoAudioPlan.gain(boost, track + ("neverBoost" to true)), 0f)
    assertEquals("only an actual boolean enables the cap", 1.9953f, PorticoAudioPlan.gain(boost, track + ("neverBoost" to "true")), 0.0001f)
    assertEquals("attenuation is untouched", 0.5012f, PorticoAudioPlan.gain(plan + ("gain" to mapOf("trackDb" to -6.0)), track + ("neverBoost" to true)), 0.0001f)
    assertEquals("no known peak never boosts", 1f, PorticoAudioPlan.gain(plan + ("gain" to mapOf("trackDb" to 6.0)), track), 0f)
  }

  @Test
  fun albumPeakBoostObeysNeverBoostAndKeepsTheTrackFallback() {
    val album = mapOf("normalization" to "album")
    val boost = plan + ("gain" to mapOf("albumDb" to 6.0, "albumPeak" to 0.5))
    assertEquals("an album peak leaves room for +6 dB", 1.9953f, PorticoAudioPlan.gain(boost, album), 0.0001f)
    assertEquals("Never boost caps the album boost too", 1f, PorticoAudioPlan.gain(boost, album + ("neverBoost" to true)), 0f)
    assertEquals("album without its own gain falls back to the track", 0.5012f, PorticoAudioPlan.gain(plan + ("gain" to mapOf("trackDb" to -6.0, "albumPeak" to 0.5)), album), 0.0001f)
  }

  @Test
  fun serverAppliedLoudnessIsNeverAppliedTwice() {
    val track = mapOf("normalization" to "track")
    val applied = plan + ("loudnessAppliedDb" to -7.0) + ("gain" to mapOf("trackDb" to -7.0))
    assertEquals(1f, PorticoAudioPlan.gain(applied, track), 0f)
    assertEquals("even with normalization off", 1f, PorticoAudioPlan.gain(applied, mapOf("normalization" to "off")), 0f)
    assertEquals("zero is still a measured application", 1f, PorticoAudioPlan.gain(plan + ("loudnessAppliedDb" to 0.0), track), 0f)
  }

  private fun withEnvelope(start: List<Double>, end: List<Double>): Map<String, Any> =
      plan + ("loudnessEnvelope" to mapOf("intervalMs" to 100, "startLufs" to start, "endLufs" to end))

  @Test
  fun sameAlbumAlwaysJoinsEvenWithMeasuredEnvelopes() {
    val current = withEnvelope(listOf(-30.0), listOf(-30.0))
    val next = withEnvelope(listOf(0.0), listOf(0.0))
    assertEquals(0.0, PorticoAudioPlan.transitionSeconds(current, next, 6.0, sameAlbum = true, smooth = true), 0.0)
    assertEquals(0.0, PorticoAudioPlan.transitionSeconds(current, next, 6.0, sameAlbum = true, smooth = false), 0.0)
  }

  @Test
  fun smoothOffKeepsTheFixedMaximum() {
    val current = withEnvelope(listOf(-30.0), listOf(-30.0))
    val next = withEnvelope(listOf(0.0), listOf(0.0))
    assertEquals(6.0, PorticoAudioPlan.transitionSeconds(current, next, 6.0, sameAlbum = false, smooth = false), 0.0)
  }

  @Test
  fun measuredContrastOfTwelveHalvesTheMaximum() {
    val current = withEnvelope(listOf(-30.0), listOf(-20.0))
    val next = withEnvelope(listOf(-8.0), listOf(0.0))
    assertEquals(3.0, PorticoAudioPlan.transitionSeconds(current, next, 6.0, sameAlbum = false, smooth = true), 0.0001)
  }

  @Test
  fun identicalMeasuredHeadsAndTailsKeepTheMaximum() {
    val current = withEnvelope(listOf(-14.0), listOf(-14.0))
    val next = withEnvelope(listOf(-14.0), listOf(-14.0))
    assertEquals(6.0, PorticoAudioPlan.transitionSeconds(current, next, 6.0, sameAlbum = false, smooth = true), 0.0)
  }

  @Test
  fun measuredContrastUsesTheEnergyMeanNotTheArithmeticMean() {
    val current = withEnvelope(listOf(-6.0), listOf(-6.0, -18.0))
    val next = withEnvelope(listOf(-2.0), listOf(-2.0))
    val seconds = PorticoAudioPlan.transitionSeconds(current, next, 6.0, sameAlbum = false, smooth = true)
    // The energy mean of [-6, -18] is -8.7446 LUFS, not the -12 dB average, so
    // the 6.7446 contrast gives 6 / (1 + 6.7446 / 12).
    assertEquals(3.8411, seconds, 0.0001)
    assertTrue("the arithmetic mean would have given 3.2727", Math.abs(seconds - 3.2727) > 0.5)
  }

  @Test
  fun absentOrEmptyEnvelopesKeepTheFixedFade() {
    assertEquals(6.0, PorticoAudioPlan.transitionSeconds(plan, plan, 6.0, sameAlbum = false, smooth = true), 0.0)
    val measuredTail = withEnvelope(listOf(-14.0), listOf(-14.0))
    val emptyTail = withEnvelope(listOf(-14.0), emptyList())
    assertEquals(6.0, PorticoAudioPlan.transitionSeconds(emptyTail, measuredTail, 6.0, sameAlbum = false, smooth = true), 0.0)
    val emptyHead = withEnvelope(emptyList(), listOf(-14.0))
    assertEquals(6.0, PorticoAudioPlan.transitionSeconds(measuredTail, emptyHead, 6.0, sameAlbum = false, smooth = true), 0.0)
  }

  @Test
  fun invalidEnvelopesFallBackToTheFixedFade() {
    val next = withEnvelope(listOf(-14.0), listOf(-14.0))
    val invalid = listOf(
        mapOf("intervalMs" to 50, "startLufs" to listOf(-14.0), "endLufs" to listOf(-14.0)),
        mapOf("intervalMs" to true, "startLufs" to listOf(-14.0), "endLufs" to listOf(-14.0)),
        mapOf("intervalMs" to 100, "startLufs" to listOf(-14.0), "endLufs" to List(31) { -14.0 }),
        mapOf("intervalMs" to 100, "startLufs" to listOf(-14.0), "endLufs" to listOf(Double.NaN)),
        mapOf("intervalMs" to 100, "startLufs" to listOf(-14.0), "endLufs" to listOf(true)),
        mapOf("intervalMs" to 100, "startLufs" to listOf(-14.0), "endLufs" to listOf(20.0)),
        mapOf("intervalMs" to 100, "startLufs" to listOf(-14.0), "endLufs" to listOf(-121.0)),
        mapOf("intervalMs" to 100, "startLufs" to listOf(-14.0)))
    for (envelope in invalid) {
      val current = plan + ("loudnessEnvelope" to envelope)
      assertEquals("$envelope", 6.0, PorticoAudioPlan.transitionSeconds(current, next, 6.0, sameAlbum = false, smooth = true), 0.0)
    }
  }

  @Test
  fun theTransitionNeverExceedsItsMaximum() {
    val quiet = withEnvelope(listOf(0.0), listOf(-60.0))
    val loud = withEnvelope(listOf(0.0), listOf(0.0))
    assertEquals(0.0, PorticoAudioPlan.transitionSeconds(quiet, loud, 0.0, sameAlbum = false, smooth = true), 0.0)
    assertEquals("a fractional cap is never widened to the 0.25 floor", 0.1, PorticoAudioPlan.transitionSeconds(quiet, loud, 0.1, sameAlbum = false, smooth = true), 0.0001)
    assertEquals(0.2, PorticoAudioPlan.transitionSeconds(loud, loud, 0.2, sameAlbum = false, smooth = true), 0.0001)
  }

  @Test
  fun invalidMaximumsAreNan() {
    val constant = withEnvelope(listOf(-14.0), listOf(-14.0))
    for (maximum in listOf(Double.NaN, Double.POSITIVE_INFINITY, Double.NEGATIVE_INFINITY, -0.1, 12.0001))
      assertTrue("$maximum", PorticoAudioPlan.transitionSeconds(constant, constant, maximum, sameAlbum = false, smooth = true).isNaN())
    assertTrue(PorticoAudioPlan.transitionSeconds(constant, constant, -1.0, sameAlbum = false, smooth = false).isNaN())
    assertEquals(12.0, PorticoAudioPlan.transitionSeconds(constant, constant, 12.0, sameAlbum = false, smooth = true), 0.0)
  }

  @Test
  fun pcmStaysTheSinkForUnknownOrUntypedDelivery() {
    for (codec in listOf(null, "", "unknown", "pcm_s16le", "flac", "opus", "dsd", 42, true, listOf("ac3")))
      assertTrue("$codec", PorticoAudioPlan.usesPCM(null, null, codec))
  }

  @Test
  fun knownEncodedDeliveryWithoutAPlayablePlanRefusesThePCMSink() {
    for (codec in listOf("ac3", "eac3", "truehd", "dts", "dts_hd"))
      assertFalse(codec, PorticoAudioPlan.usesPCM(null, null, codec))
  }

  @Test
  fun aPlayablePlanQualifiesEvenAnEncodedRecipe() {
    assertTrue("a playable render plan", PorticoAudioPlan.usesPCM(plan, null, "eac3"))
    assertTrue("a playable seek plan with effects off", PorticoAudioPlan.usesPCM(null, plan, "dts_hd"))
  }

  @Test
  fun anUnplayablePlanDoesNotQualifyAnEncodedRecipe() {
    val incomplete = plan - "url"
    assertFalse(PorticoAudioPlan.usesPCM(incomplete, incomplete, "truehd"))
    assertFalse(PorticoAudioPlan.usesPCM(plan + ("version" to 1.0), plan + ("mode" to "stream"), "dts"))
    assertFalse("no plan at all", PorticoAudioPlan.usesPCM(null, null, "ac3"))
  }
}
