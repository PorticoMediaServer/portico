package tv.getportico.portico.native

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.media.AudioManager
import android.os.Handler
import android.os.Looper
import androidx.media3.common.C
import androidx.media3.common.ForwardingPlayer
import androidx.media3.common.MediaMetadata
import androidx.media3.common.Player
import androidx.media3.session.MediaSession
import com.facebook.react.bridge.Arguments
import com.facebook.react.bridge.Promise
import com.facebook.react.bridge.ReactApplicationContext
import com.facebook.react.bridge.ReactMethod
import com.facebook.react.bridge.ReadableMap
import java.io.IOException
import java.util.concurrent.TimeUnit
import okhttp3.Call
import okhttp3.Callback
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response

/**
 * `NativeModules.PorticoSystemMedia`: a video in the system — the media session that headset
 * buttons, a television remote's transport keys, Bluetooth and other controllers talk to, audio
 * interruptions, and headphones being unplugged.
 *
 * Native never drives the player. Every press and every interruption arrives in JavaScript as a
 * `PorticoSystemMediaCommand`, which the player turns into its ordinary intent, so Watch
 * Together's rules and progress reporting stay in one place.
 */
class PorticoSystemMedia(context: ReactApplicationContext) : PorticoEventModule(context) {
  private val main = Handler(Looper.getMainLooper())
  private var session: MediaSession? = null
  private var wrapped: Player? = null
  private var info: Map<String, Any?>? = null
  private var artwork: ByteArray? = null
  private var artworkCall: Call? = null
  private var noisy: BroadcastReceiver? = null
  /** The engine that has the video changed (a lease taken or given up, a title moved to the other engine). */
  private val reattach: () -> Unit = { main.post { if (info != null && activePlayer() !== wrapped) publish() } }

  init { current = this }

  override fun getName(): String = "PorticoSystemMedia"

  @ReactMethod fun addListener(eventName: String) {}

  @ReactMethod fun removeListeners(count: Double) {}

  override fun invalidate() {
    main.post { close(); if (current === this) current = null }
    super.invalidate()
  }

  @ReactMethod
  fun setVideo(video: ReadableMap?) {
    val value = video?.toHashMap()
    main.post {
      val previous = info
      info = value
      if (previous?.get("itemId") != value?.get("itemId") || previous?.get("posterPath") != value?.get("posterPath")) loadArtwork()
      publish()
    }
  }

  @ReactMethod
  fun clear() {
    main.post { info = null; close() }
  }

  /** Android plays where the system sends audio; there is no external video route to name here. */
  @ReactMethod
  fun currentRoute(promise: Promise) {
    promise.resolve(Arguments.createMap().apply { putBoolean("external", false); putString("kind", "none"); putString("name", "") })
  }

  private fun command(name: String, reason: String, extra: Map<String, Any?> = emptyMap()) {
    val item = info?.get("itemId") as? String ?: return
    val body = HashMap<String, Any?>(extra)
    body["command"] = name
    body["itemId"] = item
    // A command belongs to the exact published source, even when a replacement
    // plays the same item. JavaScript refuses a token from an older fence/lease.
    (info?.get("sourceToken") as? String)?.let { body["sourceToken"] = it }
    body["reason"] = reason
    emit("PorticoSystemMediaCommand", Arguments.makeNativeMap(body))
  }

  /** The Picture in Picture window was closed: the film stops with it. */
  internal fun dismissed() = command("pause", "route")

  /**
   * From a player engine that manages its own sound (mpv): another app took the audio, or gave
   * it back. Media3 says the same through its player listener below.
   */
  internal fun engineCommand(name: String, reason: String) = command(name, reason)

  /**
   * The engine that has the video: mpv while it holds a lease with media open, otherwise the
   * Media3 player. The system sees one video session whichever engine plays.
   */
  private fun activePlayer(): Player? {
    val mpv = PorticoMpvController.shared
    if (mpv != null && mpv.mediaOpen) return mpv.sessionPlayer
    return PorticoPlaybackController.shared?.player
  }

  private fun watch(on: Boolean) {
    val native = PorticoPlaybackController.shared?.attachmentListeners
    val mpv = PorticoMpvController.shared?.attachmentListeners
    if (on) { native?.add(reattach); mpv?.add(reattach) } else { native?.remove(reattach); mpv?.remove(reattach) }
  }

