package tv.getportico.portico.native

import com.facebook.react.bridge.ReactApplicationContext
import com.facebook.react.bridge.ReactMethod

/**
 * `NativeModules.PorticoSiriRemote`: the Siri Remote's touch surface. No Android remote has one,
 * so nothing is ever reported; the directional pad seeks through the shared timeline, as an
 * Apple TV does with a remote that has no touch surface.
 */
class PorticoSiriRemote(context: ReactApplicationContext) : PorticoEventModule(context) {
  override fun getName(): String = "PorticoSiriRemote"

  @ReactMethod fun addListener(eventName: String) {}

  @ReactMethod fun removeListeners(count: Double) {}

  @ReactMethod fun setEnabled(enabled: Boolean) {}
}
