package tv.getportico.portico.native

import android.os.Handler
import android.os.Looper
import com.facebook.react.bridge.Arguments
import com.facebook.react.bridge.Promise
import com.facebook.react.bridge.ReactApplicationContext
import com.facebook.react.bridge.ReactMethod
import com.facebook.react.bridge.ReadableMap
import tv.getportico.portico.TelevisionDisplay

/**
 * `NativeModules.PorticoPlaybackModule`: the lease, the commands and the facts of the app's one
 * player (events `PorticoPlaybackFact`, `PorticoListeningSnapshot`, `PorticoPictureInPicture`).
 * Everything runs on the main thread, where the player lives.
 */
class PorticoPlaybackModule(context: ReactApplicationContext) : PorticoEventModule(context) {
  private val main = Handler(Looper.getMainLooper())
  @Volatile private var invalidated = false
  private val controllerStore = lazy {
    PorticoPlaybackController(reactApplicationContext.applicationContext).also { made ->
      made.onFact = { fact -> if (!invalidated) emit("PorticoPlaybackFact", fact) }
      made.onPictureInPicture = { change -> if (!invalidated) emit("PorticoPictureInPicture", change) }
      made.onListeningFact = { fact -> if (!invalidated) listening.fact(fact) }
      PorticoPlaybackController.shared = made
    }
  }
  private val controller: PorticoPlaybackController by controllerStore
  /** The native listening owner (Playback v1): music and audiobooks. */
  private val listeningStore = lazy {
    PorticoListeningOwner(reactApplicationContext.applicationContext, controller).also { made ->
      made.onUserMutation = { status -> if (!invalidated) emit("PorticoUserMutationStatus", Arguments.makeNativeMap(status)) }
      made.onSnapshot = { snapshot -> if (!invalidated) emit("PorticoListeningSnapshot", Arguments.makeNativeMap(snapshot)) }
      PorticoListeningOwner.shared = made
      PorticoListeningSession.attach(reactApplicationContext.applicationContext, made)
    }
  }
  private val listening: PorticoListeningOwner by listeningStore

  override fun getName(): String = "PorticoPlaybackModule"

  /**
   * What this engine is and does (`Native/ENGINE-CONTRACT.md`, section 2), so the player offers
   * only what applies while it is the active engine. The app draws the server's plain-text cues
   * itself and nothing else: Media3's own subtitle drawing is switched off.
   */
  override fun getConstants(): Map<String, Any> = mapOf(
      "engine" to "native",
      "available" to true,
      "features" to mapOf(
          "pictureInPicture" to !TelevisionDisplay.isTelevision,
          "airPlayVideo" to false,
          "liveChannels" to true,
          "musicSessions" to true,
          "audioTrackSwitching" to false,
          "subtitleRendering" to listOf("text"),
          "chapters" to false))

  /** Music's Match sample rate, this device's own value (smart/strict/off). The listening
   * owner reads it when it opens a track's audio session; nothing reports a match until the
   * opened session's rate equals the file's. */
  @ReactMethod
  fun setAudioSampleRateMatching(mode: String?, promise: Promise) {
    main.post {
      if (invalidated) { promise.reject("bridge_closed", "Playback bridge is closed."); return@post }
      listening.setSampleRateMatching(PorticoAudioSampleRate.parse(mode))
      promise.resolve(Arguments.makeNativeMap(listening.audioProcessingFacts(reactApplicationContext)))
    }
  }

  @ReactMethod
  fun audioProcessing(promise: Promise) {
    main.post {
      if (invalidated) { promise.reject("bridge_closed", "Playback bridge is closed."); return@post }
      promise.resolve(Arguments.makeNativeMap(listening.audioProcessingFacts(reactApplicationContext)))
    }
  }

  /** The engine's current mode, for a settings row that must not guess. */
  fun sampleRateMatching(): String = listening.sampleRateMatching()

  @ReactMethod fun addListener(eventName: String) {}

  @ReactMethod fun removeListeners(count: Double) {}

  override fun initialize() {
    super.initialize()
    // The player view asks for the shared controller as soon as it mounts.
    main.post {
      if (invalidated) return@post
      PorticoAppVisibility.install(reactApplicationContext, reactApplicationContext.currentActivity)
      listening
    }
  }

  override fun invalidate() {
    invalidated = true
    main.post {
      if (listeningStore.isInitialized()) listeningStore.value.let { owner ->
        owner.invalidate()
        PorticoListeningSession.detach(reactApplicationContext.applicationContext, owner)
        if (PorticoListeningOwner.shared === owner) PorticoListeningOwner.shared = null
      }
      if (controllerStore.isInitialized()) controllerStore.value.let { current ->
        current.invalidate()
        if (PorticoPlaybackController.shared === current) PorticoPlaybackController.shared = null
      }
    }
    super.invalidate()
  }

  @ReactMethod
  fun acquire(owner: String?, promise: Promise) {
    main.post {
      if (invalidated) { promise.reject("bridge_closed", "Playback bridge is closed."); return@post }
      val lease = controller.acquire(owner)
      if (lease != null) promise.resolve(Arguments.makeNativeMap(lease)) else promise.reject("owner_busy", "Another playback owner is still active.")
    }
  }

  @ReactMethod
  fun command(command: ReadableMap?, promise: Promise) {
    val value = command?.toHashMap()
    main.post {
      if (invalidated) { promise.reject("bridge_closed", "Playback bridge is closed."); return@post }
      val error = controller.accept(value)
      if (error != null) promise.reject("command_rejected", error) else promise.resolve(true)
    }
  }

