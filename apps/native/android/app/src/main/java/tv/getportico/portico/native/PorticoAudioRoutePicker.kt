package tv.getportico.portico.native

import android.view.View
import com.facebook.react.uimanager.SimpleViewManager
import com.facebook.react.uimanager.ThemedReactContext
import com.facebook.react.uimanager.annotations.ReactProp

/**
 * The native view `PorticoAudioRoutePicker` (`video`). On iPhone and iPad it is the system's
 * AirPlay button. Android has no such picker to embed (the output is chosen in the system's own
 * media controls), so the view draws nothing, as it does on Apple TV; the shared Play on sheet
 * only shows its row where AirPlay exists.
 */
class PorticoAudioRoutePickerManager : SimpleViewManager<View>() {
  override fun getName(): String = "PorticoAudioRoutePicker"

  override fun createViewInstance(context: ThemedReactContext): View = View(context)

  @ReactProp(name = "video", defaultBoolean = false)
  fun setVideo(view: View, video: Boolean) {}
}
