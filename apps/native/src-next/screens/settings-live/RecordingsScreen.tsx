import React, {useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {FlatList, ScrollView, Text, View} from 'react-native';
import Svg, {Defs, LinearGradient, Rect, Stop} from 'react-native-svg';
import {defaultI18n, type MessageId} from '../../../../../packages/i18n/src';
import {activeRecording, playableRecording, type DeletePreview, type DVRView, type Recording, type RecordingDetail, type RecordingGroups, type RecordingRule, type RecordingStorage, type RuleConfig} from '../../../../../packages/client-core/src/dvr';
import {titleTintHue} from '../../../../../packages/client-core/src/presentation/title-page';
import {programmeTimeRange} from '../../../../../packages/client-core/src/presentation/kit-model';
import {parseMediaItem} from '../../../../../packages/client-core/src/media-item';
import {validateContentEntry} from '../../../../../packages/client-core/src/library-content';
import {encodedRoutePath} from '../../../../../packages/contracts/src/routes';
import {Artwork, GeneratedArt} from '../../kit/artwork';
import {Button, Chip, Focusable, Select, Switch} from '../../kit/controls';
import {FocusRegion, useFocusRegistry} from '../../kit/focus';
import {Dialog, Menu} from '../../kit/overlays';
import {Icon} from '../../kit/Icon';
import {Quiet, StateView} from '../../kit/states';
import {shellType} from '../../kit/shell/visual';
import {useTheme} from '../../kit/theme';
import type {NativeShellRuntime} from '../../shell/runtime';
import {tvFocusIds} from '../../shell/tv-focus';
import type {NativeSettingsLivePorts} from './ports';
import type {NativeRecordingsController} from './recordings-controller';
import {RecordingOverlapWindow, type OverlapSnapshot} from './recording-overlap-window';
import type {NativeLiveOwnerController} from './live-owner-controller';
import {NewRuleSheet} from './LiveOwnerRecording';
import {keepChoiceList, keepChoiceValue, keepLabel, ruleFacts, ruleKeep, ruleNext, ruleScope, withKeep} from './recording-rule-presentation';
import {Screen, ScreenText, useScreenBack, useScreenEntry} from './Screen';
import {nativeHomeEntryLifetime} from '../home/entry-lifetime';

const views: readonly DVRView[] = ['upcoming', 'recorded', 'rules', 'history', 'storage'];
const viewLabels: Record<DVRView, MessageId> = {upcoming: 'recordings.upcoming', recorded: 'recordings.recorded', rules: 'recordings.rules', history: 'web.dvr.history', storage: 'recordings.storage'};
const stateLabels: Record<Recording['state'], MessageId> = {scheduled: 'web.dvr.state.scheduled', conflicted: 'web.dvr.state.conflicted', 'waiting-source': 'web.dvr.state.waitingSource', 'waiting-guide': 'web.dvr.state.waitingGuide', preparing: 'web.dvr.state.preparing', recording: 'web.dvr.state.recording', finalizing: 'web.dvr.state.finalizing', completed: 'web.dvr.state.completed', 'incomplete-playable': 'web.dvr.state.incomplete', failed: 'web.dvr.state.failed', cancelled: 'web.dvr.state.cancelled', 'pending-delete': 'web.dvr.state.pendingDelete', deleted: 'web.dvr.state.deleted'};
const reasonLabels: Readonly<Record<string, MessageId>> = {'capture-window-missed': 'web.dvr.reason.missed', 'tuner-conflict': 'web.dvr.reason.conflict', 'storage-floor': 'web.dvr.reason.storageFloor', 'storage-cap': 'web.dvr.reason.storageCap', 'owner-cancelled': 'web.dvr.reason.cancelled', 'source-changed': 'web.dvr.reason.sourceChanged', 'guide-occurrence-missing': 'web.dvr.reason.guideMissing', 'partial-capture': 'web.dvr.reason.partial', 'physical-reader-active': 'web.dvr.reason.inUse', 'artifact-remove-unavailable': 'web.dvr.reason.removeRetry'};
/** The drawn Recorded order, as the groups reader takes it. */
const sortLabels: Readonly<Record<'recent' | 'title' | 'oldest', MessageId>> = {recent: 'next.live.sortRecent', title: 'next.live.sortTitle', oldest: 'next.live.sortOldest'};
type Group = RecordingGroups['groups'][number];
type Row = Readonly<{kind: 'heading'; id: string; title: string}> | Readonly<{kind: 'recording'; recording: Recording}>;

/** Cursor pages remain bounded server pages. Full TV paging awaits the canonical window
 * projection; this screen never scans the inventory or invents an offset/loaded-page total. */
/** The same recorded row, not merely the same title: a reload can change revision. */
const sameRecording = (open: Recording | undefined, recording: Recording) => open !== undefined && open.id === recording.id && open.revision === recording.revision;

/** The loaded view a row was opened in, without the page cursor a later load added. */
const queryKey = (query: Readonly<{view: string; cursor?: string; sort?: string; group?: string}>) => JSON.stringify([query.view, query.sort ?? '', query.group ?? '']);

/** The shell route and destination a row was opened in. */
const routeKey = (route: Readonly<{revision: number; route: {kind: string}; destination: unknown}>) => JSON.stringify([route.revision, route.route.kind, route.destination]);

/** One opening of a row's menu or sheet, created by the handler that opens it. Callbacks
 * capture this object and compare it by identity, so a late close from an earlier opening
 * can never mutate the current one. `retired` says that opening may no longer act. */
type OverlayOpening = {kind: 'menu' | 'details'; recording: Recording; query: string; route: string; scope: object; pin: object; retired: boolean};

/** The delete ask started from one opening, holding that exact opening. */
type DeleteAsk = Readonly<{opening: OverlayOpening; controller: NativeRecordingsController; recording: Recording}>;

export function NativeRecordingsScreen({controller, ports, runtime, onGuide, owner}: {controller: NativeRecordingsController; ports: Pick<NativeSettingsLivePorts, 'context'>; runtime: NativeShellRuntime; onGuide: () => void; owner?: NativeLiveOwnerController}) {
  const theme = useTheme(), t = defaultI18n.t;
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot), query = state.query;
  const ids = useMemo(() => tvFocusIds(controller.lifetime.authority), [controller]), tabs = ids.surface('recordings.views'), body = ids.surface('recordings.page');
  const registry = useFocusRegistry(), bodyFocused = useRef(false);
  const [group, setGroup] = useState<Group>(), [menu, setMenu] = useState<OverlayOpening>(), [detail, setDetail] = useState<OverlayOpening>(), [rule, setRule] = useState<RecordingRule>();
  const [confirm, setConfirm] = useState<Readonly<{recording: Recording; preview?: DeletePreview; opening: OverlayOpening}>>(), [newRule, setNewRule] = useState(false), [sortOpen, setSortOpen] = useState(false);
  const opener = useRef<View | null>(null), rows = useRef(new Map<string, View>()), cancel = useRef<View>(null);
  const list = useRef<FlatList<Row>>(null), alive = useRef(true), scrollIntent = useRef(false);
  const playOperations = useRef(new Set<AbortController>()), viewerOpening = useRef({}).current;
  const navigation = ports.context.navigation;
  const route = useSyncExternalStore(navigation.subscribe, navigation.getSnapshot);
  // The presented menu or sheet, and the delete ask started from one of them. A
  // render-time change of controller, view, route or visibility retires the ask before
  // the new scope draws, so a hidden or retained destination cannot settle one.
  const overlay = useRef<OverlayOpening | undefined>(undefined), ask = useRef<DeleteAsk | undefined>(undefined);
  const ruleOpening = useRef<Readonly<{id: string; revision: number; scope: object; pin: object}> | undefined>(undefined);
  const scope = useRef<Readonly<{controller: NativeRecordingsController; key: string}> | undefined>(undefined);
  const scopeKey = `${queryKey(query)}|${routeKey(route)}|${navigation.isCurrent()}|${runtime.getSnapshot().contentVisible === true}`;
  const previousScope = scope.current;
  if (previousScope && (previousScope.controller !== controller || previousScope.key !== scopeKey)) ask.current = undefined;
  scope.current = {controller, key: scopeKey};
  const drawnRoute = routeKey(route), drawnQuery = queryKey(query), drawnScope = scopeKey;
  // The drawn witnesses this screen's guards read, held as a callback so a handler that closes
  // over them keeps its exact read order while the values it reads are declared.
  const drawnCurrent = useCallback(() => {
    if (!alive.current || !controller.viewerOpeningCurrent(viewerOpening) || scope.current?.controller !== controller || scope.current.key !== drawnScope || !controller.lifetime.current() || !navigation.isCurrent()) return false;
    if (runtime.getSnapshot().contentVisible !== true || routeKey(navigation.getSnapshot()) !== drawnRoute) return false;
    return queryKey(controller.getSnapshot().query) === drawnQuery && scope.current?.controller === controller && scope.current.key === drawnScope && controller.lifetime.current() && navigation.isCurrent() && routeKey(navigation.getSnapshot()) === drawnRoute;
  }, [controller, drawnQuery, drawnRoute, drawnScope, navigation, runtime, viewerOpening]);
  const confirmation = useRef<typeof confirm>(undefined); confirmation.current = confirm;
  const retire = () => {if (ask.current) controller.unpinViewerRow(ask.current.opening.pin, ask.current.opening.scope); ask.current = undefined;};
  const closeConfirm = () => {if (confirm) controller.unpinViewerRow(confirm.opening.pin, confirm.opening.scope); setConfirm(current => current === confirm ? undefined : current);};
  const recordings = state.viewerWindow ? state.viewerWindow.items.flatMap(row => row.kind === 'recording' ? [row.value] : []) : state.page?.recordings ?? [];
  const groups = state.viewerWindow ? state.viewerWindow.items.flatMap(row => row.kind === 'group' ? [row.value] : []) : state.groups?.groups ?? [];
  const rules = state.viewerWindow ? state.viewerWindow.items.flatMap(row => row.kind === 'rule' ? [row.value] : []) : state.page?.rules ?? [];
  const activeRuleOpening = ruleOpening.current;
  const first = query.view === 'recorded' && !query.group ? groups[0]?.key : query.view === 'rules' ? rules[0]?.id : (recordings.find(value => value.state === 'conflicted') ? `${recordings.find(value => value.state === 'conflicted')!.id}.resolve` : recordings[0]?.id);
  const emptyGuide = !state.loading && !state.accessDenied && state.error === undefined
    && (query.view === 'upcoming' && !recordings.length && state.page?.captureAvailable === true
      || query.view === 'recorded' && !query.group && !groups.length && state.storage?.captureAvailable === true);
  const entryRegion = first || emptyGuide ? body : tabs, entryChild = first ?? (emptyGuide ? 'recordings-open-guide' : query.view);
  const awaitingFirstPage = state.loading || !state.page && !state.groups && query.view !== 'storage';
  const enter = useScreenEntry(theme.isTV && !awaitingFirstPage ? {regionId: entryRegion, childId: entryChild} : undefined, controller.lifetime.current);
  // The asks die with the screen in the layout phase, so StrictMode's
  // mount/unmount/mount leaves them live again on the committed mount.
  useLayoutEffect(() => {const ownedOperations = playOperations.current; alive.current = true; controller.beginViewerOpening(viewerOpening); return () => {alive.current = false; controller.endViewerOpening(viewerOpening); for (const operation of ownedOperations) operation.abort(); if (ask.current) controller.unpinViewerRow(ask.current.opening.pin, ask.current.opening.scope); if (overlay.current) controller.unpinViewerRow(overlay.current.pin, overlay.current.scope); if (confirmation.current) controller.unpinViewerRow(confirmation.current.opening.pin, confirmation.current.opening.scope); if (ruleOpening.current) controller.unpinViewerRow(ruleOpening.current.pin, ruleOpening.current.scope); ask.current = undefined;};}, [controller, viewerOpening]);
  useEffect(() => {const previous = controller.getSnapshot().query; void controller.load(previous.group ? {view: 'recorded', sort: previous.sort} : previous); void controller.loadFacts();}, [controller]);
  const select = (view: DVRView) => {if (view === query.view && !query.group) return; retire(); setGroup(undefined); void controller.load({view}); list.current?.scrollToOffset({offset: 0, animated: false});};
  /** The drawn order of the Recorded grid; the groups reader does the ordering. */
  const sort = query.sort === 'title' || query.sort === 'oldest' ? query.sort : 'recent';
  const selectSort = (next: 'recent' | 'title' | 'oldest') => {setSortOpen(false); if (next === sort) return; retire(); void controller.load({view: 'recorded', sort: next});};
  const leaveGuide = useCallback(() => {if (!drawnCurrent()) return; controller.endViewerOpening(viewerOpening); onGuide();}, [controller, drawnCurrent, onGuide, viewerOpening]);
  const back = useCallback(() => {
    if (menu || detail || rule || confirm) return false;
    if (bodyFocused.current && registry.focus({regionId: tabs, childId: query.view})) return true;
    if (query.group) {ask.current = undefined; setGroup(undefined); void controller.load({view: 'recorded'}); return true;}
    ask.current = undefined;
    leaveGuide(); return true;
  }, [menu, detail, rule, confirm, registry, tabs, query.group, query.view, controller, leaveGuide]);
  useScreenBack(runtime, back, theme.isTV);
  const open = (recording: Recording, kind: 'menu' | 'details', expected: typeof state.viewerWindow) => {
    if (!drawnCurrent()) return;
    const window = controller.getSnapshot().viewerWindow;
    if (!window || window !== expected || !window.items.some(row => row.kind === 'recording' && sameRecording(row.value, recording))) return;
    retire();
    if (overlay.current) {overlay.current.retired = true; controller.unpinViewerRow(overlay.current.pin, overlay.current.scope);}
    const pin = controller.pinViewerRow(recording.id, window); if (!pin) return;
    const opening: OverlayOpening = {kind, recording, pin, query: drawnQuery, route: drawnRoute, scope: window.scope, retired: false};
    opener.current = rows.current.get(recording.id) ?? null; overlay.current = opening;
    if (kind === 'menu') setMenu(opening); else setDetail(opening);
  };
  /** A close acts only for the opening it was drawn for. The menu's own close after a
   * select ('action') keeps that opening's ask alive so its preview can still present. */
  const close = (opening: OverlayOpening, reason: 'user' | 'action') => {
    if (overlay.current !== opening) return;
    overlay.current = undefined;
    if (reason === 'user') {opening.retired = true; if (ask.current?.opening === opening) ask.current = undefined; controller.unpinViewerRow(opening.pin, opening.scope);}
    else if (ask.current?.opening !== opening) controller.unpinViewerRow(opening.pin, opening.scope);
    if (opening.kind === 'menu') setMenu(current => current === opening ? undefined : current);
    else setDetail(current => current === opening ? undefined : current);
  };
  /** The exact opening, still in the scope it opened in and visibly on screen. */
  const askReady = (opening: OverlayOpening) => {
    if (opening.retired || scope.current?.controller !== controller || !controller.lifetime.current() || !navigation.isCurrent()) return false;
    if (runtime.getSnapshot().contentVisible !== true) return false;
    if (routeKey(navigation.getSnapshot()) !== opening.route) return false;
    const snapshot = controller.getSnapshot();
    return queryKey(snapshot.query) === opening.query && snapshot.viewerWindow?.scope === opening.scope && snapshot.viewerWindow.items.some(row => row.kind === 'recording' && sameRecording(row.value, opening.recording)) && scope.current?.controller === controller && controller.lifetime.current() && navigation.isCurrent() && routeKey(navigation.getSnapshot()) === opening.route;
  };
  const run = (work: () => Promise<unknown>) => {void work().catch(error => {if (alive.current && controller.lifetime.current()) runtime.reportError(error);});};
  const play = async (recording: Recording, opening: OverlayOpening) => {
    if (!playableRecording(recording) || !askReady(opening)) return;
    const window = controller.getSnapshot().viewerWindow, originalScope = scope.current;
    if (!window || !originalScope || window.scope !== opening.scope) return;
    const pin = controller.pinViewerRow(recording.id, window); if (!pin) return;
    const current = () => alive.current && scope.current?.controller === controller && scope.current.key === originalScope.key && askReady(opening) && alive.current && scope.current?.controller === controller && scope.current.key === originalScope.key;
    const abort = new AbortController(); playOperations.current.add(abort);
    const parentAbort = () => abort.abort();
    controller.lifetime.signal.addEventListener('abort', parentAbort, {once: true});
    if (controller.lifetime.signal.aborted) abort.abort();
    const operation = nativeHomeEntryLifetime(current, [controller.subscribe, navigation.subscribe, runtime.subscribe], abort.signal);
    try {
      if (!operation.lifetime.isCurrent()) return;
      const item = await controller.lifetime.request(signal => ports.context.api.request(encodedRoutePath('get_items_id', {id: recording.itemId}), 'GET', undefined, signal).then(parseMediaItem), operation.lifetime.signal);
      if (!operation.lifetime.isCurrent() || !item.available || item.id !== recording.itemId || item.libraryId !== recording.libraryId) return;
      const display = validateContentEntry({id: item.id, libraryId: item.libraryId, kind: item.kind, title: item.title, available: item.available, year: item.year, posterUrl: item.posterUrl, backdropUrl: item.backdropUrl, overview: item.overview});
      const target = {itemId: item.id, startSeconds: item.progressSeconds};
      const entry = Object.freeze({...display, duration: item.duration, progressSeconds: item.progressSeconds, playback: target});
      if (!operation.lifetime.isCurrent()) return;
      await ports.context.playback.start({entry, target: entry.playback!, lifetime: operation.lifetime});
    } catch (error) {
      // A route, view or authority change aborts this read and its playback caller; that
      // cancellation is not a failure of the play the viewer asked for.
      if (operation.lifetime.isCurrent()) throw error;
    } finally {operation.dispose(); abort.abort(); playOperations.current.delete(abort); controller.lifetime.signal.removeEventListener('abort', parentAbort); controller.unpinViewerRow(pin, opening.scope);}
  };
  const askDelete = async (opening: OverlayOpening) => {
    // Refuse before the read as well as after it: a stale callback in a hidden destination
    // must not even ask the server for a preview.
    if (!alive.current || overlay.current !== opening || !askReady(opening)) return;
    const recording = opening.recording, current: DeleteAsk = {opening, controller, recording};
    ask.current = current;
    let preview: DeletePreview | undefined;
    try {preview = activeRecording(recording) ? undefined : await controller.deletePreview(recording);}
    catch (error) {if (ask.current === current) {ask.current = undefined; if (overlay.current !== opening) controller.unpinViewerRow(opening.pin, opening.scope);} throw error;}
    if (ask.current !== current || !askReady(opening)) {if (ask.current === current) {ask.current = undefined; if (overlay.current !== opening) controller.unpinViewerRow(opening.pin, opening.scope);} return;}
    if (!controller.getSnapshot().viewerWindow?.items.some(row => row.kind === 'recording' && sameRecording(row.value, recording))) return;
    ask.current = undefined;
    if (overlay.current === opening) {overlay.current = undefined; if (opening.kind === 'menu') setMenu(current => current === opening ? undefined : current); else setDetail(current => current === opening ? undefined : current);}
    setConfirm({recording, preview, opening});
  };
  const rowData: Row[] = [];
  if (query.view === 'upcoming') {
    const conflicts = recordings.filter(value => value.state === 'conflicted'), active = recordings.filter(value => ['preparing', 'recording', 'finalizing'].includes(value.state)), other = recordings.filter(value => value.state !== 'conflicted' && !['preparing', 'recording', 'finalizing'].includes(value.state));
    if (conflicts.length) rowData.push({kind: 'heading', id: 'conflicts', title: t('recordings.conflicts')}, ...conflicts.map(recording => ({kind: 'recording' as const, recording})));
    if (active.length) rowData.push({kind: 'heading', id: 'now', title: t('server.live.recordingNow')}, ...active.map(recording => ({kind: 'recording' as const, recording})));
    // The server owns order. Group only this bounded page; no client inventory join.
    let day = '';
    for (const recording of other) {const next = defaultI18n.formatDate(recording.start, {weekday: 'long', month: 'long', day: 'numeric'}); if (day !== next) {day = next; rowData.push({kind: 'heading', id: day, title: day});} rowData.push({kind: 'recording', recording});}
  } else rowData.push(...recordings.map(recording => ({kind: 'recording' as const, recording})));
  const restartViewer = (expected: NonNullable<typeof state.viewerWindow>) => {if (!drawnCurrent()) return; const current = controller.getSnapshot(); if (current.viewerWindow !== expected) return; const {view, sort, group} = current.query; void controller.load({view, ...(sort === undefined ? {} : {sort}), ...(group === undefined ? {} : {group})}, viewerOpening);};
  const isEmpty = !state.loading && (query.view === 'rules' ? !rules.length : query.view === 'recorded' && !query.group ? !groups.length : !recordings.length);
  const emptyTitle: MessageId = query.view === 'upcoming' ? 'recordings.noneScheduled' : query.view === 'rules' ? 'recordings.noneRules' : query.view === 'history' ? 'web.dvr.empty.history' : 'recordings.noneRecorded';
  const emptyBody: MessageId | undefined = query.view === 'upcoming' ? 'recordings.noneScheduledBody' : query.view === 'rules' ? 'recordings.noneRulesBody' : query.view === 'history' ? undefined : 'recordings.noneRecordedBody';
  const unavailable = state.page && !state.page.captureAvailable && !recordings.length && query.view === 'upcoming';
  // Local open-sheet/group state must not outlive an authoritative DVR access denial.
  if (state.accessDenied) return <Screen><StateView {...runtime.error(state.error ?? {status: 403}, onGuide, 'live')}/></Screen>;
  return <Screen style={{paddingTop: theme.isTV ? 56 : 20}}>
    <View style={{flexDirection: 'row', alignItems: 'center', height: theme.isTV ? 64 : 48, marginBottom: theme.isTV ? 24 : 16, paddingHorizontal: theme.isTV ? 0 : 20}}>
      {!theme.isTV && <Button testID="recordings-back" variant="text" icon="i-left" size="small" onPress={leaveGuide}>{t('action.back')}</Button>}
      <Text accessibilityRole="header" style={[shellType(theme.isTV ? 44 : 32, 800, 'normal', -.03), {color: theme.colors.text, flex: 1}]}>{group?.title ?? t('recordings.title')}</Text>
      {!group && state.storage && query.view !== 'storage' && <View style={{width: theme.isTV ? 360 : 160}}><StorageFacts storage={state.storage}/><Meter storage={state.storage}/></View>}
    </View>
    {!group ? <FocusRegion id={tabs} entry={query.view} orientation="horizontal" edges={{left: ids.rail, down: body}} onLayout={entryRegion === tabs ? enter : undefined} style={{flexDirection: 'row', gap: 12, marginBottom: theme.isTV ? 42 : 24, paddingHorizontal: theme.isTV ? 0 : 20}}>{views.map(view => <Chip key={view} focusId={view} testID={`recordings-view-${view}`} selected={view === query.view} onFocus={() => {if (theme.isTV) select(view);}} onPress={() => select(view)} count={view === 'upcoming' || view === 'recorded' || view === 'rules' ? state.summary?.[view] : undefined}>{t(viewLabels[view])}</Chip>)}</FocusRegion>
      : <View style={{marginBottom: 34, flexDirection: 'row', gap: 20}}><ScreenText secondary>{group.recording.channel?.name}</ScreenText><SpacedFacts text={t('recordings.showCountUnwatched', {count: group.recordings, unwatched: group.unwatched})}/></View>}
    <FocusRegion id={body} entry={first} orientation="free" edges={{left: ids.rail, up: tabs}} onFocusChange={value => {bodyFocused.current = value;}} onLayout={entryRegion === body ? enter : undefined} style={{flex: 1, paddingHorizontal: theme.isTV ? 0 : 20}}>
      {state.error !== undefined ? <StateView testID="recordings-error" {...runtime.error(state.error, () => {void controller.load(); void controller.loadFacts();}, 'live')}/>
        : query.view === 'storage' ? state.storage && <StorageView storage={state.storage} owner={ports.context.account.account.role === 'owner' && ports.context.account.account.primaryProfileId === controller.lifetime.authority.profileId}/>
        : state.loading && !state.page && !state.groups ? <Quiet><StateView title={t('recordings.loading')}/></Quiet>
        : unavailable ? <StateView title={t('recordings.unavailableTitle')} body={t('recordings.unavailableBody')}/>
        : isEmpty ? <StateView testID="recordings-empty" title={t(emptyTitle)} body={emptyBody && t(emptyBody)} primaryAction={emptyGuide ? {id: 'guide', testID: 'recordings-open-guide', label: t('web.dvr.openGuide'), onPress: leaveGuide} : undefined}/>
        : query.view === 'recorded' && !query.group ? <View style={{flex: 1}}>{theme.isTV && state.summary && <View style={{flexDirection: 'row', alignItems: 'center', gap: 32, marginBottom: 24}}>
          <Text style={[shellType(24, 600, 'normal'), {color: theme.colors.textSecondary, flex: 1}]}>{t('next.live.recordedHead', {shows: state.summary.groups, recordings: state.summary.recorded, unwatched: state.summary.unwatched})}</Text>
          <Chip focusId="recordings-sort" testID="recordings-sort" onPress={() => setSortOpen(true)}>{`${t('next.browse.sort')} · ${t(sortLabels[sort])}`}</Chip>
        </View>}<FlatList onScrollBeginDrag={() => {if (drawnCurrent()) scrollIntent.current = true;}} onEndReached={() => {if (drawnCurrent() && scrollIntent.current && state.viewerWindow && state.viewerWindow.error === undefined && !state.viewerWindow.refreshRequired) {scrollIntent.current = false; void controller.nextViewerPage(state.viewerWindow, viewerOpening);}}} onEndReachedThreshold={0.3} key={`groups:${theme.isTV ? 4 : theme.width >= 700 ? 3 : 2}`} data={groups} numColumns={theme.isTV ? 4 : theme.width >= 700 ? 3 : 2} keyExtractor={value => value.key} columnWrapperStyle={{gap: theme.isTV ? 28 : 16}} contentContainerStyle={{gap: theme.isTV ? 36 : 24, paddingBottom: 90}} renderItem={({item, index}) => {let lease = controller.viewerFocusToken(item.key); return <RecordingCard value={item} runtime={runtime} onFocus={() => {if (drawnCurrent() && state.viewerWindow) {lease = controller.focusViewerRow(item.key, state.viewerWindow); if (index >= groups.length - 2 && state.viewerWindow.error === undefined) void controller.nextViewerPage(state.viewerWindow, viewerOpening);}}} onBlur={() => {if (state.viewerWindow && lease) controller.blurViewerRow(item.key, lease, state.viewerWindow);}} onOpen={() => {if (!drawnCurrent()) return; const window = state.viewerWindow; if (!window || controller.getSnapshot().viewerWindow !== window || !window.items.some(row => row.kind === 'group' && row.value.key === item.key)) return; setGroup(item); retire(); void controller.load({view: 'recorded', group: item.key, sort});}}/>;}}/>{theme.isTV && <ListFade/>}</View>
        : query.view === 'rules' ? <FlatList onScrollBeginDrag={() => {if (drawnCurrent()) scrollIntent.current = true;}} onEndReached={() => {if (drawnCurrent() && scrollIntent.current && state.viewerWindow && state.viewerWindow.error === undefined && !state.viewerWindow.refreshRequired) {scrollIntent.current = false; void controller.nextViewerPage(state.viewerWindow, viewerOpening);}}} onEndReachedThreshold={0.3} key="rules" data={rules} keyExtractor={value => value.id} contentContainerStyle={{gap: theme.isTV ? 14 : 12, paddingBottom: 90}} ListHeaderComponent={<View style={{flexDirection: 'row', alignItems: 'center', gap: 20, marginBottom: theme.isTV ? 6 : 4}}><Text style={[shellType(theme.isTV ? 22 : 14), {color: theme.colors.textTertiary, flex: 1}]}>{t('recordings.rulesLedeLead')} <Text style={{color: theme.colors.text, fontWeight: '700'}}>{t('recordings.rulesLedeAction')}</Text> {t('recordings.rulesLedeTail')}</Text></View>} renderItem={({item, index}) => {let lease = controller.viewerFocusToken(item.id); return <RuleRow rule={item} runtime={runtime} onFocus={() => {if (drawnCurrent() && state.viewerWindow) {lease = controller.focusViewerRow(item.id, state.viewerWindow); if (index >= rules.length - 2 && state.viewerWindow.error === undefined) void controller.nextViewerPage(state.viewerWindow, viewerOpening);}}} onBlur={() => {if (state.viewerWindow && lease) controller.blurViewerRow(item.id, lease, state.viewerWindow);}} refNode={node => {if (node) rows.current.set(item.id, node); else rows.current.delete(item.id);}} onOpen={() => {if (!drawnCurrent()) return; const window = state.viewerWindow; if (!window || controller.getSnapshot().viewerWindow !== window || !window.items.some(row => row.kind === 'rule' && row.value.id === item.id && row.value.revision === item.revision)) return; if (ruleOpening.current) controller.unpinViewerRow(ruleOpening.current.pin, ruleOpening.current.scope); const pin = controller.pinViewerRow(item.id, window); if (!pin) return; ruleOpening.current = {id: item.id, revision: item.revision, scope: window.scope, pin}; opener.current = rows.current.get(item.id) ?? null; setRule(item);}}/>;}}/>
        : <View style={{flex: 1}}><FlatList onScrollBeginDrag={() => {if (drawnCurrent()) scrollIntent.current = true;}} onEndReached={() => {if (drawnCurrent() && scrollIntent.current && state.viewerWindow && state.viewerWindow.error === undefined && !state.viewerWindow.refreshRequired) {scrollIntent.current = false; void controller.nextViewerPage(state.viewerWindow, viewerOpening);}}} onEndReachedThreshold={0.3} key={`rows:${query.view}:${query.group ?? ''}`} ref={list} data={rowData} keyExtractor={value => value.kind === 'heading' ? value.id : value.recording.id} contentContainerStyle={{paddingBottom: 90}} renderItem={({item, index}) => {let lease = item.kind === 'recording' ? controller.viewerFocusToken(item.recording.id) : undefined; return item.kind === 'heading' ? <Text accessibilityRole="header" style={[shellType(theme.isTV ? 32 : 22, 700, 'normal', -.02), {color: theme.colors.text, marginTop: 16, marginBottom: 14}]}>{item.title}</Text> : <RecordingRow recording={item.recording} onFocus={() => {if (drawnCurrent() && state.viewerWindow) {lease = controller.focusViewerRow(item.recording.id, state.viewerWindow); if (index >= rowData.length - 2 && state.viewerWindow.error === undefined) void controller.nextViewerPage(state.viewerWindow, viewerOpening);}}} onBlur={() => {if (state.viewerWindow && lease) controller.blurViewerRow(item.recording.id, lease, state.viewerWindow);}} history={query.view === 'history'} runtime={runtime} refNode={node => {if (node) rows.current.set(item.recording.id, node); else rows.current.delete(item.recording.id);}} onOpen={() => open(item.recording, 'details', state.viewerWindow)} onMenu={() => open(item.recording, 'menu', state.viewerWindow)}/>;}}/>{theme.isTV && query.view === 'history' && <ListFade/>}</View>}
      {state.viewerWindow && <>
        {state.viewerWindow.error !== undefined && <StateView {...runtime.error(state.viewerWindow.error, () => {if (drawnCurrent() && state.viewerWindow) void controller.retryViewerPage(state.viewerWindow, viewerOpening);}, 'live')} testID="recordings-page-error"/>}
        {state.viewerWindow.refreshRequired && <StateView {...runtime.error({code: 'stale_continuation'}, () => {if (state.viewerWindow) restartViewer(state.viewerWindow);}, 'live')} testID="recordings-page-refresh"/>}
        {state.viewerWindow.chunks[0]?.cursor && <Button focusId="recordings-page-previous" testID="recordings-page-previous" onPress={() => {if (drawnCurrent() && state.viewerWindow) void controller.previousViewerPage(state.viewerWindow, viewerOpening);}}>{t('web.pager.previous')}</Button>}
        {(state.viewerWindow.nextCursor || state.viewerWindow.pending) && <Focusable key="recordings-page-next" focusId="recordings-page-next" testID="recordings-page-next" disabled={state.viewerWindow.pending} focusableWhenDisabled onFocus={() => {if (drawnCurrent() && state.viewerWindow) controller.focusViewerHost(state.viewerWindow);}} onPress={() => {if (drawnCurrent() && state.viewerWindow) void (state.viewerWindow.refreshRequired ? (restartViewer(state.viewerWindow), Promise.resolve()) : state.viewerWindow.error !== undefined ? controller.retryViewerPage(state.viewerWindow, viewerOpening) : controller.nextViewerPage(state.viewerWindow, viewerOpening));}}><ScreenText>{t(state.viewerWindow.error !== undefined || state.viewerWindow.refreshRequired ? 'next.states.tryAgain' : 'next.settings.loadMore')}</ScreenText></Focusable>}
      </>}
    </FocusRegion>
    <Menu key={menu ? `${menu.recording.id}:${menu.recording.revision}` : 'recordings-menu'} open={menu !== undefined} onOpenChange={(value, reason) => {if (!value && menu) close(menu, reason === 'action' ? 'action' : 'user');}} title={menu?.recording.programme.title} returnFocusRef={opener} testID="recordings-menu" items={menu ? [
      ...(playableRecording(menu.recording) ? [{id: 'play', icon: 'i-play' as const, label: t('action.play'), disabled: state.busy, onSelect: () => {if (overlay.current === menu && askReady(menu)) run(() => play(menu.recording, menu));}}] : []),
      ...(!activeRecording(menu.recording) && playableRecording(menu.recording) ? [{id: 'keep', icon: 'i-bookmark' as const, label: t(menu.recording.keep ? 'web.dvr.stopKeeping' : 'web.dvr.keep'), disabled: state.busy, onSelect: () => {if (overlay.current === menu && askReady(menu)) run(() => controller.keep(menu.recording));}}] : []),
      ...(activeRecording(menu.recording) ? [{id: 'cancel', label: t('recordings.cancelAction'), disabled: state.busy, onSelect: () => run(() => askDelete(menu))}] : state.page?.deletionAvailable ? [{id: 'separator', separator: true as const}, {id: 'delete', icon: 'i-trash' as const, label: t('web.dvr.deleteRecording'), destructive: true, disabled: state.busy, onSelect: () => run(() => askDelete(menu))}] : []),
    ] : []}/>
    <Menu testID="recordings-sort" presentation="sideSheet" open={sortOpen} onOpenChange={value => {if (!value) setSortOpen(false);}} title={t('next.browse.sort')}
      items={(['recent', 'title', 'oldest'] as const).map(value => ({id: value, label: t(sortLabels[value]), selected: value === sort, onSelect: () => selectSort(value)}))}/>
    {detail && <RecordingDetails key={`${detail.recording.id}:${detail.recording.revision}:${detail.query}`} recording={detail.recording} controller={controller} runtime={runtime} parent={detail} isCurrent={() => overlay.current === detail && askReady(detail)} onClose={() => close(detail, 'user')} opener={opener} onPlay={() => {if (overlay.current === detail && askReady(detail)) run(() => play(detail.recording, detail));}} onRemove={() => run(() => askDelete(detail))} deletionAvailable={state.page?.deletionAvailable === true}/>}
    <Dialog key={confirm ? `${confirm.recording.id}:${confirm.recording.revision}` : 'recordings-confirm'} open={confirm !== undefined} onOpenChange={value => {if (!value && !controller.getSnapshot().busy) closeConfirm();}} title={confirm ? t(confirm.preview ? 'web.dvr.deleteTitle' : 'web.dvr.cancelTitle', {title: confirm.recording.programme.title}) : ''} description={confirm ? confirm.preview ? t(confirm.preview.activeReaders ? 'web.dvr.deleteBodyInUse' : confirm.preview.keep ? 'web.dvr.deleteBodyKept' : 'web.dvr.deleteBody', {size: defaultI18n.bytes(confirm.preview.bytes)}) : t('web.dvr.cancelBody') : undefined} width={theme.isTV ? 980 : undefined} initialFocusRef={cancel} returnFocusRef={opener} testID="recordings-confirm" footer={<View style={{flexDirection: 'row', justifyContent: 'flex-end', gap: 16}}><Button testID="recordings-confirm-cancel" ref={cancel} variant="glass" disabled={state.busy} onPress={closeConfirm}>{t('web.dvr.keepIt')}</Button><Button testID="recordings-confirm-delete" variant="glass" textStyle={{color: theme.colors.record}} style={{backgroundColor: `${theme.colors.record}29`}} disabled={state.busy} loading={state.busy} onPress={() => {if (!confirm || !askReady(confirm.opening)) return; const opening = confirm; run(async () => {const result = opening.preview ? await controller.remove(opening.recording, opening.preview) : await controller.cancel(opening.recording); if (result && alive.current && controller.lifetime.current()) {controller.unpinViewerRow(opening.opening.pin, opening.opening.scope); setConfirm(current => current === opening ? undefined : current);}});}}>{t(confirm?.preview ? 'web.dvr.deleteRecording' : 'recordings.cancelAction')}</Button></View>}>{state.error !== undefined ? <StateView testID="recordings-confirm-error" {...runtime.error(state.error, closeConfirm, 'live')}/> : <View/>}</Dialog>
    {rule && <RuleEditor key={`${rule.id}:${rule.revision}`} rule={rule} controller={controller} runtime={runtime} opener={opener} isCurrent={() => {const opening = activeRuleOpening, window = controller.getSnapshot().viewerWindow; return !!opening && ruleOpening.current === opening && opening.id === rule.id && opening.revision === rule.revision && window?.scope === opening.scope && window.items.some(row => row.kind === 'rule' && row.value.id === rule.id && row.value.revision === rule.revision);}} onClose={() => {const opening = activeRuleOpening; if (opening && ruleOpening.current === opening && opening.id === rule.id && opening.revision === rule.revision) {controller.unpinViewerRow(opening.pin, opening.scope); ruleOpening.current = undefined; setRule(current => current === rule ? undefined : current);}}}/>}
    {/* TV viewers keep programme/series recording and rule editing; the administrative
        new-rule sheet is not a TV surface and never mounts here. */}
    {owner && !theme.isTV && <NewRuleSheet controller={owner} runtime={runtime} open={newRule} onOpenChange={setNewRule} onSaved={() => {setNewRule(false); void controller.load({view: 'rules'});}}/>}
  </Screen>;
}

