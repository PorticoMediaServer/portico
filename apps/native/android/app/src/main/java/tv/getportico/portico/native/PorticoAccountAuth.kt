package tv.getportico.portico.native

import android.app.UiModeManager
import android.content.Context
import android.content.Intent
import android.content.res.Configuration
import android.net.Uri
import android.os.Handler
import android.os.Looper
import android.util.Base64
import androidx.browser.customtabs.CustomTabsIntent
import com.facebook.react.bridge.LifecycleEventListener
import com.facebook.react.bridge.Promise
import com.facebook.react.bridge.ReactApplicationContext
import com.facebook.react.bridge.ReactContextBaseJavaModule
import com.facebook.react.bridge.ReactMethod
import java.security.SecureRandom
import java.util.UUID
import tv.getportico.portico.PorticoUrlRoutes

/**
 * `NativeModules.PorticoAccountAuth`: Portico Account sign-in on this device.
 *
 * Account continuations are not credentials. Only the existing credential owner stores Portico
 * sessions; neither provider assertions nor passwords are persisted.
 *
 * A provider (Google, Apple) signs in through the system browser in a Custom Tab and returns on
 * `portico://account-return`, as the iPhone does for Google. Android has no native Sign in with
 * Apple, so `canSignInApple` is false and Apple takes the same browser path. Televisions have no
 * browser: they use the code flow.
 */
class PorticoAccountAuth(context: ReactApplicationContext) : ReactContextBaseJavaModule(context), LifecycleEventListener {
  private val store = PorticoRecordStore.get(context)
  private val main = Handler(Looper.getMainLooper())
  private val television =
      (context.getSystemService(Context.UI_MODE_SERVICE) as? UiModeManager)?.currentModeType == Configuration.UI_MODE_TYPE_TELEVISION

  private var promise: Promise? = null
  private var expected: String? = null
  private var generation = 0
  private var left = false

  init {
    context.addLifecycleEventListener(this)
    synchronized(Companion) { current = this }
  }

  override fun getName(): String = "PorticoAccountAuth"

  override fun getConstants(): Map<String, Any> =
      mapOf("bundleId" to reactApplicationContext.packageName, "canSignInApple" to false, "canOpenBrowser" to !television)

  override fun invalidate() {
    synchronized(Companion) { if (current === this) current = null }
    reactApplicationContext.removeLifecycleEventListener(this)
    finish(null, "provider_cancelled", "Provider sign-in was cancelled.")
    super.invalidate()
  }

  /** The account origin a continuation is kept for: https, or this device itself. */
  private fun account(origin: String?): String? {
    if (origin == null || origin.length > 2048) return null
    val url = try { Uri.parse(origin) } catch (_: Exception) { return null }
    val host = url.host
    val loopback = host in LOOPBACK
    if (host.isNullOrEmpty() || url.userInfo != null || url.encodedQuery != null || url.encodedFragment != null || !url.path.isNullOrEmpty() ||
        !(url.scheme == "https" || (url.scheme == "http" && loopback))) return null
    return origin
  }

  @ReactMethod
  fun newBinding(promise: Promise) {
    val bytes = ByteArray(32).also { SecureRandom().nextBytes(it) }
    promise.resolve(Base64.encodeToString(bytes, Base64.URL_SAFE or Base64.NO_PADDING or Base64.NO_WRAP))
  }

  @ReactMethod
  fun newRequestId(promise: Promise) = promise.resolve(UUID.randomUUID().toString())

  @ReactMethod
  fun loadContinuation(origin: String?, promise: Promise) {
    val account = account(origin)
    if (account == null) {
      promise.reject("origin", "Invalid account origin.")
      return
    }
    when (val found = store.read(CONTINUATIONS, account)) {
      RecordRead.Missing -> promise.resolve(null)
      RecordRead.Unreadable -> promise.reject("secure_storage", "The private account continuation could not be read. Unlock this device and retry.")
      is RecordRead.Value -> promise.resolve(found.bytes.toString(Charsets.UTF_8))
    }
  }

