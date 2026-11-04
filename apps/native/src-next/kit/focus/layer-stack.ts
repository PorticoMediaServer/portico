import {requestNativeFocus, type FocusHost, type FocusRegistry, type FocusSnapshot} from './registry';

/** One full-screen TV layer: the page, a route over it, the player, the listening screen. Bottom first. */
export type TVLayerSpec = Readonly<{
  key: string;
  /** The layer's own native host (its CoveredLayer). A fresh layer takes focus by asking it, which lands on the
   * element last focused there or its first focusable. A layer without a host claims focus itself. */
  host?: () => FocusHost | null | undefined;
  /** A layer that names the control it opens on asks for it itself (asked instead of the host). Returns
   * whether a request was sent; the stack asks again as elements attach, within its wait. */
  claim?: () => boolean;
}>;
type Registry = Pick<FocusRegistry, 'capture' | 'focus' | 'restore' | 'retain' | 'subscribe'>;
type Clock = Readonly<{later: (run: () => void, ms: number) => unknown; cancel: (handle: unknown) => void}>;
const realClock: Clock = {later: (run, ms) => setTimeout(run, ms), cancel: handle => clearTimeout(handle as ReturnType<typeof setTimeout>)};
/** How long a layer waits for the element it is owed to attach (a windowed grid re-projecting, a page re-enabling its cards). */
export const LAYER_FOCUS_WAIT_MS = 1000;

/**
 * The TV layer stack (TV-FOCUS-REVIEW §5). Exactly one layer is on top; every other is covered. When a layer is
 * covered by a new one, the focus it had is kept as its opener and the new layer takes focus. When the layer
 * above it goes, it gets that focus back: the exact element (by title where it has one), waiting while it
 * attaches, then its row, region or neighbours, then the layer itself. It replaces the shell's per-case page and
 * route memories, claim loops and frame-counted returns.
 */
export function createTVLayerStack(registry: Registry, clock: Clock = realClock, wait = LAYER_FOCUS_WAIT_MS) {
  let layers: readonly TVLayerSpec[] = [];
  const openers = new Map<string, Readonly<{snapshot: FocusSnapshot; release: () => void}>>();
  const listeners = new Set<() => void>();
  let revision = 0, pending: (() => void) | undefined, disposed = false;
  const same = (a: FocusSnapshot | undefined, b: FocusSnapshot | undefined) => a?.regionId === b?.regionId && a?.childId === b?.childId;
  const emit = () => {revision++; for (const listener of [...listeners]) listener();};
  const drop = (key: string) => {const saved = openers.get(key); openers.delete(key); saved?.release();};

  /** Keep asking until focus has moved off where it was when the layer changed (or is already on what the layer is
   * owed), or the wait runs out. A request that cannot land yet (nothing attached) is asked again as each element
   * attaches; nothing polls frames. A request never re-enters itself through the focus event it causes. */
  const settle = (ask: () => boolean, last: () => void, owed?: FocusSnapshot) => {
    pending?.();
    const before = registry.capture();
    const done = () => {
      const now = registry.capture();
      // Already on what it is owed counts only once a request for it was actually sent.
      return !!now && (!same(now, before) || granted && !!owed && (same(now, owed) || owed.entityId !== undefined && now.entityId === owed.entityId && now.regionId === owed.regionId));
    };
    let finished = false, asking = false, granted = false;
    const finish = () => {if (finished) return; finished = true; off(); clock.cancel(timer); if (pending === finish) pending = undefined;};
    const tryAsk = () => {if (asking || finished) return; asking = true; try {granted = ask() || granted;} finally {asking = false;} if (done()) finish();};
    const off = registry.subscribe(() => {if (finished || asking) return; if (done()) finish(); else tryAsk();});
    const timer = clock.later(() => {if (finished) return; finish(); if (!done()) last();}, wait);
    pending = finish;
    // Always ask once: the registry can still name the old element when focus went somewhere it does not track.
    tryAsk();
  };
  const host = (key: string) => layers.find(layer => layer.key === key)?.host?.();
  const claim = (key: string) => {
    const target = layers.find(layer => layer.key === key);
    const ask = target?.claim ?? (target?.host ? () => requestNativeFocus(host(key)) : undefined);
    if (!ask) return;
    settle(ask, () => {});
  };
  const restore = (key: string, snapshot: FocusSnapshot) => settle(
    () => registry.focus(snapshot),
    () => {if (!registry.restore(snapshot)) requestNativeFocus(host(key));}, snapshot);

  return {
    /** The layers present now, bottom first. Call after the commit that painted them (a layout effect), so a
     * layer uncovered by that commit can already take focus. */
    sync(next: readonly TVLayerSpec[]) {
      if (disposed) return;
      const before = layers.at(-1)?.key, after = next.at(-1)?.key;
      const present = new Set(next.map(layer => layer.key));
      layers = next;
      for (const key of [...openers.keys()]) if (!present.has(key)) drop(key);
      if (before === after) return;
      emit();
      if (after === undefined) {pending?.(); return;}
      if (before !== undefined && present.has(before)) {
        // A layer was pushed over `before`: keep where focus was (an overlay's opener when focus was in one).
        const captured = registry.capture(), opener = captured?.returnTo ?? captured;
        drop(before);
        if (opener) openers.set(before, {snapshot: opener, release: registry.retain(opener)});
        claim(after);
        return;
      }
      const saved = openers.get(after);
      if (saved) {openers.delete(after); restore(after, saved.snapshot); saved.release(); return;}
      if (before !== undefined) claim(after);
    },
    top: (): string | undefined => layers.at(-1)?.key,
    isTop: (key: string): boolean => layers.at(-1)?.key === key,
    /** The focus a covered layer is owed (tests and diagnostics). */
    opener: (key: string): FocusSnapshot | undefined => openers.get(key)?.snapshot,
    subscribe(listener: () => void) {listeners.add(listener); return () => {listeners.delete(listener);};},
    getSnapshot: () => revision,
    dispose() {disposed = true; pending?.(); for (const key of [...openers.keys()]) drop(key); listeners.clear();},
  };
}
export type TVLayerStack = ReturnType<typeof createTVLayerStack>;

