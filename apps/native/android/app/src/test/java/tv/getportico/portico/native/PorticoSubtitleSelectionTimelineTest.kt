package tv.getportico.portico.native

import java.io.IOException
import okhttp3.Call
import okhttp3.Callback
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.Protocol
import okhttp3.Request
import okhttp3.Response
import okhttp3.ResponseBody.Companion.toResponseBody
import okio.Timeout
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/** Runs the real owner, URL checks and JSON parser with controlled time and HTTP completions. */
@RunWith(RobolectricTestRunner::class)
@Config(manifest = Config.NONE, sdk = [28])
class PorticoSubtitleSelectionTimelineTest {
  @Test fun initialDocumentAndConditionalRecheckUseTheAcceptedResource() {
    val fixture = Fixture()
    assertEquals(1, fixture.http.calls.size)
    val initial = fixture.http.calls.single()
    assertEquals("https://server.invalid/v1/media/subtitles/grant-a/resource-a/1", initial.request().url.toString())
    assertEquals("application/json", initial.request().header("Accept"))
    assertNull(initial.request().header("If-None-Match"))
    initial.respond(200, document("first"), "\"first\"")
    fixture.loop.runCurrent()
    assertEquals(PorticoSubtitleSelection.Display("first", italic = true, top = true), fixture.display)
    assertEquals("", fixture.statuses.last())

    fixture.loop.advanceTo(24_999)
    assertEquals(1, fixture.http.calls.size)
    fixture.loop.advanceTo(25_000)
    assertEquals(2, fixture.http.calls.size)
    assertEquals(initial.request().url, fixture.http.calls.last().request().url)
    assertEquals("\"first\"", fixture.http.calls.last().request().header("If-None-Match"))
    fixture.owner.invalidate()
    assertTrue(fixture.http.calls.last().isCanceled())
    assertEquals(0, fixture.loop.pendingCount)
  }

  @Test fun transportFailuresRetainCuesRetryAfterFiveSecondsAndNeverRenewTheGrant() {
    for (failure in listOf("io", "500", "503")) {
      val fixture = Fixture()
      fixture.accept("held")
      fixture.loop.advanceTo(25_000)
      val recheck = fixture.http.calls.last()
      if (failure == "io") recheck.fail() else recheck.respond(failure.toInt(), "unavailable")
      fixture.loop.runCurrent()
      assertEquals("transient $failure erased an authorized cue", "held", fixture.display.text)
      assertTrue(fixture.statuses.last().isNotEmpty())
      fixture.loop.advanceTo(29_999)
      assertEquals("$failure retried early", 2, fixture.http.calls.size)
      fixture.loop.advanceTo(30_000)
      assertEquals("$failure did not retry", 3, fixture.http.calls.size)
      assertEquals("\"held\"", fixture.http.calls.last().request().header("If-None-Match"))

      // Leave the retry pending. Neither a failed answer nor an in-flight request
      // grants any time beyond the first successful answer's forty seconds.
      fixture.loop.advanceTo(40_000)
      fixture.owner.tick(POSITION_US)
      assertEquals("grant must include its exact deadline", "held", fixture.display.text)
      fixture.loop.advanceTo(40_001)
      fixture.owner.tick(POSITION_US)
      assertEquals("$failure extended an expired grant", "", fixture.display.text)
      fixture.loop.advanceTo(41_000)
      val expiredRetry = fixture.http.calls.last()
      if (failure == "io") expiredRetry.fail() else expiredRetry.respond(failure.toInt(), "unavailable")
      fixture.loop.runCurrent()
      assertEquals("$failure after expiry revived a cue", "", fixture.display.text)
      fixture.loop.advanceTo(45_999)
      assertEquals("$failure after expiry retried early", 3, fixture.http.calls.size)
      assertEquals("", fixture.display.text)
      fixture.loop.advanceTo(46_000)
      assertEquals("$failure after expiry did not retry", 4, fixture.http.calls.size)
      assertEquals("$failure after expiry renewed the grant", "", fixture.display.text)
      fixture.owner.invalidate()
      assertEquals(0, fixture.loop.pendingCount)
    }
  }

