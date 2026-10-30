package tv.getportico.portico.native

import android.os.Handler
import android.os.Looper
import androidx.media3.common.C
import androidx.media3.common.Format
import androidx.media3.common.Player
import androidx.media3.common.TrackGroup
import androidx.media3.common.TrackSelectionOverride
import androidx.media3.common.Tracks
import androidx.media3.exoplayer.ExoPlayer
import tv.getportico.portico.policy.PorticoLocalAudioCatalogue
import tv.getportico.portico.policy.PorticoIsoAudioIdentity

/** Embedded audio choices read from the currently loaded download. Main thread only. */
internal class PorticoLocalAudioSelection {
  var onFact: ((Map<String, Any?>) -> Unit)? = null
  var onRestored: ((String) -> Unit)? = null

  private class Option(val group: TrackGroup, val index: Int, val format: Format) {
    val identity = PorticoLocalAudioCatalogue.Identity(group, index, format)
    override fun equals(other: Any?): Boolean = other is Option && group === other.group && index == other.index && format == other.format
    override fun hashCode(): Int = 31 * (31 * System.identityHashCode(group) + index) + format.hashCode()
  }
  private data class Selection(val revision: Long, val catalogRevision: Long, val trackId: String)
  class Retained internal constructor(internal val identity: PorticoLocalAudioCatalogue.Identity<TrackGroup, Format>?, internal val commandRevision: Long)
  internal class Restoration(
    val target: Retained,
    private val cancelTimeout: (Runnable) -> Unit,
    private val stopPlayback: () -> Unit,
    private val reportFailure: () -> Unit,
  ) {
    var applied = false
    var active = true
      private set
    val timeout = Runnable { fail() }

    fun cancel() { active = false; cancelTimeout(timeout) }

    fun fail() {
      if (!active) return
      active = false
      cancelTimeout(timeout)
      // Stop before reporting/releasing the hold: a queued READY must not start default audio.
      stopPlayback()
      reportFailure()
    }
  }

  private val main = Handler(Looper.getMainLooper())
  private val catalogue = PorticoLocalAudioCatalogue<Option>()
  private val sourceIdentity = PorticoIsoAudioIdentity.Cache()
  private var player: ExoPlayer? = null
  private var item = ""
  private var handled: Selection? = null
  private var pending: Selection? = null
  private var timeout: Runnable? = null
  private var observed: List<String> = emptyList()
  private var overrideGroup: TrackGroup? = null
  private val ownedOverrides = LinkedHashSet<TrackGroup>()
  private var restoration: Restoration? = null
  private var retiredCommandRevision = 0L
  val holdsPlayback: Boolean get() = restoration != null
  var failed = false
    private set

  fun invalidate() {
    val previous = player
    val groups = ownedOverrides.toList()
    ownedOverrides.clear()
    overrideGroup = null
    restoration?.cancel()
    restoration = null
    failed = false
    retiredCommandRevision = 0
    timeout?.let { main.removeCallbacks(it) }
    timeout = null
    pending = null
    handled = null
    player = null
    item = ""
    observed = emptyList()
    catalogue.clear()
    sourceIdentity.clear()
    // This is a playback-session choice. A reused ExoPlayer must not carry it into another file.
    if (previous != null && groups.isNotEmpty()) {
      val parameters = previous.trackSelectionParameters.buildUpon()
      for (group in groups) parameters.clearOverride(group)
      previous.trackSelectionParameters = parameters.build()
    }
  }

  private fun current(): ExoPlayer? = player?.takeIf {
    item.isNotEmpty() && it.currentMediaItem?.localConfiguration?.uri?.scheme?.lowercase() == "file"
  }

  private fun emit(type: String, extra: Map<String, Any?>) {
    if (current() == null) return
    onFact?.invoke(extra + mapOf("type" to type, "localAudioItemInstanceId" to item))
  }

  /** The controller calls this only before a same-binding, exact-file sink rebuild. */
  fun retain(value: Map<String, Any?>?): Retained? {
    val revision = value?.let { if (valid(it)) integer(it["revision"]) else null }
    val restoring = restoration
    if (restoring != null && (value == null || revision != null && revision <= retiredCommandRevision)) return restoring.target
    if (value != null && revision != null && revision <= retiredCommandRevision) return retain(null)
    val engine = current() ?: return null
    if (value != null) {
      if (!valid(value)) return Retained(null, 0)
      val option = catalogue.find(integer(value["catalogRevision"])!!, value["trackId"] as String)
      val present = option != null && engine.currentTracks.groups.any {
        it.type == C.TRACK_TYPE_AUDIO && it.mediaTrackGroup === option.group && it.isTrackSupported(option.index)
      }
      // A coalesced new choice belongs to this command, not to the audio heard before it.
      return Retained(if (present) option!!.identity else null, revision!!)
    }
    val group = overrideGroup ?: return null
    val selected = catalogue.tracks().values.filter { option -> engine.currentTracks.groups.any {
      it.type == C.TRACK_TYPE_AUDIO && it.mediaTrackGroup === option.group && it.isTrackSelected(option.index)
    } }
    val option = selected.singleOrNull()?.takeIf { it.group === group } ?: return null
    return Retained(option.identity, handled?.revision ?: retiredCommandRevision)
  }

