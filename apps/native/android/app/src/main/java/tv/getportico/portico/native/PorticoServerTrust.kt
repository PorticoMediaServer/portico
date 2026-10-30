package tv.getportico.portico.native

import android.content.Context
import android.content.pm.PackageManager
import com.facebook.react.modules.network.OkHttpClientProvider
import java.net.Socket
import java.security.KeyStore
import java.security.MessageDigest
import java.security.cert.CertificateException
import java.security.cert.X509Certificate
import java.util.concurrent.ConcurrentHashMap
import javax.net.ssl.HostnameVerifier
import javax.net.ssl.HttpsURLConnection
import javax.net.ssl.SSLContext
import javax.net.ssl.SSLEngine
import javax.net.ssl.SSLException
import javax.net.ssl.SSLSession
import javax.net.ssl.SSLSocket
import javax.net.ssl.SSLSocketFactory
import javax.net.ssl.TrustManagerFactory
import javax.net.ssl.X509ExtendedTrustManager
import javax.net.ssl.X509TrustManager
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import okhttp3.OkHttpClient
import okio.ByteString.Companion.toByteString

/**
 * The owner's own certificate. A Portico server may present a certificate no public authority
 * signed (self-signed, or from the owner's private authority). The app trusts such a certificate
 * for exactly the address it was seen at, once its fingerprint is bound into a verified
 * Portico identity proof under the app's identity decision, and keeps it pinned there.
 *
 * Every HTTP client in the app takes its TLS checks from here (`porticoTrust()` on each OkHttp
 * builder, the React Native client factory and the platform default for anything else):
 *  - a certificate the system trusts for the name is accepted as before;
 *  - otherwise the leaf certificate's SHA-256 must equal the one pinned for that host and port;
 *  - anything else is refused, and its fingerprint is remembered (only in memory) so the app can
 *    check the signed binding and, for an unknown identity away, ask for the identity code.
 *
 * A pin is the exact certificate at one address, so it is accepted whatever its dates and names
 * say: it was authorized by the identity decision, never by a Trust button.
 */
internal object PorticoServerTrust {
  data class Pin(val fingerprint: String, val serverId: String)

  private const val RECORD = "certificates-v1"
  private const val MAX_PINS = 64
  private const val MAX_REFUSED = 32
  /** The Portico Account service when the build names none (as the JavaScript default). */
  private const val PRODUCTION_ACCOUNT = "https://web.getportico.tv"
  private val FINGERPRINT = Regex("^[A-Za-z0-9_-]{43}$")
  private val SERVER_ID = Regex("^srv_[A-Za-z0-9_-]{43}$")

  @Volatile private var store: PorticoRecordStore? = null
  @Volatile private var loaded = false
  private val pins = ConcurrentHashMap<String, Pin>()
  private val refused = ConcurrentHashMap<String, String>()

  private val platform: X509TrustManager by lazy {
    val factory = TrustManagerFactory.getInstance(TrustManagerFactory.getDefaultAlgorithm())
    factory.init(null as KeyStore?)
    factory.trustManagers.filterIsInstance<X509TrustManager>().first()
  }
  private val systemNames: HostnameVerifier = HttpsURLConnection.getDefaultHostnameVerifier()

  /**
   * `host:port` of the Portico Account service (the build's PorticoHostedURL, else production). It is
   * reached over publicly trusted TLS only: no owner's certificate is ever pinned, remembered or
   * accepted there (NS-02), whatever the app asks.
   */
  @Volatile private var accountKey: String? = key(PRODUCTION_ACCOUNT)

  private fun accountOrigin(context: Context): String? = try {
    context.packageManager.getApplicationInfo(context.packageName, PackageManager.GET_META_DATA)
        .metaData?.getString("PorticoHostedURL")?.takeIf { it.isNotEmpty() }
  } catch (_: Exception) {
    null
  }

  /** At application start: load the pins and make every client in the process use them. */
  fun install(context: Context) {
    store = PorticoRecordStore.get(context.applicationContext)
    accountKey = key(accountOrigin(context) ?: PRODUCTION_ACCOUNT)
    HttpsURLConnection.setDefaultSSLSocketFactory(socketFactory)
    HttpsURLConnection.setDefaultHostnameVerifier(hostnames)
    // React Native's own client: images, and any request the app makes through `fetch`.
    OkHttpClientProvider.setOkHttpClientFactory { OkHttpClientProvider.createClientBuilder(context.applicationContext).porticoTrust().build() }
  }

  // --- pins ------------------------------------------------------------------------------

  /** `host:port` for an http(s) origin, the host lower-case and without IPv6 brackets. */
  fun key(origin: String?): String? {
    val url = origin?.toHttpUrlOrNull() ?: return null
    if (url.scheme != "https") return null
    return key(url.host, url.port)
  }

  private fun key(host: String?, port: Int): String? {
    if (host.isNullOrEmpty() || port <= 0 || port > 65535) return null
    return host.removePrefix("[").removeSuffix("]").lowercase() + ":" + port
  }

