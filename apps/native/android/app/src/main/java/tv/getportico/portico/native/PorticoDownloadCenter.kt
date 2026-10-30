package tv.getportico.portico.native

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.Service
import android.app.job.JobInfo
import android.app.job.JobParameters
import android.app.job.JobScheduler
import android.app.job.JobService
import android.content.ComponentName
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.media.MediaMetadataRetriever
import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities
import android.os.Build
import android.os.Handler
import android.os.IBinder
import android.os.Looper
import android.os.StatFs
import java.io.File
import java.io.FileOutputStream
import java.io.IOException
import java.io.RandomAccessFile
import java.net.ConnectException
import java.net.SocketException
import java.net.SocketTimeoutException
import java.net.UnknownHostException
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import okhttp3.Call
import okhttp3.OkHttpClient
import okhttp3.Request
import tv.getportico.portico.R

/**
 * Offline downloads for phones and tablets: the Android counterpart of `Native/PorticoDownloads.m`.
 *
 * The server prepares a file and hands out an address for it; this moves the bytes and keeps them.
 * A transfer belongs to the process, not to the React instance, and a foreground service keeps the
 * process alive while one runs, so it goes on with the app out of sight. A finished file is
 * checked against the server's SHA-256 before it is called ready: a truncated or substituted file
 * is deleted, never played. Files live in the app's no-backup storage (they are re-downloadable
 * and can be tens of gigabytes).
 *
 * A file is what its SHA-256 says it is, not where it was fetched from. What has already arrived
 * is kept in `partial.part` and continued with `Range` and `If-Range` under whatever address the
 * app holds for the same file, so a dropped connection, a pause, a killed process or a fresh
 * address all carry on from where the bytes stopped. A dropped connection is retried here, with
 * growing waits; "Wi-Fi only" on mobile data waits for Wi-Fi and then carries on by itself.
 *
 * Each entry says what it is doing in three fields. `state` is `downloading`, `paused`, `failed`
 * or `ready`. `waiting` says why a download that should be moving is not: `network` or `wifi`.
 * `intent` is `hold` once the person has paused it, and `run` otherwise: an entry that is paused
 * or failed with `run` stopped by itself and is started again by the app when it can be.
 *
 * When the process is gone with transfers unfinished (the system ended it, the person swiped the
 * app away, the service's allowance ran out), a platform job (`PorticoDownloadJob`, JobScheduler)
 * owned by the installation brings the process back once a network it may use is there and
 * carries them on from their bytes, under the addresses they had, with the app closed (B-14.20).
 * An address that has run out cannot be renewed without the app: that download waits for it.
 *
 * This knows nothing about accounts, receipts or what may be played; that is decided in JavaScript
 * from the server's signed receipt.
 */
internal object PorticoDownloadCenter {
  /** An entry changed (or was removed: `{id, state: "removed"}`). Called on the bookkeeping thread. */
  var onChanged: ((Map<String, Any?>) -> Unit)? = null

  private class Transfer(id: String, val attempt: Double, val digest: String, val bytes: Long,
      @Volatile var call: Call? = null, @Volatile var stopped: String? = null) {
    val progress = PorticoDownloadProgress(PorticoDownloadCenter::post) { reached ->
      val entry = PorticoDownloadCenter.accepts(id, this)
      if (entry != null) {
        entry["bytesDone"] = reached.toDouble()
        entry["state"] = "downloading"
        entry["waiting"] = ""
        PorticoDownloadCenter.changed(id, false)
      }
    }
    // Bookkeeping-owned. Checking an existing final file does not use the network.
    var networkStarted = false
  }

  private val work = Executors.newSingleThreadExecutor { Thread(it, "portico-downloads") }
  private val transfers = Executors.newFixedThreadPool(2) { Thread(it, "portico-download-transfer") }
  private val storageWork = Executors.newSingleThreadExecutor { Thread(it, "portico-download-storage") }
  private val storageReplies = ArrayList<(Result<Map<String, Any?>>) -> Unit>()
  private val fileWork = PorticoDownloadWork(transfers)
  private val entries = HashMap<String, MutableMap<String, Any?>>()
  // Process-local identities only. These never enter `entries` or the manifest.
  internal class PublicationGeneration(val entry: MutableMap<String, Any?>, val value: String = java.util.UUID.randomUUID().toString()) { var retired = false }
  internal class PublicationStateTicket(val id: String, val generation: PublicationGeneration, val fields: List<String>)
  internal class PublicationStateRegistry {
    internal val tickets = HashMap<String, PublicationStateTicket>()
    internal val used = HashSet<String>()
    internal var closed = false
  }
  private val publicationGenerations = HashMap<String, PublicationGeneration>()
  private fun retirePublicationGeneration(id: String) { publicationGenerations.remove(id)?.retired = true }
  private fun publicationGeneration(id: String, entry: MutableMap<String, Any?>): String {
    val prior = publicationGenerations[id]
    if (prior != null && !prior.retired && prior.entry === entry) return prior.value
    return PublicationGeneration(entry).also { publicationGenerations[id] = it }.value
  }
  private val active = HashMap<String, Transfer>()
  private val removing = HashSet<String>()
  private val lastEmit = HashMap<String, Long>()
  private var attempts = 0.0
  private val extraAttempts = HashMap<Pair<String?, String?>, Any>()
  private var cellular = false
  private lateinit var app: Context
  private var loaded = false
  private lateinit var manifest: PorticoDownloadRecord
  private lateinit var state: PorticoDownloadRecord
  private val http: OkHttpClient by lazy {
    OkHttpClient.Builder().porticoTrust().followRedirects(false).followSslRedirects(false).cache(null)
        .connectTimeout(15, TimeUnit.SECONDS).readTimeout(30, TimeUnit.SECONDS).build()
  }
  private val extraHttp: OkHttpClient by lazy { http.newBuilder().callTimeout(60, TimeUnit.SECONDS).build() }

  /** Everything that touches the entries runs on one thread. */
  fun post(block: () -> Unit) { work.execute(block) }

