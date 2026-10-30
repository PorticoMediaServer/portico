package tv.getportico.portico.native

import java.io.File
import java.io.IOException
import java.security.InvalidKeyException
import java.security.UnrecoverableKeyException
import javax.crypto.SecretKey
import javax.crypto.spec.SecretKeySpec
import org.junit.Assert.*
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder

/** Real sealed files and store recovery; only Android Keystore and directory fsync are substituted. */
class PorticoRecordStoreTest {
  @get:Rule val temporary = TemporaryFolder()

  private class PermanentKeyLoss : InvalidKeyException("permanently invalidated")

  private class Environment(override val apiLevel: Int = 31) : PorticoRecordStore.Environment() {
    var heldKey: SecretKey? = null
    var readFailure: Exception? = null
    var invalidKey: SecretKey? = null
    var created = 0
    var failAt: String? = null
    var failAfter = false
    var refuse: String? = null
    val events = ArrayList<String>()
    private val counts = HashMap<String, Int>()

    fun clearEvents() { events.clear(); counts.clear() }

    private fun <T> step(name: String, action: () -> T): T {
      val count = (counts[name] ?: 0) + 1
      counts[name] = count
      val event = "$name:$count"
      events.add(event)
      if (event == failAt && !failAfter) throw IOException("interrupted $event")
      val result = action()
      if (event == failAt && failAfter) throw IOException("interrupted $event")
      return result
    }

    override fun readKey(): SecretKey? = step("read-key") {
      readFailure?.let { throw it }
      heldKey
    }

    override fun createKey(): SecretKey = step("create-key") {
      created++
      SecretKeySpec(ByteArray(32) { (it + created).toByte() }, "AES").also { heldKey = it }
    }

    override fun deleteKey() = step("delete-key") {
      heldKey = null
      readFailure = null
      invalidKey = null
    }

    override fun permanentlyInvalidated(error: Exception): Boolean = error is PermanentKeyLoss

    override fun verifyKey(key: SecretKey) = step("verify-key") {
      if (key === invalidKey) throw PermanentKeyLoss()
      super.verifyKey(key)
    }

    override fun writeSynced(file: File, vararg parts: ByteArray) = step("write-${file.name}") {
      super.writeSynced(file, *parts)
    }

    override fun rename(from: File, to: File): Boolean = step("rename-${to.name}") {
      if (refuse == "rename-${to.name}") false else super.rename(from, to)
    }
    override fun delete(file: File): Boolean = step("delete-${file.name}") {
      if (refuse == "delete-${file.name}") false else super.delete(file)
    }
    override fun deleteRecords(directory: File): Boolean = step("delete-records") {
      if (refuse == "delete-records") false else super.deleteRecords(directory)
    }

    // The JVM has no android.system.Os. The ordering and failure of this fence are controlled;
    // file contents, atomic renames, and FileDescriptor.sync still use the real filesystem.
    override fun syncDirectory(directory: File) = step("sync-directory") { Unit }
  }

  private class Fixture(val root: File, val environment: Environment) {
    val store = PorticoRecordStore(root, environment)
    val pending = File(root.parentFile, root.name + ".key-loss-pending")
    val notice = File(root.parentFile, root.name + ".key-loss-notice")
    fun reopen() = PorticoRecordStore(root, environment)
    fun files(): Map<String, List<Byte>> = if (!root.exists()) emptyMap() else root.walkTopDown()
        .filter { it.isFile }.associate { it.relativeTo(root).path to it.readBytes().toList() }
  }

  private fun seeded(apiLevel: Int = 31): Fixture {
    val fixture = Fixture(File(temporary.newFolder(), "records"), Environment(apiLevel))
    assertTrue(fixture.store.write(PorticoRecordStore.CREDENTIALS, "account", "credential".toByteArray()))
    assertTrue(fixture.store.write(PorticoRecordStore.QUEUES, "queue", "queue data".toByteArray()))
    assertEquals(2, fixture.files().size)
    fixture.environment.clearEvents()
    return fixture
  }

  private fun assertPreserved(fixture: Fixture, files: Map<String, List<Byte>>) {
    assertEquals(files, fixture.files())
    assertFalse(fixture.environment.events.any { it.startsWith("delete-records:") || it.startsWith("delete-key:") || it.startsWith("create-key:") })
  }

