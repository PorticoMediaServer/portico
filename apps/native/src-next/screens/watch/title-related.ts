import type {ContentEntry, LibraryContentApi} from '../../../../../packages/client-core/src/library-content';
import type {ListeningViewerScope} from '../../../../../packages/client-core/src/listening';
import {listeningSimilarPager, type ListeningSimilarRead, type RelationRowOwner, type RelationRowPager} from '../../../../../packages/client-core/src/relation-rows';
import {relatedRowPager, type RelatedMovieRow} from '../../../../../packages/client-core/src/related-movies';
import type {HomeApi, HomeRevision, HomeRow} from '../../../../../packages/client-core/src/home';
import {fetchItemRecommendations, fetchShowRecommendations, titleRecommendationRows} from '../../../../../packages/client-core/src/recommendations';
import {recommendationRowPager} from '../../../../../packages/client-core/src/relation-rows';
import {serverTextLabel} from '../../../../../packages/client-core/src/presentation/server-text';
import {defaultI18n} from '../../../../../packages/i18n/src';

export type NativeRelatedRowSnapshot = Readonly<{
  entries: readonly ContentEntry[];
  hasMore: boolean;
  loading: boolean;
  failed: boolean;
  /** Stable per continuation: the row's waiting host survives an empty page. */
  waitingId: string;
  fulfilled?: Readonly<{waitingId: string; firstItemId?: string}>;
}>;

/** One related row of one detail revision, read to its end (MOB-054, TV-019): the shared
 * pager's pages in the shelf's paging contract. */
export class NativeRelatedRow {
  private state: NativeRelatedRowSnapshot;
  private readonly off: () => void;
  private readonly key: string;
  private readonly pager: RelationRowPager;
  constructor(key: string, pager: RelationRowPager, changed: () => void) {
    this.key = key; this.pager = pager;
    const first = pager.getSnapshot();
    this.state = Object.freeze({entries: first.entries, hasMore: !!first.cursor, loading: false, failed: false, waitingId: this.waitingId(first.cursor)});
    this.off = pager.subscribe(() => {
      const next = pager.getSnapshot(), previous = this.state;
      const added = next.entries.length > previous.entries.length ? next.entries[previous.entries.length] : undefined;
      const answered = previous.loading && !next.loading && !next.error;
      this.state = Object.freeze({entries: next.entries, hasMore: !!next.cursor, loading: next.loading, failed: !!next.error,
        waitingId: next.cursor ? this.waitingId(next.cursor) : previous.waitingId,
        ...(answered ? {fulfilled: {waitingId: previous.waitingId, ...(added ? {firstItemId: added.id} : {})}} : previous.fulfilled ? {fulfilled: previous.fulfilled} : {})});
      changed();
    });
  }
  getSnapshot = (): NativeRelatedRowSnapshot => this.state;
  private waitingId(cursor: string | undefined) {return JSON.stringify(['title-related', this.key, cursor ?? null]);}
  /** A page boundary reads the next page for the exact snapshot it rendered. */
  next = (expected: NativeRelatedRowSnapshot): void => {
    if (expected === this.state && this.state.hasMore && !this.state.loading) void this.pager.more();
  };
  dispose(): void {this.off(); this.pager.dispose();}
}

/** A title's related row (detail.related) under its detail's fence and revision. */
export function nativeRelatedRow(api: HomeApi, source: Parameters<typeof relatedRowPager>[1], row: RelatedMovieRow, scope: Parameters<typeof relatedRowPager>[3], changed: () => void): NativeRelatedRow {
  return new NativeRelatedRow(JSON.stringify([source.itemId, scope.revision, row.id]), relatedRowPager(api, source, row, scope), changed);
}

/** A listening title's similar row on its first page's basis and revision. */
export function nativeSimilarRow(api: LibraryContentApi, scope: ListeningViewerScope, first: ListeningSimilarRead, changed: () => void): NativeRelatedRow {
  return new NativeRelatedRow(JSON.stringify(['music-similar', first.itemId, first.revision]), listeningSimilarPager(api, scope, first, {limit: 24}), changed);
}

/** A row a title page draws under More like this: a detail's related row, or a show's recommendation row. */
export type NativeTitleRelatedRow = Readonly<{id: string; heading: string; entries: readonly ContentEntry[]}>;

/** A title page's More like this, as web reads it: a show's (and its seasons') from
 * `GET /v1/shows/{id}/recommendations` (N4-8), a film's from `GET /v1/items/{id}/recommendations`. Every row in
 * the server's order, named by its own words. An answer from another revision than the page's is not this page's:
 * undefined, so the caller decides (a show page shows none; a film falls back to its embedded rows, as web). */
export async function readNativeRecommendationRows(api: HomeApi, owner: RelationRowOwner, revision: HomeRevision, signal: AbortSignal): Promise<readonly HomeRow[] | undefined> {
  const value = owner.kind === 'show' ? await fetchShowRecommendations(api, owner.id, 12, signal) : await fetchItemRecommendations(api, owner.id, 12, signal);
  if (value.revision.catalog !== revision.catalog || value.revision.viewer !== revision.viewer) return undefined;
  return titleRecommendationRows(value);
}

export const nativeTitleRelatedRow = (row: HomeRow): NativeTitleRelatedRow =>
  Object.freeze({id: row.id, heading: serverTextLabel(defaultI18n, row.titleText, row.title), entries: row.entries});

/** One recommendation row, continued to its end through its owner's row route. */
export function nativeRecommendationRow(api: HomeApi, owner: RelationRowOwner, revision: HomeRevision, row: HomeRow, changed: () => void): NativeRelatedRow {
  return new NativeRelatedRow(JSON.stringify([`${owner.kind}-recommendations`, owner.id, revision, row.id]), recommendationRowPager(api, owner, row), changed);
}
