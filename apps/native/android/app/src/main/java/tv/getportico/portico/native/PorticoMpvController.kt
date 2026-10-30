package tv.getportico.portico.native

import android.content.Context
import android.hardware.display.DisplayManager
import android.media.AudioDeviceCallback
import android.media.AudioDeviceInfo
import android.media.AudioFocusRequest
import android.media.AudioManager
import android.net.Uri
import android.os.Build
import android.os.Handler
import android.os.Looper
import android.os.SystemClock
import android.view.Display
import android.view.Surface
import android.view.accessibility.CaptioningManager
import com.facebook.react.bridge.Arguments
import com.facebook.react.bridge.WritableMap
import java.util.UUID
import tv.getportico.portico.mpv.PorticoMpvNative

/**
 * The mpv player engine on Android: the second implementation of the contract in
 * `Native/ENGINE-CONTRACT.md`, and the counterpart of `Native/PorticoMpvController.swift`.
 *
 * JavaScript owns the playback session; this owns mpv. JavaScript acquires a lease, then sends
 * commands, each of which is the whole desired state. A command is validated in full before
 * anything changes. The engine answers with facts, each fenced by the binding it belongs to, a
 * rising sequence number and the last command revision the engine had seen.
 *
 * Everything here runs on the main thread. mpv itself is only ever touched through
 * [PorticoMpvCore].
 */
internal class PorticoMpvController(private val context: Context) {
  /** A fact for JavaScript. */
  var onFact: ((WritableMap) -> Unit)? = null

  /** The engine's lease or its open media changed: views and the system media session re-check. */
  val attachmentListeners = LinkedHashSet<() -> Unit>()
  /** The app-drawn subtitle line, the picture's shape or the way it is drawn changed. */
  val overlayListeners = LinkedHashSet<() -> Unit>()

  val instanceId: String = UUID.randomUUID().toString()
  var lease: String? = null
    private set
  /** The app-drawn subtitle line (plain cues from the server's cue document), when mpv is not drawing the chosen track itself. */
  /** Portico's caption style from the subtitle command in force ({scale, background}). */
  var captionStyle: Map<*, *>? = null
    private set
  var overlayCue = PorticoSubtitleSelection.Display("", italic = false, top = false)
    private set
  /** Width over height of the picture as displayed, when known. */
  var videoAspect: Double? = null
    private set
  /** The decoder draws straight onto the surface (an HDR title on an HDR screen), rather than mpv drawing the picture. */
  var directOutput = false
  /** mpv's own output while it draws the picture: "gpu", or "gpu-next" for Dolby Vision profile 5. */
  private var drawnOutput = "gpu"
    private set
  /** What JavaScript wants: paused or playing. */
  val paused: Boolean get() = desiredPaused
  /** A lease is held and media is open under it. */
  val mediaOpen: Boolean get() = lease != null && fileRequested
  /** The engine as the system's media session sees it. */
  val sessionPlayer: PorticoMpvSessionPlayer by lazy { PorticoMpvSessionPlayer(this) }

  /** Read-only fence for the exact surface currently lent by this view. No source is exported. */
  fun snapshotIdentity(owner: Any, target: Surface, expected: Map<String, Any?>): List<Any?>? {
    if (invalidated || lease.isNullOrEmpty() || expected["lease"] != lease || !PorticoSnapshotBinding.matches(expected, binding) ||
        surfaceOwner !== owner || surface !== target || !ready() || !hasPlayed || !PorticoMpvSupport.hasVideo(tracks) ||
        snapshot.vid == "no" || seeking || routeSuspended || routeSeeking || source.isNullOrEmpty() || sourceRange() != "sdr") return null
    return listOf(instanceId, lease, binding?.toMap(), itemInstanceId, source, loadEpoch, core)
  }

  private val main = Handler(Looper.getMainLooper())
  private var invalidated = false
  private var owner: String? = null
  private var commandRevision = 0L
  private var factSequence = 0L
  private var binding: Map<String, Any?>? = null
  private var command: Map<String, Any?>? = null
  private var source: String? = null
  private var intentId = 0L
  private var itemInstanceId = ""
  private var desiredPaused = true
  private var hasPlayed = false
  private var previousProgress = Double.NaN
  private var lastProgressFact = Double.NaN
  private var lastProgressPaused = true

  private var acceptedSeekRevision = -1L
  private var acceptedSeekTarget = 0.0
  private var seekRetired = false
  private var seekRevision = -1L
  private var seeking = false
  private var seekToken = 1000L
  private var seekInFlight: SeekFlight? = null
  private var startSeek: Pair<Long, Double>? = null

  private var routeSuspended = false
  private var routeResume = false
  private var routeSeeking = false
  private var routeFailed = false
  private var routeEpoch = 0
  private var routePosition = 0.0
  // A channel: its clock (media time to channel position), and the seek that already reopened
  // the playlist at its target once (mpv cannot seek a live playlist beyond what it holds).
  private var linear: PorticoMpvLinearClock? = null
  private var linearReloaded = -1L
  private var linearOpenedAt = false

  private var core: PorticoMpvCore? = null
  private val relay = PorticoMpvRelay()
  private var loadEpoch = 0
  /** A load was asked for the current source. */
  private var fileRequested = false
  private var loaded = false
  private var restarted = false
  private var displayHold = false
  private var backgroundHold = false
  private var endedEmitted = false
  private var snapshot = PorticoMpvSnapshot()
  private var lastPosition = Double.NaN
  private var tracks: List<Map<String, Any?>> = emptyList()
  private val localAudioIdentity = tv.getportico.portico.policy.PorticoIsoAudioIdentity.Cache()
  private var chapters: List<Map<String, Any?>> = emptyList()
  private var videoParams: Map<String, Any?> = emptyMap()

  private var audioCommand: Map<String, Any?>? = null
  private var audioMap: Map<String, Int>? = null
  private var audioMappingAttempted = false
  private var audioSequence = 0
  private var audioHandledRevision = -1L
  private var audioTrackHandled = -1L
  private var audioTrackPending: Triple<Long, Int, Long>? = null
  private var audioTrackReadbackRevision = -1L
  private var audioTrackTimeout: Runnable? = null

  private val subtitleOverlay = PorticoSubtitleSelection()
  private val fontFetcher = PorticoMpvFontFetcher(context)
  private var engineSubtitleKey: String? = null
  private val refusedSubtitleKeys = HashSet<String>()
  private var externalSubtitleAddress: String? = null
  private var subtitleToken = 0L
  private var subtitleTokenCount = 0L
  private var subtitleTokenKey = ""
  /** The open title is an HLS stream, which may open the resources its playlists name. */
  private var referencesAllowed = false
  /** The adding of a subtitle file that may open one companion file (a VobSub index and its `.sub`). */
  private var referencesToken = 0L

  // The surface the picture is drawn on, lent by the view that shows the leased picture.
  private var surface: Surface? = null
  private var surfaceOwner: Any? = null
  private var surfaceWidth = 0
  private var surfaceHeight = 0
  /** The decoder could not draw straight onto the surface for this file; mpv draws it instead. */
  private var directRefused = false
  /** When the way the picture is drawn last changed (`SystemClock.elapsedRealtime()`). */
  private var outputChangedAt = 0L
  private var pictureRetried = false
  private var frameRateVoted = false
  private var votedFrameSurface: Surface? = null
  private var frameRateClear: Runnable? = null
  private var displayWait = 0
  /** The first frame is ready but no view has lent its surface yet: readiness waits for it (television). */
  private var pendingDisplayReady: (() -> Unit)? = null
  private var pendingDisplayDeadline = 0L

  // Sound: audio focus is the app's to ask for, since mpv opens its own output.
  private val audioManager = context.getSystemService(Context.AUDIO_SERVICE) as AudioManager
  private var focusRequest: AudioFocusRequest? = null
  /** The app holds audio focus (possibly lost for a moment). */
  private var focusHeld = false
  /** Another app has the sound: playback waits. */
  private var focusHold = false
  /** Another app took the sound for good: it is asked for again only when the viewer plays again. */
  private var focusLost = false
  private var focusRelease: Runnable? = null
  private var outputChannels = 2
  private var watchingDevices = false

  private class SeekFlight(val token: Long, val revision: Long, val target: Double) {
    var commandAccepted = false
    var restartSeen = false
  }

  private val visibility: (Boolean) -> Unit = { visible -> if (visible) enteredForeground() else enteredBackground() }

  private val focusListener = AudioManager.OnAudioFocusChangeListener { change -> main.post { focusChanged(change) } }

  private val captioning = try { context.getSystemService(Context.CAPTIONING_SERVICE) as? CaptioningManager } catch (_: Exception) { null }
  private val captionsChanged = object : CaptioningManager.CaptioningChangeListener() {
    override fun onFontScaleChanged(fontScale: Float) { main.post { captionStyleChanged() } }
    override fun onUserStyleChanged(userStyle: CaptioningManager.CaptionStyle) { main.post { captionStyleChanged() } }
    override fun onEnabledChanged(enabled: Boolean) { main.post { captionStyleChanged() } }
  }

  private val devicesChanged = object : AudioDeviceCallback() {
    override fun onAudioDevicesAdded(added: Array<out AudioDeviceInfo>) = audioOutputsChanged()
    override fun onAudioDevicesRemoved(removed: Array<out AudioDeviceInfo>) = audioOutputsChanged()
  }

  init {
    subtitleOverlay.onText = { cue ->
      overlayCue = cue
      overlayListeners.toList().forEach { it() }
    }
    subtitleOverlay.onStatus = { error -> emit("subtitleStatus", error) }
    // A downloaded title's plain subtitles are a WebVTT file beside it, inside Downloads.
    subtitleOverlay.allowsLocalFile = { url -> PorticoMpvSupport.allowedLocalSource(context, url) }
    PorticoAppVisibility.listeners.add(visibility)
    captioning?.addCaptioningChangeListener(captionsChanged)
  }

  // --- lease -----------------------------------------------------------------------------

  fun acquire(owner: String?): Map<String, Any?>? {
    if (invalidated || owner.isNullOrEmpty() || owner.length > 128) return null
    if (lease != null && this.owner != owner) return null
    if (lease == null) {
      this.owner = owner
      lease = UUID.randomUUID().toString()
      commandRevision = 0
      factSequence = 0
      // A lease taken while the app is off screen starts held.
      backgroundHold = !PorticoAppVisibility.visible
      focusHold = false
      focusLost = false
      focusRelease?.let { main.removeCallbacks(it) }
      focusRelease = null
    }
    return mapOf("lease" to lease, "instanceId" to instanceId, "engine" to "mpv")
  }

  /**
   * Ends the lease. [completion] runs once mpv has closed the title and its sound, so another
   * engine that starts next never overlaps it.
   */
  fun release(value: String?, completion: (() -> Unit)? = null) {
    if (value == null || lease != value) { completion?.invoke(); return }
    unload()
    resetAudio()
    subtitleOverlay.invalidate()
    engineSubtitleKey = null
    refusedSubtitleKeys.clear()
    externalSubtitleAddress = null
    relay.unbind()
    scheduleFrameRateClear()
    source = null
    binding = null
    command = null
    lease = null
    owner = null
    seeking = false
    routeSuspended = false
    routeResume = false
    routeSeeking = false
    routeFailed = false
    routeEpoch++
    attachmentListeners.toList().forEach { it() }
    releaseAudioFocus()
    // The stop command only requests teardown. Termination is the fence that proves its sound
    // has closed before the coordinator starts the other engine, and runs off the main thread.
    destroyCore(completion)
  }

  fun invalidate() {
    invalidated = true
    onFact = null
    release(lease)
    PorticoAppVisibility.listeners.remove(visibility)
    captioning?.removeCaptioningChangeListener(captionsChanged)
    destroyCore()
    relay.destroy()
  }

