import {Platform} from 'react-native';

/** The Siri Remote's Play/Pause in the foreground reaches the app only as a TV key:
 * react-native-tvos's root recognizer takes the press before the system's remote
 * commands. So on Apple TV the app's own key handlers must toggle playback. Android
 * TV's media key is not consumed by the root view and falls through to the app's
 * media session, which already toggles, so JS must never toggle there as well. */
export function tvPlayPauseTogglesPlayback(): boolean {return Platform.OS === 'ios' && Platform.isTV === true;}

const claims = new Set<symbol>();

/** A screen that gives Play/Pause its own meaning (the PIN pads delete a digit,
 * F-PROFILE-30) holds a claim while it is mounted (`useEffect(claimTVPlayPause, [])`);
 * playback ignores the key then. */
export function claimTVPlayPause(): () => void {
  const claim = Symbol('tv-play-pause');
  claims.add(claim);
  return () => {claims.delete(claim);};
}
export function tvPlayPauseClaimed(): boolean {return claims.size > 0;}

type Source = 'key' | 'system';
const sameGestureMs = 600;
let last: {source: Source; at: number} | undefined;

/** One physical press toggles once. If the system also reports the same press as a
 * remote command (or the reverse), the second report inside the window is dropped.
 * Repeated presses from one source always pass. */
export function admitTVPlayPause(source: Source, now = Date.now()): boolean {
  if (last && last.source !== source && now - last.at >= 0 && now - last.at < sameGestureMs) return false;
  last = {source, at: now};
  return true;
}

export function resetTVPlayPauseForTest(): void {claims.clear(); last = undefined;}
