import {defaultI18n} from '@i18n';
import type {SettingsValue} from '@core/presentation/settings-spec.ts';
import type {SettingPresentation} from '../../../kit/settings';
import type {ProfileSettingsSource} from './source';
import type {ProfileRestrictionsSource} from './restrictions';
const flags = { 'profile.allowUnrated': 'allowUnrated', 'profile.allowDownloads': 'allowDownloads', 'profile.allowLiveTv': 'allowLiveTv', 'profile.allowRecordings': 'allowDvr', 'profile.allowWatchTogether': 'allowWatchTogether'} as const;
export const isProfileRestriction = (id: string) => Object.hasOwn(flags, id) || ['profile.maxRating', 'profile.hiddenLabels'].includes(id);
/** `serverOwner`: only the server owner may switch Recordings on (Who can record); anyone who
 * manages the profile may switch it off. */
export function restrictionRow(source: ProfileRestrictionsSource, profiles: ProfileSettingsSource, id: string, serverOwner = false): SettingPresentation | undefined {
  if (!isProfileRestriction(id)) return undefined;
  const state = source.getSnapshot(), document = state.document;
  if (!document) return {present: false};
  const manager = profiles.getSnapshot().document?.canManage === true;
  const common = {present: true, disabled: state.busy || !manager, readOnly: !manager, saving: state.busy};
  if (id === 'profile.hiddenLabels') return {...common, value: [...document.blockedLabels]};
  if (id === 'profile.maxRating') {
    const system = state.systems?.find(system => system.id === document.ratingSystem) ?? (document.ratingSystem === '' ? state.systems?.[0] : undefined);
    return {...common, value: document.maximumAgeRating, choices: [
      {value: '', label: defaultI18n.t('next.settingsSpec.account.profile.groups.3.rows.0.control.options.0.label')},
      ...(system?.values.map(value => ({value: value.code, label: value.label})) ?? []),
    ], disabled: common.disabled || !system};
  }
  const value = document[flags[id as keyof typeof flags]];
  if (id === 'profile.allowRecordings' && !value && !serverOwner) return {...common, value, disabled: true};
  return {...common, value};
}
export function changeRestriction(source: ProfileRestrictionsSource, id: string, value: SettingsValue): Promise<void> | undefined {
  const document = source.getSnapshot().document;
  if (!document) return undefined;
  if (id === 'profile.maxRating' && typeof value === 'string') {
    const system = source.getSnapshot().systems?.find(system => system.id === document.ratingSystem) ?? (document.ratingSystem === '' ? source.getSnapshot().systems?.[0] : undefined);
    if (value !== '' && !system?.values.some(option => option.code === value)) return undefined;
    return source.save({maximumAgeRating: value, ratingSystem: system?.id ?? document.ratingSystem});
  }
  if (id === 'profile.hiddenLabels' && Array.isArray(value) && value.every(label => typeof label === 'string' && label.length > 0 && label.length <= 120) && value.length <= 64 && new Set(value.map(label => String(label).toLowerCase())).size === value.length) return source.save({blockedLabels: value as string[]});
  if (Object.hasOwn(flags, id) && typeof value === 'boolean') return source.save({[flags[id as keyof typeof flags]]: value});
  return undefined;
}
