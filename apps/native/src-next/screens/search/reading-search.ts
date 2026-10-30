import {readReadingIndexedSearch, validReadingIndexedSearchQuery, type ReadingIndexedSearchHit} from '../../../../../packages/client-core/src/reading-indexed-search';
import {presentError} from '../../../../../packages/client-core/src/presentation/errors';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import type {NativeShellRoute, NativeRouteSnapshot} from '../../shell/routes';
import {openNativeRead} from '../read/eligibility';

type SearchRoute = Extract<NativeShellRoute, {kind: 'search-reading'}>;

/** The opaque, immutable authority one committed visible opening of this page
 * hands to the handlers it renders. Callers never read or reconstruct it; a
 * handler that outlives its opening simply presents a token the controller no
 * longer holds and is refused. */
export type ReadingSearchOpening = Readonly<{readonly admission: true}>;

/** The opaque witness of one published page: the exact query, drawn hits, cursor
 * and index revision a handler was rendered with. It is never a value the caller
 * can rebuild; it must be the very token this controller last published. */
export type ReadingSearchPageWitness = Readonly<{readonly page: true}>;

type WitnessData = Readonly<{query: string; cursor: string | undefined; indexRevision: number | undefined;
  hits: readonly ReadingIndexedSearchHit[]; moreError: unknown | undefined}>;

/** One bounded, explicit book-text search. The words are the person's own literal
 * text: nothing here trims, folds, lowercases, expands or rewrites them before the
 * canonical validator and the official `/v1/reading/search` request see them. */
export type NativeReadingSearchState = Readonly<{
  /** The exact words the drawn page belongs to; '' until the first search starts. */
  query: string;
  phase: 'idle' | 'loading' | 'ready' | 'more' | 'error';
  hits: readonly ReadingIndexedSearchHit[];
  hasNext: boolean;
  /** The whole page was retired; the screen offers a new search. */
  error?: unknown;
  /** A continuation failed for a positively known transient reason; the page and
   * its cursor are still real, and only an explicit Retry asks for that cursor again. */
  moreError?: unknown;
  /** True while one admitted hit is opening the reader for its exact source. */
  busy: boolean;
  /** The page witness this render's handlers must present; a newer publish retires it. */
  witness: ReadingSearchPageWitness;
}>;

export type NativeReadingSearchPort = Readonly<{
  subscribe: (listener: () => void) => () => void;
  getSnapshot: () => NativeReadingSearchState;
  /** Admits one committed visible snapshot of this exact route and returns the
   * opaque opening its handlers carry. It never reads a newer snapshot itself. */
  admit: (snapshot: NativeRouteSnapshot) => ReadingSearchOpening | undefined;
  /** Synchronously retires that opening: its commands and any activation stop being
   * authoritative before the next render, without publishing while hidden. */
  retire: (opening: ReadingSearchOpening) => void;
  /** A new search of literal words. A page witness, when supplied, must still be the
   * published one this control was drawn for (the failed-page recovery path). */
  search: (opening: ReadingSearchOpening | undefined, text: string, witness?: ReadingSearchPageWitness) => void;
  more: (opening: ReadingSearchOpening | undefined, witness: ReadingSearchPageWitness | undefined) => void;
  /** Explicit retry of the retained cursor after a transient continuation failure. */
  retry: (opening: ReadingSearchOpening | undefined, witness: ReadingSearchPageWitness | undefined) => void;
  /** Opens the exact hit object this page drew; a hit from another page is refused. */
  open: (opening: ReadingSearchOpening | undefined, witness: ReadingSearchPageWitness | undefined, hit: ReadingIndexedSearchHit) => void;
  attachReporter: (report: (error: unknown) => void) => void;
  dispose: () => void;
}>;

/** The server's own page size is the only continuation bound we ask for. */
const PAGE = 20;

