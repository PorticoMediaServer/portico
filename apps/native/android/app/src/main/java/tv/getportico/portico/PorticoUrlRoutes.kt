package tv.getportico.portico

import android.content.Intent
import android.net.Uri

/**
 * Which `portico://` URLs the app hands to React Native: the Android counterpart of
 * `tvos/Portico/PorticoURLRoutes.swift`, the same grammar route for route.
 *
 * Forwarding is not authorisation. A route that passes here is still resolved against the
 * signed-in viewer by the JavaScript router before anything opens or plays; this decides only
 * what is well-formed enough to be worth routing.
 */
internal object PorticoUrlRoutes {
  private val IDENTIFIER = Regex("^[A-Za-z0-9_-]+$")
  private val CODE = Regex("^[A-Za-z0-9-]+$")
  private val TOKEN = Regex("^[A-Za-z0-9_.~-]+$")
  private const val PUBLIC_MEASUREMENT = "portico://native-layout?specimen=signed-out-welcome"
  private const val REFUSED_MEASUREMENT = "portico://native-layout"
  private val MEASUREMENT_ATTEMPT = Regex("""^portico:[/\\]*(?:[^/?#]*@)?native-layout""", RegexOption.IGNORE_CASE)
  private val ACCOUNT_TABS = setOf("profiles", "sessions", "security")

  /** Hosts whose https links open the app (the manifest's app links). */
  private val UNIVERSAL_HOSTS = setOf("web.getportico.tv", "getportico.tv")

  private fun safeIdentifier(value: String?, limit: Int) = value != null && value.isNotEmpty() && value.length <= limit && IDENTIFIER.matches(value)

  /** The server's invitation codes: 32–128 URL-safe characters. */
  private fun safeInvitationCode(value: String?) = value != null && value.length in 32..128 && IDENTIFIER.matches(value)

  private fun safeCode(value: String?) = value != null && value.length in 4..16 && CODE.matches(value)

  /** Any length the app can hold: a short or damaged invitation still opens the app, which then says the link isn't complete. */
  private fun safeToken(value: String?) = value != null && value.length in 1..512 && TOKEN.matches(value)

  /** An http(s) origin: a host (and port), no credentials, path, query or fragment. */
  private fun safeServerOrigin(value: String?): Boolean {
    if (value == null || value.length !in 8..512) return false
    val parts = try { Uri.parse(value) } catch (_: Exception) { return false }
    return (parts.scheme == "https" || parts.scheme == "http") && !parts.host.isNullOrEmpty() && parts.userInfo == null &&
        (parts.path.isNullOrEmpty() || parts.path == "/") && parts.encodedQuery == null && parts.encodedFragment == null
  }

  /** The query's items in order, names and values decoded. */
  private fun queryItems(uri: Uri): List<Pair<String, String?>>? = uri.encodedQuery?.let { query ->
    if (query.isEmpty()) emptyList() else query.split("&").map { item ->
      val at = item.indexOf('=')
      if (at < 0) Uri.decode(item) to null else Uri.decode(item.substring(0, at)) to Uri.decode(item.substring(at + 1))
    }
  }

  /** `a=1&b=2` fragments, as the web links write them. Empty pieces are kept: a malformed link must never crash the app. */
  private fun fragmentPairs(uri: Uri): List<Pair<String, String>> {
    val fragment = uri.fragment
    if (fragment.isNullOrEmpty()) return emptyList()
    return fragment.split("&").filter { it.isNotEmpty() }.map { pair ->
      val at = pair.indexOf('=')
      if (at < 0) pair to "" else pair.substring(0, at) to Uri.decode(pair.substring(at + 1))
    }
  }

  private fun fragmentValue(uri: Uri, name: String): String? = fragmentPairs(uri).firstOrNull { it.first == name }?.second

  private fun bare(uri: Uri) = uri.path.isNullOrEmpty() || uri.path == "/"

  /** `portico://account-return?transactionId=…`, optionally `&code=…` (a 43-character completion code): the sign-in continuation. */
  fun isAccountReturn(uri: Uri): Boolean {
    val items = queryItems(uri)
    if (uri.host != "account-return" || !uri.path.isNullOrEmpty() || items == null || items.size !in 1..2 || items[0].first != "transactionId") return false
    if (items.size == 2 && (items[1].first != "code" || items[1].second?.length != 43 || !safeIdentifier(items[1].second, 43))) return false
    return safeIdentifier(items[0].second, 160)
  }

  /** `portico://item/{id}` and `portico://play/{id}`: the display and play actions the server publishes. */
  private fun isContentRoute(uri: Uri): Boolean {
    val path = uri.path ?: return false
    if ((uri.host != "item" && uri.host != "play") || uri.encodedQuery != null || !path.startsWith("/")) return false
    val identifier = path.substring(1)
    return !identifier.contains("/") && safeIdentifier(identifier, 128)
  }

