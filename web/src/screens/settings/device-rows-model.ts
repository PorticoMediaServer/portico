import {defaultI18n} from '@i18n';
import type {SettingsRow} from '@core/presentation/settings-spec.ts';
import type {SettingPresentation} from '../../kit/settings';

/** Rows the web Device section owns beyond the controller's value-backed rows. */
export const deviceSyntheticRowIds = ['device.linkTv', 'device.server', 'device.serverVersion', 'device.help', 'device.licenses', 'device.appVersion'] as const;
export type DeviceRowFacts = Readonly<{serverName: string; serverVersion?: string; linkTvAvailable?: boolean}>;
export function isDeviceSyntheticRow(id: string): boolean {
  return (deviceSyntheticRowIds as readonly string[]).includes(id);
}
/** The drawn external Help address; the row opens it, the address is what the row shows. */
export const deviceHelpAddress = 'getportico.tv/docs/';
export const deviceHelpURL = 'https://getportico.tv/docs/';

/** Real facts only: the connected server's name and version, never a guessed value. */
export function deviceRowState(row: SettingsRow, facts: DeviceRowFacts): SettingPresentation | undefined {
  if (row.id === 'device.linkTv') return {present: facts.linkTvAvailable === true};
  if (row.id === 'device.server') return {present: true, help: defaultI18n.t('next.settingsSpec.account.device.groups.3.rows.0.help', {server: facts.serverName})};
  if (row.id === 'device.serverVersion') return facts.serverVersion ? {present: true, value: facts.serverVersion, displayValue: facts.serverVersion, readOnly: true} : undefined;
  if (row.id === 'device.appVersion') return {present: true};
  if (row.id === 'device.licenses') return {present: true};
  if (row.id === 'device.help') return {present: true};
  return undefined;
}