  @ReactMethod
  fun saveContinuation(origin: String?, value: String?, promise: Promise) {
    val account = account(origin)
    val data = value?.toByteArray(Charsets.UTF_8)
    if (account == null || data == null || data.isEmpty() || data.size > 8192) {
      promise.reject("continuation", "Invalid account continuation.")
      return
    }
    if (store.write(CONTINUATIONS, account, data)) promise.resolve(null)
    else promise.reject("secure_storage", "The account continuation could not be saved securely.")
  }

  private fun finish(value: Any?, code: String?, message: String?) {
    val pending: Promise?
    synchronized(this) {
      pending = promise
      promise = null
      expected = null
      left = false
    }
    if (code != null) pending?.reject(code, message) else pending?.resolve(value)
  }

  @ReactMethod
  fun openBrowser(raw: String?, transactionId: String?, promise: Promise) {
    if (television) {
      promise.reject("browser_unavailable", "Use browser activation on a phone or computer for Google sign-in.")
      return
    }
    val url = raw?.let { try { Uri.parse(it) } catch (_: Exception) { null } }
    if (raw == null || url == null || url.scheme != "https" || url.host !in PROVIDER_HOSTS || url.userInfo != null || url.port != -1 ||
        raw.length > 16384 || transactionId.isNullOrEmpty() || transactionId.length > 160 || !IDENTIFIER.matches(transactionId)) {
      promise.reject("provider_url", "The provider return is invalid.")
      return
    }
    val activity = reactApplicationContext.currentActivity
    synchronized(this) {
      if (this.promise != null) {
        promise.reject("busy", "Finish or cancel the current provider sign-in first.")
        return
      }
      if (activity == null) {
        promise.reject("foreground", "Return to the app before signing in.")
        return
      }
      this.promise = promise
      expected = transactionId
      left = false
      generation++
    }
    try {
      CustomTabsIntent.Builder().setShowTitle(false).build().launchUrl(activity!!, url)
    } catch (_: Exception) {
      finish(null, "provider_start", "The provider browser could not start.")
    }
  }

  /** The return of the sign-in this module started, or false for anything else (which React Native's own link handling then sees). */
  private fun returned(uri: Uri): Boolean {
    synchronized(this) { if (promise == null) return false }
    if (!PorticoUrlRoutes.isAccountReturn(uri)) return false
    val text = uri.toString()
    if (uri.getQueryParameter("transactionId") != expected || text.length > 1024) {
      finish(null, "provider_return", "The provider returned a different sign-in attempt. Start again.")
    } else {
      finish(text, null, null)
    }
    return true
  }

  override fun onHostPause() {
    synchronized(this) { if (promise != null) left = true }
  }

  /** Back in the app with no return: the browser was closed. The return itself arrives just before the resume, so wait a moment for it. */
  override fun onHostResume() {
    val attempt: Int
    synchronized(this) {
      if (promise == null || !left) return
      attempt = generation
    }
    main.postDelayed({
      val stale = synchronized(this) { promise != null && generation == attempt }
      if (stale) finish(null, "provider_cancelled", "Provider sign-in was cancelled or could not finish.")
    }, 700)
  }

  override fun onHostDestroy() {}

  /** Android has no native Sign in with Apple; `canSignInApple` is false, so the shared sign-in screen never calls this. */
  @ReactMethod
  fun signInApple(nonce: String?, promise: Promise) {
    promise.reject("apple_credential", "Apple did not return an identity assertion.")
  }

  @ReactMethod
  fun cancel(promise: Promise) {
    synchronized(this) { generation++ }
    finish(null, "provider_cancelled", "Provider sign-in was cancelled.")
    promise.resolve(null)
  }

  companion object {
    private const val CONTINUATIONS = "account-continuation"
    private val LOOPBACK = setOf("localhost", "127.0.0.1", "[::1]", "::1")
    private val PROVIDER_HOSTS = setOf("accounts.google.com", "appleid.apple.com")
    private val IDENTIFIER = Regex("^[A-Za-z0-9_-]+$")
    private var current: PorticoAccountAuth? = null

    /** Called by the activity for each incoming link, before React Native sees it. True when the link was a sign-in's return and has been taken. */
    fun takeReturn(intent: Intent?): Boolean {
      val uri = intent?.data ?: return false
      val module = synchronized(this) { current } ?: return false
      return module.returned(uri)
    }
  }
}
