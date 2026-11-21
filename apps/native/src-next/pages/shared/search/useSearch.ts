import {useEffect, useMemo, useState, useSyncExternalStore} from 'react';
import {orderedSearchGroups, SEARCH_CHIPS} from '../../../../../../packages/client-core/src/presentation/search-model';
import {repeat} from '../../../../../../packages/client-core/src/timer';
import {serverNow} from '../../../../../../packages/client-core/src/server-clock';
import type {NativeSearchOnNowSnapshot} from '../../../screens/search/on-now';
import type {NativeSearchScreenProps} from '../../../screens/search/SearchScreen';

const noSubscribe = () => () => {};
const noMenu = () => 0;
const noLiveTV = () => false;
const emptyLive: NativeSearchOnNowSnapshot = Object.freeze({phase: 'idle', cards: [], at: 0});
const noLive = () => emptyLive;

export function useSearch({destination, runtime, explore, activation, actions, onNow}: Pick<NativeSearchScreenProps, 'destination' | 'runtime' | 'explore' | 'activation' | 'actions' | 'onNow'>) {
  useSyncExternalStore(actions.menuState?.subscribe ?? noSubscribe, actions.menuState?.getSnapshot ?? noMenu);
  const state = useSyncExternalStore(destination.subscribe, destination.getSnapshot);
  const landing = useSyncExternalStore(explore.subscribe, explore.getSnapshot);
  const active = useSyncExternalStore(activation.subscribe, activation.getSnapshot);
  const groups = useMemo(() => orderedSearchGroups(state.groups), [state.groups]);
  const t = runtime.text.t, typed = !!state.text.trim();
  const empty = typed && state.search.phase === 'ready' && groups.every(group => group.status === 'success' && group.totalCount === 0);
  const emptyAll = empty && state.group === 'all', emptyIn = empty && state.group !== 'all' ? state.group : undefined;
  const top = state.group === 'all' ? state.search.data?.topResult : undefined;
  const problem = state.problem === 'long' ? t('web.search.tooLong') : state.problem === 'words' ? t('web.search.tooManyWords') : state.problem ? t('web.search.tooShort') : undefined;
  const capabilities = state.search.data?.capabilities ?? state.capabilities;
  const chips = SEARCH_CHIPS.filter(chip => chip.id === 'all' || capabilities?.groups.some(group => group.id === chip.id && group.available))
    // tv.html#f5: a chip shows only for a type with results. The server leaves out a type that matched nothing, so once
    // the results are in, a type it did not send has no chip either.
    .filter(chip => chip.id === 'all' || state.group !== 'all' || (state.search.phase === 'ready'
      ? groups.some(group => group.id === chip.id && (group.status !== 'success' || group.totalCount > 0))
      : !groups.some(group => group.id === chip.id && group.status === 'success' && group.totalCount === 0)));
  const hasLive = useSyncExternalStore(onNow ? runtime.subscribe : noSubscribe, onNow ? () => runtime.getSnapshot().hasLiveTV : noLiveTV);
  const live = useSyncExternalStore(onNow?.subscribe ?? noSubscribe, onNow?.getSnapshot ?? noLive);
  const [now, setNow] = useState(() => serverNow());
  const showLive = !!onNow && hasLive && !typed, due = live.until !== undefined && now >= live.until;
  const liveNow = due ? Math.max(live.at, live.until! - 1) : now;
  useEffect(() => {
    if (active.visible && showLive && onNow) void onNow.load().catch(runtime.reportError);
  }, [active.visible, due, onNow, runtime.reportError, showLive]);
  useEffect(() => {
    if (!active.visible || !showLive) return;
    setNow(serverNow());
    const clock = repeat(async () => {setNow(serverNow());}, {everyMs: 30_000, backoff: {baseMs: 30_000, maxMs: 30_000, jitter: 'none'}, immediate: false});
    return () => clock.stop();
  }, [active.visible, showLive]);
  return {t, state, landing, active, groups, typed, empty, emptyAll, emptyIn, top, problem, chips, live, liveNow, showLive,
    chipsDrawn: typed && !emptyAll && !problem && !!capabilities};
}
