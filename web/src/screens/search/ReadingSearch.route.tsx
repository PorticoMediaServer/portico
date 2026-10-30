import {useLayoutEffect, useRef, useState} from 'react';
import {useLocation, useSearch} from '@tanstack/react-router';
import {validReadingIndexedSearchQuery} from '@core/reading-indexed-search.ts';
import {defineWebScreen, type WebScreenSearch} from '../../route-registry';
import {useProtectedWebScreenContext} from '../../route-registry-view';
import {router} from '../../router';
import type {ProtectedLibraryScreen} from '../browse/screen-context';
import {ReadingSearchController} from './reading-search-controller';
import {openIndexedReadingTarget} from './reading-open-handoff';
import {ReadingSearchScreen} from './ReadingSearch';

type Assembly = ProtectedLibraryScreen['context'];
type Session = Pick<ReturnType<Assembly['session']>, 'sessionFamilyId' | 'viewer'>;
type Lease = Readonly<{context: Assembly; session: Session; location: typeof router.state.location; controller: ReadingSearchController;
  key: number; current: () => boolean}>;
const sameSession = (left: Session, right: Session) => left.sessionFamilyId === right.sessionFamilyId
  && (['serverId', 'authority', 'accountId', 'profileId', 'role'] as const).every(key => left.viewer[key] === right.viewer[key]);
const indexedSearchQuery = (raw: WebScreenSearch): {q: string} => ({q: validReadingIndexedSearchQuery(raw.q) ? raw.q : ''});

function BoundReadingSearch({captured}: {captured: ProtectedLibraryScreen}) {
  const location = useLocation({select: value => value}), search = useSearch({from: '/search/reading'}), {q} = indexedSearchQuery(search);
  const context = captured.context, session = context.session(), viewer = session.viewer;
  const [published, publish] = useState<Lease>(), generation = useRef(0);
  // Publish only at commit, then sample once for this setup's original lifetime.
  const setupPorts = useRef({session});
  useLayoutEffect(() => {setupPorts.current = {session};});
  useLayoutEffect(() => {
    const {session} = setupPorts.current;
    const original: Session = {sessionFamilyId: session.sessionFamilyId, viewer: {...session.viewer}};
    let live = true;
    const lifetime = new AbortController();
    const current = () => live && !lifetime.signal.aborted && context.isCurrent() && sameSession(context.session(), original)
      && location.pathname === '/search/reading' && router.state.location === location;
    if (!current()) return;
    const controller = new ReadingSearchController({api: context.api, signal: lifetime.signal, isCurrent: current,
      open: (target, callerCurrent) => openIndexedReadingTarget(context, target, () => current() && callerCurrent())});
    publish({context, session: original, location, controller, key: ++generation.current, current});
    controller.select(q, true);
    return () => {live = false;lifetime.abort();controller.dispose();};
    // No playback/binding publication can replace this query or original API.
  }, [context, session.sessionFamilyId, viewer.serverId, viewer.authority, viewer.accountId, viewer.profileId, viewer.role, location, q]);
  const owned = published?.context === context && published.location === location && sameSession(session, published.session) && published.current() ? published : undefined;
  return owned ? <ReadingSearchScreen key={owned.key} controller={owned.controller} artwork={owned.context.artwork}/> : null;
}
function ReadingSearchRoute() {
  const captured = useProtectedWebScreenContext();
  return captured ? <BoundReadingSearch captured={captured}/> : null;
}
export default defineWebScreen({id: 'readingSearch', component: ReadingSearchRoute, validateSearch: indexedSearchQuery});
