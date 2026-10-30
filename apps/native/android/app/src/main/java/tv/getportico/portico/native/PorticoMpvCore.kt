package tv.getportico.portico.native

import android.os.Handler
import android.os.HandlerThread
import android.os.Looper
import android.view.Surface
import tv.getportico.portico.mpv.PorticoMpvNative

/** The properties the engine acts on, read together on the core's thread. */
internal class PorticoMpvSnapshot(
    /** Seconds in the file's own timeline; NaN when mpv has no position yet. */
    val timePos: Double = Double.NaN,
    val duration: Double = 0.0,
    val pause: Boolean = true,
    val pausedForCache: Boolean = false,
    val coreIdle: Boolean = true,
    val eofReached: Boolean = false,
    val seeking: Boolean = false,
    val speed: Double = 1.0,
    /** `aid`, `sid` and `vid` as mpv prints them: a track id, "no" or "auto". */
    val aid: String = "no",
    val sid: String = "no",
    val vid: String = "no",
    /** The hardware decoder in use ("mediacodec", "mediacodec-copy"), or "no" for software. */
    val hwdec: String = "no",
    /** How fast the cache is being filled, in bytes a second, and whether the reader is resting
     * because the cache is full (the speed then says nothing about the connection). */
    val cacheSpeed: Double = 0.0,
    val cacheIdle: Boolean = true,
    /** Seconds of media held ahead of the position; NaN when unknown. */
    val cacheDuration: Double = Double.NaN,
    /** Actual lavf demux format, sampled with this load's epoch, never inferred from a suffix. */
    val fileFormat: String = "",
)

internal sealed class PorticoMpvEvent {
  /** The file is open: its tracks and chapters are known. Nothing has been shown yet. */
  class FileLoaded(val tracks: List<Map<String, Any?>>, val chapters: List<Map<String, Any?>>) : PorticoMpvEvent()
  /** Playback (re)started after a load or a seek: the frame at the new position is ready. */
  object PlaybackRestart : PorticoMpvEvent()
  class EndFile(val reason: Int, val code: Int, val message: String, val log: List<String>) : PorticoMpvEvent()
  class TracksChanged(val tracks: List<Map<String, Any?>>) : PorticoMpvEvent()
  /** A fresh property snapshot after this exact file-track command ran on the core thread. */
  class AudioTrackReadback(val revision: Long, val code: Int) : PorticoMpvEvent()
  class VideoParams(val params: Map<String, Any?>) : PorticoMpvEvent()
  /** The subtitle line on screen now, as plain text ("" between lines). */
  class SubtitleText(val text: String) : PorticoMpvEvent()
  /** An observed property other than the two above changed; the snapshot has the new value. */
  class Changed(val name: String) : PorticoMpvEvent()
  /** An asynchronous command finished. `code` is 0 or a negative mpv error. */
  class Reply(val token: Long, val code: Int) : PorticoMpvEvent()
  /** Twice a second while a file is open. */
  object Tick : PorticoMpvEvent()
  /** Twenty times a second while the app is drawing a subtitle overlay itself. */
  class FastTick(val time: Double) : PorticoMpvEvent()
}

/**
 * A thin, serialised wrapper round one libmpv handle: the Android counterpart of
 * `Native/PorticoMpvCore.swift`.
 *
 * Every libmpv call that can wait happens on the core's own thread, never on the main thread, so
 * a busy player core can never stall the interface. The one exception is the surface: Android
 * requires that nothing draws on a surface once `surfaceDestroyed` returns, so giving and taking
 * the surface (and the video output opened on it) is done on the caller's thread and waits.
 *
 * Events are delivered on the main thread together with a property snapshot and the "epoch" of
 * the load they belong to; the controller discards anything whose epoch is not its current one,
 * so nothing from a replaced file is ever read as current.
 */
internal class PorticoMpvCore private constructor() : PorticoMpvNative.Listener {
  /** Called on the main thread. */
  var onEvent: ((Int, PorticoMpvEvent, PorticoMpvSnapshot) -> Unit)? = null
  /** Called once on the main thread when mpv has started (null) or could not (why). */
  private var onStarted: ((String?) -> Unit)? = null
  var version = ""
    private set

