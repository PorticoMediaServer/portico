package tv.getportico.portico.native

import com.facebook.react.bridge.Arguments
import com.facebook.react.bridge.Promise
import com.facebook.react.bridge.ReactApplicationContext
import com.facebook.react.bridge.ReactContextBaseJavaModule
import com.facebook.react.bridge.ReactMethod
import com.reactnativecommunity.webview.PorticoReaderAndroidAssets

/** URI only; the API never accepts a path, source or selected-server URL. */
class PorticoReaderAssets(context: ReactApplicationContext) : ReactContextBaseJavaModule(context) {
  override fun getName() = "PorticoReaderAssets"
  @ReactMethod fun bundle(promise: Promise) {
    if (!PorticoReaderAndroidAssets.available(reactApplicationContext)) {promise.reject("reader_assets_unavailable", "reader_assets_unavailable"); return}
    val value = Arguments.createMap(); value.putString("url", PorticoReaderAndroidAssets.ENTRY); promise.resolve(value)
  }
}
