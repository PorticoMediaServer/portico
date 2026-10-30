package tv.getportico.portico

import android.content.Context
import android.content.Intent
import android.content.res.Configuration
import androidx.lifecycle.Lifecycle
import android.os.Bundle
import android.view.KeyEvent
import androidx.core.splashscreen.SplashScreen.Companion.installSplashScreen
import com.facebook.react.ReactActivity
import com.facebook.react.ReactActivityDelegate
import com.facebook.react.defaults.DefaultNewArchitectureEntryPoint.fabricEnabled
import com.facebook.react.defaults.DefaultReactActivityDelegate
import tv.getportico.portico.native.PorticoAccountAuth
import tv.getportico.portico.native.PorticoOverlayKeys
import tv.getportico.portico.native.PorticoPictureInPicture

class MainActivity : ReactActivity() {

  /** The component `index.js` registers (app.json's name), the same one the Apple apps start. */
  override fun getMainComponentName(): String = "Portico"

  override fun createReactActivityDelegate(): ReactActivityDelegate =
      DefaultReactActivityDelegate(this, mainComponentName, fabricEnabled)

  override fun attachBaseContext(newBase: Context) {
    super.attachBaseContext(TelevisionDisplay.context(newBase))
  }

  /**
   * The navigation stack lives in JavaScript, so a recreated activity starts from the app's own
   * saved sign-in rather than from restored fragments (react-native-screens requires this).
   */
  override fun onCreate(savedInstanceState: Bundle?) {
    // The system splash hands over to the native boot view, which React's Boot replaces (PorticoBoot).
    PorticoBoot.keep(installSplashScreen())
    // A cold launch's link is filtered before React Native can read it.
    intent = PorticoUrlRoutes.filtered(intent)
    super.onCreate(null)
    PorticoBoot.attach(this)
  }

  /** The small window opened, or closed: back at full size when the activity is still on screen, dismissed otherwise. */
  override fun onPictureInPictureModeChanged(isInPictureInPictureMode: Boolean, newConfig: Configuration) {
    super.onPictureInPictureModeChanged(isInPictureInPictureMode, newConfig)
    PorticoPictureInPicture.changed(this, isInPictureInPictureMode, lifecycle.currentState.isAtLeast(Lifecycle.State.STARTED))
  }

  /** Leaving the app while a film plays continues it in the small window (before Android 12, which does this itself). */
  override fun onUserLeaveHint() {
    super.onUserLeaveHint()
    PorticoPictureInPicture.userLeaving(this)
  }

  /**
   * A television remote's Menu key (the Fire TV remote has one) is its own key: JavaScript hears
   * it as the remote's `menu` event and opens the focused control's own menu, if it has one. It
   * is never turned into a press of Select, which would activate a control that has no menu.
   * The views are given the key and nothing else is: the activity's own handling of Menu (the
   * developer menu in a debug build) does not run on a television.
   */
  override fun dispatchKeyEvent(event: KeyEvent): Boolean {
    // A native Modal has its own focused window and already owns Escape/BACK handling.
    if (window.decorView.hasWindowFocus() && PorticoOverlayKeys.dispatch(event) { super.dispatchKeyEvent(it) }) return true
    if (event.keyCode != KeyEvent.KEYCODE_MENU || !TelevisionDisplay.isTelevision) return super.dispatchKeyEvent(event)
    window.superDispatchKeyEvent(event)
    return true
  }

  /**
   * One allowlist (`PorticoUrlRoutes`) for every link handed to the app while it runs. A provider
   * sign-in's return goes to the sign-in that is waiting for it, as it does inside the system
   * sign-in browser on iPhone.
   */
  override fun onNewIntent(intent: Intent) {
    val allowed = PorticoUrlRoutes.filtered(intent) ?: intent
    if (PorticoAccountAuth.takeReturn(allowed)) allowed.data = null
    setIntent(allowed)
    super.onNewIntent(allowed)
  }
}
