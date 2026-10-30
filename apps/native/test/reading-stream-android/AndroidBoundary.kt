package android.util

object Base64 {
  const val NO_WRAP = 2
  fun encodeToString(bytes: ByteArray, flags: Int): String {
    check(flags == NO_WRAP)
    return java.util.Base64.getEncoder().encodeToString(bytes)
  }
}
