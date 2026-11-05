package android.util

/** Permissive byte boundary; refusals must happen before this allocating call.
 * Android codec parity is checked separately; this is not a device implementation. */
object Base64 {
  const val NO_WRAP = 2
  var decodeCalls = 0
  fun decode(value: String, flags: Int): ByteArray {
    check(flags == NO_WRAP)
    decodeCalls++
    // Android also permits whitespace between padding characters; Java does not.
    return java.util.Base64.getMimeDecoder().decode(value.filterNot { it.isWhitespace() })
  }
}
