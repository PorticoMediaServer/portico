package tv.getportico.portico.native

import com.facebook.react.bridge.ReactApplicationContext
import com.facebook.react.bridge.ReactContextBaseJavaModule
import com.facebook.react.bridge.ReactMethod
import com.facebook.react.bridge.ReadableMap

/**
 * `NativeModules.PorticoActivity`: Handoff, Spotlight and Siri on iPhone and iPad. Android has
 * no counterpart, so the module answers as it does on Apple TV: every call is accepted and does
 * nothing.
 */
class PorticoActivity(context: ReactApplicationContext) : ReactContextBaseJavaModule(context) {
  override fun getName(): String = "PorticoActivity"

  @ReactMethod fun setCurrent(title: ReadableMap?) {}

  @ReactMethod fun indexTitles(titles: ReadableMap?) {}

  @ReactMethod fun clear() {}
}
