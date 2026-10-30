import {defineWebScreen} from '../../route-registry';
import {useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore} from 'react';
import {useProtectedWebScreenContext} from '../../route-registry-view';
import {createDetailBindings, WatchTitleBody, type ScreenContext, type WatchMyList} from '../detail';
import {WatchPlayback} from './WatchPlayback';
import type {WatchBindings, WatchPanelContext} from './types';
import {WatchBodyOwner, type RetainedWatchBody} from './RetainedBody';
import {watchBodyTarget, watchBodyKey} from './body-target';
import {TogetherOwner} from '../together/owner';
import {WatchVersionPresentationOwner} from './VersionPresentationOwner';
import {useWatchPanels} from './use-watch-panels';
import {useLiveChromePort} from '../live/use-live-chrome';
import {sameFence} from '../../../../packages/player-core/src/index.ts';
import {fenceKey} from '../../shell/playback-model';
import type {DetailProjection} from '@core/detail.ts';
import {watchEndCard} from './end-card';
import {readCollectionNext, type WatchCollectionNext} from './collection-next';
import {WatchEpisodesDrawer} from './EpisodesDrawer';
import {WatchQueueList} from './QueueList';
import {watchEpisodes, type WatchEpisodeRow} from './episodes';
import {router} from '../../router';

const noSubscribe = () => () => {};
const noSnapshot = () => undefined;
const emptyList: WatchMyList = Object.freeze({available: false, busy: false});
const noList = () => emptyList;