  /** Reads the manifest once. A transfer the last process left running is paused, still meant to run. */
  fun open(context: Context): Boolean {
    if (loaded) return true
    app = context.applicationContext
    cellular = app.getSharedPreferences(PREFERENCES, Context.MODE_PRIVATE).getBoolean("cellular", false)
    manifest = PorticoDownloadRecord(File(root(), "manifest.json"))
    state = PorticoDownloadRecord(File(root(), "state.json"))
    val stored = try {
      manifest.read { raw -> decodeDownloadManifest(if (raw == null) emptyMap<String, Any?>() else parseRecord(raw)) }
    } catch (_: Exception) { return false }
    entries.putAll(stored)
    root().listFiles()?.forEach { if (it.name.endsWith(".incoming")) it.delete() }
    root().listFiles()?.filter { it.name.startsWith(".") && it.name.endsWith(".removed") }?.forEach { removed ->
      transfers.execute { removed.deleteRecursively() }
    }
    try {
      (app.getSystemService(Context.CONNECTIVITY_SERVICE) as ConnectivityManager).registerDefaultNetworkCallback(object : ConnectivityManager.NetworkCallback() {
        // "Never use mobile data" means now: a transfer in flight waits for Wi-Fi, keeping its
        // bytes, and carries on by itself when Wi-Fi is back.
        override fun onCapabilitiesChanged(network: Network, capabilities: NetworkCapabilities) = post { networkChanged() }
        override fun onAvailable(network: Network) = post { networkChanged() }
      })
    } catch (_: Exception) {}
    // Only the serial owner may publish a completely decoded manifest. A failed read above
    // leaves both the original bytes and the not-loaded state intact for an explicit retry.
    for ((id, entry) in entries.toList()) if (entry["state"] == "removed") finishRemoval(id, entry)
    loaded = true
    return true
  }

  private fun networkChanged() {
    if (!loaded) return
    if (!cellular && onCellular()) { for ((id, transfer) in active.toList()) if (transfer.networkStarted) hold(id, transfer, "wifi"); return }
    // Whatever was waiting for a network it may use starts again under the address it had. An
    // address that has run out answers 401, and the app then renews it.
    for ((id, entry) in entries.toList()) {
      val url = entry["grantUrl"] as? String ?: continue
      if (entry["state"] == "downloading" && (entry["waiting"] == "wifi" || entry["waiting"] == "network") && active[id] == null) begin(id, url, entry)
    }
  }

  private fun root(): File = File(app.noBackupFilesDir, "Downloads").also { it.mkdirs() }
  private fun directory(id: String) = File(root(), id)
  private fun partial(id: String) = File(directory(id), "partial.part")

  private fun save(): Boolean = try { loaded && manifest.write(PorticoListeningOwner.json(entries)) } catch (_: Exception) { false }

  private fun parseRecord(raw: String): Any? {
    val parser = org.json.JSONTokener(raw)
    check(parser.nextValue() is org.json.JSONObject && parser.nextClean() == '\u0000')
    return PorticoListeningOwner.parse(raw.toByteArray(Charsets.UTF_8))
  }

  private fun changed(id: String, force: Boolean) {
    // Progress arrives many times a second; the interface needs it about twice a second.
    val now = System.currentTimeMillis()
    if (!force && now - (lastEmit[id] ?: 0) < 500) return
    lastEmit[id] = now
    onChanged?.invoke(entries[id]?.takeUnless { it["state"] == "removed" }?.let { published(id, it) } ?: mapOf("id" to id, "state" to "removed"))
  }

  /** An entry as JavaScript sees it: what is stored, plus where its folder is right now. */
  private fun published(id: String, entry: Map<String, Any?>): Map<String, Any?> {
    val copy = HashMap(entry)
    copy["dir"] = android.net.Uri.fromFile(directory(id)).toString() + "/"
    copy["publicationGeneration"] = publicationGeneration(id, entries.getValue(id))
    // The address is a credential: it is used here and never handed back.
    copy.remove("grantUrl")
    return copy
  }

  private fun onCellular(): Boolean {
    val manager = app.getSystemService(Context.CONNECTIVITY_SERVICE) as? ConnectivityManager ?: return false
    return manager.getNetworkCapabilities(manager.activeNetwork ?: return false)?.hasTransport(NetworkCapabilities.TRANSPORT_CELLULAR) == true
  }

  // --- the JavaScript interface (all on the bookkeeping thread) --------------------------------

  fun list(): List<Map<String, Any?>> {
    check(loaded)
    return entries.filterValues { it["state"] != "removed" }.map { (id, entry) -> published(id, entry) }
  }

  /** The entry as it stands once the transfer has begun, or null for an invalid request. */
  fun start(request: Map<String, Any?>): Map<String, Any?>? {
    if (!loaded) return null
    val id = request["id"] as? String
    val url = request["url"] as? String
    val wanted = (request["sha256"] as? String)?.lowercase() ?: ""
    val bytes = PorticoMpvSupport.integer(request["bytes"])
    if (!safeIdentifier(id) || !SHA256.matches(wanted) || bytes == null || url == null || !(url.startsWith("https://") || url.startsWith("http://"))) return null
    id!!
    val entry = entries[id] ?: HashMap()
    if (entry["state"] == "removed") return null
    if (entry["state"] == "ready") return published(id, entry)
    retirePublicationGeneration(id)
    // Continue and retry both arrive here: an earlier transfer for the same download ends first.
    active.remove(id)?.let { it.stopped = "superseded"; it.call?.cancel() }
    // What is already on the device belongs to one exact file. It is continued under any address
    // for that same file, and thrown away only when the file asked for now is a different one.
    for (key in TEXT_FIELDS) (request[key] as? String)?.let { entry[key] = it }
    entry["id"] = id
    entry["bytes"] = bytes.toDouble()
    entry["bytesDone"] = (if (partial(id).isFile) partial(id).length() else 0L).toDouble()
    entry["error"] = ""
    entry["intent"] = "run"
    if (entry["addedAt"] == null) entry["addedAt"] = System.currentTimeMillis().toDouble()
    entries[id] = entry
    begin(id, url, entry)
    // Started by the person with the app in front: on Android 14 and later the job that carries it
    // on is a user-initiated data transfer, which the system does not cut off after ten minutes.
    if (active[id] != null || entry["waiting"] == "wifi") PorticoDownloadJob.schedule(app, cellular, userInitiated = true)
    return published(id, entry)
  }

