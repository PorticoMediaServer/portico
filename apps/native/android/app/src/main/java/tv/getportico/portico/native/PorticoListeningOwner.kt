package tv.getportico.portico.native

import tv.getportico.portico.policy.PorticoListeningPolicy

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.media.AudioDeviceInfo
import android.media.AudioManager
import android.net.Uri
import android.os.Handler
import android.os.Looper
import android.os.SystemClock
import java.io.ByteArrayOutputStream
import java.io.IOException
import java.util.Calendar
import java.util.TimeZone
import java.util.UUID
import java.util.concurrent.TimeUnit
import okhttp3.Call
import okhttp3.Callback
import okhttp3.Headers
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response
import org.json.JSONArray
import org.json.JSONObject
import org.json.JSONTokener

/**
 * The Playback v1 listening owner: the Android counterpart of `Native/PorticoListeningOwner.m`.
 *
 * One v1 audio session and its queue. It reports the timeline (which holds the lease), pauses and
 * resumes with PATCH, moves the queue with `:advance`, and makes gapless and crossfaded edges with
 * `:prepare-next` and `:commit-next` (spec §18), all without JavaScript. JavaScript hands a session
 * over once (`adopt`), then sends intents and reads snapshots.
 *
 * The engine is the app's one player controller: this builds the command JavaScript would have
 * sent, and reads the controller's facts. Main thread only.
 */
internal class PorticoListeningOwner(private val context: Context, private val controller: PorticoPlaybackController) {
  var onUserMutation: ((Map<String, Any?>) -> Unit)? = null
  var stopDisposition: Map<String, Any?>? = null
    private set
  private var mutationIssuedBaseMs = 0L
  private var mutationClockMono = 0.0
  private var mutationScope: Map<String, Any?>? = null
  var onSnapshot: ((Map<String, Any?>) -> Unit)? = null
  /** The system's media session shows this owner: it is told when there is something new to show. */
  var onSystemChange: (() -> Unit)? = null

  /** What the lock screen, the notification and a headset see. */
  class SystemState(val sessionId: String, val title: String, val artist: String, val album: String, val artwork: ByteArray?, val durationMs: Long,
      val positionMs: Long, val playing: Boolean, val buffering: Boolean, val ended: Boolean, val rate: Float, val queue: Boolean, val skipBackMs: Long, val skipForwardMs: Long)

  private val main = Handler(Looper.getMainLooper())
  private var skipBackSeconds = 30.0
  private var skipForwardSeconds = 30.0
  var activeOwner: String? = null
    private set
  private var origin = ""
  private var token = ""
  private var journalKey: String? = null
  private var error: String? = null
  private var notice: String? = null
  private var audioAttempt: String? = null
  // The session reported for now; while a committed edge drains, `audible` is the one heard.
  private var session: Map<String, Any?> = emptyMap()
  private var audible: Map<String, Any?>? = null
  private var end: Map<String, Any?>? = null
  private var queueHeader: Map<String, Any?>? = null
  private var queue: MutableMap<String, Any?>? = null
  private var duration = 0.0
  private var audibleDuration = 0.0
  private var transitions = false
  private var intent = "paused"
  private var pausedAt = 0.0
  private var wasPlaying = false
  private var rate = 1.0
  private var seq = 0L
  private var audibleSeq = 0L
  // Observations: the epoch this owner reports under (claimed, or handed over by JS), its own
  // clocks since the claim and the seek serial (seekRevision). The latest is sent once a second.
  private var epoch: String? = null
  private var replaces: String? = null
  private var observed = 0L
  private var claimedAt = 0.0
  private var playClock = 0.0
  private var playingSince = -1.0
  private var leaseUntil = 0.0
  private var nextReport = 0.0
  private var reportEvery = 10.0
  private var retryAt = 0.0
  private var backoff = 0.0
  private var audioCooldown = 0.0
  private var reportNow = false
  private var patchWanted = false
  private var ended = false
  private var endedReported = false
  private var terminal = false
  private var safetyPaused = false
  private var engineFailed = false
  private var busy = false
  private var invalidated = false
  private var emitting = false
  private var routeReady = true
  private var rendering = false
  private var routeRevision = -1L
  private var seekRevision = 0L
  private var sequence = 0L
  private var ackSequence = 0L
  private var transitionSequence = 0L
  private var failures = 0
  private var commitTries = 0
  private var actionRevision = 0L
  private var nativeSeek: Map<String, Any?>? = null
  private var lastFact: Map<String, Any?>? = null
  private var transition: Map<String, Any?>? = null
  // §18: the prepared next entry {token, expiresAtMs, entryId, itemId, presentation}; the committed answer.
  private var prepared: Map<String, Any?>? = null
  private var committed: Map<String, Any?>? = null
  private var audioReady = false
  private var commitWanted = false
  private var fastRetry = false
  // A commit whose answer never came may have landed: its token is kept and replayed (it is
  // idempotent) before anything else moves the queue or ends playback.
  private var lostCommit: Map<String, Any?>? = null
  private var parkedCommit: Map<String, Any?>? = null
  // The prepared token whose album is still being read (the commit waits for it, briefly).
  private var albumFor: String? = null
  // A start the owner must make itself (an advance that answered without a session).
  private var startPending: Map<String, Any?>? = null
  private var checkEnd = false
  private var renderFailures = 0
  private var replayTries = 0
  private var noRender: String? = null
  private val actions = ArrayList<MutableMap<String, Any?>>()
  private var request: Exchange? = null
  private var metadataRequest: Exchange? = null
  private var artworkRequest: Exchange? = null
  private var policy: Map<String, Any?>? = null
  private var timerCustody: Map<String, Any?>? = null
  private var playbackRun: Double? = null
  private var metadata: Map<String, Any?>? = null
  private val deadlines = HashMap<String, Any?>()
  private val deadlineMono = HashMap<String, Double>()
  private var timerStopped = false
  private var expiredTimerRevision = -1L
  private var activityWall = 0.0
  private var policyRateKey: String? = null
  private var artworkPath: String? = null
  private var artwork: ByteArray? = null
  private var ticking = false
  private var noisy: BroadcastReceiver? = null

  // --- adoption --------------------------------------------------------------------------

  /** Null when the session is adopted; otherwise why it was refused. */
  fun adopt(handover: Map<String, Any?>?): String? {
    if (invalidated || handover == null) return "Playback owner is closed or invalid."
    val origin = handover["origin"] as? String
    val token = handover["accessToken"] as? String
    val owner = handover["owner"] as? String
    val principal = handover["principalKey"] as? String
    val url = origin?.let { originOf(it) }
    @Suppress("UNCHECKED_CAST") val transport = handover["transport"] as? Map<String, Any?>
    val verifiedTransport = transport != null && transport["ready"] is Boolean && whole(transport["revision"], 0.0, MAX_INTEGER)
    val local = url?.host?.lowercase() in LOOPBACK
    if (url == null || !(url.scheme == "https" || (url.scheme == "http" && (local || verifiedTransport))) || !wireId(owner) || token.isNullOrEmpty() ||
        token.contains('\n') || token.contains('\r') || principal.isNullOrEmpty() || principal.length > 2048) return "Invalid listening authority."
    @Suppress("UNCHECKED_CAST") val session = handover["session"] as? Map<String, Any?>
    if (!v1Session(session)) return "Invalid v1 audio session."
    val queueId = handover["queueId"]
    val queueRevision = handover["queueRevision"]
    if (queueId != null && (!v1Id(queueId) || !v1Revision(queueRevision))) return "Invalid listening queue."
    if (!whole(handover["lastSeq"], 0.0, MAX_INTEGER) || !inRange(handover["leaseMs"], 0.0, 120000.0) || !inRange(handover["durationSeconds"], 0.0, 1e9) ||
        handover["intent"] !in INTENTS || handover["transitions"] !is Boolean) return "Invalid listening handover."
    activeOwner?.let { retire(it, false) }
    @Suppress("UNCHECKED_CAST") val mutationScope = handover["mutationScope"] as? Map<String,Any?>
    mutationIssuedBaseMs = System.currentTimeMillis() + (num(handover["mutationClockOffsetMs"])?.toLong() ?: 0L); mutationClockMono=mono()
    this.mutationScope = mutationScope; stopDisposition = null
    activeOwner = owner
    this.origin = origin
    this.token = token
    journalKey = "listening-v1:$principal"
    pausedAt = 0.0; wasPlaying = false
    this.session = session!!
    playbackRun = if(whole(handover["playbackRun"],0.0,MAX_INTEGER)) num(handover["playbackRun"]) else null
    audible = null; end = null; queueHeader = null
    queue = if (queueId != null) hashMapOf("id" to queueId, "revision" to queueRevision) else null
    duration = num(handover["durationSeconds"])!!
    if (duration <= 0) duration = PorticoAudioPlan.durationSeconds(decodePlan(presentation(session)))
    transitions = handover["transitions"] as Boolean
    intent = handover["intent"] as String
    rate = 1.0
    seq = num(handover["lastSeq"])!!.toLong(); audibleSeq = 0
    epoch = null; replaces = (handover["observerEpoch"] as? String)?.takeIf { wireId(it) }; observed = 0; playClock = 0.0; playingSince = -1.0
    leaseUntil = mono() + num(handover["leaseMs"])!! / 1000.0
    nextReport = 0.0; reportEvery = 10.0; retryAt = 0.0; backoff = 0.0; audioCooldown = 0.0
    reportNow = true; patchWanted = false; ended = false; endedReported = false; terminal = false; safetyPaused = false; engineFailed = false; busy = false; rendering = false
    routeReady = true; routeRevision = -1; seekRevision = 0; sequence = 0; ackSequence = 0; transitionSequence = 0; failures = 0; commitTries = 0; actionRevision = 0
    nativeSeek = seekTo(startSeconds(session))
    lastFact = null; transition = null; prepared = null; committed = null; audioReady = false; commitWanted = false; audioAttempt = null; error = null; notice = null
    lostCommit = null; parkedCommit = null; startPending = null; checkEnd = false; renderFailures = 0; replayTries = 0; noRender = null; albumFor = null
    actions.clear(); policy = null; timerCustody = null; metadata = null; timerStopped = false; expiredTimerRevision = -1; activityWall = wall() * 1000; policyRateKey = null
    deadlines.clear(); deadlines["sleepAtMs"] = null; deadlines["passoutAtMs"] = null; deadlineMono.clear()
    // A commit whose answer was lost (the app was killed) is replayed: it is idempotent by token.
    val journal = readJournal()
    @Suppress("UNCHECKED_CAST") val savedCommit = journal?.get("commit") as? Map<String, Any?>
    if (journal?.get("sessionId") == session["id"] && savedCommit != null && savedCommit["queueId"] == queue?.get("id") && wireToken(savedCommit["token"])) {
      prepared = mapOf("token" to savedCommit["token"], "expiresAtMs" to wall() * 1000 + 30000, "entryId" to "", "itemId" to "", "presentation" to emptyMap<String, Any?>(), "recovered" to true)
      commitWanted = true
      safetyPaused = true
    }
    (journal?.get("deadlines") as? Map<*, *>)?.let { saved ->
      for (field in DEADLINES) { val at = num(saved[field]); if (at != null && at > wall() * 1000) { deadlines[field] = at; deadlineMono[field] = mono() + Math.max(0.0, at / 1000.0 - wall()) } }
    }
    @Suppress("UNCHECKED_CAST") val handedPolicy = handover["policy"] as? Map<String, Any?>
    if (handedPolicy != null && !policy(handedPolicy, owner!!)) { retire(owner, false); return "Invalid listening policy." }
    if (!persist()) { retire(owner!!, false); return "Listening journal could not be saved." }
    if (transport != null && !route(transport, owner!!)) { retire(owner, false); return "Invalid verified transport." }
    controller.onInterruption = { change -> interruption(change) }
    val receiver = object : BroadcastReceiver() {
      override fun onReceive(context: Context?, intent: Intent?) { activeOwner?.let { action("pause", null, it) } }
    }
    noisy = receiver
    try { context.registerReceiver(receiver, IntentFilter(AudioManager.ACTION_AUDIO_BECOMING_NOISY)) } catch (_: Exception) { noisy = null }
    apply(); emit(); loadCurrentMetadata()
    ticking = true
    main.removeCallbacks(loop)
    main.postDelayed(loop, 500)
    tick()
    return null
  }

  private val loop = object : Runnable {
    override fun run() { if (!ticking) return; tick(); main.postDelayed(this, 500) }
  }

  private fun readJournal(): Map<String, Any?>? {
    val key = journalKey ?: return null
    val read = PorticoRecordStore.get(context).read(PorticoRecordStore.QUEUES, key) as? RecordRead.Value ?: return null
    @Suppress("UNCHECKED_CAST") return try { parse(read.bytes) as? Map<String, Any?> } catch (_: Exception) { null }
  }

