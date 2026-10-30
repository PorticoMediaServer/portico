# App-owned bridge methods and view properties are discovered by React Native at runtime.
# React Native's own consumer rules already keep these; stated here so the app's
# reflective surface is explicit. The Cast and mpv rules below are load-bearing.
# Module/view classes themselves are registered directly in PorticoPackage.
-keepattributes RuntimeVisibleAnnotations,RuntimeVisibleParameterAnnotations,AnnotationDefault
-keepclassmembers,allowoptimization class tv.getportico.portico.native.** {
    @com.facebook.react.bridge.ReactMethod <methods>;
    @com.facebook.react.uimanager.annotations.ReactProp <methods>;
    @com.facebook.react.uimanager.annotations.ReactPropGroup <methods>;
}

# Cast constructs this app-owned provider from the class name in the Google manifest.
# The Fire flavor intentionally has no such class.
-keep class tv.getportico.portico.native.PorticoCastOptions {
    public <init>();
}

# React Native, Media3 and the Cast SDK contribute their library consumer rules.
# portico-mpv/consumer-rules.pro owns the named JNI entry points and callbacks.
# Bouncy Castle is used only through direct lightweight API calls, with no registered JCA
# provider or reflective algorithm lookup. Do not keep its unused providers/algorithms, or
# suppress missing-class diagnostics: the release build must report any real gaps.
