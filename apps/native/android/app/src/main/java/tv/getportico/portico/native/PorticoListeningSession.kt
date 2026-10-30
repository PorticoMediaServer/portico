@file:OptIn(UnstableApi::class)

package tv.getportico.portico.native

import android.app.PendingIntent
import android.content.ComponentName
import android.content.Context
import android.content.Intent
import android.os.Looper
import androidx.annotation.OptIn
import androidx.media3.common.C
import androidx.media3.common.MediaItem
import androidx.media3.common.MediaMetadata
import androidx.media3.common.PlaybackParameters
import androidx.media3.common.Player
import androidx.media3.common.SimpleBasePlayer
import androidx.media3.common.util.UnstableApi
import androidx.media3.session.MediaController
import androidx.media3.session.MediaSession
import androidx.media3.session.MediaSessionService
import androidx.media3.session.SessionToken
import com.google.common.util.concurrent.Futures
import com.google.common.util.concurrent.ListenableFuture

/**
 * Music and audiobooks in the system: the lock screen, the media notification, a headset, a watch,
 * a car. The Android counterpart of the Now Playing and remote-command half of
 * `Native/PorticoListeningOwner.m`.
 *
 * The system talks to a player that plays nothing. It shows what the listening owner reports and
 * turns every press into the owner's ordinary intent, so the timeline, the lease and the queue
 * have one author whether a press comes from the app, the lock screen or a steering wheel.
 */
internal object PorticoListeningSession {
  private var owner: PorticoListeningOwner? = null
  private var facade: Facade? = null
  private var connection: ListenableFuture<MediaController>? = null

  fun attach(context: Context, owner: PorticoListeningOwner) {
    this.owner = owner
    owner.onSystemChange = { changed(context) }
  }

  fun detach(context: Context, owner: PorticoListeningOwner) {
    if (this.owner !== owner) return
    this.owner = null
    changed(context)
  }

  /** The player the service's media session is built on. */
  fun player(): Player = facade ?: Facade(Looper.getMainLooper()).also { facade = it }

  private fun changed(context: Context) {
    val active = owner?.systemState() != null
    if (active && connection == null) {
      // Connecting a controller starts the service; the service then keeps itself in the
      // foreground for as long as something plays, which is what keeps music going with the
      // screen off and the app out of sight.
      connection = try {
        MediaController.Builder(context, SessionToken(context, ComponentName(context, PorticoListeningService::class.java))).buildAsync()
      } catch (_: Exception) { null }
    }
    facade?.refresh()
    if (!active) {
      connection?.let { MediaController.releaseFuture(it) }
      connection = null
    }
  }

  private class Facade(looper: Looper) : SimpleBasePlayer(looper) {
    fun refresh() = invalidateState()

    override fun getState(): State {
      val state = owner?.systemState() ?: return State.Builder().setAvailableCommands(Player.Commands.EMPTY).setPlaybackState(Player.STATE_IDLE).build()
      val commands = Player.Commands.Builder().addAll(
          Player.COMMAND_PLAY_PAUSE, Player.COMMAND_STOP, Player.COMMAND_SEEK_IN_CURRENT_MEDIA_ITEM, Player.COMMAND_SEEK_BACK, Player.COMMAND_SEEK_FORWARD,
          Player.COMMAND_GET_CURRENT_MEDIA_ITEM, Player.COMMAND_GET_METADATA, Player.COMMAND_GET_TIMELINE, Player.COMMAND_SET_SPEED_AND_PITCH)
      if (state.queue) commands.addAll(Player.COMMAND_SEEK_TO_NEXT, Player.COMMAND_SEEK_TO_NEXT_MEDIA_ITEM, Player.COMMAND_SEEK_TO_PREVIOUS, Player.COMMAND_SEEK_TO_PREVIOUS_MEDIA_ITEM)
      val metadata = MediaMetadata.Builder().setTitle(state.title).setArtist(state.artist).setAlbumTitle(state.album).setMediaType(MediaMetadata.MEDIA_TYPE_MUSIC)
      state.artwork?.let { metadata.setArtworkData(it, MediaMetadata.PICTURE_TYPE_FRONT_COVER) }
      val described = metadata.build()
      val current = MediaItemData.Builder(state.sessionId)
          .setMediaItem(MediaItem.Builder().setMediaId(state.sessionId).setMediaMetadata(described).build())
          .setMediaMetadata(described)
          .setDurationUs(if (state.durationMs > 0) state.durationMs * 1000 else C.TIME_UNSET)
          .setIsSeekable(true)
          .build()
      // The system only offers Next and Previous when the playlist has neighbours: the queue is
      // the server's, so the neighbours here are stand-ins and a press is the owner's "next".
      val playlist = if (state.queue) listOf(neighbour("portico.previous"), current, neighbour("portico.next")) else listOf(current)
      val moving = state.playing && !state.buffering && !state.ended
      return State.Builder()
          .setAvailableCommands(commands.build())
          .setPlayWhenReady(state.playing, Player.PLAY_WHEN_READY_CHANGE_REASON_USER_REQUEST)
          .setPlaybackState(if (state.ended) Player.STATE_ENDED else if (state.buffering) Player.STATE_BUFFERING else Player.STATE_READY)
          .setPlaylist(playlist)
          .setCurrentMediaItemIndex(if (state.queue) 1 else 0)
          .setContentPositionMs(if (moving) PositionSupplier.getExtrapolating(state.positionMs, state.rate) else PositionSupplier.getConstant(state.positionMs))
          .setPlaybackParameters(PlaybackParameters(state.rate))
          .setSeekBackIncrementMs(state.skipBackMs)
          .setSeekForwardIncrementMs(state.skipForwardMs)
          .build()
    }

