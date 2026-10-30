package tv.getportico.portico.native

import android.content.pm.PackageManager
import android.util.Base64
import com.facebook.react.ReactApplication
import com.facebook.react.bridge.Arguments
import com.facebook.react.bridge.Promise
import com.facebook.react.bridge.ReactApplicationContext
import com.facebook.react.bridge.ReactContextBaseJavaModule
import com.facebook.react.bridge.ReactMethod
import com.facebook.react.bridge.ReadableMap
import com.facebook.react.bridge.UiThreadUtil
import com.facebook.react.bridge.WritableArray
import java.io.File
import java.net.URI
import java.security.SecureRandom
import java.util.UUID
import java.util.concurrent.ExecutorService
import java.util.concurrent.Executors
import org.json.JSONArray
import org.json.JSONObject
import tv.getportico.portico.BuildConfig
import tv.getportico.portico.PorticoBoot
import tv.getportico.portico.TelevisionDisplay

/**
 * `NativeModules.PorticoRuntime`: request identifiers, the bounded control transport, and the
 * app's secure records (credentials, the selected viewer, journals, queue records, the profile
 * trust vault). The Android counterpart of the runtime in `Native/PorticoPlayer.m`; every method
 * answers with the same values, codes and messages.
 */
class PorticoRuntime(context: ReactApplicationContext) : ReactContextBaseJavaModule(context) {
  private val tvVoice = PorticoTVVoice(context)
  @ReactMethod fun canTVVoiceSearch(promise: Promise) {promise.resolve(tvVoice.available())}
  @ReactMethod fun startTVVoiceSearch(id: String, promise: Promise) {tvVoice.start(id, promise)}
  @ReactMethod fun cancelTVVoiceSearch(id: String) {tvVoice.cancel(id)}

  private val store = PorticoRecordStore.get(context)
  private val trust = PorticoProfileTrust(store)
  private val random = SecureRandom()
  private val launchId = UUID.randomUUID().toString()

  override fun getName(): String = "PorticoRuntime"

  override fun getConstants(): Map<String, Any> {
    val values = HashMap<String, Any>()
    values["launchId"] = launchId
    // Boot's t = 0 (DESIGN "Ready, and what boot waits for"): when the person started the app.
    values["launchStartedAt"] = PorticoBoot.startedAt.toDouble()
    values["pictureInPictureSupported"] = android.os.Build.VERSION.SDK_INT >= 26 && reactApplicationContext.packageManager.hasSystemFeature(PackageManager.FEATURE_PICTURE_IN_PICTURE)
    // The layout this process was started with (the Layout setting, or what the device says).
    values["television"] = TelevisionDisplay.isTelevision
    values["layout"] = TelevisionDisplay.choice(reactApplicationContext)
    hostedUrl()?.let { values["hostedUrl"] = it }
    if (BuildConfig.DEBUG) testLogin()?.let { values["testLogin"] = it }
    return values
  }

  /** React has drawn its first frame: the native boot view goes, without animation. */
  @ReactMethod fun bootDrawn() {PorticoBoot.reactDrew()}

  /** The Portico Account origin the build names (`-PporticoHostedUrl=…`), as Info.plist's PorticoHostedURL. */
  private fun hostedUrl(): String? = try {
    val context = reactApplicationContext
    context.packageManager.getApplicationInfo(context.packageName, PackageManager.GET_META_DATA)
        .metaData?.getString("PorticoHostedURL")?.takeIf { it.isNotEmpty() }
  } catch (_: Exception) {
    null
  }

  /**
   * Debug builds only: the sign-in fixture of the credential probe, read from the app's own cache
   * directory (`adb shell run-as tv.getportico.portico` can place it; nothing else can).
   */
  private fun testLogin(): Map<String, Any?>? = try {
    val file = File(reactApplicationContext.cacheDir, "portico-test-login.json")
    if (!file.isFile || file.length() > 65536) null
    else (PorticoProfileTrust.parse(file.readBytes()) as? JSONObject)?.let { json ->
      val out = HashMap<String, Any?>()
      for (key in json.keys()) json.opt(key).let { if (it is String || it is Boolean || it is Number) out[key] = it }
      out
    }
  } catch (_: Exception) {
    null
  }