  private val thread = HandlerThread("portico-mpv-core").apply { start() }
  private val queue = Handler(thread.looper)
  private val main = Handler(Looper.getMainLooper())
  /** Guards the handle between the surface calls (any thread) and the end of mpv. */
  private val lock = Any()
  @Volatile private var handle = 0L

  // Core thread only.
  private var epoch = 0
  /** mpv has announced the start of the file loaded for `epoch`. */
  private var started = false
  /** mpv's own id for the playlist entry of that load; 0 when it could not be read. */
  private var entry = 0L
  private var ticking = false
  private var fastTicking = false
  private val logTail = ArrayDeque<String>()

  // --- mpv's events (its own thread) ------------------------------------------------------

  override fun onEvent(id: Int, userdata: Long, error: Int, entry: Long, reason: Int) {
    queue.post { handleEvent(id, userdata, error, entry, reason) }
  }

  override fun onProperty(name: String) {
    queue.post { handleProperty(name) }
  }

  override fun onLog(prefix: ByteArray, level: ByteArray, text: ByteArray) {
    // mpv only ever sees addresses on this device, but their path carries the playback's media
    // grant, which is left out of what is kept and logged.
    val line = GRANT.replace("${String(prefix, Charsets.UTF_8)}: ${String(text, Charsets.UTF_8).trim()}", "/v1/media/-/").take(300)
    // mpv's warnings and errors, where a developer looks for them (`adb logcat -s PorticoMpv`).
    android.util.Log.w("PorticoMpv", line)
    queue.post {
      logTail.addLast(line)
      while (logTail.size > 24) logTail.removeFirst()
    }
  }

  // --- commands (any thread) --------------------------------------------------------------

  /**
   * Replaces whatever is open with [url]. [options] apply to this file only (where it opens, so
   * nothing is decoded from the beginning only to be thrown away; whether it may open the
   * resources it names).
   */
  fun load(url: String, options: Map<String, String>, next: Int) {
    queue.post {
      // mpv did not start: the controller has been told why, and says so.
      if (handle == 0L) return@post
      epoch = next
      started = false
      entry = 0
      logTail.clear()
      val list = options.keys.sorted().joinToString(",") { "$it=${options[it]}" }
      // The command took per-file options as its third argument until mpv 0.38 and as its
      // fourth since. The form this mpv does not have is refused before anything is opened.
      var code = if (list.isEmpty()) run("loadfile", url, "replace") else run("loadfile", url, "replace", "-1", list)
      if (code < 0 && list.isNotEmpty()) code = run("loadfile", url, "replace", list)
      if (code < 0) {
        started = true
        deliver(PorticoMpvEvent.EndFile(PorticoMpvNative.END_ERROR, code, text(code), emptyList()))
        started = false
        return@post
      }
      // The load replaced the playlist, so its one entry is this file. Its id tells this
      // file's events from those of the file it replaced, which may still be queued.
      val id = handle.let { if (it == 0L) Long.MIN_VALUE else PorticoMpvNative.getLong(it, "playlist/0/id") }
      entry = if (id > 0) id else 0
      startTicker()
    }
  }

  /** Closes the open file. Events for it are no longer delivered. */
  fun stop() {
    queue.post {
      epoch = 0
      started = false
      stopTickers()
      run("stop")
    }
  }

  fun set(name: String, value: String) {
    queue.post { setNow(name, value) }
  }

  fun selectAudioTrack(track: Int, revision: Long, expectedEpoch: Int): Boolean {
    return queue.post {
      // The command belongs to a load that is gone (or has not started). Answer it so the
      // controller fails the choice honestly instead of waiting for a readback that can
      // never arrive; an old epoch's answer is dropped by the controller's own load fence.
      if (!started || epoch != expectedEpoch) { deliver(PorticoMpvEvent.AudioTrackReadback(revision, -2), expectedEpoch); return@post }
      val code = setNow("aid", track.toString())
      deliver(PorticoMpvEvent.AudioTrackReadback(revision, code))
    }
  }

  /** Sets each property in order; a later one applies only once the earlier ones have. */
  fun setAll(values: List<Pair<String, String>>) {
    queue.post { for ((name, value) in values) setNow(name, value) }
  }

