package tv.getportico.portico.native

import android.content.ComponentCallbacks2
import android.content.res.Configuration
import android.os.Handler
import android.os.Looper
import androidx.media3.common.C
import androidx.media3.common.MediaItem
import androidx.media3.common.PlaybackParameters
import androidx.media3.common.Player
import androidx.media3.common.SimpleBasePlayer
import com.facebook.react.bridge.Arguments
import com.facebook.react.bridge.Promise
import com.facebook.react.bridge.ReactApplicationContext
import com.facebook.react.bridge.ReactMethod
import com.facebook.react.bridge.ReadableMap
import com.facebook.react.bridge.WritableMap
import com.google.common.util.concurrent.Futures
import com.google.common.util.concurrent.ListenableFuture

/**
 * `NativeModules.PorticoMpvPlaybackModule`: the mpv engine's module. The same three methods and
 * the same facts as the Media3 engine's `PorticoPlaybackModule` (see
 * `Native/ENGINE-CONTRACT.md`), under its own names so that JavaScript can hold either.
 *
 * - Methods: `acquire(owner)`, `command(value)`, `release(lease)`, `facts()`.
 * - Events: `PorticoMpvPlaybackFact`, `PorticoMpvPictureInPicture`.
 * - Constants: `engine`, `available`, `features`.
 */
class PorticoMpvPlaybackModule(context: ReactApplicationContext) : PorticoEventModule(context) {
  private val main = Handler(Looper.getMainLooper())
  @Volatile private var invalidated = false
  private var controller: PorticoMpvController? = null

  private val memory = object : ComponentCallbacks2 {
    override fun onConfigurationChanged(newConfig: Configuration) {}
    @Deprecated("Deprecated in Java") override fun onLowMemory() { main.post { controller?.trimMemory() } }
    override fun onTrimMemory(level: Int) { if (level >= ComponentCallbacks2.TRIM_MEMORY_RUNNING_LOW) main.post { controller?.trimMemory() } }
  }

  init { current = this }

  override fun getName(): String = "PorticoMpvPlaybackModule"

  private fun pictureInPictureChanged(body: WritableMap) {
    if (!invalidated) emit("PorticoMpvPictureInPicture", body)
  }

  @ReactMethod fun addListener(eventName: String) {}

  @ReactMethod fun removeListeners(count: Double) {}

  /**
   * What this engine is and what it does not do, so the player can hide what does not apply
   * while it is the active engine. These never change while the app runs.
   */
  override fun getConstants(): Map<String, Any> = mapOf(
      "engine" to "mpv",
      "available" to PorticoMpvCore.available,
      "features" to mapOf(
          // mpv draws onto a surface of its own: the app's Picture in Picture window (which
          // re-homes the Media3 picture) does not carry it, and there is no AirPlay on Android.
          "pictureInPicture" to false,
          "airPlayVideo" to false,
          // Channels are placed by the clock epochs the server publishes (PorticoMpvLinearClock).
          "liveChannels" to PorticoMpvController.LIVE_CHANNELS,
          // Music and audiobooks stay with the native listening owner.
          "musicSessions" to false,
          // A direct-played file's audio and subtitle tracks are switched in the engine.
          "audioTrackSwitching" to true,
          "subtitleRendering" to PorticoMpvSupport.RENDERED_SUBTITLE_KINDS,
          "chapters" to true))

  override fun initialize() {
    super.initialize()
    // The libraries are loaded here, off the main thread; the engine itself lives on it.
    val usable = PorticoMpvCore.available
    main.post {
      if (invalidated || !usable) return@post
      PorticoAppVisibility.install(reactApplicationContext, reactApplicationContext.currentActivity)
      val made = PorticoMpvController(reactApplicationContext.applicationContext)
      made.onFact = { fact -> if (!invalidated) emit("PorticoMpvPlaybackFact", fact) }
      controller = made
      PorticoMpvController.shared = made
      reactApplicationContext.applicationContext.registerComponentCallbacks(memory)
    }
  }

