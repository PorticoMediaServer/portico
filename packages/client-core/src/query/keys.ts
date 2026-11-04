/**
 * Every cached document's key, from one place (CLIENT-DATA-REVIEW §5). A key names the document
 * and every input that changes its answer, and nothing else: two owners asking for the same thing
 * build the same key and share one read. The viewer is not in the key; each viewer has a cache.
 */
export const queryKeys = Object.freeze({
  /** A title's page: `GET /v1/items/{id}/detail?related=…`. */
  itemDetail: (itemId: string, related: 'all' | 'none') => ['item', itemId, 'detail', related] as const,
  /** A search's first page: `GET /v1/search` for exactly these inputs (recording is not an input; it never changes the answer). */
  searchResults: (q: string, group: string, groups: readonly string[], sort: string, direction: string, libraryIds: readonly string[], limit: number) => ['search', 'results', q, group, groups, sort, direction, libraryIds, limit] as const,
  /** The viewer's recent searches: `GET /v1/search/history`. */
  searchHistory: () => ['search', 'history'] as const,
  /** One page of a show's workspace (`GET /v1/libraries/{id}/show-workspace?…`): its route and both cursors are the query string. */
  showWorkspace: (libraryId: string, query: string) => ['library', libraryId, 'show-workspace', query] as const,
  /** One page of a library's content route (`GET /v1/libraries/{id}/content?…`): an album, artist, book or See all page. */
  content: (libraryId: string, query: string) => ['library', libraryId, 'content', query] as const,
  /** A library tab's grid: its windowed pages and their session (the value is the live grid, not a document). `query` is the address's filters and order. */
  libraryBrowse: (libraryId: string, pivot: string, query: string) => ['library', libraryId, 'browse', pivot, query] as const,
  /** An owner's view of one library: `GET /v1/admin/libraries/{id}` (its source, last scan and offered actions). */
  adminLibraryManagement: (libraryId: string) => ['admin', 'library', libraryId, 'management'] as const,
  /** A library's inventory, first page only (`GET …/inventory-config` with `GET …/inventory`, no cursor); later pages are direct reads. */
  adminLibraryInventory: (libraryId: string, sourceId: string, state: string) => ['admin', 'library', libraryId, 'inventory', {sourceId, state}, 'cursor0'] as const,
  /** Every owner document of the admin tree: what is dropped when the server refuses the viewer. */
  adminLibraries: () => ['admin', 'library'] as const,
  /** A Home row's See all grid, as `libraryBrowse`. */
  homeRowBrowse: (rowId: string, query: string) => ['home', 'row', rowId, 'browse', query] as const,
});