/** The persistent host renders the live player across routes; the outlet owns no media. */
export function renderWatch() { return null; }
function RegisteredWatchPlayback({binding}: {binding: WatchBindings | undefined}) {
  const captured = useProtectedWebScreenContext();
  const runtime = binding?.runtime;
  const snapshot = useSyncExternalStore(runtime?.subscribe ?? noSubscribe, runtime?.getSnapshot ?? noSnapshot, noSnapshot);
  const target = watchBodyTarget(snapshot), [body, setBody] = useState<RetainedWatchBody | undefined>(undefined);
  const owner = captured && runtime && captured.binding.playback?.runtime === runtime ? captured : undefined;
  const held = body && body.runtime === runtime && body.context.isCurrent() ? body : undefined;
  const panels = useWatchPanels(binding, snapshot, owner?.context);
  const live = useLiveChromePort(owner?.context, runtime, snapshot, binding?.skips ?? {back: 10, forward: 30});
  const list = useSyncExternalStore(held?.binding.myList.subscribe ?? noSubscribe, held?.binding.myList.getSnapshot ?? noList, noList);
  // desktop-player f4b: the retained title read is the only source of the film-end card.
  const detailBinding = held && held.binding.body.kind === 'title' ? held.binding.body.binding : undefined;
  const detail = useSyncExternalStore(detailBinding?.service.subscribe ?? noSubscribe, detailBinding?.service.getSnapshot ?? noSnapshot, noSnapshot);
  // desktop-player f4b: the collection's own next member, read in its order while the film ends.
  const [collectionNext, setCollectionNext] = useState<WatchCollectionNext | undefined>(undefined);
  const collection = detail?.data?.collections?.[0], finishKey = snapshot?.presentation?.finished ? `${collection?.id ?? ''}:${detail?.data?.item.id ?? ''}` : '';
  const completedFence = snapshot?.presentation?.fence, completedFenceKey = completedFence ? fenceKey(completedFence) : '';
  // The finishing pass keeps the detail data and collection row of the commit that
  // admitted its finish key; later retained reads cannot replace that witness.
  const finishAdmission = useRef({detail, collection});
  useLayoutEffect(() => {finishAdmission.current = {detail, collection};});
  useEffect(() => {
    setCollectionNext(undefined);
    const {detail, collection} = finishAdmission.current;
    const body = held, context = body?.context, data = detail?.data, card = collection, api = context?.api, playback = runtime;
    if (!body || !context || !data || !card || !api || !playback || !finishKey || card.position >= card.memberCount) return;
    const life = new AbortController(), run = playback.getSnapshot().playback.playbackRun;
    const fence = playback.getSnapshot().presentation?.fence, titleId = data.item.id;
    const current = () => {
      const live = playback.getSnapshot();
      if (life.signal.aborted || live.disposed || live.playback.playbackRun !== run || !context.isCurrent()) return false;
      const fenceNow = live.presentation?.fence;
      if (live.presentation?.finished !== true || !fenceNow || fenceNow.kind !== 'queue' || fenceNow.itemId !== titleId || !fence || !sameFence(fenceNow, fence)) return false;
      const completed = body.context === context && body.binding.body.kind === 'title' ? body.binding.body.binding.service.getSnapshot().data : undefined;
      return !!completed && completed.item.id === titleId;
    };
    void readCollectionNext(api, card, titleId, id => {
      if (!current()) return Promise.reject(new DOMException('Playback changed', 'AbortError'));
      return playback.play(id);
    }, life.signal).then((value: WatchCollectionNext | undefined) => {if (current()) setCollectionNext(value);}).catch(() => {if (current()) setCollectionNext(undefined);});
    return () => life.abort();
  }, [held, runtime, collection?.id, detail?.data?.item.id, finishKey, completedFenceKey]);
  // desktop-player f4b: one retained detail read for the exact next collection
  // member. Its lease keeps the original completed-title context, runtime, full
  // playback fence, title and collection receipt; reads, draws and actions repeat
  // that tuple.
  type NextMember = {live: boolean; context: ScreenContext; runtime: NonNullable<typeof runtime>; fence: NonNullable<typeof completedFence>; titleId: string; collectionNext: WatchCollectionNext; binding: ReturnType<typeof createDetailBindings>};
  const memberLive = useRef<NextMember | undefined>(undefined);
  const [nextMember, setNextMember] = useState<NextMember | undefined>(undefined);
  const [listPending, setListPending] = useState<NextMember | undefined>(undefined);
  const listOp = useRef<{member: NextMember; projection: DetailProjection; value: boolean} | undefined>(undefined);
  const memberWitness = (member: NextMember | undefined): member is NextMember => {
    if (!member || !member.live || memberLive.current !== member || !held || !runtime) return false;
    if (member.context !== held.context || member.runtime !== runtime || !member.context.isCurrent()) return false;
    const live = runtime.getSnapshot(), currentFence = live.presentation?.fence;
    if (live.disposed || live.presentation?.finished !== true) return false;
    if (!currentFence || currentFence.kind !== 'queue' || currentFence.itemId !== member.titleId || !sameFence(currentFence, member.fence)) return false;
    const completed = held.binding.body.kind === 'title' ? held.binding.body.binding.service.getSnapshot().data : undefined;
    if (!completed || completed.item.id !== member.titleId || completed.scope.itemId !== member.titleId) return false;
    if (detail?.data?.item.id !== member.titleId || collectionNext !== member.collectionNext) return false;
    return true;
  };
  // The next-member lease is built from the same committed detail read and witness
  // that admitted this collection's finish.
  const memberAdmission = useRef({detail, memberWitness});
  useLayoutEffect(() => {memberAdmission.current = {detail, memberWitness};});
  useLayoutEffect(() => {
    const {detail, memberWitness} = memberAdmission.current;
    if (!collectionNext || !held || !runtime || !detail?.data) return;
    const completed = detail.data, context = held.context, live = runtime.getSnapshot(), liveFence = live.presentation?.fence;
    if (!liveFence || liveFence.kind !== 'queue' || liveFence.itemId !== completed.item.id || live.presentation?.finished !== true) return;
    if (!context.isCurrent() || live.disposed || completed.scope.itemId !== completed.item.id) return;
    const memberBinding = createDetailBindings(context);
    const member: NextMember = {live: true, context, runtime, fence: liveFence, titleId: completed.item.id, collectionNext, binding: memberBinding};
    memberLive.current = member;setNextMember(member);
    void memberBinding.select({itemId: collectionNext.id}).catch(error => {
      if (!memberWitness(member)) return;
      if (error instanceof DOMException && error.name === 'AbortError') return;
      member.context.effects.reportError(error);
    });
    return () => {
      member.live = false;
      if (listOp.current?.member === member) {listOp.current = undefined;setListPending(current => current === member ? undefined : current);}
      if (memberLive.current === member) memberLive.current = undefined;
      memberBinding.dispose();
      setNextMember(current => current === member ? undefined : current);
    };
  }, [collectionNext, held, runtime, completedFenceKey]);
  const nextDetail = useSyncExternalStore(nextMember?.binding.service.subscribe ?? noSubscribe, nextMember?.binding.service.getSnapshot ?? noSnapshot, noSnapshot);
  const nextData = memberWitness(nextMember) && nextDetail?.phase === 'ready' && nextDetail.data
    && nextDetail.data === nextMember.binding.service.getSnapshot().data && nextDetail.data.item.id === nextMember.collectionNext.id ? nextDetail.data : undefined;
  // A pending watchlist write belongs to the card that started it; a successor
  // card starts clean and its own claim is never blocked by an old settlement.
  useLayoutEffect(() => {
    const op = listOp.current;
    if (!op || op.member === nextMember) return;
    listOp.current = undefined;
    setListPending(current => current === op.member ? undefined : current);
  }, [nextMember]);
  const setWatchlist = (value: boolean): Promise<void> => {
    const member = nextMember;
    if (!member || !memberWitness(member) || !nextData || listOp.current) return Promise.resolve();
    const current = member.binding.service.getSnapshot();
    if (current.phase !== 'ready' || current.data !== nextData || !nextData.actions.some(action => action.id === 'watchlist' && action.enabled)) return Promise.resolve();
    const op = {member, projection: nextData, value};
    listOp.current = op;setListPending(member);
    return member.binding.mutate(nextData, {action: 'watchlist', value}).then(() => {
      if (listOp.current !== op) return;
      listOp.current = undefined;
      if (memberWitness(member)) setListPending(current => current === member ? undefined : current);
    }, error => {
      if (listOp.current !== op) return;
      listOp.current = undefined;
      if (!memberWitness(member)) return;
      setListPending(current => current === member ? undefined : current);
      member.context.effects.reportError(error);
    });
  };
  const nextFacts = nextData ? {
    ...(nextData.metadata.genres.length ? {genres: Object.freeze(nextData.metadata.genres.map(genre => genre.name).filter(Boolean))} : {}),
    ...(nextData.actions.some(action => action.id === 'watchlist' && action.enabled) ? {myList: Object.freeze({listed: nextData.personal.watchlisted, pending: listPending === nextMember, set: setWatchlist})} : {}),
  } : undefined;
  const collectionForCard = collectionNext && nextFacts && ('genres' in nextFacts || 'myList' in nextFacts) ? {...collectionNext, ...nextFacts} : collectionNext;
  const end = held && binding && detailBinding && detail?.data
    ? watchEndCard(detail.data, entry => binding.artwork.foreground(entry.posterUrl, 260, 2 / 3, {group: 'playback'}),
      id => router.navigate({to: '/item/$itemId', params: {itemId: id}}),
      value => detail.data ? detailBinding.mutate(detail.data, {action: 'rating', value}) : Promise.reject(new Error('rating_unavailable')),
      collectionForCard,
      url => binding.artwork.foreground(url, 1024, 16 / 9, {group: 'playback', priority: 'high'}))
    : undefined;
  const matches = !!held && !!target && held.key === watchBodyKey(target);
  // desktop-player f6: the drawer reads the same retained title page the portrait body owns.
  const showBinding = held && held.binding.body.kind === 'show' ? held.binding.body.binding : undefined;
  const show = useSyncExternalStore(showBinding?.service.subscribe ?? noSubscribe, showBinding?.service.getSnapshot ?? noSnapshot, noSnapshot);
  const showData = showBinding && show?.phase === 'ready' ? show.data : null;
  const episodes = binding && held && showData && snapshot?.fence?.kind === 'queue' && matches
    ? watchEpisodes({data: showData, queue: snapshot.queueView, playingItemId: snapshot.fence.itemId,
      positionSeconds: snapshot.presentation?.positionSeconds, durationSeconds: snapshot.presentation?.durationSeconds}) : undefined;
  const playEpisode = (row: WatchEpisodeRow) => {
    if (!binding) return Promise.reject(new DOMException('Playback source retired', 'AbortError'));
    const live = binding.runtime.getSnapshot();
    if (!live.fence || live.fence.kind !== 'queue' || live.disposed) return Promise.reject(new DOMException('Playback source retired', 'AbortError'));
    return binding.runtime.selectEpisode(live.fence, row.itemId);
  };
  const selectSeason = (seasonId: string | null) => {
    if (!showBinding || !showData) return Promise.reject(new DOMException('Title changed', 'AbortError'));
    return showBinding.select({libraryId: showData.scope.libraryId, showId: showData.show.id, ...(seasonId ? {selectedSeasonId: seasonId} : {})});
  };
  // f1q: the queue list keeps the captured run's own port; shows keep the authored Episodes drawer.
  const renderPanel = (context: WatchPanelContext) => context.panel === 'queue' && panels?.queue
    ? <WatchQueueList port={panels.queue} context={context} />
    : context.panel === 'episodes' && episodes && binding
    ? <WatchEpisodesDrawer episodes={episodes} context={context} locale={binding.environment.locale}
      artwork={url => binding.artwork.foreground(url, 632, 16 / 9, {group: 'playback'})}
      onPlay={row => playEpisode(row).then(() => context.close())} onSeason={selectSeason} reportError={binding.reportError} />
    : panels?.render(context) ?? null;
  return <>
    {owner && runtime && <WatchBodyOwner captured={owner} runtime={runtime} target={target} onReady={setBody} />}
    <WatchPlayback binding={binding} context={owner?.context} surfaces={held ? {...panels, available: [...(panels?.available ?? []), ...(episodes ? ['episodes' as const] : [])],
      render: renderPanel,
      body: <div hidden={!matches}><WatchTitleBody binding={held.binding} compact /></div>, bodyIdentity: `${captured?.binding.scopeKey}:${held.key}`,
      myList: matches && list.available && typeof list.watchlisted === 'boolean' ? {selected: list.watchlisted, pending: list.busy,
        toggle: () => {const current = held.binding.myList.getSnapshot();if (typeof current.watchlisted !== 'boolean') throw new DOMException('Title state changed', 'AbortError');return held.binding.myList.set(!current.watchlisted);}} : undefined,
      ...(end ? {end} : {}),
    } : panels} live={live} />
    {owner && binding && <TogetherOwner context={owner.context} playback={binding} />}
    {owner && binding && <WatchVersionPresentationOwner context={owner.context} reportError={binding.reportError} />}
  </>;
}

export default defineWebScreen({id: 'player', chrome: 'player', component: renderWatch, playbackHost: RegisteredWatchPlayback});
