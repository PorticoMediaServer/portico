package tv.getportico.portico.native

import com.facebook.react.ReactPackage
import com.facebook.react.bridge.NativeModule
import com.facebook.react.bridge.ReactApplicationContext
import com.facebook.react.uimanager.ViewManager
import tv.getportico.portico.TelevisionDisplay
import tv.getportico.portico.BuildConfig

/**
 * Portico's native layer. Module names, methods and events are the ones the Apple modules in
 * `Native/` export, so `src/bridge` is one contract for every platform.
 */
class PorticoPackage : ReactPackage {
  // Bridge modules by design (as on Apple): they run through the New Architecture's interop layer.
  @Suppress("OVERRIDE_DEPRECATION")
  override fun createNativeModules(reactContext: ReactApplicationContext): List<NativeModule> = listOf(
      PorticoRuntime(reactContext),
      PorticoActivity(reactContext),
      PorticoSiriRemote(reactContext),
      PorticoOverlayKeys(reactContext),
      PorticoHaptics(reactContext),
      PorticoVideoSnapshot(reactContext),
      PorticoPosture(reactContext),
      PorticoNetwork(reactContext),
      PorticoAccountAuth(reactContext),
      PorticoCapabilities(reactContext),
      PorticoPlaybackModule(reactContext),
      PorticoMpvPlaybackModule(reactContext),
      PorticoSystemMedia(reactContext),
      PorticoImagePicker(reactContext),
      PorticoSubtitleFiles(reactContext),
      PorticoDocumentFiles(reactContext),
  ) + (if (BuildConfig.DEBUG) listOf(PorticoLayoutObservation(reactContext)) else emptyList()) + (
      // Downloads belong to phones and tablets, the home-screen rows to televisions: the other
      // kind of device has no such module, and the shared screens hide the feature.
      if (TelevisionDisplay.isTelevision) listOf(PorticoTopShelf(reactContext)) else listOf(PorticoHighlightsSave(reactContext), PorticoDownloads(reactContext), PorticoReaderAssets(reactContext), PorticoQRScanner(reactContext), PorticoFullWindow(reactContext))) + flavorModules(reactContext)

  override fun createViewManagers(reactContext: ReactApplicationContext): List<ViewManager<*, *>> = listOf(
      PorticoPlayerManager(),
      PorticoMpvPlayerManager(),
      PorticoAudioRoutePickerManager(),
  )
}
