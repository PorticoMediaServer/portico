package tv.getportico.portico.native

import android.content.Context
import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities
import android.net.LinkProperties
import android.net.nsd.NsdManager
import android.net.nsd.NsdServiceInfo
import android.os.Build
import android.os.Handler
import android.os.Looper
import android.util.Base64
import com.facebook.react.bridge.Arguments
import com.facebook.react.bridge.Promise
import com.facebook.react.bridge.ReactApplicationContext
import com.facebook.react.bridge.ReactMethod
import com.facebook.react.bridge.ReadableMap
import com.facebook.react.bridge.ReadableArray
import com.facebook.react.bridge.ReadableType
import com.facebook.react.bridge.WritableMap
import java.io.IOException
import java.net.Inet6Address
import java.util.concurrent.TimeUnit
import okhttp3.Call
import okhttp3.Callback
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response
import java.net.InetAddress
import java.nio.ByteBuffer
import java.nio.charset.CodingErrorAction
import java.security.cert.X509Certificate
import org.json.JSONObject
import java.security.MessageDigest
import java.security.SecureRandom
import java.util.Calendar
import java.util.TimeZone
import java.util.UUID
import javax.crypto.Cipher
import javax.crypto.Mac
import javax.crypto.spec.GCMParameterSpec
import javax.crypto.spec.SecretKeySpec
import org.bouncycastle.crypto.digests.SHA256Digest
import org.bouncycastle.crypto.generators.HKDFBytesGenerator
import org.bouncycastle.crypto.params.HKDFParameters
import org.bouncycastle.math.ec.rfc7748.X25519
import org.bouncycastle.math.ec.rfc8032.Ed25519

/**
 * `NativeModules.PorticoNetwork`: route identity checks, the TV sign-in key and grant, nearby
 * discovery and advertisement, and the network path monitor. The Android counterpart of
 * `Native/PorticoNetwork.swift`: the same service types and TXT fields, the same event.
 */
class PorticoNetwork(context: ReactApplicationContext) : PorticoEventModule(context) {
  private class Failure : Exception()

  private val store = PorticoRecordStore.get(context)
  private val main = Handler(Looper.getMainLooper())
  private val nsd by lazy { context.getSystemService(Context.NSD_SERVICE) as NsdManager }
  private val connectivity by lazy { context.getSystemService(Context.CONNECTIVITY_SERVICE) as ConnectivityManager }

  @Volatile private var invalidated = false
  private var monitor: ConnectivityManager.NetworkCallback? = null
  private var lastPath = ""
  private val searches = HashMap<String, Search>()
  private var tvService: Advertisement? = null
  private var tvExpiry: Runnable? = null
  private var playerService: Advertisement? = null

  override fun getName(): String = "PorticoNetwork"

  override fun invalidate() {
    val open: List<Search>
    synchronized(this) {
      invalidated = true
      monitor?.let { try { connectivity.unregisterNetworkCallback(it) } catch (_: Exception) {} }
      monitor = null
      open = searches.values.toList()
    }
    for (search in open) search.finish("cancelled")
    stopTVAdvertisement()
    stopPlayerAdvertisement()
    super.invalidate()
  }

  @ReactMethod fun addListener(eventName: String) {}

  @ReactMethod fun removeListeners(count: Double) {}

  // --- identity --------------------------------------------------------------------------

  @ReactMethod
  fun sha256(payload: String?, promise: Promise) {
    try {
      promise.resolve(encodeURL(MessageDigest.getInstance("SHA-256").digest(decodeURL(payload))))
    } catch (_: Exception) {
      promise.reject("crypto_input", "Invalid identity data.")
    }
  }

  /** An Ed25519 signature by a server's pinned key. */
  @ReactMethod
  fun verify(publicKey: String?, signature: String?, payload: String?, promise: Promise) {
    try {
      val key = decodeURL(publicKey, count = 32)
      val sig = decodeURL(signature, count = 64)
      val message = decodeURL(payload)
      promise.resolve(Ed25519.verify(sig, 0, key, 0, message, 0, message.size))
    } catch (_: Exception) {
      promise.reject("crypto_input", "Invalid signed identity.")
    }
  }

