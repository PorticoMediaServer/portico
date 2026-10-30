import {useCallback, useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore} from 'react';
import {useLocation, useSearch} from '@tanstack/react-router';
import {defaultI18n} from '@i18n';
import {dayStarts, localDayStart, legacyChannel, legacyChannelRow, legacyProgram, pxPerMsFor, type GuideChannelRow, type GuideProgram} from '@core/guide/index.ts';
import {dayPrimeTime} from '@core/channel-guide.ts';
import type {GuideSourceSummaries} from '@core/guide/directory.ts';
import type {LegacyGuideOptions} from '@core/guide/legacy-source.ts';
import {dependsOn} from '@core/freshness-hub.ts';
import {useQuietRefresh} from '../../platform/freshness';
import {defineWebScreen} from '../../route-registry';
import {useProtectedWebScreenContext} from '../../route-registry-view';
import {router} from '../../router';
import {ToastHost, type ToastData} from '../../kit/overlays';
import {StateView} from '../../kit/states';
import {useWindowHeight} from '../../kit/theme';
import {useShellWidth} from '../../shell/viewport';
import {useCommittedLibraryScreenContext, type ProtectedLibraryScreen} from '../browse/screen-context';
import {useScreenAppearance} from '../browse/screen-preferences';
import type {ScreenContext} from '../detail/context';
import {SeriesRulesEntry} from './SeriesRulesEntry';
import {ScreenProblem} from '../detail/ScreenProblem';
import {ProgrammePanel} from './ProgrammePanel';
import {RecordingsScreen} from './RecordingsScreen';
import {parseLiveScreenSearch, type LiveScreenSearch} from './route-search';
import {liveProgramWindow} from './program-window';
import {useGuideViewPreference} from './guide-view-preference';
import {LiveOnNow} from './OnNow';
import {GuideScreen, type GuideRowData} from './GuideScreen';
import {guideChannelEditorDestination, guideChannelMenuEntries} from './channel-menu';
import {useLiveLease, type LiveLease} from './useLiveLease';
import type {RowWitness, SelectionAdmission} from './row-witness';
import styles from './live.module.css';

const t = defaultI18n.t, halfHour = 30 * 60_000;
const liveDependencies = [dependsOn.list('guide'), dependsOn.list('liveSources'), dependsOn.list('libraryChannels'), dependsOn.list('myRecordings'), dependsOn.personal(), dependsOn.event('stream.resync')];
const nowStart = () => Math.floor(Date.now() / halfHour) * halfHour;
type Selected = Readonly<{lease: LiveLease; channel: GuideChannelRow; programme?: GuideProgram; admission: SelectionAdmission}>;
type ReadAttempt = Readonly<{current: () => boolean; retire: () => void}>;
type CommittedSelection = {selection: Selected; live: boolean; admission: SelectionAdmission};
const noSubscribe = () => () => {};
const emptySnapshot = Object.freeze({version: 0, channelsError: false});
const noSnapshot = () => emptySnapshot;
const noFailure = () => undefined;

/** The guide page a row was admitted through: its own source and provenance,
 * never a newer row with the same id. */
function admittedChannel(options: LegacyGuideOptions, row: GuideChannelRow) {
  const channel = legacyChannel(row);
  if (!channel) return undefined;
  if (options.kind !== 'all' && channel.provenance !== options.kind) return undefined;
  if (options.sourceId && channel.sourceId !== options.sourceId) return undefined;
  return channel;
}
function playableChannel(options: LegacyGuideOptions, row: GuideChannelRow) {
  const channel = admittedChannel(options, row);
  return channel && channel.tuneAvailable && !channel.tuneUnavailableReason ? channel : undefined;
}
/** This guide page's own membership witness: the same row object at the same
 * row index, in the store that drew it. A replacement page has other objects. */
