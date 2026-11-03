/**
 * Every cached document's key, from one place (CLIENT-DATA-REVIEW §5). A key names the document
 * and every input that changes its answer, and nothing else: two owners asking for the same thing
 * build the same key and share one read. The viewer is not in the key; each viewer has a cache.
 */
export const queryKeys = Object.freeze({
  /** A title's page: `GET /v1/items/{id}/detail?related=…`. */
  itemDetail: (itemId: string, related: 'all' | 'none') => ['item', itemId, 'detail', related] as const,
  /** One page of a show's workspace (`GET /v1/libraries/{id}/show-workspace?…`): its route and both cursors are the query string. */
  showWorkspace: (libraryId: string, query: string) => ['library', libraryId, 'show-workspace', query] as const,
  /** One page of a library's content route (`GET /v1/libraries/{id}/content?…`): an album, artist, book or See all page. */
  content: (libraryId: string, query: string) => ['library', libraryId, 'content', query] as const,
});
