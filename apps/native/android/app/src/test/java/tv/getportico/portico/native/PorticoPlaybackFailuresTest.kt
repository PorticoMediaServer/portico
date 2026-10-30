package tv.getportico.portico.native

import androidx.media3.common.PlaybackException
import org.junit.Assert.assertEquals
import org.junit.Test

class PorticoPlaybackFailuresTest {
  private fun expect(code: Int, status: Int = 0, network: Boolean = false,
                     delivery: Boolean = false, engine: String = "", uncertain: Boolean = false) {
    assertEquals("error $code / HTTP $status", mapOf(
        "networkError" to network, "deliveryError" to delivery, "httpStatus" to status,
        "engineCode" to engine, "uncertain" to uncertain), PorticoPlaybackFailures.classify(code, status))
  }

  @Test fun actualFactValuesCoverEveryNativeErrorCategory() {
    for (code in listOf(PlaybackException.ERROR_CODE_IO_NETWORK_CONNECTION_FAILED,
        PlaybackException.ERROR_CODE_IO_NETWORK_CONNECTION_TIMEOUT, PlaybackException.ERROR_CODE_IO_UNSPECIFIED)) {
      expect(code, network = true)
    }
    for (code in listOf(PlaybackException.ERROR_CODE_BEHIND_LIVE_WINDOW, PlaybackException.ERROR_CODE_IO_BAD_HTTP_STATUS,
        PlaybackException.ERROR_CODE_IO_INVALID_HTTP_CONTENT_TYPE, PlaybackException.ERROR_CODE_IO_FILE_NOT_FOUND,
        PlaybackException.ERROR_CODE_IO_NO_PERMISSION, PlaybackException.ERROR_CODE_IO_CLEARTEXT_NOT_PERMITTED,
        PlaybackException.ERROR_CODE_IO_READ_POSITION_OUT_OF_RANGE)) {
      expect(code, delivery = true)
    }
    for (code in listOf(PlaybackException.ERROR_CODE_DECODER_INIT_FAILED, PlaybackException.ERROR_CODE_DECODER_QUERY_FAILED,
        PlaybackException.ERROR_CODE_DECODING_FAILED, PlaybackException.ERROR_CODE_DECODING_FORMAT_EXCEEDS_CAPABILITIES,
        PlaybackException.ERROR_CODE_DECODING_FORMAT_UNSUPPORTED)) {
      expect(code, engine = "decode_error")
    }
    for (code in listOf(PlaybackException.ERROR_CODE_PARSING_CONTAINER_MALFORMED, PlaybackException.ERROR_CODE_PARSING_CONTAINER_UNSUPPORTED,
        PlaybackException.ERROR_CODE_PARSING_MANIFEST_MALFORMED, PlaybackException.ERROR_CODE_PARSING_MANIFEST_UNSUPPORTED)) {
      expect(code, engine = "source_not_supported")
    }
    for (code in listOf(PlaybackException.ERROR_CODE_TIMEOUT, PlaybackException.ERROR_CODE_REMOTE_ERROR,
        PlaybackException.ERROR_CODE_DECODING_RESOURCES_RECLAIMED, PlaybackException.ERROR_CODE_AUDIO_TRACK_INIT_FAILED,
        PlaybackException.ERROR_CODE_AUDIO_TRACK_WRITE_FAILED)) {
      expect(code, uncertain = true)
    }
    expect(PlaybackException.ERROR_CODE_UNSPECIFIED)
  }

  @Test fun httpEvidenceOverridesDecoderAndTransportErrorClasses() {
    for (code in listOf(PlaybackException.ERROR_CODE_DECODING_FAILED,
        PlaybackException.ERROR_CODE_IO_NETWORK_CONNECTION_FAILED, PlaybackException.ERROR_CODE_TIMEOUT)) {
      for (status in listOf(400, 401, 403, 404, 410, 499, 600)) expect(code, status, delivery = true)
      for (status in listOf(408, 429, 500, 502, 503, 599)) expect(code, status, network = true, delivery = true)
    }
  }
}