  // --- the owner's own certificate ------------------------------------------------------
  // See PorticoServerTrust. JavaScript decides when to pin (after the server proves its identity
  // over the certificate); these only report, prove over and store.

  /** The certificate this device most recently refused at an https origin, or null. */
  @ReactMethod
  fun refusedCertificate(origin: String?, promise: Promise) {
    promise.resolve(PorticoServerTrust.refused(origin)?.let { Arguments.createMap().apply { putString("fingerprint", it) } })
  }

  @ReactMethod
  fun pinnedCertificate(origin: String?, promise: Promise) {
    promise.resolve(PorticoServerTrust.pinned(origin)?.let { Arguments.createMap().apply { putString("fingerprint", it.fingerprint); putString("serverId", it.serverId) } })
  }

  @ReactMethod
  fun pinCertificate(origin: String?, fingerprint: String?, serverId: String?, promise: Promise) {
    if (PorticoServerTrust.pin(origin, fingerprint, serverId)) promise.resolve(true)
    else promise.reject("certificate_storage", "certificate_storage")
  }

  @ReactMethod
  fun forgetCertificate(origin: String?) = PorticoServerTrust.forget(origin)

  @ReactMethod
  fun forgetCapturedCertificate(origin: String?, fingerprint: String?, serverId: String?, promise: Promise) {
    if (PorticoServerTrust.forgetCaptured(origin, fingerprint, serverId)) promise.resolve(true)
    else promise.reject("certificate_changed", "certificate_changed")
  }

  @ReactMethod
  fun forgetServerCertificates(serverId: String?, origins: ReadableArray?, promise: Promise) {
    val values = origins?.toArrayList()?.mapNotNull { it as? String }
    if (serverId == null || origins == null || values == null || values.size != origins.size() || values.size > 256 ||
        !PorticoServerTrust.forgetServer(serverId, values)) promise.reject("certificate_storage", "certificate_storage")
    else promise.resolve(null)
  }

  /** Every certificate pinned on this device: its `host:port`, fingerprint and proving server. */
  @ReactMethod
  fun pinnedCertificates(promise: Promise) {
    val out = Arguments.createArray()
    for ((address, pin) in PorticoServerTrust.all()) {
      out.pushMap(Arguments.createMap().apply { putString("address", address); putString("fingerprint", pin.fingerprint); putString("serverId", pin.serverId) })
    }
    promise.resolve(out)
  }

