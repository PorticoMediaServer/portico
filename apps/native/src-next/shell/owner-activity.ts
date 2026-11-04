import {ActivitySummaryService, type ActivitySummarySnapshot, type ActivitySummarySource, type ActivitySummarySourceStore} from '../../../../packages/client-core/src/admin/activity-summary';
import {localV1Http} from '../../../../packages/client-core/src/playback-v1/local-http';
import type {NativeShellBootstrapContext} from './bootstrap';
import {viewerQueriesFor} from './freshness';

/** What the activity glyph draws (owner-dashboard 1a): grey when idle, blue while anything runs,
 * the stream count beside it, the bell's dot for an update. Unknown is never drawn as idle-and-known. */
export type NativeOwnerActivityIndicator = Readonly<{known: boolean; count: number; busy: boolean; update: boolean}>;
export type NativeOwnerActivity = Readonly<{
  getSnapshot: () => NativeOwnerActivityIndicator;
  subscribe: (listener: () => void) => () => void;
  /** A drawn glyph holds the summary live while it shows; the last release stops reading. */
  hold: () => () => void;
  dispose: () => void;
}>;

const unknown: NativeOwnerActivityIndicator = Object.freeze({known: false, count: 0, busy: false, update: false});

export function nativeOwnerActivityIndicator(snapshot: ActivitySummarySnapshot): NativeOwnerActivityIndicator {
  const document = snapshot.phase === 'ready' ? snapshot.document : undefined;
  if (!document) return unknown;
  const count = document.streams.count;
  return Object.freeze({known: true, count, update: document.update?.available === true,
    busy: count > 0 || document.scans.length > 0 || document.tasks.count > 0});
}

/** Only the owner's phone and tablet shell mounts the summary, over its existing scoped transport and
 * event feed (web: `shell/Activity.tsx`). Absent for everyone else. */
export function createNativeOwnerActivity(context: NativeShellBootstrapContext, tv: boolean): NativeOwnerActivity | undefined {
  const session = context.session();
  if (tv || !session || context.account.account.role !== 'owner' || session.viewer.role !== 'owner') return undefined;
  let retired = false, holders = 0, last: NativeOwnerActivityIndicator = unknown;
  const sourceListeners = new Set<() => void>(), listeners = new Set<() => void>();
  const value: ActivitySummarySource = {http: localV1Http(context.api), scope: {...session.viewer, sessionFamilyId: session.sessionFamilyId, authorizationEpoch: context.authority().epoch}};
  const source: ActivitySummarySourceStore = {
    getSnapshot: () => !retired && context.current() && context.session()?.sessionFamilyId === session.sessionFamilyId ? value : null,
    subscribe: listener => {sourceListeners.add(listener); return () => {sourceListeners.delete(listener);};},
  };
  const offAuthority = context.subscribeAuthority(() => {for (const listener of [...sourceListeners]) listener();});
  const service = new ActivitySummaryService({source, queries: viewerQueriesFor(context.api, {serverId: session.viewer.serverId})}); // query-read: kept (it reads through the viewer's cache: `queries` serves the shared activitySummaryQuery)
  const offService = service.subscribe(() => {
    const next = nativeOwnerActivityIndicator(service.getSnapshot());
    if (next.known === last.known && next.count === last.count && next.busy === last.busy && next.update === last.update) return;
    last = next; for (const listener of [...listeners]) listener();
  });
  return Object.freeze({
    getSnapshot: () => last,
    subscribe: (listener: () => void) => {listeners.add(listener); return () => {listeners.delete(listener);};},
    hold: () => {
      if (retired || !context.current()) return () => {};
      let held = true;
      if (++holders === 1) {service.setActive(true); service.attach(context.events); void service.load().catch(() => {});}
      return () => {if (!held) return; held = false; if (--holders === 0 && !retired) service.setActive(false);};
    },
    dispose: () => {
      if (retired) return;
      retired = true; offAuthority(); offService();
      for (const listener of [...sourceListeners]) listener();
      service.dispose(); sourceListeners.clear();
      // The service is cleared, but this store's own indicator is what a glyph draws: erase it and tell any glyph still
      // mounted, before its listeners go. Nothing later (the authority change) reaches a disposed store.
      const shown = last; last = unknown;
      if (shown !== unknown) for (const listener of [...listeners]) listener();
      listeners.clear();
    },
  });
}