  /** Only what a relaunch or a lost answer needs: the session, the seq reached, a commit in flight. */
  private fun persist(): Boolean {
    val key = journalKey ?: return false
    if (activeOwner == null) return false
    val commit = if (commitWanted && prepared != null) mapOf("token" to prepared!!["token"], "queueId" to (queue?.get("id") ?: "")) else lostCommit
    val row = mapOf("version" to 1, "sessionId" to session["id"], "queueId" to queue?.get("id"), "seq" to seq, "commit" to commit, "deadlines" to HashMap(deadlines),
        "timerStopped" to timerStopped, "expiredTimerRevision" to expiredTimerRevision)
    val ok = PorticoRecordStore.get(context).write(PorticoRecordStore.QUEUES, key, json(row).toByteArray(Charsets.UTF_8))
    if (!ok) { safetyPaused = true; error = "Playback storage is unavailable. Playback paused."; apply() }
    return ok
  }

  // --- the engine ------------------------------------------------------------------------

  private fun heard(): Map<String, Any?> = audible ?: session
  private fun effects(): Map<String, Any?> {
    @Suppress("UNCHECKED_CAST") return policy?.get("effects") as? Map<String, Any?> ?: mapOf("gapless" to false, "crossfadeSeconds" to 0.0, "normalization" to "off")
  }
  private fun fade(): Double = num(effects()["crossfadeSeconds"]) ?: 0.0
  private fun effectsWanted(): Boolean { val e = effects(); return e["gapless"] == true || fade() > 0 || e["normalization"] != "off" }
  private fun edgesWanted(): Boolean = effects()["gapless"] == true || fade() > 0

  /** The plan with an absolute address, when this engine can play it. */
  private fun plan(presentation: Map<String, Any?>?): Map<String, Any?>? {
    val plan = decodePlan(presentation) ?: return null
    if (!PorticoAudioPlan.playable(plan)) return null
    val url = plan["url"] as String
    return if (url.startsWith("/")) HashMap(plan).apply { put("url", origin + url) } else plan
  }
  // Music Match sample rate (23 Oct ruling): this device's own value. Reviewable WIP for Media: the
  // mode is held and reported here; the sink configures its delegate at the stage's chosen rate, but
  // no opened or configured rate is recorded process-wide, so audioProcessingFacts can never answer
  // sampleRateMatched = true.
  fun setSampleRateMatching(mode: String) {
    PorticoAudioSampleRate.mode = PorticoAudioSampleRate.parse(mode)
  }


  fun sampleRateMatching(): String = PorticoAudioSampleRate.mode

  /** The sink's own open facts. The requested rate is read from the presentation the listener is
   * hearing *now* — the audible plan while a gapless edge drains, the session otherwise — so an
   * item advance, boundary switch or retire cannot leave a stale rate behind; 0 without an
   * active owner. The routed device and the opened rate stay unknown until the sink reports
   * them, and no support, match or physical-rate claim is made here. */
  fun audioProcessingFacts(context: android.content.Context): Map<String, Any> {
    val owner = activeOwner
    val rate = if (owner == null) 0 else num(decodePlan(presentation(heard()))?.get("sampleRate"))?.toInt() ?: 0
    return PorticoAudioSampleRate.facts(context, PorticoAudioSampleRate.mode, rate, null, 0)
  }

  private fun renders(presentation: Map<String, Any?>?): Boolean = PorticoAudioPlan.playable(decodePlan(presentation))
  private fun accepts(presentation: Map<String, Any?>?): Boolean {
    val plan = plan(presentation) ?: return false
    return PorticoAudioPlan.validate(mapOf("plan" to plan, "settings" to mapOf("gapless" to true, "crossfadeSeconds" to 0.0, "normalization" to "off"), "epoch" to 0.0, "startSeconds" to 0.0), origin) == null
  }
  private fun renderNotice(presentation: Map<String, Any?>?): String? {
    if (renders(presentation)) return null
    val reason = decodePlan(presentation)?.get("reason") as? String
    return if (!reason.isNullOrEmpty() && reason.length <= 512) reason else "Gapless playback and volume leveling aren’t available for this audio."
  }

  private fun renderCommand(): Map<String, Any?>? {
    val heard = heard()
    val presentation = presentation(heard)
    // Rendering that keeps failing for this session gives way to the plain stream.
    if (noRender == heard["id"]) return null
    if (!renders(presentation) || (!effectsWanted() && !rendering)) return null
    val plan = plan(presentation) ?: return null
    rendering = true
    val command = HashMap<String, Any?>()
    command["plan"] = plan
    command["settings"] = effects()
    command["epoch"] = seekRevision.toDouble()
    command["startSeconds"] = startSeconds(heard)
    command["opaqueBinding"] = true
    val reserved = prepared
    if (reserved != null && committed == null && reserved["recovered"] != true) {
      @Suppress("UNCHECKED_CAST") val next = plan(reserved["presentation"] as? Map<String, Any?>)
      if (next != null) command["prepared"] = mapOf("token" to reserved["token"], "plan" to next, "sameAlbum" to (reserved["sameAlbum"] == true))
    }
    committed?.let { done ->
      @Suppress("UNCHECKED_CAST") val next = plan(presentation(done["session"] as? Map<String, Any?>))
      if (next != null) command["committed"] = mapOf("token" to done["token"], "plan" to next, "sameAlbum" to (done["sameAlbum"] == true))
    }
    return command
  }

  private fun engineState(): Map<String, Any?> {
    val heard = heard()
    val presentation = presentation(heard)
    val info = heard["playbackInfo"] as? Map<*, *>
    val delivered = info?.get("delivered") as? Map<*, *>
    val audio = delivered?.get("audio") as? Map<*, *>
    val url = presentation?.get("url") as? String ?: ""
    val media = if (url.isEmpty()) null else mapOf(
        "id" to heard["id"], "generation" to presentation!!["generation"], "streamUrl" to url, "absolute" to url.startsWith("http"),
        "mode" to (if (presentation["mode"] == "direct") "direct" else "hls"), "duration" to (if (audible != null) audibleDuration else duration), "resumeSeconds" to startSeconds(heard),
        // The plan, for its seek index: a resume in a direct MP3 or ADTS file lands exactly even when the plain stream plays.
        "seekPlan" to plan(presentation),
        // Exact accepted heard-session recipe, used only for renderer selection. Unknown
        // stays unknown; this never asserts the receiver emitted an encoded stream.
        "audioCodec" to (audio?.get("codec") as? String))
    return mapOf("playbackId" to heard["id"], "media" to media, "desired" to mapOf("state" to intent, "rate" to rate, "context" to mapOf("itemId" to heard["itemId"])))
  }

  private fun allowed(): Boolean {
    if (!routeReady || (commitWanted && prepared?.get("recovered") == true)) return false
    val media = !(presentation(heard())?.get("url") as? String).isNullOrEmpty() || renderCommand() != null
    return activeOwner != null && !terminal && !safetyPaused && !ended && media && mono() < leaseUntil
  }

  private fun apply() {
    val owner = activeOwner ?: return
    val playing = allowed() && intent == "playing"
    if (wasPlaying && !playing && pausedAt == 0.0) pausedAt = mono()
    if (!wasPlaying && playing && pausedAt > 0) {
      val elapsed = Math.max(0.0, mono() - pausedAt)
      if (kind() == "audiobook_file" && elapsed >= 300 && nativeSeek == null && audible == null && error == null && controller.listeningSnapshot()["seeking"] != true) {
        val position = num(controller.listeningSnapshot()["positionSeconds"]) ?: 0.0
        val choiceMs = num((policy?.get("audiobook") as? Map<*, *>)?.get("resumeRewindMs")) ?: 0.0
        val amount = PorticoListeningPolicy.resumeDisplacement(elapsed, position, choiceMs)
        if (amount > 0) { dropPrepared(null); nativeSeek = seekTo(position - amount) }
      }
      pausedAt = 0.0
    }
    wasPlaying = playing
    val state = engineState()
    val absolute = (state["media"] as? Map<*, *>)?.get("absolute") == true
    controller.listeningRoute(owner, routeReady, Math.max(0, routeRevision))
    controller.listeningOwner(owner, state, if (absolute) "" else origin, allowed(), if (audible != null) null else nativeSeek, renderCommand())
    onSystemChange?.invoke()
  }

  // --- requests --------------------------------------------------------------------------

  private inner class Exchange(val binary: Boolean = false, val byteLimit: Long = 1L shl 20, val timeoutMs: Long = 0) {
    var completion: ((Int, Any?, Double, Headers?) -> Unit)? = null
    private var call: Call? = null
    private var started = 0.0

    fun start(url: String, token: String, method: String, body: Map<String, Any?>?, headers: Map<String, String>?) {
      started = mono()
      val builder = try { Request.Builder().url(url) } catch (_: Exception) { main.post { finish(0, null, null) }; return }
      builder.header("Authorization", "Bearer $token")
      // This owner reports with observations; the server refuses playback to one that doesn't.
      builder.header("Portico-Playback", "2")
      val type = (headers?.get("Content-Type") ?: "application/json").toMediaType()
      val payload = body?.let { json(it).toByteArray(Charsets.UTF_8).toRequestBody(type) }
      builder.method(method, payload ?: if (method == "POST" || method == "PATCH" || method == "PUT") ByteArray(0).toRequestBody(type) else null)
      headers?.forEach { (name, value) -> if (name != "Content-Type") builder.header(name, value) }
      val client = if (timeoutMs > 0) http.newBuilder().callTimeout(timeoutMs, TimeUnit.MILLISECONDS).build() else http
      val made = client.newCall(builder.build())
      call = made
      made.enqueue(object : Callback {
        override fun onFailure(call: Call, e: IOException) { main.post { finish(0, null, null) } }
        override fun onResponse(call: Call, response: Response) {
          var value: Any? = null
          var status = response.code
          val responseHeaders = response.headers
          try {
            response.use {
              val source = it.body?.source()
              val out = ByteArrayOutputStream()
              val chunk = ByteArray(16384)
              while (source != null) {
                val read = source.read(chunk)
                if (read == -1) break
                out.write(chunk, 0, read)
                if (out.size() > byteLimit) { status = 0; break }
              }
              if (status != 0) value = if (binary) out.toByteArray() else try { parse(out.toByteArray()) } catch (_: Exception) { null }
            }
          } catch (_: IOException) {
            status = 0
          }
          main.post { finish(status, value, responseHeaders) }
        }
      })
    }

    private fun finish(status: Int, value: Any?, headers: Headers?) {
      val complete = completion ?: return
      completion = null
      complete(status, value, started, headers)
    }

    fun cancel() { completion = null; call?.cancel() }
  }

  /** One request at a time; a lost answer is retried with backoff, never duplicated. */
  private fun send(method: String, path: String, body: Map<String, Any?>?, headers: Map<String, String>?, timeoutMs: Long = 0, completion: (Int, Any?, Double, Headers?) -> Unit) {
    val owner = activeOwner ?: return
    if (terminal || !routeReady) return
    val requestToken = token
    val fast = fastRetry
    fastRetry = false
    busy = true
    val exchange = Exchange(timeoutMs = timeoutMs)
    request = exchange
    exchange.completion = completion@{ status, value, started, responseHeaders ->
      if (activeOwner != owner || request !== exchange || terminal) return@completion
      request = null
      busy = false
      if (status == 401 && token != requestToken) { retryAt = mono() + 0.5; return@completion }
      if (status == 401 || (status == 403 && errorCode(value) == "forbidden")) { fail("Playback authorization is no longer valid. Reopen playback after signing in.", true); return@completion }
      if (status == 0 || status >= 500 || status == 429) {
        failures++
        backoff = BACKOFF[Math.min(failures - 1, 4)]
        retryAt = mono() + (if (status == 429) retryAfter(responseHeaders, value) else if (fast) 0.5 else backoff)
        if (mono() < leaseUntil) error = "Reconnecting to the server. Audio keeps playing for now."
        apply(); emit()
        return@completion
      }
      failures = 0
      completion(status, value, started, responseHeaders)
    }
    exchange.start(origin + path, token, method, body, headers)
  }

  private fun sessionPath(suffix: String) = "/v1/playback/sessions/" + Uri.encode(session["id"] as String) + suffix
  private fun queuePath(command: String) = "/v1/queues/" + Uri.encode(queue?.get("id") as? String ?: "") + ":" + command

  /** Events and 404/410 read the same session: reconcile controls, or stop with the server's end. */
  private fun learnEnd() {
    val id = session["id"]
    send("GET", sessionPath(""), null, null) { status, value, _, _ ->
      if (session["id"] != id) return@send
      @Suppress("UNCHECKED_CAST") val answer = value as? Map<String, Any?>
      if (status == 200 && answer?.get("state") != "ended") {
        if (!reconcileSession(answer, patchWanted)) retryAt = mono() + 1
        return@send
      }
      serverEnded(answer)
    }
  }

  private fun serverEnded(answer: Map<String, Any?>?) {
    val ending = answer?.get("end") as? Map<*, *>
    // Ended as completed while a commit's answer was lost: that commit landed. Take its session.
    if (ending?.get("reason") == "completed" && lostCommit != null) { replayLostCommit(); return }
    val reason = ending?.get("reason") as? String
    // The track this owner reported finished: its own doing, and the queue is already moving on.
    if (reason == "ended" && endedReported) return
    val message = (ending?.get("message") as? String)?.takeIf { it.isNotEmpty() }
    end = if (!reason.isNullOrEmpty()) mapOf("reason" to reason, "message" to message) else mapOf("reason" to "stopped", "message" to null)
    fail(message ?: "Playback ended on the server.", true)
  }

