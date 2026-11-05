package tv.getportico.portico.native

import android.util.Base64

private fun refusedBeforeDecode(value: String?) {
  Base64.decodeCalls = 0
  check(AvatarWireRuntime.decode(value) == null) { "Invalid image was accepted" }
  check(Base64.decodeCalls == 0) { "Rejected image reached the allocating decoder" }
}

fun main() {
  // The boundary deliberately accepts ignored punctuation, as Android does.
  check(Base64.decode("AA?==", Base64.NO_WRAP).contentEquals(byteArrayOf(0)))
  refusedBeforeDecode(null)
  refusedBeforeDecode("")
  for (bytes in listOf(byteArrayOf(0, -128, -1, 13, 10, 1), ByteArray(4194304) { -1 })) {
    val encoded = java.util.Base64.getEncoder().encodeToString(bytes)
    Base64.decodeCalls = 0
    check(AvatarWireRuntime.decode(encoded)?.contentEquals(bytes) == true)
    check(Base64.decodeCalls == 1)
  }
  // The first two share the full-size image's encoded length, with less padding.
  for (size in 4194305..4194307) refusedBeforeDecode(java.util.Base64.getEncoder().encodeToString(ByteArray(size)))
  refusedBeforeDecode("A".repeat(5592449))
  for (value in listOf("A===", "AA=", "AAA==", "A", "=", "====", "=AAA", "A=AA", "AA==A", "AAAA=", "AA===",
      "AA?==", "AA!==", "AA-==", "AA_==", "AA\u0000==", "AA\u00c5==")) refusedBeforeDecode(value)
  for (whitespace in listOf(" ", "\t", "\r", "\n", "\u000b", "\u000c", "\u00a0", "\u2003", "\u2028", "\u2029")) {
    refusedBeforeDecode(whitespace)
    Base64.decodeCalls = 0
    check(AvatarWireRuntime.decode("A${whitespace}A=${whitespace}=")?.contentEquals(byteArrayOf(0)) == true)
    check(Base64.decodeCalls == 1)
  }
  // Preserve optional padding and Android's accepted unused-bit behavior.
  for (value in listOf("AA", "AAA", "AAAA", "AB==", "AAB=")) {
    Base64.decodeCalls = 0
    check(AvatarWireRuntime.decode(value)?.contentEquals(java.util.Base64.getMimeDecoder().decode(value)) == true)
    check(Base64.decodeCalls == 1)
  }
  println("PASS production avatar decoder: exact bytes; overflow, alphabet and padding refused before allocation; accepted whitespace/optional padding")
}
