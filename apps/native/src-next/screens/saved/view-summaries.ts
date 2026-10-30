import type {PersonalResource} from '../../../../../packages/client-core/src/personal-saved';
import type {SavedViewSummaryText} from '../../../../../packages/client-core/src/presentation/saved-view-summary';
import {nativeDestinationLifetime} from '../destinations/lifetime';
import type {NativeSavedDestination} from './model';
import type {NativeSavedViewDestination} from './view-model';

/** The Saved-owned capability reader. The binding borrows this exact instance:
 * it never constructs one, keeps a second cache, or disposes it. */
export type NativeSavedViewSummaryReader = Readonly<{
  load: (resources: readonly PersonalResource[]) => Promise<void>;
  line: (resource: PersonalResource, text: SavedViewSummaryText) => string | undefined;
  getSnapshot: () => number;
  subscribe: (listener: () => void) => () => void;
}>;

/** A real Saved hub or Saved subview, borrowed through its published interface. */
export type NativeSavedViewSummarySource =
  | Pick<NativeSavedDestination, 'getSnapshot' | 'subscribe'>
  | Pick<NativeSavedViewDestination, 'getSnapshot' | 'subscribe'>;

export type NativeSavedViewSummaryBinding = Readonly<{
  /** Subscribe to the borrowed source and read its genuine current window. */
  start: () => Promise<void>;
  /** Stable revision for the current window and its arrived vocabulary. */
  getSnapshot: () => number;
  subscribe: (listener: () => void) => () => void;
  /** The canonical computed line for an exact object in the current window. */
  line: (resource: PersonalResource, text: SavedViewSummaryText) => string | undefined;
  /** Stop listening locally. The borrowed source and reader stay usable. */
  dispose: () => void;
}>;

const EMPTY: readonly PersonalResource[] = Object.freeze([]);
type SourceSnapshot = ReturnType<NativeSavedViewSummarySource['getSnapshot']>;

/** Only a genuinely arrived views-directory page is a window. The hub's
 * directory totals are a separate fact and never become resources. */
function arrivedWindow(snapshot: SourceSnapshot): readonly PersonalResource[] {
  const personal = 'views' in snapshot ? snapshot.views : snapshot.personal;
  if (!personal || personal.route?.view !== 'views' || !personal.viewerFence) return EMPTY;
  return personal.resources;
}

/** Binds the canonical computed Saved-view summaries to a real Saved source.
 * The reader is Saved-owned and injected; disposing this binding disposes
 * neither the reader nor the borrowed source. */
export function createNativeSavedViewSummaryBinding(
  context: Parameters<typeof nativeDestinationLifetime>[0],
  source: NativeSavedViewSummarySource,
  ownerCurrent: () => boolean,
  reader: NativeSavedViewSummaryReader,
): NativeSavedViewSummaryBinding {
  const lifetime = nativeDestinationLifetime(context);
  const listeners = new Set<() => void>();
  let window: readonly PersonalResource[] = EMPTY;
  let revision = 0, started = false, retired = false, disposed = false;
  let pending: Promise<void> | undefined, startPromise: Promise<void> | undefined;
  let stopSource: (() => void) | undefined, stopReader: (() => void) | undefined, stopOwner: (() => void) | undefined;

  const live = (): boolean => !disposed && !retired && lifetime.current() && ownerCurrent();
  const emit = (): void => {revision++; for (const listener of [...listeners]) listener();};
  const retire = (notify = true): void => {
    if (disposed || retired) return;
    retired = true; window = EMPTY;
    stopSource?.(); stopSource = undefined;
    stopReader?.(); stopReader = undefined;
    stopOwner?.(); stopOwner = undefined;
    if (notify) emit();
  };
  /** Runs outside render: a lost owner stops this binding without touching the owner's services. */
  const check = (): boolean => {if (!live()) retire(); return live();};
  const onReader = (): void => {if (check()) emit();};
  const load = (resources: readonly PersonalResource[]): Promise<void> => {
    try {return Promise.resolve(reader.load(resources)).catch(() => undefined);} catch {return Promise.resolve();}
  };
  const reconcile = (): void => {
    if (!started || !check()) return;
    const next = arrivedWindow(source.getSnapshot());
    if (next === window) return;
    window = next; emit();
    const task = load(next); pending = task;
    void task.then(() => {if (pending === task) pending = undefined;});
  };
  const start = (): Promise<void> => {
    if (startPromise) return startPromise;
    startPromise = Promise.resolve();
    if (disposed || retired) return startPromise;
    started = true;
    if (!check()) return startPromise;
    stopSource = source.subscribe(reconcile);
    stopReader = reader.subscribe(onReader);
    stopOwner = context.subscribeAuthority(check);
    reconcile();
    if (pending) startPromise = pending;
    return startPromise;
  };
  const line = (resource: PersonalResource, text: SavedViewSummaryText): string | undefined => {
    if (!live()) {retire(false); return undefined;}
    if (!window.includes(resource) || !arrivedWindow(source.getSnapshot()).includes(resource)) return undefined;
    return reader.line(resource, text);
  };
  const dispose = (): void => {
    if (disposed) return;
    disposed = true;
    stopSource?.(); stopSource = undefined;
    stopReader?.(); stopReader = undefined;
    stopOwner?.(); stopOwner = undefined;
    listeners.clear(); window = EMPTY;
  };
  return Object.freeze({start, getSnapshot: () => revision, line, dispose,
    subscribe: (listener: () => void) => {if (disposed) return () => {}; listeners.add(listener); return () => {listeners.delete(listener);};}});
}