function SpacedFacts({text}: {text: string}) {return <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: 16}}>{text.split(' · ').map((part, index) => <ScreenText key={index} secondary>{part}</ScreenText>)}</View>;}
function StorageFacts({storage}: {storage: RecordingStorage}) {
  const theme = useTheme(), facts = defaultI18n.t('recordings.storageUsed', {used: defaultI18n.bytes(storage.measurement.usedBytes), free: defaultI18n.bytes(storage.measurement.freeBytes)}).split(' · ');
  return <View style={{flexDirection: 'row', justifyContent: 'flex-end', gap: 10}}>{facts.map((part, index) => <Text key={index} style={[shellType(theme.isTV ? 22 : 12, index ? 400 : 700), {color: index ? theme.colors.textTertiary : theme.colors.text}]}>{part}</Text>)}</View>;
}
function Meter({storage}: {storage: RecordingStorage}) {const theme = useTheme(), total = storage.measurement.usedBytes + storage.measurement.freeBytes; return <View style={{height: 8, marginTop: 10, borderRadius: 5, backgroundColor: theme.colors.line, overflow: 'hidden'}}><View style={{height: 8, width: `${total > 0 ? Math.min(100, storage.measurement.usedBytes / total * 100) : 0}%`, backgroundColor: theme.colors.accentStrong}}/></View>;}
/** The drawn bottom fade that says a list continues (frames 21 and 27). It never takes
 * focus or a press; over an empty page it fades ink into ink and disappears. */