  /** A delayed grant is not permission to recreate a removed or replaced download. */
  fun resume(request: Map<String, Any?>): Map<String, Any?>? {
    val id = request["id"] as? String ?: return null
    if (!isGeneration(id, request["expectedGeneration"] as? String)) return null
    return start(request)
  }

  /** Starts, or starts again, moving one entry's bytes from `url`. */
  private fun begin(id: String, url: String, entry: MutableMap<String, Any?>) {
    active.remove(id)?.let { it.stopped = "superseded"; it.call?.cancel() }
    val digest = (entry["sha256"] as? String)?.lowercase() ?: ""
    val bytes = PorticoMpvSupport.integer(entry["bytes"])
    if (!SHA256.matches(digest) || bytes == null) {
      entry["state"] = "failed"; entry["error"] = "verification_failed"; entry["waiting"] = ""
      save(); changed(id, true); release()
      return
    }
    entry["grantUrl"] = url
    entry["state"] = "downloading"
    entry["waiting"] = ""
    val transfer = Transfer(id, ++attempts, digest, bytes)
    entry["taskIdentifier"] = transfer.attempt
    active[id] = transfer
    save()
    changed(id, true)
    running = active.size
    PorticoDownloadService.keepAlive(app)
    PorticoDownloadJob.schedule(app, cellular)
    directory(id).mkdirs()
    val final = File(directory(id), safeFileName(entry["fileName"] as? String))
    // Cancellation closes the HTTP call asynchronously. The next attempt must wait for that
    // worker to close its file before inspecting, truncating or appending the partial file.
    fileWork.execute(id) verifyFile@ {
      if (transfer.stopped != null) return@verifyFile
      val verified = verifiedDownloadFile(final, bytes, digest) { transfer.stopped != null }
      post {
        if (transfer.stopped != null || accepts(id, transfer) == null) return@post
        when (downloadStart(verified, cellular, ::onCellular)) {
          DownloadStart.ADOPT -> finish(id, transfer, final, null, digest)
          DownloadStart.WAIT_FOR_WIFI -> hold(id, transfer, "wifi")
          DownloadStart.TRANSFER -> {
            transfer.networkStarted = true
            fileWork.execute(id) fetchFile@ {
              if (transfer.stopped != null) return@fetchFile
              if (prepareDownloadPartial(directory(id), digest)) transfer(id, url, transfer)
              else post { finish(id, transfer, partial(id), "storage_full", "") }
            }
          }
        }
      }
    }
  }

  private fun accepts(id: String, transfer: Transfer): MutableMap<String, Any?>? {
    val entry = entries[id] ?: return null
    return if (entry["state"] != "ready" && entry["state"] != "removed" && entry["taskIdentifier"] == transfer.attempt && active[id] === transfer) entry else null
  }

  /** Stops a transfer that is to carry on by itself: it stays "downloading", and says what it waits for. */
  private fun hold(id: String, transfer: Transfer, waiting: String) {
    transfer.stopped = "held"
    transfer.call?.cancel()
    val entry = accepts(id, transfer) ?: return
    active.remove(id)
    entry["state"] = "downloading"
    entry["waiting"] = waiting
    entry["error"] = ""
    save()
    changed(id, true)
    release()
    PorticoDownloadJob.schedule(app, cellular)
  }

  /** Stops a transfer for good until somebody starts it again. `intent` records who stopped it. */
  private fun suspend(id: String, transfer: Transfer?, intent: String): Boolean {
    val entry = if (transfer != null) accepts(id, transfer) else entries[id]
    val paused = pausedDownloadEntry(entry, intent) ?: return true
    if (!commitDownloadEntry(entries, id, paused, ::save)) return false
    transfer?.stopped = "paused"
    transfer?.call?.cancel()
    active.remove(id)
    retirePublicationGeneration(id)
    changed(id, true)
    release()
    return true
  }

  /** The person paused it: it stays paused until they continue it. */
  fun pause(id: String?): String? {
    if (!loaded) return "read_failed"
    if (!safeIdentifier(id)) return "invalid_download"
    return if (suspend(id!!, active[id], "hold")) null else "write_failed"
  }

  /** The system is ending the service (or the job) that keeps transfers alive: they stop, keeping their bytes, and the job or the app starts them again. */
  fun interruptAll() {
    for ((id, transfer) in active.toList()) if (!suspend(id, transfer, "run")) {
      // The system is stopping us even if storage refuses the paused record. Keep the bytes;
      // the previous durable run intent still lets the next process continue them.
      transfer.stopped = "paused"; transfer.call?.cancel(); active.remove(id)
      entries[id]?.let { it["state"] = "paused"; it["waiting"] = ""; it["error"] = "transfer_failed" }
      retirePublicationGeneration(id)
      changed(id, true)
      release()
    }
    if (unfinished()) PorticoDownloadJob.schedule(app, cellular)
  }

  /** Downloads that stopped by themselves and still have an address to continue from. */
  private fun unfinished(): Boolean = entries.values.any { resumable(it) }
  private fun resumable(entry: Map<String, Any?>): Boolean =
      entry["grantUrl"] is String && entry["intent"] != "hold" && (entry["state"] == "paused" || entry["state"] == "downloading" || (entry["state"] == "failed" && entry["error"] == "network"))

  /** Runs on the bookkeeping thread when the last transfer in flight has stopped (the job's cue to finish). */
  var onIdle: (() -> Unit)? = null

  /**
   * The job's work: every download that stopped by itself is carried on from its bytes under the
   * address it had. False when there is nothing to carry on (the job then ends and is cancelled).
   */
  fun resumeUnfinished(): Boolean {
    if (!loaded) return false
    for ((id, entry) in entries.toList()) {
      val url = entry["grantUrl"] as? String ?: continue
      if (active[id] == null && resumable(entry)) { entry["error"] = ""; begin(id, url, entry) }
    }
    if (active.isEmpty() && !unfinished()) PorticoDownloadJob.cancel(app)
    return active.isNotEmpty()
  }

  /** Whether downloads still wait to be carried on (the job asks to run again when they do). */
  fun hasUnfinished(): Boolean = unfinished()

