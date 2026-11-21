package tv.getportico.portico.native

import android.app.Activity
import android.content.pm.ActivityInfo
import tv.getportico.portico.TelevisionDisplay

/**
 * Phones are upright everywhere except the video player (Justin, 21 Nov 2026). A phone, and a closed fold, is under
 * 600 dp at its smallest side and keeps portrait; while the player is open it turns as it did before. A tablet or an
 * open fold always rotates. The player's own sideways request (PorticoFullWindow) is never overridden here: it
 * restores the value it found, and this runs again when the player closes.
 */
object PhoneUpright {
  /** Main thread only. */
  var playerOpen = false

  fun apply(activity: Activity) {
    if (TelevisionDisplay.isTelevision) return
    if (activity.requestedOrientation == ActivityInfo.SCREEN_ORIENTATION_SENSOR_LANDSCAPE) return
    val phone = activity.resources.configuration.smallestScreenWidthDp < 600
    val wanted = if (phone && !playerOpen) ActivityInfo.SCREEN_ORIENTATION_PORTRAIT else ActivityInfo.SCREEN_ORIENTATION_UNSPECIFIED
    if (activity.requestedOrientation != wanted) activity.requestedOrientation = wanted
  }
}