  override fun invalidate() {
    tvVoice.dispose()
    PorticoTransport.cancelOwnedBy(this)
    PorticoEventStream.cancelOwnedBy(this)
    super.invalidate()
  }

  /**
   * Saves the Layout setting (`automatic`, `television` or `touch`) and starts the app again: the
   * layout and the design-space density are chosen before anything is drawn (B-19.10).
   */
  @ReactMethod
  fun setLayout(choice: String, promise: Promise) {
    if (choice != "automatic" && choice != "television" && choice != "touch") { promise.reject("invalid_request", "Unknown layout."); return }
    val context = reactApplicationContext
    if (!context.getSharedPreferences(TelevisionDisplay.PREFERENCES, android.content.Context.MODE_PRIVATE).edit().putString("layout", choice).commit()) {
      promise.reject("storage_unavailable", "The layout couldn't be saved.")
      return
    }
    promise.resolve(null)
    UiThreadUtil.runOnUiThread {
      val launch = context.packageManager.getLaunchIntentForPackage(context.packageName)
      if (launch != null) {
        launch.addFlags(android.content.Intent.FLAG_ACTIVITY_NEW_TASK or android.content.Intent.FLAG_ACTIVITY_CLEAR_TASK)
        context.startActivity(launch)
      }
      android.os.Process.killProcess(android.os.Process.myPid())
    }
  }

  @ReactMethod
  fun recordTestEvent(json: String) {
    if (!BuildConfig.DEBUG) return
    storage.execute {
      try { File(reactApplicationContext.cacheDir, "portico-journey.jsonl").appendText(json + "\n") } catch (_: Exception) {}
    }
  }

  // --- identifiers -----------------------------------------------------------------------

  @ReactMethod(isBlockingSynchronousMethod = true)
  fun playbackRequestId(): String = UUID.randomUUID().toString()

  @ReactMethod
  fun newRequestId(promise: Promise) = promise.resolve(UUID.randomUUID().toString())

  @ReactMethod(isBlockingSynchronousMethod = true)
  fun playbackRandomBytes(count: Double): WritableArray? {
    val n = count.toInt()
    if (n < 1 || n > 64 || n.toDouble() != count) return null
    val bytes = ByteArray(n).also { random.nextBytes(it) }
    val result = Arguments.createArray()
    for (b in bytes) result.pushInt(b.toInt() and 0xff)
    return result
  }

  // --- transport -------------------------------------------------------------------------

  @ReactMethod
  fun queueRequest(identifier: String?, url: String?, method: String?, headers: ReadableMap?, body: String?, promise: Promise) =
      boundedRequest(identifier, url, method, headers, body, fullAPI = false, promise)

  @ReactMethod
  fun networkRequest(identifier: String?, url: String?, method: String?, headers: ReadableMap?, body: String?, promise: Promise) =
      boundedRequest(identifier, url, method, headers, body, fullAPI = true, promise)

