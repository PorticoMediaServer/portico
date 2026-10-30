import type {LibraryContentApi} from '@core/library-content.ts';
import {liveSourceChoicesService, type LiveSourceChoicePage} from '@core/administration.ts';
import {OwnerStore, errorFacts} from '../../live/owner/lifetime';
type ChoiceSnapshot = Readonly<{loading: boolean; query: string; page?: LiveSourceChoicePage; error?: unknown}>;
/** Each requested page is bounded. The source directory/configuration remain
 * with their existing owner; this store holds only the chooser's visible data. */
export class SourceChoicesController extends OwnerStore<ChoiceSnapshot> {
  constructor(private api: LibraryContentApi, private sourceId: string, readonly kind: 'groups' | 'channels', current: () => boolean, reportError: (error: unknown) => void) {super(Object.freeze({loading: false, query: ''}), current, reportError);}
  async load(query = '', more = false) {
    if (!this.isActive() || more && this.snapshot.loading) return;
    const head = this.snapshot.page, cursor = more ? head?.nextCursor : '';
    if (more && (!head || !cursor || query !== this.snapshot.query)) return;
    const attempt = this.begin();this.publish({loading: true, query, ...(more ? {page: head} : {})});if (!this.valid(attempt)) return;
    try {
      const request = async <T,>(path: string, method?: string, body?: unknown): Promise<T> => {
        if (!this.valid(attempt)) throw {code: 'cancelled'};
        const result = await this.api.request<T>(path, method, body, attempt.abort.signal);
        if (!this.valid(attempt)) throw {code: 'cancelled'};return result;
      };
      const page = await liveSourceChoicesService(request, this.sourceId).list(this.kind, cursor || '', 50, query.trim());
      if (!this.valid(attempt)) return;
      if (page.sourceId !== this.sourceId || page.kind !== this.kind || more && page.generation !== head?.generation) throw {code: 'revision_mismatch'};
      const items = more && head ? [...head.items, ...page.items.filter(row => !head.items.some(prior => prior.id === row.id))] : page.items;
      this.publish({loading: false, query, page: Object.freeze({...page, items: Object.freeze(items)})});
    } catch (error) {if (this.valid(attempt)) this.fail(error, {...this.snapshot, loading: false, error: errorFacts(error)});}
  }
}
