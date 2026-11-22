import type {BrowseNode, BrowseSortSelection, BrowseWholeSelection} from './browse.ts';
import {selectionCapabilities, selectionCapabilityNames} from './selection-model/capabilities.ts';
import type {SelectionCapabilities, SelectionKind} from './selection-model/types.ts';

/** Actual caller execution ports, never role-derived grants. Missing means unavailable. */
export type CatalogueSelectionPorts = Readonly<Partial<Pick<SelectionCapabilities, 'play' | 'queue' | 'myList' | 'favorite' | 'watched' | 'edit' | 'delete' | 'playlist' | 'collection'>>>;

/** Titles whose grants come from their own Detail read. */
export const catalogueLeafKinds: readonly SelectionKind[] = Object.freeze(['movie', 'episode', 'song', 'audiobook_file']);
/** Containers a selection takes whole. */
export const catalogueContainerKinds: readonly SelectionKind[] = Object.freeze(['show', 'season', 'album', 'artist', 'book', 'collection']);

/** The rebuilt server sends no per-card grants: a container offers what any profile may do with it (its own state and
 * lists, and playing it); edit and delete are the owner's, from the title's page. Watched where the kind has it. */
export const containerOffered: Readonly<Partial<SelectionCapabilities>> = Object.freeze({
  play: true, queue: true, myList: true, favorite: true, watched: true, playlist: true, collection: true, edit: false, delete: false,
});

/** What a leaf's Detail read offers (detail.ts detailProjection): Play or Resume while it is available, its own state and
 * lists, Edit metadata, and Watched for all but a song. It offers no Add to queue and no Delete. */
export function leafOffered(kind: SelectionKind, available: boolean): Readonly<Partial<SelectionCapabilities>> {
  return Object.freeze({play: available, queue: false, myList: true, favorite: true, watched: kind !== 'song', playlist: true,
    collection: true, edit: true, delete: false});
}

/** One title's selection grants from what is offered and what this app can carry out: both must hold. */
export function grantedCapabilities(kind: SelectionKind, offered: Readonly<Partial<SelectionCapabilities>>, ports: CatalogueSelectionPorts): SelectionCapabilities {
  const both = (name: keyof CatalogueSelectionPorts) => ports[name] === true && offered[name] === true;
  return selectionCapabilities(kind, {play: both('play'), queue: both('queue'), myList: both('myList'), favorite: both('favorite'),
    watched: both('watched'), edit: both('edit'), delete: both('delete'), playlist: both('playlist'), collection: both('collection')});
}

/** The grants a library title of this kind has, exactly as picking it alone gives them (readCatalogueSelectionItem),
 * or undefined for a kind a selection cannot pick. */
export function catalogueTitleCapabilities(kind: SelectionKind, available: boolean, ports: CatalogueSelectionPorts): SelectionCapabilities | undefined {
  if (catalogueContainerKinds.includes(kind)) return grantedCapabilities(kind, containerOffered, ports);
  if (catalogueLeafKinds.includes(kind)) return grantedCapabilities(kind, leafOffered(kind, available), ports);
  return undefined;
}

/** The server's count of a list by kind (GET /v1/items/selection), in the app's card kinds. */
export type SelectionCountRow = Readonly<{kind: SelectionKind; count: number; available: number}>;
export type WholeSelectionView = Readonly<{libraryId: string; pivot: string; filter: BrowseNode | null; sort: readonly BrowseSortSelection[]; catalogRevision: string}>;

/** "Select all" over one library's list: each kind's count, and each action's count as the sum of what every title of
 * that kind is granted when picked alone, so a window of picked titles always fits it. Undefined when the list holds a
 * kind a selection cannot pick, or holds nothing. */
export function wholeSelectionFromCounts(rows: readonly SelectionCountRow[], view: WholeSelectionView, ports: CatalogueSelectionPorts): BrowseWholeSelection | undefined {
  const types: BrowseWholeSelection['types'][number][] = [];
  let total = 0;
  for (const row of rows) {
    if (!Number.isSafeInteger(row.count) || !Number.isSafeInteger(row.available) || row.available < 0 || row.available > row.count) return;
    if (row.count === 0) continue;
    const ready = catalogueTitleCapabilities(row.kind, true, ports), missing = catalogueTitleCapabilities(row.kind, false, ports);
    if (!ready || !missing) return;
    const counts = Object.fromEntries(selectionCapabilityNames.map(name =>
      [name, (ready[name] ? row.available : 0) + (missing[name] ? row.count - row.available : 0)])) as BrowseWholeSelection['types'][number]['capabilities'];
    types.push(Object.freeze({kind: row.kind, count: row.count, capabilities: Object.freeze(counts),
      libraries: Object.freeze([Object.freeze({libraryId: view.libraryId, count: row.count})])}));
    total += row.count;
  }
  if (!types.length) return;
  return Object.freeze({libraryId: view.libraryId, pivot: view.pivot, filter: view.filter, sort: view.sort, catalogRevision: view.catalogRevision,
    total, types: Object.freeze(types)});
}
