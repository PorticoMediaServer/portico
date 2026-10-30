import {NativeModules} from 'react-native';

type BootHost = {launchStartedAt?: unknown; bootDrawn?: () => void};
const host = (): BootHost | undefined => NativeModules.PorticoRuntime as BootHost | undefined;

let launchTaken = false;
/**
 * Boot's t = 0 (DESIGN "Ready, and what boot waits for"): when the person started the app, as the
 * platform recorded it, for the first opening only. Later openings (a retry keeps its own clock;
 * a new account or server starts one) begin when they are asked for.
 */
/** The platform's launch time without taking the opening's clock (boot timing logs). */
export function launchStartedAt(): number | undefined {
  const at = host()?.launchStartedAt;
  return typeof at === 'number' && Number.isFinite(at) ? at : undefined;
}
export function takeLaunchStartedAt(now = Date.now()): number {
  if (launchTaken) return now;
  launchTaken = true;
  const at = host()?.launchStartedAt;
  return typeof at === 'number' && Number.isFinite(at) && at <= now && now - at < 60_000 ? at : now;
}

let drawn = false;
/** React's first frame is on screen: the platform's native boot layer is removed (once). */
export function reportFirstFrame(): void {
  if (drawn) return;
  drawn = true;
  host()?.bootDrawn?.();
}