  private fun load() {
    if (loaded) return
    synchronized(this) {
      if (loaded) return
      val found = store?.read(PorticoRecordStore.NETWORK, RECORD) ?: return
      if (found is RecordRead.Value) {
        for (line in String(found.bytes, Charsets.UTF_8).split('\n')) {
          val parts = line.split('\t')
          if (parts.size == 3 && FINGERPRINT.matches(parts[1]) && (parts[2].isEmpty() || SERVER_ID.matches(parts[2]))) pins[parts[0]] = Pin(parts[1], parts[2])
        }
      }
      loaded = true
    }
  }

  private fun save(): Boolean {
    val text = pins.entries.take(MAX_PINS).joinToString("\n") { "${it.key}\t${it.value.fingerprint}\t${it.value.serverId}" }
    return store?.write(PorticoRecordStore.NETWORK, RECORD, text.toByteArray(Charsets.UTF_8)) ?: false
  }

  fun pinned(origin: String?): Pin? {
    load()
    return key(origin)?.takeIf { it != accountKey }?.let { pins[it] }
  }

  /** Pins a certificate at an origin, replacing the one pinned there before. */
  fun pin(origin: String?, fingerprint: String?, serverId: String?): Boolean {
    val key = key(origin) ?: return false
    if (key == accountKey) return false
    if (fingerprint == null || !FINGERPRINT.matches(fingerprint) || serverId == null || (serverId.isNotEmpty() && !SERVER_ID.matches(serverId))) return false
    load()
    synchronized(this) {
      if (!pins.containsKey(key) && pins.size >= MAX_PINS) return false
      val previous = pins.put(key, Pin(fingerprint, serverId))
      if (!save()) { if (previous == null) pins.remove(key) else pins[key] = previous; return false }
      return true
    }
  }

  /** Every pinned certificate by `host:port`, in address order. */
  fun all(): List<Pair<String, Pin>> {
    load()
    return pins.entries.sortedBy { it.key }.map { it.key to it.value }
  }

  fun forget(origin: String?) {
    val key = key(origin) ?: return
    load()
    synchronized(this) { if (pins.remove(key) != null) save() }
  }

  /** Remove only the exact certificate shown by the confirmation. */
  fun forgetCaptured(origin: String?, fingerprint: String?, serverId: String?): Boolean {
    val key = key(origin) ?: return false
    if (key == accountKey || fingerprint == null || !FINGERPRINT.matches(fingerprint) || serverId == null || !SERVER_ID.matches(serverId)) return false
    load()
    synchronized(this) {
      val previous = pins[key] ?: return false
      if (previous.fingerprint != fingerprint || previous.serverId != serverId) return false
      pins.remove(key)
      if (!save()) { pins[key] = previous; return false }
      refused.remove(key)
      return true
    }
  }

  /** One offline durable replacement. Failed storage leaves the in-memory pins unchanged. */
  fun forgetServer(serverId: String, origins: List<String>): Boolean {
    if (!SERVER_ID.matches(serverId) || origins.size > 256) return false
    val originKeys = mutableSetOf<String>()
    // Validate the complete HTTP/HTTPS origin set before mutating any pin.
    for (origin in origins) {
      val url = origin.toHttpUrlOrNull() ?: return false
      if (url.username.isNotEmpty() || url.password.isNotEmpty() || url.encodedPath != "/" || url.query != null || url.fragment != null ||
          origin != url.toString().removeSuffix("/")) return false
      originKeys.add(key(url.host, url.port) ?: return false)
    }
    load()
    synchronized(this) {
      val removed = pins.filter { (at, pin) -> pin.serverId == serverId || at in originKeys }
      if (removed.isEmpty()) return true
      for (at in removed.keys) pins.remove(at)
      if (!save()) { pins.putAll(removed); return false }
      for (at in removed.keys) refused.remove(at)
      return true
    }
  }

  /** The certificate most recently refused at an origin, if any. */
  fun refused(origin: String?): String? = key(origin)?.let { refused[it] }

  private fun remember(key: String?, fingerprint: String) {
    if (key == null || key == accountKey) return
    if (refused.size >= MAX_REFUSED && !refused.containsKey(key)) refused.clear()
    refused[key] = fingerprint
  }

  fun fingerprint(certificate: X509Certificate): String =
      MessageDigest.getInstance("SHA-256").digest(certificate.encoded).toByteString().base64Url().trimEnd('=')

  private fun pinnedAt(key: String?, certificate: X509Certificate?): Boolean {
    if (key == null || certificate == null || key == accountKey) return false
    load()
    return pins[key]?.fingerprint == fingerprint(certificate)
  }

  // --- the checks ------------------------------------------------------------------------

