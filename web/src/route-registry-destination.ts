/** Public navigation only. This marker contains no account, username, credential,
 * proof or arbitrary URL, and cannot authorize a Settings read or mutation. */
export const profileSettingsAliasPath = '/settings/profile';
export const profileSettingsDestination = Object.freeze({to: '/settings' as const, search: Object.freeze({scope: 'account' as const, section: 'profile' as const})});
export const localDeviceDestination = Object.freeze({to: '/device' as const, search: Object.freeze({})});
export type WebEntryDestination = typeof profileSettingsDestination | typeof localDeviceDestination;
const key = 'portico.web.account-entry-destination.v1';
type Marker = 'profile-settings' | 'local-device';
type NavigationStorage = Pick<Storage, 'getItem' | 'setItem' | 'removeItem'>;
export function createEntryDestinationStore(storage: () => NavigationStorage | undefined = () => undefined) {
  let remembered: Marker | undefined;
  const clear = () => {remembered = undefined;try {storage()?.removeItem(key);} catch { /* Public navigation also works in memory. */ }};
  const read = (): Marker | undefined => {
    try {
      const value = storage()?.getItem(key);
      if (value === 'profile-settings' || value === 'local-device') return value;
      if (value !== undefined && value !== null) {clear();return;}
    } catch { /* A storage restriction never grants authority or blocks sign-in. */ }
    return remembered;
  };
  const remember = (marker: Marker) => {remembered = marker;try {storage()?.setItem(key, marker);} catch { /* Keep only this tab's fixed public intent. */ }};
  return Object.freeze({
    rememberProfileSettings() {remember('profile-settings');},
    rememberLocalDevice() {remember('local-device');},
    clearLocalDevice() {if (read() === 'local-device') clear();},
    take(): WebEntryDestination | undefined {const marker = read();if (!marker) return;clear();return marker === 'local-device' ? localDeviceDestination : profileSettingsDestination;},
    clear,
  });
}
// No I/O on import. Each browser origin/tab owns its own sessionStorage marker.
export const browserEntryDestination = createEntryDestinationStore(() => typeof window === 'undefined' ? undefined : window.sessionStorage);
export function isProfileSettingsLocation(location: Readonly<{pathname: string; search?: Record<string, unknown>}>): boolean {
  return location.pathname === '/settings' && location.search?.scope === 'account' && location.search.section === 'profile';
}
