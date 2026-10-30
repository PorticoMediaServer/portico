package tv.getportico.portico.native

import android.media.AudioDeviceInfo
import android.media.AudioFormat
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class PorticoCapabilityFactsTest {
  @Test
  fun dolbyVisionProfilesStayWithTheirReportedBaseCodec() {
    val reported = setOf(4, 5, 7, 8, 9, 10)
    for ((codec, expected) in listOf("hevc" to setOf(4, 5, 7, 8), "h264" to setOf(9), "av1" to setOf(10), "vp9" to emptySet())) {
      val decoder = PorticoVideoDecoder(codec)
      decoder.addDolbyVisionProfiles(reported)
      assertEquals(codec, expected, decoder.dolbyVision)
    }
    val hevc = PorticoVideoDecoder("hevc").apply { addDolbyVisionProfiles(setOf(10)) }
    assertTrue("an AV1-only Dolby Vision report cannot qualify HEVC", hevc.decodedRanges().isEmpty())
    val av1 = PorticoVideoDecoder("av1").apply { addDolbyVisionProfiles(setOf(8, 9)) }
    assertTrue("AV1 needs an actual profile 10 report", av1.decodedRanges().isEmpty())
    av1.addDolbyVisionProfiles(setOf(10))
    assertEquals(listOf("dolby_vision"), av1.decodedRanges())
  }

  @Test
  fun everyDolbyVisionDecoderPublishesItsDecodedRangeWithoutADisplayGate() {
    for ((codec, profile) in listOf("hevc" to 8, "h264" to 9, "av1" to 10)) {
      val decoder = PorticoVideoDecoder(codec)
      decoder.dolbyVision.add(profile)
      assertEquals(listOf("dolby_vision"), decoder.decodedRanges())
      decoder.hdr.add("hdr10")
      assertEquals(listOf("hdr10", "dolby_vision"), decoder.decodedRanges())
      decoder.hdr.add("dolby_vision")
      assertEquals("no duplicate range", 1, decoder.decodedRanges().count { it == "dolby_vision" })
    }
    assertTrue("no decoder evidence means no Dolby Vision claim", PorticoVideoDecoder("hevc").decodedRanges().isEmpty())
  }

  private fun decoder(width: Int, height: Int, hardware: Boolean = true) = PorticoVideoDecoder("hevc").apply {
    this.hardware = hardware
    maxWidth = width; maxHeight = height; maxFrameRate = 30; maxBitrate = 40_000_000
  }

  @Test
  fun largerDecoderNeverBorrowsTheSmallerDecodersProfilesOrLimits() {
    val large = decoder(3840, 2160).apply { profiles.add("main"); maxLevel = 153 }
    val small = decoder(1920, 1080).apply {
      profiles.add("main_10"); tenBit = true; hdr.add("hdr10")
      maxFrameRate = 120; maxLevel = 186; maxBitrate = 100_000_000
    }
    assertTrue(large.preferredOver(small))
    assertFalse(small.preferredOver(large))
    assertEquals(setOf("main"), large.profiles)
    assertEquals(153, large.maxLevel)
    assertEquals(40_000_000, large.maxBitrate)
    assertFalse(large.tenBit)
    assertTrue(large.decodedRanges().isEmpty())
  }

  @Test
  fun hardwareWinsOverSoftwareAndTiesPreferTheFasterSingleDecoder() {
    val chip = decoder(1920, 1080)
    val software = decoder(7680, 4320, hardware = false)
    assertTrue(chip.preferredOver(software))
    assertFalse(software.preferredOver(chip))
    assertTrue(decoder(1920, 1080).apply { maxFrameRate = 60 }.preferredOver(chip))
    assertTrue(decoder(1920, 1080).apply { maxBitrate = 80_000_000 }.preferredOver(chip))
    assertFalse(decoder(0, 0).preferredOver(null))
  }

  @Test
  fun dtsHdCarriesTheLosslessVariantsButNeverInventsDtsx() {
    val hd = PorticoCapabilityAudio.passthrough(listOf(AudioFormat.ENCODING_DTS_HD), 8).single()
    assertEquals("dts", hd.codec)
    assertEquals(8, hd.channels)
    assertEquals(listOf("dts_core", "dts_hd_hra", "dts_hd_ma"), hd.variants)
    assertTrue(hd.objectAudio.isEmpty())
    val core = PorticoCapabilityAudio.passthrough(listOf(AudioFormat.ENCODING_DTS), 8).single()
    assertEquals(listOf("dts_core"), core.variants)
    assertEquals(6, core.channels)
  }

  @Test
  fun uhdAddsDtsxAndKeepsItsObjectAudioSeparate() {
    for (uhd in listOf(AudioFormat.ENCODING_DTS_UHD_P1, AudioFormat.ENCODING_DTS_UHD_P2)) {
      val formats = PorticoCapabilityAudio.passthrough(listOf(AudioFormat.ENCODING_DTS, AudioFormat.ENCODING_DTS_HD, uhd), 8).single()
      assertEquals(listOf("dts_core", "dts_hd_hra", "dts_hd_ma", "dtsx"), formats.variants)
      assertEquals(listOf("dtsx"), formats.objectAudio)
    }
    val dolby = PorticoCapabilityAudio.passthrough(listOf(AudioFormat.ENCODING_E_AC3, AudioFormat.ENCODING_E_AC3_JOC), 6).single()
    assertEquals(listOf("atmos"), dolby.objectAudio)
    assertEquals(6, dolby.channels)
    assertTrue(dolby.variants.isEmpty())
  }

  private val speaker = PorticoOutputDevice(AudioDeviceInfo.TYPE_BUILTIN_SPEAKER, listOf(2), emptyList())
  private val optical = PorticoOutputDevice(AudioDeviceInfo.TYPE_LINE_DIGITAL, listOf(6), listOf(AudioFormat.ENCODING_AC3, AudioFormat.ENCODING_DTS))
  private val hdmi = PorticoHdmiPlug(8, listOf(AudioFormat.ENCODING_E_AC3, AudioFormat.ENCODING_DTS_HD))

  @Test
  fun opticalBeatsTheListedSpeakerAndUsesOnlyItsOwnEncodings() {
    val output = PorticoCapabilityAudio.output(listOf(speaker, optical), television = true, hdmi = hdmi)
    assertEquals("optical", output.route)
    assertTrue(output.digital)
    assertEquals(optical.encodings, output.encodings)
    assertEquals(6, output.channels)
    assertEquals(setOf("ac3", "dts"), PorticoCapabilityAudio.passthrough(output.encodings, output.channels).map { it.codec }.toSet())
  }

  @Test
  fun unlistedHdmiSinkIsTheSameOutputForRouteAndPassthrough() {
    val output = PorticoCapabilityAudio.output(listOf(speaker), television = true, hdmi = hdmi)
    assertEquals("hdmi", output.route)
    assertTrue(output.digital)
    assertEquals(8, output.channels)
    assertEquals(hdmi.encodings, output.encodings)
    assertTrue(PorticoCapabilityAudio.passthrough(output.encodings, output.channels).any { "dts_hd_ma" in it.variants })
    assertEquals("speaker", PorticoCapabilityAudio.output(listOf(speaker), television = false, hdmi = hdmi).route)
  }

  @Test
  fun headphonesBluetoothAndUsbKeepTheHdmiFallbackOut() {
    for (type in listOf(AudioDeviceInfo.TYPE_WIRED_HEADPHONES, AudioDeviceInfo.TYPE_BLUETOOTH_A2DP, AudioDeviceInfo.TYPE_USB_HEADSET)) {
      val output = PorticoCapabilityAudio.output(listOf(speaker, optical, PorticoOutputDevice(type, listOf(2), emptyList())), television = true, hdmi = hdmi)
      assertFalse(output.digital)
      assertTrue(output.encodings.isEmpty())
    }
    assertFalse(PorticoCapabilityAudio.output(emptyList(), television = true, hdmi = null).digital)
  }
}
