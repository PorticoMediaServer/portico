# The JNI bridge finds these by name.
-keep class tv.getportico.portico.mpv.PorticoMpvNative { *; }
-keep interface tv.getportico.portico.mpv.PorticoMpvNative$Listener { *; }
# GetMethodID resolves these on the concrete listener passed to create(), not only its interface.
-keepclassmembers class * implements tv.getportico.portico.mpv.PorticoMpvNative$Listener {
    public void onEvent(int, long, int, long, int);
    public void onProperty(java.lang.String);
    public void onLog(byte[], byte[], byte[]);
}
