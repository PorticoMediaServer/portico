import type {FilesystemEntry, FilesystemPage} from '../../../../../../packages/client-core/src/administration';

export type ExplorerFolder = Readonly<{path: string; name: string}>;
export type ExplorerSelectionMode = 'folders' | 'file-or-folder';

export function explorerEntries(page: FilesystemPage | undefined, mode: ExplorerSelectionMode): readonly FilesystemEntry[] {
  return (page?.entries ?? []).filter(entry => entry.kind === 'directory' || mode === 'file-or-folder' && entry.kind === 'file');
}

/** Selection belongs to the opening, not to the rows currently mounted by FlatList. */
export function toggleExplorerSelection(current: readonly ExplorerFolder[], folder: ExplorerFolder, multiple: boolean): readonly ExplorerFolder[] {
  if (!multiple) return [folder];
  return current.some(value => value.path === folder.path) ? current.filter(value => value.path !== folder.path) : [...current, folder];
}

/** A page from another directory, or a repeated cursor, must never enter this list. */
export function appendExplorerPage(current: FilesystemPage, next: FilesystemPage): FilesystemPage {
  if (next.path !== current.path || next.separator !== current.separator || next.platform !== current.platform ||
    next.nextCursor && next.nextCursor === current.nextCursor) throw new Error('The folder listing changed. Open it again.');
  return Object.freeze({...next, entries: Object.freeze([...new Map([...current.entries, ...next.entries].map(entry => [entry.path, entry])).values()])});
}