  /** Whether the exact published entry version is still the one this center holds. */
  private fun isGeneration(id: String, generation: String?): Boolean {
    val live = publicationGenerations[id] ?: return false
    return !live.retired && live.entry === entries[id] && generation != null && live.value == generation
  }

  /** Conditional removal for a confirmed target: the exact published entry version is
   * matched on this bookkeeping thread before any file, task or manifest side effect.
   * Null on success; otherwise the rejection code (never a silent success). */
  fun removeGeneration(id: String?, generation: String?): String? {
    if (!safeIdentifier(id)) return "invalid_download"
    id!!
    if (!isGeneration(id, generation)) return "generation_changed"
    return remove(id)
  }

  /** A durable tombstone precedes cancellation or file removal, so a full disk cannot make
   * removal appear successful and then resurrect the row on the next process start. */
  fun remove(id: String?): String? {
    if (!loaded) return "read_failed"
    if (!safeIdentifier(id)) return "invalid_download"
    id!!
    val entry = entries[id] ?: return null
    if (entry["state"] == "removed") { finishRemoval(id, entry); return null }
    val removed = HashMap(entry).apply { put("state", "removed"); remove("grantUrl") }
    if (!commitDownloadEntry(entries, id, removed, ::save)) return "write_failed"
    retirePublicationGeneration(id)
    active.remove(id)?.let { it.stopped = "removed"; it.call?.cancel() }
    finishRemoval(id, removed)
    changed(id, true)
    release()
    return null
  }

  private fun finishRemoval(id: String, tombstone: MutableMap<String, Any?>) {
    if (entries[id] !== tombstone || !removing.add(id)) return
    val folder = directory(id)
    val removed = File(root(), ".$id-${java.util.UUID.randomUUID()}.removed")
    val moved = try { !downloadPathExists(folder) || folder.renameTo(removed) } catch (_: Exception) { false }
    if (moved) {
      entries.remove(id)
      removing.remove(id)
      // The committed tombstone is sufficient until the next ordinary manifest save. A crash
      // here cleans the renamed directory and the tombstone on the next open.
      fileWork.execute(id) { removed.deleteRecursively() }
    } else {
      // Do not admit a replacement under this directory while the old writer is closing. The
      // hidden tombstone survives a failed deletion and is retried on the next open/remove.
      fileWork.execute(id) {
        val deleted = folder.deleteRecursively()
        post { removing.remove(id); if (deleted && entries[id] === tombstone) entries.remove(id) }
      }
    }
  }

  /** Null when saved; otherwise the rejection code. */
  fun setMeta(id: String?, meta: String?): String? {
    val entry = entries[id]?.takeUnless { it["state"] == "removed" } ?: return "not_found"
    if (meta == null || meta.length >= 65536) return "too_large"
    val previous = entry["meta"]
    if (previous != meta) retirePublicationGeneration(id!!)
    entry["meta"] = meta
    if (!save()) { if (previous != null) entry["meta"] = previous else entry.remove("meta"); return "write_failed" }
    changed(id!!, true)
    return null
  }

  /** Several entries' details in one write of the manifest. Null when saved; otherwise the rejection code. */
  fun setMetas(metas: Map<String, Any?>): String? {
    for ((id, meta) in metas) {
      if (entries[id]?.get("state") != "removed" && entries[id] != null && (meta !is String || meta.length >= 65536)) return "too_large"
    }
    val previous = HashMap<String, Any?>()
    for ((id, meta) in metas) {
      val entry = entries[id]?.takeUnless { it["state"] == "removed" } ?: continue
      previous[id] = entry["meta"]
      if (entry["meta"] != meta) retirePublicationGeneration(id)
      entry["meta"] = meta
    }
    if (!save()) {
      for ((id, meta) in previous) entries[id]?.let { if (meta != null) it["meta"] = meta else it.remove("meta") }
      return "write_failed"
    }
    for (id in previous.keys) changed(id, true)
    return null
  }

  // --- what travels with a download: its description, artwork and subtitles -------------------

  private fun extra(id: String?, name: String?): File? {
    if (!safeIdentifier(id) || entries[id] == null || entries[id]?.get("state") == "removed" || name == null || !EXTRA.matches(name)) return null
    // All generic read/write/fetch paths reject reserved aliases even on case-insensitive storage.
    if (name.startsWith("reader-state-", ignoreCase = true) || (name.equals("publication.json", ignoreCase = true) && name != "publication.json")) return null
    return File(File(directory(id!!), "extras"), name)
  }

  /** All state ticket operations run on `work`, including close/invalidate and the final rename.
   * This is equality/custody, not new receipt verification or offline admission. */
  private fun publicationStateFields(entry: Map<String, Any?>): List<String>? = try {
    val meta = PorticoListeningOwner.parse((entry["meta"] as String).toByteArray(Charsets.UTF_8)) as Map<*, *>
    val receipt = meta["receipt"] as Map<*, *>
    val claims = receipt["claims"] as Map<*, *>
    val publication = claims["publication"] as Map<*, *>
    val artifact = claims["artifact"] as Map<*, *>
    val bytes = PorticoMpvSupport.integer(entry["bytes"]) ?: error("state_identity")
    check(claims["itemId"] == entry["itemId"] && artifact["sha256"] == entry["sha256"] && PorticoMpvSupport.integer(artifact["bytes"]) == bytes)
    listOf(entry["itemId"], meta["owner"], entry["sha256"], bytes.toString(), receipt["keyId"], receipt["payload"], receipt["signature"],
      publication["sourceId"], publication["revision"], publication["format"], publication["documentSha256"]).map { (it as String).also { value -> check(value.isNotEmpty()) } }
  } catch (_: Exception) { null }