  private fun sessionGone(status: Int, value: Any?) = status == 404 || status == 410 || errorCode(value) in GONE

  private fun fail(message: String, terminal: Boolean) {
    error = message
    if (terminal) {
      this.terminal = true; safetyPaused = true; leaseUntil = 0.0
      request?.cancel(); request = null; busy = false
      metadataRequest?.cancel(); metadataRequest = null
      clearArtwork()
      prepared = null; committed = null; commitWanted = false
      actions.clear()
    }
    apply(); emit()
  }

  // --- the loop --------------------------------------------------------------------------

  private fun tick() {
    if (activeOwner == null) return
    checkDeadlines()
    // Audible authority ends 120 s after the last accepted report, whatever the network is doing.
    // Like a pause the listener made: Play (or Retry) resumes once the server answers again.
    if (mono() >= leaseUntil && !terminal && !safetyPaused && intent == "playing") {
      safetyPaused = true
      dropPrepared(null)
      error = "Playback paused: the server hasn’t confirmed it for two minutes. Press Play to try again."
      apply(); emit()
    }
    if (!routeReady || terminal || busy || mono() < retryAt) return
    if (commitWanted) { commit(); return }
    if (lostCommit != null && actions.isNotEmpty()) { replayLostCommit(); return } // before any :advance
    if (checkEnd) { checkEnd = false; learnEnd(); return }
    if (startPending != null) { startPendingEntry(); return }
    if (patchWanted) { patch(); return }
    if (actions.isNotEmpty()) { runAction(); return }
    val reserved = prepared
    if (reserved != null && committed == null && (num(reserved["expiresAtMs"]) ?: 0.0) <= wall() * 1000) { dropPrepared("The next track wasn’t ready in time; it plays normally."); return }
    if (reportNow || mono() >= nextReport) { report(); return }
    maybePrepare()
  }

  /** One observation (P2-21): the latest state, once a second. The first claims the session's
   * epoch (naming JS's, when it handed over); a refused or lost sample is replaced by the next one,
   * never resent. The lease is the one the server last wrote, from the ack. */
  private fun report() {
    val engine = controller.listeningSnapshot()
    val binding = engine["binding"] as? Map<*, *>
    val state: String
    val position: Double
    if (audible != null) { state = "buffering"; position = 0.0 } // the committed successor, before its first sample
    else {
      val bound = binding?.get("sessionId") == session["id"] && num(binding?.get("generation")) == generation(session)
      if (nativeSeek != null || (bound && engine["seeking"] == true)) { nextReport = mono() + 1; reportNow = false; return }
      position = if (bound && engine["observed"] == true) num(engine["positionSeconds"]) ?: 0.0 else startSeconds(session)
      state = if (ended) "ended" else if (!allowed() || intent != "playing") "paused" else if (engine["ready"] == true) "playing" else "buffering"
    }
    if (endedReported && state == "ended") { reportNow = false; nextReport = Double.POSITIVE_INFINITY; return }
    val id = session["id"]
    val now = mono()
    if (playingSince >= 0) { playClock += now - playingSince; playingSince = -1.0 }
    if (state == "playing") playingSince = now
    reportNow = false
    val claimed = epoch
    if (claimed == null) {
      val claim = if (replaces != null) mapOf("generation" to generation(session), "replaces" to replaces) else mapOf("generation" to generation(session))
      send("POST", sessionPath("/observer"), claim, null) { status, value, _, _ ->
        if (session["id"] != id) return@send
        val granted = (value as? Map<*, *>)?.get("epoch") as? String
        if (status in 200..299 && granted != null && wireId(granted)) {
          epoch = granted; replaces = null; observed = 0; claimedAt = mono(); playClock = 0.0
          if (playingSince >= 0) playingSince = claimedAt
          reportNow = true
          return@send
        }
        if (sessionGone(status, value)) { learnEnd(); return@send }
        nextReport = mono() + 1 // another owner still reports (a handover): claim again shortly
      }
      return
    }
    val monotonic = Math.max(0.0, now - claimedAt) * 1000
    val played = Math.min(monotonic, (playClock + (if (playingSince >= 0) now - playingSince else 0.0)) * 1000)
    val next = ++observed
    val body = mapOf("epoch" to claimed, "seq" to next, "generation" to generation(session), "state" to state, "positionMs" to Math.round(Math.max(0.0, position) * 1000),
      "rate" to (if (state == "playing") rate else 0.0), "monotonicMs" to Math.floor(monotonic).toLong(), "playClockMs" to Math.floor(played).toLong(), "seekSerial" to seekRevision)
    send("PUT", sessionPath("/observation"), body, null, OBSERVATION_TIMEOUT_MS) { status, value, _, _ ->
      if (session["id"] != id) return@send
      if (status in 200..299) {
        seq = Math.max(seq, next)
        timestamp((value as? Map<*, *>)?.get("leaseUntil"))?.let { leaseUntil = mono() + (it - wall()) }
        if (error?.startsWith("Reconnecting") == true || error?.startsWith("Playback paused: the server") == true) error = null
        nextReport = mono() + 1
        if (state == "ended") { endedReported = true; nextReport = Double.POSITIVE_INFINITY; completed() }
        emit()
        return@send
      }
      if (errorCode(value) == "observation_epoch_changed") { replaces = epoch; epoch = null; reportNow = true; return@send }
      if (sessionGone(status, value)) { learnEnd(); return@send }
      nextReport = mono() + 1
    }
  }

  /** Pause and resume are the session's desired state (PATCH with If-Match); rate stays local. */
  private fun patch() {
    val want = intent
    val id = session["id"]
    send("PATCH", sessionPath(""), mapOf("state" to want), mapOf("If-Match" to (session["revision"] as String), "Content-Type" to "application/merge-patch+json")) { status, value, _, _ ->
      if (session["id"] != id) return@send
      @Suppress("UNCHECKED_CAST") val answer = value as? Map<String, Any?>
      if (status in 200..299 && reconcileSession(answer, intent != want)) return@send
      if (status == 412) {
        // Adopt the server's current session; apply the intent again only if it still differs.
        val current = currentOf(value)
        if (!reconcileSession(current, true)) patchWanted = false
        return@send
      }
      if (sessionGone(status, value)) { patchWanted = false; learnEnd(); return@send }
      patchWanted = false
      error = "The server didn’t accept that change. Playback continues here."
      emit()
    }
  }

  /** Every accepted server session is assigned before any command can apply its new generation. */
  private fun reconcileSession(value: Map<String, Any?>?, keepLocalIntent: Boolean): Boolean {
    val fresh = value ?: return false
    if (!v1Session(fresh) || fresh["id"] != session["id"] || fresh["itemId"] != session["itemId"]) return false
    if (fresh["state"] == "ended") { patchWanted = false; serverEnded(fresh); return true }
    val clock = controller.listeningSnapshot()
    val binding = clock["binding"] as? Map<*, *>
    val observed = if (audible == null && clock["observed"] == true && binding?.get("sessionId") == session["id"] &&
        num(binding?.get("generation")) == generation(session)) num(clock["positionSeconds"]) else null
    val update = PorticoListeningPolicy.reconcileSession(session, fresh, intent, keepLocalIntent, observed, num(nativeSeek?.get("positionSeconds"))) ?: return false
    session = fresh
    intent = update.intent
    patchWanted = update.patchWanted
    if (update.seekSeconds != null) {
      nativeSeek = seekTo(update.seekSeconds)
      ended = false; endedReported = false; audioAttempt = null; reportNow = true; pausedAt = 0.0
      if (audible != null) {
        // A seek/replan of the committed successor retires the old buffered edge; its lease and
        // report sequence remain the successor's, not a new locally invented session.
        val previous = audible?.get("id")
        transition = mapOf("previousSessionId" to (transition?.get("previousSessionId") ?: previous), "queue" to queueHeader)
        transitionSequence = sequence + 1
        audible = null; committed = null; prepared = null; audioReady = false; commitWanted = false; rendering = false
        metadata = null; metadataRequest?.cancel(); metadataRequest = null; clearArtwork()
        controller.resetListeningEngine(activeOwner ?: return false)
        loadCurrentMetadata()
      }
    }
    if (update.dropPrepared) { reportNow = true; dropPrepared(null) }
    persist(); apply(); emit()
    return true
  }

  // --- queue moves -----------------------------------------------------------------------

  private fun mayAdvance(): Boolean {
    val postPlay = queueHeader?.get("postPlay") as? Map<*, *>
    return PorticoListeningPolicy.mayAdvance(timerStopped, postPlay)
  }

  /** The track ended and the server knows: move on if the viewer's policy allows. */
  private fun completed() {
    checkDeadlines()
    if (queue == null || !mayAdvance()) { safetyPaused = true; apply(); emit(); return }
    val next = queueHeader?.get("next") as? Map<*, *>
    if (next != null && next["available"] != true) { emit(); return } // the end of the queue
    enqueue("completion", null)
  }

  private fun enqueue(reason: String, entryId: String?) {
    if (actions.size >= 16) actions.removeAt(0)
    actions.add(hashMapOf("reason" to reason, "entryId" to entryId, "key" to newId(), "revision" to ++actionRevision, "tries" to 0))
  }

  fun advance(reason: String?, entryId: String?, owner: String): Boolean {
    if (activeOwner != owner || terminal || reason !in ADVANCES || queue == null) return false
    if ((reason == "entry") != v1Id(entryId)) return false
    finishBoundaryTimer()
    resetInactivity()
    // Between commit and boundary the server's queue is already on the successor: Next, Previous
    // and a lock-screen skip mean "that one, now". A chosen entry then moves from there.
    if (committed != null) { promoteCommitted(); if (reason != "entry") return true }
    if (reason == "previous" && audible == null && (num(controller.listeningSnapshot()["positionSeconds"]) ?: 0.0) > 3) return action("seek", 0.0, owner)
    dropPrepared(null)
    safetyPaused = false
    ended = false
    enqueue(reason!!, if (reason == "entry") entryId else null)
    emit(); tick()
    return true
  }

  private fun runAction() {
    val action = actions.first()
    dropPrepared(null) // any queue command cancels a preparation (§18.2)
    val body = HashMap<String, Any?>()
    body["reason"] = action["reason"]
    if (v1Id(action["entryId"])) body["entryId"] = action["entryId"]
    val id = session["id"]
    send("POST", queuePath("advance"), body, mapOf("If-Match" to (queue!!["revision"] as String), "Idempotency-Key" to (action["key"] as String))) { status, value, started, headers ->
      if (actions.firstOrNull() !== action || session["id"] != id) return@send
      if (status == 412) {
        // The queue changed elsewhere (Up Next on another device): take its revision, ask once more.
        val current = currentOf(value)
        // A commit given up while offline did land after all: replay its token before anything else.
        parkedCommit?.let { parked -> lostCommit = parked; parkedCommit = null; replayTries = 0; if (v1Queue(current)) acceptQueue(current!!); return@send }
        actions.removeAt(0)
        if (v1Queue(current) && (action["tries"] as Int) < 1) {
          acceptQueue(current!!)
          actions.add(0, HashMap(action).apply { put("key", newId()); put("tries", 1) })
        } else { error = "The queue changed. Open it to choose what plays next."; emit() }
        return@send
      }
      if (status == 503) { retryAt = mono() + retryAfter(headers, value); return@send } // queue_building: the same request again
      actions.removeAt(0)
      if (status == 409 && errorCode(value) == "queue_ended") { safetyPaused = true; apply(); emit(); return@send }
      @Suppress("UNCHECKED_CAST") val result = value as? Map<String, Any?>
      @Suppress("UNCHECKED_CAST") val moved = (result?.get("queue") as? Map<String, Any?>) ?: result
      if (status !in 200..299 || !v1Queue(moved)) {
        error = if (sessionGone(status, value)) "The queue is no longer available." else "The queue couldn’t move on. Try again."
        emit()
        return@send
      }
      acceptQueue(moved!!)
      @Suppress("UNCHECKED_CAST") val started2 = result?.get("session") as? Map<String, Any?>
      if (v1Session(started2)) { moveTo(started2!!, started, moved, action["reason"] == "completion"); return@send }
      val current = moved["current"] as? Map<*, *>
      val entry = current?.get("entryId")
      if (v1Id(entry)) startEntry(entry as String, moved, action["reason"] == "completion") else { safetyPaused = true; apply(); emit() }
    }
  }

  /** An advance that didn't start the next session itself: the owner starts it, and the loop retries until the server answers. */
  private fun startEntry(entryId: String, moved: Map<String, Any?>?, automatic: Boolean = false) {
    startPending = mapOf("key" to newId(), "previous" to session["id"], "queue" to moved, "automatic" to automatic,
        "body" to mapOf("queue" to mapOf("queueId" to queue!!["id"], "entryId" to entryId), "state" to intent, "startFrom" to "resume", "replacesSessionId" to session["id"]))
    tick()
  }

