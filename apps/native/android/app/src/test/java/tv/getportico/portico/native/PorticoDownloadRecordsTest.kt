package tv.getportico.portico.native

import java.io.File
import java.io.IOException
import java.nio.file.Files
import org.junit.Test

class PorticoDownloadRecordsTest {
  private fun entry(id: String, state: String = "downloading") = hashMapOf<String, Any?>(
      "id" to id, "state" to state, "sha256" to "a".repeat(64), "bytes" to 42.0,
      "intent" to "run", "waiting" to "network", "error" to "network",
  )

  @Test
  fun onlyAnAbsentOrSuccessfullyDecodedRecordCanBeWritten() {
    val directory = Files.createTempDirectory("portico-download-record").toFile()
    try {
      val file = File(directory, "state.json")
      val record = PorticoDownloadRecord(file, load = { if (it.exists()) it.readBytes() else null })
      check(!record.write("before-read"))
      check(!file.exists())
      check(record.read { it } == null)
      check(record.write("{\"version\":3}"))
      check(file.readText() == "{\"version\":3}")
      check(record.read { it } == "{\"version\":3}")
    } finally { directory.deleteRecursively() }
  }

  @Test
  fun anUnreadableRecordDoesNotTurnIntoAnEmptyWritableRecord() {
    val directory = Files.createTempDirectory("portico-download-unreadable").toFile()
    try {
      val file = File(directory, "state.json").apply { writeText("original bytes") }
      var writes = 0
      val record = PorticoDownloadRecord(file, load = { throw IOException("unreadable") }, store = { _, _ -> writes++; true })
      var failed = false
      try { record.read { it ?: "empty" } } catch (_: IOException) { failed = true }
      check(failed)
      check(!record.write("replacement"))
      check(writes == 0 && file.readText() == "original bytes")
    } finally { directory.deleteRecursively() }
  }

  @Test
  fun aDecoderRefusalKeepsAllOriginalBytesAndBlocksFurtherWrites() {
    val directory = Files.createTempDirectory("portico-download-corrupt").toFile()
    try {
      val file = File(directory, "manifest.json").apply { writeText("[]") }
      val record = PorticoDownloadRecord(file, load = { it.readBytes() })
      var failed = false
      try { record.read { decodeDownloadManifest(emptyList<Any?>()) } } catch (_: IllegalStateException) { failed = true }
      check(failed)
      check(!record.write("{}"))
      check(file.readText() == "[]")
    } finally { directory.deleteRecursively() }
  }

  @Test
  fun invalidUtf8AndEmptyExistingRecordsAreNotAbsence() {
    val directory = Files.createTempDirectory("portico-download-encoding").toFile()
    try {
      val file = File(directory, "state.json")
      for (bytes in listOf(byteArrayOf(0xc3.toByte(), 0x28), byteArrayOf())) {
        file.writeBytes(bytes)
        val record = PorticoDownloadRecord(file, load = { it.readBytes() })
        var failed = false
        try { record.read { check(it == null || it.isNotEmpty()); it } } catch (_: Exception) { failed = true }
        check(failed)
        check(!record.write("{}"))
        check(file.readBytes().contentEquals(bytes))
      }
    } finally { directory.deleteRecursively() }
  }

  @Test
  fun aFailedRereadRevokesEarlierWriteAdmission() {
    var unreadable = false
    var writes = 0
    val record = PorticoDownloadRecord(File("unused"), load = {
      if (unreadable) throw IOException("unreadable") else "{}".toByteArray()
    }, store = { _, _ -> writes++; true })
    record.read { it }
    check(record.write("{}"))
    unreadable = true
    var failed = false
    try { record.read { it } } catch (_: IOException) { failed = true }
    check(failed && !record.write("{}") && writes == 1)
  }

  @Test
  fun malformedManifestMembersRefuseTheWholeRead() {
    val original = entry("kept")
    for (bad in listOf<Any?>(null, emptyList<Any?>(), entry("wrong-id"), entry("bad", "unknown"), entry("bad").apply { put("bytes", -1.0) })) {
      var failed = false
      try { decodeDownloadManifest(linkedMapOf("kept" to original, "bad" to bad)) } catch (_: IllegalStateException) { failed = true }
      check(failed)
      check(original["state"] == "downloading")
    }
    val decoded = decodeDownloadManifest(mapOf("kept" to original, "removed" to entry("removed", "removed")))
    check(decoded.getValue("kept")["state"] == "paused")
    check(decoded.getValue("kept")["intent"] == "run")
    check(decoded.getValue("removed")["state"] == "removed")
  }

  @Test
  fun failedPauseAndRemovalCommitsKeepTheOriginalRow() {
    val original = entry("download")
    val entries = hashMapOf<String, MutableMap<String, Any?>>("download" to original)
    for (state in listOf("paused", "removed")) {
      val next = HashMap(original).apply { put("state", state) }
      var attempted = false
      check(!commitDownloadEntry(entries, "download", next) {
        attempted = true
        check(entries.getValue("download")["state"] == state)
        false
      })
      check(attempted && entries["download"] === original)
      check(original["state"] == "downloading")
    }
  }

  @Test
  fun aCommittedRemovalSurvivesManifestReloadAsATombstone() {
    val original = entry("download")
    val entries = hashMapOf<String, MutableMap<String, Any?>>("download" to original)
    var durable: Map<String, MutableMap<String, Any?>> = emptyMap()
    val removed = HashMap(original).apply { put("state", "removed") }
    check(commitDownloadEntry(entries, "download", removed) {
      durable = entries.mapValues { HashMap(it.value) }
      true
    })
    entries.remove("download")
    check(decodeDownloadManifest(durable).getValue("download")["state"] == "removed")
  }

  @Test
  fun aRestoredOrFailedRunIntentCanBeDurablyHeldWithoutAnActiveTransfer() {
    for (state in listOf("downloading", "paused", "failed")) {
      val original = entry("download", state)
      val entries = hashMapOf<String, MutableMap<String, Any?>>("download" to original)
      val paused = checkNotNull(pausedDownloadEntry(original, "hold"))
      var durable: Map<String, MutableMap<String, Any?>> = emptyMap()
      check(commitDownloadEntry(entries, "download", paused) {
        durable = entries.mapValues { HashMap(it.value) }
        true
      })
      val restored = decodeDownloadManifest(durable).getValue("download")
      check(restored["state"] == "paused" && restored["intent"] == "hold")
      check(restored["waiting"] == "" && restored["error"] == "")
      check(original["state"] == state && original["intent"] == "run")
    }
    check(pausedDownloadEntry(entry("ready", "ready"), "hold") == null)
    check(pausedDownloadEntry(entry("removed", "removed"), "hold") == null)
    check(pausedDownloadEntry(null, "hold") == null)
  }
}