  // --- commands --------------------------------------------------------------------------

  /** Accepts a command, or answers why not. Nothing changes unless the whole command is valid. */
  fun accept(value: Map<String, Any?>?): String? {
    if (invalidated) return "Playback controller is closed."
    val command = value ?: return "Invalid playback binding."
    if (command["matchFrameRate"] != null && command["matchFrameRate"] != "auto" && command["matchFrameRate"] != "off") return "Invalid display matching choice."
    val binding = PorticoMpvSupport.map(command["binding"]) ?: return "Invalid playback binding."
    val lease = this.lease
    if (lease == null || binding["lease"] != lease) return "Playback lease is no longer active."
    if (PorticoMpvSupport.boolean(command["paused"]) == null) return "Invalid paused flag."
    val source = command["source"] as? String
    if (source == null || PorticoMpvSupport.number(command["rate"]) == null) return "Invalid playback command."
    val intent = PorticoMpvSupport.integer(binding["intentId"])
    if (intent == null || PorticoMpvSupport.integer(binding["generation"]) == null) return "Invalid playback binding."
    val revision = PorticoMpvSupport.integer(command["commandRevision"]) ?: return "Invalid playback revision."
    if (revision < commandRevision) return "Stale playback command."
    if (revision == commandRevision) return if (this.command == command) null else "Conflicting playback command."
    if (binding["sessionId"] !is String || binding["itemId"] !is String) return "Invalid playback binding."
    val clock = command["timelineOriginMs"]
    if (clock != null) {
      if (PorticoMpvSupport.integer(clock) == null) return "Invalid channel clock."
      if (!LIVE_CHANNELS) return "This player does not play live channels."
      if (PorticoMpvLinearClock.parse(command["linearClock"]) == null) return "Invalid channel clock."
    } else if (command["linearClock"] != null) return "Invalid channel clock."
    val route = command["routeRevision"]
    val connection = command["connectionState"]
    if (connection != null) {
      if ((connection != "ready" && connection != "verifying") || PorticoMpvSupport.integer(route, 0.0) == null) return "Invalid route transition."
    }
    val previousRoute = PorticoMpvSupport.number(this.command?.get("routeRevision"))
    val nextRoute = PorticoMpvSupport.number(route)
    if (previousRoute != null && (nextRoute == null || nextRoute < previousRoute)) return "Stale route transition."
    if (command["listeningOwner"] != null) return "This player does not play music sessions."
    if (command["audioRender"] != null) return "Unsupported audio render context."
    val old = this.binding
    if (old != null) {
      val oldIntent = PorticoMpvSupport.integer(old["intentId"]) ?: 0L
      val sameBinding = old == binding
      val sameSource = this.source == source
      val oldUrl = PorticoMpvSupport.parse(this.source)
      val nextUrl = PorticoMpvSupport.parse(source)
      val sameResource = oldUrl != null && nextUrl != null && oldUrl.path == nextUrl.path && (oldUrl.encodedQuery ?: "") == (nextUrl.encodedQuery ?: "") &&
          nextUrl.userInfo.isNullOrEmpty() && nextUrl.encodedFragment.isNullOrEmpty()
      val routeChange = sameBinding && sameResource && nextRoute != null && nextRoute > (previousRoute ?: 0.0)
      val same = sameBinding && (sameSource || routeChange)
      if (intent < oldIntent || (intent == oldIntent && !same && !PorticoMpvSupport.replannedInPlace(old, binding))) return "Stale playback binding."
    }
    var pendingSeek: Pair<Long, Double>? = null
    val pending = command["pendingSeek"]
    if (pending != null) {
      val seek = PorticoMpvSupport.map(pending)
      val seekRevisionValue = PorticoMpvSupport.number(seek?.get("revision"))
      val target = PorticoMpvSupport.number(seek?.get("positionSeconds"))
      if (seekRevisionValue == null || target == null || Math.abs(seekRevisionValue) > PorticoMpvSupport.MAX_SAFE_INTEGER || Math.abs(target) > PorticoMpvSupport.MAX_SAFE_INTEGER ||
          seekRevisionValue < 0 || target < 0) return "Invalid seek command."
      if (seekRevisionValue < 1 || Math.floor(seekRevisionValue) != seekRevisionValue) return "Invalid seek revision."
      pendingSeek = Pair(seekRevisionValue.toLong(), target)
    }
    val sameBinding = this.binding == binding
    if (sameBinding && pendingSeek != null) {
      if (pendingSeek.first < acceptedSeekRevision) return "Stale seek command."
      if (pendingSeek.first == acceptedSeekRevision && (seekRetired || pendingSeek.second != acceptedSeekTarget)) return "Conflicting or retired seek command."
    }
    val track = command["audioTrack"]
    if (track != null) {
      val wanted = PorticoMpvSupport.map(track)
      val index = PorticoMpvSupport.integer(wanted?.get("streamIndex"), 0.0)
      if (wanted == null || PorticoMpvSupport.integer(wanted["revision"]) == null || index == null || index > 65535) return "Invalid audio track command."
    }
    // Every check comes before any change. A live seek that is repeated still lets a newer
    // pause or rate through.
    if (!sameBinding) { acceptedSeekRevision = -1; seekRetired = false }
    if (pendingSeek != null) {
      acceptedSeekRevision = pendingSeek.first
      acceptedSeekTarget = pendingSeek.second
      seekRetired = false
    } else if (acceptedSeekRevision >= 0) {
      seekRetired = true
    }
    commandRevision = revision
    this.binding = binding
    apply(command)
    attachmentListeners.toList().forEach { it() }
    return null
  }

  private fun pendingSeekCommand(): Map<String, Any?>? = PorticoMpvSupport.map(command?.get("pendingSeek"))

  private fun apply(next: Map<String, Any?>) {
    val sameBinding = command?.get("binding") == next["binding"]
    val verifying = next["connectionState"] == "verifying"
    val nextIntent = PorticoMpvSupport.integer(PorticoMpvSupport.map(next["binding"])?.get("intentId")) ?: 0L
    val nextSource = (next["source"] as? String) ?: ""
    if (!sameBinding) {
      routeResume = false
      routeSeeking = false
      routeSuspended = false
      routeFailed = false
      routeEpoch++
    }
    val changed = source != nextSource
    // A change of route is not a new playback and not a seek: the position already reached is
    // kept, and playback returns to it on the new route.
    if (sameBinding && (verifying || changed) && !routeResume && !routeSuspended) {
      val position = if (routeFailed) routePosition else currentPosition()
      if (position.isFinite() && position >= 0) {
        routePosition = position
        routeResume = true
        routeFailed = false
        routeEpoch++
      }
    }
    command = next
    clearDisabledFrameRate()
    linear = if (next["timelineOriginMs"] != null) PorticoMpvLinearClock.parse(next["linearClock"]) else null
    val wasPaused = desiredPaused
    desiredPaused = PorticoMpvSupport.boolean(next["paused"]) ?: true
    // Play, pressed again after another app had taken the sound, asks for it back.
    if (wasPaused && !desiredPaused) focusLost = false
    if (verifying) {
      if (!routeSuspended) {
        subtitleOverlay.invalidate()
        resetAudio()
        unload()
        routeSeeking = false
        seeking = false
      }
      routeSuspended = true
      return
    }
    val reload = routeSuspended || nextIntent != intentId || changed || !fileRequested
    routeSuspended = false
    if (reload) {
      hasPlayed = false
      previousProgress = Double.NaN
      routeEpoch++
      subtitleOverlay.invalidate()
      resetAudio()
      seeking = false
      routeSeeking = false
      unload()
      intentId = nextIntent
      source = nextSource
      seekRevision = -1
      if (nextSource.isNotEmpty()) {
        val url = PorticoMpvSupport.parse(nextSource)
        if (url == null || !PorticoMpvSupport.allowedSource(context, url)) {
          emit("error", "Invalid media address.")
          return
        }
        itemInstanceId = UUID.randomUUID().toString()
        linearReloaded = -1
        beginLoad(url, linearStart())
        if (routeResume) {
          val epoch = routeEpoch
          val load = loadEpoch
          main.postDelayed({
            if (!invalidated && routeEpoch == epoch && loadEpoch == load && routeResume) {
              routeResume = false
              routeSeeking = false
              routeFailed = true
              routeEpoch++
              core?.set("pause", "yes")
              emit("error", "Connection restored, but the retained position is not available. Your playback session was not replaced.")
            }
          }, 10_000)
        }
      }
    }
    val pending = pendingSeekCommand()
    if (pending != null && (routeResume || routeSeeking || routeFailed)) {
      // A seek the viewer asked for takes the place of the position being restored.
      routeResume = false
      routeSeeking = false
      routeFailed = false
      routeEpoch++
    }
    if (pending == null && seeking) {
      seeking = false
      seekInFlight = null
      startSeek = null
    }
    applyAudio()
    applyAudioTrack()
    applySubtitles()
    applyPendingSeek()
    applyIntent()
  }

  // --- loading ---------------------------------------------------------------------------

  /** Hardware decoding while mpv draws the picture itself. */
  private fun gpuDecoding(): String {
    if (PorticoMpvDevice.hardwareDecoders.isEmpty()) return "no"
    // A television's processor cannot copy 4K frames fast enough, so its decoder's frames are
    // drawn where they are; a phone or tablet copies them, which keeps every colour exact.
    return if (PorticoMpvDevice.isTelevision) "mediacodec,mediacodec-copy" else "mediacodec-copy,mediacodec"
  }

  private fun mpvOptions(): List<Pair<String, String>> {
    val memory = PorticoMpvDevice.memoryBytes(context) / 1_073_741_824.0
    val small = memory > 0 && memory <= 2.5
    val modest = PorticoMpvDevice.isTelevision || (memory > 0 && memory < 3.5)
    val options = ArrayList<Pair<String, String>>()
    // mpv reads no settings: the folder holds only the default subtitle font (PorticoMpvFonts).
    options += listOf(
        "config" to "yes", "config-dir" to PorticoMpvFonts.configDirectory(context).path,
        "load-scripts" to "no", "osc" to "no", "osd-level" to "0", "ytdl" to "no", "terminal" to "no",
        "input-default-bindings" to "no", "input-vo-keyboard" to "no", "resume-playback" to "no", "save-position-on-quit" to "no")
    // Devices with little memory or a modest graphics chip keep the cheaper scalers.
    if (modest) options += listOf(
        "profile" to "fast", "scale" to "bilinear", "dscale" to "bilinear", "cscale" to "bilinear", "dither-depth" to "no", "deband" to "no",
        "correct-downscaling" to "no", "linear-downscaling" to "no", "sigmoid-upscaling" to "no", "hdr-compute-peak" to "no")
    options += listOf(
        "idle" to "yes", "keep-open" to "yes", "pause" to "yes",
        // Picture: nothing is opened until a view lends its surface. Then mpv draws through
        // OpenGL ES, or the decoder draws straight onto the surface (see updateOutput).
        "vo" to "null", "force-window" to "no", "gpu-context" to "android", "opengl-es" to "yes",
        // Decoding: MediaCodec for the codecs the chip has, mpv's own software decoders otherwise.
        "hwdec" to gpuDecoding(), "hwdec-codecs" to PorticoMpvDevice.hardwareDecoders.keys.joinToString(",").ifEmpty { "h264" },
        "tone-mapping" to "auto",
        // Interlaced video (DVD rips, broadcast recordings) is deinterlaced when its frames say
        // so (FFmpeg's bwdif in the media engine), as on Apple. Frames a television's decoder
        // draws straight onto the surface are the decoder's to deinterlace.
        "deinterlace" to "auto",
        // A file may not send mpv to other network or file sources. HLS is allowed to, per
        // load, once the relay has rewritten every address in its playlists to itself; and
        // mpv's own network code may speak plain HTTP to the relay and nothing else. The
        // server's certificate is checked by the relay (OkHttp, the app's own trust); should
        // mpv ever be given an HTTPS address itself, it verifies it and refuses rather than
        // trusting it unchecked (mpv's default is not to verify).
        "access-references" to "no",
        "stream-lavf-o" to "protocol_whitelist=%20%file,http,tcp,crypto",
        "demuxer-lavf-o" to "protocol_whitelist=%20%file,http,tcp,crypto",
        "tls-verify" to "yes",
        // Sound: decoded on the device and handed to the system as PCM, in as many channels as
        // the output takes. Nothing is passed through as a bitstream.
        "ao" to "audiotrack", "audio-channels" to PorticoMpvDevice.channelLayouts(outputChannels),
        "hr-seek" to "yes",
        "cache" to "yes", "demuxer-max-bytes" to (if (small) "48MiB" else "150MiB"), "demuxer-max-back-bytes" to (if (small) "8MiB" else "48MiB"),
        "network-timeout" to "30",
        // Tracks are chosen by command, never by mpv's own preferences or by files nearby.
        "sub-auto" to "no", "audio-file-auto" to "no", "sid" to "no", "audio-display" to "no",
        // Text subtitles look the way the Media3 engine's line does: white on a soft black box,
        // inside the picture. Styled (ASS) tracks keep their own look. (The border options have
        // had two names; the one this mpv does not know is skipped.)
        "sub-use-margins" to "no", "sub-ass-force-margins" to "no",
        // The family is the ordinary system face's own name, in the fonts folder with the faces for
        // other scripts (PorticoMpvFonts): a line mixing scripts draws each letter in a face that has it.
        "sub-font" to PorticoMpvFonts.captionFamily(captionStyle), "sub-fonts-dir" to PorticoMpvFonts.fallbackDirectory(context).path,
        "sub-font-size" to "45", "sub-margin-y" to "58", "sub-color" to "1.0/1.0/1.0/1.0",
        "sub-border-style" to "background-box", "sub-back-color" to "0.0/0.0/0.0/0.7",
        "sub-outline-size" to "0", "sub-border-size" to "0", "sub-shadow-offset" to "0",
        "sub-scale" to PorticoMpvSupport.decimal(PorticoMpvSupport.captionScale(context), 2),
        "user-agent" to "Portico")
    return options
  }

