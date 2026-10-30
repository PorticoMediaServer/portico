package tv.getportico.portico.mpv;

import android.view.Surface;

/**
 * libmpv's client API, as the mpv player engine uses it (`src/main/cpp/portico_mpv_jni.c`).
 *
 * A handle is one mpv player. Every method may be called from any thread. Text values cross as
 * UTF-8 bytes; option, property and command names are ASCII strings. Results are mpv's own: 0
 * or a positive number for success, a negative `MPV_ERROR_*` for failure.
 */
public final class PorticoMpvNative {
  private PorticoMpvNative() {}

  /** mpv's events, delivered one at a time on a thread of the handle's own. */
  public interface Listener {
    /**
     * Any event except a property change or a log line. {@code id} is mpv's event id.
     * {@code userdata} is the token of a command reply; {@code entry} the playlist entry of a
     * start-file or end-file; {@code reason} and {@code error} say why a file ended.
     */
    void onEvent(int id, long userdata, int error, long entry, int reason);

    /** An observed property changed. The value is read separately. */
    void onProperty(String name);

    /** A log line at warning level or above. */
    void onLog(byte[] prefix, byte[] level, byte[] text);
  }

  // mpv_event_id
  public static final int EVENT_SHUTDOWN = 1;
  public static final int EVENT_COMMAND_REPLY = 5;
  public static final int EVENT_START_FILE = 6;
  public static final int EVENT_END_FILE = 7;
  public static final int EVENT_FILE_LOADED = 8;
  public static final int EVENT_PLAYBACK_RESTART = 21;

  // mpv_end_file_reason
  public static final int END_EOF = 0;
  public static final int END_STOP = 2;
  public static final int END_QUIT = 3;
  public static final int END_ERROR = 4;
  public static final int END_REDIRECT = 5;

  // mpv_error, the ones the engine tells apart
  public static final int ERROR_LOADING_FAILED = -13;
  public static final int ERROR_AO_INIT_FAILED = -14;
  public static final int ERROR_VO_INIT_FAILED = -15;
  public static final int ERROR_NOTHING_TO_PLAY = -16;
  public static final int ERROR_UNKNOWN_FORMAT = -17;
  public static final int ERROR_UNSUPPORTED = -18;

  private static Boolean loaded;

  /** Loads libmpv and the bridge. False when this build does not carry them. */
  public static synchronized boolean load() {
    if (loaded == null) {
      try {
        System.loadLibrary("mpv");
        System.loadLibrary("porticompv");
        loaded = Boolean.TRUE;
      } catch (Throwable missing) {
        android.util.Log.w("PorticoMpv", "The mpv libraries could not be loaded.", missing);
        loaded = Boolean.FALSE;
      }
    }
    return loaded;
  }

  /** A new, uninitialised handle, or 0. */
  public static native long create(Listener listener);

  public static native int setOption(long handle, String name, byte[] value);

  /** Initialises mpv and starts its events. */
  public static native int initialize(long handle);

  public static native int observe(long handle, String name);

  /** Runs a command and waits for it. */
  public static native int command(long handle, byte[][] arguments);

  /** Starts a command; its result arrives as a command-reply event carrying {@code token}. */
  public static native int commandAsync(long handle, long token, byte[][] arguments);

  public static native int setProperty(long handle, String name, byte[] value);

  /** A property as mpv prints it (lists and maps as JSON), or null. */
  public static native byte[] getString(long handle, String name);

  /** NaN when unavailable. */
  public static native double getDouble(long handle, String name);

  /** 1, 0, or -1 when unavailable. */
  public static native int getFlag(long handle, String name);

  /** {@link Long#MIN_VALUE} when unavailable. */
  public static native long getLong(long handle, String name);

  /**
   * The surface mpv draws on, or null for none. Close the video output (vo=null) before taking
   * a surface away.
   */
  public static native int setSurface(long handle, Surface surface);

  public static native String errorString(int code);

  /** Ends mpv. The handle is dead afterwards. May block for a moment. */
  public static native void destroy(long handle);
}
