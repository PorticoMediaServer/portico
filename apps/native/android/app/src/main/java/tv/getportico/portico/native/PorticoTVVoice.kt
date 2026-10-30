package tv.getportico.portico.native

import android.app.Activity
import android.content.Intent
import android.speech.RecognizerIntent
import com.facebook.react.bridge.BaseActivityEventListener
import com.facebook.react.bridge.Promise
import com.facebook.react.bridge.ReactApplicationContext
import com.facebook.react.bridge.UiThreadUtil
import tv.getportico.portico.TelevisionDisplay

/** System recognition UI owns microphone disclosure. No audio or account data enters this bridge. */
internal class PorticoTVVoice(private val context: ReactApplicationContext) {
  private data class Attempt(val id: String, val code: Int, val activity: Activity, val promise: Promise)
  @Volatile private var closed = false
  private var attempt: Attempt? = null
  private var nextCode = 48000
  private var listening = false
  private fun intent() = Intent(RecognizerIntent.ACTION_RECOGNIZE_SPEECH)
      .putExtra(RecognizerIntent.EXTRA_LANGUAGE_MODEL, RecognizerIntent.LANGUAGE_MODEL_FREE_FORM)
      .putExtra(RecognizerIntent.EXTRA_MAX_RESULTS, 1)
  private val listener = object : BaseActivityEventListener() {
    override fun onActivityResult(activity: Activity, requestCode: Int, resultCode: Int, data: Intent?) {
      val held = attempt ?: return
      if (held.code != requestCode || held.activity !== activity) return
      attempt = null
      val text = if (resultCode == Activity.RESULT_OK) data?.getStringArrayListExtra(RecognizerIntent.EXTRA_RESULTS)?.firstOrNull()?.trim() else null
      held.promise.resolve(text?.takeIf { it.isNotEmpty() && it.length <= 256 })
    }
  }
  fun available(): Boolean = !closed && TelevisionDisplay.isTelevision && intent().resolveActivity(context.packageManager) != null
  fun start(id: String, promise: Promise) = UiThreadUtil.runOnUiThread {
    if (!available()) {promise.reject("voice_unavailable", "Voice search is unavailable."); return@runOnUiThread}
    if (attempt != null) {promise.reject("voice_busy", "Voice search is already open."); return@runOnUiThread}
    val activity = context.currentActivity
    if (activity == null || activity.isFinishing || id.isEmpty() || id.length > 128 || nextCode >= 65535) {
      promise.reject("voice_unavailable", "Voice search is unavailable."); return@runOnUiThread
    }
    if (!listening) {context.addActivityEventListener(listener); listening = true}
    val held = Attempt(id, nextCode++, activity, promise)
    attempt = held
    try {activity.startActivityForResult(intent(), held.code)} catch (_: Exception) {
      if (attempt === held) attempt = null
      promise.reject("voice_unavailable", "Voice search is unavailable.")
    }
  }
  fun cancel(id: String) = UiThreadUtil.runOnUiThread {
    val held = attempt ?: return@runOnUiThread
    if (held.id != id) return@runOnUiThread
    attempt = null
    try {held.activity.finishActivity(held.code)} catch (_: Exception) {}
    held.promise.resolve(null)
  }
  fun dispose() {
    closed = true
    UiThreadUtil.runOnUiThread {
      attempt?.id?.let {cancel(it)}
      if (listening) {context.removeActivityEventListener(listener); listening = false}
    }
  }
}