  /** Runs a command; when [token] is not 0 its completion arrives as [PorticoMpvEvent.Reply]. */
  fun command(arguments: List<String>, token: Long = 0) {
    queue.post {
      val current = handle
      if (current == 0L) return@post
      val code = PorticoMpvNative.commandAsync(current, token, bytes(arguments))
      if (code < 0 && token != 0L) deliver(PorticoMpvEvent.Reply(token, code))
    }
  }

  /** Whether the app wants [PorticoMpvEvent.FastTick] (it is drawing subtitles itself). */
  fun setFastTicks(wanted: Boolean) {
    queue.post {
      if (!wanted) {
        fastTicking = false
        queue.removeCallbacks(fastTick)
        return@post
      }
      if (fastTicking || handle == 0L) return@post
      fastTicking = true
      queue.postDelayed(fastTick, 50)
    }
  }

  /** Ends mpv. Nothing is delivered afterwards. Call on the main thread. */
  fun destroy(completion: (() -> Unit)? = null) {
    onEvent = null
    onStarted = null
    val queued = queue.post {
      stopTickers()
      epoch = 0
      started = false
      synchronized(lock) {
        val current = handle
        handle = 0
        if (current != 0L) PorticoMpvNative.destroy(current)
      }
      if (completion != null) main.post(completion)
      thread.quitSafely()
    }
    if (!queued && completion != null) main.post(completion)
  }

  // --- the surface (the caller's thread; these wait) --------------------------------------

  // Guarded by `lock`.
  private var surfaceAttached = false
  private var output = "gpu"
  private var decoding = "no"

  /**
   * Which video output draws the picture ("gpu", "gpu-next" or "mediacodec_embed"), and which
   * hardware decoding goes with it. Applied at
   * once when a surface is attached, and remembered for the next surface otherwise: a video
   * output is never opened without a surface to draw on.
   */
  fun configureOutput(output: String, decoding: String) {
    synchronized(lock) {
      if (this.output == output && this.decoding == decoding) return
      this.output = output
      this.decoding = decoding
      val current = handle
      if (current == 0L || !surfaceAttached) return
      // Each step leaves a picture that can be drawn. Towards the decoder drawing straight onto
      // the surface, the decoder is switched first (its frames suit both outputs); away from it,
      // the output first (mpv's own output takes the frames the decoder was already making).
      if (output != "mediacodec_embed") {
        PorticoMpvNative.setProperty(current, "vo", output.toByteArray())
        PorticoMpvNative.setProperty(current, "hwdec", decoding.toByteArray())
      } else {
        PorticoMpvNative.setProperty(current, "hwdec", decoding.toByteArray())
        PorticoMpvNative.setProperty(current, "vo", output.toByteArray())
      }
    }
  }

  /** The same, after the commands already asked for (a file being closed, say) have run. */
  fun configureOutputLater(output: String, decoding: String) {
    queue.post { configureOutput(output, decoding) }
  }

  /** Gives mpv [surface] and opens the video output on it. When this returns, mpv may draw on the surface. */
  fun attachSurface(surface: Surface, width: Int, height: Int) {
    synchronized(lock) {
      val current = handle
      if (current == 0L) return
      if (surfaceAttached) {
        PorticoMpvNative.setProperty(current, "vo", "null".toByteArray())
        PorticoMpvNative.setOption(current, "force-window", "no".toByteArray())
      }
      if (PorticoMpvNative.setSurface(current, surface) < 0) {
        surfaceAttached = false
        return
      }
      if (width > 0 && height > 0) PorticoMpvNative.setProperty(current, "android-surface-size", "${width}x$height".toByteArray())
      PorticoMpvNative.setOption(current, "force-window", "yes".toByteArray())
      PorticoMpvNative.setProperty(current, "hwdec", decoding.toByteArray())
      PorticoMpvNative.setProperty(current, "vo", output.toByteArray())
      surfaceAttached = true
    }
  }

  /** The surface changed size. */
  fun surfaceSize(width: Int, height: Int) {
    synchronized(lock) {
      val current = handle
      if (current == 0L || !surfaceAttached || width <= 0 || height <= 0) return
      PorticoMpvNative.setProperty(current, "android-surface-size", "${width}x$height".toByteArray())
    }
  }