  /** Only subtitle endpoints can upload bytes, and only from the picker's private staging root. */
  @ReactMethod
  fun networkUploadRequest(identifier: String?, url: String?, method: String?, headers: ReadableMap?, metadata: String?, fileUri: String?, fileName: String?, companionUri: String?, companionName: String?, promise: Promise) {
    try {
      val target = PorticoTransport.target(url)
      val path = target?.encodedPath ?: ""
      val allowed = method == "POST" && Regex("^/v1/items/[A-Za-z0-9_-]+/subtitles$").matches(path) ||
          method == "PUT" && Regex("^/v1/items/[A-Za-z0-9_-]+/subtitles/[A-Za-z0-9_-]+$").matches(path)
      if (target == null || !allowed || identifier.isNullOrEmpty() || identifier.length > 128 || metadata == null || metadata.length > 16384 || metadata.toByteArray(Charsets.UTF_8).size > 16384)
        throw IllegalArgumentException()
      val root = File(reactApplicationContext.cacheDir, "portico-subtitle-uploads").canonicalFile
      fun staged(uri: String?, name: String?): Pair<File, String> {
        if (uri == null || name.isNullOrEmpty() || name.length > 1024 || name.toByteArray(Charsets.UTF_8).size > 1024) throw IllegalArgumentException()
        val parsed = URI(uri)
        if (parsed.scheme != "file" || !uri.startsWith("file://") || !parsed.rawAuthority.isNullOrEmpty() || parsed.rawQuery != null || parsed.rawFragment != null) throw IllegalArgumentException()
        val file = File(parsed).canonicalFile
        if (!file.path.startsWith(root.path + File.separator) || !file.isFile || file.length() <= 0) throw IllegalArgumentException()
        val safeName = name.map { if (it == '"' || it == '\\' || it.isISOControl()) '_' else it }.joinToString("")
        return Pair(file, safeName)
      }
      val files = mutableListOf(staged(fileUri, fileName))
      if (companionUri != null || companionName != null) files.add(staged(companionUri, companionName))
      if (files.sumOf { it.first.length() } > 33554432L) throw IllegalArgumentException()
      val request = PorticoTransport.uploadRequest(target, method!!, headers, metadata, files) ?: throw IllegalArgumentException()
      PorticoTransport.submit(this, identifier, request, PorticoTransport.DEFAULT_LIMIT, "queue_transport", "Too many queue requests.", promise)
    } catch (_: Exception) { promise.reject("queue_transport", "Invalid subtitle upload request.") }
  }

  /** Actual bounded Reading bytes over the existing trust/cancel transport. */
  @ReactMethod
  fun networkBytesRequest(identifier: String?, url: String?, method: String?, headers: ReadableMap?, byteLimit: Double, promise: Promise) {
    val target = PorticoTransport.target(url)
    val path = target?.encodedPath ?: ""
    val reading = Regex("^/v1/reading/sessions/[A-Za-z0-9_-]{1,128}/(?:source|assets|pages/[0-9]{1,10})$").matches(path)
    val limit = byteLimit.toLong()
    val request = if (target != null && reading && (method == "GET" || method == "HEAD" && path.endsWith("/source")) &&
        limit in 1..33554432L && limit.toDouble() == byteLimit && identifier != null && identifier.length <= 128)
      PorticoTransport.request(target, method!!, headers, null) else null
    if (request == null || identifier == null) {promise.reject("queue_transport", "Invalid Reading byte request."); return}
    PorticoTransport.submit(this, identifier, request, limit, "queue_transport", "Too many requests.", promise, binary = true)
  }

  /** Header-first from-start Reading PDF on the same trust/cancel transport. */
  @ReactMethod
  fun networkStreamOpen(identifier: String?, url: String?, headers: ReadableMap?, promise: Promise) {
    val target = PorticoTransport.target(url)
    val request = if (target != null && target.encodedQuery == null && Regex("^/v1/reading/sessions/[A-Za-z0-9_-]{1,128}/source$").matches(target.encodedPath) &&
        identifier != null && identifier.isNotEmpty() && identifier.length <= 128) PorticoTransport.request(target, "GET", headers, null) else null
    if (request == null || request.header("Range") != null || identifier == null) {promise.reject("queue_transport", "Invalid Reading stream request."); return}
    PorticoTransport.submit(this, identifier, request, 49152L, "queue_transport", "Too many requests.", promise, binary = true, streaming = true)
  }

  @ReactMethod
  fun networkStreamRead(identifier: String?, promise: Promise) {
    if (identifier == null || identifier.isEmpty() || identifier.length > 128) {promise.reject("cancelled", "Reading stream cancelled."); return}
    PorticoTransport.readStream(this, identifier, promise)
  }

