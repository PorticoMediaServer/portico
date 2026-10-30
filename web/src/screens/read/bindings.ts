import type {ShellFactsSource} from '../../shell/fact-source';
import {subscribePresentation} from '../../shell/fact-hooks';
import {ReadingError, type HttpLocalApi, type LocalSession, type PreferenceSnapshot, type ReadingClient, type ReadingSession} from '@portico/client-core';
import type {ReadPreferenceSource} from './preferences';
import type {ReaderMountAuthority} from './mount';
import {libraryAccessEnded} from '../../shell/library-route-proof';
import {routePath} from '../../../../packages/contracts/src/routes.ts';

export type ReadBindingContext = Readonly<{
  api: Pick<HttpLocalApi, 'reading'> & Partial<Pick<HttpLocalApi, 'farewell'>>; session: () => Pick<LocalSession, 'viewer' | 'sessionFamilyId'>;
  isCurrent: () => boolean; preferences?: Omit<ReadPreferenceSource, 'getSnapshot'> & Readonly<{getSnapshot: () => PreferenceSnapshot | undefined}>;
  facts: Readonly<{preferences: PreferenceSnapshot}>; factsSource?: ShellFactsSource; libraryRouteSignal?: AbortSignal;
}>;
export type ReadBindings = Readonly<{
  client: ReadingClient; authority: ReaderMountAuthority; preferences: ReadPreferenceSource;
  open: (request: Parameters<ReadingClient['open']>[0]) => Promise<ReadingSession>;
  close: () => Promise<void>; dispose: () => Promise<void>;
}>;

/** Capture the admitted shell once. No constructor requests, route strings,
 * token access, preference projection or independent book state. */
export function createReadBindings(context: ReadBindingContext): ReadBindings {
  const captured = context.session(), viewer = captured.viewer, life = new AbortController(), routeLeft = new AbortController();
  let alive = true, retiring = false, client: ReadingClient | undefined;
  let pending: AbortController | undefined, session: ReadingSession | undefined;
  let closing = Promise.resolve(), disposal: Promise<void> | undefined;
  // The same viewer, whatever the route: a session this binding opened may be released after the reader's
  // route has gone (unmount follows navigation), but never under another viewer.
  const viewerMatches = () => {
    if (client?.signal.aborted) return false;
    const current = context.session();
    return current.sessionFamilyId === captured.sessionFamilyId && current.viewer.serverId === viewer.serverId
      && current.viewer.authority === viewer.authority && current.viewer.accountId === viewer.accountId
      && current.viewer.profileId === viewer.profileId && current.viewer.role === viewer.role;
  };
  const authorized = () => alive && !life.signal.aborted && !routeLeft.signal.aborted && context.isCurrent() && viewerMatches();
  // Opens on their way and releases in progress keep the client usable for this same viewer only.
  let releasing = 0, inFlight = 0;
  const current = () => !retiring && authorized();
  client = context.api.reading({viewer, isCurrent: () => authorized() || releasing + inFlight > 0 && viewerMatches(), signal: life.signal});
  const owner = client;
  const authority: ReaderMountAuthority = Object.freeze({isCurrent: current, subscribe(listener: () => void) {
    let notified = false;
    const retired = () => {if (!notified) {notified = true;listener();}};
    for (const signal of [owner.signal, life.signal, routeLeft.signal]) signal.addEventListener('abort', retired);
    return () => {for (const signal of [owner.signal, life.signal, routeLeft.signal]) signal.removeEventListener('abort', retired);};
  }});
  // Each open holds one of the profile's few server reading sessions (P2-74 E-web-15): release it on
  // replacement and on leaving, including one that arrives after the reader has already gone.
  const release = (opened: ReadingSession): Promise<void> => {
    if (!viewerMatches()) return Promise.resolve();
    releasing++;
    const cleanup = new AbortController(), deadline = setTimeout(() => cleanup.abort(), 2000);
    return opened.close(cleanup.signal).catch(() => {}).finally(() => {clearTimeout(deadline); releasing--;});
  };
  let opening: Promise<void> = Promise.resolve();
  const close = () => {
    // A pending open is not aborted: its session may already exist on the server. It is released on arrival.
    pending = undefined;
    const previous = session; session = undefined;
    const task = previous ? release(previous) : Promise.resolve();
    closing = Promise.all([closing.catch(() => {}), task, opening]).then(() => {});
    return closing;
  };
  const open: ReadBindings['open'] = async request => {
    if (!current()) throw new ReadingError('cancelled');
    const before = close(), operation = new AbortController(); pending = operation;
    await before;
    if (!current() || pending !== operation) throw new ReadingError('cancelled');
    inFlight++;
    const attempt = owner.open(request, operation.signal).finally(() => {inFlight--;});
    opening = attempt.then(opened => {if (pending !== operation || !current()) return release(opened);}, () => {});
    const opened = await attempt;
    if (!current() || pending !== operation) throw new ReadingError('cancelled');
    pending = undefined; session = opened; return opened;
  };
  // A hard leave (reload, tab close) never runs the cleanup below, so each one left a session held until it
  // expired, and about ten reloads reached the profile's cap (P2-74 E-web-33). The held session gets a farewell the
  // browser delivers after the page is gone. A page kept for Back (bfcache) may return, so it keeps its session.
  const page = typeof window === 'undefined' ? undefined : window;
  const pageGone = (event: Event) => {
    const held = session;
    if ((event as PageTransitionEvent).persisted || !held || !viewerMatches()) return;
    context.api.farewell?.(routePath('delete_reading_sessions_sessionId', {sessionId: held.manifest.sessionId}), 'DELETE');
  };
  page?.addEventListener('pagehide', pageGone);
  const dispose = () => {
    if (disposal) return disposal;
    page?.removeEventListener('pagehide', pageGone);
    // Keep captured authority valid through explicit Close, then retire it.
    const stopped = close(); retiring = true;
    disposal = stopped.finally(() => {alive = false; context.libraryRouteSignal?.removeEventListener('abort', stopRoute);life.abort(); owner.dispose();});
    return disposal;
  };
  // Leaving the route retires the reader at once, but the client lives until this binding's sessions are
  // released: the route guard is disposed before the reader unmounts (P2-74 E-web-15). A library that is no
  // longer the viewer's stops the client at once instead, releasing nothing.
  const stopRoute = () => {
    routeLeft.abort();
    if (libraryAccessEnded(context.libraryRouteSignal)) life.abort();
    else void dispose().catch(() => {});
  };
  context.libraryRouteSignal?.addEventListener('abort', stopRoute, {once: true});
  if (context.libraryRouteSignal?.aborted) stopRoute();
  const preferences: ReadPreferenceSource = Object.freeze({
    getSnapshot: () => context.preferences?.getSnapshot() ?? context.facts.preferences,
    subscribe: listener => subscribePresentation(context, listener),
  });
  return Object.freeze({client: owner, authority, preferences, open, close, dispose});
}
