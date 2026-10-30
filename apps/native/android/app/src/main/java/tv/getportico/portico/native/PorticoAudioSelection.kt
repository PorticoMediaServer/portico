package tv.getportico.portico.native

import android.os.Handler
import android.os.Looper
import androidx.media3.common.C
import androidx.media3.common.TrackGroup
import androidx.media3.common.TrackSelectionOverride
import androidx.media3.common.Tracks
import androidx.media3.exoplayer.ExoPlayer

/**
 * The chosen audio track of a stream: the Android counterpart of `Native/PorticoAudioSelection.m`.
 *
 * The server's plan names each audio rendition by the name its manifest gives it. This maps those
 * names onto the player's audio tracks (every rendition to exactly one track and every track to a
 * rendition, or the mapping is refused), selects the pending one, and reports what the player is
 * really playing.
 *
 * Main thread only.
 */
internal class PorticoAudioSelection {
  var onFact: ((Map<String, Any?>) -> Unit)? = null

  private class Option(val group: TrackGroup, val index: Int)

  private val main = Handler(Looper.getMainLooper())
  private var player: ExoPlayer? = null
  private var item = ""
  private var command: Map<String, Any?>? = null
  private var options: Map<String, Option>? = null
  private var sequence = 0L
  private var handledRevision = -1L
  private var lifetime = 0
  private var waiting = false

  fun invalidate() {
    lifetime++
    player = null
    item = ""
    command = null
    options = null
    sequence = 0
    handledRevision = -1
    waiting = false
  }

  private fun emit(type: String, extra: Map<String, Any?>) {
    val current = command ?: return
    val fact = HashMap<String, Any?>()
    fact["type"] = type
    fact["audioBinding"] = current["binding"]
    fact["audioEpoch"] = current["epoch"]
    fact.putAll(extra)
    onFact?.invoke(fact)
  }

  fun apply(next: Map<String, Any?>?, engine: ExoPlayer?, itemInstanceId: String) {
    if (next == null || next["binding"] !is Map<*, *> || next["epoch"] !is Number || next["renditions"] !is List<*> || engine == null) {
      invalidate()
      return
    }
    val changed = itemInstanceId != item || engine !== player || command?.get("binding") != next["binding"] || command?.get("epoch") != next["epoch"]
    if (changed) {
      invalidate()
      player = engine
      item = itemInstanceId
    }
    command = next
    if (options != null) { selectPending(); return }
    if (waiting) return
    waiting = true
    val life = lifetime
    main.postDelayed({ if (lifetime == life && waiting) { waiting = false; emit("audioMapped", mapOf("renditionIds" to emptyList<String>(), "audioMappingFailure" to "timeout")) } }, 15_000)
    map(engine.currentTracks)
  }

  fun tracksChanged(tracks: Tracks) {
    if (command == null) return
    if (options == null) { if (waiting) map(tracks) } else sample()
  }

  private fun map(tracks: Tracks) {
    val audio = ArrayList<Pair<Option, String?>>()
    for (group in tracks.groups) {
      if (group.type != C.TRACK_TYPE_AUDIO) continue
      for (i in 0 until group.length) audio.add(Pair(Option(group.mediaTrackGroup, i), group.getTrackFormat(i).label))
    }
    // The tracks are not known until the manifest is read.
    if (audio.isEmpty()) return
    waiting = false
    val renditions = command?.get("renditions") as? List<*> ?: emptyList<Any?>()
    val mapped = LinkedHashMap<String, Option>()
    val used = HashSet<Option>()
    var valid = renditions.size in 1..16 && audio.size == renditions.size
    if (valid) for (entry in renditions) {
      val rendition = entry as? Map<*, *>
      val id = rendition?.get("id") as? String
      val name = rendition?.get("name") as? String
      if (id.isNullOrEmpty() || name.isNullOrEmpty() || mapped.containsKey(id)) { valid = false; break }
      val matches = audio.filter { it.second == name }
      if (matches.size != 1 || !used.add(matches[0].first)) { valid = false; break }
      mapped[id] = matches[0].first
    }
    if (!valid || mapped.size != renditions.size || used.size != audio.size) {
      emit("audioMapped", mapOf("renditionIds" to emptyList<String>(), "audioMappingFailure" to "unsupported_metadata"))
      return
    }
    options = mapped
    emit("audioMapped", mapOf("renditionIds" to mapped.keys.toList()))
    sample()
    selectPending()
  }

  /** What the player is playing now, and whether that settles the pending choice. */
  private fun sample() {
    val engine = player ?: return
    val known = options ?: return
    var observed: String? = null
    for (group in engine.currentTracks.groups) {
      if (group.type != C.TRACK_TYPE_AUDIO) continue
      for ((id, option) in known) if (option.group == group.mediaTrackGroup && group.isTrackSelected(option.index)) observed = id
    }
    emit("audioObserved", mapOf("audioSequence" to (++sequence).toDouble(), "renditionId" to observed))
    val pending = command?.get("pending") as? Map<*, *> ?: return
    val revision = PorticoPlaybackController.number(pending["revision"])?.toLong()
    if (revision == handledRevision && pending["renditionId"] == observed) emit("audioApplied", mapOf("audioRevision" to pending["revision"], "renditionId" to observed))
  }

  private fun selectPending() {
    val engine = player ?: return
    val known = options ?: return
    val pending = command?.get("pending") as? Map<*, *> ?: return
    val revision = PorticoPlaybackController.number(pending["revision"])?.toLong() ?: return
    if (revision == handledRevision) return
    handledRevision = revision
    val option = known[pending["renditionId"]]
    if (option == null) {
      emit("audioFailed", mapOf("audioRevision" to revision.toDouble(), "error" to "This audio track is unavailable in the current player."))
      return
    }
    engine.trackSelectionParameters = engine.trackSelectionParameters.buildUpon()
        .setOverrideForType(TrackSelectionOverride(option.group, option.index))
        .build()
    // Choosing the track already playing changes nothing, so look at once as well.
    sample()
  }
}