  /** O-162 shared links (client-core `server-links.ts`, frozen): `portico://shared/{serverId}/{segment}…`
   * with an optional short query. 1–6 encoded segments (the server alone is its Home); an encoded
   * slash, `?`, `#` or control character, a dot segment, or a fragment never passes. Only
   * `web.getportico.tv/s/…` app links produce it; the JavaScript parser decides what it means. */
  private val SHARED_SEGMENT = Regex("^[A-Za-z0-9._~%-]{1,512}$")
  private val SHARED_FORBIDDEN = Regex("%(2[fF]|3[fF]|23|[01][0-9a-fA-F]|7[fF])")
  private val SHARED_QUERY = Regex("^[A-Za-z0-9._~%&=+-]{1,1024}$")
  private fun sharedSegments(path: String?): List<String>? {
    if (path == null || !path.startsWith("/")) return null
    val segments = path.substring(1).split("/")
    return segments.takeIf { it.size in 1..6 && it.all { segment -> SHARED_SEGMENT.matches(segment) && !SHARED_FORBIDDEN.containsMatchIn(segment) && segment != "." && segment != ".." } }
  }
  private fun sharedQuery(query: String?) = query == null || (SHARED_QUERY.matches(query) && !SHARED_FORBIDDEN.containsMatchIn(query))
  private fun isShared(uri: Uri) = uri.host == "shared" && uri.encodedFragment == null && sharedQuery(uri.encodedQuery) && sharedSegments(uri.encodedPath) != null

  /** `portico://downloads`: where a "download finished" notification leads. */
  private fun isDownloads(uri: Uri) = uri.host == "downloads" && bare(uri) && uri.encodedQuery == null

  /** `portico://account` or `portico://account?tab=profiles|sessions|security`. */
  private fun isAccount(uri: Uri): Boolean {
    if (uri.host != "account" || !bare(uri) || uri.encodedFragment != null) return false
    val items = queryItems(uri) ?: return true
    return items.size == 1 && items[0].first == "tab" && items[0].second in ACCOUNT_TABS
  }

  /** `portico://device`: approve a TV's sign-in (the code is typed on the approval screen). */
  private fun isDevice(uri: Uri) = uri.host == "device" && bare(uri) && uri.encodedQuery == null && uri.encodedFragment == null

  /** `portico://together/join/{code}`: a Watch Together invite. */
  private fun isTogetherJoin(uri: Uri): Boolean {
    val path = uri.path ?: return false
    return uri.host == "together" && uri.encodedQuery == null && uri.encodedFragment == null && path.startsWith("/join/") && safeCode(path.substring("/join/".length))
  }

  /** `portico://invite#invite={token}`: the token only in the fragment. */
  private fun isInvite(uri: Uri) =
      uri.host == "invite" && bare(uri) && uri.encodedQuery == null && fragmentPairs(uri).size == 1 && safeToken(fragmentValue(uri, "invite"))

  /** `portico://join#code={code}&server={origin}`: a server's own invitation, only in the fragment. */
  private fun isJoin(uri: Uri) =
      uri.host == "join" && bare(uri) && uri.encodedQuery == null && fragmentPairs(uri).size == 2 &&
          safeInvitationCode(fragmentValue(uri, "code")) && safeServerOrigin(fragmentValue(uri, "server"))

  private fun isPublicMeasurement(raw: String) = BuildConfig.DEBUG && raw == PUBLIC_MEASUREMENT

  /** DEBUG only: preserve an attempted measurement as a fixed public refusal,
   * never as a normal launch or an arbitrary URL containing caller data. */
  internal fun debugMeasurementRoute(raw: String, host: String?): String? {
    if (!BuildConfig.DEBUG) return null
    if (isPublicMeasurement(raw)) return PUBLIC_MEASUREMENT
    return if (host.equals("native-layout", ignoreCase = true) || MEASUREMENT_ATTEMPT.containsMatchIn(raw.trim())) REFUSED_MEASUREMENT else null
  }

  /** Exact DEV gallery grammar. Raw matching refuses credentials, ports, fragments,
   * encoded aliases, duplicate/unknown parameters and payloads outside gallery IDs. */
  internal fun debugKitRoute(raw: String): String? {
    if (!BuildConfig.DEBUG) return null
    return raw.takeIf { Regex("^portico://kit/?(?:\\?gallery=[A-Za-z0-9_-]{1,128}(?:&window=1)?)?$").matches(it) }
  }

