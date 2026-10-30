package tv.getportico.portico

import android.app.Application
import android.content.Context
import com.facebook.react.PackageList
import com.facebook.react.ReactApplication
import com.facebook.react.ReactHost
import com.facebook.react.ReactNativeApplicationEntryPoint.loadReactNative
import com.facebook.react.defaults.DefaultReactHost.getDefaultReactHost
import tv.getportico.portico.native.PorticoPackage
import tv.getportico.portico.native.PorticoServerTrust

class MainApplication : Application(), ReactApplication {

  override val reactHost: ReactHost by lazy {
    getDefaultReactHost(
      context = applicationContext,
      packageList = PackageList(this).packages.apply { add(PorticoPackage()) },
      // An APK that carries its JavaScript must not look for Metro: the debug build type sets
      // `BuildConfig.DEBUG` in both cases, so the packaging flag is the only reliable answer.
      useDevSupport = !BuildConfig.PORTICO_EMBEDDED_JAVASCRIPT,
    )
  }

  override fun attachBaseContext(base: Context) {
    super.attachBaseContext(TelevisionDisplay.context(base))
  }

  override fun onCreate() {
    super.onCreate()
    // Before any client exists: every connection checks certificates the same way.
    PorticoServerTrust.install(this)
    TelevisionDisplay.install(this)
    loadReactNative(this)
    TelevisionDisplay.keep(this)
  }
}