  private fun publish() {
    val video = info
    val controller = PorticoPlaybackController.shared
    val player = activePlayer()
    // Music and audiobooks have their own session (the listening owner's). While that owner holds
    // the player there is no video session: a second one, even for a moment, takes the headset
    // and lock-screen keys with it when it goes.
    if (video == null || player == null || controller?.listeningActive == true) {
      close(stopArtwork = video == null)
      if (video != null) watch(true)
      return
    }
    if (wrapped !== player) {
      close(stopArtwork = false)
      watch(true)
      wrapped = player
      player.addListener(interruptions)
      session = MediaSession.Builder(reactApplicationContext, Proxy(player)).setId("portico.video").build()
      val receiver = object : BroadcastReceiver() {
        // Headphones unplugged: never carry on through the speaker.
        override fun onReceive(context: Context?, intent: Intent?) { if (player.playWhenReady) command("pause", "route") }
      }
      noisy = receiver
      reactApplicationContext.registerReceiver(receiver, IntentFilter(AudioManager.ACTION_AUDIO_BECOMING_NOISY))
    }
    // The session reads the item's description from the player; say it changed.
    (session?.player as? Proxy)?.describe()
  }

  private fun close(stopArtwork: Boolean = true) {
    if (stopArtwork) {
      artworkCall?.cancel()
      artworkCall = null
    }
    wrapped?.removeListener(interruptions)
    wrapped = null
    session?.release()
    session = null
    noisy?.let { try { reactApplicationContext.unregisterReceiver(it) } catch (_: Exception) {} }
    noisy = null
    watch(false)
  }

  private val interruptions = object : Player.Listener {
    // A call or another app took the audio for a moment, then gave it back.
    override fun onPlaybackSuppressionReasonChanged(reason: Int) {
      if (reason == Player.PLAYBACK_SUPPRESSION_REASON_TRANSIENT_AUDIO_FOCUS_LOSS) command("pause", "interruption")
      else if (reason == Player.PLAYBACK_SUPPRESSION_REASON_NONE && wrapped?.playWhenReady == true) command("play", "interruption")
    }

    // Another app took the audio for good.
    override fun onPlayWhenReadyChanged(playWhenReady: Boolean, reason: Int) {
      if (!playWhenReady && reason == Player.PLAY_WHEN_READY_CHANGE_REASON_AUDIO_FOCUS_LOSS) command("pause", "interruption")
    }
  }

  /** For artwork: the viewer's server origin, bearer, and a protected poster path. */
  private fun loadArtwork() {
    artworkCall?.cancel()
    artworkCall = null
    artwork = null
    val video = info ?: return
    val origin = video["origin"] as? String ?: return
    val bearer = video["bearer"] as? String ?: return
    val path = video["posterPath"] as? String ?: return
    if (!POSTER.matches(path) || !(origin.startsWith("https://") || origin.startsWith("http://"))) return
    val item = video["itemId"]
    val call = try { artworkClient.newCall(Request.Builder().url(origin.trimEnd('/') + path + "?w=800").header("Authorization", "Bearer $bearer").build()) } catch (_: Exception) { return }
    artworkCall = call
    call.enqueue(object : Callback {
      override fun onFailure(call: Call, e: IOException) {}

      override fun onResponse(call: Call, response: Response) {
        val bytes = response.use {
          val body = it.body
          if (it.code != 200 || body == null || body.contentLength() > ARTWORK_LIMIT) null
          else try {
            val source = body.source()
            val buffer = okio.Buffer()
            while (source.read(buffer, 16384) != -1L) {
              if (buffer.size > ARTWORK_LIMIT) return@use null
            }
            buffer.readByteArray()
          } catch (_: IOException) { null }
        }
        if (bytes == null || bytes.size > ARTWORK_LIMIT) return
        main.post { if (info?.get("itemId") == item && artworkCall === call) { artwork = bytes; (session?.player as? Proxy)?.describe() } }
      }
    })
  }