  fun publicationStateInvalidate(registry: PublicationStateRegistry) {
    registry.closed = true; registry.tickets.clear(); registry.used.clear()
  }
  fun publicationStateClose(registry: PublicationStateRegistry, handle: String?) {
    if (registry.closed || handle == null || !handle.matches(Regex("[A-Za-z0-9_-]{8,128}"))) return
    if (registry.used.size >= 4096 && !registry.used.contains(handle)) { publicationStateInvalidate(registry); return }
    registry.used.add(handle); registry.tickets.remove(handle)
  }
  fun publicationStateOpen(registry: PublicationStateRegistry, handle: String?, id: String?, generation: String?, expected: String?): Boolean {
    if (registry.closed || handle == null || !handle.matches(Regex("[A-Za-z0-9_-]{8,128}")) || registry.used.contains(handle)) return false
    if (registry.used.size >= 4096) { publicationStateInvalidate(registry); return false }
    registry.used.add(handle) // Includes failed opens: this original handle can never adopt a successor.
    if (registry.tickets.size >= 8 || id == null || !safeIdentifier(id) || expected == null || expected.toByteArray(Charsets.UTF_8).size > 65536) return false
    val original = publicationGenerations[id] ?: return false
    val entry = entries[id] ?: return false
    val fields = publicationStateFields(entry) ?: return false
    val captured = try { PorticoListeningOwner.parse(expected.toByteArray(Charsets.UTF_8)) as? List<*> } catch (_: Exception) { null }
    if (original.retired || original.entry !== entry || original.value != generation || entry["state"] != "ready" || captured != fields) return false
    registry.tickets[handle] = PublicationStateTicket(id, original, fields)
    return true
  }
  private fun publicationStateTicket(registry: PublicationStateRegistry, handle: String, name: String): PublicationStateTicket {
    check(!registry.closed && name.matches(Regex("reader-state-[A-Za-z0-9_-]{1,64}\\.json")))
    val ticket = registry.tickets[handle] ?: error("reading_retired")
    val entry = entries[ticket.id] ?: error("reading_retired")
    check(!ticket.generation.retired && entry === ticket.generation.entry && publicationGenerations[ticket.id] === ticket.generation && entry["state"] == "ready" && publicationStateFields(entry) == ticket.fields)
    return ticket
  }
  fun publicationStateRead(registry: PublicationStateRegistry, handle: String, name: String): String {
    val ticket = publicationStateTicket(registry, handle, name)
    val file = File(File(directory(ticket.id), "extras"), name)
    val stat = try { android.system.Os.lstat(file.path) } catch (error: android.system.ErrnoException) {
      if (error.errno == android.system.OsConstants.ENOENT) return "" else throw error
    }
    check(android.system.OsConstants.S_ISREG(stat.st_mode) && stat.st_size in 0L..1048576L)
    val bytes = java.io.FileInputStream(file).use { input ->
      val out = ByteArray(stat.st_size.toInt()); var offset = 0
      while (offset < out.size) { val count = input.read(out, offset, out.size - offset); check(count > 0); offset += count }
      check(input.read() == -1); out
    }
    return Charsets.UTF_8.newDecoder()
      .onMalformedInput(java.nio.charset.CodingErrorAction.REPORT)
      .onUnmappableCharacter(java.nio.charset.CodingErrorAction.REPORT)
      .decode(java.nio.ByteBuffer.wrap(bytes)).toString()
  }
  fun publicationStateWrite(registry: PublicationStateRegistry, handle: String, name: String, json: String) {
    val ticket = publicationStateTicket(registry, handle, name)
    val bytes = json.toByteArray(Charsets.UTF_8)
    check(bytes.size <= 1048576)
    val parser = org.json.JSONTokener(json); parser.nextValue(); check(parser.nextClean() == '\u0000')
    val file = File(File(directory(ticket.id), "extras"), name)
    val holding = File(file.path + "." + java.util.UUID.randomUUID() + ".tmp")
    try {
      check(file.parentFile?.isDirectory == true || file.parentFile?.mkdirs() == true)
      FileOutputStream(holding).use { it.write(bytes); it.fd.sync() }
      // No hop to fileWork/transfer/IO executors between this proof and the atomic rename.
      check(publicationStateTicket(registry, handle, name) === ticket)
      check(holding.renameTo(file))
    } finally { holding.delete() } // Only this invocation's scratch; never the person's record.
  }

  /** Writes one small text file beside the download. False when it could not be kept. */
  fun writeExtra(id: String?, name: String?, text: String?): Boolean {
    val file = extra(id, name) ?: return false
    if (text == null || text.length > MAX_EXTRA) return false
    if (name == "publication.json") retirePublicationGeneration(id!!)
    return try {
      file.parentFile?.mkdirs()
      val holding = File(file.path + ".tmp")
      FileOutputStream(holding).use { it.write(text.toByteArray(Charsets.UTF_8)); it.fd.sync() }
      holding.renameTo(file)
    } catch (_: Exception) { false }
  }

  fun readExtra(id: String?, name: String?): String {
    val file = extra(id, name) ?: return ""
    return try { if (file.isFile && file.length() <= MAX_EXTRA) file.readText(Charsets.UTF_8) else "" } catch (_: Exception) { "" }
  }

  /**
   * Fetches one small file (a poster, a subtitle track) to keep beside the download. Answers
   * `saved`, `missing` (the server has no such file for this title, which is an ordinary answer),
   * `later` (not ready yet, or the connection failed) or `invalid`. Runs on a transfer thread.
   */
  fun fetchExtra(id: String?, name: String?, url: String?, done: (String) -> Unit) {
    val file = extra(id, name)
    if (file == null || url == null || !(url.startsWith("https://") || url.startsWith("http://"))) { done("invalid"); return }
    val entry = entries[id]
    val key = id to name
    val attempt = Any()
    extraAttempts[key] = attempt
    val holding = File(file.path + "." + java.util.UUID.randomUUID() + ".tmp")
    file.parentFile?.mkdirs()
    extras.execute {
      val outcome = try {
        extraHttp.newCall(Request.Builder().url(url).build()).execute().use { response ->
          val body = response.body
          when {
            response.code == 404 -> "missing"
            response.code !in 200..299 || body == null -> "later"
            body.contentLength() > MAX_EXTRA -> "missing"
            else -> {
              var total = 0L
              var fits = true
              FileOutputStream(holding).use { out ->
                val source = body.byteStream()
                val buffer = ByteArray(1 shl 16)
                while (true) {
                  val read = source.read(buffer)
                  if (read == -1) break
                  total += read
                  if (total > MAX_EXTRA) { fits = false; break }
                  out.write(buffer, 0, read)
                }
              }
              if (fits && total > 0) "saved" else if (fits) "later" else "missing"
            }
          }
        }
      } catch (_: Exception) { "later" }
      post {
        // An old fetch must neither overwrite a newer extra nor remove a replacement download.
        val current = entries[id] === entry && extraAttempts[key] === attempt
        if (current && outcome == "saved" && name == "publication.json") retirePublicationGeneration(id!!)
        if (extraAttempts[key] === attempt) extraAttempts.remove(key)
        val result = if (!current) "later" else if (outcome == "saved" && !holding.renameTo(file)) "later" else outcome
        holding.delete()
        if (entries[id] == null) { file.parentFile?.delete(); directory(id!!).delete() }
        done(result)
      }
    }
  }