  @ReactMethod
  fun openNetworkStream(identifier: String?, url: String?, headers: ReadableMap?, promise: Promise) =
      PorticoEventStream.open(this, identifier, url, headers, promise)

  @ReactMethod
  fun readNetworkStream(identifier: String?, promise: Promise) = PorticoEventStream.read(identifier, promise)

  @ReactMethod
  fun cancelNetworkStream(identifier: String?) = PorticoEventStream.cancel(identifier)

  // The JSON envelope exists only on the JS/native bridge, never on the wire.
  private fun avatarUploadPath(path: String, method: String?) =
      method == "POST" && Regex("^/v1/(direct/profiles/[A-Za-z0-9_-]{1,128}|admin/access/members/[A-Za-z0-9_-]{1,128}/profiles/[A-Za-z0-9_-]{1,128})/avatar$").matches(path)

  private fun avatarUploadBody(body: String?): ByteArray? = try {
    if (body == null || body.length > 5592448) null else {
      val envelope = JSONObject(body)
      val encoded = envelope.opt("nativeAvatarBase64")
      if (envelope.length() != 1 || encoded !is String) null else
        Base64.decode(encoded, Base64.NO_WRAP).takeIf { it.isNotEmpty() && it.size <= 4194304 }
    }
  } catch (_: Exception) { null }

  private fun boundedRequest(identifier: String?, url: String?, method: String?, headers: ReadableMap?, body: String?, fullAPI: Boolean, promise: Promise) {
    val target = PorticoTransport.target(url)
    val path = target?.encodedPath ?: ""
    val avatar = avatarUploadPath(path, method)
    val data = if (avatar) avatarUploadBody(body) else body?.toByteArray(Charsets.UTF_8)
    val pathAllowed = if (fullAPI) path.startsWith("/v1/") || path.startsWith("/v2/") else path.startsWith("/v2/") || SUBTITLES.matches(path)
    val methodAllowed = method in (if (fullAPI) API_METHODS else QUEUE_METHODS)
    val request = if (target != null && pathAllowed && methodAllowed && (if (avatar) data != null else (data?.size ?: 0) <= 1048576) && identifier != null && identifier.length <= 128)
      PorticoTransport.request(target, method!!, headers, data) else null
    if (request == null || identifier == null) {
      promise.reject("queue_transport", "Invalid queue request.")
      return
    }
    // API reads that grow with the library (Home rows, queue views, admin pages) get 8 MB; writes
    // and the narrow queue-control surface keep the 1 MB bound.
    val limit = if (fullAPI && method == "GET") PorticoTransport.READ_LIMIT else PorticoTransport.DEFAULT_LIMIT
    PorticoTransport.submit(this, identifier, request, limit, "queue_transport", "Too many queue requests.", promise)
  }

  /** Passwords, PINs and profile proofs: TLS only (or this device itself), and never a redirect. */
  @ReactMethod
  fun profileRequest(identifier: String?, url: String?, method: String?, headers: ReadableMap?, body: String?, promise: Promise) {
    val target = PorticoTransport.target(url)
    val avatar = avatarUploadPath(target?.encodedPath ?: "", method)
    val data = if (avatar) avatarUploadBody(body) else body?.toByteArray(Charsets.UTF_8)
    val secure = target != null && (target.isHttps || target.host in LOOPBACK)
    val request = if (target != null && secure && target.encodedPath.startsWith("/v1/") && method in PROFILE_METHODS &&
        (if (avatar) data != null else (data?.size ?: 0) <= 65536) && identifier != null && identifier.length <= 128)
      PorticoTransport.request(target, method!!, headers, data) else null
    if (request == null || identifier == null) {
      promise.reject("profile_transport", "A secure, bounded profile request is required.")
      return
    }
    PorticoTransport.submit(this, identifier, request, PorticoTransport.DEFAULT_LIMIT, "profile_transport", "Too many requests are in progress.", promise)
  }