  private fun startPendingEntry() {
    val pending = startPending ?: return
    @Suppress("UNCHECKED_CAST") val body = pending["body"] as Map<String, Any?>
    send("POST", "/v1/playback/sessions", body, mapOf("Idempotency-Key" to (pending["key"] as String))) { status, value, started, _ ->
      if (startPending !== pending) return@send
      if (status == 202) { // preparing: the same request again
        val wait = (num((value as? Map<*, *>)?.get("retryAfterMs")) ?: 0.0) / 1000.0
        retryAt = mono() + Math.min(10.0, Math.max(0.25, if (wait > 0) wait else 1.0))
        return@send
      }
      startPending = null
      @Suppress("UNCHECKED_CAST") val answer = value as? Map<String, Any?>
      @Suppress("UNCHECKED_CAST") val moved = pending["queue"] as? Map<String, Any?>
      if ((status == 200 || status == 201) && v1Session(answer)) { moveTo(answer!!, started, moved, pending["automatic"] == true); return@send }
      fail("The next item couldn’t start. Open the queue to choose it again.", false)
    }
  }

  private fun acceptQueue(next: Map<String, Any?>) {
    val held = queue ?: return
    if (next["id"] != held["id"]) return
    held["revision"] = next["revision"]
    queueHeader = next
  }

  /** The owner moved to another session (an advance, a start): report it from now on. */
  private fun moveTo(next: Map<String, Any?>, started: Double, moved: Map<String, Any?>?, automatic: Boolean = false) {
    carryTimer(next, automatic)
    val previous = (audible?.get("id") ?: session["id"]) as? String
    transition = mapOf("previousSessionId" to (transition?.get("previousSessionId") ?: previous), "queue" to moved)
    transitionSequence = sequence + 1
    session = next; audible = null; seq = 0; epoch = null; replaces = null; leaseUntil = started + 120; nextReport = 0.0; reportNow = true; ended = false; endedReported = false; error = null; notice = null
    prepared = null; committed = null; commitWanted = false; audioReady = false; renderFailures = 0; startPending = null; parkedCommit = null; replayTries = 0
    pausedAt = 0.0; wasPlaying = false
    intent = if (timerStopped || next["state"] == "paused") "paused" else "playing"
    safetyPaused = false; engineFailed = false; audioAttempt = null; rendering = false; policyRateKey = null
    duration = PorticoAudioPlan.durationSeconds(decodePlan(presentation(next)))
    nativeSeek = seekTo(startSeconds(next))
    lastFact = null; metadata = null
    metadataRequest?.cancel(); metadataRequest = null
    clearArtwork()
    applyPreferredRate(); persist(); apply(); emit(); loadCurrentMetadata()
  }

  // --- gapless and crossfade (§18.2, §18.3) ----------------------------------------------

  private fun maybePrepare() {
    val engine = controller.listeningSnapshot()
    if (!transitions || queue == null || prepared != null || audible != null || committed != null || !rendering || !edgesWanted() || intent != "playing" || !allowed() || !mayAdvance() ||
        engine["ready"] != true || engine["observed"] != true || engine["seeking"] == true || nativeSeek != null || audioAttempt == session["id"] || mono() < audioCooldown) return
    if (!renders(presentation(session))) return
    val next = queueHeader?.get("next") as? Map<*, *>
    if (next != null && next["available"] != true) return
    val remaining = duration - (num(engine["positionSeconds"]) ?: 0.0)
    if (duration <= 0 || remaining <= 0 || remaining > Math.min(40.0, 30 * rate) || !timerAllowsEdge(remaining / Math.max(0.25, rate))) return
    audioAttempt = session["id"] as String // one try per track
    val id = session["id"]
    val timerPolicy = policy?.get("revision")
    send("POST", queuePath("prepare-next"), mapOf("sessionId" to id, "sessionGeneration" to generation(session)), mapOf("If-Match" to (queue!!["revision"] as String), "Idempotency-Key" to newId())) { status, value, _, _ ->
      if (session["id"] != id || actions.isNotEmpty() || audioAttempt == null || timerPolicy != policy?.get("revision")) return@send
      @Suppress("UNCHECKED_CAST") val answer = value as? Map<String, Any?>
      @Suppress("UNCHECKED_CAST") val presentation = answer?.get("presentation") as? Map<String, Any?>
      val expires = timestamp(answer?.get("expiresAt"))
      if (status != 200 || !wireToken(answer?.get("token")) || !v1Id(answer?.get("entryId")) || !v1Id(answer?.get("itemId")) || expires == null || expires <= wall() || presentation == null || !accepts(presentation)) {
        if (status == 412) { val current = currentOf(value); if (v1Queue(current)) acceptQueue(current!!) }
        return@send // no edge for this track: it ends and advances as usual
      }
      if (intent != "playing" || !mayAdvance()) return@send
      prepared = mapOf("token" to answer!!["token"], "expiresAtMs" to expires * 1000, "entryId" to answer["entryId"], "itemId" to answer["itemId"], "presentation" to presentation)
      audioReady = false
      apply(); emit()
      learnAlbum(answer["itemId"] as String, answer["token"] as String)
    }
  }

  /** Whether the next track follows this one on the same album: such tracks are joined, never crossfaded. */
  private fun learnAlbum(itemId: String, token: String) {
    val album = metadata?.get("albumId") as? String
    if (album.isNullOrEmpty() || !v1Id(itemId)) return
    val disc = (num(metadata?.get("discNumber")) ?: 0.0).toInt()
    val track = (num(metadata?.get("trackNumber")) ?: 0.0).toInt()
    val owner = activeOwner
    albumFor = token
    val exchange = Exchange()
    exchange.completion = completion@{ status, value, _, _ ->
      if (albumFor == token) albumFor = null
      val reserved = prepared
      if (activeOwner != owner || reserved?.get("token") != token) return@completion
      val song = (value as? Map<*, *>)?.get("song") as? Map<*, *>
      if (status == 200 && consecutiveOnAlbum(album, disc, track, song?.get("albumId") as? String, (num(song?.get("discNumber")) ?: 0.0).toInt(), (num(song?.get("trackNumber")) ?: 0.0).toInt())) {
        prepared = HashMap(reserved).apply { put("sameAlbum", true) }
        apply()
      }
      if (commitWanted) tick() // a commit waiting for this answer goes now
    }
    exchange.start(origin + "/v1/items/" + Uri.encode(itemId), this.token, "GET", null, null)
  }

  /** A local cancellation (pause, seek, rate, any queue command): the reserved track goes, no commit. */
  private fun dropPrepared(message: String?) {
    if (prepared == null || committed != null) return
    prepared = null; audioReady = false; commitWanted = false; audioCooldown = mono() + 3
    if (message != null) notice = message
    persist(); apply()
  }

  private fun commit() {
    val reserved = prepared
    if (reserved == null) { commitWanted = false; return }
    val id = session["id"]
    val recovered = reserved["recovered"] == true
    // A commit that can't be confirmed before the current track runs out is given up at once: the
    // track then ends and advances as usual, rather than leaving silence while the network recovers.
    val remaining = duration - (num(controller.listeningSnapshot()["positionSeconds"]) ?: 0.0)
    if (!recovered && !timerAllowsEdge(Math.max(0.0, remaining) / Math.max(0.25, rate))) { commitWanted = false; dropPrepared(null); return }
    if (!recovered && ((num(reserved["expiresAtMs"]) ?: 0.0) <= wall() * 1000 || ++commitTries > 4 || (commitTries > 1 && remaining < 1.0))) {
      // No answer came: the commit may have landed. Keep its token; the track ends as usual, and
      // the token is replayed before any :advance (or when the old session reads as completed).
      if (commitTries > 1) lostCommit = mapOf("token" to reserved["token"], "queueId" to (queue?.get("id") ?: ""))
      commitWanted = false
      dropPrepared("The next track plays normally.")
      return
    }
    // Whether the edge is gapless (same album) or a crossfade is decided when it's committed, so
    // the commit waits for the album answer while there's time; after that it crossfades.
    if (!recovered && albumFor == reserved["token"] && remaining > 1.5) { commitTries--; return }
    if (recovered && ++commitTries > 4) { commitWanted = false; prepared = null; safetyPaused = false; persist(); apply(); emit(); return }
    if (!persist()) return // the token is saved before it is sent
    fastRetry = true // a lost answer is asked again quickly: the commit is idempotent by token
    send("POST", queuePath("commit-next"), mapOf("token" to reserved["token"]), null) { status, value, started, _ ->
      if (prepared !== reserved || session["id"] != id) return@send
      val answer = value as? Map<*, *>
      @Suppress("UNCHECKED_CAST") val moved = answer?.get("queue") as? Map<String, Any?>
      @Suppress("UNCHECKED_CAST") val next = answer?.get("session") as? Map<String, Any?>
      if (status == 200 && v1Queue(moved) && v1Session(next) && (recovered || accepts(presentation(next)))) {
        commitWanted = false
        acceptQueue(moved!!)
        if (recovered) { prepared = null; safetyPaused = true; moveTo(next!!, started, moved); return@send }
        // The server moved the queue: the new session reports from now on, the old track plays out
        // to the boundary. The old grant fetches nothing more.
        committed = mapOf("token" to reserved["token"], "session" to next, "sameAlbum" to (reserved["sameAlbum"] == true))
        audible = session; audibleSeq = seq; audibleDuration = duration
        session = next!!; seq = 0; epoch = null; replaces = null; leaseUntil = started + 120; reportNow = true; endedReported = false
        duration = PorticoAudioPlan.durationSeconds(decodePlan(presentation(next)))
        persist(); apply(); emit()
        return@send
      }
      // 410 prepared_expired, 409 prepared_canceled, 404: finish this track, then advance as usual.
      commitWanted = false; lostCommit = null
      dropPrepared(if (errorCode(value) == "prepared_expired") "The next track wasn’t ready in time; it plays normally." else "The next track plays normally.")
      if (recovered) { safetyPaused = false; apply() }
    }
  }

  /** Replays a commit whose answer was lost (idempotent by token): a session means it landed, and
   * that session plays from its start; a refusal means it didn't, and the queue moves as usual. */
  private fun replayLostCommit() {
    val lost = lostCommit ?: return
    val id = session["id"]
    // While the network is down a replay can't answer either: after six quick tries the queue moves
    // as usual. If the commit had landed, that :advance meets a 412 and the token is replayed then.
    if (++replayTries > 6) { lostCommit = null; replayTries = 0; parkedCommit = lost; persist(); tick(); return }
    fastRetry = true
    send("POST", queuePath("commit-next"), mapOf("token" to lost["token"]), null) { status, value, started, _ ->
      if (lostCommit !== lost || session["id"] != id) return@send
      lostCommit = null; replayTries = 0; parkedCommit = null
      val answer = value as? Map<*, *>
      @Suppress("UNCHECKED_CAST") val moved = answer?.get("queue") as? Map<String, Any?>
      @Suppress("UNCHECKED_CAST") val next = answer?.get("session") as? Map<String, Any?>
      if (status == 200 && v1Queue(moved) && v1Session(next)) { actions.clear(); acceptQueue(moved!!); safetyPaused = false; moveTo(next!!, started, moved); return@send }
      persist(); tick() // it didn't land: the pending :advance (or the end) proceeds
    }
  }

  /** The committed successor plays now, from its start, as an ordinary start: a skip while the old
   * track plays out, or the successor failing or ending before its boundary. */
  private fun promoteCommitted() {
    @Suppress("UNCHECKED_CAST") val next = committed?.get("session") as? Map<String, Any?>
    if (!v1Session(next)) return
    controller.resetListeningEngine(activeOwner ?: return)
    moveTo(next!!, mono(), queueHeader, true)
  }

  private fun boundary(fact: Map<String, Any?>) {
    val done = committed ?: return
    if (done["token"] != fact["token"]) return
    carryTimer(session, true)
    val previous = (audible?.get("id") ?: session["id"]) as? String
    transition = mapOf("previousSessionId" to (transition?.get("previousSessionId") ?: previous), "queue" to queueHeader)
    transitionSequence = sequence + 1
    audible = null; committed = null; prepared = null; audioReady = false; audioAttempt = null; lastFact = fact; nativeSeek = null; ended = false; engineFailed = false; reportNow = true; policyRateKey = null; renderFailures = 0
    metadata = null
    metadataRequest?.cancel(); metadataRequest = null
    clearArtwork()
    // Rebind without a seek: the engine already plays the next track.
    applyPreferredRate(); persist(); apply(); emit(); loadCurrentMetadata()
  }

  // --- engine facts ----------------------------------------------------------------------