  /** A single tree walk serves overlapping requests, without holding up pause/remove/list. */
  fun storage(done: (Result<Map<String, Any?>>) -> Unit) {
    val folder = root()
    storageReplies.add(done)
    if (storageReplies.size > 1) return
    storageWork.execute {
      val result = try {
        val volume = StatFs(folder.absolutePath)
        val measured = PorticoDownloadStorage.measure(folder)
        Result.success(mutableMapOf<String, Any?>("freeBytes" to volume.availableBytes.toDouble(), "totalBytes" to volume.totalBytes.toDouble()).apply {
          if (measured != null) {
            put("storedBytes", measured.storedBytes.toDouble())
            put("allocatedBytes", measured.allocatedBytes.toDouble())
          }
        })
      } catch (error: Exception) { Result.failure<MutableMap<String, Any?>>(error) }
      post {
        // Only this thread reads mutable entries. Quota bytes are the ready artifacts now;
        // the separate physical usage is a best-effort sample and may be unknown during writes.
        var used = 0.0
        for (entry in entries.values) if (entry["state"] == "ready") used += (entry["bytes"] as? Number)?.toDouble() ?: 0.0
        val answer = result.map { it.apply { put("usedBytes", used) }.toMap() }
        val replies = storageReplies.toList()
        storageReplies.clear()
        replies.forEach { it(answer) }
      }
    }
  }

  fun setCellular(allowed: Boolean) {
    app.getSharedPreferences(PREFERENCES, Context.MODE_PRIVATE).edit().putBoolean("cellular", allowed).apply()
    cellular = allowed
    // Tightened on mobile data: what is in flight waits for Wi-Fi. Loosened: what was waiting goes on.
    networkChanged()
  }

  fun readState(): String {
    check(loaded)
    return state.read { raw -> if (raw != null) parseRecord(raw); raw ?: "" }
  }

  fun writeState(json: String): Boolean = loaded && state.write(json)

  /** The file of a finished download, or the rejection code (`not_ready`, `missing_file`). */
  fun file(id: String?): Any {
    val entry = entries[id] ?: return "not_ready"
    val name = entry["file"] as? String
    if (entry["state"] != "ready" || name == null) return "not_ready"
    val file = File(directory(id!!), safeFileName(name))
    return if (file.isFile) file else "missing_file"
  }

  /** Resolve only an exact ready owned download; never a caller supplied path. */
  fun publication(identifier: String, owner: String, digest: String): Pair<File, () -> Boolean>? {
    val entry = entries[identifier] ?: return null
    fun current(): Boolean = try {
      entries[identifier] === entry && entry["state"] == "ready" && entry["sha256"] == digest && org.json.JSONObject(entry["meta"] as? String ?: "{}").optString("owner") == owner
    } catch (_: Exception) {false}
    if (!current()) return null
    val file = file(identifier) as? File ?: return null
    return Pair(file, ::current)
  }

  fun durationSeconds(file: File): Double = try {
    val reader = MediaMetadataRetriever()
    try { reader.setDataSource(file.path); (reader.extractMetadata(MediaMetadataRetriever.METADATA_KEY_DURATION)?.toDoubleOrNull() ?: 0.0) / 1000.0 } finally { reader.release() }
  } catch (_: Exception) { 0.0 }

  // --- the transfer --------------------------------------------------------------------------

  private fun transfer(id: String, url: String, transfer: Transfer) {
    val part = partial(id)
    var failure: String? = null
    var attempt = 0
    while (true) {
      failure = attemptTransfer(id, url, transfer, part)
      if (transfer.stopped != null) return
      // A dropped connection is not the end of a download: wait a little longer each time and
      // carry on from the bytes already here. Anything else is for the app to deal with.
      if (failure != "network" || attempt >= RETRY_WAITS.size) break
      post { accepts(id, transfer)?.let { it["waiting"] = "network"; changed(id, true) } }
      val wait = RETRY_WAITS[attempt++]
      var slept = 0L
      while (slept < wait) { Thread.sleep(500); slept += 500; if (transfer.stopped != null) return }
      post { accepts(id, transfer)?.let { it["waiting"] = ""; changed(id, true) } }
    }
    if (transfer.stopped != null) return
    val digest = if (failure == null) sha256(part, transfer) else ""
    if (transfer.stopped != null) return
    post { finish(id, transfer, part, failure, digest) }
  }

