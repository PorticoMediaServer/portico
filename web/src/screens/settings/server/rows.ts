import type {SettingsRow} from '@core/presentation/settings-spec.ts';

/** A section inside Settings, optionally with the row to open and flash. */
export type SettingsTarget = Readonly<{scope: 'account' | 'server'; section: string; row?: string}>;

/**
 * Rows whose drawn control this batch does not own yet. They stay absent rather
 * than showing an empty control or a setting with no engine (ruling 3).
 */
export const pendingServerRows: ReadonlySet<string> = new Set(['storage.upgradeCopy']);

/** The rows this page draws from its own owner-scoped producers. */
export const factRows: ReadonlySet<string> = new Set(['storage.dataFolder', 'storage.usage', 'storage.downloads', 'storage.emptyCache']);

/** The Retention rows that name the page which owns their value. */
export const sectionLinkRows: ReadonlySet<string> = new Set(['storage.keepHistory', 'storage.keepLogsLink']);

/** The Backups rows this batch draws; they need the server's own backup answer first. */
export const backupRows: ReadonlySet<string> = new Set(['storage.backupFolder', 'storage.backupNow', 'storage.backups', 'storage.backupPassphrase']);

/** Restoring from a file or folder only needs the owner's live session; the file is chosen here. */
export const fileRestoreRows: ReadonlySet<string> = new Set(['storage.restoreFile']);

/** What the page has actually learned: the selected session is live and the backup document arrived. */
export type StorageRowFacts = Readonly<{live: boolean; backups: boolean; backupProtectionKnown?: boolean; dataDirectoryOnNetwork?: boolean}>;

/**
 * A drawn link's `to` is `server/<section>[/<row>]` for a page inside Settings.
 * A mock page (`.html`) is design artwork, never app navigation, so it is refused.
 */
export function sectionTarget(to: string | undefined): SettingsTarget | undefined {
  if (!to || to.includes('.html')) return undefined;
  const [scope, section, row] = to.split('/');
  if (scope !== 'account' && scope !== 'server') return undefined;
  if (!section) return undefined;
  return {scope, section, ...(row ? {row} : {})};
}

/** The Storage surfaces answer for their own rows and leave every other row to the shared reader. */
export function storageRowState(row: SettingsRow, facts: StorageRowFacts): Readonly<{present: boolean}> | undefined {
  if (row.id === 'storage.backupKeysNote') return {present: facts.live && facts.backupProtectionKnown === true};
  if (row.id === 'storage.networkNotice') return {present: facts.live && facts.dataDirectoryOnNetwork === true};
  if (pendingServerRows.has(row.id)) return {present: false};
  if (factRows.has(row.id) || fileRestoreRows.has(row.id)) return {present: facts.live};
  if (backupRows.has(row.id)) return {present: facts.backups};
  if (!sectionLinkRows.has(row.id)) return undefined;
  // A link with no in-app target would be a dead control, so it stays absent.
  return {present: sectionTarget(row.control.to) !== undefined};
}