  /**
   * The identity proof, and only it, over one named certificate: a POST of the caller's JSON to
   * `<origin>/v1/networking/identity-proof`, bound to that exact observed leaf even
   * if another leaf is system-trusted. No credentials/cookies, no redirects, 8 KB, 5 seconds.
   * The returned TLS fingerprint comes from this response's handshake, never its headers.
   */
  @ReactMethod
  fun proofRequest(origin: String?, fingerprint: String?, body: String?, promise: Promise) {
    val base = origin?.toHttpUrlOrNull()
    val target = if (base?.scheme == "https" && base.username.isEmpty() && base.password.isEmpty() &&
        base.encodedPath == "/" && base.query == null && base.fragment == null &&
        origin == base.toString().removeSuffix("/")) base.newBuilder().encodedPath("/v1/networking/identity-proof").build() else null
    val validBody = if (body == null || body.toByteArray(Charsets.UTF_8).size > 4096) false else try {
      val document = JSONObject(body ?: "")
      val nonce = document.optString("nonce", "")
      val decoded = if (CERT_FINGERPRINT.matches(nonce)) Base64.decode(nonce, Base64.URL_SAFE or Base64.NO_WRAP or Base64.NO_PADDING) else null
      document.length() == 2 && document.opt("baseUrl") == origin && document.opt("nonce") is String &&
          decoded != null && decoded.size == 32 && encodeURL(decoded) == nonce
    } catch (_: Exception) { false }
    if (invalidated || origin == null || target == null || fingerprint == null || !CERT_FINGERPRINT.matches(fingerprint) || body == null ||
        body.toByteArray(Charsets.UTF_8).size > 4096 || !validBody) {
      promise.reject("certificate_proof", "certificate_proof"); return
    }
    val client = PorticoServerTrust.proofClient(OkHttpClient.Builder().followRedirects(false).followSslRedirects(false).cache(null)
        .retryOnConnectionFailure(false).cookieJar(okhttp3.CookieJar.NO_COOKIES).callTimeout(5, TimeUnit.SECONDS), origin, fingerprint)
    val request = Request.Builder().url(target).header("Accept", "application/json; portico-route-proof=2")
        .post(body.toByteArray(Charsets.UTF_8).toRequestBody("application/json".toMediaType())).build()
    client.newCall(request).enqueue(object : Callback {
      override fun onFailure(call: Call, e: IOException) = promise.reject("certificate_proof", "certificate_proof")
      override fun onResponse(call: Call, response: Response) {
        response.use {
          try {
            val bytes = it.body?.source()?.let { source -> source.request(8193); source.buffer.readByteArray(minOf(source.buffer.size, 8193)) } ?: ByteArray(0)
            val leaf = it.handshake?.peerCertificates?.firstOrNull() as? X509Certificate ?: throw IOException()
            val observed = encodeURL(MessageDigest.getInstance("SHA-256").digest(leaf.encoded))
            if (invalidated || bytes.size > 8192 || it.isRedirect || observed != fingerprint) throw IOException()
            val headers = Arguments.createMap()
            for (name in it.headers.names()) headers.putString(name, it.headers.values(name).joinToString(", "))
            promise.resolve(Arguments.createMap().apply {
              putInt("status", it.code); putMap("headers", headers)
              putString("tlsLeafFingerprint", observed)
              putString("body", Charsets.UTF_8.newDecoder().onMalformedInput(CodingErrorAction.REPORT).onUnmappableCharacter(CodingErrorAction.REPORT).decode(ByteBuffer.wrap(bytes)).toString())
            })
          } catch (_: Exception) {
            promise.reject("certificate_proof", "certificate_proof")
          }
        }
      }
    })
  }

  // --- the network path ------------------------------------------------------------------

  @ReactMethod
  fun startMonitoring() {
    val callback = object : ConnectivityManager.NetworkCallback() {
      override fun onAvailable(network: Network) = pathChanged(this)
      override fun onLost(network: Network) = pathChanged(this)
      override fun onCapabilitiesChanged(network: Network, capabilities: NetworkCapabilities) = pathChanged(this)
      override fun onLinkPropertiesChanged(network: Network, properties: LinkProperties) = pathChanged(this)
    }
    synchronized(this) {
      if (invalidated || monitor != null) return
      monitor = callback
    }
    try {
      connectivity.registerDefaultNetworkCallback(callback)
    } catch (_: Exception) {
      synchronized(this) { if (monitor === callback) monitor = null }
      return
    }
    // With no network at all the system calls nothing; say so once.
    pathChanged(callback)
  }