  private fun ensureCore(): PorticoMpvCore? {
    core?.let { return it }
    outputChannels = PorticoMpvDevice.outputChannels(context)
    PorticoMpvFonts.select(context, null)
    var starting: PorticoMpvCore? = null
    starting = PorticoMpvCore.start(mpvOptions(), OPTIONAL_OPTIONS) { failure -> starting?.let { coreStarted(it, failure) } }
    val made = starting ?: return null
    made.onEvent = { epoch, event, next -> handle(epoch, event, next) }
    core = made
    directOutput = false
    drawnOutput = "gpu"
    made.configureOutput("gpu", gpuDecoding())
    if (!watchingDevices) {
      watchingDevices = true
      audioManager.registerAudioDeviceCallback(devicesChanged, main)
    }
    return made
  }

  /** mpv is ready, or could not start. Until it is ready it has no surface. */
  private fun coreStarted(started: PorticoMpvCore, failure: String?) {
    if (core !== started) return
    if (failure != null) {
      destroyCore()
      if (fileRequested) publishFailure(PorticoMpvNative.ERROR_VO_INIT_FAILED, "mpv could not start: $failure", emptyList())
      return
    }
    surface?.let { if (it.isValid) started.attachSurface(it, surfaceWidth, surfaceHeight) }
  }

  private fun destroyCore(completion: (() -> Unit)? = null) {
    val current = core
    if (current == null) { completion?.invoke(); return }
    core = null
    directOutput = false
    // The surface goes back before mpv ends, so nothing draws on it afterwards.
    current.detachSurface()
    current.destroy(completion)
    if (watchingDevices) {
      watchingDevices = false
      try { audioManager.unregisterAudioDeviceCallback(devicesChanged) } catch (_: Exception) {}
    }
  }

  /** The system is short of memory: an idle mpv is given up at once. */
  fun trimMemory() {
    if (lease != null || core == null) return
    destroyCore()
    relay.stop()
  }

  private fun unload() {
    val wasOpen = fileRequested
    loadEpoch++
    fileRequested = false
    loaded = false
    restarted = false
    displayHold = false
    pendingDisplayReady = null
    startSeek = null
    seekInFlight = null
    cancelAudioTrackTimeout()
    audioTrackPending = null
    audioTrackReadbackRevision = -1
    // A new playback object restarts its revisions; a later load must accept them again.
    audioTrackHandled = -1
    tracks = emptyList()
    localAudioIdentity.clear()
    chapters = emptyList()
    videoParams = emptyMap()
    directRefused = false
    if (engineSubtitleKey != null) { engineSubtitleKey = null; externalSubtitleAddress = null }
    subtitleToken = 0
    fontFetcher.cancel()
    core?.setFastTicks(false)
    core?.stop()
    if (referencesToken != 0L) {
      referencesToken = 0
      core?.set("access-references", "no")
    }
    referencesAllowed = false
    setVideoAspect(null)
    updateOutput()
    if (wasOpen) sessionPlayer.changed()
  }

  /**
   * Where a channel opens: the position being restored, or the seek in hand; null for the
   * server's own start (near the live edge). mpv cannot seek a live playlist outside what it has
   * read, so the playlist itself is asked to start there.
   */
  private fun linearStart(): Double? {
    if (linear == null) return null
    if (routeResume && !routeSuspended) return routePosition
    val pending = pendingSeekCommand()
    val revision = PorticoMpvSupport.integer(pending?.get("revision"))
    val target = PorticoMpvSupport.number(pending?.get("positionSeconds"))
    return if (revision != null && target != null && revision > seekRevision && target >= 0) target else null
  }

  private fun beginLoad(original: Uri, linearAt: Double? = null) {
    // The playlist starting at a channel position: the server serves it from the segment
    // holding that position (?at=milliseconds from the channel's origin).
    linearOpenedAt = linear != null && linearAt != null && linearAt.isFinite() && linearAt >= 0
    val url = if (linearOpenedAt)
      original.buildUpon().clearQuery().appendQueryParameter("at", Math.floor(linearAt!! * 1000).toLong().toString()).build() else original
    loadEpoch++
    val epoch = loadEpoch
    fileRequested = true
    loaded = false
    restarted = false
    displayHold = false
    endedEmitted = false
    directRefused = false
    pictureRetried = false
    lastPosition = Double.NaN
    lastProgressFact = Double.NaN
    snapshot = PorticoMpvSnapshot()
    audioTrackHandled = -1
    refusedSubtitleKeys.clear()
    frameRateClear?.let { main.removeCallbacks(it) }
    frameRateClear = null
    if (ensureCore() == null) {
      publishFailure(PorticoMpvNative.ERROR_VO_INIT_FAILED, "mpv could not start", emptyList())
      return
    }
    sessionPlayer.changed()
    if (url.scheme?.lowercase() == "file") {
      startLoad(url.path ?: "", epoch)
      return
    }
    relay.clearFailure()
    relay.bind(url) { local ->
      if (loadEpoch != epoch || !fileRequested) return@bind
      if (local == null) {
        publishFailure(PorticoMpvNative.ERROR_LOADING_FAILED, "relay unavailable", emptyList())
        return@bind
      }
      startLoad(local, epoch)
    }
  }

  private fun startLoad(address: String, epoch: Int) {
    val core = core ?: return
    var start: Double? = null
    // The position to start at is part of opening the file, so nothing is decoded from the
    // beginning only to be thrown away.
    if (routeResume && !routeSuspended) {
      routeSeeking = true
      start = routePosition
    } else {
      val pending = pendingSeekCommand()
      val revision = PorticoMpvSupport.integer(pending?.get("revision"))
      val target = PorticoMpvSupport.number(pending?.get("positionSeconds"))
      if (revision != null && target != null && revision > seekRevision) {
        seekRevision = revision
        seeking = true
        startSeek = Pair(revision, target)
        start = target
      }
    }
    val options = HashMap<String, String>()
    if (linear != null) {
      // A channel is placed by its clock: media time stays the stream's own (so the server's
      // clock epochs apply), what has been read stays seekable, and a playlist reopened at a
      // position is read from its first segment. The position itself is then reached by seek.
      options["rebase-start-time"] = "no"
      options["demuxer-seekable-cache"] = "yes"
      if (linearOpenedAt) options["demuxer-lavf-o"] = "protocol_whitelist=%20%file,http,tcp,crypto,live_start_index=0"
      if (startSeek != null) {
        // The seek is made once the picture is up (applyPendingSeek), on the clock.
        seeking = false
        seekRevision = startSeek!!.first - 1
        startSeek = null
      }
    } else if (start != null && start > 0) options["start"] = PorticoMpvSupport.decimal(start)
    val media = PorticoMpvSupport.parse(source)
    referencesAllowed = media != null && media.scheme?.lowercase() != "file" && (media.path ?: "").lowercase().endsWith(".m3u8")
    if (referencesAllowed) {
      options["access-references"] = "yes"
      options["demuxer-lavf-format"] = "hls"
    }
    core.setAll(listOf("pause" to "yes", "sid" to "no", "aid" to "auto", "vid" to "auto", "sub-delay" to "0", "volume" to "100"))
    core.load(address, options, epoch)
  }

  // --- mpv events ------------------------------------------------------------------------

  private fun handle(epoch: Int, event: PorticoMpvEvent, next: PorticoMpvSnapshot) {
    if (epoch != loadEpoch || !fileRequested || invalidated || lease == null) return
    if (event is PorticoMpvEvent.FastTick) {
      subtitleOverlay.tick((event.time * 1_000_000).toLong())
      return
    }
    val before = snapshot
    snapshot = next
    if (next.timePos.isFinite()) lastPosition = next.timePos
    when (event) {
      is PorticoMpvEvent.FileLoaded -> {
        loaded = true
        tracks = event.tracks
        chapters = event.chapters
        applyAudio()
        applyAudioTrack()
        applySubtitles()
      }
      is PorticoMpvEvent.PlaybackRestart -> {
        val flight = seekInFlight
        if (!restarted) {
          restarted = true
          firstFrame()
        } else if (flight != null) {
          flight.restartSeen = true
          finishRestartedSeek()
        }
      }
      is PorticoMpvEvent.EndFile -> {
        if (event.reason == PorticoMpvNative.END_ERROR) publishFailure(event.code, event.message, event.log)
        else if (event.reason == PorticoMpvNative.END_EOF) {
          if (!restarted) publishFailure(PorticoMpvNative.ERROR_NOTHING_TO_PLAY, "file ended before playback began", emptyList())
          else reachedEnd()
        }
      }
      is PorticoMpvEvent.TracksChanged -> {
        tracks = event.tracks
        removeStaleExternalSubtitles()
        if (loaded) {
          applyAudio()
          applySubtitles()
          publishMediaInfo()
        }
      }
      is PorticoMpvEvent.VideoParams -> {
        videoParams = event.params
        val width = PorticoMpvSupport.number(event.params["dw"])
        val height = PorticoMpvSupport.number(event.params["dh"])
        if (width != null && height != null && width > 0 && height > 0) {
          val turned = (PorticoMpvSupport.number(event.params["rotate"])?.toInt() ?: 0) % 180 != 0
          setVideoAspect(if (turned) height / width else width / height)
        }
        if (restarted) {
          updateOutput()
          publishMediaInfo()
        }
      }
      is PorticoMpvEvent.SubtitleText -> fontForText(event.text)
      is PorticoMpvEvent.AudioTrackReadback -> {
        val pending = audioTrackPending
        if (pending != null && pending.first == event.revision) {
          if (event.code < 0) failAudioTrack(pending.first, pending.third)
          else {
            audioTrackReadbackRevision = event.revision
            confirmAudioTrack()
          }
        }
      }
      is PorticoMpvEvent.Changed -> when (event.name) {
        "paused-for-cache" -> {
          if (next.pausedForCache && !before.pausedForCache && hasPlayed && !next.eofReached) emit("stalled", null)
          emit("rateStatus", null)
          sessionPlayer.changed()
        }
        "eof-reached" -> if (next.eofReached) reachedEnd() else endedEmitted = false
        "aid" -> {
          sampleAudio()
          confirmAudioTrack()
        }
        "vid" -> pictureLost()
        "pause", "speed", "core-idle" -> {
          emit("rateStatus", null)
          sessionPlayer.changed()
        }
        "duration" -> if (restarted) { publishMediaInfo(); sessionPlayer.changed() }
        "hwdec-current" -> if (restarted) publishMediaInfo()
        else -> {}
      }
      is PorticoMpvEvent.Reply -> {
        if (event.token == referencesToken && event.token != 0L) closeReferences()
        val flight = seekInFlight
        if (flight != null && flight.token == event.token) {
          if (event.code < 0) {
            seekInFlight = null
            if (!reopenLinearAt(flight.revision, flight.target)) completeSeek(flight.revision, flight.target, false)
          } else {
            flight.commandAccepted = true
            finishRestartedSeek()
          }
        } else if (event.token == subtitleToken && event.token != 0L && event.code < 0) {
          // The subtitle file could not be opened: fall back to the plain cue document.
          refusedSubtitleKeys.add(subtitleTokenKey)
          clearEngineSubtitle()
          applySubtitles()
        }
      }
      is PorticoMpvEvent.Tick -> {
        // An end reached before the first frame was announced is announced now.
        if (next.eofReached) reachedEnd()
        finishRestartedSeek()
        applyPendingSeek()
        val position = currentPosition()
        // A paused picture reports once, not twice a second for as long as it stays paused.
        if (position.isFinite() && (position != lastProgressFact || next.pause != lastProgressPaused)) {
          lastProgressFact = position
          lastProgressPaused = next.pause
          emit("progress", null)
        }
      }
      is PorticoMpvEvent.FastTick -> {}
    }
  }