/** A page request only exists while its own opening, query and ticket are current. */
type Page = Readonly<{query: string; indexRevision: number; hits: readonly ReadingIndexedSearchHit[]}>;

const EMPTY_WITNESS: ReadingSearchPageWitness = Object.freeze({page: true as const});
const EMPTY: NativeReadingSearchState = Object.freeze({query: '', phase: 'idle', hits: Object.freeze([]), hasNext: false, busy: false, witness: EMPTY_WITNESS});

const position = (hit: ReadingIndexedSearchHit): string =>
  JSON.stringify([hit.itemId, hit.sourceId, hit.sourceRevision, hit.sectionPath, hit.sectionIndex, hit.utf16Offset]);

const failure = (code: string): Error => Object.assign(new Error(code), {code});

/** Only positively known transport/busy failures may keep a drawn page and its cursor. */
const transient = (error: unknown): boolean => {
  try {
    const category = presentError(error, 'search').category;
    return category === 'offline' || category === 'unreachable' || category === 'timeout' || category === 'busy';
  } catch {return false;}
};

/** Root54 native surface. It owns one route-local request lifetime, the opening
 * each handler was rendered under, the captured index revision and the cursor of
 * the page it drew. A retained page hidden behind a reader owns no authority: its
 * old opening stays retired and only a fresh visible commit can hand out a new one. */