  /** Closes the video output and takes the surface away. When this returns, mpv no longer touches it. */
  fun detachSurface() {
    synchronized(lock) {
      val current = handle
      if (current == 0L || !surfaceAttached) return
      PorticoMpvNative.setProperty(current, "vo", "null".toByteArray())
      PorticoMpvNative.setOption(current, "force-window", "no".toByteArray())
      PorticoMpvNative.setSurface(current, null)
      surfaceAttached = false
    }
  }

  // --- core thread ------------------------------------------------------------------------

  private fun bytes(arguments: List<String>): Array<ByteArray> = Array(arguments.size) { arguments[it].toByteArray(Charsets.UTF_8) }

  private fun text(code: Int): String = try { PorticoMpvNative.errorString(code) ?: "" } catch (_: Throwable) { "" }

  private fun run(vararg arguments: String): Int {
    val current = handle
    if (current == 0L) return -1
    return PorticoMpvNative.command(current, bytes(arguments.asList()))
  }

  private fun setNow(name: String, value: String): Int {
    val current = handle
    if (current == 0L) return -1
    return PorticoMpvNative.setProperty(current, name, value.toByteArray(Charsets.UTF_8))
  }

  private val tick = object : Runnable {
    override fun run() {
      if (!ticking) return
      if (started) deliver(PorticoMpvEvent.Tick)
      queue.postDelayed(this, 500)
    }
  }

  private val fastTick = object : Runnable {
    override fun run() {
      if (!fastTicking) return
      val current = handle
      if (started && current != 0L) {
        val time = PorticoMpvNative.getDouble(current, "time-pos")
        if (time.isFinite()) {
          val load = epoch
          main.post { onEvent?.invoke(load, PorticoMpvEvent.FastTick(time), EMPTY) }
        }
      }
      queue.postDelayed(this, 50)
    }
  }

  private fun startTicker() {
    if (ticking) return
    ticking = true
    queue.postDelayed(tick, 500)
  }

  private fun stopTickers() {
    ticking = false
    fastTicking = false
    queue.removeCallbacks(tick)
    queue.removeCallbacks(fastTick)
  }

  private fun string(name: String): String? {
    val current = handle
    if (current == 0L) return null
    return PorticoMpvNative.getString(current, name)?.let { String(it, Charsets.UTF_8) }
  }

  private fun double(name: String): Double? {
    val current = handle
    if (current == 0L) return null
    return PorticoMpvNative.getDouble(current, name).takeIf { !it.isNaN() }
  }

  private fun flag(name: String): Boolean? {
    val current = handle
    if (current == 0L) return null
    val value = PorticoMpvNative.getFlag(current, name)
    return if (value < 0) null else value != 0
  }

  private fun readSnapshot() = PorticoMpvSnapshot(
      timePos = double("time-pos") ?: Double.NaN,
      duration = double("duration") ?: 0.0,
      pause = flag("pause") ?: true,
      pausedForCache = flag("paused-for-cache") ?: false,
      coreIdle = flag("core-idle") ?: true,
      eofReached = flag("eof-reached") ?: false,
      seeking = flag("seeking") ?: false,
      speed = double("speed") ?: 1.0,
      aid = string("aid") ?: "no",
      sid = string("sid") ?: "no",
      vid = string("vid") ?: "no",
      hwdec = string("hwdec-current") ?: "no",
      cacheSpeed = double("cache-speed") ?: 0.0,
      cacheIdle = flag("demuxer-cache-idle") ?: true,
      cacheDuration = double("demuxer-cache-duration") ?: Double.NaN,
      fileFormat = string("file-format") ?: "")

  private fun deliver(event: PorticoMpvEvent, load: Int = epoch) {
    val snapshot = readSnapshot()
    main.post { onEvent?.invoke(load, event, snapshot) }
  }