  /** The first frame of a newly opened file is decoded and on screen. */
  private fun firstFrame() {
    // mpv plays on without a picture, or without sound, when it has no decoder for a track.
    // That is a failure of this engine for this file, and the server is told so.
    if (PorticoMpvSupport.hasVideo(tracks) && snapshot.vid == "no") {
      publishFailure(PorticoMpvNative.ERROR_UNSUPPORTED, "video track could not be decoded", emptyList(), "decode_error")
      return
    }
    if (PorticoMpvSupport.tracks(tracks, "audio").isNotEmpty() && snapshot.aid == "no") {
      // No sound is as often the output refusing to open (a call in progress) as a track mpv
      // cannot decode, so the media is not blamed for it.
      publishFailure(PorticoMpvNative.ERROR_AO_INIT_FAILED, "audio could not start", emptyList())
      return
    }
    hasPlayed = true
    emit("firstFrame", null)
    updateOutput()
    matchDisplay { displayReady() }
  }

  private fun displayReady() {
    if (routeResume) {
      routeSeeking = false
      val actual = currentPosition()
      // A channel reopens at the segment holding the position: within one segment of it.
      val tolerance = if (linear != null) 8.0 else 0.5
      if (!actual.isFinite() || Math.abs(actual - routePosition) > tolerance) {
        routeResume = false
        routeFailed = true
        routeEpoch++
        core?.set("pause", "yes")
        emit("error", "The previous playback position is unavailable on this connection. Your position has been retained.")
        return
      }
      routeResume = false
      emit("ready", null)
    } else if (!routeFailed) {
      emit("ready", null)
    }
    startSeek?.let { start ->
      startSeek = null
      completeSeek(start.first, start.second, true)
    }
    publishMediaInfo()
    applyPendingSeek()
    applyIntent()
    sessionPlayer.changed()
  }

  private fun localSource(): Boolean = PorticoMpvSupport.parse(source)?.scheme?.lowercase() == "file"

  private fun reachedEnd() {
    if (!restarted || endedEmitted) return
    val duration = snapshot.duration
    val position = currentPosition()
    // A connection that died leaves mpv at the end of what it had: that is not the end of the
    // title. Unresolved media gaps belong to this relay binding and survive while its cache drains.
    val failure = relay.unresolvedFailure
    if (failure != null && !localSource() && duration > 0 && position.isFinite() && position < duration - 3) {
      publishFailure(PorticoMpvNative.ERROR_LOADING_FAILED, "stream ended early", emptyList(), relayFailure = failure)
      return
    }
    endedEmitted = true
    emit("ended", null)
    sessionPlayer.changed()
  }

  // --- intent, seeking -------------------------------------------------------------------

  private fun desiredRate(): Double = PorticoMpvSupport.number(command?.get("rate")) ?: 1.0

  private fun ready(): Boolean = fileRequested && loaded && restarted

  private fun supportedRates(): List<Double> = if (ready()) PorticoMpvSupport.RATES else emptyList()

  private fun effectiveRate(): Double {
    if (!ready() || snapshot.pause || snapshot.pausedForCache || snapshot.coreIdle || snapshot.eofReached) return 0.0
    return snapshot.speed
  }

  private fun currentPosition(): Double {
    val media = if (!fileRequested || !snapshot.timePos.isFinite()) lastPosition else snapshot.timePos
    val clock = linear ?: return media
    return clock.position(media)
  }

  private fun applyIntent() {
    val core = core
    if (core == null || !fileRequested) return
    val rate = desiredRate()
    val held = desiredPaused || seeking || pendingSeekCommand() != null || routeSuspended || routeResume || routeSeeking || routeFailed ||
        displayHold || backgroundHold || !ready() || !PorticoMpvSupport.RATES.contains(rate)
    // Sound is asked for when playback is about to start, and not before.
    if (!held && !focusHeld && !focusLost) requestAudioFocus()
    if (held || focusHold) {
      core.set("pause", "yes")
    } else {
      core.setAll(listOf("speed" to PorticoMpvSupport.decimal(rate, 2), "pause" to "no"))
    }
    emit("rateStatus", null)
    sessionPlayer.changed()
  }

  private fun applyPendingSeek() {
    val core = core ?: return
    val pending = pendingSeekCommand() ?: return
    if (routeSuspended || routeResume || routeSeeking || !ready() || displayHold) return
    val revision = PorticoMpvSupport.integer(pending["revision"]) ?: return
    if (revision <= seekRevision) return
    val target = PorticoMpvSupport.number(pending["positionSeconds"]) ?: return
    if (target < 0) return
    seekRevision = revision
    seeking = true
    startSeek = null
    seekToken++
    seekInFlight = SeekFlight(seekToken, revision, target)
    core.set("pause", "yes")
    // A channel position is sought as the media time its clock maps it to.
    val media = linear?.media(target, snapshot.timePos) ?: target
    core.command(listOf("seek", PorticoMpvSupport.decimal(media), "absolute+exact"), seekToken)
    if (linear != null) {
      // Outside what mpv holds of a live playlist the seek is accepted and then quietly not
      // made: no new frame comes. That is a refusal too.
      val flight = seekInFlight
      main.postDelayed({
        if (!invalidated && seekInFlight === flight && flight != null && !flight.restartSeen) {
          seekInFlight = null
          if (!reopenLinearAt(flight.revision, flight.target)) completeSeek(flight.revision, flight.target, false)
        }
      }, 3_000)
    }
  }

  /**
   * mpv holds only what it has read of a live playlist. A channel seek it refuses is made again
   * once, on the playlist reopened at the target; a second refusal is a failed seek.
   */
  private fun reopenLinearAt(revision: Long, target: Double): Boolean {
    if (linear == null || linearReloaded == revision || source.isNullOrEmpty()) return false
    val url = PorticoMpvSupport.parse(source) ?: return false
    linearReloaded = revision
    seekInFlight = null
    seekRevision = revision - 1
    seeking = true
    subtitleOverlay.invalidate()
    resetAudio()
    unload()
    beginLoad(url, target)
    return true
  }

  /** A seek is done when mpv has both accepted it and shown the frame there, in whichever order they are reported. */
  private fun finishRestartedSeek() {
    val flight = seekInFlight ?: return
    if (!flight.commandAccepted || !flight.restartSeen || snapshot.seeking) return
    seekInFlight = null
    completeSeek(flight.revision, flight.target, true)
  }

  private fun completeSeek(revision: Long, target: Double, finished: Boolean) {
    val current = pendingSeekCommand()
    if (seekRevision != revision || current == null || PorticoMpvSupport.integer(current["revision"]) != revision) return
    seeking = false
    endedEmitted = false
    val observed = currentPosition()
    val applied = finished && observed.isFinite() && Math.abs(observed - target) <= 0.5
    val fact = HashMap<String, Any?>()
    fact["instanceId"] = instanceId
    fact["type"] = if (applied) "seekApplied" else "seekFailed"
    fact["intentId"] = intentId.toDouble()
    fact["seekRevision"] = revision.toDouble()
    fact["position"] = if (observed.isFinite()) observed else 0.0
    fact["paused"] = pausedFact()
    fact["error"] = if (applied) "" else "That position is not available yet. Try seeking again."
    fact.putAll(rateFacts())
    publish(fact)
    sessionPlayer.changed()
  }

  // --- facts -----------------------------------------------------------------------------

  private fun pausedFact(): Boolean = snapshot.pause && !seeking && !routeResume

  private fun rateFacts(): Map<String, Any?> {
    val isReady = ready()
    val valid = PorticoMpvSupport.RATES.contains(desiredRate())
    return mapOf(
        "effectiveRate" to effectiveRate(),
        "supportedRates" to supportedRates(),
        "rateReady" to isReady,
        "rateWaiting" to (isReady && !snapshot.pause && (snapshot.pausedForCache || snapshot.coreIdle) && !snapshot.eofReached),
        "rateError" to (if (isReady && !valid) "This speed is unavailable for this media. Choose a supported speed, then press Play." else ""),
        "monotonicSeconds" to SystemClock.elapsedRealtime() / 1000.0)
  }

  private fun publish(fact: MutableMap<String, Any?>) {
    val send = onFact ?: return
    val bound = binding ?: return
    if (lease == null || routeSuspended) return
    if ((routeResume || routeSeeking || routeFailed) && fact["type"] != "error") return
    fact["itemInstanceId"] = itemInstanceId
    fact["binding"] = bound
    fact["factSequence"] = (++factSequence).toDouble()
    fact["lastCommandRevision"] = commandRevision.toDouble()
    fact["seeking"] = seeking || pendingSeekCommand() != null
    fact["engine"] = "mpv"
    send(Arguments.makeNativeMap(fact))
  }

  private fun baseFact(type: String, position: Double, error: String?): HashMap<String, Any?> {
    val fact = HashMap<String, Any?>()
    fact["instanceId"] = instanceId
    fact["type"] = type
    fact["intentId"] = intentId.toDouble()
    fact["position"] = if (position.isFinite()) position else 0.0
    fact["paused"] = pausedFact()
    fact["error"] = error ?: ""
    fact.putAll(rateFacts())
    return fact
  }