  @Test fun unchangedResponseRenewsFromCompletionRatherThanRequestStart() {
    val fixture = Fixture()
    fixture.accept("renewed")
    fixture.loop.advanceTo(25_000)
    val recheck = fixture.http.calls.last()
    fixture.loop.advanceTo(30_000)
    recheck.respond(304)
    fixture.loop.runCurrent()
    assertEquals("renewed", fixture.display.text)
    assertEquals("", fixture.statuses.last())

    fixture.loop.advanceTo(40_001)
    fixture.owner.tick(POSITION_US)
    assertEquals("304 failed to renew the previous grant", "renewed", fixture.display.text)
    fixture.loop.advanceTo(65_001)
    fixture.owner.tick(POSITION_US)
    assertEquals("renewal was measured from the request instead of its completion", "renewed", fixture.display.text)
    fixture.loop.advanceTo(70_000)
    fixture.owner.tick(POSITION_US)
    assertEquals("renewed", fixture.display.text)
    fixture.loop.advanceTo(70_001)
    fixture.owner.tick(POSITION_US)
    assertEquals("", fixture.display.text)
    fixture.owner.invalidate()
  }

  @Test fun theTimerClearsExpiredCuesEvenWithoutAnotherPositionTick() {
    val fixture = Fixture()
    fixture.accept("held")
    fixture.loop.advanceTo(25_000)
    fixture.http.calls.last().fail()
    fixture.loop.runCurrent()
    fixture.loop.advanceTo(40_999)
    assertEquals("held", fixture.display.text)
    fixture.loop.advanceTo(41_000)
    assertEquals("the first timer tick after expiry retained a cue", "", fixture.display.text)
    fixture.owner.invalidate()
  }

  @Test fun permanentRefusalsAndInvalidDocumentsClearTheHeldCuesImmediately() {
    val answers = listOf(
      403 to "forbidden",
      200 to "not JSON",
      200 to document("wrong version").replace("\"version\":1", "\"version\":2"),
      200 to document("wrong domain").replace("source-relative", "other"),
    )
    for ((code, body) in answers) {
      val fixture = Fixture()
      fixture.accept("held")
      fixture.loop.advanceTo(25_000)
      fixture.http.calls.last().respond(code, body)
      fixture.loop.runCurrent()
      assertEquals("$code/$body retained refused cues", "", fixture.display.text)
      assertTrue(fixture.statuses.last().isNotEmpty())
      fixture.owner.tick(POSITION_US)
      assertEquals("a position update revived refused cues", "", fixture.display.text)
      fixture.loop.advanceTo(50_000)
      assertNull("a discarded document cannot authorize a conditional renewal", fixture.http.calls.last().request().header("If-None-Match"))
      fixture.owner.invalidate()
    }
  }

  @Test fun canceledGenerationCannotOverwriteItsSuccessorWithSuccessOrFailure() {
    for (failure in listOf(false, true)) {
      val fixture = Fixture()
      fixture.accept("previous")
      fixture.loop.advanceTo(25_000)
      val previous = fixture.http.calls.last()
      fixture.select("resource-b", "binding-b")
      assertTrue(previous.isCanceled())
      assertEquals(3, fixture.http.calls.size)
      val successor = fixture.http.calls.last()
      assertNull(successor.request().header("If-None-Match"))
      successor.respond(200, document("successor"), "\"successor\"")
      fixture.loop.runCurrent()
      assertEquals("successor", fixture.display.text)
      val statusCount = fixture.statuses.size

      // Cancellation can race with delivery. Complete the old callback after
      // the successor has already published its own parsed document.
      if (failure) previous.fail() else previous.respond(200, document("stale"), "\"stale\"")
      fixture.loop.runCurrent()
      assertEquals("stale completion overwrote the successor", "successor", fixture.display.text)
      assertEquals("stale failure changed the successor status", statusCount, fixture.statuses.size)
      fixture.owner.invalidate()
    }
  }

  @Test fun offCancelsTimersAndLateQueuedCallbacksCannotReopenTheSelection() {
    val fixture = Fixture()
    val pending = fixture.http.calls.single()
    // Queue a completion, then turn subtitles off before main-thread delivery.
    pending.respond(200, document("too late"), "\"late\"")
    fixture.owner.apply(null, null)
    assertTrue(pending.isCanceled())
    assertFalse(fixture.owner.overlayActive)
    fixture.loop.runCurrent()
    assertEquals("", fixture.display.text)
    assertTrue(fixture.statuses.isEmpty())
    assertEquals(0, fixture.loop.pendingCount)
    fixture.loop.advanceTo(100_000)
    fixture.owner.tick(POSITION_US)
    assertEquals("", fixture.display.text)
    assertEquals("the invalidated timer made another request", 1, fixture.http.calls.size)
  }

