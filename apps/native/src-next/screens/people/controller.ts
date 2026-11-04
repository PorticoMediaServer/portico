import {readPerson, type PersonPage, type PersonRole} from '../../../../../packages/client-core/src/people';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import type {NativeShellRoute} from '../../shell/routes';
import type {NativeDestinationActivationPort} from '../destinations/focus';
import {nativeDestinationLifetime} from '../destinations/lifetime';

export type NativePersonGroup = Readonly<{role: PersonRole} | {kind: string} | {department: string}>;
export type NativePersonSnapshot = Readonly<{
  phase: 'idle' | 'loading' | 'ready' | 'error';
  group: NativePersonGroup;
  page?: PersonPage;
  /** Provenance of the actual canonical request, never inferred from row facts. */
  pageOrigin?: 'initial' | 'continuation';
  /** TV, all credits, after a re-read only: the first page of each department (by group id), read with it so each
   * department row holds its first page from the replacement's first render (credit-window, credit-refocus). A department
   * whose page could not be read, or answered for another revision, is absent and its row reads it itself. */
  departmentPages?: ReadonlyMap<string, PersonPage>;
  error?: unknown;
}>;

/** The server's own split of credits into Cast and Crew (catalog `personDepartmentFilter`): Cast is the acting (or
 * cast) department, Crew every other one. */
const castDepartment = (department: string) => ['acting', 'cast'].includes(department.toLowerCase());

/** The departments the TV page draws a row each for (tv-detail frame 10, `f9`): the server groups all credits by
 * department only when a person has more than one. Filters apply to every row (the frame's caption): Cast keeps the
 * acting row, Crew the others. The groups are the person's whatever the filter, so the split is the client's. */
export function personDepartments(page: PersonPage | undefined, role: PersonRole = 'all'): readonly Readonly<{id: string; department: string; total: number}>[] {
  return (page?.groups ?? []).flatMap(group => group.department && group.total > 0 && (role === 'all' || castDepartment(group.department) === (role === 'cast'))
    ? [{id: group.id, department: group.department, total: group.total}] : []);
}

/** One exact Person history object, with one bounded canonical credit page.
 * No library is inferred from a person or from their first known-for title. */
