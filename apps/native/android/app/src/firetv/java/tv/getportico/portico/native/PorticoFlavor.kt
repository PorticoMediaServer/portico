package tv.getportico.portico.native

import com.facebook.react.bridge.NativeModule
import com.facebook.react.bridge.ReactApplicationContext

/**
 * What only one build has. Fire OS has no Google Play services, so this build has no Cast: the
 * module is absent, and the shared code offers no Cast destination (as on a device where the
 * Apple module reports Cast unavailable).
 */
internal fun flavorModules(@Suppress("UNUSED_PARAMETER") context: ReactApplicationContext): List<NativeModule> = emptyList()
