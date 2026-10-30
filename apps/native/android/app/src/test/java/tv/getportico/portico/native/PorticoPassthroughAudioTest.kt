package tv.getportico.portico.native

import android.media.AudioFormat
import androidx.media3.common.Format
import androidx.media3.common.MimeTypes
import androidx.media3.exoplayer.audio.AudioOffloadSupport
import androidx.media3.exoplayer.audio.AudioSink
import java.lang.reflect.Proxy
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertSame
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test

class PorticoPassthroughAudioTest {
  private fun format(mime: String) = Format.Builder().setSampleMimeType(mime).setChannelCount(6).setSampleRate(48000).build()

  @Test
  fun allAndPerFormatChoicesKeepPcmAvailable() {
    for (policy in listOf(PorticoPassthroughPolicy(disabled = true), PorticoPassthroughPolicy(off = setOf("dd", "ddp", "truehd", "dts", "dtshd", "dtsx")))) {
      assertTrue(policy.allowsMime(MimeTypes.AUDIO_RAW))
      for (mime in listOf(MimeTypes.AUDIO_AC3, MimeTypes.AUDIO_E_AC3, MimeTypes.AUDIO_E_AC3_JOC, MimeTypes.AUDIO_TRUEHD, MimeTypes.AUDIO_DTS, MimeTypes.AUDIO_DTS_HD, MimeTypes.AUDIO_DTS_X))
        assertFalse(mime, policy.allowsMime(mime))
      for (encoding in listOf(AudioFormat.ENCODING_AC3, AudioFormat.ENCODING_E_AC3, AudioFormat.ENCODING_E_AC3_JOC, AudioFormat.ENCODING_DOLBY_TRUEHD, AudioFormat.ENCODING_DTS, AudioFormat.ENCODING_DTS_HD, AudioFormat.ENCODING_DTS_HD_MA, AudioFormat.ENCODING_DTS_UHD_P1, AudioFormat.ENCODING_DTS_UHD_P2))
        assertFalse("$encoding", policy.allowsEncoding(encoding))
    }
    val dd = PorticoPassthroughPolicy(off = setOf("dd"))
    assertFalse(dd.allowsMime(MimeTypes.AUDIO_AC3))
    assertTrue("DD Off does not turn DD+ Off", dd.allowsMime(MimeTypes.AUDIO_E_AC3))
  }

  @Test
  fun wirePolicyIsStrictAndReadbackKeepsOnlyOffChoices() {
    val wire = mapOf("disabled" to false, "formats" to mapOf("dd" to "off", "dtshd" to "auto"))
    assertTrue(PorticoPassthroughPolicy.valid(wire))
    assertEquals(PorticoPassthroughPolicy(off = setOf("dd")), PorticoPassthroughPolicy.read(wire))
    assertTrue(PorticoPassthroughPolicy.valid(null))
    for (bad in listOf(true, mapOf("formats" to emptyMap<String, String>()), mapOf("disabled" to "true", "formats" to emptyMap<String, String>()),
        mapOf("disabled" to false, "formats" to mapOf("unknown" to "off")), mapOf("disabled" to false, "formats" to mapOf("dd" to "on"))))
      assertFalse("$bad", PorticoPassthroughPolicy.valid(bad))
  }

  private class Delegate {
    val calls = ArrayList<String>()
    var supported = true
    val sink = Proxy.newProxyInstance(AudioSink::class.java.classLoader, arrayOf(AudioSink::class.java)) { _, method, _ ->
      calls.add(method.name)
      when (method.name) {
        "supportsFormat" -> supported
        "getFormatSupport" -> if (supported) AudioSink.SINK_FORMAT_SUPPORTED_DIRECTLY else AudioSink.SINK_FORMAT_UNSUPPORTED
        "getFormatOffloadSupport" -> AudioOffloadSupport.DEFAULT_UNSUPPORTED
        else -> null
      }
    } as AudioSink
  }

  @Test
  fun disabledEncodingNeverReachesPassthroughOffloadOrConfiguration() {
    val delegate = Delegate()
    val sink = PorticoPassthroughSink(delegate.sink, PorticoPassthroughPolicy(off = setOf("dd"))) { _, _, _ -> AudioFormat.ENCODING_AC3 }
    val encoded = format(MimeTypes.AUDIO_AC3)
    assertFalse(sink.supportsFormat(encoded))
    assertEquals(AudioSink.SINK_FORMAT_UNSUPPORTED, sink.getFormatSupport(encoded))
    assertSame(AudioOffloadSupport.DEFAULT_UNSUPPORTED, sink.getFormatOffloadSupport(encoded))
    try {
      sink.configure(encoded, 0, null)
      fail("a disabled encoding must not be configured by any bypass path")
    } catch (_: AudioSink.ConfigurationException) {
      assertTrue(delegate.calls.isEmpty())
    }
    val pcm = format(MimeTypes.AUDIO_RAW)
    assertTrue(sink.supportsFormat(pcm))
    assertEquals(AudioSink.SINK_FORMAT_SUPPORTED_DIRECTLY, sink.getFormatSupport(pcm))
    sink.configure(pcm, 0, null)
    assertTrue(delegate.calls.contains("configure"))
  }

  @Test
  fun aDtsHdSinkCannotFallBackToCoreWhenCoreIsOff() {
    val delegate = Delegate()
    var output = AudioFormat.ENCODING_DTS
    val sink = PorticoPassthroughSink(delegate.sink, PorticoPassthroughPolicy(off = setOf("dts"))) { _, _, _ -> output }
    val hd = format(MimeTypes.AUDIO_DTS_HD)
    assertFalse(sink.supportsFormat(hd))
    assertEquals(AudioSink.SINK_FORMAT_UNSUPPORTED, sink.getFormatSupport(hd))
    assertTrue(delegate.calls.isEmpty())
    output = AudioFormat.ENCODING_DTS_HD
    assertTrue("the same source may pass through to a receiver that accepts HD", sink.supportsFormat(hd))
  }

  @Test
  fun automaticNeverManufacturesSinkSupport() {
    val delegate = Delegate().apply { supported = false }
    val sink = PorticoPassthroughSink(delegate.sink, PorticoPassthroughPolicy()) { _, _, _ -> error("no restrictions need a second capability probe") }
    assertFalse(sink.supportsFormat(format(MimeTypes.AUDIO_DTS_HD)))
    assertEquals(AudioSink.SINK_FORMAT_UNSUPPORTED, sink.getFormatSupport(format(MimeTypes.AUDIO_RAW)))
  }
}