  private fun handleEvent(id: Int, userdata: Long, error: Int, eventEntry: Long, reason: Int) {
    if (handle == 0L) return
    when (id) {
      PorticoMpvNative.EVENT_START_FILE -> {
        if (epoch == 0) return
        started = entry == 0L || eventEntry == entry
      }
      PorticoMpvNative.EVENT_FILE_LOADED -> {
        if (!started) return
        deliver(PorticoMpvEvent.FileLoaded(PorticoMpvSupport.jsonList(string("track-list")), PorticoMpvSupport.jsonList(string("chapter-list"))))
      }
      PorticoMpvNative.EVENT_PLAYBACK_RESTART -> if (started) {
        // A property notification can trail restart. The first frame's colour information
        // must reach the controller before it chooses the output and releases the display hold.
        deliver(PorticoMpvEvent.VideoParams(PorticoMpvSupport.jsonMap(string("video-params"))))
        deliver(PorticoMpvEvent.PlaybackRestart)
      }
      PorticoMpvNative.EVENT_END_FILE -> {
        if (!started) return
        if (entry != 0L && eventEntry != entry) return
        // A redirect (a playlist that names the real stream) is followed by another start.
        if (reason == PorticoMpvNative.END_REDIRECT) return
        deliver(PorticoMpvEvent.EndFile(reason, error, text(error), logTail.toList()))
        started = false
      }
      PorticoMpvNative.EVENT_COMMAND_REPLY -> if (userdata != 0L) deliver(PorticoMpvEvent.Reply(userdata, error))
      else -> {}
    }
  }

  private fun handleProperty(name: String) {
    if (!started || handle == 0L) return
    when (name) {
      "track-list" -> deliver(PorticoMpvEvent.TracksChanged(PorticoMpvSupport.jsonList(string("track-list"))))
      "video-params" -> deliver(PorticoMpvEvent.VideoParams(PorticoMpvSupport.jsonMap(string("video-params"))))
      "sub-text" -> deliver(PorticoMpvEvent.SubtitleText(string("sub-text") ?: ""))
      else -> deliver(PorticoMpvEvent.Changed(name))
    }
  }

  companion object {
    private val EMPTY = PorticoMpvSnapshot()
    private val GRANT = Regex("/v1/media/(?:subtitles/)?[A-Za-z0-9_-]+/")
    private val OBSERVED = listOf("pause", "paused-for-cache", "core-idle", "eof-reached", "seeking", "speed", "duration", "aid", "sid", "vid", "track-list", "video-params", "hwdec-current", "sub-text")

    /** The libraries are in this build and load on this device. */
    val available: Boolean by lazy { try { PorticoMpvNative.load() } catch (_: Throwable) { false } }

    /**
     * Starts mpv on its own thread, so creating it never holds up the interface. [options] are
     * set before initialisation, in order. An option this build of mpv refuses stops the start
     * and is named, unless it is in [optional] (looks and performance, and options whose name
     * has changed between mpv versions). [started] runs on the main thread with null when mpv
     * is ready, or with why it is not. Null when this build has no mpv.
     *
     * Commands may be given at once: they wait their turn behind the start.
     */
    fun start(options: List<Pair<String, String>>, optional: Set<String>, started: (String?) -> Unit): PorticoMpvCore? {
      if (!available) return null
      val core = PorticoMpvCore()
      core.onStarted = started
      core.queue.post {
        var failure: String? = null
        val handle = try { PorticoMpvNative.create(core) } catch (_: Throwable) { 0L }
        if (handle == 0L) {
          failure = "mpv could not be created"
        } else {
          for ((name, value) in options) {
            val code = PorticoMpvNative.setOption(handle, name, value.toByteArray(Charsets.UTF_8))
            if (code < 0 && name !in optional) { failure = "option $name: ${core.text(code)}"; break }
          }
          if (failure == null && PorticoMpvNative.initialize(handle) < 0) failure = "mpv could not be initialised"
          if (failure != null) PorticoMpvNative.destroy(handle)
        }
        if (failure == null) {
          synchronized(core.lock) { core.handle = handle }
          core.version = PorticoMpvNative.getString(handle, "mpv-version")?.let { String(it, Charsets.UTF_8) } ?: ""
          for (name in OBSERVED) PorticoMpvNative.observe(handle, name)
        }
        core.main.post { core.onStarted?.invoke(failure) }
      }
      return core
    }
  }
}