  private fun emit(type: String, error: String?) {
    if (onFact == null || source.isNullOrEmpty()) return
    val position = if (routeResume || routeFailed) routePosition else currentPosition()
    if (type == "progress") {
      if (effectiveRate() > 0 && !seeking && !routeResume && position.isFinite() && previousProgress.isFinite() && position > previousProgress + 0.05) hasPlayed = true
      previousProgress = position
    }
    val fact = baseFact(type, position, error)
    if (type == "progress" && source?.startsWith("file:") != true) {
      // What the reader measured of the connection, for Automatic quality: only while it is
      // reading as fast as it can. With the cache full it rests, and its speed is then the
      // title's own bit rate, which says nothing about the connection.
      if (!snapshot.cacheIdle && snapshot.cacheSpeed.isFinite() && snapshot.cacheSpeed > 0) fact["throughputKbps"] = Math.round(snapshot.cacheSpeed * 8 / 1000).toDouble()
      if (snapshot.cacheDuration.isFinite() && snapshot.cacheDuration >= 0) fact["bufferedSeconds"] = snapshot.cacheDuration
    }
    publish(fact)
  }

  /**
   * One failure, classified the way the Media3 engine classifies its own: a network failure is
   * weather and is retried on the same route; an engine failure (no decoder, a file mpv cannot
   * read) carries its class so the server can be asked for the next route; anything whose cause
   * is unclear says so and blames nobody.
   */
  private fun publishFailure(code: Int, message: String, log: List<String>, named: String = "", relayFailure: PorticoMpvRelay.Failure? = null) {
    var network = false
    var delivery = false
    var uncertain = false
    var httpStatus = 0
    var engine = named
    val local = localSource()
    if (engine.isEmpty()) {
      val failure = relayFailure ?: relay.lastFailure?.takeIf { SystemClock.elapsedRealtime() - it.at <= 15_000 }
      if (!local && failure != null) {
        if (failure.httpStatus in 400..599) httpStatus = failure.httpStatus
        else if (failure.httpStatus == 0) { if (failure.permanent) delivery = true else network = true }
      }
      if (httpStatus != 0) {
        delivery = true
        network = httpStatus >= 500 || httpStatus == 408 || httpStatus == 429
      } else if (!network && !delivery) {
        when (code) {
          // Not a format mpv knows, nothing in it mpv can play, or not supported.
          PorticoMpvNative.ERROR_UNKNOWN_FORMAT, PorticoMpvNative.ERROR_NOTHING_TO_PLAY, PorticoMpvNative.ERROR_UNSUPPORTED -> engine = "source_not_supported"
          // The file could not be opened at all. On this device that is the file; over the
          // network, with no answer to go by, it is most often the connection.
          PorticoMpvNative.ERROR_LOADING_FAILED -> if (local) delivery = true else network = true
          // The picture or sound output would not start, or mpv gave no reason: the media is
          // not to blame, and nothing is reported against it.
          else -> uncertain = true
        }
      }
    }
    val position = if (routeResume || routeFailed) routePosition else currentPosition()
    var detail = "mpv $code $message"
    log.lastOrNull()?.takeIf { it.isNotEmpty() }?.let { detail += " / $it" }
    val fact = HashMap<String, Any?>()
    fact["instanceId"] = instanceId
    fact["type"] = "error"
    fact["intentId"] = intentId.toDouble()
    fact["position"] = if (position.isFinite()) position else 0.0
    fact["paused"] = true
    fact["networkError"] = network
    fact["deliveryError"] = delivery
    fact["httpStatus"] = httpStatus
    fact["hasPlayed"] = hasPlayed
    fact["uncertain"] = uncertain
    fact["engineCode"] = if (network || delivery) "" else engine
    fact["errorDetail"] = detail.take(240)
    fact["error"] = if (network) "The media connection was interrupted. Verifying another route." else "This media could not be played. Check its format or try again."
    fact.putAll(rateFacts())
    core?.set("pause", "yes")
    publish(fact)
    sessionPlayer.changed()
  }

  private fun selectedVideo(): Map<String, Any?>? = PorticoMpvSupport.tracks(tracks, "video").firstOrNull { it["selected"] == true }

  /** What the file turned out to hold: its length, chapters, picture and tracks. */
  private fun publishMediaInfo() {
    if (onFact == null || !ready()) return
    val video = selectedVideo()
    val proof = localAudioIdentity.get(if (localSource()) source else null)
    val localAudio = tracks.asSequence().filter { it["type"] == "audio" && it["external"] != true }.take(65).toList()
    val sourceIds = if (localAudio.size <= 64) localAudio.groupingBy { it["src-id"] }.eachCount() else emptyMap()
    val picture = HashMap<String, Any?>()
    picture["dynamicRange"] = sourceRange()
    picture["hardwareDecoding"] = snapshot.hwdec != "no" && snapshot.hwdec.isNotEmpty()
    picture["outputDynamicRange"] = if (directOutput) "hdr" else "sdr"
    PorticoMpvSupport.number(videoParams["w"])?.let { picture["width"] = it.toInt() }
    PorticoMpvSupport.number(videoParams["h"])?.let { picture["height"] = it.toInt() }
    PorticoMpvSupport.number(video?.get("demux-fps"))?.takeIf { it > 0 }?.let { picture["framesPerSecond"] = it }
    (video?.get("codec") as? String)?.let { picture["codec"] = it }
    val describe: (Map<String, Any?>) -> Map<String, Any?> = { track ->
      val out = HashMap<String, Any?>()
      out["selected"] = track["selected"] == true
      out["external"] = track["external"] == true
      PorticoMpvSupport.streamIndex(track)?.let { out["streamIndex"] = it }
      if (track["type"] == "audio" && sourceIds[track["src-id"]] == 1) {
        proof.mpv(snapshot.fileFormat, track["src-id"], track["external"] == true)?.let { out["sourceTrackIdentity"] = it }
      }
      (track["lang"] as? String)?.let { out["language"] = it }
      (track["title"] as? String)?.let { out["title"] = it }
      (track["codec"] as? String)?.let { out["codec"] = it }
      PorticoMpvSupport.number(track["demux-channel-count"])?.let { out["channels"] = it.toInt() }
      if (track["default"] == true) out["default"] = true
      if (track["forced"] == true) out["forced"] = true
      out
    }
    val marks = ArrayList<Map<String, Any?>>()
    for (chapter in chapters.take(500)) {
      val time = PorticoMpvSupport.number(chapter["time"]) ?: continue
      if (time < 0) continue
      marks.add(mapOf("startSeconds" to time, "title" to ((chapter["title"] as? String) ?: "").take(256)))
    }
    val fact = baseFact("mediaInfo", currentPosition(), null)
    fact["durationSeconds"] = if (snapshot.duration.isFinite()) snapshot.duration else 0.0
    fact["chapters"] = marks
    fact["video"] = picture
    fact["audioTracks"] = PorticoMpvSupport.tracks(tracks, "audio").take(64).map(describe)
    fact["subtitleTracks"] = PorticoMpvSupport.tracks(tracks, "sub").take(128).map(describe)
    publish(fact)
  }

  // --- audio -----------------------------------------------------------------------------

  /** A track command applies only to the playback it was made for. */
  private fun scoped(value: Any?): Map<String, Any?>? {
    val part = PorticoMpvSupport.map(value) ?: return null
    val own = PorticoMpvSupport.map(part["binding"]) ?: return null
    val bound = binding ?: return null
    for (field in listOf("intentId", "sessionId", "generation")) if (own[field] == null || own[field] != bound[field]) return null
    return part
  }

  private fun resetAudio() {
    audioCommand = null
    audioMap = null
    audioMappingAttempted = false
    audioSequence = 0
    audioHandledRevision = -1
  }

  private fun emitAudio(type: String, extra: Map<String, Any?>) {
    val audio = audioCommand ?: return
    if (onFact == null || source.isNullOrEmpty()) return
    val audioBinding = PorticoMpvSupport.map(audio["binding"]) ?: return
    if (PorticoMpvSupport.integer(audioBinding["intentId"]) != intentId) return
    val epoch = audio["epoch"] ?: return
    val fact = baseFact(type, currentPosition(), null)
    fact.remove("error")
    fact["audioBinding"] = audioBinding
    fact["audioEpoch"] = epoch
    fact.putAll(extra)
    publish(fact)
  }

  private fun currentAudioTrack(): Int? = snapshot.aid.toIntOrNull()

  /**
   * The server's audio renditions (an HLS stream's named tracks, or a file's tracks by stream
   * index) are mapped onto mpv's tracks once per file; the pending choice is then selected.
   */
  private fun applyAudio() {
    val audio = scoped(command?.get("audio"))
    if (audio == null || PorticoMpvSupport.map(audio["binding"]) == null || PorticoMpvSupport.number(audio["epoch"]) == null || audio["renditions"] !is List<*>) {
      resetAudio()
      return
    }
    val sameBinding = audioCommand?.get("binding") == audio["binding"]
    val sameEpoch = audioCommand?.get("epoch") == audio["epoch"]
    if (!sameBinding || !sameEpoch) resetAudio()
    audioCommand = audio
    if (!loaded) return
    if (audioMap == null) {
      if (audioMappingAttempted) return
      audioMappingAttempted = true
      val renditions = (audio["renditions"] as List<*>).mapNotNull { PorticoMpvSupport.map(it) }
      val map = if (renditions.size == (audio["renditions"] as List<*>).size) PorticoMpvSupport.mapRenditions(renditions, PorticoMpvSupport.tracks(tracks, "audio")) else null
      if (map == null) {
        emitAudio("audioMapped", mapOf("renditionIds" to emptyList<String>(), "audioMappingFailure" to "unsupported_metadata"))
        return
      }
      audioMap = map
      emitAudio("audioMapped", mapOf("renditionIds" to map.keys.toList()))
      sampleAudio()
    }
    selectPendingAudio()
  }

  private fun sampleAudio() {
    val map = audioMap ?: return
    val audio = audioCommand ?: return
    val current = currentAudioTrack()
    val observed = map.entries.firstOrNull { it.value == current }?.key
    audioSequence++
    emitAudio("audioObserved", mapOf("audioSequence" to audioSequence, "renditionId" to observed))
    val pending = PorticoMpvSupport.map(audio["pending"]) ?: return
    val revision = PorticoMpvSupport.integer(pending["revision"])
    val wanted = pending["renditionId"] as? String
    if (revision != null && revision == audioHandledRevision && wanted != null && wanted == observed) {
      emitAudio("audioApplied", mapOf("audioRevision" to revision.toDouble(), "renditionId" to wanted))
    }
  }

  private fun selectPendingAudio() {
    val map = audioMap ?: return
    val pending = PorticoMpvSupport.map(audioCommand?.get("pending")) ?: return
    val revision = PorticoMpvSupport.integer(pending["revision"]) ?: return
    if (revision == audioHandledRevision) return
    audioHandledRevision = revision
    val wanted = pending["renditionId"] as? String
    val track = wanted?.let { map[it] }
    if (track == null) {
      emitAudio("audioFailed", mapOf("audioRevision" to revision.toDouble(), "error" to "This audio track is unavailable in the current player."))
      return
    }
    core?.set("aid", track.toString())
    // Choosing the track already in use changes nothing, so no change is announced: sample now.
    if (currentAudioTrack() == track) sampleAudio()
  }

  /** A direct-played file's audio track, chosen by its stream index (`audioTrack`). */
  private fun applyAudioTrack() {
    if (!loaded) return
    val wanted = PorticoMpvSupport.map(command?.get("audioTrack")) ?: return
    val revision = PorticoMpvSupport.integer(wanted["revision"]) ?: return
    // JavaScript sends rising revisions. A replayed older command must not supersede the
    // current readback, so equal or lower revisions are already handled.
    if (revision <= audioTrackHandled) return
    val index = PorticoMpvSupport.integer(wanted["streamIndex"], 0.0) ?: return
    val found = PorticoMpvSupport.tracks(tracks, "audio").filter { PorticoMpvSupport.streamIndex(it)?.toLong() == index }
    val track = if (found.size == 1) PorticoMpvSupport.trackId(found[0]) else null
    if (track == null) {
      audioTrackHandled = revision
      failAudioTrack(revision, index)
      return
    }
    val core = core
    // Nothing can be posted without a live core; leave this revision unhandled so the next
    // command tick retries it once the engine is back.
    if (core == null) return
    cancelAudioTrackTimeout()
    audioTrackPending = Triple(revision, track, index)
    audioTrackReadbackRevision = -1
    if (!core.selectAudioTrack(track, revision, loadEpoch)) {
      // A stopped Handler rejected the post. Keep the revision retryable.
      audioTrackPending = null
      return
    }
    audioTrackHandled = revision
    // A guard-dropped command is answered with a failure readback. An `aid` mpv accepts but
    // coerces never produces the exact readback, so bound the wait like the native path.
    val timeout = Runnable {
      val pending = audioTrackPending
      if (pending != null && pending.first == revision) failAudioTrack(pending.first, pending.third)
    }
    audioTrackTimeout = timeout
    main.postDelayed(timeout, AUDIO_TRACK_TIMEOUT_MS)
  }

