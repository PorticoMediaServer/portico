import type {SettingsBulkAccess} from '@core/settings-people.ts';

export type BulkLibraryChoice = 'keep' | 'all' | 'some';

/** Empty allowedLibraries grants all libraries. A still-empty custom selection
 * must never be confused with that explicit choice when the owner presses Apply. */
export function bulkAccessWithLibraries(access: SettingsBulkAccess | undefined, choice: BulkLibraryChoice, ids: readonly string[]): SettingsBulkAccess {
  const next = {...access};
  if (choice === 'keep') delete next.allowedLibraries;
  else if (choice === 'all') next.allowedLibraries = [];
  else {
    if (!ids.length) throw new Error('bulk_library_required');
    next.allowedLibraries = [...new Set(ids)];
  }
  return next;
}