  @Test fun ambiguousKeyFailuresNeverEraseOrReplaceRecords() {
    for (api in listOf(30, 31)) {
      for (failure in listOf(IOException("keystore unavailable"), IllegalStateException("alias unavailable"), UnrecoverableKeyException("unrecoverable"))) {
        val fixture = seeded(api)
        val before = fixture.files()
        fixture.environment.readFailure = failure
        val expected = if (failure is UnrecoverableKeyException) RecordKeyLostException::class.java else failure.javaClass
        assertThrows(expected) { fixture.reopen().prepareSecureRecords() }
        assertPreserved(fixture, before)
        assertFalse(fixture.pending.exists())
        assertFalse(fixture.notice.exists())
        if (failure !is UnrecoverableKeyException) {
          assertThrows(expected) { fixture.reopen().resetAfterConfirmation() }
          assertPreserved(fixture, before)
        }
      }
    }
    val older = seeded(30)
    val before = older.files()
    older.environment.heldKey = null
    assertThrows(RecordKeyLostException::class.java) { older.reopen().prepareSecureRecords() }
    assertPreserved(older, before)
    assertFalse(older.pending.exists())
  }

  @Test fun verificationFailureDoesNotAuthorizeReset() {
    val fixture = seeded()
    val before = fixture.files()
    fixture.environment.failAt = "verify-key:1"
    assertThrows(IOException::class.java) { fixture.reopen().prepareSecureRecords() }
    assertPreserved(fixture, before)
    assertFalse(fixture.pending.exists())
    fixture.environment.clearEvents()
    assertThrows(IOException::class.java) { fixture.reopen().resetAfterConfirmation() }
    assertPreserved(fixture, before)
  }

  @Test fun definiteKeyLossResetsAllServicesAndReportsExactlyOnce() {
    for ((api, permanent) in listOf(31 to false, 30 to true, 31 to true)) {
      val fixture = seeded(api)
      val oldKey = fixture.environment.heldKey
      if (permanent) fixture.environment.invalidKey = oldKey else fixture.environment.heldKey = null
      val reopened = fixture.reopen()
      assertTrue(reopened.prepareSecureRecords())
      assertEquals(emptyMap<String, List<Byte>>(), fixture.files())
      assertNotSame(oldKey, fixture.environment.heldKey)
      assertEquals(2, fixture.environment.created)
      assertFalse(fixture.pending.exists())
      assertFalse(fixture.notice.exists())
      assertFalse(reopened.prepareSecureRecords())
      assertTrue(reopened.write(PorticoRecordStore.CREDENTIALS, "account", "replacement".toByteArray()))
      val read = reopened.read(PorticoRecordStore.CREDENTIALS, "account") as RecordRead.Value
      assertArrayEquals("replacement".toByteArray(), read.bytes)
    }
  }

  @Test fun confirmationRechecksTheKeyAndOnlyResetsIfItIsStillLost() {
    val recovered = seeded(30)
    val before = recovered.files()
    val key = recovered.environment.heldKey
    recovered.environment.heldKey = null
    val reopened = recovered.reopen()
    assertThrows(RecordKeyLostException::class.java) { reopened.prepareSecureRecords() }
    recovered.environment.heldKey = key
    assertFalse(reopened.resetAfterConfirmation())
    assertPreserved(recovered, before)

    for (unrecoverable in listOf(false, true)) {
      val fixture = seeded(30)
      if (unrecoverable) fixture.environment.readFailure = UnrecoverableKeyException("unrecoverable")
      else fixture.environment.heldKey = null
      val store = fixture.reopen()
      assertTrue(store.resetAfterConfirmation())
      assertTrue(store.prepareSecureRecords())
      assertFalse(store.prepareSecureRecords())
      assertTrue(fixture.files().isEmpty())
    }
  }

