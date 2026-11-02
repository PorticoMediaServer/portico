package tv.getportico.portico.native

/**
 * ENGINE-CONTRACT: "A seek present in the command that opens the media is where the media opens."
 * The opening load itself is that seek, for every source (video, a rendered audio plan, a plain
 * file), so `ready` never describes 0:00 on a resume. A route resume keeps its own retained
 * position, a live channel seeks inside its window, and 0 is the media's own start.
 */
internal object PorticoOpeningSeek {
  data class Start(val revision: Long, val positionSeconds: Double)

  fun start(pendingSeek: Map<*, *>?, routeResume: Boolean, linear: Boolean): Start? {
    if (pendingSeek == null || routeResume || linear) return null
    val position = (pendingSeek["positionSeconds"] as? Number)?.toDouble() ?: return null
    val revision = (pendingSeek["revision"] as? Number)?.toLong() ?: return null
    if (!position.isFinite() || position <= 0 || revision < 0) return null
    return Start(revision, position)
  }
}