export function createNativeReadingSearch(context: NativeShellBootstrapContext, route: SearchRoute): NativeReadingSearchPort {
  const captured = context.session();
  if (!captured) throw new Error('native_reading_search_authority_required');
  const family = captured.sessionFamilyId;
  const listeners = new Set<() => void>();
  let alive = true, state = EMPTY, reporter: ((error: unknown) => void) | undefined;
  let current: ReadingSearchOpening | undefined;
  const snapshots = new Map<ReadingSearchOpening, NativeRouteSnapshot>();
  let page: Page | undefined, cursor: string | undefined;
  let currentWitness: ReadingSearchPageWitness = EMPTY_WITNESS;
  const witnesses = new Map<ReadingSearchPageWitness, WitnessData>();
  let sequence = 0, inflight: AbortController | undefined, activationTicket = 0;
  let activation: Readonly<{controller: AbortController; ticket: number; opening: ReadingSearchOpening}> | undefined;
  const liveSession = (): boolean => {
    if (!alive || !context.current()) return false;
    const session = context.session();
    return !!session && session.sessionFamilyId === family;
  };
  /** The only authority a command or activation has: an opening that is still the
   * current one, still bound to the exact snapshot it was admitted under. */
  const authority = (opening: ReadingSearchOpening | undefined): NativeRouteSnapshot | undefined => {
    if (!opening || opening !== current || !liveSession()) return undefined;
    const snapshot = snapshots.get(opening);
    return snapshot && context.navigation.getSnapshot() === snapshot ? snapshot : undefined;
  };
  /** Every publish hands out a new immutable page witness bound to the exact query,
   * hits, cursor and index revision of that page. Older witnesses are dropped, so a
   * handler that rendered under them can never act on a newer page. */
  const publish = (next: Omit<NativeReadingSearchState, 'witness'>): void => {
    if (!alive) return;
    const witness: ReadingSearchPageWitness = Object.freeze({page: true as const});
    witnesses.clear();
    witnesses.set(witness, Object.freeze({query: next.query, cursor, indexRevision: page?.indexRevision, hits: next.hits, moreError: next.moreError}));
    currentWitness = witness;
    state = Object.freeze({...next, witness});
    for (const listener of [...listeners]) listener();
  };
  const witnessData = (witness: ReadingSearchPageWitness | undefined): WitnessData | undefined =>
    witness && witness === currentWitness ? witnesses.get(witness) : undefined;
  const cancelRequest = (): void => {inflight?.abort(); inflight = undefined; sequence++;};
  const cancelActivation = (): void => {activation?.controller.abort(); activation = undefined; activationTicket++;};
  const release = (opening: ReadingSearchOpening): void => {
    snapshots.delete(opening);
    if (current === opening) current = undefined;
  };
  const retirePage = (opening: ReadingSearchOpening, error: unknown): void => {
    if (!authority(opening)) return;
    page = undefined; cursor = undefined;
    cancelActivation();
    publish({query: state.query, phase: 'error', hits: Object.freeze([]), hasNext: false, error, busy: false});
  };
  const commit = (opening: ReadingSearchOpening, query: string, indexRevision: number, hits: readonly ReadingIndexedSearchHit[], next: string | undefined): void => {
    page = Object.freeze({query, indexRevision, hits});
    cursor = next;
    publish({query, phase: 'ready', hits, hasNext: next !== undefined, busy: state.busy});
  };
  const run = (opening: ReadingSearchOpening, query: string, follow: string | undefined): void => {
    const admitted = authority(opening);
    if (!admitted) return;
    if (follow !== undefined && (!page || page.query !== query || cursor !== follow)) return;
    cancelRequest();
    const ticket = sequence, request = new AbortController();
    const isCurrent = () => ticket === sequence && authority(opening) === admitted && !request.signal.aborted;
    inflight = request;
    if (follow === undefined) {
      // Replacing the words retires the page and any activation opening from it.
      page = undefined; cursor = undefined;
      cancelActivation();
      publish({query, phase: 'loading', hits: Object.freeze([]), hasNext: false, busy: false});
    } else publish({...state, phase: 'more', moreError: undefined, error: undefined});
    void readReadingIndexedSearch(context.api, {query, limit: PAGE, ...(follow === undefined ? {} : {cursor: follow})}, {signal: request.signal, isCurrent})
      .then(result => {
        if (!isCurrent()) return;
        if (follow === undefined) {commit(opening, query, result.indexRevision, result.hits, result.next); return;}
        // The continuation must extend exactly the page on screen: same index revision,
        // a cursor that actually moved, and at least one position we do not already
        // hold. Anything else retires the page instead of appending duplicates or
        // pretending the list ended.
        const drawn = page;
        if (!drawn || result.indexRevision !== drawn.indexRevision) return retirePage(opening, failure('reading_indexed_search_changed'));
        if (result.next !== undefined && result.next === follow) return retirePage(opening, failure('reading_indexed_search_cursor'));
        const seen = new Set(drawn.hits.map(position));
        const fresh = result.hits.filter(hit => !seen.has(position(hit)));
        if (!fresh.length) return retirePage(opening, failure('reading_indexed_search_cursor'));
        commit(opening, query, result.indexRevision, Object.freeze([...drawn.hits, ...fresh]), result.next);
      })
      .catch(error => {
        if (!isCurrent()) return;
        // A transient continuation failure keeps the real page and its cursor; retry is
        // explicit. Every other failure, including a refused cursor or a malformed page,
        // retires the results so the person starts from a real new search.
        if (follow !== undefined && transient(error)) {publish({...state, phase: 'ready', moreError: error}); return;}
        retirePage(opening, error);
      })
      .finally(() => {if (inflight === request) inflight = undefined;});
  };
  return Object.freeze({
    subscribe: (listener: () => void) => {
      if (!alive) return () => {};
      listeners.add(listener);
      return () => {listeners.delete(listener);};
    },
    getSnapshot: () => state,
    admit: (snapshot: NativeRouteSnapshot) => {
      // Only the caller's captured committed snapshot may admit, and only if it is
      // still the shell's exact current commit for this exact route.
      if (!liveSession() || snapshot.route !== route || context.navigation.getSnapshot() !== snapshot) return undefined;
      if (current) return snapshots.get(current) === snapshot ? current : undefined;
      const opening: ReadingSearchOpening = Object.freeze({admission: true as const});
      snapshots.set(opening, snapshot);
      current = opening;
      // A request or activation that could not publish while hidden is settled here,
      // on the visible commit, rather than left as a stuck loading or busy surface.
      const settled = state.phase === 'loading' ? {...state, phase: 'idle' as const, hits: Object.freeze([]), hasNext: false, busy: false}
        : state.phase === 'more' ? {...state, phase: 'ready' as const, busy: false}
          : state.busy ? {...state, busy: false} : undefined;
      if (settled) publish(settled);
      // The carried words on the first opening, then the person's own last asked
      // words if a hidden request could not finish.
      if (state.phase === 'idle') run(opening, state.query || route.query, undefined);
      return opening;
    },
    retire: (opening: ReadingSearchOpening) => {
      // Synchronous, silent retirement: a hidden or replayed page publishes nothing and
      // every handler it already rendered is refused from here on.
      if (current !== opening) return;
      cancelRequest(); cancelActivation();
      release(opening);
    },
    search: (opening, text: string, witness?: ReadingSearchPageWitness) => {
      if (!opening || !authority(opening)) return;
      if (witness !== undefined && !witnessData(witness)) return;
      if (!validReadingIndexedSearchQuery(text)) return;
      // Replacing the words cancels an activation that was opening under this page.
      cancelActivation();
      run(opening, text, undefined);
    },
    more: (opening, witness) => {
      // The original published page only: its own query and next cursor, never the
      // newest state's.
      const drawn = witnessData(witness);
      if (!opening || !authority(opening) || !drawn || state.phase !== 'ready' || drawn.moreError || drawn.cursor === undefined) return;
      run(opening, drawn.query, drawn.cursor);
    },
    retry: (opening, witness) => {
      const failed = witnessData(witness);
      if (!opening || !authority(opening) || !failed || state.phase !== 'ready' || !failed.moreError || failed.cursor === undefined) return;
      run(opening, failed.query, failed.cursor);
    },
    open: (opening, witness, hit: ReadingIndexedSearchHit) => {
      const admitted = opening ? authority(opening) : undefined, drawn = witnessData(witness);
      // The hit must be the exact object this page witness drew, for that page's query
      // and index revision; a rendered row from an older page can never open anything.
      if (!admitted || !opening || !drawn || state.phase !== 'ready' || state.busy || !drawn.hits.includes(hit)) return;
      const ticket = ++activationTicket, controller = new AbortController();
      activation = Object.freeze({controller, ticket, opening});
      const owns = () => activation?.controller === controller && activationTicket === ticket;
      publish({...state, busy: true});
      void (async () => {
        try {
          // Exactly what the hit named, and the origin is the opening's admitted commit,
          // never a revision borrowed at press time. No page, chapter, CFI or progress is claimed.
          const opened = await openNativeRead(context, {itemId: hit.itemId, sourceId: hit.sourceId, expectedSourceRevision: hit.sourceRevision},
            controller.signal, {route: admitted.route, revision: admitted.revision});
          if (!owns() || controller.signal.aborted || !authority(opening)) return;
          // Refused, never substituted: the page retires so the person searches again.
          if (!opened) retirePage(opening, failure('reading_indexed_search_source_changed'));
        } catch (error) {
          if (!owns() || controller.signal.aborted || (error instanceof Error && error.name === 'AbortError')) return;
          // No stale toast: only this opening, still admitted, may report.
          if (authority(opening)) reporter?.(error);
        } finally {
          // Check this activation's own ticket and original admission before releasing,
          // then clear the busy flag only under that same original proof.
          const mine = owns() && authority(opening) !== undefined;
          if (owns()) activation = undefined;
          if (mine) publish({...state, busy: false});
        }
      })();
    },
    attachReporter: (report: (error: unknown) => void) => {reporter = report;},
    dispose: () => {
      if (!alive) return;
      alive = false; cancelRequest(); cancelActivation(); listeners.clear();
      if (current) release(current);
      page = undefined; cursor = undefined;
    },
  });
}
