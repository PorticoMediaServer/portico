package tv.getportico.portico.native

import android.os.Handler
import android.os.Looper
import android.view.HapticFeedbackConstants
import com.facebook.react.bridge.Promise
import com.facebook.react.bridge.ReactApplicationContext
import com.facebook.react.bridge.ReactContextBaseJavaModule
import com.facebook.react.bridge.ReactMethod
import tv.getportico.portico.TelevisionDisplay

/** One OS-admitted selection-entry request. No Vibrator permission or policy override. */
class PorticoHaptics(context: ReactApplicationContext) : ReactContextBaseJavaModule(context) {
  override fun getName() = "PorticoHaptics"

  @ReactMethod fun selection(promise: Promise) {
    Handler(Looper.getMainLooper()).post {
      val view = reactApplicationContext.currentActivity?.window?.decorView
      val admitted = try {
        !TelevisionDisplay.isTelevision && view?.isAttachedToWindow == true && view.hasWindowFocus() &&
            view.performHapticFeedback(HapticFeedbackConstants.LONG_PRESS)
      } catch (_: Exception) { false }
      promise.resolve(admitted)
    }
  }
}