  /** The player as the system sees it: what it shows is real, what it asks for goes to JavaScript. */
  private inner class Proxy(player: Player) : ForwardingPlayer(player) {
    private var described = MediaMetadata.EMPTY
    private val observers = LinkedHashSet<Player.Listener>()

    fun describe() {
      val video = info ?: return
      val builder = MediaMetadata.Builder()
          .setTitle(video["title"] as? String)
          .setSubtitle(video["subtitle"] as? String)
          .setArtist((video["seriesTitle"] as? String) ?: (video["subtitle"] as? String))
          .setMediaType(MediaMetadata.MEDIA_TYPE_VIDEO)
      artwork?.let { builder.setArtworkData(it, MediaMetadata.PICTURE_TYPE_FRONT_COVER) }
      described = builder.build()
      observers.toList().forEach { it.onMediaMetadataChanged(described); it.onAvailableCommandsChanged(availableCommands) }
    }

    private fun flag(name: String) = info?.get(name) == true

    private fun seconds(name: String, fallback: Double) = PorticoPlaybackController.number(info?.get(name)) ?: fallback

    override fun addListener(listener: Player.Listener) { observers.add(listener); super.addListener(listener) }

    override fun removeListener(listener: Player.Listener) { observers.remove(listener); super.removeListener(listener) }

    override fun getMediaMetadata(): MediaMetadata = described

    override fun getAvailableCommands(): Player.Commands {
      val builder = super.getAvailableCommands().buildUpon()
          .add(Player.COMMAND_PLAY_PAUSE)
          .removeAll(Player.COMMAND_SEEK_TO_NEXT, Player.COMMAND_SEEK_TO_NEXT_MEDIA_ITEM, Player.COMMAND_SEEK_TO_PREVIOUS, Player.COMMAND_SEEK_TO_PREVIOUS_MEDIA_ITEM)
      if (flag("canNext")) builder.addAll(Player.COMMAND_SEEK_TO_NEXT, Player.COMMAND_SEEK_TO_NEXT_MEDIA_ITEM)
      if (flag("canPrevious")) builder.addAll(Player.COMMAND_SEEK_TO_PREVIOUS, Player.COMMAND_SEEK_TO_PREVIOUS_MEDIA_ITEM)
      if (flag("live")) builder.removeAll(Player.COMMAND_SEEK_BACK, Player.COMMAND_SEEK_FORWARD, Player.COMMAND_SEEK_IN_CURRENT_MEDIA_ITEM)
      return builder.build()
    }

    override fun isCommandAvailable(command: Int): Boolean = availableCommands.contains(command)

    override fun getSeekBackIncrement(): Long = (seconds("skipBackSeconds", 10.0) * 1000).toLong()

    override fun getSeekForwardIncrement(): Long = (seconds("skipForwardSeconds", 30.0) * 1000).toLong()

    override fun play() = command("play", "remote")

    override fun pause() = command("pause", "remote")

    override fun setPlayWhenReady(playWhenReady: Boolean) = command(if (playWhenReady) "play" else "pause", "remote")

    override fun stop() = command("pause", "remote")

    override fun seekTo(positionMs: Long) = command("seek", "remote", mapOf("positionSeconds" to positionMs / 1000.0))

    override fun seekTo(mediaItemIndex: Int, positionMs: Long) {
      if (positionMs != C.TIME_UNSET) seekTo(positionMs)
    }

    override fun seekBack() = command("skip", "remote", mapOf("deltaSeconds" to -seconds("skipBackSeconds", 10.0)))

    override fun seekForward() = command("skip", "remote", mapOf("deltaSeconds" to seconds("skipForwardSeconds", 30.0)))

    override fun seekToNext() = command("next", "remote")

    override fun seekToNextMediaItem() = command("next", "remote")

    override fun seekToPrevious() = command("previous", "remote")

    override fun seekToPreviousMediaItem() = command("previous", "remote")

    override fun setPlaybackSpeed(speed: Float) {}

    override fun setPlaybackParameters(playbackParameters: androidx.media3.common.PlaybackParameters) {}
  }

  companion object {
    private const val ARTWORK_LIMIT = 4L * 1024 * 1024
    private val POSTER = Regex("^/v1/items/[A-Za-z0-9_-]{1,128}/art/(poster|backdrop|thumb)$")
    private val artworkClient: OkHttpClient by lazy {
      OkHttpClient.Builder().porticoTrust().followRedirects(false).followSslRedirects(false).cache(null).callTimeout(15, TimeUnit.SECONDS).build()
    }

    /** The module of the running JavaScript, for the parts of the native layer that have a command to report. */
    @Volatile internal var current: PorticoSystemMedia? = null
  }
}