export function createNativePersonController(context: NativeShellBootstrapContext, target: Extract<NativeShellRoute, {kind: 'person'}>,
  options: Readonly<{departmentPages?: boolean}> = {}) {
  const lifetime = nativeDestinationLifetime(context);
  const retained = () => context.navigation.getSnapshot().route === target || context.navigation.retainedRoutes().includes(target);
  if (!target.personId || !retained()) throw new Error('native_person_route_required');
  let disposed = false, sequence = 0, request: AbortController | undefined;
  // One quiet re-read after the server refuses a credit cursor (access was decided again). A first-page read, or a credit
  // continuation that answers (paged), resets it, so a later access change in the same visit is read again too.
  let rereadDone = false;
  let snapshot: NativePersonSnapshot = Object.freeze({phase: 'idle', group: Object.freeze({role: 'all'})});
  const listeners = new Set<() => void>(), cleanups: (() => void)[] = [];
  let previous: ReturnType<typeof context.navigation.getSnapshot> | undefined, previousLive: boolean | undefined;
  let activeSnapshot: ReturnType<NativeDestinationActivationPort['getSnapshot']>;
  const emit = () => {for (const listener of [...listeners]) listener();};
  const dispose = () => {
    if (disposed) return;
    disposed = true; sequence++; request?.abort(); request = undefined;
    cleanups.splice(0).forEach(stop => stop());
    snapshot = Object.freeze({phase: 'idle', group: snapshot.group});
    emit(); listeners.clear();
  };
  const current = () => {
    if (!disposed && (!lifetime.current() || !retained())) dispose();
    return !disposed;
  };
  const visible = () => current() && context.navigation.getSnapshot().route === target;
  const publish = (value: NativePersonSnapshot) => {snapshot = Object.freeze(value); emit();};
  /** The first page of the shown group read again in place of the shown one (the page stays until the answer). A new
   * initial page replaces the credit window built on the old one. False when already done or it could not be read. */
  const reread = async (): Promise<boolean> => {
    if (rereadDone || !visible() || snapshot.phase !== 'ready') return false;
    rereadDone = true;
    request?.abort(); const controller = new AbortController(); request = controller;
    const captured = ++sequence, group = snapshot.group;
    const live = () => !controller.signal.aborted && current() && visible() && captured === sequence;
    try {
      const page = await readPerson(context.api, lifetime.scope, target.personId, {...group, limit: 40, signal: controller.signal});
      if (!live()) return false;
      // TV's department rows replace theirs too: each is read with the page, so the replacement arrives with them.
      const departments = options.departmentPages && 'role' in group ? personDepartments(page, group.role) : [];
      const pages = await Promise.all(departments.map(department => readPerson(context.api, lifetime.scope, target.personId, {department: department.department, limit: 40, signal: controller.signal})
        .then(value => value.revision.catalog === page.revision.catalog && value.revision.viewer === page.revision.viewer && value.person.revision === page.person.revision
          ? [[department.id, value] as const] : [], () => [])));
      if (!live()) return false;
      publish({phase: 'ready', group, page, pageOrigin: 'initial', ...(departments.length ? {departmentPages: new Map(pages.flat())} : {})});
      return true;
    } catch {return false;}
    finally {if (request === controller) request = undefined;}
  };
  const read = async (group: NativePersonGroup, cursor?: string, prior?: PersonPage, rereading = false): Promise<void> => {
    if (!visible()) return;
    if (cursor === undefined && !rereading) rereadDone = false;
    request?.abort(); const controller = new AbortController(); request = controller;
    const captured = ++sequence;
    publish({phase: 'loading', group});
    const live = () => !controller.signal.aborted && current() && visible() && captured === sequence;
    try {
      const page = await readPerson(context.api, lifetime.scope, target.personId, {...group, limit: 40, cursor, signal: controller.signal});
      if (!live()) return;
      // A continuation cannot silently splice a different snapshot of the person (its revision); a cursor from before the
      // fence moved is the server's to refuse (invalid_cursor, below).
      if (prior && (page.revision.catalog !== prior.revision.catalog || page.revision.viewer !== prior.revision.viewer)) {
        throw Object.assign(new Error('native_person_page_changed'), {code: 'stale_data'});
      }
      if (cursor !== undefined) rereadDone = false;
      publish({phase: 'ready', group, page, pageOrigin: cursor === undefined ? 'initial' : 'continuation'});
    } catch (error) {
      if (!live()) return;
      // A refused continuation: the first page once more (the same one-shot guard as reread).
      if (cursor !== undefined && !rereadDone && (error as {code?: unknown})?.code === 'invalid_cursor') {rereadDone = true; void read(group, undefined, undefined, true); return;}
      publish({phase: 'error', group, error});
    }
    finally {if (request === controller) request = undefined;}
  };
  const changed = () => {
    if (!current()) return;
    if (!visible() && request) {
      sequence++; request.abort(); request = undefined;
      snapshot = Object.freeze({phase: 'idle', group: snapshot.group});
    }
    emit();
  };
  const activation: NativeDestinationActivationPort = Object.freeze({
    subscribe: listener => {if (disposed) return () => {}; listeners.add(listener); return () => {listeners.delete(listener);};},
    getSnapshot: () => {
      const state = context.navigation.getSnapshot(), live = current();
      if (state !== previous || live !== previousLive) {
        previous = state; previousLive = live;
        activeSnapshot = Object.freeze({visible: live && state.route === target, intent: context.navigation.getIntent(), revision: state.revision});
      }
      return activeSnapshot;
    },
  });
  cleanups.push(context.navigation.subscribe(changed), context.subscribeAuthority(changed));
  return Object.freeze({
    getSnapshot: (): NativePersonSnapshot => {current(); return snapshot;},
    subscribe: activation.subscribe, activation,
    load: (): Promise<void> => snapshot.phase === 'idle' || snapshot.phase === 'error' ? read(snapshot.group) : Promise.resolve(),
    select: (group: NativePersonGroup): Promise<void> => read(Object.freeze({...group})),
    /** After the server refuses a credit cursor: the first page once more (see reread). */
    reread,
    /** A credit continuation answered: a later refusal is a new access change (see reread). */
    paged: (): void => {rereadDone = false;},
    next: (): Promise<void> => snapshot.phase === 'ready' && snapshot.page?.pageInfo.nextCursor ? read(snapshot.group, snapshot.page.pageInfo.nextCursor, snapshot.page) : Promise.resolve(),
    contains: (entry: ContentEntry): boolean => visible() && snapshot.phase === 'ready' && !!snapshot.page &&
      (snapshot.page.person.knownFor.includes(entry) || snapshot.page.credits.some(credit => credit.media === entry)),
    dispose,
  });
}
export type NativePersonController = ReturnType<typeof createNativePersonController>;