    private fun neighbour(id: String): MediaItemData =
        MediaItemData.Builder(id).setMediaItem(MediaItem.Builder().setMediaId(id).build()).setIsPlaceholder(true).build()

    private fun done(): ListenableFuture<*> = Futures.immediateVoidFuture()

    override fun handleSetPlayWhenReady(playWhenReady: Boolean): ListenableFuture<*> {
      owner?.remote(if (playWhenReady) "resume" else "pause", null)
      return done()
    }

    override fun handlePrepare(): ListenableFuture<*> = done()

    override fun handleStop(): ListenableFuture<*> {
      owner?.remote("stop", null)
      return done()
    }

    override fun handleRelease(): ListenableFuture<*> = done()

    override fun handleSeek(mediaItemIndex: Int, positionMs: Long, seekCommand: Int): ListenableFuture<*> {
      val current = if (owner?.systemState()?.queue == true) 1 else 0
      when {
        seekCommand == Player.COMMAND_SEEK_TO_NEXT || seekCommand == Player.COMMAND_SEEK_TO_NEXT_MEDIA_ITEM || mediaItemIndex > current -> owner?.remote("next", null)
        seekCommand == Player.COMMAND_SEEK_TO_PREVIOUS || seekCommand == Player.COMMAND_SEEK_TO_PREVIOUS_MEDIA_ITEM || mediaItemIndex < current -> owner?.remote("previous", null)
        positionMs != C.TIME_UNSET -> owner?.remote("seek", positionMs / 1000.0)
      }
      return done()
    }

    override fun handleSetPlaybackParameters(playbackParameters: PlaybackParameters): ListenableFuture<*> {
      owner?.remote("rate", playbackParameters.speed.toDouble())
      return done()
    }
  }
}

/**
 * The service the system binds to for Portico's music and audiobooks. It holds the media session
 * and, while something plays, the foreground notification.
 */
class PorticoListeningService : MediaSessionService() {
  private var session: MediaSession? = null

  override fun onCreate() {
    super.onCreate()
    // After the process was killed there is no owner and nothing to resume: the service ends.
    if (PorticoListeningOwner.shared == null) return
    val builder = MediaSession.Builder(this, PorticoListeningSession.player()).setId("portico.listening")
    packageManager.getLaunchIntentForPackage(packageName)?.let { open ->
      builder.setSessionActivity(PendingIntent.getActivity(this, 0, open.addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP), PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT))
    }
    session = try { builder.build() } catch (_: Exception) { null }
  }

  override fun onGetSession(controllerInfo: MediaSession.ControllerInfo): MediaSession? = session

  override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
    val result = super.onStartCommand(intent, flags, startId)
    if (session == null) stopSelf()
    return result
  }

  override fun onDestroy() {
    session?.release()
    session = null
    super.onDestroy()
  }
}