  fun fact(fact: Map<String, Any?>) {
    val heard = heard()
    val binding = fact["binding"] as? Map<*, *>
    if (activeOwner == null || terminal || binding?.get("sessionId") != heard["id"] || num(binding?.get("generation")) != generation(heard)) return
    val type = fact["type"] as? String ?: return
    lastFact = fact
    if (type == "ready" && controller.listeningSnapshot()["ready"] == true) engineFailed = false
    if (type == "audioPrepared" && prepared?.get("token") == fact["token"]) audioReady = true
    if (type == "audioCommit" && audioReady && committed == null && prepared?.get("token") == fact["token"]) {
      if (mayAdvance() && intent == "playing" && allowed()) { commitWanted = true; commitTries = 0; tick() } else dropPrepared(null)
    }
    if (type == "audioBoundary") { boundary(fact); return }
    // The successor failed, or the old track ended with no boundary: start it as an ordinary track.
    if (committed != null && type in PROMOTING) { if(type == "ended") ended = true; checkDeadlines(); promoteCommitted(); return }
    if (type == "audioPreparationFailed") dropPrepared("The next track couldn’t be buffered; it plays normally.")
    if (type == "seekApplied" && num(fact["seekRevision"]) == num(nativeSeek?.get("revision"))) { nativeSeek = null; reportNow = true; apply() }
    if (type == "seekFailed") { nativeSeek = null; safetyPaused = true; error = "That position isn’t available. Seek again or press Retry."; dropPrepared(null); apply() }
    if (type == "ended" && !ended && committed == null) { ended = true; reportNow = true; checkDeadlines() }
    if (type == "error" && rendering && fact["networkError"] != true && ++renderFailures >= 1 && noRender != heard["id"]) {
      // The plan's file keeps failing: play this session's ordinary stream from here, and say so quietly.
      val position = num(controller.listeningSnapshot()["positionSeconds"]) ?: 0.0
      noRender = heard["id"] as? String
      rendering = false
      dropPrepared(null)
      controller.resetListeningEngine(activeOwner!!)
      nativeSeek = seekTo(position)
      notice = "Gapless playback isn’t available for this track right now; it plays normally."
      reportNow = true
      apply(); emit()
      return
    }
    if (type == "error") { engineFailed = true; safetyPaused = true; error = "This audio couldn’t be played. Press Retry."; reportNow = true; dropPrepared(null); apply() }
    if (type == "progress") {
      checkDeadlines()
      // While paused nothing moves: no snapshot per engine tick.
      if (!(intent == "playing" && allowed())) return
    }
    emit()
  }

  /** Audio focus: a moment's loss is the engine's own to wait out; a lasting one is a pause the listener can undo. */
  private fun interruption(change: String) {
    if (activeOwner == null || terminal) return
    if (change == "lost") { dropPrepared(null); intent = "paused"; patchWanted = true; reportNow = true; apply(); persist() }
    else if (change == "began") dropPrepared(null)
    checkDeadlines()
    emit(); tick()
  }

  // --- intents ---------------------------------------------------------------------------

  /** JS commands carry the original output, not just this long-lived owner token. */
  fun actionFor(expected: Map<String, Any?>?, action: String?, value: Double?, owner: String): Boolean {
    if (activeOwner != owner || terminal || expected == null || (committed != null || audible != null) && action != "pause") return false
    val heard = heard()
    val actual = controller.listeningSnapshot()
    val binding = actual["binding"] as? Map<*, *> ?: return false
    val wanted = expected["binding"] as? Map<*, *> ?: return false
    if (!v1Id(wanted["lease"]) || !whole(wanted["intentId"], 1.0, MAX_INTEGER) || !v1Id(wanted["sessionId"]) ||
        !whole(wanted["generation"], 0.0, MAX_INTEGER) || !v1Id(wanted["itemId"]) || !whole(expected["commandRevision"], 0.0, MAX_INTEGER)) return false
    if (wanted["lease"] != binding["lease"] || wanted["sessionId"] != heard["id"] || wanted["itemId"] != heard["itemId"] ||
        wanted["sessionId"] != binding["sessionId"] || wanted["itemId"] != binding["itemId"] ||
        num(wanted["generation"]) != generation(heard) || num(wanted["generation"]) != num(binding["generation"]) ||
        num(wanted["intentId"]) != num(binding["intentId"]) || num(expected["commandRevision"]) != num(actual["commandRevision"])) return false
    val queue = heard["queue"] as? Map<*, *>
    val target = expected["queue"] as? Map<*, *>
    if (queue == null) { if (expected["queue"] != null) return false }
    else if (target == null || !v1Id(target["queueId"]) || !v1Id(target["entryId"]) || target["queueId"] != queue["queueId"] || target["entryId"] != queue["entryId"]) return false
    return action(action, value, owner)
  }

  fun action(action: String?, value: Double?, owner: String): Boolean {
    if (activeOwner != owner || terminal || action !in ACTIONS) return false
    if ((action == "seek" || action == "rate") && (value == null || !value.isFinite() || value < 0 || value > 1e9)) return false
    if (action == "rate" && !PorticoListeningPolicy.rateAllowed(kind() == "audiobook_file", value, BOOK_RATES)) return false
    if (action == "retry" && value != null && (!value.isFinite() || value < 0 || value > 1e9)) return false
    if (action == "next" || action == "previous") return advance(action, null, owner)
    checkDeadlines(); resetInactivity()
    var resolved = action!!
    var amount = value
    if (resolved == "resume" && PorticoListeningPolicy.resumeNeedsRetry(ended, engineFailed)) { resolved = "retry"; amount = if (ended) 0.0 else null }
    if (resolved == "pause") { if (intent == "playing") pausedAt = mono(); dropPrepared(null); intent = "paused"; patchWanted = true; reportNow = true; apply() }
    if (resolved == "resume") { intent = "playing"; safetyPaused = false; timerStopped = false; error = null; patchWanted = true; reportNow = true; apply() }
    if (resolved == "seek") pausedAt = 0.0
    if (resolved == "seek" && committed != null) { promoteCommitted(); persist(); emit(); return !terminal }
    if (resolved == "seek" && audible == null) {
      val seconds = Math.max(0.0, if (duration > 0) Math.min(amount!!, duration) else amount!!)
      dropPrepared(null); audioAttempt = null; ended = false; endedReported = false
      nativeSeek = seekTo(seconds)
      apply()
    }
    if (resolved == "rate") { rate = amount!!; if (committed == null) dropPrepared(null); reportNow = true; apply() }
    if (resolved == "retry") {
      val seconds = amount ?: num(controller.listeningSnapshot()["positionSeconds"]) ?: 0.0
      dropPrepared(null); intent = "playing"; safetyPaused = false; engineFailed = false; timerStopped = false; ended = false; endedReported = false; error = null; patchWanted = true; reportNow = true; renderFailures = 0
      controller.resetListeningEngine(owner)
      nativeSeek = seekTo(seconds)
      apply()
    }
    persist(); emit(); tick()
    return !terminal
  }

  /** The event stream says this session changed: reconcile its controls or end at once. */
  fun sessionUpdated(sessionId: String, owner: String): Boolean {
    if (activeOwner != owner || terminal) return false
    // An end this owner reported itself needs no check; anything else is read at once.
    if ((sessionId == session["id"] && !endedReported) || sessionId == audible?.get("id")) { checkEnd = true; tick() }
    return true
  }

  fun credentials(token: String?, owner: String): Boolean {
    if (activeOwner != owner || terminal || token.isNullOrEmpty() || token.contains('\n') || token.contains('\r')) return false
    this.token = token
    return true
  }

  fun route(route: Map<String, Any?>?, owner: String): Boolean {
    if (activeOwner != owner || route == null || route["ready"] !is Boolean || !whole(route["revision"], 0.0, MAX_INTEGER)) return false
    val revision = num(route["revision"])!!.toLong()
    val ready = route["ready"] as Boolean
    if (revision < routeRevision) return true
    val next = if (ready) route["origin"] as? String else origin
    if (ready && (next == null || originOf(next) == null)) return false
    if (revision == routeRevision) return ready == routeReady && (!ready || origin == next)
    val changed = ready && origin != next
    val clock = controller.listeningSnapshot()
    val position = num(clock["positionSeconds"])
    routeRevision = revision
    routeReady = ready
    if (!ready || changed) {
      request?.cancel(); request = null; busy = false
      metadataRequest?.cancel(); metadataRequest = null
      clearArtwork()
      dropPrepared(null)
      if (clock["observed"] == true && position != null && position >= 0 && audible == null) nativeSeek = seekTo(position)
      reportNow = true
      retryAt = 0.0
    }
    if (ready) origin = next!!
    apply(); emit()
    return true
  }

  // --- policy and timers -----------------------------------------------------------------

  private fun validPolicy(value: Map<String, Any?>): Boolean {
    for (key in listOf("revision", "activityRevision", "timerRevision")) if (!whole(value[key], 0.0, MAX_INTEGER)) return false
    if (!whole(value["activityAtMs"], 1.0, MAX_INTEGER)) return false
    val effects = value["effects"] as? Map<*, *> ?: return false
    if (effects["gapless"] !is Boolean || !inRange(effects["crossfadeSeconds"], 0.0, 12.0) || effects["normalization"] !in NORMALIZATION) return false
    if (!PorticoListeningPolicy.validPreferences(value["preferences"], BOOK_RATES, PASSOUT)) return false
    if (value.containsKey("audiobook") && !PorticoListeningPolicy.validAudiobookPreferences(value["audiobook"])) return false
    val timer = value["timer"]
    if (timer != null) {
      val t = timer as? Map<*, *> ?: return false
      val label = t["label"] as? String
      if (label.isNullOrEmpty() || label.length > 512 || !wireId(t["libraryId"])) return false
      val choice = t["choice"]
      if (choice is Number) { if (num(choice) !in SLEEP_MINUTES || !whole(t["wallDeadline"], 1.0, MAX_INTEGER)) return false }
      else if (choice in TIMER_CHOICES) {
        if (!v1Id(t["itemId"]) || !validTimerOccurrence(t["occurrence"])) return false
        if (choice == "book") {
          val boundary = t["bookBoundary"] as? Map<*, *> ?: return false
          val revision = boundary["revision"] as? Map<*, *> ?: return false
          val parts = boundary["items"] as? List<*> ?: return false
          if (!whole(revision["catalog"],0.0,MAX_INTEGER) || !whole(revision["viewer"],0.0,MAX_INTEGER) || parts.isEmpty() || parts.size > 1000 || parts.any { !v1Id(it) } || parts.toSet().size != parts.size || parts.last() != t["itemId"]) return false
        }
        if (choice == "chapter" && (!v1Id(t["sessionId"]) || !whole(t["generation"], 0.0, MAX_INTEGER) || !inRange(t["endSeconds"], 0.0, 1e9))) return false
        if (choice == "book" && !v1Id(t["bookId"])) return false
      } else return false
    }
    return true
  }

  private fun installDeadline(key: String, value: Double?) {
    if (deadlines[key] == value) return
    deadlines[key] = value
    if (value == null) deadlineMono.remove(key) else deadlineMono[key] = mono() + Math.max(0.0, value / 1000.0 - wall())
  }

  private fun passoutMinutes(from: Map<String, Any?>?): Int = (num((from?.get("preferences") as? Map<*, *>)?.get("passoutMinutes")) ?: 0.0).toInt()

  private fun resetInactivity() {
    activityWall = wall() * 1000
    val minutes = passoutMinutes(policy)
    installDeadline("passoutAtMs", if (minutes > 0) Math.floor(activityWall + minutes * 60000.0) else null)
  }

  private fun applyPreferredRate() {
    if (activeOwner == null || terminal) return
    val book = kind() == "audiobook_file"
    skipIntervals(PorticoListeningPolicy.skipSeconds(book, policy?.get("audiobook"), false), PorticoListeningPolicy.skipSeconds(book, policy?.get("audiobook"), true))
    val wanted = PorticoListeningPolicy.preferredRate(kind() == "audiobook_file", policy?.get("preferences")) ?: return
    val key = "${session["id"]}:$wanted"
    if (policyRateKey == key || wanted !in (if (kind() == "audiobook_file") BOOK_RATES else RATES)) return
    policyRateKey = key
    if (Math.abs(rate - wanted) > 0.00001) { rate = wanted; reportNow = true }
  }

  /** Music or a book: the item's kind from its metadata, else music. */
  private fun kind(): String = if (metadata?.get("kind") == "audiobook_file") "audiobook_file" else "song"

  private fun validTimerOccurrence(value: Any?): Boolean {
    val o = value as? Map<*, *> ?: return false
    if (!v1Id(o["sessionId"]) || !v1Id(o["itemId"]) || !whole(o["generation"],0.0,MAX_INTEGER) || !whole(o["playbackRun"],0.0,MAX_INTEGER) || o["owner"] != null && !v1Id(o["owner"])) return false
    val q = o["queue"]
    return q == null || q is Map<*, *> && v1Id(q["queueId"]) && v1Id(q["entryId"])
  }

  private fun matchesTimer(o: Map<*, *>?, media: Map<String, Any?> = heard()): Boolean {
    if (o == null || playbackRun == null || num(o["playbackRun"]) != playbackRun || o["sessionId"] != media["id"] || o["itemId"] != media["itemId"] || num(o["generation"]) != generation(media) || o["owner"] != null && o["owner"] != activeOwner) return false
    val target = o["queue"] as? Map<*, *>
    val actual = media["queue"] as? Map<*, *>
    return if(actual == null) target == null else target != null && target["queueId"] == actual["queueId"] && target["entryId"] == actual["entryId"]
  }

  private fun finishBoundaryTimer() { if ((activeTimer()?.get("choice")) in TIMER_CHOICES) finishPolicyTimer() }

