import {useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import type { FlatList,View } from 'react-native';
import {activeRecording, playableRecording, type DeletePreview, type DVRView, type Recording, type RecordingGroups, type RecordingRule} from '../../../../../../packages/client-core/src/dvr';
import {validateContentEntry} from '../../../../../../packages/client-core/src/library-content';
import {parseMediaItem} from '../../../../../../packages/client-core/src/media-item';
import type { RecordingSpace } from '../../../../../../packages/client-core/src/next-dvr';
import {readCatalogueItemEncodedResponse} from '../../../../../../packages/client-core/src/query/catalogue-reads';
import {defaultI18n, type MessageId} from '../../../../../../packages/i18n/src';
import {useFocusRegistry} from '../../../kit/focus';
import {useTheme} from '../../../kit/theme';
import {nativeHomeEntryLifetime} from '../../../screens/home/entry-lifetime';
import type { NativeLiveOwnerController } from '../../../screens/settings-live/live-owner-controller';
import type { NativeSettingsLivePorts } from '../../../screens/settings-live/ports';
import type { NativeRecordingsController } from '../../../screens/settings-live/recordings-controller';
import {useScreenBack, useScreenEntry} from '../../../screens/settings-live/Screen';
import type { NativeShellRuntime } from '../../../shell/runtime';
import {tvFocusIds} from '../../../shell/tv-focus';

export const views: readonly DVRView[] = ['upcoming', 'recorded', 'rules', 'history', 'storage'];
export const viewLabels: Record<DVRView, MessageId> = {upcoming: 'recordings.upcoming', recorded: 'recordings.recorded', rules: 'recordings.rules', history: 'web.dvr.history', storage: 'recordings.storage'};
export const stateLabels: Record<Recording['state'], MessageId> = {scheduled: 'web.dvr.state.scheduled', conflicted: 'web.dvr.state.conflicted', skipped: 'next.live.skipped', 'waiting-source': 'web.dvr.state.waitingSource', 'waiting-guide': 'web.dvr.state.waitingGuide', preparing: 'web.dvr.state.preparing', recording: 'web.dvr.state.recording', finalizing: 'web.dvr.state.finalizing', completed: 'web.dvr.state.completed', 'incomplete-playable': 'web.dvr.state.incomplete', failed: 'web.dvr.state.failed', cancelled: 'web.dvr.state.cancelled', 'pending-delete': 'web.dvr.state.pendingDelete', deleted: 'web.dvr.state.deleted'};
export const reasonLabels: Readonly<Record<string, MessageId>> = {'capture-window-missed': 'web.dvr.reason.missed', 'tuner-conflict': 'web.dvr.reason.conflict', 'storage-floor': 'web.dvr.reason.storageFloor', 'storage-cap': 'web.dvr.reason.storageCap', 'owner-cancelled': 'web.dvr.reason.cancelled', 'source-changed': 'web.dvr.reason.sourceChanged', 'guide-occurrence-missing': 'web.dvr.reason.guideMissing', 'partial-capture': 'web.dvr.reason.partial', 'physical-reader-active': 'web.dvr.reason.inUse', 'artifact-remove-unavailable': 'web.dvr.reason.removeRetry'};
/** The drawn Recorded order, as the groups reader takes it. */
export const sortLabels: Readonly<Record<'recent' | 'title' | 'oldest', MessageId>> = {recent: 'next.live.sortRecent', title: 'next.live.sortTitle', oldest: 'next.live.sortOldest'};
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
export type OverlayOpening = {kind: 'menu' | 'details'; recording: Recording; query: string; route: string; scope: object; pin: object; retired: boolean};

/** The delete ask started from one opening, holding that exact opening. */
type DeleteAsk = Readonly<{opening: OverlayOpening; controller: NativeRecordingsController; recording: Recording}>;

export type RecordingsProps = {controller: NativeRecordingsController; ports: Pick<NativeSettingsLivePorts, 'context'>; runtime: NativeShellRuntime; onGuide: () => void; owner?: NativeLiveOwnerController};

export function useRecordings({controller, ports, runtime, onGuide}: RecordingsProps) {
  const theme = useTheme(), t = defaultI18n.t;
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot), query = state.query;
  const ownerCanSeeRules = ports.context.account.account.role === 'owner';
  const canSeeRules = useCallback((snapshot: typeof state) => ownerCanSeeRules || snapshot.summary?.canRecord === true, [ownerCanSeeRules]);
  const availableViews = canSeeRules(state) ? views : views.filter(view => view !== 'rules');
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
      || query.view === 'recorded' && !query.group && !groups.length && state.storage?.writeHealthy === true);
  const entryRegion = first || emptyGuide ? body : tabs, entryChild = first ?? (emptyGuide ? 'recordings-open-guide' : query.view);
  const awaitingFirstPage = state.loading || !state.page && !state.groups && query.view !== 'storage';
  const enter = useScreenEntry(theme.isTV && !awaitingFirstPage ? {regionId: entryRegion, childId: entryChild} : undefined, controller.lifetime.current);
  // The asks die with the screen in the layout phase, so StrictMode's
  // mount/unmount/mount leaves them live again on the committed mount.
  useLayoutEffect(() => {const ownedOperations = playOperations.current; alive.current = true; controller.beginViewerOpening(viewerOpening); return () => {alive.current = false; controller.endViewerOpening(viewerOpening); for (const operation of ownedOperations) operation.abort(); if (ask.current) controller.unpinViewerRow(ask.current.opening.pin, ask.current.opening.scope); if (overlay.current) controller.unpinViewerRow(overlay.current.pin, overlay.current.scope); if (confirmation.current) controller.unpinViewerRow(confirmation.current.opening.pin, confirmation.current.opening.scope); if (ruleOpening.current) controller.unpinViewerRow(ruleOpening.current.pin, ruleOpening.current.scope); ask.current = undefined;};}, [controller, viewerOpening]);
  useEffect(() => {
    const snapshot = controller.getSnapshot(), previous = snapshot.query;
    const next = previous.view === 'rules' && !canSeeRules(snapshot) ? {view: 'upcoming'} as const
      : previous.group ? {view: 'recorded', sort: previous.sort} as const : previous;
    void controller.load(next);
    void controller.loadFacts();
  }, [controller, canSeeRules]);
  const select = (view: DVRView) => {
    // A profile that cannot record is not offered Series rules (the server refuses them with a 403).
    if (view === 'rules' && !canSeeRules(controller.getSnapshot())) return;
    if (view === query.view && !query.group) return;
    retire();
    setGroup(undefined);
    void controller.load({view});
    list.current?.scrollToOffset({offset: 0, animated: false});
  };
  /** The drawn order of the Recorded grid; the groups reader does the ordering. */
  const sort: 'recent' | 'title' | 'oldest' = query.sort === 'title' || query.sort === 'oldest' ? query.sort : 'recent';
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
      const item = await controller.lifetime.request(signal => readCatalogueItemEncodedResponse(ports.context.api, recording.itemId, signal).then(parseMediaItem), operation.lifetime.signal);
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
  const window = state.viewerWindow;
  const watchFocus = (id: string, index: number, length: number) => {
    let lease = controller.viewerFocusToken(id);
    return {onFocus: () => {
      if (!drawnCurrent() || !window) return;
      lease = controller.focusViewerRow(id, window);
      if (index >= length - 2 && window.error === undefined) void controller.nextViewerPage(window, viewerOpening);
    }, onBlur: () => {
      if (window && lease) controller.blurViewerRow(id, lease, window);
    }};
  };
  const refRow = (id: string) => (node: View | null) => {if (node) rows.current.set(id, node); else rows.current.delete(id);};
  const scroll = {onScrollBeginDrag: () => {if (drawnCurrent()) scrollIntent.current = true;}, onEndReached: () => {
    if (drawnCurrent() && scrollIntent.current && window && window.error === undefined && !window.refreshRequired) {
      scrollIntent.current = false;
      void controller.nextViewerPage(window, viewerOpening);
    }
  }, onEndReachedThreshold: .3};
  const openRule = (rule: RecordingRule) => {
    if (!drawnCurrent() || !window || controller.getSnapshot().viewerWindow !== window
      || !window.items.some(row => row.kind === 'rule' && row.value.id === rule.id && row.value.revision === rule.revision)) return;
    if (ruleOpening.current) controller.unpinViewerRow(ruleOpening.current.pin, ruleOpening.current.scope);
    const pin = controller.pinViewerRow(rule.id, window);
    if (!pin) return;
    ruleOpening.current = {id: rule.id, revision: rule.revision, scope: window.scope, pin};
    opener.current = rows.current.get(rule.id) ?? null;
    setRule(rule);
  };
  const ruleCurrent = () => {
    const opening = activeRuleOpening, window = controller.getSnapshot().viewerWindow;
    return !!opening && ruleOpening.current === opening && opening.id === rule?.id && opening.revision === rule.revision
      && window?.scope === opening.scope && window.items.some(row => row.kind === 'rule' && row.value.id === rule.id && row.value.revision === rule.revision);
  };
  const closeRule = () => {
    const opening = activeRuleOpening;
    if (!opening || ruleOpening.current !== opening || opening.id !== rule?.id || opening.revision !== rule.revision) return;
    controller.unpinViewerRow(opening.pin, opening.scope);
    ruleOpening.current = undefined;
    setRule(current => current === rule ? undefined : current);
  };
  return {theme, t, state, query, ids, tabs, body, bodyFocused, group, setGroup, menu, detail, rule, confirm, setConfirm,
    newRule, setNewRule, sortOpen, setSortOpen, opener, cancel, list, alive, viewerOpening, overlay,
    drawnCurrent, closeConfirm, retire, groups, rules, first, emptyGuide, entryRegion, enter, select, sort,
    selectSort, leaveGuide, back, open, close, askReady, run, play, askDelete, rowData, restartViewer, isEmpty, emptyTitle,
    emptyBody, unavailable, window, watchFocus, refRow, scroll, openRule, closeRule, ruleCurrent, availableViews};
}
export function storageFacts(storage: RecordingSpace) {
  const [used = '', free = ''] = defaultI18n.t('recordings.storageUsed', {used: storage.usedBytes === undefined ? '—' : defaultI18n.bytes(storage.usedBytes), free: defaultI18n.bytes(storage.freeBytes)}).split(' · ');
  const total = (storage.usedBytes ?? 0) + storage.freeBytes;
  return {used, free, fraction: storage.usedBytes !== undefined && total > 0 ? storage.usedBytes / total : undefined};
}

/** live-owner 22: a show's recordings say Kept, else Unwatched (the two facts the frame draws). */
export function keptOrWatched(recording: Recording): string | undefined {
  const t = defaultI18n.t;
  return recording.keep ? t('recordings.kept') : recording.watched === false ? t('next.live.unwatched') : undefined;
}
