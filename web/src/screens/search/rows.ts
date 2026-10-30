import type {SearchEntry, SearchGroup, SearchGroupResult, SearchService, SearchSnapshot} from '@core/search.ts';

type Source = Pick<SearchService, 'getSnapshot' | 'subscribe' | 'forkContinuation' | 'select' | 'retry'>;
type Page = {group: SearchGroup; service: SearchService; off: () => void; result: SearchGroupResult; loading: boolean; error: SearchSnapshot['error']; data: SearchSnapshot['data']};
const entryKey = (entry: SearchEntry) => JSON.stringify(['libraryId' in entry ? entry.libraryId : '', entry.kind, entry.id]);
const changed = Object.freeze({code: 'stale_continuation', message: '', retryable: true});

/** Independent server continuations under one exact displayed All query. The combined
 * snapshot is also the command/selection source; a page never becomes unadmitted UI. */
export function createSearchRows(source: Source, isCurrent: () => boolean) {
  let base = source.getSnapshot(), snapshot = base, disposed = false, invalidated = false;
  let initialGroups = base.groups;
  const pages = new Map<SearchGroup, Page>(), listeners = new Set<() => void>();
  const publish = () => {for (const listener of [...listeners]) listener();};
  const retire = () => {for (const page of pages.values()) {page.off(); page.service.dispose();} pages.clear();};
  const active = () => !disposed && isCurrent() && source.getSnapshot() === base && base.phase === 'ready' && !invalidated;
  const owns = (page: Page) => active() && pages.get(page.group) === page;
  const invalidate = (error: SearchSnapshot['error']) => {
    invalidated = true; retire();
    snapshot = Object.freeze({...base, phase: error?.code === 'unauthorized' || error?.code === 'forbidden' ? 'error' : 'refresh-required', data: null, groups: Object.freeze([]), error: error ?? changed});
    publish();
  };
  const combine = () => {
    const groups = Object.freeze(initialGroups.map(group => pages.get(group.id)?.result ?? group));
    snapshot = Object.freeze({...base, data: base.data ? Object.freeze({...base.data, groups}) : null, groups});
    publish();
  };
  const off = source.subscribe(() => {
    retire(); base = source.getSnapshot(); invalidated = false;
    // A pause/replay can retain the same server group objects. Give callbacks a new
    // row identity for this parent generation rather than reviving an old opening.
    const groups = initialGroups = Object.freeze(base.groups.map(group => Object.freeze({...group})));
    snapshot = Object.freeze({...base, data: base.data ? Object.freeze({...base.data, groups}) : null, groups});
    publish();
  });
  const update = (page: Page) => {
    if (!owns(page)) return;
    const next = page.service.getSnapshot(), data = next.data;
    // A real continuation starts with its offered cursor. Core's refused-cursor
    // recovery starts a group-only first page instead; withdraw the old All answer
    // at that transition, before the replacement can hang or fail transiently.
    if (next.phase === 'loading' && !next.pagination.cursor) {invalidate(changed); return;}
    page.loading = next.phase === 'loading';
    page.error = next.error;
    if (next.phase === 'error' || next.phase === 'refresh-required') {
      if (next.phase === 'refresh-required' || !next.error?.retryable || ['unauthorized', 'forbidden'].includes(next.error.code)) {invalidate(next.error); return;}
    } else if (next.phase === 'ready' && data && data !== page.data) {
      // Core may recover a refused cursor with a fresh group-only first page. That is
      // not an append to this All answer, even when its revision happens to match.
      if (!next.pagination.cursor || !base.data || data.scope.serverId !== base.data.scope.serverId
        || data.scope.viewerFence !== base.data.scope.viewerFence || data.query.q !== base.data.query.q
        || data.query.group !== page.group || data.query.sort !== base.data.query.sort || data.query.direction !== base.data.query.direction
        || JSON.stringify(data.query.libraryIds) !== JSON.stringify(base.data.query.libraryIds)
        || data.revision.catalog !== base.data.revision.catalog || data.revision.viewer !== base.data.revision.viewer) {invalidate(changed); return;}
      const result = next.groups.find(group => group.id === page.group);
      if (!result || result.status !== 'success') {
        page.error = {code: result?.errorCode || 'request_failed', message: '', retryable: true};
      } else {
        const seen = new Set(page.result.items.map(entryKey)), entries = [...page.result.items];
        for (const entry of result.items) if (!seen.has(entryKey(entry))) {seen.add(entryKey(entry)); entries.push(entry);}
        page.result = Object.freeze({...result, items: Object.freeze(entries)});
        page.data = data;
      }
    }
    combine();
  };
  return {
    getSnapshot: () => snapshot,
    subscribe(listener: () => void) {listeners.add(listener); return () => {listeners.delete(listener);};},
    page(group: SearchGroup) {const page = pages.get(group); return {loading: page?.loading ?? false, error: page?.error ?? null};},
    async next(result: SearchGroupResult) {
      if (!active() || base.query?.group || !snapshot.groups.includes(result) || !result.nextCursor) return;
      let page = pages.get(result.id);
      if (!page) {
        const service = source.forkContinuation(result.id); if (!service) return;
        page = {group: result.id, service, result, loading: false, error: null, data: service.getSnapshot().data, off: () => {}};
        const held = page; pages.set(result.id, held); held.off = service.subscribe(() => update(held));
      }
      if (page.loading || page.error || !owns(page)) return;
      await page.service.next(result.id);
    },
    async retry(result?: SearchGroupResult) {
      if (disposed || !isCurrent() || source.getSnapshot() !== base) return;
      if (invalidated) {
        const query = base.query; if (query) await source.select(query.q, {...query, record: false});
        return;
      }
      if (!result) {await source.retry(); return;}
      const page = pages.get(result.id);
      if (page && owns(page) && snapshot.groups.includes(result) && page.error && !page.loading) await page.service.retry();
    },
    dispose() {if (disposed) return; disposed = true; off(); retire(); snapshot = Object.freeze({...base, phase: 'idle', query: null, data: null, groups: Object.freeze([]), error: null}); listeners.clear();},
  };
}
export type SearchRows = ReturnType<typeof createSearchRows>;