function guideWitness(lease: LiveLease, index: number, channel: GuideChannelRow): RowWitness {
  return {row: channel, current: () => lease.live && lease.life.current() && lease.routeCurrent() && lease.bindings.store.channelAt(index) === channel};
}
/** The exact source/channel/kind a deep link asked to read. */
const linkKey = (sourceId?: string, channelId?: string, kind?: string) => JSON.stringify([sourceId ?? '', channelId ?? '', kind ?? '']);
function currentLink() {
  const value = router.state.location.search as unknown as Record<string, unknown>;
  return linkKey(typeof value.sourceId === 'string' ? value.sourceId : undefined,
    typeof value.channelId === 'string' ? value.channelId : undefined, typeof value.kind === 'string' ? value.kind : undefined);
}

function BoundLive({captured}: {captured: ProtectedLibraryScreen}) {
  const search = parseLiveScreenSearch(useSearch({from: '/live'}));
  const pathname = useLocation({select: location => location.pathname});
  const context = useCommittedLibraryScreenContext(captured, pathname, () => router.state.location.pathname === pathname);
  return context ? <LiveArea captured={captured} context={context} search={search} pathname={pathname}/> : null;
}

function LiveArea({captured, context, search, pathname}: {captured: ProtectedLibraryScreen; context: ScreenContext; search: LiveScreenSearch; pathname: string}) {
  const width = useShellWidth(), height = useWindowHeight();
  const appearance = useScreenAppearance(captured.context)!;
  const timezone = defaultI18n.region.timeZone ?? Intl.DateTimeFormat().resolvedOptions().timeZone;
  const owner = context.identity?.viewer.role === 'owner';
  const key = JSON.stringify({kind: search.kind ?? 'all', sourceId: search.sourceId ?? '', timezone, group: search.group, favorites: search.favorites,
    search: search.q, sort: search.sort ?? 'number', ...(owner && search.hidden ? {includeServerHidden: true} : {})} satisfies LegacyGuideOptions);
  const runtime = captured.binding.playback?.runtime, routeCurrent = () => router.state.location.pathname === pathname;
  const [reopen, setReopen] = useState(0);
  const lease = useLiveLease(context, key, pathname, routeCurrent, runtime, reopen);
  const store = lease?.bindings.store;
  const snapshot = useSyncExternalStore(store?.subscribe ?? noSubscribe, store?.getSnapshot ?? noSnapshot, store?.getSnapshot ?? noSnapshot);
  const failure = lease?.bindings.failure;
  const authorityFailure = useSyncExternalStore(failure?.subscribe ?? noSubscribe, failure?.getSnapshot ?? noFailure, failure?.getSnapshot ?? noFailure);
  const [summary, setSummary] = useState<{lease: LiveLease; value: GuideSourceSummaries}>();
  const [failed, setFailed] = useState<LiveLease>(), [attempt, setAttempt] = useState(0);
  useEffect(() => {
    if (!lease) return;
    let current = true;
    void lease.bindings.sources().then(value => {if (current && lease.life.current()) {setSummary({lease, value});setFailed(undefined);}})
      .catch(() => {if (current && lease.life.current()) {setSummary(undefined);setFailed(lease);}});
    return () => {current = false;};
  }, [lease, attempt]);
  const searchKey = JSON.stringify(search);
  const navigate = useCallback((change: Partial<LiveScreenSearch>) => {
    const filtersChanged = ['sourceId', 'kind', 'group', 'favorites', 'q', 'hidden'].some(key => Object.hasOwn(change, key));
    if (context.isCurrent()) void router.navigate({to: '/live', search: {...JSON.parse(searchKey), ...(filtersChanged ? {channelId: undefined} : {}), ...change}, replace: true, resetScroll: false}).catch(context.effects.reportError);
  }, [context, searchKey]);
  const [start, setStart] = useState(nowStart), [now, setNow] = useState(Date.now), [selected, setSelected] = useState<Selected>();
  const [committedSelection, setCommittedSelection] = useState<CommittedSelection>();
  const committedSelectionRef = useRef<CommittedSelection | undefined>(undefined);
  const reads = useRef<ReadAttempt | undefined>(undefined);
  const selectRow = useCallback((selection: Selected) => {
    // A row choice replaces any deep-link read still in flight.
    reads.current?.retire();reads.current = undefined;setSelected(selection);
  }, []);
  // Publish the exact committed selection. Its cleanup retires only the
  // admission of the selection it published; a successor owns its own.
  useLayoutEffect(() => {
    if (!selected || !lease || selected.lease !== lease) return;
    // A stable admission for every retained panel command: the original row
    // admission plus this exact committed selection's own live flag.
    const committed: CommittedSelection = {selection: selected, live: true,
      admission: {row: selected.channel, current: () => committed.live && selected.admission.current()}};
    committedSelectionRef.current = committed;
    setCommittedSelection(committed);
    return () => {
      committed.live = false;
      if (committedSelectionRef.current === committed) committedSelectionRef.current = undefined;
      selected.admission.retire?.();
    };
  }, [selected, lease]);
  const closeSelection = (target: Selected) => {
    const committed = committedSelectionRef.current;
    if (!committed?.live || committed.selection !== target) return;
    // Consume before dispatch so a retained same-tick callback refuses.
    committedSelectionRef.current = undefined;committed.live = false;
    setSelected(current => current === target ? undefined : current);
  };
  useEffect(() => {const timer = setInterval(() => setNow(Date.now()), 30_000);return () => clearInterval(timer);}, []);
  useLayoutEffect(() => {
    if (!lease || !search.sourceId || !search.channelId || search.kind !== 'live-source' && search.kind !== 'library-channel') return;
    const request = new AbortController(), link = linkKey(search.sourceId, search.channelId, search.kind);
    // The deep-link read owns a local admission for this exact link. Layout
    // retirement, a newer read, a row choice, or a cleared selection retires
    // it; the admission also checks the current route search, so a same-path
    // replacement never keeps the old panel's commands alive until passive
    // cleanup. Its result can never replace a successor.
    let attemptLive = true;
    const attempt: ReadAttempt = {current: () => attemptLive && link === currentLink() && lease.live && lease.life.current() && lease.routeCurrent(),
      retire: () => {attemptLive = false;}};
    reads.current?.retire();reads.current = attempt;
    void lease.bindings.selectedChannel({sourceId: search.sourceId, channelId: search.channelId, provenance: search.kind}, request.signal).then(channel => {
      if (!channel || request.signal.aborted || !attempt.current()) return;
      const time = Date.now(), programme = channel.programmes.find(programme => Date.parse(programme.start) <= time && time < Date.parse(programme.end)) ?? channel.programmes[0];
      const row = legacyChannelRow(channel);
      setSelected({lease, channel: row, ...(programme ? {programme: legacyProgram(programme)} : {}), admission: {row, current: attempt.current, retire: attempt.retire}});
    }).catch(error => {if (!request.signal.aborted && attempt.current()) lease.screen.effects.reportError(error);});
    return () => {attempt.retire();request.abort();if (reads.current === attempt) reads.current = undefined;};
  }, [lease, search.sourceId, search.channelId, search.kind]);
  const phone = width < 600, landscape = width >= 600 && height < 600 && width > height, column = phone ? 60 : width < 1024 || landscape ? 180 : 284, rowHeight = phone ? 72 : width < 1024 || landscape ? 62 : 76;
  const scale = pxPerMsFor(width < 1024 || landscape ? 90 : 240), end = start + Math.max(1, width - column) / scale;
  const {view, remember} = useGuideViewPreference({...context.scope, kind: search.kind ?? 'all', sourceId: search.sourceId ?? ''}, search.view, context.isCurrent);
  const {start: readStart, end: readEnd} = liveProgramWindow(view, start, end, now);
  // The guide body scrolls with the page; the loaded window is the visible rows.
  const [visible, setVisible] = useState({first: 0, last: Math.max(0, Math.ceil(640 / rowHeight))});
  const onVisibleWindow = useCallback((first: number, last: number) => setVisible(old => old.first === first && old.last === last ? old : {first, last}), []);
  const first = visible.first, last = visible.last;
  useEffect(() => {if (lease) lease.bindings.store.setViewport({firstRow: first, lastRow: last, start: readStart, end: readEnd});}, [lease, first, last, readStart, readEnd]);
  useEffect(() => {
    if (!lease || !search.channelId || selected?.lease === lease) return;
    for (let index = first; index <= last; index++) {
      const channel = lease.bindings.store.channelAt(index);
      if (channel?.id !== search.channelId || !admittedChannel(lease.options, channel)) continue;
      const programme = lease.bindings.store.programsFor(index, readStart, readEnd).programs.find(programme => programme.start <= now && now < programme.end);
      if (programme) selectRow({lease, channel, programme, admission: guideWitness(lease, index, channel)});
      return;
    }
  }, [lease, snapshot.version, search.channelId, selected, first, last, readStart, readEnd, now, selectRow]);
  // Drawn for the committed page even when a row witness has since retired;
  // every retained action checks that exact admission again.
  const currentSelection = committedSelection?.live && committedSelection.selection === selected && lease && committedSelection.selection.lease === lease
    && lease.live && lease.routeCurrent() && committedSelection.admission.row === committedSelection.selection.channel ? committedSelection : undefined;
  useQuietRefresh('live-guide', async refresh => {
    if (!lease) return;
    // Only the committed selection that existed when this refresh began may be
    // retired when it ends. A row or deep link opened while it awaited stays.
    const originalCommitted = committedSelectionRef.current, originalRead = reads.current;
    await refresh.settle();
    if (refresh.signal.aborted || !lease.life.current()) return;
    const value = await lease.bindings.refresh(refresh.signal);
    if (!refresh.signal.aborted && lease.life.current()) {
      // A deep link that was already pending when this refresh began cannot
      // publish after it. A read opened while we awaited is not touched.
      if (originalRead && reads.current === originalRead) {originalRead.retire();reads.current = undefined;}
      if (originalCommitted && committedSelectionRef.current === originalCommitted) {
        committedSelectionRef.current = undefined;originalCommitted.live = false;
        setSelected(current => current === originalCommitted.selection ? undefined : current);
      }
      setSummary({lease, value});setFailed(undefined);
    }
  }, () => !!lease && summary?.lease === lease, {dependencies: liveDependencies, everyMs: 0});
  const onChanged = useCallback(() => {
    if (!lease || !lease.life.current() || !lease.routeCurrent()) return;
    lease.bindings.store.invalidate();
    setAttempt(value => value + 1);
  }, [lease]);
  // One channel preference at a time: the next one is made against the re-read row's revision.
  const [preferenceBusy, setPreferenceBusy] = useState<LiveLease>();
  const channelPreference = (channel: GuideChannelRow, witness: RowWitness | undefined, values: Readonly<{favorite: boolean; hidden: boolean}>) => {
    if (!lease || preferenceBusy === lease || !witness || witness.row !== channel || !witness.current() || !admittedChannel(lease.options, channel)) return;
    setPreferenceBusy(lease);
    void lease.bindings.channelPreference(channel, values).then(onChanged, lease.screen.effects.reportError)
      .finally(() => {if (lease.life.current()) setPreferenceBusy(current => current === lease ? undefined : current);});
  };
  const [visibilityToast, setVisibilityToast] = useState<{lease: LiveLease; toast: ToastData}>();
  const visibilityBusy = useRef(false);
  const channelHidden = (channel: GuideChannelRow, hidden: boolean, witness?: RowWitness, expected?: Readonly<{revision: number; hidden: boolean}>) => {
    if (!lease || !owner || visibilityBusy.current || !lease.life.current() || !lease.routeCurrent() || (!expected && (!witness || witness.row !== channel || !witness.current()))) return;
    visibilityBusy.current = true;setPreferenceBusy(lease);
    void lease.bindings.channelHidden(channel, hidden, expected).then(receipt => {
      if (!lease.life.current() || !lease.routeCurrent()) return;
      onChanged();
      setVisibilityToast({lease, toast: {id: crypto.randomUUID(), message: t(hidden ? 'channels.hiddenNotice' : 'channels.shownNotice'),
        action: {label: t('action.undo'), onAction: () => channelHidden(channel, receipt.previousHidden, undefined, receipt)}}});
    }, lease.screen.effects.reportError).finally(() => {visibilityBusy.current = false;if (lease.life.current()) setPreferenceBusy(current => current === lease ? undefined : current);});
  };
  const editChannel = (channel: GuideChannelRow) => {
    if (context.isCurrent()) void router.navigate(guideChannelEditorDestination(channel.id)).catch(context.effects.reportError);
  };
  const openRecordings = useCallback((target: Selected) => {
    // An old panel's callback cannot retire or navigate away from a successor.
    const committed = committedSelectionRef.current;
    if (!committed?.live || committed.selection !== target) return;
    if (!target.lease.live || !target.lease.life.current() || !target.lease.routeCurrent()
      || target.admission.row !== target.channel || !target.admission.current()) return;
    // Consume before dispatch so a retained same-tick callback refuses.
    committedSelectionRef.current = undefined;committed.live = false;
    setSelected(current => current === target ? undefined : current);
    if (context.isCurrent()) void router.navigate({to: '/live', search: {...JSON.parse(searchKey), tab: 'recordings', dvr: 'upcoming'}, replace: true, resetScroll: false}).catch(context.effects.reportError);
  }, [context, searchKey]);
  if (!lease) return null;
  if (authorityFailure) return <ScreenProblem context={lease.screen} error={authorityFailure} retry={() => {if (context.isCurrent()) setReopen(value => value + 1);}}/>;
  if (!lease.live || !lease.life.current() || !lease.routeCurrent()) return null;
  const play = (channel: GuideChannelRow, admission?: SelectionAdmission) => {
    const actual = playableChannel(lease.options, channel), playback = lease.playbackOwner;
    if (!actual || !playback || !admission || admission.row !== channel) return;
    const current = () => lease.live && lease.life.current() && lease.routeCurrent() && admission.row === channel
      && admission.current() && !!playableChannel(lease.options, channel);
    if (!current()) return;
    void playback.playFromGuide(actual, {signal: lease.life.signal(), isCurrent: current}).catch(lease.screen.effects.reportError);
  };
  const onPlay = lease.playbackOwner ? play : undefined;
  const sources = summary?.lease === lease ? summary.value.sources : undefined;
  const selectedSources = sources?.filter(source => (!search.sourceId || source.id === search.sourceId) && (!search.kind || search.kind === 'all' || source.provenance === search.kind)) ?? [];
  const groupCounts = new Map<string, number>();
  // Source summaries partition channels. Their published counts are combined,
  // never calculated from the loaded guide page or a lineup scan.
  for (const source of selectedSources) for (const group of source.groupCounts) groupCounts.set(group.name, (groupCounts.get(group.name) ?? 0) + group.count);
  const groups = [...groupCounts].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]));
  const ends = selectedSources.map(source => Date.parse(source.availableEnd));
  const through = ends.length && ends.every(value => Number.isFinite(value)) ? Math.min(...ends) : undefined;
  const days = through === undefined ? [] : dayStarts(now, through, timezone);
  const throughDay = through === undefined ? undefined : new Intl.DateTimeFormat(defaultI18n.region.formatLocale, {weekday: 'long', timeZone: timezone}).format(through - 1);
  const hasSources = !!sources?.length;
  const total = snapshot.total ?? Math.max(1, last + 1);
  const rows: GuideRowData[] = [];
  for (let index = first; index <= last; index++) {
    const channel = lease.bindings.store.channelAt(index), page = lease.bindings.store.programsFor(index, readStart, readEnd);
    rows.push({index, ...(channel ? {channel, witness: guideWitness(lease, index, channel)} : {}), programs: page.programs, complete: page.complete, failed: page.failed});
  }
  const favoriteCount = selectedSources.reduce((sum, source) => sum + source.favoriteCount, 0);
  const chips = [
    {id: 'all', label: t('channels.filterAll'), count: snapshot.total, selected: !search.group && !search.favorites, onSelect: () => navigate({group: undefined, favorites: undefined})},
    {id: 'favorites', label: t('channels.favorites'), favorites: true, count: favoriteCount, selected: search.favorites === true, onSelect: () => navigate({favorites: !search.favorites || undefined, group: undefined})},
    ...groups.map(([group, count]) => ({id: `group:${group}`, label: group, count, selected: search.group === group, onSelect: () => navigate({group, favorites: undefined})})),
    ...(owner ? [{id: 'hidden', label: t('channels.showHidden'), selected: search.hidden === true, onSelect: () => navigate({hidden: !search.hidden || undefined})}] : []),
  ];
  const sourceOptions = [{value: 'all', label: t('channels.all')},
    ...(sources ?? []).map(source => ({value: JSON.stringify(['source', source.provenance, source.id]), label: source.provenance === 'library-channel' ? t('channels.custom.name') : source.name}))];
  const switchSource = (value: string) => {
    const source = sources?.find(source => JSON.stringify(['source', source.provenance, source.id]) === value);
    if (source) navigate({sourceId: source.id, kind: source.provenance, channelId: undefined, group: undefined});
    else if (value === 'all') navigate({sourceId: undefined, kind: undefined, channelId: undefined, group: undefined});
  };
  const sourceName = search.sourceId ? selectedSources[0]?.provenance === 'library-channel' ? t('channels.custom.name') : selectedSources[0]?.name : t('channels.all');
  return <>
    {failed === lease ? <div className={styles.guideState}><StateView title={t('next.states.pageFailed')} body={t('next.states.pageFailedBody')} primary={{id: 'retry', label: t('next.states.tryAgain'), onPress: () => {if (lease.life.current() && lease.routeCurrent()) setAttempt(value => value + 1);}}}/></div>
      : sources && !sources.length && !owner ? <div className={styles.guideState}><StateView title={t('channels.noSourcesTitle')} body={t(context.identity?.viewer.role === 'owner' ? 'channels.noSourcesOwner' : 'channels.noSourcesMember')}/></div>
        : <GuideScreen title={t('next.home.shell.live')}
          switcher={hasSources ? {value: search.sourceId ? JSON.stringify(['source', selectedSources[0]?.provenance, search.sourceId]) : 'all', options: sourceOptions, onChange: switchSource} : undefined}
          summary={hasSources ? {name: sourceName ?? t('channels.all'), ...(snapshot.total !== undefined ? {channels: snapshot.total} : {}), ...(throughDay ? {through: t('next.live.guideThrough', {day: throughDay})} : {})} : undefined}
          onTabChange={() => navigate({tab: 'recordings'})}
          chips={chips}
          onNow={width >= 1024 && hasSources ? <LiveOnNow owner={lease.bindings} now={now} timezone={timezone} artwork={context.artwork} reducedMotion={appearance.reducedMotion}
            onOpen={(channel, programme, witness) => {if (!witness || witness.row !== channel || !witness.current() || !admittedChannel(lease.options, channel)) return;selectRow({lease, channel, programme, admission: witness});}}
            onPlay={onPlay}/> : undefined}
          day={{label: t('guide.day'), value: String(localDayStart(start, timezone)),
            options: days.map(day => ({value: String(day), label: day === localDayStart(now, timezone) ? t('guide.today') : new Intl.DateTimeFormat(defaultI18n.region.formatLocale, {weekday: 'long', timeZone: timezone}).format(day)})),
            onChange: value => {const day = days.find(day => String(day) === value);if (day !== undefined) setStart(dayPrimeTime(day, timezone));}}}
          sort={{value: search.sort ?? 'number', options: [{value: 'number', label: t('channels.sortNumber')}, {value: 'name', label: t('channels.sortName')}, {value: 'favorites', label: t('channels.favorites')}],
            onChange: sort => {if (sort === 'number' || sort === 'name' || sort === 'favorites') navigate({sort});}}}
          problem={snapshot.channelsError ? <StateView variant="page" title={t('guide.tileFailed')} primary={{id: 'retry', label: t('next.states.tryAgain'), onPress: () => {if (lease.life.current()) lease.bindings.store.retry();}}}/> : undefined}
          empty={sources && !sources.length ? <StateView variant="page" title={t('channels.noSourcesTitle')} body={t('web.live.noSourcesOwner')}
            primary={{id: 'settings', label: t('web.live.setUpChannels'), onPress: () => {if (context.isCurrent()) void router.navigate({to: '/settings', search: {scope: 'server', section: 'live'}});}}}/> : snapshot.total === 0 ? <StateView variant="page" title={t('channels.filterEmptyTitle', {filter: search.group ?? (search.favorites ? t('channels.favorites') : search.q ?? t('channels.all'))})}
            primary={{id: 'all', label: t('channels.showAll'), onPress: () => navigate({group: undefined, favorites: undefined, q: undefined})}}/> : undefined}
          view={view} onViewChange={view => {if (context.isCurrent()) {remember(view);navigate({view});}}}
          time={{earlier: t('guide.earlier'), now: t('guide.now'), later: t('guide.later'), onEarlier: () => setStart(value => value - 2 * 60 * 60_000), onNow: () => setStart(nowStart()), onLater: () => setStart(value => value + 2 * 60 * 60_000)}}
          span={{start, end, now, scale, column, rowHeight, timezone}}
          rows={rows} total={total}
          selected={currentSelection ? {channelId: currentSelection.selection.channel.id, ...(currentSelection.selection.programme ? {programmeId: currentSelection.selection.programme.id} : {})} : undefined}
          onSelect={(channel, programme, witness) => {if (!witness || witness.row !== channel || !witness.current() || !admittedChannel(lease.options, channel)) return;selectRow({lease, channel, ...(programme ? {programme} : {}), admission: witness});}}
          onPlay={onPlay}
          channelMenu={(channel, witness) => guideChannelMenuEntries(channel, {owner, busy: preferenceBusy === lease,
            onFavorite: row => channelPreference(row, witness, {favorite: !row.favorite, hidden: legacyChannel(row)?.hidden ?? false}),
            onHidden: (row, hidden) => channelHidden(row, hidden, witness),
            onEdit: editChannel})}
          onVisibleWindow={onVisibleWindow} onRetry={() => {if (lease.life.current()) lease.bindings.store.retry();}}/>}
    {currentSelection && <ProgrammePanel key={JSON.stringify([currentSelection.selection.lease.id, currentSelection.selection.channel.sourceId, currentSelection.selection.channel.id, currentSelection.selection.programme?.id, currentSelection.selection.programme?.start])} context={lease.screen} admission={currentSelection.admission} channel={currentSelection.selection.channel} programme={currentSelection.selection.programme}
      now={now} timezone={timezone} open onClose={() => closeSelection(currentSelection.selection)}
      onWatch={onPlay ? () => onPlay(currentSelection.selection.channel, currentSelection.admission) : undefined} onChanged={onChanged} onViewRecordings={() => openRecordings(currentSelection.selection)}/>}
  <ToastHost toast={visibilityToast?.lease === lease && lease?.life.current() && lease.routeCurrent() ? visibilityToast.toast : null} onDismiss={() => setVisibilityToast(undefined)}/></>;
}
function LiveRouteScreen() {
  const captured = useProtectedWebScreenContext();
  const search = parseLiveScreenSearch(useSearch({from: '/live'}));
  const openView = (dvr: 'upcoming' | 'recorded' | 'rules' | 'history' | 'storage') => {
    if (captured) void router.navigate({to: '/live', search: {...search, tab: 'recordings', dvr}, replace: true, resetScroll: false}).catch(() => {});
  };
  const openGuide = () => {if (captured) void router.navigate({to: '/live', search: {...search, tab: 'guide'}, replace: true, resetScroll: false}).catch(() => {});};
  if (!captured) return null;
  if (search.tab !== 'recordings') return <BoundLive captured={captured}/>;
  const view = search.dvr ?? 'upcoming';
  if (view === 'rules') return <SeriesRulesEntry captured={captured} onView={openView}/>;
  return <RecordingsScreen captured={captured} view={view === 'recorded' || view === 'history' || view === 'storage' ? view : 'upcoming'} onView={openView} onGuide={openGuide}/>;
}
export default defineWebScreen({id: 'live', component: LiveRouteScreen, validateSearch: parseLiveScreenSearch});