  private fun pathChanged(source: ConnectivityManager.NetworkCallback) {
    val network = connectivity.activeNetwork
    val capabilities = network?.let { connectivity.getNetworkCapabilities(it) }
    val properties = network?.let { connectivity.getLinkProperties(it) }
    val online = capabilities?.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET) == true
    // The kind of link, as one coarse word, is the only thing about the path that leaves the
    // device: the viewer's own server uses it to apply their per-network quality choice.
    val transport = when {
      capabilities == null -> "unknown"
      capabilities.hasTransport(NetworkCapabilities.TRANSPORT_ETHERNET) -> "ethernet"
      capabilities.hasTransport(NetworkCapabilities.TRANSPORT_WIFI) -> "wifi"
      capabilities.hasTransport(NetworkCapabilities.TRANSPORT_CELLULAR) -> "cellular"
      else -> "unknown"
    }
    val parts = ArrayList<String>()
    parts.add(online.toString())
    parts.add((capabilities?.hasCapability(NetworkCapabilities.NET_CAPABILITY_NOT_METERED) == false).toString())
    parts.add((capabilities?.hasCapability(NetworkCapabilities.NET_CAPABILITY_NOT_RESTRICTED) == false).toString())
    parts.add(transport)
    for (address in properties?.linkAddresses ?: emptyList()) parts.add((properties?.interfaceName ?: "") + ":" + address.address.hostAddress)
    // This stays on the device. Neither interface names and addresses nor this keyed network
    // generation are sent to Hosted.
    val generation = try {
      val key = secret("path-hmac-v1") { ByteArray(32).also { SecureRandom().nextBytes(it) } }
      val mac = Mac.getInstance("HmacSHA256").apply { init(SecretKeySpec(key, "HmacSHA256")) }
      encodeURL(mac.doFinal(parts.sorted().joinToString("\n").toByteArray(Charsets.UTF_8)))
    } catch (_: Exception) {
      null
    }
    synchronized(this) {
      if (invalidated || monitor !== source) return
      if (generation != null && generation == lastPath) return
      if (generation != null) lastPath = generation
    }
    val body = Arguments.createMap()
    body.putString("generation", generation ?: UUID.randomUUID().toString())
    body.putBoolean("online", online)
    body.putString("transport", transport)
    emit("portico.network", body)
  }

  // --- nearby discovery ------------------------------------------------------------------

  private inner class Search(val identifier: String, kind: String, val promise: Promise) : NsdManager.DiscoveryListener {
    val type = if (kind == "tv") TV_SERVICE else if (kind == "players") PLAYER_SERVICE else SERVER_SERVICE
    private val rows = ArrayList<WritableMap>()
    private val receivers = HashSet<String>()
    private val waiting = ArrayDeque<NsdServiceInfo>()
    private var found = 0
    private var resolving = false
    private var completed = false
    private var discovering = false
    private val timeout = Runnable { finish(null) }

    fun start() {
      main.postDelayed(timeout, 3500)
      try {
        nsd.discoverServices(type, NsdManager.PROTOCOL_DNS_SD, this)
        discovering = true
      } catch (_: Exception) {
        finish("discovery_unavailable")
      }
    }

    fun finish(failure: String?) {
      val result = Arguments.createArray()
      synchronized(this@PorticoNetwork) {
        if (completed) return
        completed = true
        if (searches[identifier] === this) searches.remove(identifier)
        if (failure == null) for (row in rows) result.pushMap(row)
      }
      main.removeCallbacks(timeout)
      if (discovering) try { nsd.stopServiceDiscovery(this) } catch (_: Exception) {}
      if (failure == null) promise.resolve(result)
      else promise.reject(failure, "Nearby discovery is unavailable.")
    }

    override fun onStartDiscoveryFailed(serviceType: String, errorCode: Int) { discovering = false; finish("discovery_unavailable") }
    override fun onStopDiscoveryFailed(serviceType: String, errorCode: Int) {}
    override fun onDiscoveryStarted(serviceType: String) {}
    override fun onDiscoveryStopped(serviceType: String) {}
    override fun onServiceLost(service: NsdServiceInfo) {}

    override fun onServiceFound(service: NsdServiceInfo) {
      synchronized(this@PorticoNetwork) {
        if (completed || found >= 32) return
        found++
        waiting.addLast(service)
      }
      next()
    }

    /** The system resolves one service at a time. */
    private fun next() {
      val service: NsdServiceInfo
      synchronized(this@PorticoNetwork) {
        if (completed || resolving) return
        service = waiting.removeFirstOrNull() ?: return
        resolving = true
      }
      val done = { resolved: NsdServiceInfo? ->
        synchronized(this@PorticoNetwork) {
          resolving = false
          if (resolved != null && !completed) try { add(resolved) } catch (_: Exception) {}
        }
        next()
      }
      try {
        @Suppress("DEPRECATION")
        nsd.resolveService(service, object : NsdManager.ResolveListener {
          override fun onResolveFailed(info: NsdServiceInfo, errorCode: Int) = done(null)
          override fun onServiceResolved(info: NsdServiceInfo) = done(info)
        })
      } catch (_: Exception) {
        done(null)
      }
    }

    /** Caller holds the module's lock. Only version 1 records with every field in bounds are kept. */
    private fun add(service: NsdServiceInfo) {
      val txt = service.attributes ?: return
      if (rows.size >= 64 || txt.entries.sumOf { it.key.length + (it.value?.size ?: 0) + 2 } > 2048) return
      fun field(key: String, limit: Int = 128): String? {
        val data = txt[key] ?: return null
        if (data.size > limit) return null
        return try {
          Charsets.UTF_8.newDecoder().onMalformedInput(CodingErrorAction.REPORT).onUnmappableCharacter(CodingErrorAction.REPORT)
              .decode(ByteBuffer.wrap(data)).toString()
        } catch (_: Exception) {
          null
        }
      }
      if (field("v") != "1") return
      // A signed-in Portico player on this network. Opaque ids only: the controller keeps a row
      // only when it matches its own server and its profile's receiver directory, and sending
      // still goes through the server's authorised handoff.
      if (type == PLAYER_SERVICE) {
        val rid = field("rid")?.takeIf { OPAQUE.matches(it) } ?: return
        val sid = field("sid")?.takeIf { OPAQUE.matches(it) } ?: return
        val kind = field("kind", 16)?.takeIf { it in PLAYER_KINDS } ?: return
        if (!receivers.add(rid)) return
        rows.add(Arguments.createMap().apply {
          putString("receiverId", rid); putString("serverId", sid); putString("kind", kind)
          putString("name", field("name", 96) ?: service.serviceName)
        })
        return
      }
      if (type == TV_SERVICE) {
        val code = field("code", 9)?.takeIf { TV_CODE.matches(it) } ?: return
        val request = field("request", 64) ?: return
        val issuer = field("issuer", 255) ?: return
        val key = field("key", 43) ?: return
        val expires = field("expires", 32)?.let { networkDate(it) } ?: return
        val now = System.currentTimeMillis()
        if (expires <= now || expires - now > 605000) return
        rows.add(Arguments.createMap().apply {
          putString("code", code); putString("requestId", request); putString("issuer", issuer); putString("publicKey", key)
          putString("name", field("name", 96) ?: service.serviceName); putDouble("expiresAt", expires.toDouble())
        })
        return
      }
      val id = field("id") ?: return
      val fingerprint = field("fp", 43) ?: return
      val port = field("port", 5)?.toIntOrNull() ?: return
      val path = field("path", 8) ?: return
      if (port != service.port || port <= 0 || port >= 65536) return
      for (address in addresses(service)) {
        if (rows.size >= 64) break
        val host = address.hostAddress ?: continue
        // A scoped (link-local) address is only meaningful with its interface; skip it.
        if (host.contains("%")) continue
        val literal = if (address is Inet6Address) "[$host]" else host
        rows.add(Arguments.createMap().apply {
          putString("serverId", id); putString("fingerprint", fingerprint); putInt("port", port); putString("path", path)
          putString("baseUrl", "http://$literal:$port"); putDouble("expiresAt", (System.currentTimeMillis() + 5000).toDouble())
        })
      }
    }
  }

  private fun addresses(service: NsdServiceInfo): List<InetAddress> =
      if (Build.VERSION.SDK_INT >= 34) service.hostAddresses
      else @Suppress("DEPRECATION") listOfNotNull(service.host)

  @ReactMethod
  fun browse(identifier: String?, kind: String?, promise: Promise) {
    val search: Search
    synchronized(this) {
      if (invalidated || identifier.isNullOrEmpty() || identifier.length > 64 || searches.containsKey(identifier) ||
          searches.size >= 2 || kind !in BROWSE_KINDS) {
        promise.reject("discovery_busy", "Discovery is already in progress.")
        return
      }
      search = Search(identifier, kind!!, promise)
      searches[identifier] = search
    }
    search.start()
  }

  @ReactMethod
  fun cancelBrowse(identifier: String?) {
    val search = synchronized(this) { searches[identifier] } ?: return
    search.finish("cancelled")
  }

  // --- TV sign-in ------------------------------------------------------------------------
  // Only the public half leaves the record store. The operation id must be durably recorded by
  // the caller before starting an approval request.

  @ReactMethod
  fun tvPublicKey(requestId: String?, promise: Promise) {
    try {
      if (requestId == null || !UUID_TEXT.matches(requestId)) throw Failure()
      val secretKey = secret("tv-v1:$requestId") { ByteArray(32).also { X25519.generatePrivateKey(SecureRandom(), it) } }
      promise.resolve(encodeURL(publicKey(secretKey)))
    } catch (_: Exception) {
      promise.reject("secure_storage", "TV setup identity could not be saved.")
    }
  }

  @ReactMethod
  fun openTVGrant(requestId: String?, envelope: ReadableMap?, promise: Promise) {
    try {
      fun text(key: String): String =
          envelope?.takeIf { it.hasKey(key) && it.getType(key) == ReadableType.String }?.getString(key) ?: throw Failure()
      if (requestId == null || !UUID_TEXT.matches(requestId) || envelope == null || text("kind") != "portico.tv-grant" ||
          !envelope.hasKey("version") || envelope.getType("version") != ReadableType.Number || envelope.getDouble("version") != 1.0 ||
          text("requestId") != requestId) throw Failure()
      val issuer = text("issuer")
      val serverKey = text("publicKey")
      val recipient = text("recipientKey")
      // Missing keys are not recreated while opening a response.
      val local = secret("tv-v1:$requestId") { throw Failure() }
      if (local.size != 32 || encodeURL(publicKey(local)) != recipient) throw Failure()
      val peer = decodeURL(serverKey, count = 32)
      val context = ("portico.tv-grant.v1\u0000$issuer\u0000$requestId\u0000$recipient\u0000$serverKey").toByteArray(Charsets.UTF_8)
      val shared = ByteArray(32)
      if (!X25519.calculateAgreement(local, 0, peer, 0, shared, 0)) throw Failure()
      val key = ByteArray(32)
      HKDFBytesGenerator(SHA256Digest()).apply { init(HKDFParameters(shared, decodeURL(text("salt"), count = 32), context)) }.generateBytes(key, 0, 32)
      shared.fill(0)
      val sealed = decodeURL(text("ciphertext"), limit = 65536)
      if (sealed.size <= 16) throw Failure()
      val cipher = Cipher.getInstance("AES/GCM/NoPadding")
      cipher.init(Cipher.DECRYPT_MODE, SecretKeySpec(key, "AES"), GCMParameterSpec(128, decodeURL(text("nonce"), count = 12)))
      cipher.updateAAD(context)
      val plain = cipher.doFinal(sealed)
      key.fill(0)
      promise.resolve(Charsets.UTF_8.newDecoder().onMalformedInput(CodingErrorAction.REPORT).onUnmappableCharacter(CodingErrorAction.REPORT)
          .decode(ByteBuffer.wrap(plain)).toString())
    } catch (_: Exception) {
      promise.reject("tv_grant_invalid", "The TV setup response could not be verified. No sign-in was installed.")
    }
  }

  @ReactMethod
  fun forgetTVKey(requestId: String?) {
    if (requestId != null && UUID_TEXT.matches(requestId)) store.write(PorticoRecordStore.NETWORK, "tv-v1:$requestId", null)
  }

  // --- advertisement ---------------------------------------------------------------------

  private inner class Advertisement(val name: String, type: String, val text: Map<String, String>) : NsdManager.RegistrationListener {
    private val info = NsdServiceInfo().apply {
      serviceName = name
      serviceType = type
      // Nothing listens: the record only carries the hint. Port 9 is "discard".
      port = 9
      for ((key, value) in text) setAttribute(key, value)
    }
    private var registered = false

    fun publish() {
      try { nsd.registerService(info, NsdManager.PROTOCOL_DNS_SD, this); registered = true } catch (_: Exception) {}
    }

    fun stop() {
      if (registered) try { nsd.unregisterService(this) } catch (_: Exception) {}
      registered = false
    }

    override fun onRegistrationFailed(service: NsdServiceInfo, errorCode: Int) { registered = false }
    override fun onUnregistrationFailed(service: NsdServiceInfo, errorCode: Int) {}
    override fun onServiceRegistered(service: NsdServiceInfo) {}
    override fun onServiceUnregistered(service: NsdServiceInfo) {}
  }

  /** A television waiting for approval says so on the network, so a phone can offer it without typing the code. */
  @ReactMethod
  fun advertiseTV(value: ReadableMap?, promise: Promise) {
    fun text(key: String): String? = value?.takeIf { it.hasKey(key) && it.getType(key) == ReadableType.String }?.getString(key)
    val code = text("code")?.takeIf { TV_CODE.matches(it) }
    val request = text("requestId")?.takeIf { UUID_TEXT.matches(it) }
    val issuer = text("issuer")?.takeIf { it.toByteArray(Charsets.UTF_8).size < 240 }
    val key = text("publicKey")?.takeIf { it.length == 43 }
    val expires = text("expiresAt")
    val expiry = expires?.let { networkDate(it) }
    val now = System.currentTimeMillis()
    if (code == null || request == null || issuer == null || key == null || expires == null || expiry == null || expiry <= now || expiry - now > 605000) {
      promise.reject("tv_discovery_invalid", "Invalid TV discovery hint.")
      return
    }
    val name = (text("name") ?: "Portico TV").take(32)
    val service = Advertisement(name, TV_SERVICE, mapOf("v" to "1", "code" to code, "request" to request, "issuer" to issuer, "key" to key, "expires" to expires, "name" to name))
    val expire = Runnable { stopTVAdvertisement() }
    synchronized(this) {
      if (invalidated) { promise.resolve(true); return }
      tvExpiry?.let { main.removeCallbacks(it) }
      tvService?.stop()
      tvService = service
      tvExpiry = expire
    }
    service.publish()
    main.postDelayed(expire, expiry - now)
    promise.resolve(true)
  }

  /** Play on: this signed-in player is on the network (receiver id, server id, name, kind). */
  @ReactMethod
  fun advertisePlayer(value: ReadableMap?, promise: Promise) {
    fun text(key: String): String? = value?.takeIf { it.hasKey(key) && it.getType(key) == ReadableType.String }?.getString(key)
    val rid = text("receiverId")?.takeIf { OPAQUE.matches(it) }
    val sid = text("serverId")?.takeIf { OPAQUE.matches(it) }
    val kind = text("kind")?.takeIf { it in PLAYER_KINDS }
    if (rid == null || sid == null || kind == null) {
      promise.reject("player_discovery_invalid", "Invalid player advertisement.")
      return
    }
    val name = (text("name") ?: "Portico").take(32)
    val service: Advertisement
    synchronized(this) {
      if (invalidated) { promise.reject("player_discovery_invalid", "Invalid player advertisement."); return }
      val current = playerService
      if (current != null && current.name == name && current.text["rid"] == rid && current.text["sid"] == sid) {
        promise.resolve(true)
        return
      }
      current?.stop()
      service = Advertisement(name, PLAYER_SERVICE, mapOf("v" to "1", "rid" to rid, "sid" to sid, "kind" to kind, "name" to name))
      playerService = service
    }
    service.publish()
    promise.resolve(true)
  }

  @ReactMethod
  fun stopPlayerAdvertisement() {
    val service = synchronized(this) { playerService.also { playerService = null } }
    service?.stop()
  }

  @ReactMethod
  fun stopTVAdvertisement() {
    val service = synchronized(this) {
      tvExpiry?.let { main.removeCallbacks(it) }
      tvExpiry = null
      tvService.also { tvService = null }
    }
    service?.stop()
  }

  // --- helpers ---------------------------------------------------------------------------

  /** A device-only secret in the record store, created on first use. Never repaired when unreadable. */
  private fun secret(account: String, create: () -> ByteArray): ByteArray = store.transaction {
    when (val found = store.read(PorticoRecordStore.NETWORK, account)) {
      is RecordRead.Value -> found.bytes
      RecordRead.Unreadable -> throw Failure()
      RecordRead.Missing -> create().also { if (!store.write(PorticoRecordStore.NETWORK, account, it)) throw Failure() }
    }
  }

  private fun publicKey(secretKey: ByteArray): ByteArray = ByteArray(32).also { X25519.generatePublicKey(secretKey, 0, it, 0) }

  companion object {
    private const val SERVER_SERVICE = "_portico._tcp"
    private const val TV_SERVICE = "_portico-tv._tcp"
    private const val PLAYER_SERVICE = "_portico-player._tcp"
    private val BROWSE_KINDS = setOf("servers", "tv", "players")
    private val PLAYER_KINDS = setOf("tv", "phone", "tablet", "computer")
    private val OPAQUE = Regex("^[A-Za-z0-9_-]{1,128}$")
    private val TV_CODE = Regex("^[ABCDEFGHJKMNPQRSTUVWXYZ23456789]{4}-[ABCDEFGHJKMNPQRSTUVWXYZ23456789]{4}$")
    private val UUID_TEXT = Regex("^[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}$")
    private val URL_TEXT = Regex("^[A-Za-z0-9_-]+$")
    private val CERT_FINGERPRINT = Regex("^[A-Za-z0-9_-]{43}$")
    private val DATE = Regex("^(\\d{4})-(\\d{2})-(\\d{2})T(\\d{2}):(\\d{2}):(\\d{2})(?:\\.(\\d{1,9}))?(Z|[+-]\\d{2}:\\d{2})$")

    private fun encodeURL(bytes: ByteArray): String =
        Base64.encodeToString(bytes, Base64.URL_SAFE or Base64.NO_PADDING or Base64.NO_WRAP)

    /** Unpadded base64url, in its one canonical spelling. */
    private fun decodeURL(value: String?, count: Int? = null, limit: Int = 65536): ByteArray {
      if (value.isNullOrEmpty() || value.length > limit * 2 || !URL_TEXT.matches(value)) throw IllegalArgumentException()
      val bytes = Base64.decode(value, Base64.URL_SAFE or Base64.NO_PADDING or Base64.NO_WRAP)
      if (bytes.size > limit || (count != null && bytes.size != count) || encodeURL(bytes) != value) throw IllegalArgumentException()
      return bytes
    }

    /** An RFC 3339 instant, with or without fractional seconds, as milliseconds since 1970. */
    private fun networkDate(value: String): Long? {
      val match = DATE.matchEntire(value) ?: return null
      val (year, month, day, hour, minute, second, fraction, zone) = match.destructured
      val calendar = Calendar.getInstance(TimeZone.getTimeZone("UTC"))
      calendar.isLenient = false
      return try {
        calendar.clear()
        calendar.set(year.toInt(), month.toInt() - 1, day.toInt(), hour.toInt(), minute.toInt(), second.toInt())
        var millis = calendar.timeInMillis + (if (fraction.isEmpty()) 0 else fraction.padEnd(3, '0').take(3).toInt())
        if (zone != "Z") {
          val offset = (zone.substring(1, 3).toInt() * 60 + zone.substring(4, 6).toInt()) * 60000L
          millis += if (zone[0] == '+') -offset else offset
        }
        millis
      } catch (_: Exception) {
        null
      }
    }
  }
}
