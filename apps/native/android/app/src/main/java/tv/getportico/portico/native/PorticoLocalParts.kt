package tv.getportico.portico.native

/** A downloaded title keeps one clock across its private, ordered files. */
internal data class PorticoLocalPart(val url: String, val offset: Double, val duration: Double, val start: Double, val end: Double?, val sourceOffset: Double?)
internal object PorticoLocalParts {
  fun read(value: Any?, source: String, allowed: (String) -> Boolean): List<PorticoLocalPart> {
    if (value == null) return emptyList()
    val rows = value as? List<*> ?: error("Invalid downloaded files.")
    check(rows.size > 1)
    var offset = 0.0
    return rows.map { row ->
      val part = row as? Map<*, *> ?: error("Invalid downloaded file.")
      val url = part["url"] as? String ?: error("Invalid downloaded file.")
      check(url.startsWith("file:") && allowed(url))
      fun seconds(key: String): Double = (part[key] as? Number)?.toDouble()?.also { check(it.isFinite() && it >= 0) } ?: error("Invalid downloaded clock.")
      val at = seconds("offsetSeconds"); val duration = seconds("durationSeconds"); val start = seconds("startSeconds")
      val end = part["endSeconds"]?.let { seconds("endSeconds") }
      val sourceOffset = part["sourceOffsetSeconds"]?.let { seconds("sourceOffsetSeconds") }
      check(kotlin.math.abs(at - offset) < 0.002 && duration > 0 && (end == null || end > start))
      offset = at + duration
      PorticoLocalPart(url, at, duration, start, end, sourceOffset)
    }.also { check(it.first().url == source) }
  }
  fun target(parts: List<PorticoLocalPart>, seconds: Double): Pair<Int, Long> {
    val index = parts.indexOfLast { seconds >= it.offset }.coerceAtLeast(0)
    return index to ((seconds - parts[index].offset).coerceIn(0.0, parts[index].duration) * 1000).toLong()
  }
  fun sourceTime(parts: List<PorticoLocalPart>, seconds: Double): Double {
    val part = parts.lastOrNull { seconds >= it.offset } ?: return seconds
    return (part.sourceOffset ?: part.offset) + seconds - part.offset
  }
  /** mpv's EDL is built here from admitted files, never accepted as a caller supplied source. */
  fun edl(parts: List<PorticoLocalPart>, path: (String) -> String): String = "edl://" + parts.joinToString(";") {
    val file = path(it.url)
    "%${file.toByteArray(Charsets.UTF_8).size}%$file,${it.start},${it.duration}"
  }
}
