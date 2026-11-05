package android.util

/** Canonical base64 only; the production method and byte checks are compiled unchanged. */
object Base64 {
  const val NO_WRAP = 2
  fun decode(value: String, flags: Int): ByteArray {
    check(flags == NO_WRAP)
    return java.util.Base64.getDecoder().decode(value)
  }
}