  /** One request for the rest of the file. Null when the whole file is now in `part`; otherwise why not. */
  private fun attemptTransfer(id: String, url: String, transfer: Transfer, part: File): String? {
    try {
      if (transfer.stopped != null) return "stopped"
      val expected = transfer.digest
      val size = transfer.bytes
      var offset = if (part.isFile) part.length() else 0L
      // Everything is already here (the last attempt died between the last byte and the check).
      if (size > 0 && offset == size) return null
      if (size > 0 && offset > size) { part.delete(); offset = 0L }
      val request = Request.Builder().url(url)
      if (offset > 0) {
        request.header("Range", "bytes=$offset-")
        // The server answers with the rest only if the file is still the one these bytes came
        // from; otherwise it sends the whole file and the bytes here are replaced.
        if (expected.isNotEmpty()) request.header("If-Range", "\"sha256-$expected\"")
      }
      val call = http.newCall(request.build())
      transfer.call = call
      if (transfer.stopped != null) return "stopped"
      call.execute().use { response ->
        val status = response.code
        if (status == 416) { part.delete(); return "network" }
        if (status == 503 || status == 429) return "network"
        if (status !in 200..299) return if (status == 401 || status == 403 || status == 404 || status == 409 || status == 410) "grant_expired" else "server_refused"
        // 206 must continue exactly where the bytes here end, of the same file.
        val range = response.header("Content-Range") ?: ""
        val tag = (response.header("ETag") ?: "").lowercase()
        val appending = status == 206 && offset > 0 && range.startsWith("bytes $offset-") && (expected.isEmpty() || tag.isEmpty() || tag == "\"sha256-$expected\"")
        if (status == 206 && !appending) { part.delete(); return "network" }
        val body = response.body ?: return "transfer_failed"
        val length = body.contentLength()
        var done = if (appending) offset else 0L
        val total = if (length > 0) length + done else 0L
        if (total > size) return "verification_failed"
        RandomAccessFile(part, "rw").use { out ->
          if (appending) out.seek(offset) else out.setLength(0)
          val source = body.byteStream()
          val buffer = ByteArray(1 shl 16)
          while (true) {
            val read = source.read(buffer)
            if (read == -1) break
            if (done + read > size) return "verification_failed"
            out.write(buffer, 0, read)
            done += read
            transfer.progress.offer(done)
            if (transfer.stopped != null) return "stopped"
          }
          // Completion may be published only after the received bytes have
          // reached the file, before its verified final rename and record.
          out.fd.sync()
        }
        // The connection closed early without an error: the rest is fetched on the next attempt.
        if (done < size) return "network"
      }
      return null
    } catch (error: IOException) {
      if (transfer.stopped != null) return "stopped"
      return when {
        error.message?.contains("ENOSPC") == true -> "storage_full"
        error is UnknownHostException || error is ConnectException || error is SocketTimeoutException || error is SocketException || error is javax.net.ssl.SSLException || error is java.io.EOFException || error is okhttp3.internal.http2.StreamResetException -> "network"
        else -> "network"
      }
    } catch (_: Exception) {
      return "transfer_failed"
    }
  }

  private fun finish(id: String, transfer: Transfer, part: File, failure: String?, digest: String) {
    val entry = accepts(id, transfer) ?: return
    active.remove(id)
    retirePublicationGeneration(id)
    var error = failure
    if (error == null) {
      if (digest != transfer.digest || part.length() != transfer.bytes) error = "verification_failed"
    }
    if (error == "verification_failed") part.delete()
    if (error == null) {
      val final = File(directory(id), safeFileName(entry["fileName"] as? String))
      if (part != final) final.delete()
      if (part == final || part.renameTo(final)) {
        val size = final.length().toDouble()
        entry["state"] = "ready"; entry["error"] = ""; entry["bytes"] = size; entry["bytesDone"] = size
        entry["file"] = final.name
        entry["completedAt"] = System.currentTimeMillis().toDouble()
        // The grant address has done its job; a finished download keeps no token.
        entry.remove("grantUrl")
      } else { part.delete(); error = "storage_full" }
    }
    if (error != null) { entry["state"] = "failed"; entry["error"] = error }
    entry["waiting"] = ""
    if (!save() && entry["state"] == "ready") {
      // Keep the verified file, but refuse an offline-ready publication until
      // its completed local record has actually been committed.
      entry["state"] = "failed"; entry["error"] = "transfer_failed"
      entry.remove("completedAt")
      save()
    }
    changed(id, true)
    release()
  }

  /** Transfers in flight, for the service that keeps the process alive for them. */
  @Volatile var running = 0
    private set

  private fun release() {
    running = active.size
    if (active.isEmpty()) {
      PorticoDownloadService.done(app)
      if (!unfinished()) PorticoDownloadJob.cancel(app)
      onIdle?.invoke()
    }
  }

  private fun sha256(file: File, transfer: Transfer): String = downloadFileDigest(file) { transfer.stopped != null }

  private fun safeIdentifier(value: String?): Boolean = value != null && IDENTIFIER.matches(value)

  /** A file name from the server is used as a path component: its last component, with nothing that could leave the download's own folder. */
  fun safeFileName(value: String?): String {
    val name = (value ?: "").substringAfterLast('/').replace(Regex("[/\\\\:\\u0000]"), "_")
    return if (name.isEmpty() || name.startsWith(".") || name.length > 200 || name == "partial.part") "media" else name
  }

  private const val PREFERENCES = "portico.downloads"
  /** Waits between attempts after a dropped connection, in milliseconds: about a quarter of an hour in all. */
  private val RETRY_WAITS = longArrayOf(2000, 5000, 15000, 30000, 60000, 120000, 300000, 300000)
  /** The most one extra (a poster, a subtitle track, the description) may weigh. */
  private const val MAX_EXTRA = 32L * 1024 * 1024
  private val EXTRA = Regex("^[A-Za-z0-9_-]{1,96}(\\.[a-z0-9]{1,8})?$")
  private val extras = Executors.newFixedThreadPool(2) { Thread(it, "portico-download-extras") }
  private val IDENTIFIER = Regex("^[A-Za-z0-9_-]{1,160}$")
  private val SHA256 = Regex("^[a-f0-9]{64}$")
  private val TEXT_FIELDS = listOf("itemId", "title", "subtitle", "quality", "fileName", "sha256", "contentType", "artworkUrl", "meta")
}

/**
 * Keeps the process alive while a download runs with the app out of sight. Android requires a
 * foreground service to carry a notification; it says only that a download is in progress.
 */
class PorticoDownloadService : Service() {
  override fun onBind(intent: Intent?): IBinder? = null

