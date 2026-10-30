import {useGuardedLibraryContext, useLibraryRouteGuard} from '../browse/library-route-guard';
import {forwardFacts} from '../../shell/fact-source';
import {useLayoutEffect, useRef, useState} from 'react';
import {useLocation, useParams} from '@tanstack/react-router';
import {defineWebScreen} from '../../route-registry';
import {useProtectedWebScreenContext} from '../../route-registry-view';
import {useCommittedLibraryScreenContext, type ProtectedLibraryScreen} from '../browse/screen-context';
import {router} from '../../router';
import {useAdmittedTitleArtwork} from '../../shell/artwork-navigation';
import type {ScreenContext} from './context';
import {createDetailBindings} from './bindings';
import {DetailScreen} from './Detail';
import {createReadBindings, ReadItemEntry, type ReadItemEligibility} from '../read';
import {resolveItemRead} from './read-resolution';
import {DetailLoading} from './Loading';
import {ScreenProblem} from './ScreenProblem';

type DetailLease = {id: number; context: ScreenContext; itemId: string; live: boolean; binding: ReturnType<typeof createDetailBindings>};
function OrdinaryItem({context, itemId, artworkClaim}: {context: ScreenContext; itemId: string; artworkClaim?: import('../../kit/cards/source-art-transition').SourceArtClaim}) {
  const [lease, publish] = useState<DetailLease>();
  const serial = useRef(0);
  useLayoutEffect(() => {
    const held: DetailLease = {id: ++serial.current, context, itemId, live: true, binding: createDetailBindings(context)};
    publish(held);
    return () => {held.live = false;held.binding.dispose();};
  }, [context, itemId]);
  return lease?.live && lease.context === context && lease.itemId === itemId && context.isCurrent()
    ? <DetailScreen key={lease.id} binding={lease.binding} itemId={itemId} artworkClaim={artworkClaim}/> : <DetailLoading/>;
}
type ReadResolution = {phase: 'loading'} | {phase: 'ready'; eligibility: ReadItemEligibility | null} | {phase: 'error'; error: unknown};
type ResolutionLease = {context: ScreenContext; itemId: string; retry: number; live: boolean};
function BoundItem({captured, itemId}: {captured: ProtectedLibraryScreen; itemId: string}) {
  const entry = useLocation({select: location => ({key: location.state.__TSR_key ?? '', href: location.href, pathname: location.pathname})});
  const href = entry.href, opening = useLocation({select: location => location});
  const guard = useLibraryRouteGuard(captured.context, opening, () => router.state.location === opening, {itemId}, captured.binding.home.effects.reportError);
  const guardedContext = useGuardedLibraryContext(captured.context, guard)!;
  const guarded = {...captured, context: guardedContext};
  const artworkClaim = useAdmittedTitleArtwork(captured.binding.scopeKey, itemId, entry, () => guardedContext.isCurrent() && router.state.location.state.__TSR_key === entry.key);
  const context = useCommittedLibraryScreenContext(guarded, href, () => router.state.location.href === href);
  return context ? <ResolveItem captured={guarded} handoff={captured.context} context={context} itemId={itemId} artworkClaim={artworkClaim}/> : <DetailLoading/>;
}
function ResolveItem({captured, handoff, context, itemId, artworkClaim}: {captured: ProtectedLibraryScreen; handoff: ProtectedLibraryScreen['context']; context: ScreenContext; itemId: string; artworkClaim?: import('../../kit/cards/source-art-transition').SourceArtClaim}) {
  const [retry, setRetry] = useState(0);
  const active = useRef<ResolutionLease | undefined>(undefined);
  const [published, publish] = useState<Readonly<{lease: ResolutionLease; resolution: ReadResolution}>>();
  useLayoutEffect(() => {
    const held: ResolutionLease = {context, itemId, retry, live: true};
    const owns = () => held.live && active.current === held && context.isCurrent();
    active.current = held;
    const reading = createReadBindings(forwardFacts(captured.context, {isCurrent: owns})), request = new AbortController();
    const current = () => owns() && reading.authority.isCurrent();
    const off = reading.authority.subscribe(() => {request.abort();});
    publish({lease: held, resolution: {phase: 'loading'}});
    void resolveItemRead(reading.client, itemId, request.signal, current)
      .then(eligibility => {if (current()) publish({lease: held, resolution: {phase: 'ready', eligibility}});})
      // A refused reading client may already have retired its own authority.
      // The live route still owns the refusal surface, never an ordinary-item fallback.
      .catch(error => {if (owns()) publish({lease: held, resolution: {phase: 'error', error}});});
    return () => {
      held.live = false;if (active.current === held) active.current = undefined;
      request.abort();off();void reading.dispose().catch(() => {});
    };
  }, [captured.context, context, itemId, retry]);
  if (!published) return <DetailLoading/>;
  const held = published.lease;
  if (!held.live || active.current !== held || held.context !== context || held.itemId !== itemId || held.retry !== retry || !context.isCurrent()) return <DetailLoading/>;
  const resolution = published.resolution;
  if (resolution.phase === 'loading') return <DetailLoading/>;
  if (resolution.phase === 'error') return <ScreenProblem context={context} error={resolution.error} retry={() => {
    if (held.live && active.current === held && context.isCurrent()) {held.live = false;setRetry(value => value + 1);}
  }}/>;
  return resolution.eligibility ? <ReadItemEntry captured={captured} handoff={handoff} itemId={itemId} eligibility={resolution.eligibility}/>
    : <OrdinaryItem context={context} itemId={itemId} artworkClaim={artworkClaim}/>;
}
function ItemRouteScreen() {
  const captured = useProtectedWebScreenContext();
  const {itemId} = useParams({from: '/item/$itemId'});
  return captured ? <BoundItem key={itemId} captured={captured} itemId={itemId}/> : null;
}
export default defineWebScreen({id: 'item', component: ItemRouteScreen, phoneChrome: 'standalone'});
