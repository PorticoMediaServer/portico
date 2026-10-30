package tv.getportico.portico.native

/** Production pure policy/math only. Native attachment/span/paint require an actual collector. */
fun main() {
  val uri = "portico://native-layout?specimen=signed-out-welcome"
  val action = "android.intent.action.VIEW"
  var checks = 0
  fun verify(ok: Boolean) { check(ok); checks++ }
  verify(LayoutObservationPolicy.Stage.entries.map { it.code }.toSet() == setOf("admission", "ui-current", "root-resolve", "root-attachment",
    "root-walk", "pre-observation-scope", "observation", "post-observation-scope", "response"))
  for (stage in LayoutObservationPolicy.Stage.entries) {
    verify(LayoutObservationPolicy.failure(stage, false) == mapOf("nativeObservationStage" to stage.code))
    verify(LayoutObservationPolicy.failure(stage, true) == mapOf("nativeObservationStage" to stage.code + "-exception"))
  }
  verify(LayoutObservationPolicy.ScopeCheck.entries.map { it.code } == listOf("activity-intent-current", "root-tag-current", "root-attached",
    "decor-attached", "decor-current", "root-window-current", "decor-window-current", "root-in-decor", "fixture-found",
    "fixture-tag-current", "fixture-attached", "fixture-window-current", "fixture-in-root"))
  verify(LayoutObservationPolicy.scopeFailure { true } == null)
  for (failed in LayoutObservationPolicy.ScopeCheck.entries) {
    val read = mutableListOf<LayoutObservationPolicy.ScopeCheck>()
    verify(LayoutObservationPolicy.scopeFailure { read.add(it); it != failed } == failed)
    verify(read == LayoutObservationPolicy.ScopeCheck.entries.take(failed.ordinal + 1))
    for (stage in listOf(LayoutObservationPolicy.Stage.PRE_OBSERVATION_SCOPE, LayoutObservationPolicy.Stage.POST_OBSERVATION_SCOPE))
      verify(LayoutObservationPolicy.failure(stage, false, failed) == mapOf("nativeObservationStage" to stage.code, "nativeObservationScopeCheck" to failed.code))
    for (stage in LayoutObservationPolicy.Stage.entries) {
      verify("nativeObservationScopeCheck" !in LayoutObservationPolicy.failure(stage, true, failed))
      if (stage != LayoutObservationPolicy.Stage.PRE_OBSERVATION_SCOPE && stage != LayoutObservationPolicy.Stage.POST_OBSERVATION_SCOPE)
        verify("nativeObservationScopeCheck" !in LayoutObservationPolicy.failure(stage, false, failed))
    }
  }
  verify(LayoutObservationPolicy.admitted(true, action, uri, 1.0, 2.0, listOf(1.0, 2.0, 3.0)))
  verify(LayoutObservationPolicy.admitted(true, action, uri, 1.0, 2.0, emptyList()))
  verify(LayoutObservationPolicy.admitted(true, action, uri, 1.0, 2.0, (1..2000).map(Int::toDouble)))
  verify(!LayoutObservationPolicy.admitted(true, action, uri, 1.0, 2.0, (1..2001).map(Int::toDouble)))
  verify(!LayoutObservationPolicy.admitted(false, action, uri, 1.0, 2.0, listOf(3.0)))
  for (badAction in listOf(null, "android.intent.action.MAIN", "android.intent.action.SEND"))
    verify(!LayoutObservationPolicy.publicIntent(true, badAction, uri))
  for (bad in listOf(null, "$uri&", "$uri#", "$uri&server=https://fixture.invalid", "$uri&specimen=other",
      uri.replace("signed-out-welcome", "unknown"), uri.replace("portico:", "PORTICO:"), " $uri", "$uri ",
      uri.replace("?", "/?"), uri.replace("specimen", "%73pecimen")))
    verify(!LayoutObservationPolicy.publicIntent(true, action, bad))
  for (bad in listOf(Double.NaN, Double.POSITIVE_INFINITY, -1.0, 0.0, 1.5, 2147483648.0)) {
    verify(!LayoutObservationPolicy.admitted(true, action, uri, bad, 2.0, listOf(3.0)))
    verify(!LayoutObservationPolicy.admitted(true, action, uri, 1.0, bad, listOf(3.0)))
    verify(!LayoutObservationPolicy.admitted(true, action, uri, 1.0, 2.0, listOf(bad)))
  }
  verify(!LayoutObservationPolicy.admitted(true, action, uri, 1.0, 1.0, emptyList()))
  verify(!LayoutObservationPolicy.admitted(true, action, uri, 1.0, 2.0, listOf(3.0, 3.0)))
  data class Node(val tag: Int, val children: List<Node> = emptyList())
  val fixture = Node(2, listOf(Node(3)))
  val root = Node(1, listOf(fixture, Node(4)))
  fun collect(node: Node, wanted: Set<Int>) = LayoutObservationPolicy.collect(node, wanted, Node::tag, { it.children.size }, { n, i -> n.children[i] })
  val found = collect(root, setOf(1, 2, 3, 8))!!
  verify(found.keys == setOf(1, 2, 3)); verify(found[2] === fixture); verify(collect(fixture, setOf(1, 4))!!.isEmpty())
  verify(collect(Node(1, listOf(Node(2), Node(2))), setOf(2)) == null)
  verify(collect(Node(1, List(20000) { Node(it + 2) }), emptySet()) == null)
  // The boundary cannot allocate/read even the first child of an oversized root.
  verify(LayoutObservationPolicy.collect(1, emptySet(), { it }, { Int.MAX_VALUE }, { _, _ -> error("unbounded child lookup") }) == null)
  val budget = LayoutObservationPolicy.TextBudget()
  verify(!budget.lines(-1)); verify(budget.lines(4096)); verify(!budget.lines(1))
  repeat(4096) { check(budget.run()) }; verify(!budget.run())
  verify(LayoutObservationPolicy.opacity(listOf(0.5, 0.4, 1.0)) == 0.2)
  verify(LayoutObservationPolicy.opacity(listOf(1.0, 0.0, 0.5)) == 0.0)
  for (bad in listOf(-0.1, 1.1, Double.NaN, Double.POSITIVE_INFINITY)) verify(LayoutObservationPolicy.opacity(listOf(1.0, bad)) == null)
  verify(LayoutObservationPolicy.opacity(emptyList()) == null)
  verify(LayoutObservationPolicy.uniform(listOf(0, 0)) == 0)
  verify(LayoutObservationPolicy.uniform(listOf(0xffeeeeee.toInt(), 0xffeeeeee.toInt())) == 0xffeeeeee.toInt())
  verify(LayoutObservationPolicy.uniform(listOf(400, 400)) == 400)
  verify(LayoutObservationPolicy.uniform(listOf(450, 450)) == 450)
  verify(LayoutObservationPolicy.uniform(listOf(88.0, 88.0)) == 88.0)
  verify(LayoutObservationPolicy.uniform(listOf(400, 800)) == null)
  verify(LayoutObservationPolicy.uniform(listOf(88.0, 44.0)) == null)
  verify(LayoutObservationPolicy.uniform(listOf<Int?>(400, null)) == null)
  verify(LayoutObservationPolicy.uniform(listOf<Int?>(null, null)) == null)
  verify(LayoutObservationPolicy.uniform(emptyList<Int>()) == null)
  verify(LayoutObservationPolicy.fontStyle(false, 0.0) == "normal")
  verify(LayoutObservationPolicy.fontStyle(true, 0.0) == "italic")
  verify(LayoutObservationPolicy.fontStyle(null, 0.0) == null)
  for (skew in listOf(-0.25, 0.25, Double.NaN, Double.POSITIVE_INFINITY)) {
    verify(LayoutObservationPolicy.fontStyle(false, skew) == null)
    verify(LayoutObservationPolicy.fontStyle(true, skew) == null)
  }
  verify(LayoutObservationPolicy.uniform(listOf(LayoutObservationPolicy.fontStyle(false, 0.0), LayoutObservationPolicy.fontStyle(true, 0.0))) == null)
  verify(kotlin.math.abs(LayoutObservationPolicy.letterSpacing(-0.045, 88.0, 1.0)!! + 3.96) < 0.0000001)
  verify(kotlin.math.abs(LayoutObservationPolicy.letterSpacing(0.1, 126.0, 3.0)!! - 4.2) < 0.0000001)
  verify(LayoutObservationPolicy.letterSpacing(0.0, 126.0, 3.0) == 0.0)
  verify(LayoutObservationPolicy.letterSpacing(0.5, 48.0, 3.0) == LayoutObservationPolicy.letterSpacing(1.0, 24.0, 3.0))
  for (bad in listOf(0.0, -1.0, Double.NaN, Double.POSITIVE_INFINITY)) {
    verify(LayoutObservationPolicy.letterSpacing(0.1, bad, 3.0) == null)
    verify(LayoutObservationPolicy.letterSpacing(0.1, 88.0, bad) == null)
  }
  verify(LayoutObservationPolicy.letterSpacing(Double.NaN, 88.0, 3.0) == null)
  verify(LayoutObservationPolicy.letterSpacing(Double.MAX_VALUE, 88.0, 3.0) == null)
  verify(LayoutObservationPolicy.decoration(false, false) == "none")
  verify(LayoutObservationPolicy.decoration(true, false) == "underline")
  verify(LayoutObservationPolicy.decoration(false, true) == "line-through")
  verify(LayoutObservationPolicy.decoration(true, true) == "underline line-through")
  verify(LayoutObservationPolicy.noTextShadow(0.0, true, true))
  verify(!LayoutObservationPolicy.noTextShadow(0.0, false, true))
  verify(!LayoutObservationPolicy.noTextShadow(0.0, true, false))
  for (radius in listOf(-1.0, 0.1, Double.NaN, Double.POSITIVE_INFINITY)) verify(!LayoutObservationPolicy.noTextShadow(radius, true, true))
  verify(LayoutObservationPolicy.ImageMechanism.entries.map { it.code }.toSet() == setOf("fit-xy", "fit-x", "fit-y", "fit-start", "fit-center", "fit-end", "center",
    "center-inside", "center-crop", "focus-crop", "fit-bottom-start", "matrix"))
  class CustomScale {
    override fun equals(other: Any?) = true
    override fun hashCode() = 0
  }
  val knownScale = CustomScale()
  val scales = listOf(knownScale to LayoutObservationPolicy.ImageMechanism.FIT_CENTER)
  verify(LayoutObservationPolicy.imageMechanism(knownScale, scales) == "fit-center")
  verify(LayoutObservationPolicy.imageMechanism(CustomScale(), scales) == null)
  verify(LayoutObservationPolicy.imageMechanism(null, scales) == null)
  verify(LayoutObservationPolicy.imageMechanism(knownScale, emptyList()) == null)
  val box = doubleArrayOf(105.0, 210.0, 1275.0, 2742.0)
  val mappedOrigin = doubleArrayOf(105.0, 210.0)
  val nativeOrigin = doubleArrayOf(15.0, 72.0)
  val window = LayoutObservationPolicy.windowRect(box, mappedOrigin, nativeOrigin, 3.0)!!
  verify(window == mapOf("x" to 5.0, "y" to 24.0, "width" to 390.0, "height" to 844.0))
  // Negative position, transformed AABB extending left of mapped local origin,
  // and non-zero window origin stay visible; none are normalized to zero.
  verify(LayoutObservationPolicy.windowRect(doubleArrayOf(-10.0, 5.0, 50.0, 95.0), doubleArrayOf(20.0, 5.0), doubleArrayOf(-6.0, 12.0), 3.0) ==
    mapOf("x" to -12.0, "y" to 4.0, "width" to 20.0, "height" to 30.0))
  verify(LayoutObservationPolicy.windowRect(box, mappedOrigin, nativeOrigin, 1.0)!!.getValue("y") == 72.0)
  for (bad in listOf(0.0, -1.0, Double.NaN, Double.POSITIVE_INFINITY)) verify(LayoutObservationPolicy.windowRect(box, mappedOrigin, nativeOrigin, bad) == null)
  verify(LayoutObservationPolicy.windowRect(doubleArrayOf(1.0, 1.0, 0.0, 0.0), mappedOrigin, nativeOrigin, 3.0) == null)
  verify(LayoutObservationPolicy.windowRect(doubleArrayOf(Double.NaN, 1.0, 2.0, 3.0), mappedOrigin, nativeOrigin, 3.0) == null)
  verify(LayoutObservationPolicy.windowRect(box, doubleArrayOf(1.0), nativeOrigin, 3.0) == null)
  verify(LayoutObservationPolicy.windowRect(box, mappedOrigin, doubleArrayOf(1.0, Double.NaN), 3.0) == null)
  verify(LayoutObservationPolicy.rgba(0xffff0000.toInt()) == "rgba(255, 0, 0, 1)")
  verify(LayoutObservationPolicy.rgba(0) == "rgba(0, 0, 0, 0)")
  verify(LayoutObservationPolicy.rgba(0x8000ff00.toInt()) == "rgba(0, 255, 0, 0.50196078)")
  fun background(alpha: List<Double>) = LayoutObservationPolicy.rgba(LayoutObservationPolicy.background(listOf(0xffff0000.toInt(), 0xff0000ff.toInt()), alpha))
  verify(background(listOf(0.5, 1.0)) == "rgba(127.5, 0, 127.5, 1)")
  verify(background(listOf(0.0, 1.0)) == "rgba(0, 0, 255, 1)")
  verify(background(listOf(1.0, 0.0)) == "rgba(0, 0, 0, 0)")
  verify(background(listOf(1.0, 0.5)) == "rgba(255, 0, 0, 0.5)")
  println("Layout observation policy/math: $checks checks PASS (no native paint/device assertion)")
}