  private fun confirmAudioTrack() {
    val pending = audioTrackPending ?: return
    if (audioTrackReadbackRevision != pending.first || currentAudioTrack() != pending.second) return
    cancelAudioTrackTimeout()
    audioTrackPending = null
    val fact = baseFact("audioTrackApplied", currentPosition(), null)
    fact["audioTrackRevision"] = pending.first.toDouble()
    fact["streamIndex"] = pending.third.toDouble()
    publish(fact)
  }

  private fun failAudioTrack(revision: Long, index: Long) {
    cancelAudioTrackTimeout()
    audioTrackPending = null
    audioTrackReadbackRevision = -1
    val fact = baseFact("audioTrackFailed", currentPosition(), "This audio track is unavailable in the current player.")
    fact["audioTrackRevision"] = revision.toDouble()
    fact["streamIndex"] = index.toDouble()
    publish(fact)
  }

  private fun cancelAudioTrackTimeout() {
    audioTrackTimeout?.let { main.removeCallbacks(it) }
    audioTrackTimeout = null
  }

  // --- subtitles -------------------------------------------------------------------------

  /** The chosen subtitles are drawn by the app (the plain cue document) rather than by mpv. */
  val subtitleOverlayActive: Boolean get() = subtitleOverlay.overlayActive

  private fun clearEngineSubtitle() {
    if (engineSubtitleKey == null && externalSubtitleAddress == null && subtitleToken == 0L) return
    engineSubtitleKey = null
    core?.set("sid", "no")
    removeExternalSubtitle()
  }

  private fun removeExternalSubtitle() {
    val address = externalSubtitleAddress ?: return
    externalSubtitleAddress = null
    subtitleToken = 0
    fontFetcher.cancel()
    PorticoMpvFonts.useAttachedFolder(null)
    core?.set("sub-fonts-dir", PorticoMpvFonts.fallbackDirectory(context).path)
    for (track in PorticoMpvSupport.tracks(tracks, "sub")) {
      if (track["external-filename"] != address) continue
      PorticoMpvSupport.trackId(track)?.let { core?.command(listOf("sub-remove", it.toString())) }
    }
  }

  /** The one companion file has been opened (or the adding failed): files may name no others again. */
  private fun closeReferences() {
    if (referencesToken == 0L) return
    referencesToken = 0
    if (!referencesAllowed) core?.set("access-references", "no")
  }

  /**
   * A subtitle file whose adding was already under way can arrive after its choice was replaced
   * or turned off. Such late tracks are removed, so the current choice is never overridden.
   */
  private fun removeStaleExternalSubtitles() {
    for (track in PorticoMpvSupport.tracks(tracks, "sub")) {
      if (track["external"] != true || track["external-filename"] == externalSubtitleAddress) continue
      PorticoMpvSupport.trackId(track)?.let { core?.command(listOf("sub-remove", it.toString())) }
    }
  }

  private fun overlayOff() {
    subtitleOverlay.apply(null, source)
    core?.setFastTicks(false)
  }

  /**
   * Draws the chosen track with mpv itself (styled text through libass, image subtitles as
   * images) when the command says where the track is (`render`); otherwise the app draws the
   * server's plain cue document over the picture, exactly as the Media3 engine does.
   *
   * Plain text that the server also publishes as a cue document is drawn by the app: Android's
   * own text drawing has a font for every script, and mpv here has one default font.
   */
  /**
   * Portico's caption style on top of the system's: the size multiplies the size the viewer set
   * for captions in Accessibility; the background is a soft box, a solid box, or none (the line
   * then gets an outline instead, so it stays readable). Styled (ASS) tracks keep their own look;
   * this is for plain text mpv draws, and the app's own line follows the same style.
   */
  private fun applyCaptionStyle(style: Map<*, *>?) {
    val same = style == captionStyle
    captionStyle = style
    val render = PorticoMpvSupport.map(scoped(command?.get("subtitles"))?.get("render"))
    core?.set("sub-ass-override", PorticoMpvSupport.captionAssOverride(style, render?.get("kind")))
    core?.set("sub-font", PorticoMpvFonts.captionFamily(style))
    core?.set("sub-align-y", if (PorticoMpvSupport.captionAtTop(style, false)) "top" else "bottom")
    core?.set("sub-margin-y", PorticoMpvSupport.mpvCaptionMargin(style).toString())
    core?.set("sub-scale", PorticoMpvSupport.decimal(PorticoMpvSupport.captionScale(context, style) * PorticoMpvSupport.styleScale(style), 2))
    // Plain/stripped text uses the same canonical/system colour as the cue view.
    // An ASS track retaining its file style ignores these fallback properties
    // through sub-ass-override=scale above, retaining caption size scaling.
    val (text, box) = PorticoMpvSupport.captionColors(context, style)
    val edge = PorticoMpvSupport.mpvCaptionEdges(style, box)
    core?.set("sub-color", PorticoMpvSupport.captionColor(text))
    core?.set("sub-back-color", PorticoMpvSupport.captionColor(edge.backColor))
    core?.set("sub-border-style", if (edge.boxed) "background-box" else "outline-and-shadow")
    core?.set("sub-border-size", edge.outline.toString())
    core?.set("sub-outline-size", edge.outline.toString())
    core?.set("sub-shadow-offset", edge.shadow.toString())
    if (!same) overlayListeners.toList().forEach { it() }
  }

  private fun applySubtitles() {
    val subtitles = scoped(command?.get("subtitles"))
    applyCaptionStyle(subtitles?.get("style") as? Map<*, *>)
    // "Forced only": image subtitles show just their forced lines (foreign dialogue, signs).
    core?.set("sub-forced-events-only", if (subtitles?.get("forcedOnly") == true) "yes" else "no")
    if (subtitles == null || subtitles["mode"] != "track") {
      clearEngineSubtitle()
      overlayOff()
      updateOutput()
      return
    }
    val document = subtitles["documentUrl"] is String
    val render = PorticoMpvSupport.map(subtitles["render"])
    if (render != null && !(render["kind"] == "text" && document)) {
      val offset = PorticoMpvSupport.originalSubtitleDelay(subtitles, render)
      if (offset == null) {
        clearEngineSubtitle()
        overlayOff()
        emit("subtitleStatus", "subtitle_source_invalid")
        updateOutput()
        return
      }
      // Which mpv track that is can only be said once the file is open.
      if (!loaded) return
      val embedded = PorticoMpvSupport.map(render["embedded"])
      val all = PorticoMpvSupport.tracks(tracks, "sub")
      val embeddedTrack = embedded?.let { PorticoMpvSupport.embeddedSubtitle(it, (render["kind"] as? String) ?: "", all) }
      val externalVobSub = render["kind"] == "vobsub" && embeddedTrack == null
      val pairValid = !externalVobSub || checkedVobSubCompanion(render, subtitles) != null
      val key = PorticoMpvSupport.originalSubtitleKey(subtitles, render)
      val action = PorticoMpvSupport.originalSubtitleAction(key, engineSubtitleKey, refusedSubtitleKeys, externalVobSub, pairValid)
      if (action == PorticoMpvSupport.OriginalSubtitleAction.INVALID) {
        clearEngineSubtitle()
        overlayOff()
        emit("subtitleStatus", "subtitle_source_invalid")
        updateOutput()
        return
      }
      if (action != PorticoMpvSupport.OriginalSubtitleAction.REFUSED) {
        if (action == PorticoMpvSupport.OriginalSubtitleAction.REUSE) {
          // The choice stands: make sure it is still the track mpv has selected.
          val address = externalSubtitleAddress
          val track = embeddedTrack
              ?: address?.let { all.firstOrNull { candidate -> candidate["external-filename"] == it } }?.let { PorticoMpvSupport.trackId(it) }
          if (track != null && snapshot.sid != track.toString()) core?.set("sid", track.toString())
          core?.set("sub-delay", offset)
          return
        }
        clearEngineSubtitle()
        // mpv draws the track, so mpv must be drawing the picture before the track is chosen.
        engineSubtitleKey = key
        updateOutput()
        if (startEngineSubtitle(render, key, subtitles)) {
          core?.set("sub-delay", offset)
          overlayOff()
          emit("subtitleStatus", "")
          return
        }
        engineSubtitleKey = null
        refusedSubtitleKeys.add(key)
      }
    }
    clearEngineSubtitle()
    updateOutput()
    if (!document) {
      overlayOff()
      emit("subtitleStatus", "This subtitle track is unavailable in the current player.")
      return
    }
    subtitleOverlay.apply(subtitles, source)
    core?.setFastTicks(true)
  }

  /** The track whose lines were already looked at for their script (one look per choice). */
  private var fontCheckedKey: String? = null
  private var fontCheckedLines = 0

  /**
   * N16: the face mpv draws a track with is chosen by its language tag. A track with no tag, the
   * wrong one, or lines in another script would show empty boxes, since the media engine's
   * libass has no system font lookup to fall back on. So the first lines of the track are read:
   * when they are written in a script the chosen face does not draw, the face for that script
   * joins the fallback chain (and becomes the default face) and the track is opened again (once
   * per new face, within its first lines).
   */
  private fun fontForText(text: String) {
    val key = engineSubtitleKey ?: return
    if (text.isBlank()) return
    if (fontCheckedKey != key) { fontCheckedKey = key; fontCheckedLines = 0 }
    if (fontCheckedLines >= 8) return
    fontCheckedLines++
    if (!PorticoMpvFonts.selectFor(context, text)) return
    val sid = snapshot.sid
    if (sid.isEmpty() || sid == "no" || sid == "false") return
    core?.set("sid", "no")
    core?.set("sid", sid)
  }

  /** Revalidate the current supplied pair, including both local artifacts,
   * before cache admission and again before an original can be opened. */
  private fun checkedVobSubCompanion(render: Map<String, Any?>, subtitles: Map<String, Any?>): Uri? {
    val media = PorticoMpvSupport.parse(source) ?: return null
    val indexText = render["url"] as? String ?: return null
    val offered = render["companionUrl"] as? String ?: return null
    if (listOf(indexText, offered).any { it.length > 8192 || it.toByteArray(Charsets.UTF_8).size > 8192 }) return null
    val index = PorticoMpvFontFetcher.resolve(indexText, media) ?: return null
    val companion = PorticoMpvFontFetcher.resolve(offered, media) ?: return null
    if (!PorticoMpvSupport.vobSubPair(index.toString(), companion.toString(), media.toString(),
        subtitles["resourceId"] as? String, PorticoMpvSupport.integer(subtitles["resourceRevision"]))) return null
    if (index.scheme?.lowercase() == "file") {
      if (!PorticoMpvSupport.allowedLocalSource(context, index) || !PorticoMpvSupport.allowedLocalSource(context, companion)) return null
    } else if (!PorticoMpvSupport.subtitleFileBelongs(index, media) || !PorticoMpvSupport.subtitleFileBelongs(companion, media)) return null
    return companion
  }