  /** Old catalogue tokens stay retired; restoration uses an exact unique native identity. */
  fun restore(retained: Retained, engine: ExoPlayer?, itemInstanceId: String) {
    invalidate()
    player = engine
    item = itemInstanceId
    beginRestoration(retained)
    current()?.let { refresh(it.currentTracks, true) }
  }

  private fun beginRestoration(target: Retained) {
    restoration?.cancel()
    val restoring = Restoration(target, { main.removeCallbacks(it) }, {
      failed = true
      player?.let { engine -> engine.playWhenReady = false; engine.stop() }
    }, {
      restoration = null
      cancelPending()
      // The seek-index read may still be pending, before the player has a current MediaItem.
      onFact?.invoke(mapOf("type" to "error", "localAudioItemInstanceId" to item,
          "error" to "This audio track is unavailable in the current player.", "uncertain" to true))
    })
    restoration = restoring
    retiredCommandRevision = Math.max(retiredCommandRevision, target.commandRevision)
    main.postDelayed(restoring.timeout, SELECTION_TIMEOUT_MS)
  }

  private fun failRestoration(restoring: Restoration) {
    if (restoration !== restoring) return
    restoring.fail()
  }

  fun apply(value: Map<String, Any?>?, engine: ExoPlayer?, itemInstanceId: String, local: Boolean) {
    if (local && item == itemInstanceId && restoration != null && player == null) {
      player = engine
      if (engine == null) return
    }
    if (!local || engine == null || itemInstanceId.isEmpty()) { invalidate(); return }
    if (player !== engine || item != itemInstanceId) { invalidate(); player = engine; item = itemInstanceId }
    if (current() == null) return
    refresh(engine.currentTracks, false)
    if (value == null) { cancelPending(); return }
    if (!valid(value)) return // the controller rejects malformed commands before changing state
    val next = Selection(integer(value["revision"])!!, integer(value["catalogRevision"])!!, value["trackId"] as String)
    if (next.revision <= retiredCommandRevision) return
    val previous = handled
    if (previous != null && next.revision <= previous.revision) {
      if (next.revision == previous.revision && next != previous) failed(next)
      return
    }
    cancelPending()
    handled = next
    val option = catalogue.find(next.catalogRevision, next.trackId)
    if (option == null) { failed(next); return }
    if (restoration != null) beginRestoration(Retained(option.identity, next.revision))
    pending = next
    val previousGroup = overrideGroup
    overrideGroup = option.group
    ownedOverrides.add(option.group)
    restoration?.applied = true
    try {
      engine.trackSelectionParameters = engine.trackSelectionParameters.buildUpon()
          .setTrackTypeDisabled(C.TRACK_TYPE_AUDIO, false)
          .setOverrideForType(TrackSelectionOverride(option.group, option.index))
          .build()
      ownedOverrides.retainAll(listOfNotNull(overrideGroup))
    } catch (_: RuntimeException) {
      overrideGroup = previousGroup
      cancelPending(); failed(next); restoration?.let { failRestoration(it) }; return
    }
    // An already-selected track needs no future onTracksChanged event to prove the choice.
    refresh(engine.currentTracks, true)
    if (pending != next) return
    val capturedItem = item
    val task = Runnable {
      if (item == capturedItem && player === engine && pending == next) { cancelPending(); failed(next) }
    }
    timeout = task
    main.postDelayed(task, SELECTION_TIMEOUT_MS)
  }

  fun tracksChanged(tracks: Tracks) {
    val engine = current() ?: return
    // Sample the retained engine, never an old callback's detached inventory.
    val actual = engine.currentTracks
    if (tracks !== actual && tracks != actual) return
    refresh(actual, true)
  }