  private fun carryTimer(next: Map<String, Any?>, automatic: Boolean) {
    val timer = activeTimer() ?: return
    if (timer["choice"] !in TIMER_CHOICES) return
    val before = heard()
    val oldQueue = before["queue"] as? Map<*, *>
    val nextQueue = next["queue"] as? Map<*, *>
    val parts = (timer["bookBoundary"] as? Map<*, *>)?.get("items") as? List<*>
    val index = parts?.indexOf(before["itemId"]) ?: -1
    if(timer["choice"] == "book" && automatic && matchesTimer(timerCustody) && oldQueue != null && nextQueue != null && oldQueue["queueId"] == nextQueue["queueId"] && oldQueue["entryId"] != nextQueue["entryId"] && index >= 0 && parts?.getOrNull(index+1) == next["itemId"]) {
      timerCustody = HashMap(timerCustody!!).apply {put("sessionId",next["id"]);put("generation",generation(next));put("itemId",next["itemId"]);put("queue",HashMap(nextQueue))}
    } else finishPolicyTimer()
  }

  fun policy(value: Map<String, Any?>?, owner: String): Boolean {
    if (activeOwner != owner || terminal || value == null || !validPolicy(value)) return false
    val previous = policy
    if (previous != null && num(value["revision"])!! < num(previous["revision"])!!) return true
    if (previous != null && num(value["revision"]) == num(previous["revision"])) return previous == value
    val newTimer = previous == null || num(previous["timerRevision"]) != num(value["timerRevision"])
    val timer = value["timer"] as? Map<*, *>
    if (!newTimer && previous?.get("timer") != value["timer"] && !(value["timer"] == null && num(value["timerRevision"])?.toLong() == expiredTimerRevision)) return false
    if (newTimer && timer != null && timer["choice"] in TIMER_CHOICES) {
      val occurrence = timer["occurrence"] as? Map<*, *> ?: return false
      if (!matchesTimer(occurrence) || ended || actions.isNotEmpty() || startPending != null || committed != null || audible != null || commitWanted || lostCommit != null || parkedCommit != null) return false
      val binding = controller.listeningSnapshot()["binding"] as? Map<*, *> ?: return false
      if(binding["sessionId"] != occurrence["sessionId"] || binding["itemId"] != occurrence["itemId"] || num(binding["generation"]) != num(occurrence["generation"])) return false
      val bookParts = (timer["bookBoundary"] as? Map<*, *>)?.get("items") as? List<*>
      if (timer["choice"] == "book" && bookParts?.contains(heard()["itemId"]) != true) return false
      if (timer["choice"] != "book" && timer["itemId"] != occurrence["itemId"]) return false
      if (timer["choice"] == "chapter" && (timer["sessionId"] != occurrence["sessionId"] || num(timer["generation"]) != num(occurrence["generation"]))) return false
      if (timer["choice"] == "book" && metadata?.containsKey("bookId") == true && metadata?.get("bookId") != timer["bookId"]) return false
      @Suppress("UNCHECKED_CAST")
      timerCustody = HashMap(occurrence as Map<String, Any?>)
    } else if(newTimer) timerCustody = null
    policy = value
    val activity = previous == null || num(previous["activityRevision"]) != num(value["activityRevision"])
    if (activity) activityWall = num(value["activityAtMs"])!!
    val minutes = passoutMinutes(value)
    if (activity || minutes != passoutMinutes(previous)) installDeadline("passoutAtMs", if (minutes > 0) Math.floor(activityWall + minutes * 60000.0) else null)
    if ((previous == null || num(previous["timerRevision"]) != num(value["timerRevision"])) && num(value["timerRevision"])!!.toLong() != expiredTimerRevision) {
      installDeadline("sleepAtMs", num((value["timer"] as? Map<*, *>)?.get("wallDeadline")))
    }
    if (newTimer && timer?.get("choice") in TIMER_CHOICES || previous != null && (previous["effects"] != value["effects"] || !mayAdvance())) dropPrepared(null)
    if (previous?.get("effects") != value["effects"] && !rendering && effectsWanted() && renders(presentation(heard())) && lastFact != null && audible == null) {
      // The plan starts mid-track: from where the listener is.
      nativeSeek = seekTo(num(controller.listeningSnapshot()["positionSeconds"]) ?: 0.0)
    }
    applyPreferredRate(); checkDeadlines(); if(!persist()) {apply();emit();return false}; apply(); emit()
    return !terminal
  }

  private fun activeTimer(): Map<*, *>? {
    if (num(policy?.get("timerRevision"))?.toLong() == expiredTimerRevision) return null
    val timer = policy?.get("timer") as? Map<*, *> ?: return null
    if(timer["choice"] in TIMER_CHOICES && !matchesTimer(timerCustody)) {finishPolicyTimer();return null}
    return timer
  }

  private fun timerAllowsEdge(remaining: Double): Boolean {
    val timer = activeTimer()
    val choice = timer?.get("choice")
    if (choice == "chapter" || ((choice == "item" || choice == "book") && timer?.get("itemId") == heard()["itemId"])) return false
    for (field in DEADLINES) {
      val at = num(deadlines[field]) ?: continue
      if (at <= wall() * 1000 + remaining * 1000 || (deadlineMono[field] ?: Double.POSITIVE_INFINITY) <= mono() + remaining) return false
    }
    return true
  }

  private fun finishPolicyTimer() { timerCustody = null; expiredTimerRevision = num(policy?.get("timerRevision"))?.toLong() ?: 0; installDeadline("sleepAtMs", null) }

  private fun expireListening() {
    if (timerStopped) return
    timerStopped = true
    finishPolicyTimer()
    installDeadline("passoutAtMs", null)
    dropPrepared(null)
    intent = "paused"; patchWanted = true; reportNow = true
    persist(); apply(); emit()
  }

  private fun checkDeadlines() {
    if (activeOwner == null || timerStopped) return
    var expired = false
    for (key in DEADLINES) {
      val at = num(deadlines[key]) ?: continue
      if (at > 0 && (wall() * 1000 >= at || mono() >= (deadlineMono[key] ?: Double.POSITIVE_INFINITY))) expired = true
    }
    val timer = activeTimer()
    val heard = heard()
    val engine = controller.listeningSnapshot()
    if (timer?.get("choice") == "chapter") {
      if (timer["sessionId"] != heard["id"] || num(timer["generation"]) != generation(heard)) finishPolicyTimer()
      else if ((engine["binding"] as? Map<*, *>)?.get("sessionId") == heard["id"] && num((engine["binding"] as? Map<*, *>)?.get("generation")) == generation(heard) && engine["seeking"] != true && engine["observed"] == true && (num(engine["positionSeconds"]) ?: 0.0) >= (num(timer["endSeconds"]) ?: Double.POSITIVE_INFINITY)) expired = true
    }
    val choice = timer?.get("choice")
    if (ended && (choice == "item" || choice == "book") && timer?.get("itemId") == heard["itemId"]) expired = true
    if (expired) expireListening()
  }

  fun deadlines(value: Map<String, Any?>?, owner: String): Boolean {
    if (activeOwner != owner || terminal || value == null) return false
    for (key in DEADLINES) if (value[key] != null && !whole(value[key], 1.0, MAX_INTEGER)) return false
    for (key in DEADLINES) installDeadline(key, num(value[key]))
    if (!persist()) return false
    checkDeadlines(); emit()
    return true
  }

  // --- snapshot --------------------------------------------------------------------------

  fun snapshot(owner: String?): Map<String, Any?> {
    if (activeOwner == null || activeOwner != owner) return emptyMap()
    checkDeadlines()
    return makeSnapshot()
  }

  private fun makeSnapshot(): Map<String, Any?> {
    val snapshot = controller.listeningSnapshot()
    val heard = heard()
    val binding = snapshot["binding"] as? Map<*, *>
    if (binding?.get("sessionId") != heard["id"] || num(binding?.get("generation")) != generation(heard) ||
        binding?.get("itemId") != heard["itemId"]) {
      snapshot["binding"] = null; snapshot["ready"] = false; snapshot["observed"] = false
    } else snapshot["binding"] = mapOf("lease" to binding?.get("lease"), "intentId" to binding?.get("intentId"), "sessionId" to heard["id"], "generation" to generation(heard), "itemId" to heard["itemId"])
    if (snapshot["binding"] == null || !allowed() || intent != "playing") {
      snapshot.remove("levelingApplied"); snapshot.remove("levelingGainDb")
    }
    snapshot["owner"] = activeOwner
    snapshot["sequence"] = sequence.toDouble()
    snapshot["session"] = heard
    snapshot["durationSeconds"] = if (audible != null) audibleDuration else duration
    snapshot["lastSeq"] = (if (audible != null) audibleSeq else seq).toDouble()
    snapshot["queue"] = queue?.let { HashMap(it) }
    snapshot["intent"] = if (safetyPaused || ended || (!allowed() && mono() >= leaseUntil)) "paused" else intent
    snapshot["rate"] = rate
    snapshot["ended"] = ended
    snapshot["terminal"] = terminal
    snapshot["error"] = error
    snapshot["deadlines"] = HashMap(deadlines)
    snapshot["timerStopped"] = timerStopped
    snapshot["expiredTimerRevision"] = expiredTimerRevision.toDouble()
    snapshot["policyRevision"] = num(policy?.get("revision")) ?: 0.0
    (notice ?: if (effectsWanted()) renderNotice(presentation(heard)) else null)?.let { snapshot["notice"] = it }
    end?.let { snapshot["end"] = it }
    snapshot["route"] = routeName()
    transition?.let { snapshot["transition"] = it }
    return snapshot
  }

  private fun emit() {
    if (activeOwner == null || emitting) return
    emitting = true
    ++sequence
    val snapshot = makeSnapshot()
    onSystemChange?.invoke()
    onSnapshot?.invoke(snapshot)
    emitting = false
  }

  fun acknowledge(owner: String, sequence: Double?): Boolean {
    val value = sequence?.toLong() ?: return false
    if (activeOwner != owner || value < 1 || value > this.sequence) return false
    ackSequence = Math.max(ackSequence, value)
    if (ackSequence >= transitionSequence) transition = null
    return true
  }

  // --- now playing, remote commands ------------------------------------------------------

  fun metadata(value: Map<String, Any?>?, owner: String?) {
    val heard = heard()
    if (activeOwner == null || activeOwner != owner || terminal || value == null || value["playbackId"] != heard["id"] || value["itemId"] != heard["itemId"]) return
    val clean = HashMap<String, Any?>()
    clean["itemId"] = value["itemId"]
    for (key in listOf("title", "artist", "album", "kind", "albumId")) (value[key] as? String)?.takeIf { it.length <= 4096 }?.let { clean[key] = it }
    for (key in listOf("discNumber", "trackNumber")) if (whole(value[key], 0.0, 100000.0)) clean[key] = value[key]
    val known = metadata
    // JavaScript's metadata does not carry the album position.
    if (clean["albumId"] == null && known?.get("albumId") != null && known["itemId"] == value["itemId"]) for (key in listOf("albumId", "discNumber", "trackNumber")) known[key]?.let { clean[key] = it }
    if (value.containsKey("bookId")) clean["bookId"] = (value["bookId"] as? String)?.takeIf { v1Id(it) } ?: ""
    else if(known != null && known["itemId"] == value["itemId"] && known.containsKey("bookId")) clean["bookId"] = known["bookId"]
    metadata = clean
    val timer = activeTimer()
    if(timer != null && timer["choice"] == "book" && clean.containsKey("bookId") && clean["bookId"] != timer["bookId"]) finishPolicyTimer()
    loadArtwork(value["posterUrl"])
    applyPreferredRate()
    onSystemChange?.invoke()
  }

  private fun loadCurrentMetadata() {
    metadataRequest?.cancel(); metadataRequest = null
    val heard = heard()
    val owner = activeOwner
    val playback = heard["id"]
    val item = heard["itemId"] as? String
    if (owner == null || terminal || !routeReady || !v1Id(item)) return
    val exchange = Exchange()
    metadataRequest = exchange
    exchange.completion = completion@{ status, value, _, _ ->
      if (activeOwner != owner || heard()["id"] != playback || metadataRequest !== exchange || terminal) return@completion
      metadataRequest = null
      val answer = value as? Map<*, *>
      if (status != 200 || answer == null || answer["id"] != item) return@completion
      val song = answer["song"] as? Map<*, *>
      val book = answer["bookFile"] as? Map<*, *>
      val length = (num(answer["durationMs"]) ?: 0.0) / 1000.0
      if (duration <= 0 && length > 0 && audible == null) duration = length
      metadata(mapOf("playbackId" to playback, "itemId" to item, "title" to (answer["title"] ?: "Audio"), "artist" to (song?.get("artist") ?: ""),
          "album" to (song?.get("albumTitle") ?: book?.get("bookTitle") ?: ""), "bookId" to (book?.get("bookId") ?: ""), "kind" to (answer["kind"] as? String ?: ""), "posterUrl" to answer["posterUrl"],
          "albumId" to ((song?.get("albumId") as? String) ?: ""), "discNumber" to (num(song?.get("discNumber")) ?: 0.0), "trackNumber" to (num(song?.get("trackNumber")) ?: 0.0)), owner)
    }
    exchange.start(origin + "/v1/items/" + Uri.encode(item), token, "GET", null, null)
  }