function ListFade() {
  const theme = useTheme();
  return <View pointerEvents="none" accessible={false} style={{position: 'absolute', left: 0, right: 0, bottom: 0, height: 90}}>
    <Svg width="100%" height="100%"><Defs><LinearGradient id="recordings-list-fade" x1="0" y1="0" x2="0" y2="1"><Stop offset="0" stopColor={theme.colors.ink} stopOpacity={0}/><Stop offset="1" stopColor={theme.colors.ink} stopOpacity={.94}/></LinearGradient></Defs><Rect width="100%" height="100%" fill="url(#recordings-list-fade)"/></Svg>
  </View>;
}
function StorageView({storage, owner}: {storage: RecordingStorage; owner: boolean}) {
  const theme = useTheme(), t = defaultI18n.t;
  return <View style={{maxWidth: theme.isTV ? 1100 : undefined, gap: 16}}><StorageFacts storage={storage}/><Meter storage={storage}/><ScreenText secondary>{t('recordings.hoursLeft', {count: storage.forecastHours})}</ScreenText>
    <View style={{gap: 12, marginTop: 24}}>{storage.pendingDeleteBytes > 0 && <StorageRow icon="i-trash" text={t('recordings.pendingDelete', {size: defaultI18n.bytes(storage.pendingDeleteBytes)})}/>} {owner && storage.policy.retentionDays > 0 && <StorageRow icon="i-clock" text={t('recordings.retention', {count: storage.policy.retentionDays})}/>} {!storage.measurement.writeHealthy && <StorageRow icon="i-warn" text={t('recordings.writeUnhealthy')}/>}</View></View>;
}
function StorageRow({icon, text}: {icon: 'i-trash' | 'i-clock' | 'i-warn'; text: string}) {const theme = useTheme(); return <View style={{height: theme.isTV ? 92 : 64, paddingHorizontal: 24, borderRadius: 16, flexDirection: 'row', alignItems: 'center', gap: 20, backgroundColor: theme.colors.raised}}><Icon name={icon} size={theme.isTV ? 34 : 24}/><ScreenText>{text}</ScreenText></View>;}
function ProgrammeArt({title, seed, image, runtime, width}: {title: string; seed: string; image?: string; runtime?: NativeShellRuntime; width: number}) {
  const source = image && runtime?.artwork(image), hue = titleTintHue(seed);
  return <View style={{width, aspectRatio: 16 / 9, backgroundColor: `hsl(${hue}, 32%, 22%)`, borderRadius: 12, overflow: 'hidden'}}><GeneratedArt glyph={(Array.from(title.trim())[0] ?? '').toLocaleUpperCase(defaultI18n.locale)} palette={{first: `hsl(${hue}, 40%, 32%)`, second: `hsl(${hue}, 30%, 12%)`}}/>{source && <Artwork source={source}/>}</View>;
}
function RecordingCard({value, runtime, onOpen, onFocus, onBlur}: {value: Group; runtime: NativeShellRuntime; onOpen: () => void; onFocus: () => void; onBlur: () => void}) {
  const theme = useTheme(), t = defaultI18n.t, recording = value.recording;
  return <View style={{width: theme.isTV ? 385 : (theme.width - 56) / 2, gap: 12}}><Focusable onFocus={onFocus} onBlur={onBlur} focusId={value.key} testID={`recordings-group-${value.key}`} focusKind="card" onPress={onOpen} style={{aspectRatio: 16 / 9, borderRadius: 14, overflow: 'hidden'}}><ProgrammeArt title={value.title} seed={recording.occurrence.channelId} image={recording.programme.image} runtime={runtime} width={theme.isTV ? 385 : (theme.width - 56) / 2}/><View style={{position: 'absolute', left: 16, right: 12, bottom: 16, gap: 8}}><ScreenText secondary>{recording.channel?.name}</ScreenText><Text numberOfLines={1} style={[shellType(theme.isTV ? 28 : 17, 700), {color: theme.colors.text}]}>{value.title}</Text></View>{value.kept > 0 && <View style={{position: 'absolute', left: 16, top: 12}}><ScreenText secondary>{t('recordings.kept')}</ScreenText></View>}{value.exceptions > 0 && <View style={{position: 'absolute', left: 16, top: value.kept > 0 ? 44 : 12}}><ScreenText secondary>{t('web.dvr.state.incomplete')}</ScreenText></View>}{recording.watched === false && value.recordings === 1 && <View style={{position: 'absolute', right: 16, top: 16, width: 14, height: 14, borderRadius: 7, backgroundColor: theme.colors.accentStrong}}/>}</Focusable><SpacedFacts text={t(value.unwatched ? 'recordings.showCountUnwatched' : 'recordings.showCount', {count: value.recordings, unwatched: value.unwatched})}/></View>;
}
function RecordingRow({recording, history, runtime, refNode, onOpen, onMenu, onFocus, onBlur}: {recording: Recording; history: boolean; runtime: NativeShellRuntime; refNode: (node: View | null) => void; onOpen: () => void; onMenu: () => void; onFocus: () => void; onBlur: () => void}) {
  const theme = useTheme(), t = defaultI18n.t, conflict = recording.state === 'conflicted', reason = reasonLabels[recording.reason];
  const range = programmeTimeRange(Date.parse(recording.programme.start), Date.parse(recording.programme.end), runtime.text);
  return <Focusable onFocus={conflict ? undefined : onFocus} onBlur={conflict ? undefined : onBlur} ref={conflict ? undefined : refNode} focusable={!conflict} focusId={recording.id} testID={`recordings-row-${recording.id}`} focusKind="row" onPress={onOpen} onLongPressAction={onMenu} style={{height: theme.isTV ? history ? 112 : 124 : 100, paddingHorizontal: theme.isTV ? 24 : 8, marginBottom: 6, borderRadius: 18, flexDirection: 'row', alignItems: 'center', gap: theme.isTV ? 28 : 12, backgroundColor: conflict ? theme.colors.raised : undefined}}>
    {theme.width >= 700 && <View style={{width: theme.isTV ? 190 : 110, gap: 6}}><ScreenText>{history ? defaultI18n.formatDate(recording.programme.start, {month: 'short', day: 'numeric'}) : defaultI18n.time(recording.programme.start)}</ScreenText><ScreenText secondary>{range?.text}</ScreenText></View>}
    <ProgrammeArt title={recording.programme.title} seed={recording.occurrence.channelId} image={recording.programme.image} runtime={runtime} width={theme.isTV ? 192 : 110}/>
    <View style={{flex: 1, gap: 8}}><Text numberOfLines={1} style={[shellType(theme.isTV ? 26 : 17, 700, 'normal', -.01), {color: theme.colors.text}]}>{recording.programme.title}</Text><ScreenText secondary>{recording.channel?.name}</ScreenText>{history && reason && <ScreenText secondary>{t(reason)}</ScreenText>}</View>
    {(history || recording.state !== 'scheduled') && <View style={{paddingHorizontal: 16, paddingVertical: 6, borderRadius: 10, backgroundColor: theme.colors.raisedHigh}}><ScreenText secondary>{t(stateLabels[recording.state])}</ScreenText></View>}
    {conflict && <Button onFocus={onFocus} onBlur={onBlur} ref={refNode} focusId={`${recording.id}.resolve`} onPress={onOpen} onLongPressAction={onMenu}>{t('web.dvr.seeConflict')}</Button>}
  </Focusable>;
}

