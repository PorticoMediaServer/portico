import {createWindowedCollection} from '../../../../../packages/client-core/src/collections/windowed';
import {readPerson, type PersonCredit, type PersonPage} from '../../../../../packages/client-core/src/people';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import type {NativeShellRoute} from '../../shell/routes';
import {nativeDestinationLifetime} from '../destinations/lifetime';
import type {NativePersonController} from './controller';

const cancelled = () => Object.assign(new Error('native_person_window_retired'), {name: 'AbortError', code: 'cancelled'});
const changed = () => Object.assign(new Error('native_person_page_changed'), {code: 'stale_data'});

/** Sparse credits for one arrived Person/group snapshot. Cursors are only
 * server-returned tokens; seeking follows those tokens, never an offset guess. */
export function createNativePersonCreditWindow(context: NativeShellBootstrapContext,
  target: Extract<NativeShellRoute, {kind: 'person'}>, source: NativePersonController) {
  const lifetime = nativeDestinationLifetime(context), seed = source.getSnapshot();
  if (context.navigation.getSnapshot().route !== target || seed.phase !== 'ready' || !seed.page || seed.page.person.id !== target.personId) {
    throw new Error('native_person_arrived_page_required');
  }
  if (seed.pageOrigin !== 'initial') throw new Error('native_person_initial_page_required');
  const first = seed.page;
  // Selection uses real sparse offsets, never the compact order of resident pages.
  const positions = new WeakMap<PersonCredit, number>();
  first.credits.forEach((credit, index) => positions.set(credit, index));
  let disposed = false;
  const pending = new Set<AbortController>(), cleanups: (() => void)[] = [];
  // Bounded cursor checkpoints contain no page bodies. Page zero is the real
  // arrived seed; old checkpoints can be reached again through canonical reads.
  const cursors = new Map<number, string | null>([[0, null], [1, first.pageInfo.nextCursor || null]]);
  const retained = () => context.navigation.getSnapshot().route === target || context.navigation.retainedRoutes().includes(target);
  const dispose = () => {
    if (disposed) return;
    disposed = true; for (const request of pending) request.abort(); pending.clear();
    cleanups.splice(0).forEach(stop => stop()); cursors.clear(); collection.dispose();
  };
  const current = () => {
    if (!disposed && (!lifetime.current() || !retained() || source.getSnapshot() !== seed)) dispose();
    return !disposed;
  };
  const visible = () => current() && context.navigation.getSnapshot().route === target;
  const remember = (index: number, cursor: string | null) => {
    if (cursors.has(index) && cursors.get(index) !== cursor) throw changed();
    if (cursor && [...cursors].some(([position, value]) => position !== index && value === cursor)) throw changed();
    cursors.set(index, cursor);
    while (cursors.size > 64) {
      const oldest = [...cursors.keys()].find(position => position > 1);
      if (oldest === undefined) break;
      cursors.delete(oldest);
    }
  };
  const request = async (index: number, cursor: string | null, signal: AbortSignal): Promise<PersonPage> => {
    if (!visible() || signal.aborted) throw cancelled();
    const controller = new AbortController(), abort = () => controller.abort();
    signal.addEventListener('abort', abort, {once: true}); pending.add(controller);
    try {
      const page = await readPerson(context.api, lifetime.scope, target.personId, {...seed.group, limit: 40, cursor, signal: controller.signal});
      if (!visible() || controller.signal.aborted) throw cancelled();
      if (page.viewerFence !== first.viewerFence || page.revision.catalog !== first.revision.catalog || page.revision.viewer !== first.revision.viewer
        || page.person.revision !== first.person.revision || page.pageInfo.total !== first.pageInfo.total) throw changed();
      remember(index + 1, page.pageInfo.nextCursor || null);
      page.credits.forEach((credit, offset) => positions.set(credit, index * 40 + offset));
      return page;
    } finally {signal.removeEventListener('abort', abort); pending.delete(controller);}
  };
  const collection = createWindowedCollection<PersonCredit>({pageSize: 40, maxResidentPages: 5, maxConcurrent: 1, prefetchPages: 0,
    keyOf: credit => JSON.stringify([credit.media.libraryId, credit.media.kind, credit.media.id, credit.creditKind, credit.role, credit.character]),
    fetchPage: async (start, count, signal) => {
      if (!visible() || signal.aborted) throw cancelled();
      if (count !== 40 || start % 40 !== 0) throw new Error('native_person_page_alignment_required');
      const index = start / 40;
      if (index === 0) return {items: first.credits, total: first.pageInfo.total};
      let known = index;
      while (!cursors.has(known)) known--;
      let page: PersonPage | undefined;
      for (let position = known; position <= index; position++) {
        const cursor = cursors.get(position);
        if (position > 0 && !cursor) return {items: [], total: first.pageInfo.total};
        page = await request(position, cursor ?? null, signal);
      }
      return {items: page!.credits, total: first.pageInfo.total};
    }});
  const reconcile = () => {
    if (!current()) return;
    if (!visible()) {for (const controller of pending) controller.abort();}
    else collection.retry();
  };
  cleanups.push(context.navigation.subscribe(reconcile), context.subscribeAuthority(reconcile), source.subscribe(reconcile));
  return Object.freeze({collection, positionOf: (credit: PersonCredit): number | undefined => positions.get(credit),
    contains: (entry: ContentEntry): boolean => visible() && collection.residentItems().some(credit => credit.media === entry),
    dispose,
  });
}
export type NativePersonCreditWindow = ReturnType<typeof createNativePersonCreditWindow>;
