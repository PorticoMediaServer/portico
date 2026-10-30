import type {BrowseNode, BrowseSortSelection} from '../browse.ts';
import type {LibraryMemoryScope, RememberedFilter, RememberedSort} from '../library-memory.ts';

/**
 * Groups of titles as the common browse route sorts and filters them (Justin, 14 October: the same
 * sort and filter in every grouping). Pure and shared by every client.
 *
 * A group is a condition that is always sent (the group itself) and a pivot its members are read
 * as. A group that spans the viewer's libraries (a person's titles, a playlist, a personal
 * collection, a Home row, My List, Favorites, the history) is read across them, from
 * `acrossLibraries`, as one list; a library's own group (a collection, a season, an album, a
 * book's parts) is read in its library.
 */

/** The server sort for a group's own order: a playlist's positions, a collection's order, a show's episode order, an album's tracks, an artist's releases by type, a book's parts, a Home row's order. */
export const groupOrderSort = 'groupOrder';

/** Whether a vocabulary offers the group's own order as a sort (it does on every current server). */
export const offersGroupOrder = (sorts: readonly Readonly<{id: string}>[] | undefined): boolean => !!sorts?.some(sort => sort.id === groupOrderSort);

/**
 * The sort a group's page sends: the viewer's when they chose one; otherwise the group's own order,
 * which the server keeps under any filter, when the group has one and the server offers it.
 */
export function groupSort(chosen: BrowseSortSelection | undefined, ownOrder: boolean, sorts: readonly Readonly<{id: string}>[] | undefined, fallback: BrowseSortSelection): BrowseSortSelection {
  if (chosen) return chosen;
  return ownOrder && offersGroupOrder(sorts) ? {field: groupOrderSort, direction: 'asc'} : fallback;
}

/** The groups that span libraries. */
export type AcrossGroup = 'person' | 'cast' | 'crew' | 'list' | 'savedCollection' | 'homeRow' | 'watchlist' | 'favorites' | 'history';

/** The condition that names a group across libraries. */
export function acrossCondition(group: AcrossGroup, value = ''): BrowseNode {
  switch (group) {
    case 'watchlist': return {variant: 'predicate', field: 'watchlisted', operator: 'equals', value: true};
    case 'favorites': return {variant: 'predicate', field: 'favorite', operator: 'equals', value: true};
    case 'history': return {variant: 'predicate', field: 'lastPlayedAt', operator: 'is-present', value: null};
    default: return {variant: 'predicate', field: group, operator: 'equals', value};
  }
}

/**
 * The pivot a group across libraries is read as: the things played one at a time for a playlist
 * and the history (a film, an episode, a song, a book's part), the titles themselves otherwise.
 */
export const acrossPivot = (group: AcrossGroup): 'titles' | 'items' => (group === 'list' || group === 'history' ? 'items' : 'titles');

/** Whether a group across libraries has an order of its own on the server (a playlist's, a personal collection's, a row's, the history's most recent first). */
export const acrossOwnOrder = (group: AcrossGroup): boolean => group === 'list' || group === 'savedCollection' || group === 'homeRow' || group === 'history';

/**
 * The surface a group is entered through, by the field that names it, for a vocabulary that lists
 * its surfaces. A library's and the across-libraries vocabulary share these names.
 */
export const groupSurfaces: Readonly<Record<string, string>> = {collection: 'collection-titles', memberOf: 'container-titles', person: 'person-titles', cast: 'cast-titles', crew: 'crew-titles', list: 'list-titles', savedCollection: 'saved-collection-titles', episodeGroup: 'episode-group', homeRow: 'row-titles', search: 'search-results'};

/** One of a show's episode groups (`all`, `specials`, `unassigned_absolute`, `range:N`) as a condition. */
export const episodeGroupCondition = (showId: string, group: string): BrowseNode => ({variant: 'predicate', field: 'episodeGroup', operator: 'equals', value: `${showId}/${group}`});

// ---- remembering a group's choices ----

/**
 * The choices made on each group's page (a sort, filters) are remembered on the device the way a
 * library tab's are, in a store of their own beside it so groups never push libraries out: the
 * last 64 groups, per profile.
 */
export const groupMemoryScope = (scope: LibraryMemoryScope): LibraryMemoryScope => ({serverId: scope.serverId, viewerId: `${scope.viewerId}#groups`});
/** The one "tab" each remembered group has. */
export const groupMemoryTab = 'group';

/** A short, stable name for a group: its library, pivot and condition. */
export function groupMemoryId(libraryId: string, pivot: string, base: readonly BrowseNode[]): string {
  const text = JSON.stringify([libraryId, pivot, base]);
  // FNV-1a, twice with different offsets: a name well under the store's bound for any condition.
  let a = 0x811c9dc5, b = 0x01000193 ^ text.length;
  for (let i = 0; i < text.length; i++) {
    const c = text.charCodeAt(i);
    a = Math.imul(a ^ c, 0x01000193) >>> 0;
    b = Math.imul(b ^ c, 0x5bd1e995) >>> 0;
  }
  return `g:${a.toString(36)}${b.toString(36)}`;
}

/** What a group page needs to reopen as it was left. */
export type RememberedGroup = Readonly<{sort?: RememberedSort; filters?: readonly RememberedFilter[]}>;

/** What a queue selector names to play a group whole on the server: the same shape as a browse. */
export type WholeGroupQuery = Readonly<{libraryId: string; pivot: string; query: BrowseNode; sort: readonly BrowseSortSelection[]}>;

/**
 * One of a show's episode groups that is not a season (its whole list, its specials, its
 * absolute-only episodes, a range of a hundred) played whole by the server, in the show's own
 * order, rather than the episodes the page has loaded.
 */
export function episodeGroupQuery(libraryId: string, showId: string, group: string): WholeGroupQuery {
  return Object.freeze({libraryId, pivot: 'episodes', query: Object.freeze({variant: 'group' as const, all: Object.freeze([episodeGroupCondition(showId, group)])}), sort: Object.freeze([Object.freeze({field: groupOrderSort, direction: 'asc' as const})])});
}

/** Kinds played one at a time: a Home row of these plays them, not the titles that hold them. */
const playedOneAtATime: ReadonlySet<string> = new Set(['song', 'episode', 'audiobook_file', 'chapter']);

/**
 * A Home row played whole: the server reads the row through Home when Play is pressed (at most
 * its first 500 titles) and plays exactly that, in the row's order, across libraries. A row of
 * songs or episodes plays those; a row of titles plays each title's own list.
 */
export function homeRowQuery(rowId: string, entryKinds: readonly string[]): WholeGroupQuery {
  const pivot = entryKinds.some(kind => playedOneAtATime.has(kind)) ? 'items' : 'titles';
  return Object.freeze({libraryId: '*', pivot, query: Object.freeze({variant: 'group' as const, all: Object.freeze([acrossCondition('homeRow', rowId)])}), sort: Object.freeze([Object.freeze({field: groupOrderSort, direction: 'asc' as const})])});
}