  @Test fun damagedCiphertextAndALaterMissingKeyDoNotAuthorizeReset() {
    val fixture = seeded()
    val file = File(File(fixture.root, PorticoRecordStore.CREDENTIALS),
        PorticoRecordStore.hex(java.security.MessageDigest.getInstance("SHA-256").digest("account".toByteArray())))
    val changed = file.readBytes().also { it[it.lastIndex] = (it.last().toInt() xor 1).toByte() }
    file.writeBytes(changed)
    val before = fixture.files()
    assertSame(RecordRead.Unreadable, fixture.reopen().read(PorticoRecordStore.CREDENTIALS, "account"))
    assertPreserved(fixture, before)

    fixture.environment.clearEvents()
    fixture.environment.heldKey = null
    // This owner already checked its key while writing the seeded records. It must not replace
    // a key that disappears later while ciphertext is still retained.
    assertFalse(fixture.store.write(PorticoRecordStore.CREDENTIALS, "new account", "new".toByteArray()))
    assertSame(RecordRead.Unreadable, fixture.store.read(PorticoRecordStore.QUEUES, "queue"))
    assertPreserved(fixture, before)
  }

  @Test fun anUnrecognizedResetMarkerCannotAuthorizeDeletion() {
    val fixture = seeded()
    val before = fixture.files()
    fixture.pending.writeText("not a reset proof")
    assertThrows(IllegalStateException::class.java) { fixture.reopen().prepareSecureRecords() }
    assertPreserved(fixture, before)
    assertEquals("not a reset proof", fixture.pending.readText())
  }

  @Test fun theDurableMarkerMustPrecedeEveryDestructiveStep() {
    for (step in listOf("write-records.key-loss-pending.new:1", "rename-records.key-loss-pending:1", "sync-directory:1", "sync-directory:2")) {
      val fixture = seeded()
      val before = fixture.files()
      fixture.environment.heldKey = null
      fixture.environment.failAt = step
      assertThrows("failure at $step", IOException::class.java) { fixture.reopen().prepareSecureRecords() }
      assertPreserved(fixture, before)
    }
  }

  @Test fun reopeningCompletesInterruptedResetsBeforeAcceptingNewRecords() {
    val steps = listOf(
        "delete-records:1", "sync-directory:3", "delete-key:1", "create-key:1", "verify-key:1",
        "write-records.key-loss-notice.new:1", "rename-records.key-loss-notice:1", "sync-directory:4",
        "delete-records.key-loss-pending:1", "sync-directory:5")
    for (step in steps) for (after in listOf(false, true)) {
      val fixture = seeded()
      fixture.environment.heldKey = null
      fixture.environment.failAt = step
      fixture.environment.failAfter = after
      assertThrows("failure ${if (after) "after" else "before"} $step", IOException::class.java) {
        fixture.reopen().prepareSecureRecords()
      }
      // Each reset either retains its proof marker or has already durably written the notice.
      assertTrue("restart evidence at $step", fixture.pending.exists() || fixture.notice.exists())
      fixture.environment.failAt = null
      fixture.environment.clearEvents()
      val reopened = fixture.reopen()
      assertTrue("notice after restarting at $step", reopened.prepareSecureRecords())
      assertTrue(fixture.files().isEmpty())
      assertFalse(fixture.pending.exists())
      assertFalse(fixture.notice.exists())
      assertFalse(reopened.prepareSecureRecords())
      assertTrue(reopened.write(PorticoRecordStore.QUEUES, "queue", "new queue".toByteArray()))
      assertArrayEquals("new queue".toByteArray(), (reopened.read(PorticoRecordStore.QUEUES, "queue") as RecordRead.Value).bytes)
    }
  }

  @Test fun filesystemRefusalsCannotBeReportedAsACompletedReset() {
    for (operation in listOf("rename-records.key-loss-pending", "delete-records", "rename-records.key-loss-notice", "delete-records.key-loss-pending", "delete-records.key-loss-notice")) {
      val fixture = seeded()
      fixture.environment.heldKey = null
      fixture.environment.refuse = operation
      assertThrows("refusal at $operation", IllegalStateException::class.java) { fixture.reopen().prepareSecureRecords() }
      if (operation == "rename-records.key-loss-pending" || operation == "delete-records") {
        assertEquals("records retained at $operation", 2, fixture.files().size)
        assertFalse(fixture.environment.events.any { it.startsWith("delete-key:") })
      }
      fixture.environment.refuse = null
      val reopened = fixture.reopen()
      assertTrue("notice after retrying $operation", reopened.prepareSecureRecords())
      assertTrue(fixture.files().isEmpty())
      assertFalse(reopened.prepareSecureRecords())
    }
  }
}
