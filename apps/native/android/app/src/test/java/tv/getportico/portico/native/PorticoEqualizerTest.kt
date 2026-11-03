package tv.getportico.portico.native

import androidx.media3.common.C
import java.nio.ByteBuffer
import java.nio.ByteOrder
import kotlin.math.PI
import kotlin.math.abs
import kotlin.math.log10
import kotlin.math.sin
import kotlin.math.sqrt
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertSame
import org.junit.Assert.assertTrue
import org.junit.Test

class PorticoEqualizerTest {
  @After fun flat() = PorticoEqualizerStore.setForTest(PorticoEqualizerSettings.FLAT)

  @Test fun theSettingIsTenBandsAndAPreampInHalfDecibelStepsWithinTwelve() {
    val ok = PorticoEqualizerSettings.parse(mapOf("enabled" to true, "preampDb" to -0.5, "bands" to List(10) { 12.0 }))!!
    assertEquals(listOf(12.0), ok.bands.distinct())
    assertNull(PorticoEqualizerSettings.parse(mapOf("enabled" to true, "preampDb" to 0.25, "bands" to List(10) { 0.0 })))
    assertNull(PorticoEqualizerSettings.parse(mapOf("enabled" to true, "preampDb" to 0.0, "bands" to List(9) { 0.0 })))
    assertNull(PorticoEqualizerSettings.parse(mapOf("enabled" to true, "preampDb" to 0.0, "bands" to List(10) { 12.5 })))
    assertNull(PorticoEqualizerSettings.parse(mapOf("preampDb" to 0.0, "bands" to List(10) { 0.0 })))
    assertEquals(mapOf("eqEnabled" to true, "eqPreampDb" to -0.5, "eqBands" to List(10) { 12.0 }), ok.facts())
  }

  @Test fun offOrFlatIsNoStage() {
    assertTrue(PorticoEqualizerSettings.FLAT.transparent)
    assertTrue(PorticoEqualizerSettings(false, 6.0, List(10) { 6.0 }).transparent)
    assertFalse(PorticoEqualizerSettings(true, 0.0, List(10) { if (it == 5) 3.0 else 0.0 }).transparent)
  }

  private fun rmsDb(frequency: Double, settings: PorticoEqualizerSettings, rate: Int = 48000): Double {
    val eq = PorticoEqualizer(); assertTrue(eq.prepare(settings, rate, 1))
    var input = 0.0; var output = 0.0
    for (n in 0 until rate) {
      val x = (0.25 * sin(2 * PI * frequency * n / rate)).toFloat()
      val y = eq.process(x, 0)
      if (n > rate / 4) { input += x * x; output += y.toDouble() * y }
    }
    return 20 * log10(sqrt(output / input))
  }

  @Test fun aBandBoostsItsOwnFrequencyAndLeavesFarOnesAlone() {
    val boost = PorticoEqualizerSettings(true, 0.0, List(10) { if (it == 5) 6.0 else 0.0 })
    assertEquals(6.0, rmsDb(1000.0, boost), 0.3)
    assertEquals(0.0, rmsDb(62.0, boost), 0.3)
    assertEquals(0.0, rmsDb(16000.0, boost), 0.5)
    assertEquals(-6.0, rmsDb(440.0, PorticoEqualizerSettings(true, -6.0, List(10) { 0.0 })), 0.05)
  }

  @Test fun aBandAtOrAboveNyquistIsBypassed() {
    val top = PorticoEqualizerSettings(true, 0.0, List(10) { if (it == 9) 12.0 else 0.0 })
    val eq = PorticoEqualizer(); assertTrue(eq.prepare(top, 22050, 1))
    assertEquals(0.3f, eq.process(0.3f, 0), 0f)
  }

  private fun pcm(vararg samples: Float): ByteBuffer {
    val buffer = ByteBuffer.allocateDirect(samples.size * 4).order(ByteOrder.nativeOrder())
    samples.forEach { buffer.putFloat(it) }; buffer.flip(); return buffer
  }

  @Test fun theMusicGainStageRunsTheEqualizerAndKeepsTheBitExactPassWhenFlat() {
    val gain = PorticoAudioGain(); gain.reset()
    val input = pcm(0.1f, -0.1f, 0.2f, -0.2f)
    assertSame("flat: the decoder's own buffer, untouched", input, gain.apply(input, C.ENCODING_PCM_FLOAT, C.ENCODING_PCM_FLOAT, 2, 48000))
    PorticoEqualizerStore.setForTest(PorticoEqualizerSettings(true, -6.0, List(10) { 0.0 }))
    val out = gain.apply(input, C.ENCODING_PCM_FLOAT, C.ENCODING_PCM_FLOAT, 2, 48000).order(ByteOrder.nativeOrder())
    assertEquals(0.1f * 0.501f, out.getFloat(0), 0.001f)
    assertTrue(abs(out.getFloat(4) + 0.0501f) < 0.001f)
  }
}
