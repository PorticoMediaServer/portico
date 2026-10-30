package tv.getportico.portico.native

import android.content.Context
import android.content.pm.PackageManager
import android.os.Handler
import android.os.Looper
import androidx.fragment.app.FragmentActivity
import androidx.mediarouter.app.MediaRouteChooserDialogFragment
import com.facebook.react.bridge.Arguments
import com.facebook.react.bridge.NativeModule
import com.facebook.react.bridge.Promise
import com.facebook.react.bridge.ReactApplicationContext
import com.facebook.react.bridge.ReactMethod
import com.google.android.gms.cast.Cast
import com.google.android.gms.cast.framework.CastContext
import com.google.android.gms.cast.framework.CastOptions
import com.google.android.gms.cast.framework.CastSession
import com.google.android.gms.cast.framework.OptionsProvider
import com.google.android.gms.cast.framework.SessionManagerListener
import com.google.android.gms.cast.framework.SessionProvider
import com.google.android.gms.common.ConnectionResult
import com.google.android.gms.common.GoogleApiAvailability
import java.util.UUID
import tv.getportico.portico.TelevisionDisplay

/** What only this build has: Google Cast, on phones and tablets with Google Play services. */
internal fun flavorModules(context: ReactApplicationContext): List<NativeModule> =
    if (TelevisionDisplay.isTelevision) emptyList() else listOf(PorticoCast(context))

private fun castApplicationId(context: Context): String = try {
  context.packageManager.getApplicationInfo(context.packageName, PackageManager.GET_META_DATA).metaData?.getString("PorticoCastApplicationID") ?: ""
} catch (_: Exception) { "" }

/** The Cast SDK asks this class for its options; the receiver is Portico's own. */
class PorticoCastOptions : OptionsProvider {
  override fun getCastOptions(context: Context): CastOptions = CastOptions.Builder().setReceiverApplicationId(castApplicationId(context)).build()
  override fun getAdditionalSessionProviders(context: Context): List<SessionProvider>? = null
}

/**
 * `NativeModules.PorticoCast`: the Android counterpart of `Native/PorticoCast.m`. The same five
 * methods and two events (`PorticoCastSession`, `PorticoCastMessage`), over Portico's own channel.
 * Main thread only: the Cast SDK requires it.
 */
class PorticoCast(context: ReactApplicationContext) : PorticoEventModule(context) {
  private val main = Handler(Looper.getMainLooper())
  private var cast: CastContext? = null
  private data class Attachment(val session: CastSession, val id: String, val callback: Cast.MessageReceivedCallback)
  private var attached: Attachment? = null
  private var listening = false
  private var listenerCount = 0

  override fun getName(): String = "PorticoCast"

  @ReactMethod fun addListener(eventName: String) { main.post {
    listenerCount++
    if (!listening) { listening = true; context()?.sessionManager?.addSessionManagerListener(sessions, CastSession::class.java) }
  } }

  @ReactMethod fun removeListeners(count: Double) { main.post {
    if (count.isFinite() && count > 0) listenerCount = (listenerCount - count.toInt()).coerceAtLeast(0)
    if (listenerCount == 0) { listening = false; cast?.sessionManager?.removeSessionManagerListener(sessions, CastSession::class.java); detach() }
  } }

  override fun invalidate() {
    main.post {
      listening = false
      listenerCount = 0
      cast?.sessionManager?.removeSessionManagerListener(sessions, CastSession::class.java)
      detach()
    }
    super.invalidate()
  }

  /** The SDK's context, when this device can cast at all (Play services present, a receiver configured). */
  private fun context(): CastContext? {
    cast?.let { return it }
    val app = reactApplicationContext.applicationContext
    if (castApplicationId(app).isEmpty() || GoogleApiAvailability.getInstance().isGooglePlayServicesAvailable(app) != ConnectionResult.SUCCESS) return null
    return try { @Suppress("DEPRECATION") CastContext.getSharedInstance(app).also { cast = it } } catch (_: Exception) { null }
  }

  private fun emitSession(state: String, session: CastSession, attachmentId: String, failed: Boolean) {
    if (!listening) return
    val body = Arguments.createMap()
    body.putString("state", state)
    body.putString("attachmentId", attachmentId)
    body.putString("deviceName", session.castDevice?.friendlyName ?: "")
    // The SDK's own id for the television: two with one name keep separate pairing proofs.
    body.putString("deviceId", session.castDevice?.deviceId ?: "")
    body.putString("error", if (failed) "cast_failed" else "")
    emit("PorticoCastSession", body)
  }

  private fun currentAttachment(session: CastSession, id: String): Boolean =
      attached?.session === session && attached?.id == id && context()?.sessionManager?.currentCastSession === session && session.isConnected

