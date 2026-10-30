import type {ContentEntryKind} from '@core/library-content.ts';
import type {ShowWorkspaceRoute} from '@core/show-workspace.ts';
import type {ScreenContext} from './context';
import {createDetailBindings} from './bindings';
import {createShowBindings} from './show-bindings';

/** Supplied from the actual playing subject and its offered library scope. */
export type WatchBodyTarget = Readonly<{libraryId: string; itemId: string; kind: ContentEntryKind; showId?: string; seasonId?: string}>;
export type WatchMyList = Readonly<{available: boolean; watchlisted?: boolean; busy: boolean}>;

/** Inert, title-owned lifetime. The Watch host retains this through posture and
 * full-window changes; load is explicit and disposal erases its private state. */
export function createWatchBodyBindings(context: ScreenContext, input: WatchBodyTarget) {
  const target = Object.freeze({...input});
  const body = target.kind === 'show' || target.kind === 'season' || target.kind === 'episode'
    ? {kind: 'show' as const, binding: createShowBindings(context)}
    : {kind: 'title' as const, binding: createDetailBindings(context)};
  let active = true, request: Promise<void> | undefined;
  let state: WatchMyList = Object.freeze({available: false, busy: false});
  const listeners = new Set<() => void>();
  const current = () => active && context.isCurrent();
  const assert = () => {if (!current()) throw new DOMException('Watch title authority changed', 'AbortError');};
  const changed = () => {
    const snapshot = body.binding.service.getSnapshot();
    const personal = body.kind === 'show' ? body.binding.personal.getSnapshot() : undefined;
    const detail = body.kind === 'title' ? body.binding.service.getSnapshot() : undefined;
    const next: WatchMyList = current() && snapshot.phase === 'ready'
      ? {available: body.kind === 'show' ? !!personal : !!detail?.data?.actions.some(action => action.id === 'watchlist' && action.enabled),
        watchlisted: body.kind === 'show' ? personal?.watchlisted : detail?.data?.personal.watchlisted,
        busy: body.kind === 'show' ? body.binding.personal.getBusy() : !!detail?.pending.length}
      : {available: false, busy: false};
    if (next.available === state.available && next.watchlisted === state.watchlisted && next.busy === state.busy) return;
    state = Object.freeze(next);for (const listener of listeners) listener();
  };
  const off = body.binding.service.subscribe(changed);
  const offPersonal = body.kind === 'show' ? body.binding.personal.subscribe(changed) : undefined;
  return {
    target, body,
    load(signal?: AbortSignal): Promise<void> {
      assert();signal?.throwIfAborted();
      if (!request) {
        const route: ShowWorkspaceRoute = {libraryId: target.libraryId,
          ...(target.kind === 'show' ? {showId: target.showId ?? target.itemId} : target.showId ? {showId: target.showId} : {}),
          ...(target.kind === 'season' ? {seasonId: target.seasonId ?? target.itemId} : target.seasonId ? {seasonId: target.seasonId} : {}),
          ...(target.kind === 'episode' ? {episodeId: target.itemId} : {})};
        request = (body.kind === 'show' ? body.binding.select(route, signal) : body.binding.select({libraryId: target.libraryId, itemId: target.itemId, kind: target.kind}, signal))
          .catch(error => {request = undefined;throw error;});
      }
      return request;
    },
    myList: {
      getSnapshot: () => state,
      subscribe: (listener: () => void) => {listeners.add(listener);return () => {listeners.delete(listener);};},
      async set(value: boolean) {
        assert();
        if (!state.available || state.busy) throw new DOMException('Watch title action changed', 'AbortError');
        if (body.kind === 'show') {
          const data = body.binding.service.getSnapshot().data;
          if (!data) throw new DOMException('Watch title changed', 'AbortError');
          await body.binding.setMyList(data, value);
        } else {
          const data = body.binding.service.getSnapshot().data;
          if (!data) throw new DOMException('Watch title changed', 'AbortError');
          await body.binding.mutate(data, {action: 'watchlist', value});
        }
      },
    },
    dispose() {
      if (!active) return;
      active = false;off();offPersonal?.();body.binding.dispose();changed();listeners.clear();
    },
  };
}
export type WatchBodyBindings = ReturnType<typeof createWatchBodyBindings>;