  private fun refresh(tracks: Tracks, force: Boolean) {
    if (failed || current() == null) return
    val options = ArrayList<Option>()
    outer@ for (group in tracks.groups) {
      if (group.type != C.TRACK_TYPE_AUDIO) continue
      for (index in 0 until group.length) {
        if (!group.isTrackSupported(index)) continue
        options.add(Option(group.mediaTrackGroup, index, group.getTrackFormat(index)))
        // Refuse an oversized inventory as a whole; never map a silently truncated list.
        if (options.size > PorticoLocalAudioCatalogue.MAX_TRACKS) break@outer
      }
    }
    val changed = catalogue.replace(item, options)
    if (changed) {
      pending?.let { stale -> cancelPending(); failed(stale) }
      val group = overrideGroup
      if (group != null && options.none { it.group === group }) {
        overrideGroup = null
        ownedOverrides.remove(group)
        restoration?.applied = false
        player?.let { it.trackSelectionParameters = it.trackSelectionParameters.buildUpon().clearOverride(group).build() }
      }
    }
    val restoring = restoration
    var restoredOption: Option? = null
    if (restoring != null) {
      if (restoring.active) {
        val identity = restoring.target.identity
        val matched = PorticoLocalAudioCatalogue.uniqueRetained(identity, options.map { it.identity })
        restoredOption = options.singleOrNull { it.identity === matched }
        if (identity == null || options.size > PorticoLocalAudioCatalogue.MAX_TRACKS || restoredOption == null && player?.playbackState == Player.STATE_READY) failRestoration(restoring)
        else if (restoredOption != null && !restoring.applied) {
          val engine = current() ?: return
          overrideGroup = restoredOption.group
          ownedOverrides.add(restoredOption.group)
          restoring.applied = true
          try {
            engine.trackSelectionParameters = engine.trackSelectionParameters.buildUpon()
                .setTrackTypeDisabled(C.TRACK_TYPE_AUDIO, false)
                .setOverrideForType(TrackSelectionOverride(restoredOption.group, restoredOption.index))
                .build()
            ownedOverrides.retainAll(listOfNotNull(overrideGroup))
          } catch (_: RuntimeException) { failRestoration(restoring) }
        }
      }
    }
    if (restoring != null && (!restoring.active || restoration !== restoring)) return
    val selected = catalogue.tracks().filter { (_, option) ->
      tracks.groups.any { it.type == C.TRACK_TYPE_AUDIO && it.mediaTrackGroup === option.group && it.isTrackSelected(option.index) }
    }.keys.toList()
    val publish = force || changed || observed != selected
    observed = selected
    if (publish) {
      val proof = sourceIdentity.get(current()?.currentMediaItem?.localConfiguration?.uri?.toString())
      val formatIds = catalogue.tracks().values.groupingBy { it.format.id }.eachCount()
      val choices = catalogue.tracks().map { (id, option) ->
        val choice = hashMapOf<String, Any?>("id" to id, "selected" to (id in selected))
        if (formatIds[option.format.id] == 1) proof.media3(option.format.containerMimeType, option.format.id)?.let {
          choice["sourceTrackIdentity"] = it
        }
        option.format.language?.takeIf { it.isNotEmpty() && it.length <= 128 }?.let { choice["language"] = it }
        option.format.label?.takeIf { it.isNotEmpty() && it.length <= 4096 }?.let { choice["title"] = it }
        (option.format.codecs ?: option.format.sampleMimeType)?.takeIf { it.isNotEmpty() && it.length <= 128 }?.let { choice["codec"] = it }
        if (option.format.channelCount in 1..64) choice["channels"] = option.format.channelCount.toDouble()
        choice
      }
      emit("mediaInfo", mapOf("audioTracks" to choices, "audioTrackCatalogRevision" to catalogue.revision().toDouble()))
    }
    val wanted = pending
    if (wanted != null && wanted.catalogRevision == catalogue.revision() && selected == listOf(wanted.trackId)) {
      cancelPending()
      emit("localAudioTrackApplied", selectionFact(wanted))
    }
    if (restoring != null && restoration === restoring && restoring.active && restoring.applied && restoredOption != null) {
      val restoredId = catalogue.tracks().entries.singleOrNull { it.value == restoredOption }?.key
      if (restoredId != null && selected == listOf(restoredId)) {
        restoring.cancel()
        restoration = null
        onRestored?.invoke(item)
      }
    }
  }

  private fun cancelPending() { timeout?.let { main.removeCallbacks(it) }; timeout = null; pending = null }
  private fun selectionFact(value: Selection): Map<String, Any?> = mapOf("audioTrackRevision" to value.revision.toDouble(),
      "catalogRevision" to value.catalogRevision.toDouble(), "trackId" to value.trackId)
  private fun failed(value: Selection) = emit("localAudioTrackFailed", selectionFact(value) + ("error" to "This audio track is unavailable in the current player."))

  companion object {
    internal const val SELECTION_TIMEOUT_MS = 15_000L
    private fun integer(value: Any?): Long? {
      val number = (value as? Number)?.toDouble() ?: return null
      return if (number.isFinite() && number >= 1 && number <= 9007199254740991.0 && number == Math.floor(number)) number.toLong() else null
    }
    fun valid(value: Any?): Boolean {
      if (value == null) return true
      val map = value as? Map<*, *> ?: return false
      val id = map["trackId"] as? String ?: return false
      return map.keys == setOf("revision", "catalogRevision", "trackId") && integer(map["revision"]) != null &&
          integer(map["catalogRevision"]) != null && id.isNotEmpty() && id.length <= 256
    }
  }
}