  /**
   * Starts mpv drawing the track `render` describes: the track inside the media itself when the
   * description names one and the media carries it, otherwise the original subtitle file at
   * `url`. False when neither can be done here.
   */
  private fun startEngineSubtitle(render: Map<String, Any?>, key: String, subtitles: Map<String, Any?>): Boolean {
    val core = core ?: return false
    val kind = render["kind"] as? String ?: return false
    if (!PorticoMpvSupport.RENDERED_SUBTITLE_KINDS.contains(kind)) return false
    val embedded = PorticoMpvSupport.map(render["embedded"])
    PorticoMpvFonts.select(context, (embedded?.get("language") as? String) ?: (subtitles["language"] as? String))
    if (embedded != null) {
      val track = PorticoMpvSupport.embeddedSubtitle(embedded, kind, PorticoMpvSupport.tracks(tracks, "sub"))
      if (track != null) {
        core.set("sid", track.toString())
        return true
      }
    }
    val text = render["url"] as? String ?: return false
    if (text.toByteArray(Charsets.UTF_8).size > 8192) return false
    val media = PorticoMpvSupport.parse(source) ?: return false
    val url = PorticoMpvFontFetcher.resolve(text, media) ?: return false
    val companion = if (kind == "vobsub") {
      checkedVobSubCompanion(render, subtitles) ?: return false
    } else null
    val local = url.scheme?.lowercase() == "file"
    val target: String = if (local) {
      if (media.scheme?.lowercase() != "file" || !PorticoMpvSupport.allowedLocalSource(context, url)) return false
      val path = url.path ?: return false
      if (companion != null && !PorticoMpvSupport.allowedLocalSource(context, companion)) return false
      path
    } else {
      if (!PorticoMpvSupport.subtitleFileBelongs(url, media)) return false
      relay.localUrl(url) ?: return false
    }
    externalSubtitleAddress = target
    subtitleTokenCount++
    val token = 1_000_000_000L + subtitleTokenCount
    subtitleToken = token
    subtitleTokenKey = key
    val add = {
      if (subtitleToken == token && externalSubtitleAddress == target) {
        // The supplied, checked VobSub pair shares a basename, so mpv's companion
        // opening names exactly that offered file through the relay or download. That one
        // opening is allowed for as long as the adding takes.
        if (kind == "vobsub" && !referencesAllowed) {
          referencesToken = token
          this.core?.set("access-references", "yes")
        }
        this.core?.command(listOf("sub-add", target, "select"), token)
      }
    }
    // A styled file may name fonts that came attached to it: they are fetched first, so the
    // first line is already drawn in the right face.
    // A download's script has them as files beside it: they are gathered into a folder of their own.
    val fonts = (render["fonts"] as? List<*>)?.mapNotNull { PorticoMpvSupport.map(it) } ?: emptyList()
    if (kind == "ass" && fonts.isNotEmpty()) {
      val ready = { folder: java.io.File? ->
        // A fetch that finishes late must not change the fonts of a newer choice.
        if (subtitleToken == token && externalSubtitleAddress == target) {
          if (folder != null) {
            // mpv reads one fonts folder: the attached fonts' gets the fallback faces too.
            PorticoMpvFonts.useAttachedFolder(folder)
            this.core?.set("sub-fonts-dir", folder.path)
          }
          add()
        }
      }
      if (local) fontFetcher.gather(fonts, ready) else fontFetcher.fetch(fonts, media, ready)
    } else {
      add()
    }
    return true
  }

  /** The viewer changed the caption size in the system's Accessibility settings. */
  private fun captionStyleChanged() {
    if (invalidated) return
    applyCaptionStyle(captionStyle)
    overlayListeners.toList().forEach { it() }
  }

  private fun setVideoAspect(value: Double?) {
    if (videoAspect == value) return
    videoAspect = value
    overlayListeners.toList().forEach { it() }
  }

  // --- picture: the surface, dynamic range and the display -------------------------------

  /** The source's dynamic range, by the capability document's names. */
  private fun sourceRange(): String {
    if (videoParams["colormatrix"] == "dolbyvision") return "dolby_vision"
    return when (videoParams["gamma"]) {
      "pq" -> "hdr10"
      "hlg" -> "hlg"
      else -> "sdr"
    }
  }

  /**
   * Whether this title is shown in HDR: the decoder then draws straight onto the surface and
   * the system sends the screen its HDR signal, exactly as it does for the Media3 engine. It
   * needs an HDR title, a screen that shows that kind of HDR, a hardware decoder for the codec,
   * and no subtitles that mpv would have to draw (nothing of mpv's own is drawn in this mode;
   * the app's plain-text subtitle line still is). Otherwise mpv draws the picture, and an HDR
   * title is tone-mapped to standard range on the device.
   */
  private fun wantsDirectOutput(): Boolean {
    if (!HDR_DIRECT_OUTPUT || directRefused || !fileRequested || !restarted || engineSubtitleKey != null) return false
    // Dolby Vision profile 5 has no HDR10 layer for the decoder to show: mpv draws it (gpu-next).
    if (dolbyVisionProfile() == 5) return false
    val range = sourceRange()
    if (range == "sdr") return false
    val shown = PorticoMpvDevice.displayHdr(context)
    if (!shown.contains(if (range == "hlg") "hlg" else "hdr10")) return false
    val codec = selectedVideo()?.get("codec") as? String ?: return false
    return PorticoMpvDevice.hardwareDecoders[codec]?.tenBit == true
  }

  /** The Dolby Vision profile of the selected picture track, when it has one (mpv's track list). */
  private fun dolbyVisionProfile(): Int? = (selectedVideo()?.get("dolby-vision-profile") as? Number)?.toInt()

  /**
   * mpv's own output when it draws the picture. Dolby Vision profile 5 carries its colours only
   * in the Dolby Vision layer, which mpv's gpu-next renderer (libplacebo with libdovi) reshapes
   * and tone-maps; every other title keeps the gpu renderer.
   */
  private fun drawOutput(): String = if (fileRequested && dolbyVisionProfile() == 5) "gpu-next" else "gpu"

  private fun updateOutput() {
    val wanted = wantsDirectOutput()
    val drawn = drawOutput()
    if (wanted == directOutput && drawn == drawnOutput) return
    directOutput = wanted
    drawnOutput = drawn
    outputChangedAt = SystemClock.elapsedRealtime()
    // With a title open the change is made at once, in step with the surface. With none (the
    // title was just closed) it waits its turn, and costs nothing.
    if (fileRequested) core?.configureOutput(if (wanted) "mediacodec_embed" else drawn, if (wanted) "mediacodec" else gpuDecoding())
    else { drawnOutput = "gpu"; core?.configureOutputLater("gpu", gpuDecoding()) }
    overlayListeners.toList().forEach { it() }
    publishMediaInfo()
  }

  /**
   * mpv dropped the picture track after playback had begun. While the decoder was drawing
   * straight onto the surface, that way of drawing does not work for this file: mpv draws it
   * instead. Just after any other change of output or surface, the track is chosen again, once.
   */
  private fun pictureLost() {
    if (snapshot.vid != "no" || !restarted || !PorticoMpvSupport.hasVideo(tracks)) return
    if (directOutput) {
      directRefused = true
      updateOutput()
      core?.set("vid", "auto")
    } else if (!pictureRetried && SystemClock.elapsedRealtime() - outputChangedAt < 5000) {
      pictureRetried = true
      core?.set("vid", "auto")
    }
  }

  /**
   * The size to draw the picture at inside a view area of [width] by [height] pixels, when that
   * is more than the area itself: a television's interface is often 1080p on a 4K panel, and a
   * 4K title is then drawn at the panel's size. Null when the area's own size is right.
   */
  fun renderSize(width: Int, height: Int): Pair<Int, Int>? {
    if (!PorticoMpvDevice.isTelevision || directOutput || width <= 0 || height <= 0) return null
    // Drawing 4K through the graphics chip is beyond the smallest boxes.
    if (PorticoMpvDevice.memoryBytes(context) <= 2_684_354_560L) return null
    val videoWidth = PorticoMpvSupport.number(videoParams["dw"]) ?: return null
    val panel = PorticoMpvDevice.screen(context)
    var targetWidth = Math.min(videoWidth, panel.first.toDouble())
    var targetHeight = targetWidth * height / width
    if (targetHeight > panel.second) {
      targetHeight = panel.second.toDouble()
      targetWidth = targetHeight * width / height
    }
    if (targetWidth < width * 1.05) return null
    return Pair((targetWidth.toInt() / 2) * 2, (targetHeight.toInt() / 2) * 2)
  }

  /** The view that shows the leased picture lends its surface. */
  fun surfaceAvailable(owner: Any, next: Surface, width: Int, height: Int) {
    if (surface === next && surfaceOwner === owner) {
      surfaceChanged(owner, width, height)
      return
    }
    surfaceOwner = owner
    surface = next
    surfaceWidth = width
    surfaceHeight = height
    frameRateVoted = false
    outputChangedAt = SystemClock.elapsedRealtime()
    core?.attachSurface(next, width, height)
    // A surface that arrives after the first frame still gets the display matched. One that
    // arrives while the display is being waited for (a television may replace the surface as
    // it switches) is told the rate again, so the display does not switch back.
    val waiting = pendingDisplayReady
    if (waiting != null) {
      pendingDisplayReady = null
      displayHold = false
      matchDisplay(pendingDisplayDeadline, waiting)
    } else if (restarted && fileRequested) {
      if (!displayHold) matchDisplay { applyIntent() }
      else frameRate()?.let { fps -> if (next.isValid) voteFrameRate(next, fps) }
    }
  }

  fun surfaceChanged(owner: Any, width: Int, height: Int) {
    if (surfaceOwner !== owner || (width == surfaceWidth && height == surfaceHeight)) return
    surfaceWidth = width
    surfaceHeight = height
    core?.surfaceSize(width, height)
  }

  /** The surface is going away. When this returns, mpv no longer draws on it. */
  fun surfaceGone(owner: Any) {
    if (surfaceOwner !== owner) return
    core?.detachSurface()
    surface = null
    surfaceOwner = null
    frameRateVoted = false
  }

  /** Tells the system the title's frame rate for this surface. False when the surface would not take it. */
  private fun clearDisabledFrameRate() {
    if (command?.get("matchFrameRate") != "off") return
    val target = votedFrameSurface
    frameRateClear?.let { main.removeCallbacks(it) }
    frameRateClear = null
    if (target == null || !target.isValid || Build.VERSION.SDK_INT < 30) {votedFrameSurface = null; frameRateVoted = false; return}
    try {
      target.setFrameRate(0f, Surface.FRAME_RATE_COMPATIBILITY_DEFAULT)
      frameRateVoted = false
      votedFrameSurface = null
    } catch (_: Exception) {}
  }

  private fun voteFrameRate(target: Surface, fps: Float): Boolean {
    clearDisabledFrameRate()
    if (Build.VERSION.SDK_INT < 30 || command?.get("matchFrameRate") == "off") return false
    return try {
      val previous = votedFrameSurface
      if (previous != null && previous !== target && previous.isValid) previous.setFrameRate(0f, Surface.FRAME_RATE_COMPATIBILITY_DEFAULT)
      if (Build.VERSION.SDK_INT >= 31) target.setFrameRate(fps, Surface.FRAME_RATE_COMPATIBILITY_FIXED_SOURCE, Surface.CHANGE_FRAME_RATE_ALWAYS)
      else target.setFrameRate(fps, Surface.FRAME_RATE_COMPATIBILITY_FIXED_SOURCE)
      frameRateVoted = true
      votedFrameSurface = target
      true
    } catch (_: Exception) {
      false
    }
  }

  private fun frameRate(): Float? {
    val fps = PorticoMpvSupport.number(selectedVideo()?.get("demux-fps")) ?: return null
    return if (fps >= 1 && fps <= 240) fps.toFloat() else null
  }

