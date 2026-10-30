import type {ManagedLibrary} from '@core/library-management.ts';
import type {InventorySource} from '@core/library-inventory.ts';
import {defaultI18n, type MessageId} from '@i18n';
import {movedLibraryIds} from './library-order';
export {movedLibraryIds} from './library-order';

/** The seven drawn kinds, in the order Portico lists them. */
export const libraryKindIds = Object.freeze(['movie', 'tv', 'anime', 'music', 'audiobook', 'book', 'comic'] as const);

/** The frames' own name and one-line description for each kind, shared by every
 * place that asks the owner to choose one. */
export type LibraryKind = ManagedLibrary['kind'];
export const libraryKindMessages: Readonly<Record<LibraryKind, Readonly<{label: MessageId; description: MessageId}>>> = Object.freeze({
  movie: {label: 'next.ownerLibraries.kind.movie', description: 'next.ownerLibraries.kindDescription.movie'},
  tv: {label: 'next.ownerLibraries.kind.tv', description: 'next.ownerLibraries.kindDescription.tv'},
  anime: {label: 'next.ownerLibraries.kind.anime', description: 'next.ownerLibraries.kindDescription.anime'},
  music: {label: 'next.ownerLibraries.kind.music', description: 'next.ownerLibraries.kindDescription.music'},
  audiobook: {label: 'next.ownerLibraries.kind.audiobook', description: 'next.ownerLibraries.kindDescription.audiobook'},
  book: {label: 'next.ownerLibraries.kind.book', description: 'next.ownerLibraries.kindDescription.book'},
  comic: {label: 'next.ownerLibraries.kind.comic', description: 'next.ownerLibraries.kindDescription.comic'},
});
export const libraryKindLabel = (kind: LibraryKind) => defaultI18n.t(libraryKindMessages[kind].label);
export const libraryKindDescription = (kind: LibraryKind) => defaultI18n.t(libraryKindMessages[kind].description);

/** Audiobooks, books and comics have no online source today: local files only. */
export const localOnlyKind = (kind: ManagedLibrary['kind']) => kind === 'audiobook' || kind === 'book' || kind === 'comic';

/** The metadata source a new library starts with, from its kind alone. */
export const metadataAgentFor = (kind: ManagedLibrary['kind']): 'local' | 'online' => localOnlyKind(kind) ? 'local' : 'online';

/** One drawn Libraries row: the library's own name, folder and scan state. */
export type LibraryRowModel = Readonly<{
  id: string; name: string; folder: string; kind: LibraryKind;
  scanning: boolean; canScan: boolean; canMoveUp: boolean; canMoveDown: boolean;
}>;

/** A scan is running only while the server says so. */
export const scanning = (library: ManagedLibrary) => library.lastScan?.status === 'running';

/** The frame draws the folder and, while a scan runs, the one word "Scanning".
 * It never draws a percentage, a file count or a bar on this page. */
export function libraryRows(libraries: readonly ManagedLibrary[]): readonly LibraryRowModel[] {
  return libraries.map((library, index) => Object.freeze({
    id: library.id,
    name: library.name,
    kind: library.kind,
    folder: library.source.path,
    scanning: scanning(library),
    canScan: library.actions.includes('scan') && !scanning(library),
    canMoveUp: index > 0,
    canMoveDown: index < libraries.length - 1,
  }));
}

/** Stable complete order, or undefined when the move changes nothing. */
export function movedIds(libraries: readonly ManagedLibrary[], id: string, offset: -1 | 1): readonly string[] | undefined {
  return movedLibraryIds(libraries, id, libraries.findIndex(library => library.id === id) + offset);
}

/** The two scan commands the drawn library page carries as action rows. */
const SCAN_ACTIONS: Readonly<Record<string, 'incremental' | 'full'>> = Object.freeze({
  'libraries.scanNow': 'incremental',
  'libraries.scanAll': 'full',
});

/** The scan a drawn action row runs, or undefined when the row is not a scan. */
export const libraryScanAction = (rowId: string) => SCAN_ACTIONS[rowId];

/** An action row belongs to the per-library page only while that page is open
 * for one concrete library. */
export const isLibraryPanelContext = (context: Readonly<{panelId?: string; recordId?: string}>) =>
  context.panelId === 'library' && !!context.recordId;

/** One drawn folder row of a library page: where it is, whether it is a local
 * disk or a network share, and the server's own health word. */
export type LibraryFolderRow = Readonly<{id: string; name: string; path: string; network: boolean; health: string; enabled: boolean}>;

export function folderRows(sources: readonly InventorySource[]): readonly LibraryFolderRow[] {
  return sources.map(source => Object.freeze({
    id: source.id, name: source.name, path: source.path,
    network: source.classification === 'network', health: source.health, enabled: source.enabled,
  }));
}

/** How often the server checks one folder for changes, in the drawn order. */
export const folderIntervalOptions = () => Object.freeze([
  {value: '900', label: 'next.settingsSpec.server.libraries.panels.library.tabs.1.groups.1.rows.3.control.options.0.label' as const},
  {value: '3600', label: 'next.settingsSpec.server.libraries.panels.library.tabs.1.groups.1.rows.3.control.options.1.label' as const},
  {value: '21600', label: 'next.settingsSpec.server.libraries.panels.library.tabs.1.groups.1.rows.3.control.options.2.label' as const},
  {value: '86400', label: 'next.settingsSpec.server.libraries.panels.library.tabs.1.groups.1.rows.3.control.options.3.label' as const},
  {value: '0', label: 'next.settingsSpec.server.libraries.panels.library.tabs.1.groups.1.rows.3.control.options.4.label' as const},
]);