  /** System trust, else the exact pinned certificate for the address, else refused. */
  val trustManager: X509ExtendedTrustManager = object : X509ExtendedTrustManager() {
    override fun checkServerTrusted(chain: Array<out X509Certificate>?, authType: String?, socket: Socket?) {
      val session = (socket as? SSLSocket)?.handshakeSession
      trustOrPinned(chain, key(session?.peerHost, session?.peerPort ?: -1)) {
        val system = platform
        if (system is X509ExtendedTrustManager) system.checkServerTrusted(chain, authType, socket) else system.checkServerTrusted(chain, authType)
      }
    }

    override fun checkServerTrusted(chain: Array<out X509Certificate>?, authType: String?, engine: SSLEngine?) {
      trustOrPinned(chain, key(engine?.peerHost, engine?.peerPort ?: -1)) {
        val system = platform
        if (system is X509ExtendedTrustManager) system.checkServerTrusted(chain, authType, engine) else system.checkServerTrusted(chain, authType)
      }
    }

    override fun checkServerTrusted(chain: Array<out X509Certificate>?, authType: String?) =
        trustOrPinned(chain, null) { platform.checkServerTrusted(chain, authType) }

    override fun checkClientTrusted(chain: Array<out X509Certificate>?, authType: String?, socket: Socket?) { throw CertificateException() }
    override fun checkClientTrusted(chain: Array<out X509Certificate>?, authType: String?, engine: SSLEngine?) { throw CertificateException() }
    override fun checkClientTrusted(chain: Array<out X509Certificate>?, authType: String?) { throw CertificateException() }
    override fun getAcceptedIssuers(): Array<X509Certificate> = platform.acceptedIssuers
  }

  private inline fun trustOrPinned(chain: Array<out X509Certificate>?, key: String?, system: () -> Unit) {
    try {
      system()
      // Hostname verification can still refuse later and record that refusal.
      if (key != null) refused.remove(key)
    } catch (e: CertificateException) {
      val leaf = chain?.firstOrNull() ?: throw e
      remember(key, fingerprint(leaf))
      if (pinnedAt(key, leaf)) return
      throw e
    }
  }

  /** The system's name check, else the exact pinned certificate for the address. */
  val hostnames = HostnameVerifier { host, session ->
    val leaf = leaf(session) ?: return@HostnameVerifier false
    val key = key(host, session.peerPort)
    // A valid name alone is not system trust when the chain needed a Portico pin.
    if (systemNames.verify(host, session)) return@HostnameVerifier true
    remember(key, fingerprint(leaf))
    if (pinnedAt(key, leaf)) return@HostnameVerifier true
    false
  }

  private fun leaf(session: SSLSession?): X509Certificate? = try {
    session?.peerCertificates?.firstOrNull() as? X509Certificate
  } catch (_: SSLException) {
    null
  }

  val socketFactory: SSLSocketFactory by lazy {
    SSLContext.getInstance("TLS").apply { init(null, arrayOf(trustManager), null) }.socketFactory
  }

  /**
   * A client for one identity proof only, under exactly the observed leaf at `origin`.
   * Even a system-trusted replacement must match; this grants no durable trust.
   */
  fun proofClient(base: OkHttpClient.Builder, origin: String, expected: String): OkHttpClient {
    val target = key(origin)
    val manager = object : X509ExtendedTrustManager() {
      private fun accept(chain: Array<out X509Certificate>?) {
        val leaf = chain?.firstOrNull() ?: throw CertificateException()
        if (fingerprint(leaf) != expected) throw CertificateException()
      }
      override fun checkServerTrusted(chain: Array<out X509Certificate>?, authType: String?, socket: Socket?) = accept(chain)
      override fun checkServerTrusted(chain: Array<out X509Certificate>?, authType: String?, engine: SSLEngine?) = accept(chain)
      override fun checkServerTrusted(chain: Array<out X509Certificate>?, authType: String?) = accept(chain)
      override fun checkClientTrusted(chain: Array<out X509Certificate>?, authType: String?, socket: Socket?) { throw CertificateException() }
      override fun checkClientTrusted(chain: Array<out X509Certificate>?, authType: String?, engine: SSLEngine?) { throw CertificateException() }
      override fun checkClientTrusted(chain: Array<out X509Certificate>?, authType: String?) { throw CertificateException() }
      override fun getAcceptedIssuers(): Array<X509Certificate> = platform.acceptedIssuers
    }
    val factory = SSLContext.getInstance("TLS").apply { init(null, arrayOf(manager), null) }.socketFactory
    return base.sslSocketFactory(factory, manager)
        .hostnameVerifier { host, session -> key(host, session.peerPort) == target && leaf(session)?.let { fingerprint(it) } == expected }
        .build()
  }

  /** Whether a failed call failed because a certificate was refused. */
  fun refusedCertificate(error: Throwable?): Boolean {
    var cause = error
    var depth = 0
    while (cause != null && depth++ < 8) {
      if (cause is CertificateException || cause is javax.net.ssl.SSLPeerUnverifiedException || cause is javax.net.ssl.SSLHandshakeException) return true
      cause = cause.cause
    }
    return false
  }
}

/** Every Portico client: TLS checks from [PorticoServerTrust]. */
internal fun OkHttpClient.Builder.porticoTrust(): OkHttpClient.Builder =
    sslSocketFactory(PorticoServerTrust.socketFactory, PorticoServerTrust.trustManager).hostnameVerifier(PorticoServerTrust.hostnames)