  /**
   * Asks the display to match the title's frame rate before playback starts, and on a
   * television waits for the switch to finish, so the first seconds are not lost to a blank
   * screen. The request is the system's own (`Surface.setFrameRate`, Android 11 and later), so
   * the viewer's "match content frame rate" setting decides whether and how the display
   * switches. An HDR title drawn straight onto the surface also switches the screen to HDR
   * here, and the same wait covers it.
   */
  private fun matchDisplay(deadline: Long = SystemClock.elapsedRealtime() + 10_000, done: () -> Unit) {
    val target = surface
    clearDisabledFrameRate()
    val fps = if (command?.get("matchFrameRate") == "off") null else frameRate()
    if (((Build.VERSION.SDK_INT >= 30 && fps != null) || directOutput) && PorticoMpvDevice.isTelevision && (target == null || !target.isValid) && !backgroundHold) {
      // Even a very fast load waits for its view before the television is asked to switch, so
      // the title never starts and then loses its opening seconds to the switch.
      displayHold = true
      core?.set("pause", "yes")
      pendingDisplayReady = done
      pendingDisplayDeadline = deadline
      val epoch = loadEpoch
      main.postDelayed({
        val waiting = pendingDisplayReady
        if (loadEpoch == epoch && waiting != null) {
          pendingDisplayReady = null
          displayHold = false
          waiting()
        }
      }, (deadline - SystemClock.elapsedRealtime()).coerceAtLeast(0))
      return
    }
    if ((Build.VERSION.SDK_INT < 30 && !directOutput) || target == null || !target.isValid || (fps == null && !directOutput)) { done(); return }
    val displays = context.getSystemService(Context.DISPLAY_SERVICE) as? DisplayManager
    // The viewer's setting, where the system has one (Android 12): never, only when the picture
    // does not break, or always.
    val preference = if (Build.VERSION.SDK_INT >= 31) displays?.matchContentFrameRateUserPreference else null
    val never = command?.get("matchFrameRate") == "off" || preference == DisplayManager.MATCH_CONTENT_FRAMERATE_NEVER
    if (never && !directOutput) { done(); return }
    val display = displays?.getDisplay(Display.DEFAULT_DISPLAY)
    val before = display?.mode
    val voted = Build.VERSION.SDK_INT >= 30 && !never && fps != null && voteFrameRate(target, fps)
    if (Build.VERSION.SDK_INT >= 30 && !never && fps != null && !voted) { done(); return }
    // A phone or tablet changes rate without a break in the picture, and so does a television
    // set to switch only when there is none: nothing to wait for. A television that may
    // renegotiate its connection (or is about to switch to HDR) is waited for.
    val mayBreak = voted && preference != DisplayManager.MATCH_CONTENT_FRAMERATE_SEAMLESSS_ONLY
    if (!PorticoMpvDevice.isTelevision || displays == null || display == null || !(mayBreak || directOutput)) { done(); return }
    val epoch = loadEpoch
    val wait = ++displayWait
    displayHold = true
    // Nothing plays while the television may be switching.
    core?.set("pause", "yes")
    var settled = false
    var listener: DisplayManager.DisplayListener? = null
    val settle = {
      if (!settled) {
        settled = true
        listener?.let { displays.unregisterDisplayListener(it) }
        if (loadEpoch == epoch && displayWait == wait && displayHold) {
          displayHold = false
          done()
        }
      }
    }
    var switching = false
    listener = object : DisplayManager.DisplayListener {
      override fun onDisplayAdded(displayId: Int) {}
      override fun onDisplayRemoved(displayId: Int) {}
      override fun onDisplayChanged(displayId: Int) {
        if (displayId != Display.DEFAULT_DISPLAY || switching) return
        val now = display.mode
        if (before != null && now.modeId == before.modeId && Math.abs(now.refreshRate - before.refreshRate) < 0.01f) return
        switching = true
        // A switch the television makes without renegotiating its connection shows no break;
        // any other needs a moment before its picture is steady.
        val seamless = Build.VERSION.SDK_INT >= 31 && before != null && before.alternativeRefreshRates.any { Math.abs(it - now.refreshRate) < 0.01f }
        main.postDelayed({ settle() }, if (seamless) 100 else 2000)
      }
    }
    displays.registerDisplayListener(listener, main)
    // No switch begins when the television is already in the right mode.
    main.postDelayed({ if (!switching) settle() }, 1000)
    main.postDelayed({ settle() }, (deadline - SystemClock.elapsedRealtime()).coerceAtLeast(0))
  }

  /** The display goes back to its own rate a moment after playback is left, not between two episodes. */
  private fun scheduleFrameRateClear() {
    frameRateClear?.let { main.removeCallbacks(it) }
    val target = votedFrameSurface
    val work = Runnable {
      frameRateClear = null
      if (lease != null || target !== votedFrameSurface || target == null || !target.isValid || Build.VERSION.SDK_INT < 30) return@Runnable
      frameRateVoted = false
      votedFrameSurface = null
      try { target.setFrameRate(0f, Surface.FRAME_RATE_COMPATIBILITY_DEFAULT) } catch (_: Exception) {}
    }
    frameRateClear = work
    main.postDelayed(work, 2000)
  }

  // --- sound -----------------------------------------------------------------------------

  private fun mediaCommand(name: String) {
    if (mediaOpen) PorticoSystemMedia.current?.engineCommand(name, "interruption")
  }

  private fun requestAudioFocus() {
    focusRelease?.let { main.removeCallbacks(it) }
    focusRelease = null
    val granted = try {
      if (Build.VERSION.SDK_INT >= 26) {
        val request = focusRequest ?: AudioFocusRequest.Builder(AudioManager.AUDIOFOCUS_GAIN)
            .setAudioAttributes(android.media.AudioAttributes.Builder().setUsage(android.media.AudioAttributes.USAGE_MEDIA).setContentType(android.media.AudioAttributes.CONTENT_TYPE_MOVIE).build())
            .setOnAudioFocusChangeListener(focusListener, main)
            .build().also { focusRequest = it }
        audioManager.requestAudioFocus(request)
      } else {
        @Suppress("DEPRECATION") audioManager.requestAudioFocus(focusListener, AudioManager.STREAM_MUSIC, AudioManager.AUDIOFOCUS_GAIN)
      }
    } catch (_: Exception) {
      AudioManager.AUDIOFOCUS_REQUEST_FAILED
    } == AudioManager.AUDIOFOCUS_REQUEST_GRANTED
    focusHeld = granted
    val wasHeld = focusHold
    focusHold = !granted
    // A call is in progress, say: the video waits, and JavaScript is told it is paused.
    if (!granted && !wasHeld) mediaCommand("pause")
  }

  /** Lets another app's audio (music the film interrupted) know it may resume. */
  private fun releaseAudioFocus() {
    focusHold = false
    focusLost = false
    if (!focusHeld) return
    focusRelease?.let { main.removeCallbacks(it) }
    val work = Runnable {
      focusRelease = null
      if (lease != null || !focusHeld) return@Runnable
      focusHeld = false
      try {
        if (Build.VERSION.SDK_INT >= 26) focusRequest?.let { audioManager.abandonAudioFocusRequest(it) }
        else @Suppress("DEPRECATION") audioManager.abandonAudioFocus(focusListener)
      } catch (_: Exception) {}
    }
    focusRelease = work
    main.postDelayed(work, 500)
  }

  private fun focusChanged(change: Int) {
    if (invalidated) return
    when (change) {
      AudioManager.AUDIOFOCUS_GAIN -> {
        val returned = focusHold
        focusHeld = true
        focusHold = false
        focusLost = false
        core?.set("volume", "100")
        applyIntent()
        // The interruption is over: JavaScript resumes what it had paused for it.
        if (returned) mediaCommand("play")
      }
      AudioManager.AUDIOFOCUS_LOSS, AudioManager.AUDIOFOCUS_LOSS_TRANSIENT -> {
        // A lasting loss must be asked for again; a passing one comes back by itself.
        if (change == AudioManager.AUDIOFOCUS_LOSS) { focusHeld = false; focusLost = true }
        focusHold = true
        applyIntent()
        mediaCommand("pause")
      }
      // Before Android 8 the app lowers its own sound under a notification; later the system does.
      AudioManager.AUDIOFOCUS_LOSS_TRANSIENT_CAN_DUCK -> core?.set("volume", "35")
      else -> {}
    }
  }

  /** Another output (a receiver, headphones) may take a different number of channels. */
  private fun audioOutputsChanged() {
    val next = PorticoMpvDevice.outputChannels(context)
    if (next == outputChannels) return
    outputChannels = next
    core?.set("audio-channels", PorticoMpvDevice.channelLayouts(next))
  }

  // --- app life --------------------------------------------------------------------------

  /**
   * The app left the screen. The surface goes with it, so playback is held; JavaScript is told,
   * so its intent becomes "paused" and the Play button says so when the viewer comes back.
   */
  private fun enteredBackground() {
    backgroundHold = true
    val core = core
    if (lease == null || !fileRequested || core == null) return
    val wasPlaying = !desiredPaused
    core.set("pause", "yes")
    if (wasPlaying) {
      val fact = baseFact("systemPaused", currentPosition(), null)
      fact["paused"] = true
      fact["reason"] = "background"
      publish(fact)
    }
    sessionPlayer.changed()
  }

  private fun enteredForeground() {
    if (!backgroundHold) return
    backgroundHold = false
    if (lease == null || !fileRequested || core == null) return
    applyIntent()
  }

  // --- what the system's media session reads ---------------------------------------------

  fun clockPosition(): Double = currentPosition().let { if (it.isFinite()) it else 0.0 }
  fun clockRate(): Double = effectiveRate()
  fun clockSpeed(): Double = if (snapshot.speed > 0) snapshot.speed else 1.0
  fun clockDuration(): Double = if (snapshot.duration.isFinite()) snapshot.duration else 0.0
  fun clockReady(): Boolean = ready()
  fun clockEnded(): Boolean = endedEmitted

  companion object {
    /** The app's one mpv engine, for its view and the system media session. */
    @Volatile var shared: PorticoMpvController? = null

    /**
     * An HDR title on an HDR screen is drawn by the decoder straight onto the surface, so the
     * screen shows it in HDR (see `wantsDirectOutput`). Off: every title is drawn by mpv, and
     * HDR is always tone-mapped on the device.
     */
    const val HDR_DIRECT_OUTPUT = true

    /** A local `audioTrack` choice must produce its exact readback within this window. */
    const val AUDIO_TRACK_TIMEOUT_MS = 15_000L

    /**
     * Whether this engine plays live channels. The code is complete but has not yet been played
     * on a device: until it has, channels stay with the Media3 engine (the capability document
     * and the engine's features both read this).
     */
    const val LIVE_CHANNELS = false

    /**
     * Options whose refusal does not stop mpv starting: how the picture is scaled and how text
     * subtitles look, switches for parts this build may not have, and options that have had
     * two names. Every other option is needed for the engine to behave as the contract says,
     * and a refusal is reported with the option's name.
     */
    private val OPTIONAL_OPTIONS = setOf(
        "load-scripts", "osc", "ytdl", "input-default-bindings", "input-vo-keyboard", "resume-playback", "save-position-on-quit",
        "profile", "scale", "dscale", "cscale", "dither-depth", "deband", "correct-downscaling", "linear-downscaling", "sigmoid-upscaling",
        "hdr-compute-peak", "tone-mapping", "sub-border-style", "sub-outline-size", "sub-border-size", "sub-shadow-offset", "sub-font", "sub-fonts-dir",
        "sub-font-size", "sub-margin-y", "sub-color", "sub-back-color", "sub-scale", "sub-use-margins", "sub-ass-force-margins", "user-agent",
        "demuxer-max-bytes", "demuxer-max-back-bytes", "network-timeout", "hwdec-codecs", "audio-channels")
  }
}
