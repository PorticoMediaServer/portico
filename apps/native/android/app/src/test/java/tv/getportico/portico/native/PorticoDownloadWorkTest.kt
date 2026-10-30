package tv.getportico.portico.native

import java.io.File
import java.nio.file.Files
import java.util.Collections
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import org.junit.Test

/** Download file work: cancellation, replacement and independent downloads, and the partial file's identity. */
class PorticoDownloadWorkTest {
  @Test
  fun workForOneDownloadStaysInOrderWhileOthersRun() {
    val pool = Executors.newFixedThreadPool(2)
    val work = PorticoDownloadWork(pool)
    val held = CountDownLatch(1)
    val entered = CountDownLatch(1)
    val independent = CountDownLatch(1)
    val finished = CountDownLatch(1)
    val order = Collections.synchronizedList(ArrayList<String>())
    try {
      work.execute("film") {
        order.add("old-open")
        entered.countDown()
        check(held.await(5, TimeUnit.SECONDS))
        order.add("old-closed")
      }
      check(entered.await(5, TimeUnit.SECONDS))
      work.execute("film") { order.add("remove") }
      work.execute("film") {
        order.add("replacement")
        work.execute("film") { order.add("later"); finished.countDown() }
      }
      work.execute("another") { independent.countDown() }
      check(independent.await(5, TimeUnit.SECONDS))
      check(order.toList() == listOf("old-open"))
      held.countDown()
      check(finished.await(5, TimeUnit.SECONDS))
      check(order.toList() == listOf("old-open", "old-closed", "remove", "replacement", "later"))
    } finally { held.countDown(); pool.shutdownNow() }
  }

  @Test
  fun aPartialFileIsKeptOnlyForTheBytesItHolds() {
    val directory = Files.createTempDirectory("portico-download-partial").toFile()
    try {
      val part = File(directory, "partial.part")
      val first = "a".repeat(64)
      val next = "b".repeat(64)
      check(prepareDownloadPartial(directory, first))
      part.writeText("first file prefix")
      check(prepareDownloadPartial(directory, first))
      check(part.readText() == "first file prefix")
      // A request for the next digest may be cancelled before it runs. The manifest's desired
      // digest is then already next; only this marker establishes whose bytes actually arrived.
      check(prepareDownloadPartial(directory, next))
      check(!part.exists())
      part.writeText("next file prefix")
      check(prepareDownloadPartial(directory, next))
      check(part.readText() == "next file prefix")
      File(directory, "partial.sha256").writeText("damaged")
      check(prepareDownloadPartial(directory, next))
      check(!part.exists())
    } finally { directory.deleteRecursively() }
  }

  @Test
  fun aFinalFileIsRecoveredOnlyWithItsExactLengthAndDigest() {
    val directory = Files.createTempDirectory("portico-download-final").toFile()
    try {
      val file = File(directory, "media")
      val digest = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
      check(!verifiedDownloadFile(file, 3, digest) { false })
      file.writeText("abc")
      check(verifiedDownloadFile(file, 3, digest) { false })
      check(file.readText() == "abc")
      check(!verifiedDownloadFile(file, 2, digest) { false })
      check(!verifiedDownloadFile(file, 3, "0".repeat(64)) { false })
      check(!verifiedDownloadFile(file, 3, digest) { true })
      file.writeText("abd")
      check(!verifiedDownloadFile(file, 3, digest) { false })
      check(file.readText() == "abd")
    } finally { directory.deleteRecursively() }
  }

  @Test
  fun cancellationDuringTheFinalFileCheckNeverAdoptsIt() {
    val directory = Files.createTempDirectory("portico-download-final-cancel").toFile()
    try {
      val file = File(directory, "media").apply { writeText("abc") }
      var checks = 0
      val digest = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
      check(!verifiedDownloadFile(file, 3, digest) { ++checks > 1 })
      check(checks == 2 && file.readText() == "abc")
    } finally { directory.deleteRecursively() }
  }

  @Test
  fun onlyUnverifiedFilesNeedNetworkAdmission() {
    val directory = Files.createTempDirectory("portico-download-final-policy").toFile()
    try {
      val file = File(directory, "media").apply { writeText("abc") }
      val digest = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
      var networkChecks = 0
      val onCellular = { networkChecks++; true }
      check(downloadStart(verifiedDownloadFile(file, 3, digest) { false }, false, onCellular) == DownloadStart.ADOPT)
      check(networkChecks == 0 && file.readText() == "abc")
      file.writeText("abd")
      check(downloadStart(verifiedDownloadFile(file, 3, digest) { false }, false, onCellular) == DownloadStart.WAIT_FOR_WIFI)
      check(networkChecks == 1)
      check(downloadStart(false, false) { false } == DownloadStart.TRANSFER)
      check(downloadStart(false, true) { error("Cellular is allowed") } == DownloadStart.TRANSFER)
    } finally { directory.deleteRecursively() }
  }
}
