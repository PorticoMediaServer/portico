package tv.getportico.portico.native

import java.util.UUID
import org.junit.Test

/** The native guard rejects stale grants before file or network work. The process singleton's
 * bookkeeping is seeded directly so these refusal tests need no Android Context or downloader. */
class PorticoDownloadGenerationTest {
  @Test
  fun resumeRequiresTheSameLiveEntryGeneration() {
    val center = PorticoDownloadCenter
    val type = center.javaClass
    val loaded = type.getDeclaredField("loaded").apply { isAccessible = true }
    @Suppress("UNCHECKED_CAST") val entries = type.getDeclaredField("entries").apply { isAccessible = true }.get(center) as MutableMap<String, MutableMap<String, Any?>>
    @Suppress("UNCHECKED_CAST") val generations = type.getDeclaredField("publicationGenerations").apply { isAccessible = true }.get(center) as MutableMap<String, PorticoDownloadCenter.PublicationGeneration>
    val accepts = type.getDeclaredMethod("isGeneration", String::class.java, String::class.java).apply { isAccessible = true }
    val wasLoaded = loaded.getBoolean(center)
    val id = UUID.randomUUID().toString()
    val old = hashMapOf<String, Any?>("id" to id, "state" to "paused", "bytes" to 3.0, "sha256" to "a".repeat(64))
    val original = PorticoDownloadCenter.PublicationGeneration(old)
    val request = mapOf<String, Any?>("id" to id, "expectedGeneration" to original.value,
        "url" to "https://example.invalid/never-requested", "bytes" to 3.0, "sha256" to "a".repeat(64))
    try {
      loaded.setBoolean(center, true)
      check(center.resume(request) == null && !entries.containsKey(id))
      entries[id] = old; generations[id] = original
      check(accepts.invoke(center, id, original.value) == true)
      check(center.resume(request + ("expectedGeneration" to "stale")) == null)
      check(entries[id] === old)
      original.retired = true
      check(center.resume(request) == null && entries[id] === old)
      original.retired = false
      val replacement = HashMap(old)
      entries[id] = replacement
      check(center.resume(request) == null && entries[id] === replacement)
      entries.remove(id)
      check(center.resume(request) == null && !entries.containsKey(id))
    } finally {
      entries.remove(id); generations.remove(id); loaded.setBoolean(center, wasLoaded)
    }
  }
}
