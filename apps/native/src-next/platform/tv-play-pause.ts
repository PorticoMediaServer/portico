import {Platform} from 'react-native';

/** The Siri Remote's Play/Pause in the foreground reaches the app only as a TV key:
 * react-native-tvos's root recognizer takes the press before the system's remote
 * commands. So on Apple TV the app's own key handlers must toggle playback. Android
 * TV's media key is not consumed by the root view and falls through to the app's
 * media session, which already toggles, so JS must never toggle there as well. */
export function tvPlayPauseTogglesPlayback(): boolean {return Platform.OS === 'ios' && Platform.isTV === true;}

/**
 * One owner for the remote's Play/Pause (TV-FOCUS-REVIEW step 6). Every screen that gives the key a meaning
 * registers a scope and when it applies; on a press only the topmost applicable registration acts:
 *
 *   overlay  a PIN pad over anything (Play/Pause deletes a digit there, F-PROFILE-30)
 *   surface  the full video player (raise controls, then the transport key, F-PLAYER-1)
 *   shell    listening playback from any screen (tv-player 8)
 *
 * Newest first inside a scope. It replaces the claim set the PIN pads held and the checks each listener made.
 */
export type TVPlayPauseScope = 'overlay' | 'surface' | 'shell';
const scopes: readonly TVPlayPauseScope[] = ['overlay', 'surface', 'shell'];
type Registration = Readonly<{scope: TVPlayPauseScope; active: () => boolean; sequence: number}>;
const registrations: Registration[] = [];
let sequence = 0;

function owner(): Registration | undefined {
  for (const scope of scopes) {
    const found = registrations.filter(entry => entry.scope === scope).sort((a, b) => b.sequence - a.sequence).find(entry => entry.active());
    if (found) return found;
  }
  return undefined;
}

/** Register a meaning for Play/Pause. `owns()` says, at the press, whether this registration is the one that acts. */
export function registerTVPlayPause(scope: TVPlayPauseScope, active: () => boolean = () => true): Readonly<{owns: () => boolean; release: () => void}> {
  const registration: Registration = {scope, active, sequence: ++sequence};
  registrations.push(registration);
  return {
    owns: () => registrations.includes(registration) && owner() === registration,
    release: () => {const index = registrations.indexOf(registration); if (index >= 0) registrations.splice(index, 1);},
  };
}

type Hooks = Readonly<{useCallback: <T extends (...args: never[]) => unknown>(run: T, deps: readonly unknown[]) => T; useEffect: (run: () => void | (() => void), deps: readonly unknown[]) => void;
  useRef: <T>(value: T) => {current: T}}>;
/** The registration hook over a given React (platform/tv-play-pause-owner binds the app's): a component's
 * registration for its lifetime, its `active` read at the press. */
export function tvPlayPauseOwnerHook(react: Hooks) {
  return function useTVPlayPauseOwner(scope: TVPlayPauseScope, active: () => boolean = () => true): () => boolean {
    const latest = react.useRef(active); latest.current = active;
    const registration = react.useRef<ReturnType<typeof registerTVPlayPause> | undefined>(undefined);
    react.useEffect(() => {
      const owned = registerTVPlayPause(scope, () => latest.current());
      registration.current = owned;
      return () => {owned.release(); if (registration.current === owned) registration.current = undefined;};
    }, [scope]);
    return react.useCallback(() => registration.current?.owns() === true, []);
  };
}

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

export function resetTVPlayPauseForTest(): void {registrations.length = 0; last = undefined;}
