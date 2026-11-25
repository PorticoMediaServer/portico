import type {ShellBootstrap} from './shell/bootstrap';
import {browserEntryDestination, isProfileSettingsLocation, profileSettingsAliasPath, type createEntryDestinationStore} from './route-registry-destination';
import {accountReturnPath} from './route-registry-return';
import {localDeviceHint} from './platform/local-device-entry';
import {currentWebEnvironment} from './platform/environment';
import {isServicesPath} from './services/paths';

function isHostedEnvironment(): boolean {
  if (typeof location === 'undefined') return false;
  try { return currentWebEnvironment().hostedWeb; } catch { return false; }
}
/** Hosted Services pages drawn at web.getportico.tv without a server runtime, MediaHost, or GlobalShortcuts. */
export function isHostedServicesSuspendedPath(path: string, hosted = isHostedEnvironment()): boolean {
  return hosted && isServicesPath(path);
}
export function isAccountEntryPath(path: string, hosted = isHostedEnvironment()): boolean {
  return path === '/setup' || path === '/sign-in' || path === '/profiles' || path === '/servers' || path === accountReturnPath
    || isServicesPath(path) && (hosted || path !== '/account');
}
type Location = Readonly<{pathname: string; search?: Record<string, unknown>}>;
type Navigation = Readonly<{fromLocation?: Location; toLocation: Location}>;
type EntryRouter = Readonly<{
  state: Readonly<{location: Location}>;
  subscribe: {
    (event: 'onBeforeNavigate', listener: (event: Navigation) => void): () => void;
    (event: 'onResolved', listener: () => void): () => void;
  };
  navigate: (options: import('./route-registry-destination').WebEntryDestination & {replace: boolean}) => Promise<void>;
}>;
/** Account entry retires private media/feeds before its controls can select a
 * successor. A public hand-off destination is used only AFTER normal restoration
 * has produced a genuine ready binding; it never promotes an account-only grant. */
export function bindWebAccountEntry(router: EntryRouter, bootstrap: Pick<ShellBootstrap, 'subscribe' | 'getSnapshot' | 'start' | 'suspendForAccountEntry' | 'resumeAfterAccountEntry' | 'dispose'>, destination: ReturnType<typeof createEntryDestinationStore> = browserEntryDestination, hosted = isHostedEnvironment()): () => void {
  let closed = false, returningFromEntry = false, navigating = false;
  const suspended = (path: string) => isAccountEntryPath(path, hosted);
  const remember = (location: Location) => {
    if (location.pathname === profileSettingsAliasPath || isProfileSettingsLocation(location)) destination.rememberProfileSettings();
    else if (location.pathname === '/device') destination.rememberLocalDevice();
  };
  const finish = () => {
    const snapshot = bootstrap.getSnapshot();
    if (closed || navigating || snapshot.phase !== 'ready' || !snapshot.binding) return;
    const location = router.state.location;
    if (isProfileSettingsLocation(location) || location.pathname === '/device') {destination.clear();returningFromEntry = false;return;}
    if (!returningFromEntry || suspended(location.pathname)) return;
    const target = destination.take();returningFromEntry = false;
    if (!target) return;
    navigating = true;
    void router.navigate({...target, replace: true}).catch(() => {if (!closed) {
      if (target.to === '/device') destination.rememberLocalDevice();else destination.rememberProfileSettings();
    }}).finally(() => {navigating = false;});
  };
  const off = router.subscribe('onBeforeNavigate', event => {
    if (!navigating) remember(event.toLocation);
    if (suspended(event.toLocation.pathname)) {
      if (event.fromLocation?.pathname === '/device') destination.rememberLocalDevice();
      if (!suspended(event.fromLocation?.pathname ?? '')) bootstrap.suspendForAccountEntry();
    } else if (suspended(event.fromLocation?.pathname ?? '')) {
      returningFromEntry = true;
      void bootstrap.resumeAfterAccountEntry();
    } else if (event.toLocation.pathname !== '/device') {destination.clearLocalDevice();localDeviceHint.clear();}
  });
  const offResolved = router.subscribe('onResolved', finish), offBootstrap = bootstrap.subscribe(finish);
  remember(router.state.location);
  if (suspended(router.state.location.pathname)) bootstrap.suspendForAccountEntry();
  else void bootstrap.start();
  return () => {closed = true;off();offResolved();offBootstrap();bootstrap.dispose();};
}
