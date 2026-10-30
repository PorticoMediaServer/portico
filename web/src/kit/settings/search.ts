import type {MessageId} from '@i18n';
import {settingsRowIndex, settingsSpec, type SettingsRow, type SettingsSection} from '@core/presentation/settings-spec.ts';

export type SettingSearchHit = Readonly<{section: SettingsSection; rowId: string; label: string; context: readonly string[]; panelId?: string; tabId?: string; advanced: boolean}>;
/** Searches configuration copy only, independently of table people/media and their private data. */
export function searchSettings(query: string, text: (id: MessageId) => string, visible: (row: SettingsRow, section: SettingsSection) => boolean): readonly SettingSearchHit[] {
  const normalized = query.trim().toLocaleLowerCase(), words = normalized.split(/\s+/).filter(Boolean);
  if (normalized.length < 2) return [];
  const keys = /[._]/.test(normalized), hits: SettingSearchHit[] = [];
  for (const {section, row, group, panelId, tabId, advanced} of settingsRowIndex.values()) {
    if (!visible(row, section)) continue;
    const c = row.control, preset = c.options?.some(option => option.sets);
    const corpus = [text(row.label), row.help && text(row.help), group.title && text(group.title), c.label && text(c.label), ...(c.options ?? []).filter(option => !preset || option.sets).map(option => text(option.label)), ...(keys ? [row.key, row.id] : [])].filter(Boolean).join(' ').toLocaleLowerCase();
    if (!words.every(word => corpus.includes(word))) continue;
    const panel = panelId ? section.panels?.[panelId] : undefined, tab = panel?.tabs?.find(tab => tab.id === tabId);
    const context = [panel && (panel.searchName ? text(panel.searchName) : panel.title ? text(panel.title) : panelId), tab && text(tab.title)].filter((value): value is string => !!value);
    hits.push({section, rowId: row.id, label: text(row.label), context, advanced, ...(panelId ? {panelId} : {}), ...(tabId ? {tabId} : {})});
  }
  return hits;
}
export type SettingsRoute = Readonly<{scope: 'account' | 'server'; section: string; row?: string; channelId?: string}>;
export function settingsRoute(raw: Record<string, unknown>, permitted: (section: SettingsSection) => boolean, rowPermitted?: (row: SettingsRow, section: SettingsSection) => boolean): SettingsRoute {
  const scope = raw.scope === 'server' ? 'server' : 'account';
  const section = settingsSpec.find(section => section.scope === scope && section.id === raw.section && permitted(section)) ?? settingsSpec.find(section => section.scope === scope && permitted(section)) ?? settingsSpec[0];
  const location = typeof raw.row === 'string' ? settingsRowIndex.get(raw.row) : undefined;
  return {scope: section.scope, section: section.id, ...(section.scope === 'server' && section.id === 'live' && typeof raw.channelId === 'string' && /^[A-Za-z0-9_-]{1,256}$/.test(raw.channelId) ? {channelId: raw.channelId} : {}), ...(location?.section === section && (!rowPermitted || rowPermitted(location.row, section)) ? {row: location.row.id} : {})};
}