/** One opening of the details sheet. The parent keys the sheet by its row and this
 * opening retires itself as well, so either side alone stops a stale completion. The opening
 * also holds the recording and the currency callback it was drawn for, so its read can only
 * ever use the witnesses of the render that committed it. */
type RecordingOpening = {id: string; revision: number; controller: NativeRecordingsController; parent: OverlayOpening; recording: Recording; isCurrent: () => boolean; live: boolean};

function RecordingDetails({recording, controller, runtime, opener, onClose, onPlay, onRemove, deletionAvailable, isCurrent, parent}: {recording: Recording; controller: NativeRecordingsController; runtime: NativeShellRuntime; parent: OverlayOpening; isCurrent: () => boolean; opener: React.RefObject<View | null>; onClose: () => void; onPlay: () => void; onRemove: () => void; deletionAvailable: boolean}) {
  const theme = useTheme(), t = defaultI18n.t, state = useSyncExternalStore(controller.subscribe, controller.getSnapshot);
  const conflict = recording.state === 'conflicted';
  const primary = useRef<View>(null);
  // Retire the previous opening during render, before the replacement draws, so a
  // completion or a read that belongs to A can never act on B.
  const opening = useRef<RecordingOpening | undefined>(undefined);
  const previous = opening.current;
  const opened: RecordingOpening = previous && previous.id === recording.id && previous.revision === recording.revision && previous.controller === controller && previous.parent === parent
    ? previous : {id: recording.id, revision: recording.revision, controller, parent, recording, isCurrent, live: true};
  if (previous && previous !== opened) previous.live = false;
  opening.current = opened;
  // The opening this component committed, written only by the layout pass below. The passive
  // read copies it once and every settlement retains that copy, so a render React throws away
  // can never hand its witnesses to a read.
  const committed = useRef<RecordingOpening | undefined>(undefined);
  // A layout effect retires the opening on unmount as well, before any later callback
  // can land; a passive cleanup would leave it live through a replacement. It is keyed by the
  // same values that decide the opening, and it is where the read takes its witnesses from.
  useLayoutEffect(() => {
    const drawn = opening.current; if (!drawn) return;
    committed.current = drawn; drawn.live = true;
    return () => {drawn.live = false; if (committed.current === drawn) committed.current = undefined;};
  }, [opened.id, opened.revision, opened.controller, opened.parent]);
  // The read carries the opening it was made for, so A's state, capacity and overlaps
  // are never drawn under B's title. The recording and the currency callback travel on that
  // committed opening, so they are exactly the values of the render that committed it.
  const [read, setRead] = useState<Readonly<{for: RecordingOpening; detail?: RecordingDetail; resolver?: ReturnType<NativeRecordingsController['detailOpening']>; error?: unknown}>>();
  const [overlap, setOverlap] = useState<Readonly<{for: RecordingOpening; window: RecordingOverlapWindow; snapshot: OverlapSnapshot}>>();
  useEffect(() => {
    const drawn = committed.current;
    if (!drawn) return;
    let active = true, window: RecordingOverlapWindow | undefined;
    const resolver = drawn.controller.detailOpening(drawn.recording, () => active && drawn.live && drawn.isCurrent());
    if (drawn.isCurrent()) void resolver.read().then(value => {
      if (!active || !drawn.live || !drawn.isCurrent()) return;
      try {
        window = new RecordingOverlapWindow(drawn.recording, value, drawn.controller, () => active && drawn.live && drawn.isCurrent(), snapshot => {
          if (active && drawn.live && window && drawn.isCurrent()) setOverlap({for: drawn, window, snapshot});
        });
        setRead({for: drawn, detail: value, resolver}); window.seed(value);
      } catch (error) {if (active && drawn.live && drawn.isCurrent()) setRead({for: drawn, error});}
    }, value => {
      if (active && drawn.live && drawn.isCurrent()) setRead({for: drawn, error: value});
    });
    return () => {active = false; resolver.dispose(); window?.dispose();};
  }, [opened.id, opened.revision, opened.controller, opened.parent]);
  const current = read?.for === opened ? read : undefined;
  const detail = current?.detail, error = current?.error;
  const overlapCurrent = overlap?.for === opened ? overlap : undefined;
  const runResolve = (resolver: NonNullable<typeof current>['resolver'], value: RecordingDetail) => {
    if (!resolver) return;
    void resolver.resolve(value).then(result => {if (result && opened.live && isCurrent()) onClose();}, error => {if (opened.live && isCurrent()) runtime.reportError(error);});
  };
  return <Dialog open onOpenChange={value => {if (!value && !controller.getSnapshot().busy) onClose();}} title={conflict ? t('web.dvr.conflictTitle', {title: recording.programme.title}) : recording.programme.title} placement={theme.isTV || theme.width >= 700 ? 'side' : undefined} width={theme.isTV ? 640 : undefined} initialFocusRef={primary} returnFocusRef={opener} testID="recordings-details">
    <View style={{gap: 24}}>{error !== undefined && <StateView testID="recordings-details-error" {...runtime.error(error, onClose, 'live')}/>}{!detail && error === undefined && <StateView title={t('recordings.loading')}/>}
      {detail && <><ScreenText secondary>{t(stateLabels[detail.recording.state])}</ScreenText><ScreenText secondary>{t('guide.range', {start: defaultI18n.time(detail.recording.start), end: defaultI18n.time(detail.recording.end)})}</ScreenText>{reasonLabels[detail.recording.reason] && <ScreenText secondary>{t(reasonLabels[detail.recording.reason])}</ScreenText>}
        {conflict && <><ScreenText secondary>{t(detail.capacity.known ? 'recordings.conflictBodyTuners' : 'recordings.conflictBody', {count: detail.capacity.effective})}</ScreenText>{(overlapCurrent?.snapshot.items ?? detail.overlaps).map(value => <View key={value.id} style={{flexDirection: 'row', gap: 20, alignItems: 'center'}}><Icon name="i-rec" size={20}/><View style={{flex: 1}}><ScreenText>{value.title}</ScreenText></View><ScreenText secondary>{defaultI18n.time(value.start)}</ScreenText></View>)}{overlapCurrent && <>
          {overlapCurrent.snapshot.error !== undefined && <StateView {...runtime.error(overlapCurrent.snapshot.error, () => {void overlapCurrent.window.retry(overlapCurrent.snapshot);}, 'live')} testID="recordings-overlap-error"/>}
          {overlapCurrent.snapshot.refreshRequired && <StateView {...runtime.error({code: 'stale_continuation'}, onClose, 'live')} testID="recordings-overlap-refresh"/>}
          {overlapCurrent.snapshot.chunks[0]?.cursor && <Button focusId="recordings-overlap-previous" testID="recordings-overlap-previous" onPress={() => {void overlapCurrent.window.previous(overlapCurrent.snapshot);}}>{t('web.pager.previous')}</Button>}
          {(overlapCurrent.snapshot.nextCursor || overlapCurrent.snapshot.pending) && <Focusable key="recordings-overlap-next" focusId="recordings-overlap-next" testID="recordings-overlap-next" disabled={overlapCurrent.snapshot.pending} focusableWhenDisabled onPress={() => {if (!opened.live || !isCurrent()) return; void (overlapCurrent.snapshot.error !== undefined ? overlapCurrent.window.retry(overlapCurrent.snapshot) : overlapCurrent.window.next(overlapCurrent.snapshot));}}><ScreenText>{t(overlapCurrent.snapshot.error !== undefined ? 'next.states.tryAgain' : 'next.settings.loadMore')}</ScreenText></Focusable>}
        </>}<Button testID="recordings-conflict-record" ref={primary} disabled={state.busy || !current?.resolver?.canResolve(detail)} loading={state.busy} onPress={() => {if (!opened.live || !isCurrent() || !current?.resolver?.canResolve(detail)) return; runResolve(current.resolver, detail);}} icon="i-rec">{t('recordings.recordThis')}</Button><Button testID="recordings-conflict-skip" variant="glass" disabled={state.busy} onPress={() => {if (!opened.live || !isCurrent()) return; void controller.cancel(detail.recording).then(result => {if (result && opened.live && isCurrent()) onClose();});}}>{t('recordings.skipThis')}</Button></>}
        {!conflict && <>{playableRecording(detail.recording) && <Button testID="recordings-details-play" ref={primary} onPress={onPlay} icon="i-play">{t('action.play')}</Button>}{activeRecording(detail.recording) && <Button testID="recordings-details-cancel" ref={!playableRecording(detail.recording) ? primary : undefined} variant="glass" disabled={state.busy} onPress={onRemove}>{t('recordings.cancelAction')}</Button>}{deletionAvailable && !activeRecording(detail.recording) && <Button testID="recordings-details-delete" variant="glass" textStyle={{color: theme.colors.record}} style={{backgroundColor: `${theme.colors.record}29`}} disabled={state.busy} onPress={onRemove}>{t('web.dvr.deleteRecording')}</Button>}</>}
      </>}
    </View>
  </Dialog>;
}

