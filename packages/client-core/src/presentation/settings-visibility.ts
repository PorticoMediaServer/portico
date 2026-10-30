import {settingsRowIndex, type SettingsOperatingSystem, type SettingsPlatform, type SettingsRow, type SettingsSection} from './settings-spec.ts';

/** Supplied by the actual platform adapter. A browser on Windows is still `browser`. */
export type SettingsEnvironment = Readonly<{
  platform: SettingsPlatform; os: SettingsOperatingSystem;
  signIn?: 'portico' | 'local'; role?: 'owner' | 'admin' | 'member';
}>;
export const browserSettingsEnvironment: SettingsEnvironment = Object.freeze({platform: 'web', os: 'browser'});
const systems: Readonly<Record<SettingsPlatform, readonly SettingsOperatingSystem[]>> = {
  web: ['browser'], desktop: ['windows', 'macos', 'linux'], phone: ['android', 'ios', 'ipados'],
  tv: ['tvos', 'androidtv', 'firetv', 'roku', 'webos', 'tizen'],
};
function platformVisible(platforms: readonly SettingsPlatform[] | undefined, environment: SettingsEnvironment): boolean {
  return !platforms || platforms.includes(environment.platform) || environment.platform === 'desktop' && platforms.includes('web');
}
export function settingsSectionVisible(section: SettingsSection, environment: SettingsEnvironment): boolean {
  return systems[environment.platform].includes(environment.os) && platformVisible(section.platforms, environment);
}
/** Filter branches before rendering, searching or summarising; no product OS tags. */
export function settingsRowVisible(row: SettingsRow, environment: SettingsEnvironment): boolean {
  const location = settingsRowIndex.get(row.id);
  if (!location || !settingsSectionVisible(location.section, environment)) return false;
  return [...location.ancestors, row].every(candidate => {
    if (!platformVisible(candidate.platforms ?? location.group.platforms ?? location.section.platforms, environment)) return false;
    if (candidate.os && !candidate.os.includes(environment.os)) return false;
    if (candidate.audience === 'owner' && environment.role !== 'owner') return false;
    if (candidate.shapes && (!environment.signIn || !candidate.shapes.includes(environment.signIn === 'local' ? 'local-account' : 'portico-account'))) return false;
    return true;
  });
}
