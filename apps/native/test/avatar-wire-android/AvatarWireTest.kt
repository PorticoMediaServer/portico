package tv.getportico.portico.native

fun main() {
  check(AvatarWireRuntime.decode(null) == null)
  check(AvatarWireRuntime.decode("") == null)
  for (bytes in listOf(byteArrayOf(0, -128, -1, 13, 10, 1), ByteArray(4194304) { -1 })) {
    val encoded = java.util.Base64.getEncoder().encodeToString(bytes)
    check(AvatarWireRuntime.decode(encoded)?.contentEquals(bytes) == true)
  }
  val oversized = java.util.Base64.getEncoder().encodeToString(ByteArray(4194305))
  check(AvatarWireRuntime.decode(oversized) == null)
  check(AvatarWireRuntime.decode("A".repeat(5592449)) == null)
  println("PASS production avatar decoder: canonical bytes, full-size image, empty/oversized bounds")
}
