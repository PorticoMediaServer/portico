import {defaultI18n} from '@i18n';
import type {SettingsValue} from '@core/presentation/settings-spec.ts';
import type {SettingPresentation} from '../../../kit/settings';
import type {ProfileSettingsSource} from './source';
import type {ProfileRestrictionsSource} from './restrictions';
import {CodedError} from '@core/server-messages.ts';
/** Allow unrated titles, downloads, Live TV, recordings and Watch Together are not limits the server keeps yet,
 * so their rows are not shown. */
const absent = ['profile.allowUnrated', 'profile.allowDownloads', 'profile.allowLiveTv', 'profile.allowRecordings', 'profile.allowWatchTogether'];
export const isProfileRestriction = (id: string) => absent.includes(id) || ['profile.maxRating', 'profile.hiddenLabels'].includes(id);
export function restrictionRow(source: ProfileRestrictionsSource, profiles: ProfileSettingsSource, id: string, _serverOwner = false): SettingPresentation | undefined {
  if (!isProfileRestriction(id)) return undefined;
  if (absent.includes(id)) return {present: false};
  const state = source.getSnapshot(), document = state.document;
  // Limits that could not be read say so once, on the Highest rating row, instead of the rows vanishing.
  if (!document) return state.error && !state.loading && id === 'profile.maxRating'
    ? {present: true, readOnly: true, displayValue: '', error: defaultI18n.t('next.settings.profileLimitsFailed')} : {present: false};
  const manager = profiles.getSnapshot().document?.canManage === true;
  const common = {present: true, disabled: state.busy || !manager, readOnly: !manager, saving: state.busy};
  // Blocked Tags are kept by id; the row shows and takes their names.
  if (id === 'profile.hiddenLabels') return {...common, value: document.blockedLabels.map(tag => state.tags?.find(choice => choice.id === tag)?.name ?? tag),
    ...(state.problem ? {error: state.problem} : {})};
  if (id === 'profile.maxRating') {
    const system = state.systems?.find(system => system.id === document.ratingSystem) ?? (document.ratingSystem === '' ? state.systems?.[0] : undefined);
    return {...common, value: document.maximumAgeRating, choices: [
      {value: '', label: defaultI18n.t('next.settingsSpec.account.profile.groups.3.rows.0.control.options.0.label')},
      ...(system?.values.map(value => ({value: value.code, label: value.label})) ?? []),
    ], disabled: common.disabled || !system};
  }
  return undefined;
}
export function changeRestriction(source: ProfileRestrictionsSource, id: string, value: SettingsValue): Promise<void> | undefined {
  const document = source.getSnapshot().document;
  if (!document) return undefined;
  if (id === 'profile.maxRating' && typeof value === 'string') {
    const system = source.getSnapshot().systems?.find(system => system.id === document.ratingSystem) ?? (document.ratingSystem === '' ? source.getSnapshot().systems?.[0] : undefined);
    if (value !== '' && !system?.values.some(option => option.code === value)) return undefined;
    return source.save({maximumAgeRating: value, ratingSystem: system?.id ?? document.ratingSystem});
  }
  if (id === 'profile.hiddenLabels' && Array.isArray(value) && value.length <= 200) {
    const tags = source.getSnapshot().tags ?? [], blocked: string[] = [];
    for (const entry of value) {
      const name = String(entry).trim(), tag = tags.find(choice => choice.id === name || choice.name.toLowerCase() === name.toLowerCase());
      // A Tag already blocked but not in the picker's list (no title the main profile sees has it) is shown and kept by its id.
      if (!tag && document.blockedLabels.includes(name)) {if (!blocked.includes(name)) blocked.push(name); continue;}
      if (!tag) {
        const problem = defaultI18n.t('next.settings.noSuchTag', {name});
        source.refuse(problem);
        return Promise.reject(new CodedError('unknown_tag', problem));
      }
      if (!blocked.includes(tag.id)) blocked.push(tag.id);
    }
    return source.save({blockedLabels: blocked});
  }
  return undefined;
}