  @ReactMethod
  fun cancelQueueRequest(identifier: String?) {
    if (identifier != null) PorticoTransport.cancel(identifier)
  }

  /**
   * Rejects `secure_records_key_lost` when the key that seals this device's records is gone while
   * the records remain and the system cannot say so for certain (see [RecordKeyLostException]):
   * nothing has been removed, and the app asks the person whether to start again. Every other
   * failure stays `secure_storage`: not readable now.
   */
  @ReactMethod
  fun prepareSecureRecords(promise: Promise) = storage.execute {
    try { promise.resolve(store.prepareSecureRecords()) }
    catch (_: RecordKeyLostException) { promise.reject("secure_records_key_lost", "This device can no longer read its saved records.") }
    catch (_: Exception) { promise.reject("secure_storage", "Secure records could not be prepared.") }
  }

  /**
   * The person confirmed starting again. Resolves true when the records were reset, false when
   * the key answered after all and nothing was removed. Rejects, removing nothing, when the
   * Keystore cannot be asked.
   */
  @ReactMethod
  fun resetSecureRecords(promise: Promise) = storage.execute {
    try { promise.resolve(store.resetAfterConfirmation()) }
    catch (_: Exception) { promise.reject("secure_storage", "Secure records could not be reset.") }
  }

  /**
   * Starts the app's JavaScript again, as a fresh launch would: after a reset nothing that was
   * read from the old records may stay in memory. Resolves false where that is not possible, and
   * the app then asks the person to close and reopen it.
   */
  @ReactMethod
  fun restart(promise: Promise) {
    val host = (reactApplicationContext.applicationContext as? ReactApplication)?.reactHost
    if (host == null) { promise.resolve(false); return }
    promise.resolve(true)
    UiThreadUtil.runOnUiThread { host.reload("Portico restarted itself") }
  }

  // --- records ---------------------------------------------------------------------------

  /** Missing resolves [missing]; unreadable (or not text) rejects, and nothing is repaired. */
  private fun resolveRead(read: RecordRead, missing: Any?, failure: String, promise: Promise) {
    when (read) {
      RecordRead.Missing -> promise.resolve(missing)
      RecordRead.Unreadable -> promise.reject("secure_storage", failure)
      is RecordRead.Value -> promise.resolve(read.bytes.toString(Charsets.UTF_8))
    }
  }

  private fun load(service: String, account: String?, missing: Any?, failure: String, promise: Promise) = storage.execute {
    if (account == null) promise.reject("secure_storage", failure) else resolveRead(store.read(service, account), missing, failure, promise)
  }

  private fun save(service: String, account: String?, json: String?, sizeCode: String, sizeMessage: String, failure: String, promise: Promise) = storage.execute {
    val data = json?.toByteArray(Charsets.UTF_8)
    if (data == null || data.size > 262144) promise.reject(sizeCode, sizeMessage)
    else if (account != null && store.write(service, account, data)) promise.resolve(true)
    else promise.reject("secure_storage", failure)
  }

  /**
   * A record that is there but is not one this app could have written rejects as
   * `secure_record_damaged` (the Apple runtime's code too): waiting will not mend it. Every other
   * failure, a Keystore that will not answer included, stays `secure_storage`: not readable now.
   */
  @ReactMethod
  fun userMutationTransaction(operation: String, json: String, promise: Promise) = storage.execute {
    try { promise.resolve(PorticoUserMutationStore.transact(store, operation, org.json.JSONObject(json))) }
    catch(e: Exception) { promise.reject("mutation_store_unavailable", "User changes could not be saved.", e) }
  }

  @ReactMethod
  fun loadQueueRecord(key: String?, promise: Promise) = storage.execute {
    val failure = "Queue recovery could not be read."
    if (key == null) promise.reject("secure_storage", failure)
    else store.transaction {
      val read = store.read(PorticoRecordStore.QUEUES, key)
      if (read is RecordRead.Unreadable && store.damaged(PorticoRecordStore.QUEUES, key)) promise.reject("secure_record_damaged", failure)
      else resolveRead(read, null, failure, promise)
    }
  }