  /** The single decision, for warm opens and cold launches alike. */
  fun allows(uri: Uri): Boolean {
    if (uri.scheme != "portico" || uri.userInfo != null || uri.port != -1) return false
    if (isPublicMeasurement(uri.toString())) return true
    // Only an invitation carries a fragment (its token or code); every other route refuses one.
    if (uri.encodedFragment != null) return isInvite(uri) || isJoin(uri)
    return isAccountReturn(uri) || isContentRoute(uri) || isDownloads(uri) || isAccount(uri) || isDevice(uri) || isTogetherJoin(uri) || isShared(uri)
  }

  private fun percentEncoded(value: String): String {
    val out = StringBuilder()
    for (byte in value.toByteArray(Charsets.UTF_8)) {
      val c = byte.toInt() and 0xff
      if ((c in 0x30..0x39) || (c in 0x41..0x5a) || (c in 0x61..0x7a)) out.append(c.toChar()) else out.append('%').append("%02X".format(c))
    }
    return out.toString()
  }

  /** Translates an app link (web.getportico.tv/account, /device, /join…) into the one `portico://` route it means, or null. */
  fun route(web: Uri): Uri? {
    if (web.scheme != "https" || web.host?.lowercase() !in UNIVERSAL_HOSTS || web.userInfo != null || web.port != -1) return null
    val raw = web.path ?: ""
    val path = if (raw.endsWith("/") && raw.length > 1) raw.dropLast(1) else raw
    // O-162: only web.getportico.tv shares; the fragment is dropped, a short query is kept for the parser.
    // The encoded path: an encoded slash or anything outside the identifier set stays refused.
    val encoded = (web.encodedPath ?: "").let { if (it.endsWith("/") && it.length > 1) it.dropLast(1) else it }
    if (encoded == "/s" || encoded.startsWith("/s/")) {
      if (web.host?.lowercase() != "web.getportico.tv") return null
      val segments = sharedSegments(encoded.removePrefix("/s")) ?: return null
      val query = web.encodedQuery?.takeIf { it.isNotEmpty() }
      if (!sharedQuery(query)) return null
      return Uri.parse("portico://shared/" + segments.joinToString("/") + (query?.let { "?$it" } ?: "")).takeIf { allows(it) }
    }
    val candidate: String? = when (path) {
      // `event` and anything else in the query is for the web page only.
      "/account" -> queryItems(web)?.firstOrNull { it.first == "tab" }?.second.let { tab -> if (tab != null && tab in ACCOUNT_TABS) "portico://account?tab=$tab" else "portico://account" }
      "/device" -> "portico://device"
      "/join" -> {
        val code = fragmentValue(web, "code")
        val server = fragmentValue(web, "server")
        val token = fragmentValue(web, "invite")
        if (code != null && server != null && web.encodedQuery == null && fragmentPairs(web).size == 2 && safeInvitationCode(code) && safeServerOrigin(server))
          "portico://join#code=$code&server=${percentEncoded(server)}"
        // An old Portico Account invitation: the app says to ask for a new one.
        else if (safeToken(token)) "portico://invite#invite=$token"
        else null
      }
      "/together/join" -> fragmentValue(web, "code")?.takeIf { safeCode(it) }?.let { "portico://together/join/$it" }
      "/app/account-return" -> {
        // A provider sign-in's return (`#tx=…&code=…`); the sign-in screen checks it is the one it
        // started. Only the two values pass, and only into the query of the app route.
        val tx = fragmentValue(web, "tx")
        val code = fragmentValue(web, "code")
        if (web.encodedQuery == null && fragmentPairs(web).size == 2 && safeIdentifier(tx, 160) && code?.length == 43 && safeIdentifier(code, 43))
          "portico://account-return?transactionId=$tx&code=$code" else null
      }
      else -> null
    }
    return candidate?.let { Uri.parse(it) }?.takeIf { allows(it) }
  }

  /**
   * The intent React Native may see: its link kept when the allowlist accepts it, translated
   * when it is an app link, and removed otherwise (the app then simply opens).
   */
  fun filtered(intent: Intent?): Intent? {
    val data = intent?.data ?: return intent
    // MainActivity calls this same filter before both initial and warm RN delivery.
    // Release never evaluates this exception, including malformed-attempt handling.
    val measurement = if (BuildConfig.DEBUG) debugMeasurementRoute(data.toString(), data.host) else null
    // DEV only: the in-app gallery host reaches the DEV Kit. A release build never evaluates it.
    val kit = debugKitRoute(data.toString())?.let { Uri.parse(it) }
    val route = measurement?.let { Uri.parse(it) } ?: kit ?: when (data.scheme) {
      "portico" -> data.takeIf { allows(it) }
      "https" -> route(data)
      else -> null
    }
    return Intent(intent).apply { setData(route) }
  }
}