  override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
    val notification = notification(this)
    try {
      if (Build.VERSION.SDK_INT >= 29) startForeground(NOTIFICATION, notification, ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC) else startForeground(NOTIFICATION, notification)
    } catch (_: Exception) {
      // Not allowed to go to the foreground from here: the transfer goes on for as long as the process does.
      stopSelf()
    }
    return START_NOT_STICKY
  }

  /**
   * Android gives a data-transfer service a daily allowance and says when it has run out. The
   * transfers stop cleanly, keeping their bytes, and the app starts them again the next time it
   * is in front, where no service is needed.
   */
  override fun onTimeout(startId: Int, fgsType: Int) {
    PorticoDownloadCenter.post { PorticoDownloadCenter.interruptAll() }
    stopSelf()
  }

  companion object {
    private const val CHANNEL = "portico.downloads"
    internal const val NOTIFICATION = 4101

    /** The one quiet notification a running download carries (the service's, and a user-initiated job's). */
    internal fun notification(context: Context): Notification {
      val manager = context.getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager
      if (Build.VERSION.SDK_INT >= 26) manager.createNotificationChannel(NotificationChannel(CHANNEL, context.getString(R.string.downloads_channel), NotificationManager.IMPORTANCE_LOW))
      val builder = if (Build.VERSION.SDK_INT >= 26) Notification.Builder(context, CHANNEL) else @Suppress("DEPRECATION") Notification.Builder(context)
      return builder.setContentTitle(context.getString(R.string.downloads_running)).setSmallIcon(android.R.drawable.stat_sys_download).setOngoing(true).build()
    }

    fun keepAlive(context: Context) {
      try {
        val intent = Intent(context, PorticoDownloadService::class.java)
        if (Build.VERSION.SDK_INT >= 26) context.startForegroundService(intent) else context.startService(intent)
      } catch (_: Exception) {}
    }

    /** Stops a moment later, and only if nothing started meanwhile: a service asked to start must reach the foreground before it may stop. */
    fun done(context: Context) {
      Handler(Looper.getMainLooper()).postDelayed({
        if (PorticoDownloadCenter.running == 0) try { context.stopService(Intent(context, PorticoDownloadService::class.java)) } catch (_: Exception) {}
      }, 1500)
    }
  }
}

/**
 * Carries unfinished downloads on while the app's process is gone (B-14.20). JobScheduler belongs
 * to the system, so the job outlives the process: when a network the download may use is there
 * (any network with "use mobile data" on, otherwise one that is not metered), the system starts
 * the process for it, the manifest is read, and each download that stopped by itself continues
 * from its bytes. The job ends when nothing is moving; it asks to run again when downloads are
 * still waiting, and when the system stops it they stop cleanly and wait for the next run.
 */
class PorticoDownloadJob : JobService() {
  @Volatile private var params: JobParameters? = null

  override fun onStartJob(params: JobParameters?): Boolean {
    if (params == null) return false
    this.params = params
    running = true
    // A user-initiated data transfer (Android 14 and later) shows its notification for as long as it runs.
    if (Build.VERSION.SDK_INT >= 34 && params.isUserInitiatedJob) {
      try { setNotification(params, PorticoDownloadService.NOTIFICATION, PorticoDownloadService.notification(this), JOB_END_NOTIFICATION_POLICY_REMOVE) } catch (_: Exception) {}
    }
    PorticoDownloadCenter.post {
      if (this.params !== params) return@post
      if (!PorticoDownloadCenter.open(applicationContext)) { finish(true); return@post }
      PorticoDownloadCenter.onIdle = { finish() }
      if (!PorticoDownloadCenter.resumeUnfinished()) finish()
    }
    return true
  }

  override fun onStopJob(params: JobParameters?): Boolean {
    this.params = null
    running = false
    PorticoDownloadCenter.post {
      PorticoDownloadCenter.onIdle = null
      PorticoDownloadCenter.interruptAll()
    }
    return true
  }

  /** On the bookkeeping thread: nothing is moving any more. */
  private fun finish(retry: Boolean = PorticoDownloadCenter.hasUnfinished()) {
    val current = params ?: return
    params = null
    running = false
    scheduledCellular = null
    scheduledUserInitiated = false
    PorticoDownloadCenter.onIdle = null
    try { jobFinished(current, retry) } catch (_: Exception) {}
  }

  companion object {
    private const val JOB = 4102

    /** Whether the job is running now: scheduling it again would stop it. */
    @Volatile private var running = false
    /** The network the scheduled job waits for, so a change of "use mobile data" schedules it again. */
    @Volatile private var scheduledCellular: Boolean? = null
    /** Whether the waiting job is a user-initiated data transfer (Android 14 and later). */
    @Volatile private var scheduledUserInitiated = false

    /**
     * Asks the system to carry unfinished downloads on if the process goes, and after a restart
     * of the device (the job is persisted: RECEIVE_BOOT_COMPLETED). One job: left alone while it
     * is running or already waiting for the same kind of network (scheduling the same job again
     * would stop a running one). `userInitiated`: the person started a download with the app in
     * front, so on Android 14 and later it becomes a user-initiated data transfer, which runs as
     * long as the transfer needs instead of in ten-minute turns. Where the system refuses that,
     * the ordinary job is scheduled.
     */
    fun schedule(context: Context, cellular: Boolean, userInitiated: Boolean = false) {
      val wantsUser = userInitiated && Build.VERSION.SDK_INT >= 34
      try {
        val scheduler = context.getSystemService(Context.JOB_SCHEDULER_SERVICE) as JobScheduler
        if (running || (scheduler.getPendingJob(JOB) != null && scheduledCellular == cellular && (!wantsUser || scheduledUserInitiated))) return
        if (wantsUser && submit(scheduler, context, cellular, true)) return
        submit(scheduler, context, cellular, false)
      } catch (_: Exception) {}
    }

    private fun submit(scheduler: JobScheduler, context: Context, cellular: Boolean, user: Boolean): Boolean = try {
      val builder = JobInfo.Builder(JOB, ComponentName(context, PorticoDownloadJob::class.java))
          .setRequiredNetworkType(if (cellular) JobInfo.NETWORK_TYPE_ANY else JobInfo.NETWORK_TYPE_UNMETERED)
          .setBackoffCriteria(30_000, JobInfo.BACKOFF_POLICY_EXPONENTIAL)
          .setPersisted(true)
      if (user && Build.VERSION.SDK_INT >= 34) builder.setUserInitiated(true)
      val ok = scheduler.schedule(builder.build()) == JobScheduler.RESULT_SUCCESS
      if (ok) { scheduledCellular = cellular; scheduledUserInitiated = user }
      ok
    } catch (_: Exception) { false }

    fun cancel(context: Context) {
      if (running) return
      scheduledCellular = null
      scheduledUserInitiated = false
      try { (context.getSystemService(Context.JOB_SCHEDULER_SERVICE) as JobScheduler).cancel(JOB) } catch (_: Exception) {}
    }
  }
}