  @ReactMethod
  fun saveQueueRecord(key: String?, json: String?, promise: Promise) =
      save(PorticoRecordStore.QUEUES, key, json, "queue_journal_size", "Queue recovery is full.", "Queue recovery could not be saved.", promise)

  @ReactMethod
  fun loadCredentials(origin: String?, promise: Promise) =
      load(PorticoRecordStore.CREDENTIALS, origin, null, "Account credentials could not be read.", promise)

  @ReactMethod
  fun saveCredentials(origin: String?, json: String?, promise: Promise) =
      save(PorticoRecordStore.CREDENTIALS, origin, json, "credential_size", "Account credential storage is full.", "Account credentials could not be saved.", promise)

  @ReactMethod
  fun loadJournal(scope: String?, promise: Promise) =
      load(PorticoRecordStore.PLAYBACK, scope, "[]", "Playback recovery could not be read.", promise)

  @ReactMethod
  fun saveJournal(scope: String?, json: String?, promise: Promise) =
      save(PorticoRecordStore.PLAYBACK, scope, json, "journal_size", "Playback recovery is full.", "Playback recovery could not be saved.", promise)

  // --- the selected viewer ---------------------------------------------------------------

  private val flags by lazy { reactApplicationContext.getSharedPreferences("portico", 0) }

  /** Set (and committed) before a removal or after a save that could not be verified. */
  private fun quarantine(value: Boolean) = flags.edit().putBoolean(QUARANTINED, value).commit()

  @ReactMethod
  fun loadSelectedViewer(promise: Promise) = storage.execute {
    store.transaction {
      if (flags.getBoolean(QUARANTINED, false)) promise.resolve(null)
      else resolveRead(store.read(PorticoRecordStore.CREDENTIALS, SELECTED_VIEWER), null, "The saved server selection could not be read.", promise)
    }
  }

  @ReactMethod
  fun saveSelectedViewer(json: String?, promise: Promise) = storage.execute {
    store.transaction {
      val removing = json == "null"
      if (removing) quarantine(true)
      val data = json?.toByteArray(Charsets.UTF_8)
      if (data == null || data.size > 65536) {
        promise.reject("secure_storage", "The server credential record is invalid.")
        return@transaction
      }
      // The store confirms a write by reading it back, and a removal by finding nothing. When
      // that fails, what is stored now says which failure it was: the record as it was before
      // (nothing was saved), or something else (it cannot be trusted until the next sign-in).
      val before = store.read(PorticoRecordStore.CREDENTIALS, SELECTED_VIEWER)
      if (!store.write(PorticoRecordStore.CREDENTIALS, SELECTED_VIEWER, if (removing) null else data)) {
        val after = store.read(PorticoRecordStore.CREDENTIALS, SELECTED_VIEWER)
        val untouched = when (before) {
          is RecordRead.Value -> after is RecordRead.Value && after.bytes.contentEquals(before.bytes)
          else -> after == before
        }
        if (untouched) {
          promise.reject("secure_storage", "The server credential could not be saved. The account credential is unchanged.")
        } else {
          quarantine(true)
          promise.reject("secure_storage", "Server credential storage could not be verified. Sign in to the server again.")
        }
        return@transaction
      }
      if (!removing) quarantine(false)
      promise.resolve(true)
    }
  }

  // --- the profile trust vault -----------------------------------------------------------
  // Each PIN attempt, including a failed decrypt, is durably charged before the expensive
  // operation. A restart or a second JavaScript runtime cannot reset the delay.

  @ReactMethod
  fun profileInstallation(promise: Promise) = storage.execute {
    store.transaction {
      val value = trust.installation()
      if (value != null) promise.resolve(value)
      else promise.reject("secure_storage", "This device's profile installation could not be read.")
    }
  }