  private class Fixture {
    val loop = ManualScheduler()
    val http = ControlledHttp()
    val owner = PorticoSubtitleSelection(elapsedRealtime = { loop.now }, main = loop, calls = http)
    var display = PorticoSubtitleSelection.Display("", italic = false, top = false)
    val statuses = mutableListOf<String>()

    init {
      owner.onText = { display = it }
      owner.onStatus = { statuses.add(it) }
      owner.tick(POSITION_US)
      select()
    }

    fun select(resource: String = "resource-a", binding: String = "binding-a") {
      owner.apply(mapOf(
        "mode" to "track", "revision" to 1, "resourceRevision" to 1,
        "resourceId" to resource, "offsetUs" to "0", "binding" to binding,
        "documentUrl" to "https://server.invalid/v1/media/subtitles/grant-a/$resource/1",
      ), "https://server.invalid/v1/media/grant-a/video")
    }

    fun accept(text: String) {
      http.calls.last().respond(200, document(text), "\"$text\"")
      loop.runCurrent()
      assertEquals(text, display.text)
    }
  }

  private class ManualScheduler : PorticoSubtitleSelection.Scheduler {
    private data class Entry(val at: Long, val order: Long, val action: Runnable)
    private val entries = mutableListOf<Entry>()
    private var order = 0L
    var now = 0L
      private set
    val pendingCount: Int get() = entries.size

    override fun post(action: Runnable) { postDelayed(action, 0) }
    override fun postDelayed(action: Runnable, delayMs: Long) {
      require(delayMs >= 0)
      entries.add(Entry(now + delayMs, order++, action))
    }
    override fun removeCallbacks(action: Runnable) { entries.removeAll { it.action === action } }
    fun runCurrent() { advanceTo(now) }
    fun advanceTo(target: Long) {
      require(target >= now)
      var steps = 0
      while (true) {
        val next = entries.filter { it.at <= target }.minWithOrNull(compareBy<Entry> { it.at }.thenBy { it.order }) ?: break
        check(++steps <= 10_000) { "subtitle dispatch did not settle" }
        entries.remove(next)
        now = next.at
        next.action.run()
      }
      now = target
    }
  }

  private class ControlledHttp : Call.Factory {
    val calls = mutableListOf<ControlledCall>()
    override fun newCall(request: Request): Call = ControlledCall(request).also { calls.add(it) }
  }

  /** Deliberately permits a canceled call's callback to model a delivery race. */
  private class ControlledCall(private val accepted: Request) : Call {
    private var callback: Callback? = null
    private var canceled = false
    private var delivered = false
    private val callTimeout = Timeout()
    override fun request(): Request = accepted
    override fun execute(): Response = throw AssertionError("subtitle owner must enqueue its request")
    override fun enqueue(responseCallback: Callback) {
      check(callback == null) { "request enqueued twice" }
      callback = responseCallback
    }
    override fun cancel() { canceled = true }
    override fun isExecuted(): Boolean = callback != null
    override fun isCanceled(): Boolean = canceled
    override fun timeout(): Timeout = callTimeout
    override fun clone(): Call = ControlledCall(accepted)

    fun respond(code: Int, body: String = "", etag: String? = null) {
      val receiver = completion()
      val response = Response.Builder().request(accepted).protocol(Protocol.HTTP_1_1)
        .code(code).message("fixture").header("Content-Type", "application/json")
        .body(body.toResponseBody("application/json".toMediaType()))
      if (etag != null) response.header("ETag", etag)
      receiver.onResponse(this, response.build())
    }

    fun fail() { completion().onFailure(this, IOException("injected transport failure")) }

    private fun completion(): Callback {
      check(!delivered) { "request completed twice" }
      val receiver = checkNotNull(callback) { "request was not enqueued" }
      delivered = true
      return receiver
    }
  }

  companion object {
    private const val POSITION_US = 2_000_000L
    private fun document(text: String) = """{"version":1,"timeDomain":"source-relative","cues":[{"startUs":"0","endUs":"120000000","text":"$text","italic":true,"top":true}]}"""
  }
}
