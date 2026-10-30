package tv.getportico.portico

/** Executes the production pure DEBUG decision. Android API signatures compile
 * against the actual SDK jar; no fake Android URI parser or device state is used. */
fun main() {
  val kitGood = listOf("portico://kit", "portico://kit/", "portico://kit?gallery=controls",
    "portico://kit/?gallery=shell-owner-dashboard-all&window=1")
  for (raw in kitGood) check(PorticoUrlRoutes.debugKitRoute(raw) == if (BuildConfig.DEBUG) raw else null)
  val kitBad = listOf("portico://user@kit", "portico://user:password@kit", "portico://kit:80",
    "portico://kit#", "portico://kit#payload", "portico://kit/path", "portico://kit?",
    "portico://kit?window=1", "portico://kit?gallery=", "portico://kit?gallery=a&window=0",
    "portico://kit?gallery=a&unknown=x", "portico://kit?gallery=a&gallery=b",
    "portico://kit?gallery=a&window=1&window=1", "portico://kit?gallery=a&",
    "portico://kit?%67allery=a", "portico://kit?gallery=%61", "portico://kit?gallery=a%0A",
    "portico://kit?gallery=" + "a".repeat(129), "portico://KIT", "PORTICO://kit",
    "portico://%6bit", "https://kit", "portico:kit", "portico:////kit", " portico://kit",
    "portico://kit\n", "portico://kit?gallery=a#payload")
  for (raw in kitBad) check(PorticoUrlRoutes.debugKitRoute(raw) == null) { "Unexpected kit forwarding result" }
  println("Native kit route DEBUG=" + BuildConfig.DEBUG + ": " + (kitGood.size + kitBad.size) + " cases PASS")
  val good = "portico://native-layout?specimen=signed-out-welcome"
  val refused = "portico://native-layout"
  check(PorticoUrlRoutes.debugMeasurementRoute(good, "native-layout") == if (BuildConfig.DEBUG) good else null)
  val malformed = listOf(
    refused to "native-layout",
    "portico://native-layout?specimen=unknown" to "native-layout",
    "portico://native-layout?specimen=" to "native-layout",
    "portico://native-layout?specimen" to "native-layout",
    "$good&specimen=signed-out-welcome" to "native-layout",
    "$good&server=https://fixture.invalid" to "native-layout",
    "$good&password=synthetic-value" to "native-layout",
    "$good&runtime=injected" to "native-layout",
    "$good&" to "native-layout",
    "$good#" to "native-layout",
    "$good#synthetic-value" to "native-layout",
    "portico://native-layout/?specimen=signed-out-welcome" to "native-layout",
    "portico://native-layout/path?specimen=signed-out-welcome" to "native-layout",
    "portico://user@example@native-layout?specimen=signed-out-welcome" to "native-layout",
    "portico://user:synthetic-value@native-layout?specimen=signed-out-welcome" to "native-layout",
    "portico://native-layout:1?specimen=signed-out-welcome" to "native-layout",
    "portico://native-layout?%73pecimen=signed-out-welcome" to "native-layout",
    "portico://native-layout?specimen=signed-out-%77elcome" to "native-layout",
    "portico://%6eative-layout?specimen=signed-out-welcome" to "native-layout",
    "portico://NATIVE-LAYOUT?specimen=signed-out-welcome" to "NATIVE-LAYOUT",
    "PORTICO://native-layout?specimen=signed-out-welcome" to "native-layout",
    "portico://native-layout-bad?specimen=signed-out-welcome" to "native-layout-bad",
    "portico://native-layout%zz?specimen=signed-out-welcome" to null,
    "portico:native-layout?specimen=signed-out-welcome" to null,
    "portico:/native-layout?specimen=signed-out-welcome" to null,
    "portico:////native-layout?specimen=signed-out-welcome" to null,
    "portico:\\native-layout?specimen=signed-out-welcome" to null,
    "https://native-layout?specimen=signed-out-welcome" to "native-layout",
    " $good " to null,
  )
  for ((raw, host) in malformed) {
    val result = PorticoUrlRoutes.debugMeasurementRoute(raw, host)
    check(result == if (BuildConfig.DEBUG) refused else null) { "Unexpected measurement forwarding result" }
    if (result != null) check(result == refused && '?' !in result && '#' !in result && '@' !in result)
  }
  val ordinary = listOf(
    "portico://item/item-id" to "item", "portico://play/item-id" to "play",
    "portico://account?tab=security" to "account", "portico://downloads" to "downloads",
    "portico://device" to "device", "portico://together/join/ABCD" to "together",
    "https://web.getportico.tv/account" to "web.getportico.tv",
    "portico://account-proof?server=https://fixture.invalid" to "account-proof",
    "https://fixture.invalid/native-layout" to "fixture.invalid",
    "portico://item/native-layout" to "item",
  )
  for ((raw, host) in ordinary) check(PorticoUrlRoutes.debugMeasurementRoute(raw, host) == null)
  println("Native measurement route DEBUG=" + BuildConfig.DEBUG + ": " + (malformed.size + ordinary.size + 1) + " cases PASS")
}