  override fun invalidate() {
    invalidated = true
    main.post {
      if (current === this) current = null
      val made = controller ?: return@post
      controller = null
      try { reactApplicationContext.applicationContext.unregisterComponentCallbacks(memory) } catch (_: Exception) {}
      made.invalidate()
      if (PorticoMpvController.shared === made) PorticoMpvController.shared = null
    }
    super.invalidate()
  }

  @ReactMethod
  fun acquire(owner: String?, promise: Promise) {
    main.post {
      if (invalidated) { promise.reject("bridge_closed", "Playback bridge is closed."); return@post }
      val engine = controller
      if (engine == null) { promise.reject("engine_unavailable", "This build does not include the mpv player."); return@post }
      val lease = engine.acquire(owner)
      if (lease != null) promise.resolve(Arguments.makeNativeMap(lease)) else promise.reject("owner_busy", "Another playback owner is still active.")
    }
  }

  @ReactMethod
  fun command(command: ReadableMap?, promise: Promise) {
    val value = command?.toHashMap()
    main.post {
      if (invalidated) { promise.reject("bridge_closed", "Playback bridge is closed."); return@post }
      val engine = controller
      if (engine == null) { promise.reject("engine_unavailable", "This build does not include the mpv player."); return@post }
      val error = engine.accept(value)
      if (error != null) promise.reject("command_rejected", error) else promise.resolve(true)
    }
  }

  @ReactMethod
  fun release(lease: String?, promise: Promise) {
    main.post {
      // Resolved once mpv has closed the title, so the next engine never starts over its sound.
      val engine = if (invalidated) null else controller
      if (engine == null) promise.resolve(true) else engine.release(lease) { promise.resolve(true) }
    }
  }

  /**
   * What this device gives the engine right now, for the mpv capability profile: which codecs
   * the chip decodes and to what size (everything else is decoded in software, within the limits
   * the profile sets by memory and processor count), whether the screen shows HDR from this
   * engine, and how many channels the audio output takes.
   */
  @ReactMethod
  fun facts(promise: Promise) {
    try {
      val context = reactApplicationContext
      val decoders = PorticoMpvDevice.hardwareDecoders
      val shown = PorticoMpvDevice.displayHdr(context)
      val screen = PorticoMpvDevice.screen(context)
      val limits = Arguments.createMap()
      for ((codec, decoder) in decoders) limits.putMap(codec, Arguments.createMap().apply {
        putInt("maxWidth", decoder.maxWidth)
        putInt("maxHeight", decoder.maxHeight)
        putInt("maxFrameRate", decoder.maxFrameRate)
        putBoolean("tenBit", decoder.tenBit)
        if (decoder.points.isNotEmpty()) putArray("limits", videoPointsArray(decoder.points))
      })
      // HDR reaches the screen when the decoder draws straight onto the surface, which takes a
      // hardware decoder for a 10-bit codec and a screen that shows HDR10.
      val hdr = PorticoMpvController.HDR_DIRECT_OUTPUT && shown.contains("hdr10") && decoders.values.any { it.tenBit }
      promise.resolve(Arguments.createMap().apply {
        putString("engine", "mpv")
        putBoolean("available", PorticoMpvCore.available)
        putDouble("memoryBytes", PorticoMpvDevice.memoryBytes(context).toDouble())
        putInt("processorCount", Runtime.getRuntime().availableProcessors())
        putMap("hardwareDecode", Arguments.createMap().apply {
          for (codec in listOf("h264", "hevc", "vp9", "av1")) putBoolean(codec, decoders.containsKey(codec))
        })
        putBoolean("hdrOutput", hdr)
        putMap("screen", Arguments.createMap().apply {
          putInt("width", screen.first)
          putInt("height", screen.second)
          if (screen.third > 0) putInt("maxFrameRate", screen.third)
        })
        putMap("audio", Arguments.createMap().apply { putInt("maxOutputChannels", PorticoMpvDevice.outputChannels(context)) })
        // Beyond the shared shape: what differs on Android.
        putMap("hardwareLimits", limits)
        putArray("hdrOutputRanges", Arguments.fromList(if (hdr) listOf("hdr10", "hlg").filter { shown.contains(it) } else emptyList<String>()))
        // The HEVC decoder shows a Dolby Vision title's HDR10 layer (profiles 7 and 8); a title
        // with no such layer (profile 5) is drawn by mpv's gpu-next renderer, which reshapes it.
        putArray("dolbyVisionProfiles", Arguments.fromList(listOf(5, 7, 8)))
        // mpv deinterlaces interlaced frames itself (deinterlace=auto, FFmpeg's bwdif).
        putBoolean("softwareDeinterlace", true)
        putBoolean("liveChannels", PorticoMpvController.LIVE_CHANNELS)
      })
    } catch (e: Exception) {
      promise.reject("capabilities", "This device's mpv capabilities could not be read.", e)
    }
  }

