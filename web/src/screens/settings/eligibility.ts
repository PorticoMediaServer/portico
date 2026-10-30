import {settingsRowIndex, type SettingsRow} from '@core/presentation/settings-spec.ts';
import type {SettingsContext} from './Settings';

export type SettingsEligibility = Readonly<{
  /** Actual checked producer/effect availability, independent of stored values. */
  hasEngine: (row: SettingsRow, context: SettingsContext) => boolean;
  library: (context: SettingsContext) => Readonly<{libraryId: string; eligibleControls: readonly string[]}> | undefined;
}>;

/** Shared by the Library row, episode artwork command and related notice adapters. */
export function libraryControlEligible(document: Readonly<{libraryId: string; eligibleControls: readonly string[]}> | undefined, libraryId: string | undefined, rowId: string): boolean {
  return !!document && !!libraryId && document.libraryId === libraryId && document.eligibleControls.includes(rowId);
}

export function settingsRowEligible(row: SettingsRow, context: SettingsContext, sources: SettingsEligibility): boolean {
  const location = settingsRowIndex.get(row.id);
  if (!location || location.section !== context.section) return false;
  // The fifteen Reading controls store preferences before their Phase 8 reader exists.
  if (location.section.scope === 'account' && location.section.id === 'reading') return true;
  if (row.id === 'libraries.episodeThumbs' && !libraryControlEligible(sources.library(context), context.recordId, row.id)) return false;
  return sources.hasEngine(row, context);
}

/** Config-key search uses the same visibility decision as rows and actions. */
export function configurationFieldEligible(field: Readonly<{id: string; key: string}>, eligible: (row: SettingsRow, context: SettingsContext) => boolean): boolean {
  const location = settingsRowIndex.get(field.id) ?? [...settingsRowIndex.values()].find(location => location.row.key === field.key);
  return !location || eligible(location.row, {section: location.section, panelId: location.panelId, tabId: location.tabId});
}