  @ReactMethod
  fun release(lease: String?, promise: Promise) {
    main.post {
      if (!invalidated) controller.release(lease)
      promise.resolve(true)
    }
  }

  // --- the listening owner ----------------------------------------------------------------

  /** Whether [owner] is the listening owner's current handover. */
  private fun v1(owner: String?): Boolean = owner != null && listening.activeOwner == owner

  @ReactMethod
  fun adoptListeningSession(context: ReadableMap?, promise: Promise) {
    val value = context?.toHashMap()
    main.post {
      val error = if (invalidated) "Listening owner is closed." else listening.adopt(value)
      if (error == null) promise.resolve(Arguments.makeNativeMap(listening.snapshot(value?.get("owner") as? String))) else promise.reject("listening_authority", error)
    }
  }

  @ReactMethod
  fun listeningSessionUpdated(owner: String?, sessionId: String?, promise: Promise) {
    main.post {
      if (!invalidated && v1(owner) && sessionId != null && listening.sessionUpdated(sessionId, owner!!)) promise.resolve(true)
      else promise.reject("listening_stale", "This listening owner is no longer active.")
    }
  }

  /** Another viewer (sign-out, a profile or server switch) never reads this viewer's cached audio. The player keeps no cache on disk. */
  @ReactMethod
  fun purgeAudioCache(promise: Promise) = promise.resolve(true)

  @ReactMethod
  fun listeningAdvance(owner: String?, reason: String?, entryId: String?, promise: Promise) {
    main.post {
      if (!invalidated && v1(owner) && listening.advance(reason, entryId, owner!!)) promise.resolve(true)
      else promise.reject("listening_stale", "This listening owner is no longer active.")
    }
  }

  @ReactMethod
  fun listeningRoute(owner: String?, route: ReadableMap?, promise: Promise) {
    val value = route?.toHashMap()
    main.post {
      if (!invalidated && v1(owner) && listening.route(value, owner!!)) promise.resolve(true)
      else promise.reject("listening_route", "The verified listening route could not be applied.")
    }
  }

  @ReactMethod
  fun listeningAction(owner: String?, action: String?, value: Double?, promise: Promise) {
    main.post {
      if (!invalidated && v1(owner) && listening.action(action, value, owner!!)) promise.resolve(true)
      else promise.reject("listening_stale", "This listening owner is no longer active.")
    }
  }

  @ReactMethod
  fun listeningActionFor(owner: String?, expected: ReadableMap?, action: String?, value: Double?, promise: Promise) {
    val target = expected?.toHashMap()
    main.post {
      if (!invalidated && v1(owner) && listening.actionFor(target, action, value, owner!!)) promise.resolve(true)
      else promise.reject("listening_stale", "This listening output is no longer active.")
    }
  }

  @ReactMethod
  fun listeningSnapshot(owner: String?, promise: Promise) {
    main.post { promise.resolve(Arguments.makeNativeMap(if (invalidated) emptyMap() else listening.snapshot(owner))) }
  }

  @ReactMethod
  fun acknowledgeListening(owner: String?, sequence: Double, promise: Promise) {
    main.post {
      if (!invalidated && v1(owner) && listening.acknowledge(owner!!, sequence)) promise.resolve(true)
      else promise.reject("listening_stale", "Listening handover expired.")
    }
  }

  @ReactMethod
  fun listeningPolicy(owner: String?, policy: ReadableMap?, promise: Promise) {
    val value = policy?.toHashMap()
    main.post {
      if (!invalidated && v1(owner) && listening.policy(value, owner!!)) promise.resolve(true)
      else promise.reject("listening_policy", "Listening policy could not be validated or saved.")
    }
  }

  @ReactMethod
  fun listeningDeadlines(owner: String?, deadlines: ReadableMap?, promise: Promise) {
    val value = deadlines?.toHashMap()
    main.post {
      if (!invalidated && v1(owner) && listening.deadlines(value, owner!!)) promise.resolve(true)
      else promise.reject("listening_deadline", "Listening deadlines could not be saved.")
    }
  }

  @ReactMethod
  fun listeningCredentials(owner: String?, token: String?, promise: Promise) {
    main.post {
      if (!invalidated && v1(owner) && listening.credentials(token, owner!!)) promise.resolve(true)
      else promise.reject("listening_stale", "Listening authority changed.")
    }
  }

  @ReactMethod
  fun consumeListeningStop(key: String, promise: Promise) {
    main.post {
      try { promise.resolve(listening.consumeStop(key)) }
      catch(e: Exception) { promise.reject("mutation_store_unavailable","User changes could not be saved.",e) }
    }
  }

  @ReactMethod
  fun retireListening(owner: String?, stop: Boolean, promise: Promise) {
    main.post {
      if (!invalidated) listening.retire(owner, stop)
      val status = listening.stopDisposition
      promise.resolve(status?.let { Arguments.makeNativeMap(it) } ?: true)
    }
  }

  @ReactMethod
  fun listeningMetadata(owner: String?, metadata: ReadableMap?) {
    val value = metadata?.toHashMap()
    main.post { if (!invalidated) listening.metadata(value, owner) }
  }

  @ReactMethod
  fun listeningSkipIntervals(back: Double, forward: Double) {
    main.post { if (!invalidated) listening.skipIntervals(back, forward) }
  }
}