type LayerNavigation = Readonly<{getSnapshot: () => Readonly<{route: Readonly<{kind: string}>}>;
  presentationStack?: () => readonly Readonly<{occurrence: string; route: Readonly<{kind: string}>}>[]}> | undefined;
/** The routes above the TV page, one layer per occurrence in the Back stack (a route opened twice is two layers).
 * A JSON string, so it can be a useSyncExternalStore snapshot. */
export function tvRouteLayerKeys(navigation: LayerNavigation): string {
  const stack = navigation?.presentationStack?.();
  if (!stack) {const route = navigation?.getSnapshot().route; return JSON.stringify(route && route.kind !== 'shell' ? [`route:${JSON.stringify(route)}`] : []);}
  let page = stack.length - 1;
  while (page >= 0 && stack[page]!.route.kind !== 'shell') page--;
  return JSON.stringify(stack.slice(page + 1).map(presentation => `route:${presentation.occurrence}`));
}
/** How a layer drawn outside the shell (the listening screen) takes focus, provided by that layer while it is
 * mounted (TVF-A1). The stack asks it when the layer goes on top. */
const layerClaims = new Map<string, () => boolean>();
export function provideTVLayerClaim(key: 'listening', claim: () => boolean): () => void {
  layerClaims.set(key, claim);
  return () => {if (layerClaims.get(key) === claim) layerClaims.delete(key);};
}
/** The full-screen TV layers, bottom first: the page, each route over it, the player, the listening screen. */
export function tvLayerSpecs(routes: readonly string[], expanded: boolean, listening: boolean,
  page: () => FocusHost | null, route: () => FocusHost | null): readonly TVLayerSpec[] {
  return [{key: 'page', host: page}, ...routes.map(key => ({key, host: route})),
    // The player takes focus itself (its preferred control); the listening screen tells the stack which control it
    // opens on. Either way the stack covers what is under them and gives the layer below its focus back when they go.
    ...expanded ? [{key: 'player'}] : [], ...listening ? [{key: 'listening', claim: () => layerClaims.get('listening')?.() ?? false}] : []];
}
