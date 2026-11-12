package tv.getportico.portico.native

import java.io.File
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.RuntimeEnvironment
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [28], manifest = Config.NONE)
class PorticoAppLogTest {
  @Test fun theLastRunIsPrivateBoundedAndCanBeCleared() {
    val app = RuntimeEnvironment.getApplication()
    val log = PorticoAppLog(app)
    log.write(null)
    assertNull(log.read())
    log.write("first run")
    assertTrue(File(app.noBackupFilesDir, "app.log").isFile)
    assertEquals("first run", PorticoAppLog(app).read())
    log.write("next run")
    assertEquals("next run", log.read())
    try { log.write("x".repeat(1024 * 1024 + 1)); fail("an oversized log was stored") }
    catch (_: IllegalArgumentException) { }
    assertEquals("next run", log.read())
    log.write(null)
    assertNull(PorticoAppLog(app).read())
  }
}
