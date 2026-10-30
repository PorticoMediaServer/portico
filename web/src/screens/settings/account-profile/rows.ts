import {PROFILE_ART, type ProfileArt} from '@core/profile-management.ts';
import type {SettingsValue} from '@core/presentation/settings-spec.ts';
import type {SettingPresentation} from '../../../kit/settings';
import type {ProfileSettingsSource} from './source';

/** Only supported rows are projected; the spec's illustrative values never
 * become defaults for a live account. Other profile groups need their own
 * checked producers before the route may expose them. */
export function profileSettingState(source: ProfileSettingsSource, id: string): SettingPresentation | undefined {
  if (!['profile.name', 'profile.color', 'profile.username', 'profile.pin', 'profile.pinChange', 'profile.pinForget', 'profile.delete', 'profile.picture'].includes(id)) return undefined;
  const state = source.getSnapshot(), document = state.document, profile = source.selected();
  if (!document || !profile) return {present: false};
  if (id === 'profile.pin') return {present: true, value: profile.pinRequired, disabled: state.busy || !document.canManage};
  if (id === 'profile.pinChange' || id === 'profile.pinForget') return {present: document.canManage && profile.pinRequired, disabled: state.busy};
  if (id === 'profile.picture') return {present: document.canManage, disabled: state.busy};
  if (id === 'profile.delete') return {present: document.canManage && !profile.primary, disabled: state.busy};
  if (id === 'profile.username') return {present: true, displayValue: document.account.username, readOnly: true};
  return {present: true, value: id === 'profile.name' ? profile.name : profile.art,
    disabled: state.busy || !document.canManage, readOnly: !document.canManage,
    saving: state.busy};
}

/** Returning a promise lets the route use its normal safe error surface. */
export function changeProfileSetting(source: ProfileSettingsSource, id: string, value: SettingsValue): Promise<void> | undefined {
  if (id === 'profile.name' && typeof value === 'string') return source.rename(value);
  if (id === 'profile.color' && typeof value === 'string' && PROFILE_ART.includes(value as ProfileArt)) return source.color(value as ProfileArt);
  return undefined;
}
