/** Stable destinations shared by web links and native handoffs. Visibility still depends on authorization. */
export const settingsDestinations = ['personal','listening','account','account-profiles','account-sessions','account-membership','about','libraries','connection','remote','storage','activity','sharing','support','live-sources','library-channels','playback','console','runtime','maintenance','jobs','triage','feedback','notices'] as const;
export type SettingsDestination = typeof settingsDestinations[number];
export function isSettingsDestination(value:unknown):value is SettingsDestination {
 return typeof value==='string'&&(settingsDestinations as readonly string[]).includes(value);
}
export const serverSettingsDestinations:readonly SettingsDestination[] = ['libraries','connection','remote','storage','activity','sharing','support','live-sources','library-channels','playback','console','runtime','maintenance','jobs','triage'];
