package tv.getportico.portico.native

import com.facebook.react.bridge.JavaOnlyMap
import com.facebook.react.bridge.Promise
import com.facebook.react.bridge.WritableMap
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicReference
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/** The change feed is cancelled from the JavaScript thread while its reader thread is inside a read
 * (a profile switch, sign-out or server switch). That must end the read, not throw on the canceller. */
@RunWith(RobolectricTestRunner::class)
@Config(manifest = Config.NONE, sdk = [28])
class PorticoEventStreamCancelTest {
  private class Answer : Promise {
    val done = CountDownLatch(1); val value = AtomicReference<Any?>(); val code = AtomicReference<String?>()
    override fun resolve(value: Any?) { this.value.set(value); done.countDown() }
    override fun reject(code: String, message: String?) { this.code.set(code); done.countDown() }
    override fun reject(code: String, throwable: Throwable?) = reject(code, throwable?.message)
    override fun reject(code: String, message: String?, throwable: Throwable?) = reject(code, message)
    override fun reject(throwable: Throwable) = reject("error", throwable.message)
    override fun reject(throwable: Throwable, userInfo: WritableMap) = reject("error", throwable.message)
    override fun reject(code: String, userInfo: WritableMap) = reject(code, null as String?)
    override fun reject(code: String, throwable: Throwable?, userInfo: WritableMap) = reject(code, throwable?.message)
    override fun reject(code: String, message: String?, userInfo: WritableMap) = reject(code, message)
    override fun reject(code: String?, message: String?, throwable: Throwable?, userInfo: WritableMap?) = reject(code ?: "error", message)
    @Deprecated("") override fun reject(message: String) = reject("error", message)
    fun await(): Answer { assertTrue("no answer within 10 s", done.await(10, TimeUnit.SECONDS)); return this }
  }

  @Test fun cancellingWhileAReadIsInFlightEndsTheReadInsteadOfThrowing() {
    PorticoEventStream.newMap = { JavaOnlyMap() }
    val server = MockWebServer()
    // The first chunk arrives at once; the next is held back, so the second read blocks in the socket.
    server.enqueue(MockResponse().setHeader("Content-Type", "text/event-stream")
        .setChunkedBody(": connected\n\n" + "x".repeat(4096), 13).throttleBody(13, 20, TimeUnit.SECONDS))
    server.start()
    try {
      val owner = Any()
      val opened = Answer(); PorticoEventStream.open(owner, "feed", server.url("/v1/events").toString(), null, opened)
      assertNull(opened.await().code.get())
      val first = Answer(); PorticoEventStream.read("feed", first)
      assertNull(first.await().code.get())
      val second = Answer(); PorticoEventStream.read("feed", second)
      Thread.sleep(500) // the reader is now inside its read
      // Before the fix this threw IllegalStateException "Unbalanced enter/exit" from OkHttp, here, on the canceller.
      val thrown = runCatching { PorticoEventStream.cancel("feed") }.exceptionOrNull()
      assertNull("cancel threw: $thrown", thrown)
      assertEquals("event_cancelled", second.await().code.get())
      val after = Answer(); PorticoEventStream.read("feed", after)
      assertEquals("event_local", after.await().code.get())
    } finally { runCatching { server.shutdown() } }
  }
}