  companion object {
    @Volatile private var current: PorticoMpvPlaybackModule? = null

    /** A Picture in Picture answer from the engine's view ({lease, state}). */
    internal fun pictureInPicture(body: WritableMap) {
      current?.pictureInPictureChanged(body)
    }
  }
}

/**
 * The mpv engine as the system's media session sees it (`PorticoSystemMedia` wraps it exactly as
 * it wraps the Media3 player): what it shows is real, and nothing it is asked to do happens here.
 * Every press reaches JavaScript through the session's own wrapper.
 */
internal class PorticoMpvSessionPlayer(private val controller: PorticoMpvController) : SimpleBasePlayer(Looper.getMainLooper()) {
  private val item = MediaItem.Builder().setMediaId("portico-mpv").build()

  /** The engine's position, speed, length or state changed. Main thread. */
  fun changed() {
    // The session is a mirror: nothing it cannot express may ever disturb playback.
    try { invalidateState() } catch (_: RuntimeException) {}
  }

  override fun getState(): State {
    val open = controller.mediaOpen
    val ready = open && controller.clockReady()
    val rate = controller.clockRate()
    val position = (controller.clockPosition() * 1000).toLong().coerceAtLeast(0)
    val duration = controller.clockDuration()
    val builder = State.Builder()
        .setAvailableCommands(COMMANDS)
        .setPlayWhenReady(open && !controller.paused, Player.PLAY_WHEN_READY_CHANGE_REASON_USER_REQUEST)
        .setPlaybackParameters(PlaybackParameters(controller.clockSpeed().toFloat().coerceIn(0.1f, 8f)))
    if (!open) return builder.setPlaybackState(Player.STATE_IDLE).build()
    val data = MediaItemData.Builder("portico-mpv")
        .setMediaItem(item)
        .setIsSeekable(true)
        .setDurationUs(if (duration > 0) (duration * 1_000_000).toLong() else C.TIME_UNSET)
        .build()
    return builder
        .setPlaylist(listOf(data))
        .setPlaybackState(if (controller.clockEnded()) Player.STATE_ENDED else if (ready) Player.STATE_READY else Player.STATE_BUFFERING)
        .setContentPositionMs(if (rate > 0) PositionSupplier.getExtrapolating(position, rate.toFloat()) else PositionSupplier.getConstant(position))
        .build()
  }

  // The session's wrapper turns every request into a command for JavaScript before it gets here.
  override fun handleSetPlayWhenReady(playWhenReady: Boolean): ListenableFuture<*> = Futures.immediateVoidFuture()

  override fun handleSeek(mediaItemIndex: Int, positionMs: Long, seekCommand: Int): ListenableFuture<*> = Futures.immediateVoidFuture()

  override fun handleStop(): ListenableFuture<*> = Futures.immediateVoidFuture()

  override fun handlePrepare(): ListenableFuture<*> = Futures.immediateVoidFuture()

  override fun handleSetPlaybackParameters(playbackParameters: PlaybackParameters): ListenableFuture<*> = Futures.immediateVoidFuture()

  companion object {
    private val COMMANDS: Player.Commands = Player.Commands.Builder().addAll(
        Player.COMMAND_PLAY_PAUSE, Player.COMMAND_STOP, Player.COMMAND_SEEK_IN_CURRENT_MEDIA_ITEM, Player.COMMAND_SEEK_BACK, Player.COMMAND_SEEK_FORWARD,
        Player.COMMAND_GET_CURRENT_MEDIA_ITEM, Player.COMMAND_GET_TIMELINE, Player.COMMAND_GET_METADATA, Player.COMMAND_SET_SPEED_AND_PITCH).build()
  }
}
