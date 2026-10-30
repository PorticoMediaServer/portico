package tv.getportico.portico.native

/**
 * A channel's clock as mpv sees it. The platform players place themselves on a channel by the
 * playlist's program dates; mpv reports media time instead (with `rebase-start-time` off: the
 * stream's own timestamps). The server publishes, per stretch of the channel's timeline, the
 * offset from media time to channel position (`presentation.linear.clocks`), and the coordinator
 * hands it to this engine as `linearClock`: oldest first, the first without a start.
 *
 * MPEG-TS timestamps wrap every 2^33 ticks of 90 kHz (about 26.5 hours) and FFmpeg unwraps them
 * from wherever it started, so a media time is matched modulo that period.
 */
internal class PorticoMpvLinearClock private constructor(private val epochs: List<Epoch>) {
  private class Epoch(val startSeconds: Double, val offsetSeconds: Double)

  /** The channel position (seconds from the channel's origin) of a media time, or NaN. */
  fun position(media: Double): Double {
    if (!media.isFinite()) return Double.NaN
    for (i in epochs.indices.reversed()) {
      val epoch = epochs[i]
      val end = if (i + 1 < epochs.size) epochs[i + 1].startSeconds else Double.POSITIVE_INFINITY
      var position = media + epoch.offsetSeconds
      val anchor = when {
        epoch.startSeconds.isFinite() -> epoch.startSeconds
        end.isFinite() -> end
        else -> position
      }
      position += WRAP_SECONDS * Math.rint((anchor - position) / WRAP_SECONDS)
      if (position >= epoch.startSeconds - 2 && position < end + 2) return position
    }
    return Double.NaN
  }

  /** The media time of a channel position, on the same side of a wrap as the media time now. */
  fun media(position: Double, near: Double): Double {
    val epoch = epochs.lastOrNull { !it.startSeconds.isFinite() || it.startSeconds <= position } ?: epochs.first()
    var media = position - epoch.offsetSeconds
    if (near.isFinite()) media += WRAP_SECONDS * Math.rint((near - media) / WRAP_SECONDS)
    return media
  }

  override fun equals(other: Any?): Boolean =
      other is PorticoMpvLinearClock && other.epochs.size == epochs.size &&
          epochs.indices.all { epochs[it].startSeconds == other.epochs[it].startSeconds && epochs[it].offsetSeconds == other.epochs[it].offsetSeconds }

  override fun hashCode(): Int = epochs.size

  companion object {
    const val WRAP_SECONDS = 8589934592.0 / 90000.0

    /** `linearClock` as the coordinator sends it: [{offsetMs}, {startMs, offsetMs}, …], at most 64; null when malformed. */
    fun parse(value: Any?): PorticoMpvLinearClock? {
      val list = value as? List<*> ?: return null
      if (list.isEmpty() || list.size > 64) return null
      val epochs = ArrayList<Epoch>()
      for ((index, item) in list.withIndex()) {
        val entry = item as? Map<*, *> ?: return null
        val offset = (entry["offsetMs"] as? Number)?.toDouble() ?: return null
        if (!offset.isFinite() || Math.abs(offset) > 1e15) return null
        val start = if (index == 0) Double.NEGATIVE_INFINITY else {
          val ms = (entry["startMs"] as? Number)?.toDouble() ?: return null
          if (!ms.isFinite() || ms < 0 || ms / 1000.0 <= epochs.last().startSeconds) return null
          ms / 1000.0
        }
        epochs.add(Epoch(start, offset / 1000.0))
      }
      return PorticoMpvLinearClock(epochs)
    }
  }
}
