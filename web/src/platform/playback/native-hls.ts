/**
 * When the browser's own HLS player is used for a stream instead of hls.js.
 *
 * hls.js is the engine everywhere it can run: one set of behaviours, and it measures the
 * connection for Automatic quality. Apple's browsers also play HLS themselves, and there are two
 * places where that is the better path:
 *
 * - **iPhone** (`first`). Safari on iPhone has no MediaSource, only ManagedMediaSource. MMS needs a native
 *   source alternative for AirPlay, and full screen on iPhone is the system's own
 *   player in any case. The system's HLS player is what the phone is built around, so it is used
 *   for every stream.
 * - **Safari on Mac and iPad** (`fallback`). hls.js stays first. When it says it cannot play a
 *   stream's codecs through MediaSource, or cannot recover from a decoding error, the same stream
 *   is handed to Safari's player before anything is reported as a failure: Safari plays HEVC,
 *   Dolby Vision and Dolby audio itself more widely than it does through MediaSource.
 *
 * Other browsers (`never`) keep to hls.js even where they have started to play HLS themselves:
 * only Apple's player is known to behave with these streams.
 */
export type NativeHlsPolicy = 'first' | 'fallback' | 'never';

export function nativeHlsPolicy(facts: Readonly<{userAgent: string; mediaSource: boolean}>): NativeHlsPolicy {
  const apple = /AppleWebKit\//.test(facts.userAgent) && !/(?:Chrome|Chromium|Edg|OPR)\/|Android/.test(facts.userAgent);
  if (!apple) return 'never';
  return facts.mediaSource ? 'fallback' : 'first';
}

/** The policy for this browser. */
export function browserNativeHlsPolicy(): NativeHlsPolicy {
  try { return nativeHlsPolicy({userAgent: navigator.userAgent, mediaSource: typeof (window as unknown as {MediaSource?: unknown}).MediaSource !== 'undefined'}); } catch { return 'never'; }
}