  @ReactMethod
  fun saveProfileTrust(scope: String?, metadataJSON: String?, payloadJSON: String?, pin: String?, promise: Promise) = storage.execute {
    store.transaction {
      val key = trust.recordKey(scope)
      val installation = trust.installation()
      val metadata = PorticoProfileTrust.parse(metadataJSON?.toByteArray(Charsets.UTF_8))
      if (key == null || scope == null || metadataJSON == null || installation == null || !trust.metadata(scope, metadata, installation) ||
          pin == null || !trust.pin(pin, PorticoProfileTrust.flag((metadata as JSONObject).opt("pinRequired")))) {
        promise.reject("profile_trust_invalid", "A current, fully scoped selection and its PIN are required to trust this profile.")
        return@transaction
      }
      val index = trust.activeIndex()
      if (index == null) {
        promise.reject("secure_storage", "Trusted profiles could not be read.")
        return@transaction
      }
      if (index.size >= 64 && key !in index) {
        promise.reject("profile_trust_full", "Remove an unused trusted profile before remembering another.")
        return@transaction
      }
      val record = trust.envelope(scope, metadataJSON, payloadJSON, pin)
      if (record == null) {
        promise.reject("secure_storage", "The profile proof could not be protected on this device.")
        return@transaction
      }
      val old = trust.read(key)
      if (trust.status != PorticoProfileTrust.Status.OK && trust.status != PorticoProfileTrust.Status.NOT_FOUND) {
        promise.reject("secure_storage", "The existing profile proof could not be read.")
        return@transaction
      }
      var saved = trust.write(key, PorticoProfileTrust.json(record))
      if (saved == PorticoProfileTrust.Status.OK && key !in index) saved = trust.write("index.v1", PorticoProfileTrust.json(index + key))
      if (saved != PorticoProfileTrust.Status.OK) {
        trust.write(key, old)
        trust.write("index.v1", PorticoProfileTrust.json(index))
        promise.reject("secure_storage", "This profile selection could not be remembered.")
        return@transaction
      }
      promise.resolve(true)
    }
  }

  @ReactMethod
  fun listProfileTrust(promise: Promise) = storage.execute {
    store.transaction {
      val index = trust.activeIndex()
      if (index == null) {
        promise.reject("secure_storage", "Trusted profiles could not be read.")
        return@transaction
      }
      val out = JSONArray()
      val now = System.currentTimeMillis().toDouble()
      for (key in index) {
        val raw = trust.read(key)
        if (trust.status == PorticoProfileTrust.Status.NOT_FOUND) continue
        if (trust.status != PorticoProfileTrust.Status.OK) {
          promise.reject("secure_storage", "A trusted profile could not be read.")
          return@transaction
        }
        val record = PorticoProfileTrust.parse(raw)
        val metadata = if (record is JSONObject && PorticoProfileTrust.text(record.opt("metadata"), 8192))
          PorticoProfileTrust.parse(record.getString("metadata").toByteArray(Charsets.UTF_8)) else null
        if (metadata !is JSONObject) {
          promise.reject("secure_storage", "A trusted profile record is invalid.")
          return@transaction
        }
        val expires = (metadata.opt("expiresAtMs") as? Number)?.toDouble()
        if (expires != null && expires > now) out.put(metadata)
      }
      promise.resolve(out.toString())
    }
  }

