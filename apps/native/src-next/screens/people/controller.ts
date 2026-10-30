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
  error?: unknown;
}>;

/** One exact Person history object, with one bounded canonical credit page.
 * No library is inferred from a person or from their first known-for title. */
export function createNativePersonController(context: NativeShellBootstrapContext, target: Extract<NativeShellRoute, {kind: 'person'}>) {
  const lifetime = nativeDestinationLifetime(context);
  const retained = () => context.navigation.getSnapshot().route === target || context.navigation.retainedRoutes().includes(target);
  if (!target.personId || !retained()) throw new Error('native_person_route_required');
  let disposed = false, sequence = 0, request: AbortController | undefined;
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
  const read = async (group: NativePersonGroup, cursor?: string, prior?: PersonPage): Promise<void> => {
    if (!visible()) return;
    request?.abort(); const controller = new AbortController(); request = controller;
    const captured = ++sequence;
    publish({phase: 'loading', group});
    const live = () => !controller.signal.aborted && current() && visible() && captured === sequence;
    try {
      const page = await readPerson(context.api, lifetime.scope, target.personId, {...group, limit: 40, cursor, signal: controller.signal});
      if (!live()) return;
      // A continuation cannot silently splice a different authorized snapshot.
      if (prior && (page.viewerFence !== prior.viewerFence || page.revision.catalog !== prior.revision.catalog || page.revision.viewer !== prior.revision.viewer)) {
        throw Object.assign(new Error('native_person_page_changed'), {code: 'stale_data'});
      }
      publish({phase: 'ready', group, page, pageOrigin: cursor === undefined ? 'initial' : 'continuation'});
    } catch (error) {if (live()) publish({phase: 'error', group, error});}
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
    next: (): Promise<void> => snapshot.phase === 'ready' && snapshot.page?.pageInfo.nextCursor ? read(snapshot.group, snapshot.page.pageInfo.nextCursor, snapshot.page) : Promise.resolve(),
    contains: (entry: ContentEntry): boolean => visible() && snapshot.phase === 'ready' && !!snapshot.page &&
      (snapshot.page.person.knownFor.includes(entry) || snapshot.page.credits.some(credit => credit.media === entry)),
    dispose,
  });
}
export type NativePersonController = ReturnType<typeof createNativePersonController>;