function RuleRow({rule, runtime, refNode, onOpen, onFocus, onBlur}: {rule: RecordingRule; runtime: NativeShellRuntime; refNode: (node: View | null) => void; onOpen: () => void; onFocus: () => void; onBlur: () => void}) {
  const theme = useTheme(), t = runtime.text.t, tv = theme.isTV, paused = !rule.config.enabled, facts = ruleFacts(rule.summary);
  const scope = ruleScope(rule, rule.summary?.channel.name, runtime.text), next = ruleNext(rule.summary, defaultI18n, Date.now());
  const factsRow = [t(rule.config.episodes === 'new' ? 'recordings.newEpisodes' : 'recordings.allEpisodes'), scope, keepLabel(runtime.text, ruleKeep(rule.config.options))];
  return <Focusable onFocus={onFocus} onBlur={onBlur} ref={refNode} focusId={rule.id} testID={`recordings-rule-${rule.id}`} focusKind="row" onPress={onOpen}
    style={{height: tv ? 140 : 110, paddingVertical: tv ? 8 : 12, paddingHorizontal: tv ? 24 : 12, borderRadius: tv ? 20 : 16, flexDirection: 'row', alignItems: 'center', gap: tv ? 30 : 12, backgroundColor: theme.colors.raised}}>
    <View style={paused ? {opacity: .55} : undefined}><ProgrammeArt title={rule.config.name} seed={rule.config.sourceId} width={tv ? 192 : 110}/></View>
    <View style={{flex: 1, minWidth: 0}}>
      <Text numberOfLines={1} style={[shellType(tv ? 28 : 17, 700), {color: theme.colors.text, opacity: paused ? .55 : 1}]}>{rule.config.name}</Text>
      <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: tv ? 18 : 10, marginTop: tv ? 6 : 3}}>{factsRow.map((fact, index) => <Text key={index} numberOfLines={1} style={[shellType(tv ? 20 : 12.5, 400), {color: theme.colors.textSecondary}]}>{fact}</Text>)}</View>
      <Text numberOfLines={tv ? 1 : 2} style={[shellType(tv ? 20 : 12.5, 400), {color: !paused && next?.conflict ? theme.colors.record : theme.colors.textTertiary, marginTop: tv ? 8 : 3}]}>{paused && facts.tonight > 0 ? t('recordings.rulePausedTonight', {count: facts.tonight}) : next?.text}</Text>
    </View>
    <View style={{alignItems: 'flex-end', gap: tv ? 8 : 4}}>
      <View style={{paddingVertical: tv ? 6 : 3, paddingHorizontal: tv ? 16 : 9, borderRadius: tv ? 10 : 7, backgroundColor: rule.config.enabled ? `${theme.colors.healthy}21` : `${theme.colors.text}14`}}>
        <Text style={[shellType(tv ? 20 : 12, 700), {color: rule.config.enabled ? theme.colors.healthy : theme.colors.textSecondary}]}>{t(rule.config.enabled ? 'recordings.on' : 'recordings.paused')}</Text>
      </View>
      {!paused && <Text style={[shellType(tv ? 20 : 12, 400), {color: theme.colors.textTertiary}]}>{t('recordings.ruleUpcoming', {count: facts.upcoming})}</Text>}
    </View>
  </Focusable>;
}