  @ReactMethod
  fun unlockProfileTrust(scope: String?, pin: String?, promise: Promise) = storage.execute {
    store.transaction {
      val key = trust.recordKey(scope)
      if (key == null || scope == null) {
        promise.reject("profile_trust_invalid", "Choose a previously trusted profile.")
        return@transaction
      }
      val record = PorticoProfileTrust.parse(trust.read(key))
      if (trust.status != PorticoProfileTrust.Status.OK || record !is JSONObject) {
        promise.reject("profile_trust_missing", "This profile is not available for offline selection on this device.")
        return@transaction
      }
      if (!PorticoProfileTrust.text(record.opt("metadata"), 8192) || !PorticoProfileTrust.attempts(record)) {
        promise.reject("secure_storage", "The trusted profile record is invalid. Select it online again.")
        return@transaction
      }
      val metadata = PorticoProfileTrust.parse(record.getString("metadata").toByteArray(Charsets.UTF_8))
      val installation = trust.installation()
      if (installation == null || !trust.metadata(scope, metadata, installation)) {
        promise.reject("profile_trust_expired", "This trusted profile expired or belongs to another installation. Select it online again.")
        return@transaction
      }
      val now = System.currentTimeMillis() / 1000.0
      if (record.getDouble("lockedUntil") > now) {
        promise.reject("profile_pin_locked", "Profile PIN attempts are temporarily locked. Try again later.")
        return@transaction
      }
      if (pin == null || !trust.pin(pin, PorticoProfileTrust.flag((metadata as JSONObject).opt("pinRequired")))) {
        promise.reject("profile_pin_required", "Enter this profile's four-digit PIN.")
        return@transaction
      }
      val attempts = minOf(1000, record.getInt("attempts") + 1)
      val delay = if (attempts >= 10) 900 else if (attempts >= 5) 300 else if (attempts >= 3) 30 else 2
      record.put("attempts", attempts).put("lockedUntil", now + delay)
      if (trust.write(key, PorticoProfileTrust.json(record)) != PorticoProfileTrust.Status.OK) {
        promise.reject("secure_storage", "The profile unlock attempt could not be saved.")
        return@transaction
      }
      val payload = trust.decrypt(record, scope, pin)
      if (payload == null) {
        val wrong = trust.status == PorticoProfileTrust.Status.AUTH_FAILED
        promise.reject(
            if (wrong) "profile_pin_required" else "secure_storage",
            if (wrong) "That profile PIN is not correct. Try again after the delay." else "The protected profile proof could not be read.")
        return@transaction
      }
      record.put("attempts", 0).put("lockedUntil", 0)
      if (trust.write(key, PorticoProfileTrust.json(record)) != PorticoProfileTrust.Status.OK) {
        promise.reject("secure_storage", "The profile unlock could not be saved.")
        return@transaction
      }
      promise.resolve(payload)
    }
  }

  @ReactMethod
  fun removeProfileTrust(scope: String?, promise: Promise) = storage.execute {
    store.transaction {
      val key = trust.recordKey(scope)
      if (key == null) {
        promise.reject("profile_trust_invalid", "A complete profile scope is required.")
        return@transaction
      }
      var removed = trust.write(key, null)
      if (removed == PorticoProfileTrust.Status.OK) {
        val index = trust.index()
        removed = if (index != null) trust.write("index.v1", PorticoProfileTrust.json(index - key)) else trust.status
      }
      if (removed == PorticoProfileTrust.Status.OK) promise.resolve(true)
      else promise.reject("secure_storage", "The trusted profile could not be removed.")
    }
  }

  companion object {
    private const val SELECTED_VIEWER = "portico:selected-viewer:v1"
    private const val QUARANTINED = "PorticoSelectedViewerQuarantined"
    private val SUBTITLES = Regex("^/v1/items/[A-Za-z0-9_-]+/playback/[A-Za-z0-9_-]+/subtitles$")
    private val API_METHODS = setOf("GET", "POST", "PUT", "PATCH", "DELETE", "HEAD")
    private val QUEUE_METHODS = setOf("GET", "POST", "PUT")
    private val PROFILE_METHODS = setOf("GET", "POST", "PUT", "PATCH", "DELETE")
    private val LOOPBACK = setOf("localhost", "127.0.0.1", "::1")

    /**
     * Keystore and file work happens here, in order, off the thread the other native modules
     * answer on: a PIN check (200,000 rounds) must not hold up the player.
     */
    private val storage: ExecutorService = Executors.newSingleThreadExecutor { Thread(it, "portico-records") }
  }
}
