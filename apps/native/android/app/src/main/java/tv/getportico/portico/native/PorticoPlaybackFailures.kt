package tv.getportico.portico.native

import androidx.media3.common.PlaybackException

/** The actual emitted classification, independent of an Android player or clock. */
internal object PorticoPlaybackFailures {
  fun classify(errorCode: Int, httpStatus: Int): HashMap<String, Any?> {
    val delivery = httpStatus >= 400 || errorCode in DELIVERY_ERRORS
    val network = if (httpStatus >= 400) httpStatus in 500..599 || httpStatus == 408 || httpStatus == 429
        else errorCode in NETWORK_ERRORS
    val engine = when (errorCode) {
      in DECODE_ERRORS -> "decode_error"
      in SOURCE_ERRORS -> "source_not_supported"
      else -> ""
    }
    val fact = HashMap<String, Any?>()
    fact["networkError"] = network
    fact["deliveryError"] = delivery
    fact["httpStatus"] = httpStatus
    // A time-out, or a failure in another process, is not evidence about the media.
    fact["uncertain"] = !network && !delivery && engine.isEmpty() && errorCode in UNCERTAIN_ERRORS
    fact["engineCode"] = if (network || delivery) "" else engine
    return fact
  }

  private val NETWORK_ERRORS = setOf(
      PlaybackException.ERROR_CODE_IO_NETWORK_CONNECTION_FAILED, PlaybackException.ERROR_CODE_IO_NETWORK_CONNECTION_TIMEOUT,
      PlaybackException.ERROR_CODE_IO_UNSPECIFIED)
  private val DELIVERY_ERRORS = setOf(
      PlaybackException.ERROR_CODE_BEHIND_LIVE_WINDOW, PlaybackException.ERROR_CODE_IO_BAD_HTTP_STATUS,
      PlaybackException.ERROR_CODE_IO_INVALID_HTTP_CONTENT_TYPE, PlaybackException.ERROR_CODE_IO_FILE_NOT_FOUND,
      PlaybackException.ERROR_CODE_IO_NO_PERMISSION, PlaybackException.ERROR_CODE_IO_CLEARTEXT_NOT_PERMITTED,
      PlaybackException.ERROR_CODE_IO_READ_POSITION_OUT_OF_RANGE)
  private val DECODE_ERRORS = setOf(
      PlaybackException.ERROR_CODE_DECODER_INIT_FAILED, PlaybackException.ERROR_CODE_DECODER_QUERY_FAILED,
      PlaybackException.ERROR_CODE_DECODING_FAILED, PlaybackException.ERROR_CODE_DECODING_FORMAT_EXCEEDS_CAPABILITIES,
      PlaybackException.ERROR_CODE_DECODING_FORMAT_UNSUPPORTED)
  private val SOURCE_ERRORS = setOf(
      PlaybackException.ERROR_CODE_PARSING_CONTAINER_MALFORMED, PlaybackException.ERROR_CODE_PARSING_CONTAINER_UNSUPPORTED,
      PlaybackException.ERROR_CODE_PARSING_MANIFEST_MALFORMED, PlaybackException.ERROR_CODE_PARSING_MANIFEST_UNSUPPORTED)
  private val UNCERTAIN_ERRORS = setOf(
      PlaybackException.ERROR_CODE_TIMEOUT, PlaybackException.ERROR_CODE_REMOTE_ERROR,
      PlaybackException.ERROR_CODE_DECODING_RESOURCES_RECLAIMED,
      PlaybackException.ERROR_CODE_AUDIO_TRACK_INIT_FAILED, PlaybackException.ERROR_CODE_AUDIO_TRACK_WRITE_FAILED)
}