  private fun clearArtwork() { artworkRequest?.cancel(); artworkRequest = null; artworkPath = null; artwork = null }

  private fun loadArtwork(value: Any?) {
    if (!routeReady) { clearArtwork(); return }
    val item = heard()["itemId"]
    val path = value as? String
    if (path == null || path != "/v1/items/$item/art/poster" || path.length > 8192) { clearArtwork(); return }
    if (artworkPath == path) return
    clearArtwork()
    artworkPath = path
    val owner = activeOwner
    val exchange = Exchange(binary = true, byteLimit = 4L * 1024 * 1024)
    artworkRequest = exchange
    exchange.completion = completion@{ status, bytes, _, _ ->
      if (activeOwner != owner || artworkRequest !== exchange || terminal) return@completion
      artworkRequest = null
      if (status != 200 || bytes !is ByteArray || artworkPath != path) return@completion
      artwork = thumbnail(bytes) ?: return@completion
      onSystemChange?.invoke()
    }
    exchange.start(origin + path, token, "GET", null, null)
  }

  /** The profile's skip amounts for music and audiobooks on the lock screen. */
  fun skipIntervals(back: Double, forward: Double) {
    if (!back.isFinite() || !forward.isFinite()) return
    if (skipBackSeconds == back && skipForwardSeconds == forward) return
    skipBackSeconds = back.coerceIn(5.0, 120.0)
    skipForwardSeconds = forward.coerceIn(5.0, 120.0)
    onSystemChange?.invoke()
  }

  /** A lock-screen, notification or headset command. */
  fun remote(action: String, value: Double?) {
    val owner = activeOwner ?: return
    if (terminal) return
    if (action == "stop") { retire(owner, true); return }
    var resolved = action
    var amount = value
    if (action == "toggle") resolved = if (allowed() && intent == "playing") "pause" else "resume"
    if (action == "skip") { amount = Math.max(0.0, (num(controller.listeningSnapshot()["positionSeconds"]) ?: 0.0) + (value ?: 0.0)); resolved = "seek" }
    action(resolved, amount, owner)
  }

  fun systemState(): SystemState? {
    if (activeOwner == null || terminal) return null
    val engine = controller.listeningSnapshot()
    val heard = heard()
    val playing = allowed() && intent == "playing"
    return SystemState(heard["id"] as String, (metadata?.get("title") as? String) ?: "Portico Audio", (metadata?.get("artist") as? String) ?: "", (metadata?.get("album") as? String) ?: "", artwork,
        Math.round((if (audible != null) audibleDuration else duration) * 1000), Math.round((num(engine["positionSeconds"]) ?: 0.0) * 1000), playing,
        playing && engine["ready"] != true, ended, rate.toFloat(), queue != null, Math.round(skipBackSeconds * 1000), Math.round(skipForwardSeconds * 1000))
  }

  private fun routeName(): String {
    val audio = context.getSystemService(Context.AUDIO_SERVICE) as? AudioManager ?: return ""
    val names = ArrayList<String>()
    try {
      for (device in audio.getDevices(AudioManager.GET_DEVICES_OUTPUTS)) {
        if (device.type in EXTERNAL_OUTPUTS) device.productName?.toString()?.takeIf { it.isNotBlank() }?.let { if (it !in names) names.add(it) }
      }
    } catch (_: Exception) {}
    return names.joinToString(", ").take(512)
  }

  // --- retirement ------------------------------------------------------------------------

  private fun publishStop(status: Map<String, Any?>) { stopDisposition = status; onUserMutation?.invoke(status) }
  private fun stopAuthorized(entry: Map<String, Any?>): Boolean = try {
    PorticoUserMutationStore.transact(PorticoRecordStore.get(context), "authorizeStop", JSONObject(json(mapOf("entry" to entry)))) == true
  } catch(e: Exception) { publishStop(mapOf("key" to entry["key"],"state" to "pending","code" to "mutation_store_unavailable")); false }
  private fun finishStop(entry: Map<String, Any?>, change: Map<String, Any?>) {
    try {
      val saved = PorticoUserMutationStore.transact(PorticoRecordStore.get(context),"finishStop",JSONObject(json(mapOf("key" to entry["key"],"leaseOwner" to entry["leaseOwner"],"change" to change)))) == true
      publishStop(mapOf("key" to entry["key"],"intentId" to entry["intentId"],"state" to if(saved) change["state"] else "pending","code" to if(saved) (change["errorCode"] ?: "") else "mutation_pending","receipt" to if(saved) change["receipt"] else null,"terminalProof" to if(saved) change["terminalProof"] else null))
    } catch(e: Exception) { publishStop(mapOf("key" to entry["key"],"state" to "pending","code" to "mutation_store_unavailable")) }
  }
  private fun stopRetryFloor(headers: Headers?): Long {
    val text = headers?.get("Retry-After") ?: return 0
    text.trim().toLongOrNull()?.takeIf { it >= 0 }?.let { return if(it > Long.MAX_VALUE/1000) Long.MAX_VALUE else it*1000 }
    return try { maxOf(0, java.text.SimpleDateFormat("EEE, dd MMM yyyy HH:mm:ss zzz",java.util.Locale.US).apply { timeZone=TimeZone.getTimeZone("GMT") }.parse(text)!!.time-(mutationIssuedBaseMs+((mono()-mutationClockMono)*1000).toLong())) } catch(e: Exception) { 0 }
  }
  private fun lookupStop(entry: Map<String, Any?>, token: String, origin: String, floor: Long) {
    if(!stopAuthorized(entry)) return
    val exchange = Exchange(byteLimit=65536,timeoutMs=10000)
    exchange.completion = { status,value,_,headers ->
      @Suppress("UNCHECKED_CAST") val outer = value as? Map<String,Any?>
      @Suppress("UNCHECKED_CAST") val receipt = (outer?.get("data") as? Map<String,Any?>) ?: outer
      val valid = receipt != null && receipt["key"] == entry["key"] && receipt["operation"] == "stop_playback_session" &&
          receipt["status"] in setOf("applied","already_stopped","deleted","removed","partial","conflict","invalid","terminal") &&
          whole(receipt["committedMs"],0.0,MAX_INTEGER) && whole(receipt["expiresMs"],num(receipt["committedMs"]) ?: MAX_INTEGER,MAX_INTEGER) &&
          listOf("revision","generation","positionMs").all { receipt[it] == null || whole(receipt[it],0.0,MAX_INTEGER) } &&
          (receipt["generation"] == null || whole(receipt["generation"],0.0,2147483647.0)) &&
          (receipt["targetId"] == null || receipt["targetId"] is String && (receipt["targetId"] as String).toByteArray(Charsets.UTF_8).size <= 128) &&
          (receipt["positionStatus"] == null || receipt["positionStatus"] in setOf("","applied","already_stopped","not_renderer","policy_rejected","unchanged"))
      if(status in 200..299 && valid) {
        val tiny = receipt!!.filterKeys { it in setOf("key","operation","status","targetId","revision","generation","positionMs","positionStatus","committedMs","expiresMs") }
        val rejected = receipt["status"] in setOf("partial","conflict","invalid","terminal") || receipt["positionStatus"] == "policy_rejected"
        finishStop(entry,mapOf("state" to if(rejected) "review" else "confirmed","receipt" to tiny,"errorCode" to if(receipt["positionStatus"] == "policy_rejected") "final_position_rejected" else if(rejected) "mutation_needs_review" else ""))
      } else {
        val code = errorCode(value)
        if(status in 400..499 && status != 429 && !(status == 404 && code == "receipt_unknown"))
          finishStop(entry,mapOf("state" to if(status == 401 || status == 403) "quarantined" else "review","errorCode" to code.ifEmpty { "mutation_needs_review" }))
        else {
          val delay = maxOf(1000L,floor,stopRetryFloor(headers)); val now=System.currentTimeMillis()
          finishStop(entry,mapOf("state" to "pending","errorCode" to "mutation_pending","dueMs" to minOf(MAX_INTEGER.toLong(),if(delay > Long.MAX_VALUE-now) Long.MAX_VALUE else now+delay)))
        }
      }
    }
    exchange.start(origin+"/v1/user-mutations/"+entry["key"],token,"GET",null,null)
  }
  private fun reserveStop(position: Double): Map<String,Any?>? {
    val scope = mutationScope
    val id=session["id"] as? String; val generation=generation(session)
    if(scope == null || !v1Id(id) || generation == null) { publishStop(mapOf("state" to "not_queued","code" to "mutation_scope_changed")); return null }
    val now=System.currentTimeMillis(); val random=ByteArray(16).also { java.security.SecureRandom().nextBytes(it) }
    val key="p251_${mutationIssuedBaseMs+((mono()-mutationClockMono)*1000).toLong()}_"+random.joinToString("") { "%02x".format(it.toInt() and 255) }
    val identity="stop:$id:${generation.toLong()}"; val path="/v1/playback/sessions/$id"
    val engine=controller.listeningSnapshot(); val binding=engine["binding"] as? Map<*,*>
    val evidence=binding?.get("sessionId") == id && num(binding?.get("generation")) == generation && engine["observed"] == true && engine["seeking"] != true
    val body=if(evidence) mapOf("generation" to generation.toLong(),"positionMs" to Math.round(maxOf(0.0,if(position.isFinite()) position else 0.0)*1000)) else mapOf("generation" to generation.toLong())
    // All fingerprint object keys recursively sorted, identical to core's canonical JSON.
    val bodyPrint="{\"generation\":${body["generation"]}"+(if(evidence) ",\"positionMs\":${body["positionMs"]}" else "")+"}"
    val fingerprint="{\"body\":$bodyPrint,\"headers\":{},\"method\":\"DELETE\",\"operation\":\"stop_playback_session\",\"path\":\"$path\",\"target\":\"$identity\"}"
    val entry=mapOf("nativeStopOwner" to activeOwner,"key" to key,"operation" to "stop_playback_session","method" to "DELETE","path" to path,"target" to identity,"intentId" to identity,"fingerprint" to fingerprint,"scope" to scope,"body" to body,"headers" to mapOf("Idempotency-Key" to key),"state" to "pending","attempts" to 1,"dueMs" to now,"leaseOwner" to "native:$key","leaseUntilMs" to now+30000)
    return try {
      val saved = PorticoUserMutationStore.transact(PorticoRecordStore.get(context),"reserveStop",JSONObject(json(mapOf("entry" to entry)))) as String
      @Suppress("UNCHECKED_CAST") val result=parse(saved.toByteArray(Charsets.UTF_8)) as Map<String,Any?>
      publishStop(mapOf("key" to result["key"],"intentId" to identity,"state" to result["state"],"code" to (result["errorCode"] ?: "mutation_pending"),"receipt" to result["receipt"]))
      if(result["key"] == key && result["leaseOwner"] != null) result else null
    } catch(e: Exception) { publishStop(mapOf("state" to "not_queued","key" to key,"code" to "mutation_not_queued","draft" to entry)); null }
  }
  private fun sendStop(entry: Map<String,Any?>, token: String, origin: String, ready: Boolean) {
    if(!ready || token.isEmpty()) { finishStop(entry,mapOf("state" to "pending","errorCode" to "mutation_pending")); return }
    if(!stopAuthorized(entry)) return
    val exchange=Exchange(byteLimit=65536,timeoutMs=10000)
    exchange.completion = { status,value,_,headers ->
      if(status in 400..499 && status != 429) {
        val code=errorCode(value).ifEmpty { "mutation_needs_review" }
        val change=hashMapOf<String,Any?>("state" to if(status == 401 || status == 403) "quarantined" else "review","errorCode" to code)
        if(status in setOf(400,409,412,422)) change["terminalProof"]=mapOf("kind" to "refused","httpStatus" to status,"code" to code)
        finishStop(entry,change)
      }
      else lookupStop(entry,token,origin,stopRetryFloor(headers))
    }
    @Suppress("UNCHECKED_CAST") val body=entry["body"] as Map<String,Any?>
    @Suppress("UNCHECKED_CAST") val headers=entry["headers"] as Map<String,String>
    exchange.start(origin+entry["path"],token,"DELETE",body,headers)
  }

  fun consumeStop(key: String): Boolean {
    val state=JSONObject(PorticoUserMutationStore.transact(PorticoRecordStore.get(context),"read",JSONObject()) as String)
    val entries=state.getJSONArray("entries")
    val entry=(0 until entries.length()).map { entries.getJSONObject(it) }.firstOrNull { it.getString("key") == key } ?: return false
    if(entry.optString("nativeStopOwner").isEmpty() || entry.getString("operation") != "stop_playback_session") return false
    if(activeOwner != null && listOfNotNull(session,audible).any { "stop:${it["id"]}:${generation(it)?.toLong()}" == entry.getString("target") }) return false
    val consumed=PorticoUserMutationStore.transact(PorticoRecordStore.get(context),"consumeStop",JSONObject().put("key",key).put("owner",entry.getString("nativeStopOwner"))) == true
    if(consumed) onUserMutation?.invoke(mapOf("key" to key,"state" to entry.getString("state"),"nativeStopConsumed" to true))
    return consumed
  }

