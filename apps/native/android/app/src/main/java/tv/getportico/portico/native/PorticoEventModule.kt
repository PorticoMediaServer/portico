package tv.getportico.portico.native

import com.facebook.react.bridge.ReactApplicationContext
import com.facebook.react.bridge.ReactContextBaseJavaModule

/**
 * A module that reports to JavaScript through `NativeEventEmitter`, as the Apple modules do
 * through `RCTEventEmitter`: the same event names, the same bodies.
 *
 * `NativeEventEmitter` calls `addListener` and `removeListeners` on every platform. React Native
 * exports only the methods a module's own class declares, so each module declares the pair
 * itself; the subscription is JavaScript's.
 */
abstract class PorticoEventModule(context: ReactApplicationContext) : ReactContextBaseJavaModule(context) {
  protected fun emit(event: String, body: Any?) {
    val context = reactApplicationContext
    if (context.hasActiveReactInstance()) context.emitDeviceEvent(event, body)
  }
}
