package tv.getportico.portico.native

import org.junit.Test

class PorticoDownloadProgressTest {
  @Test
  fun aBlockedBookkeeperHoldsOneDrainAndOnlyTheLatestByteCount() {
    val queued = ArrayDeque<() -> Unit>()
    val published = ArrayList<Long>()
    val progress = PorticoDownloadProgress({ queued.addLast(it) }, { published.add(it) })
    for (bytes in 1L..10_000L) progress.offer(bytes)
    check(queued.size == 1)
    check(published.isEmpty())
    queued.removeFirst()()
    check(queued.isEmpty())
    check(published == listOf(10_000L))
    progress.offer(10_001L)
    check(queued.size == 1)
    queued.removeFirst()()
    check(published == listOf(10_000L, 10_001L))
  }

  @Test
  fun aNewValueDuringPublicationGetsItsOwnBoundedDrain() {
    val queued = ArrayDeque<() -> Unit>()
    val published = ArrayList<Long>()
    lateinit var progress: PorticoDownloadProgress
    progress = PorticoDownloadProgress({ queued.addLast(it) }) { bytes ->
      published.add(bytes)
      if (bytes == 1L) for (later in 2L..10_000L) progress.offer(later)
    }
    progress.offer(1L)
    queued.removeFirst()()
    check(queued.size == 1)
    queued.removeFirst()()
    check(published == listOf(1L, 10_000L))
    check(queued.isEmpty())
  }

  @Test
  fun transfersHaveIndependentDrainsAndCanReportRestartedBytes() {
    val queued = ArrayDeque<() -> Unit>()
    val first = ArrayList<Long>()
    val second = ArrayList<Long>()
    val one = PorticoDownloadProgress({ queued.addLast(it) }, { first.add(it) })
    val two = PorticoDownloadProgress({ queued.addLast(it) }, { second.add(it) })
    one.offer(1000L)
    two.offer(20L)
    one.offer(10L) // An If-Range refusal can restart a partial file from its beginning.
    two.offer(2000L)
    check(queued.size == 2)
    while (queued.isNotEmpty()) queued.removeFirst()()
    check(first == listOf(10L))
    check(second == listOf(2000L))
  }
}
