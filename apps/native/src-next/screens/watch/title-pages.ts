import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import type {WorkspaceSeason} from '../../../../../packages/client-core/src/show-workspace';

type Pages = {episodes: Map<string | null, readonly ContentEntry[]>; seasons: Map<string | null, readonly WorkspaceSeason[]>};
export type NativeTitlePage = Readonly<{
  key: string;
  /** Exact server/library/viewer/catalog/viewer-revision evidence from the current workspace. */
  fence: string;
  episodeCursor: string | null;
  seasonCursor: string | null;
  episodes: readonly ContentEntry[];
  seasons: readonly WorkspaceSeason[];
}>;

/** Retains only pages genuinely read by ShowWorkspaceService. No speculative cursors or rows. */
export class NativeTitlePages {
  private fence = '';
  private queries = new Map<string, Pages>();
  private value: Readonly<{episodes: readonly ContentEntry[]; seasons: readonly WorkspaceSeason[]}> = Object.freeze({episodes: [], seasons: []});
  getSnapshot = () => this.value;
  ingest(page: NativeTitlePage) {
    if (this.fence !== page.fence) {this.queries.clear(); this.fence = page.fence;}
    let pages = this.queries.get(page.key);
    if (!pages) {pages = {episodes: new Map(), seasons: new Map()}; this.queries.set(page.key, pages);}
    pages.episodes.set(page.episodeCursor, page.episodes);
    pages.seasons.set(page.seasonCursor, page.seasons);
    const unique = <T extends {id: string}>(values: Iterable<readonly T[]>): readonly T[] => {
      const seen = new Set<string>(), result: T[] = [];
      for (const rows of values) for (const row of rows) if (!seen.has(row.id)) {seen.add(row.id); result.push(row);}
      return Object.freeze(result);
    };
    return this.value = Object.freeze({episodes: unique(pages.episodes.values()), seasons: unique(pages.seasons.values())});
  }
  dispose() {this.queries.clear(); this.value = Object.freeze({episodes: [], seasons: []});}
}