function RuleEditor({rule, controller, runtime, opener, onClose, isCurrent}: {rule: RecordingRule; controller: NativeRecordingsController; runtime: NativeShellRuntime; isCurrent: () => boolean; opener: React.RefObject<View | null>; onClose: () => void}) {
  const theme = useTheme(), t = runtime.text.t, state = useSyncExternalStore(controller.subscribe, controller.getSnapshot);
  const [config, setConfig] = useState<RuleConfig>(() => ({...rule.config, options: {...rule.config.options}})), [deleting, setDeleting] = useState(false), [future, setFuture] = useState<'keep' | 'cancel'>('keep');
  const save = useRef<View>(null), cancel = useRef<View>(null), keep = useRef<View>(null), tv = theme.isTV, facts = ruleFacts(rule.summary);
  const mounted = useRef(false);
  useLayoutEffect(() => {mounted.current = true; return () => {mounted.current = false;};}, [controller, rule.id, rule.revision]);
  const current = () => mounted.current && isCurrent() && controller.lifetime.current() && runtime.getSnapshot().destination.kind === 'live' && runtime.getSnapshot().contentVisible !== false;
  const close = () => {if (current() && !controller.getSnapshot().busy) onClose();};
  const padding = [0, 60, 120, 300, 600, 900], minutes = (value: number) => t(value ? 'recordings.minutes' : 'recordings.none', {count: value / 60});
  const rowStyle = {height: tv ? 76 : 52, borderRadius: tv ? 16 : 12, paddingHorizontal: tv ? 26 : 16, backgroundColor: `${theme.colors.text}0F`};
  const rowText = {fontSize: (tv ? 25 : 15.5) * theme.textScale, fontWeight: '600' as const};
  const write = async (next: RuleConfig) => {if (!current() || controller.getSnapshot().busy) return; const result = await controller.saveRule(rule, next); if (result && current()) onClose();};
  const remove = async () => {if (!current() || controller.getSnapshot().busy) return; const result = await controller.deleteRule(rule, future); if (result && current()) onClose();};
  return <><Dialog open={!deleting} onOpenChange={value => {if (!value) close();}} title={rule.config.name}
    headerContext={<View style={{flexDirection: 'row', alignItems: 'center', gap: 16, marginBottom: tv ? 18 : 12}}><Icon name="i-repeatrule" size={tv ? 26 : 20} color={theme.colors.accentStrong}/><Text style={[shellType(tv ? 22 : 14, 700), {color: theme.colors.accentStrong}]}>{t('recordings.seriesRule')}</Text></View>}
    placement={tv || theme.width >= 700 ? 'side' : undefined} width={tv ? 760 : undefined} initialFocusRef={keep} returnFocusRef={opener} testID="recordings-rule-editor">
    <ScrollView contentContainerStyle={{gap: 12}}>{state.error !== undefined && <StateView testID="recordings-error" {...runtime.error(state.error, () => {void controller.load();}, 'live')}/>}
      {rule.summary && <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: 22, marginBottom: tv ? 18 : 6}}><Text style={[shellType(tv ? 22 : 13, 700), {color: theme.colors.text}]}>{t('recordings.matches', {count: facts.upcoming})}</Text><Text style={[shellType(tv ? 22 : 13, 400), {color: theme.colors.textTertiary}]}>{t('recordings.recordedSoFar', {count: facts.recorded})}</Text></View>}
      <View style={[rowStyle, {flexDirection: 'row', alignItems: 'center', gap: 20}]}><Text style={[shellType(tv ? 25 : 15.5, 600), {color: theme.colors.text, flex: 1}]}>{t('recordings.ruleIsOn')}</Text><Switch testID="recordings-rule-enabled" accessibilityLabel={t('recordings.ruleIsOn')} checked={config.enabled} onCheckedChange={enabled => setConfig(value => ({...value, enabled}))}/></View>
      <View style={{flexDirection: 'row', gap: 12}}>{(['new', 'all'] as const).map(episodes => <Chip key={episodes} testID={`recordings-rule-episodes-${episodes}`} selected={config.episodes === episodes} onPress={() => setConfig(value => ({...value, episodes}))} style={{flex: 1, justifyContent: 'center'}}>{t(episodes === 'new' ? 'recordings.newEpisodes' : 'recordings.allEpisodes')}</Chip>)}</View>
      <Select ref={keep} testID="recordings-rule-keep" rowLabel={t('recordings.keepLabel')} accessibilityLabel={t('recordings.keepLabel')} value={keepChoiceValue(config.options)} options={keepChoiceList(runtime.text, config.options)} onValueChange={value => setConfig(current => ({...current, options: withKeep(current.options, value)}))} style={rowStyle} textStyle={rowText} pickerContext={<View style={{flexDirection: 'row', alignItems: 'center', gap: 16}}><Icon name="i-repeatrule" size={26} color={theme.colors.accentStrong}/><Text style={[shellType(22, 700), {color: theme.colors.accentStrong}]}>{t('recordings.seriesRule')}</Text></View>}/>
      <Select testID="recordings-rule-before" rowLabel={t('recordings.startEarly')} accessibilityLabel={t('recordings.startEarly')} value={String(config.options.beforeSeconds)} options={padding.map(value => ({value: String(value), label: minutes(value)}))} onValueChange={value => setConfig(current => ({...current, options: {...current.options, beforeSeconds: Number(value)}}))} style={rowStyle} textStyle={rowText}/>
      <Select testID="recordings-rule-after" rowLabel={t('recordings.endLate')} accessibilityLabel={t('recordings.endLate')} value={String(config.options.afterSeconds)} options={padding.map(value => ({value: String(value), label: minutes(value)}))} onValueChange={value => setConfig(current => ({...current, options: {...current.options, afterSeconds: Number(value)}}))} style={rowStyle} textStyle={rowText}/>
      <View style={{gap: 12, marginTop: tv ? 16 : 8}}><Button testID="recordings-rule-save" ref={save} disabled={state.busy} loading={state.busy} onPress={() => {void write(config);}}>{t('action.save')}</Button><Button testID="recordings-rule-toggle" variant="glass" disabled={state.busy} onPress={() => {void write({...config, enabled: !config.enabled});}} icon={config.enabled ? 'i-pause' : 'i-play'}>{t(config.enabled ? 'recordings.pauseRule' : 'recordings.resumeRule')}</Button><Button testID="recordings-rule-delete-open" variant="glass" textStyle={{color: theme.colors.record}} style={{backgroundColor: `${theme.colors.record}29`}} disabled={state.busy} onPress={() => setDeleting(true)} icon="i-trash">{t('web.dvr.deleteRule')}</Button></View>
    </ScrollView>
  </Dialog>
    <Dialog open={deleting} onOpenChange={value => {if (!value && current() && !controller.getSnapshot().busy) setDeleting(false);}} title={t('recordings.deleteRuleTitle', {name: rule.config.name})} description={t('recordings.deleteRuleBody')} width={theme.isTV ? 980 : undefined} initialFocusRef={cancel} returnFocusRef={opener} testID="recordings-rule-delete" footer={<View style={{flexDirection: 'row', justifyContent: 'flex-end', gap: 16}}><Button testID="recordings-rule-delete-cancel" ref={cancel} variant="glass" disabled={state.busy} onPress={() => {if (current() && !controller.getSnapshot().busy) setDeleting(false);}}>{t('action.cancel')}</Button><Button testID="recordings-rule-delete-confirm" variant="glass" textStyle={{color: theme.colors.record}} style={{backgroundColor: `${theme.colors.record}29`}} disabled={state.busy} loading={state.busy} onPress={() => {void remove();}}>{t('recordings.deleteRule')}</Button></View>}><View style={{gap: 18}}><ScreenText secondary>{t('web.dvr.upcomingChoice')}</ScreenText><View style={{flexDirection: 'row', gap: 16}}>{(['keep', 'cancel'] as const).map(value => <Chip key={value} testID={`recordings-rule-delete-future-${value}`} selected={future === value} disabled={state.busy} onPress={() => setFuture(value)} style={{flex: 1, justifyContent: 'center'}}>{t(value === 'keep' ? 'recordings.keepUpcoming' : 'recordings.cancelUpcoming')}</Chip>)}</View></View></Dialog>
  </>;
}