  fun retire(owner: String?, stop: Boolean) {
    if (activeOwner == null || activeOwner != owner) return
    val origin = this.origin
    val token = this.token
    val id = session["id"] as? String
    val ready = routeReady
    val position = num(controller.listeningSnapshot()["positionSeconds"]) ?: 0.0
    val stopEntry = if(stop && !terminal) reserveStop(position) else null
    // Fence locally before any network cleanup: nothing below can play or publish.
    safetyPaused = true
    metadataRequest?.cancel(); metadataRequest = null
    clearArtwork()
    ticking = false
    main.removeCallbacks(loop)
    request?.cancel(); request = null; busy = false
    controller.clearListeningOwner(owner!!)
    controller.onInterruption = null
    noisy?.let { try { context.unregisterReceiver(it) } catch (_: Exception) {} }
    noisy = null
    val key = journalKey
    if (key != null && !commitWanted) PorticoRecordStore.get(context).write(PorticoRecordStore.QUEUES, key, json(mapOf("version" to 1, "sessionId" to id, "retired" to true)).toByteArray(Charsets.UTF_8))
    activeOwner = null
    this.token = ""
    actions.clear(); transition = null; metadata = null; prepared = null; committed = null; audible = null; policy = null; commitWanted = false
    onSystemChange?.invoke()
    if(stop) (stopDisposition?.get("key") as? String)?.let { key ->
      try { consumeStop(key) } catch(e: Exception) { publishStop(mapOf("key" to key,"state" to "pending","code" to "mutation_store_unavailable")) }
    }
    if(stopEntry != null) sendStop(stopEntry,token,origin,ready)
  }

  fun invalidate() {
    activeOwner?.let { retire(it, true) }
    invalidated = true
    onSnapshot = null
    onSystemChange = null
  }

  private fun seekTo(seconds: Double): Map<String, Any?> = mapOf("revision" to (++seekRevision).toDouble(), "positionSeconds" to Math.max(0.0, if (seconds.isFinite()) seconds else 0.0))

  companion object {
    /** An observation that has not answered in this long is replaced by the next one. */
    private const val OBSERVATION_TIMEOUT_MS = 1500L
    /** The app's one listening owner, for the media session service. */
    @Volatile var shared: PorticoListeningOwner? = null

    private const val MAX_INTEGER = 9007199254740991.0
    private val LOOPBACK = setOf("localhost", "127.0.0.1", "[::1]", "::1")
    private val INTENTS = setOf("playing", "paused")
    private val ADVANCES = setOf("next", "previous", "entry")
    private val ACTIONS = setOf("pause", "resume", "seek", "rate", "retry", "next", "previous")
    private val RATES = setOf(0.5, 0.75, 1.0, 1.25, 1.5, 1.75, 2.0)
    // Audiobooks also take 2.5x and 3x (the shared `audiobookRates`); music stays at 2x.
    private val BOOK_RATES = RATES + setOf(2.5, 3.0)
    private val PASSOUT = setOf(0.0, 30.0, 60.0, 90.0, 120.0)
    private val SLEEP_MINUTES = setOf(15.0, 30.0, 45.0, 60.0, 90.0)
    private val TIMER_CHOICES = setOf("chapter", "item", "book")
    private val NORMALIZATION = setOf("off", "track", "album")
    private val DEADLINES = listOf("sleepAtMs", "passoutAtMs")
    private val GONE = setOf("session_ended", "session_not_found", "lease_expired")
    private val PROMOTING = setOf("ended", "audioPreparationFailed", "audioRenderFailed", "error")
    private val BACKOFF = doubleArrayOf(1.0, 2.0, 5.0, 10.0, 30.0)
    private val WIRE = Regex("^[A-Za-z0-9_-]{1,128}$")
    private val V1 = Regex("^[A-Za-z0-9_.-]{1,128}$")
    private val ISO = Regex("^(\\d{4})-(\\d{2})-(\\d{2})T(\\d{2}):(\\d{2}):(\\d{2})(\\.\\d{1,9})?(Z|[+-]\\d{2}:\\d{2})$")
    private val EXTERNAL_OUTPUTS = setOf(
        AudioDeviceInfo.TYPE_BLUETOOTH_A2DP, AudioDeviceInfo.TYPE_WIRED_HEADPHONES, AudioDeviceInfo.TYPE_WIRED_HEADSET, AudioDeviceInfo.TYPE_USB_HEADSET,
        AudioDeviceInfo.TYPE_USB_DEVICE, AudioDeviceInfo.TYPE_HDMI, AudioDeviceInfo.TYPE_HDMI_ARC)

    /** Credentialed requests: no redirects, no cookies, no cache, a bounded body. They go on without JavaScript. */
    private val http: OkHttpClient by lazy {
      OkHttpClient.Builder().porticoTrust().followRedirects(false).followSslRedirects(false).cache(null)
          .connectTimeout(15, TimeUnit.SECONDS).readTimeout(15, TimeUnit.SECONDS).callTimeout(20, TimeUnit.SECONDS).build()
    }

    private fun mono(): Double = SystemClock.elapsedRealtime() / 1000.0
    private fun wall(): Double = System.currentTimeMillis() / 1000.0
    private fun newId(): String = UUID.randomUUID().toString()
    private fun num(value: Any?): Double? = if (value is Boolean) null else PorticoPlaybackController.number(value)
    private fun whole(value: Any?, min: Double, max: Double): Boolean { val n = num(value) ?: return false; return n >= min && n <= max && Math.floor(n) == n }
    private fun inRange(value: Any?, min: Double, max: Double): Boolean { val n = num(value) ?: return false; return n >= min && n <= max }
    private fun wireId(value: Any?): Boolean = value is String && WIRE.matches(value)
    private fun v1Id(value: Any?): Boolean = value is String && V1.matches(value)
    private fun wireToken(value: Any?): Boolean = v1Id(value)
    private fun v1Revision(value: Any?): Boolean = value is String && value.isNotEmpty() && value.length <= 256

    @Suppress("UNCHECKED_CAST")
    private fun presentation(session: Map<String, Any?>?): Map<String, Any?>? = session?.get("presentation") as? Map<String, Any?>
    private fun generation(session: Map<String, Any?>?): Double? = num(presentation(session)?.get("generation"))
    private fun startSeconds(session: Map<String, Any?>?): Double = Math.max(0.0, (num(presentation(session)?.get("startPositionMs")) ?: 0.0) / 1000.0)

    /** A v1 presentation's version 2 plan: `audioRender` on the wire, `audio` once client-core parsed it. */
    @Suppress("UNCHECKED_CAST")
    private fun decodePlan(presentation: Map<String, Any?>?): Map<String, Any?>? {
      val wire = presentation?.get("audioRender") as? Map<String, Any?>
      if (num(wire?.get("version")) == 2.0) return wire
      val parsed = presentation?.get("audio") as? Map<String, Any?>
      return if (num(parsed?.get("version")) == 2.0) parsed else null
    }

    /** A v1 session as the owner needs it. */
    private fun v1Session(value: Map<String, Any?>?): Boolean {
      val p = presentation(value) ?: return false
      if (!v1Id(value!!["id"]) || !v1Revision(value["revision"]) || value["kind"] != "audio" || !v1Id(value["itemId"])) return false
      val url = p["url"] as? String ?: return false
      if (!whole(p["generation"], 0.0, MAX_INTEGER) || url.length > 4096) return false
      if (url.isNotEmpty() && !(url.startsWith("/v1/") || url.startsWith("https://") || url.startsWith("http://"))) return false
      val held = value["queue"]
      if (held != null) { val q = held as? Map<*, *> ?: return false; if (!v1Id(q["queueId"]) || !v1Id(q["entryId"])) return false }
      return true
    }

    private fun v1Queue(value: Map<String, Any?>?): Boolean = value != null && v1Id(value["id"]) && v1Revision(value["revision"])

    private fun errorCode(value: Any?): String = (((value as? Map<*, *>)?.get("error") as? Map<*, *>)?.get("code") as? String) ?: ""

    @Suppress("UNCHECKED_CAST")
    private fun currentOf(value: Any?): Map<String, Any?>? = ((value as? Map<*, *>)?.get("error") as? Map<*, *>)?.get("current") as? Map<String, Any?>

    private fun retryAfter(headers: Headers?, value: Any?): Double {
      val seconds = headers?.get("Retry-After")?.toDoubleOrNull() ?: num(((value as? Map<*, *>)?.get("error") as? Map<*, *>)?.get("retryAfterMs"))?.div(1000.0)
      return if (seconds != null && seconds > 0) Math.min(30.0, seconds) else 2.0
    }

    /** `scheme://host[:port]` and nothing else. */
    private fun originOf(value: String): Uri? {
      val url = try { Uri.parse(value) } catch (_: Exception) { return null }
      if (url.host.isNullOrEmpty() || url.userInfo != null || url.encodedQuery != null || url.encodedFragment != null || !url.path.isNullOrEmpty()) return null
      return if (url.scheme == "http" || url.scheme == "https") url else null
    }

    private fun consecutiveOnAlbum(album: String?, disc: Int, track: Int, nextAlbum: String?, nextDisc: Int, nextTrack: Int): Boolean {
      if (album.isNullOrEmpty() || album != nextAlbum) return false
      if (track <= 0 || nextTrack <= 0) return true
      val d = if (disc > 0) disc else 1
      val nd = if (nextDisc > 0) nextDisc else 1
      return (nd == d && nextTrack == track + 1) || (nd == d + 1 && nextTrack == 1)
    }

    /** Seconds since 1970 for an RFC 3339 time, or null. */
    private fun timestamp(value: Any?): Double? {
      val match = ISO.matchEntire(value as? String ?: return null) ?: return null
      val g = match.groupValues
      val calendar = Calendar.getInstance(TimeZone.getTimeZone("UTC"))
      calendar.clear()
      calendar.set(g[1].toInt(), g[2].toInt() - 1, g[3].toInt(), g[4].toInt(), g[5].toInt(), g[6].toInt())
      var seconds = calendar.timeInMillis / 1000.0 + (if (g[7].isEmpty()) 0.0 else g[7].toDouble())
      if (g[8] != "Z") {
        val sign = if (g[8][0] == '-') -1 else 1
        seconds -= sign * (g[8].substring(1, 3).toInt() * 3600 + g[8].substring(4, 6).toInt() * 60)
      }
      return seconds
    }

    /** At most 512 pixels on the long side, as PNG or JPEG bytes the notification can decode. */
    private fun thumbnail(bytes: ByteArray): ByteArray? = try {
      val bounds = BitmapFactory.Options().apply { inJustDecodeBounds = true }
      BitmapFactory.decodeByteArray(bytes, 0, bytes.size, bounds)
      var sample = 1
      while (Math.max(bounds.outWidth, bounds.outHeight) / (sample * 2) >= 512) sample *= 2
      val bitmap = BitmapFactory.decodeByteArray(bytes, 0, bytes.size, BitmapFactory.Options().apply { inSampleSize = sample })
      if (bitmap == null) null else ByteArrayOutputStream().also { bitmap.compress(Bitmap.CompressFormat.JPEG, 88, it); bitmap.recycle() }.toByteArray()
    } catch (_: Exception) { null } catch (_: OutOfMemoryError) { null }

    // --- JSON, with every number a Double so values compare the same wherever they came from ---

    fun parse(bytes: ByteArray): Any? = plain(JSONTokener(bytes.toString(Charsets.UTF_8)).nextValue())

    private fun plain(value: Any?): Any? = when (value) {
      null, JSONObject.NULL -> null
      is JSONObject -> HashMap<String, Any?>().also { map -> for (key in value.keys()) map[key] = plain(value.opt(key)) }
      is JSONArray -> ArrayList<Any?>().also { list -> for (i in 0 until value.length()) list.add(plain(value.opt(i))) }
      is Boolean, is String -> value
      is Number -> value.toDouble()
      else -> value.toString()
    }

    /** Sorted keys and whole numbers without a fraction: the same request is the same bytes. */
    fun json(value: Any?): String = StringBuilder().also { write(value, it) }.toString()

    private fun write(value: Any?, out: StringBuilder) {
      when (value) {
        null -> out.append("null")
        is Boolean -> out.append(value.toString())
        is Number -> {
          val n = value.toDouble()
          if (!n.isFinite()) out.append("null") else if (Math.floor(n) == n && Math.abs(n) <= MAX_INTEGER) out.append(n.toLong().toString()) else out.append(n.toString())
        }
        is String -> out.append(JSONObject.quote(value))
        is Map<*, *> -> {
          out.append('{')
          var first = true
          for (key in value.keys.map { it.toString() }.sorted()) {
            if (!first) out.append(',')
            first = false
            out.append(JSONObject.quote(key)).append(':')
            write(value[key], out)
          }
          out.append('}')
        }
        is Iterable<*> -> {
          out.append('[')
          var first = true
          for (item in value) { if (!first) out.append(','); first = false; write(item, out) }
          out.append(']')
        }
        else -> out.append(JSONObject.quote(value.toString()))
      }
    }
  }
}