  private fun attach(session: CastSession): Attachment? {
    if (context()?.sessionManager?.currentCastSession !== session || !session.isConnected) return null
    attached?.let { if (it.session === session) return it }
    detach()
    val id = UUID.randomUUID().toString()
    // A distinct closure retains the original session and attachment identity,
    // even if the SDK has already queued a callback when unregister runs.
    val callback = Cast.MessageReceivedCallback { _, namespace, message ->
      main.post {
        if (listening && currentAttachment(session, id) && namespace == NAMESPACE && message.toByteArray(Charsets.UTF_8).size <= 65536) {
          emit("PorticoCastMessage", Arguments.createMap().apply { putString("message", message); putString("attachmentId", id) })
        }
      }
    }
    val original = Attachment(session, id, callback)
    attached = original
    return try { session.setMessageReceivedCallbacks(NAMESPACE, callback); original }
    catch (_: Exception) { if (attached === original) detach(); null }
  }

  private fun detach() {
    val original = attached
    attached = null
    try { original?.session?.removeMessageReceivedCallbacks(NAMESPACE) } catch (_: Exception) {}
  }

  private fun started(session: CastSession) {
    val original = attach(session) ?: return
    if (currentAttachment(original.session, original.id)) emitSession("started", session, original.id, false)
  }

  private fun finished(session: CastSession, state: String, failed: Boolean) {
    val original = attached ?: return
    if (original.session !== session || session.isConnected) return
    val current = context()?.sessionManager?.currentCastSession
    if (current != null && current !== session) return
    detach()
    emitSession(state, session, original.id, failed)
  }

  private val sessions = object : SessionManagerListener<CastSession> {
    override fun onSessionStarted(session: CastSession, sessionId: String) { started(session) }
    override fun onSessionResumed(session: CastSession, wasSuspended: Boolean) { started(session) }
    override fun onSessionEnded(session: CastSession, error: Int) { finished(session, "ended", error != 0) }
    override fun onSessionStartFailed(session: CastSession, error: Int) { finished(session, "failed", true) }
    override fun onSessionResumeFailed(session: CastSession, error: Int) { finished(session, "failed", true) }
    override fun onSessionStarting(session: CastSession) {}
    override fun onSessionEnding(session: CastSession) {}
    override fun onSessionResuming(session: CastSession, sessionId: String) {}
    override fun onSessionSuspended(session: CastSession, reason: Int) { finished(session, "ended", false) }
  }

  @ReactMethod
  fun available(promise: Promise) { main.post { promise.resolve(context() != null) } }

  /** The current session, if one is connected (after an app relaunch the SDK may resume it). */
  @ReactMethod
  fun current(promise: Promise) {
    main.post {
      val session = if (listening) context()?.sessionManager?.currentCastSession else null
      if (session == null || !session.isConnected) { promise.resolve(null); return@post }
      val original = attach(session)
      if (original == null || !currentAttachment(original.session, original.id)) { promise.resolve(null); return@post }
      promise.resolve(Arguments.createMap().apply { putString("attachmentId", original.id); putString("deviceName", session.castDevice?.friendlyName ?: ""); putString("deviceId", session.castDevice?.deviceId ?: "") })
    }
  }

  /** The SDK's own device chooser; the session events report the outcome. */
  @ReactMethod
  fun choose() {
    main.post {
      val selector = context()?.mergedSelector ?: return@post
      val activity = reactApplicationContext.currentActivity as? FragmentActivity ?: return@post
      try {
        MediaRouteChooserDialogFragment().also { it.routeSelector = selector }.show(activity.supportFragmentManager, "portico.cast.chooser")
      } catch (_: Exception) {}
    }
  }

  @ReactMethod
  fun send(json: String?, attachmentId: String?, promise: Promise) {
    main.post {
      val original = attached
      if (original == null || attachmentId == null || original.id != attachmentId || !currentAttachment(original.session, attachmentId) || json == null || json.toByteArray(Charsets.UTF_8).size > 65536) {
        promise.reject("cast_attachment_retired", "The Cast attachment changed."); return@post
      }
      try {
        original.session.sendMessage(NAMESPACE, json).setResultCallback { status ->
          main.post {
            if (!currentAttachment(original.session, original.id)) promise.reject("cast_attachment_retired", "The Cast attachment changed.")
            else if (status.isSuccess) promise.resolve(true)
            else promise.reject("cast_send_failed", "The message was not sent.")
          }
        }
      } catch (_: Exception) { promise.reject("cast_send_failed", "The message was not sent.") }
    }
  }

  @ReactMethod
  fun end(stopReceiver: Boolean, attachmentId: String?) {
    main.post {
      val original = attached
      if (original == null || attachmentId == null || original.id != attachmentId || !currentAttachment(original.session, attachmentId)) return@post
      // SessionManager operates on its current session; prove it is still the
      // original attachment immediately before invoking it on the main thread.
      try { context()?.sessionManager?.endCurrentSession(stopReceiver) } catch (_: Exception) {}
    }
  }

  private companion object { const val NAMESPACE = "urn:x-cast:tv.getportico.cast" }
}
