package tv.getportico.portico.native

import android.os.Handler
import android.os.Looper
import android.view.HapticFeedbackConstants
import com.facebook.react.bridge.Promise
import com.facebook.react.bridge.ReactApplicationContext
import com.facebook.react.bridge.ReactContextBaseJavaModule
import com.facebook.react.bridge.ReactMethod
import tv.getportico.portico.TelevisionDisplay

/** One OS-admitted feedback request per call. No Vibrator permission or policy override. */
class PorticoHaptics(context: ReactApplicationContext) : ReactContextBaseJavaModule(context) {
  override fun getName() = "PorticoHaptics"

  /** A light tick as selection starts, like iOS's selection feedback. */
  @ReactMethod fun selection(promise: Promise) = perform(HapticFeedbackConstants.CLOCK_TICK, promise)

  /** The firm tap for a long-press menu. */
  @ReactMethod fun contextMenu(promise: Promise) = perform(HapticFeedbackConstants.LONG_PRESS, promise)

  private fun perform(feedback: Int, promise: Promise) {
    Handler(Looper.getMainLooper()).post {
      val view = reactApplicationContext.currentActivity?.window?.decorView
      val admitted = try {
        !TelevisionDisplay.isTelevision && view?.isAttachedToWindow == true && view.hasWindowFocus() &&
            view.performHapticFeedback(feedback)
      } catch (_: Exception) { false }
      promise.resolve(admitted)
    }
  }
}
