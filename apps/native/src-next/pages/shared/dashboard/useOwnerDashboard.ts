import {serverNow} from '../../../../../../packages/client-core/src/server-clock';
import React, {useEffect, useMemo, useState} from 'react';
import {Linking} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {repeat} from '../../../../../../packages/client-core/src/timer';
import type {TelemetryReading} from '../../../../../../packages/client-core/src/telemetry';
import type {AdminSession} from '../../../../../../packages/client-core/src/playback-v1/types';
import {sessionViewer, STOP_MESSAGE_MAX, stopMessage} from '../../../../../../packages/client-core/src/server-admin/now-playing';
import {dataFolderConfirmText} from '../../../../../../packages/client-core/src/admin/data-folder';
import {ApiError} from '../../../../../../packages/client-core/src/http';
import {SettingsUpdatesSource, type UpdatesSnapshot} from '../../../../../../packages/client-core/src/server-admin/updates-source';
import type {MenuEntry} from '../../../kit/overlays';
import {usePullRefresh} from '../../../kit/states/usePullRefresh';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeDashboardController} from '../../../screens/settings-live/dashboard-controller';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import {createNativeOwnerDashboardReader, type NativeOwnerDashboardReader} from '../../../screens/settings-live/owner-dashboard/reader';
import {
  ownerAlertView, ownerDerivedTotals, ownerHealthLine, ownerIsNewServer, ownerJobView, ownerPeopleLine, ownerStreamView, ownerUpdatedLabel, ownerUsesCompactRows,
  type OwnerAlertView, type OwnerHealthWindow, type OwnerJobView, type OwnerStreamView, type OwnerViewing, type OwnerViewingPeriod,
} from '../../../screens/settings-live/owner-dashboard/model';
import {ownerUpdateView} from '../../../kit/dashboard/OwnerUpdateCard';
import type {OwnerStreamEntry} from '../../../kit/dashboard/OwnerStreams';
import type {OwnerDashboardViewProps} from '../../../kit/dashboard/OwnerDashboardView';

const noUpdates: UpdatesSnapshot = Object.freeze({phase: 'idle', busy: false}), noUpdatesSubscription = () => () => {};
const noUpdatesSnapshot = () => noUpdates;

type TelemetryState = Readonly<{reading?: TelemetryReading; error?: unknown}>;
type SummaryState = Readonly<{summary?: OwnerViewing; error?: unknown}>;
type AllStreams = Readonly<{items: readonly AdminSession[]; loading: boolean; error?: unknown}>;

function useOwnerTelemetry(reader: NativeOwnerDashboardReader, window: OwnerHealthWindow, enabled: boolean) {
  const [state, setState] = useState<TelemetryState>({}), [revision, setRevision] = useState(0);
  useEffect(() => {
    if (!enabled) {setState({}); return;}
    // Live is read every 2 seconds, as the web does; the 24-hour chart moves slowly and is read every minute.
    const everyMs = window === 'live' ? 2_000 : 60_000;
    const poll = repeat(async signal => {const reading = await reader.telemetry(window); if (!signal.aborted) setState({reading});},
      {everyMs, backoff: {baseMs: everyMs * 2, maxMs: 120_000}, onError: error => setState(current => ({...current, error}))});
    return () => poll.stop();
  }, [reader, window, enabled, revision]);
  return Object.freeze({...state, reload: () => setRevision(value => value + 1)});
}

function useOwnerStatistics(reader: NativeOwnerDashboardReader, period: OwnerViewingPeriod, enabled: boolean) {
  const [state, setState] = useState<SummaryState>({}), [revision, setRevision] = useState(0);
  useEffect(() => {
    if (!enabled) {setState({}); return;}
    let live = true;
    void reader.viewing(period).then(summary => {if (live) setState({summary});}, error => {if (live) setState(current => ({...current, error}));});
    return () => {live = false;};
  }, [reader, period, enabled, revision]);
  return Object.freeze({...state, reload: () => setRevision(value => value + 1)});
}


/** The owner Dashboard on phones and tablets: binds the one authorized dashboard snapshot to the drawn page and owns its
 * menus and sheets (Stop with a message, Fix permissions); it never chooses a role. Moved from NativeOwnerDashboardPhone as it was. */
export function useOwnerDashboard({controller, ports, runtime, inline = false}: {controller: NativeDashboardController; ports: NativeSettingsLivePorts; runtime: NativeShellRuntime; inline?: boolean}) {
  const state = React.useSyncExternalStore(controller.subscribe, controller.getSnapshot);
  const insets = useSafeAreaInsets(), t = runtime.text.t;
  const reader = useMemo(() => createNativeOwnerDashboardReader(controller.lifetime, ports), [controller, ports]);
  useEffect(() => () => reader.dispose(), [reader]);
  const [healthWindow, setHealthWindow] = useState<OwnerHealthWindow>('live'), [viewingPeriod, setViewingPeriod] = useState<OwnerViewingPeriod>('30d');
  const [now, setNow] = useState(() => serverNow());
  const [streamMenu, setStreamMenu] = useState<OwnerStreamView>(), [stopView, setStopView] = useState<OwnerStreamView>(), [message, setMessage] = useState('');
  const [jobMenu, setJobMenu] = useState<OwnerJobView>(), [busy, setBusy] = useState(false), [actionError, setActionError] = useState<unknown>();
  const owner = state.admission === 'owner';
  const telemetry = useOwnerTelemetry(reader, healthWindow, owner), statistics = useOwnerStatistics(reader, viewingPeriod, owner);
  // owner-dashboard 2b: the owner's cached release check, read once per showing and on pull; a pressed check asks the server once.
  const updates = useMemo(() => owner
    ? new SettingsUpdatesSource(reader.updates, () => controller.lifetime.current(), runtime.reportError)
    : undefined, [owner, reader, controller, runtime]);
  useEffect(() => {if (!updates) return; void updates.load(); return () => updates.dispose();}, [updates]);
  const updateSnapshot = React.useSyncExternalStore(updates?.subscribe ?? noUpdatesSubscription, updates?.getSnapshot ?? noUpdatesSnapshot);
  const update = owner ? ownerUpdateView(updateSnapshot) : undefined;
  // The "Updated …" line's clock, every 30 s (repeat() is the repo's one polling helper).
  useEffect(() => {const clock = repeat(async () => {setNow(serverNow());}, {everyMs: 30_000, backoff: {baseMs: 30_000, maxMs: 30_000}, immediate: false}); return () => clock.stop();}, []);
  const totals = state.totals ?? ownerDerivedTotals(state.sessions);
  const compact = ownerUsesCompactRows(totals);
  // Show all opens every stream in place. The first page is the controller's own read; the rest
  // are the server's next canonical pages, appended without re-ranking anything here.
  const [expanded, setExpanded] = useState(false), [all, setAll] = useState<AllStreams>({items: [], loading: false}), [allRevision, setAllRevision] = useState(0);
  useEffect(() => {
    if (!expanded || !compact) {setAll({items: [], loading: false}); return;}
    let live = true;
    void (async () => {
      setAll(previous => ({items: previous.items, loading: true}));
      try {
        // The rebuilt server answers every stream in one read, so Show all draws the controller's own list.
        const items: readonly AdminSession[] = state.sessions;
        if (live) setAll({items, loading: false});
      } catch (error) {
        if (live) setAll(previous => ({items: previous.items, loading: false, error}));
      }
    })();
    return () => {live = false;};
  }, [expanded, compact, reader, state.sessions, state.nextCursor, allRevision]);
  const streamRows = expanded && all.items.length ? all.items : state.sessions;
  const raw = (id: string) => streamRows.find(session => session.id === id) ?? state.sessions.find(session => session.id === id);
  const run = async (work: () => Promise<void>) => {
    setBusy(true); setActionError(undefined);
    try {await work(); await controller.refresh();} catch (error) {setActionError(error);} finally {setBusy(false);}
  };
  const reload = () => {setActionError(undefined); setAllRevision(value => value + 1); telemetry.reload(); statistics.reload(); void updates?.load(); return controller.refresh();};
  const refresh = () => {void reload();};
  // owner-dashboard 2: pulling down refreshes the page; the ring stays until the Dashboard's own read settles, under the page's top bar.
  const pull = usePullRefresh(reload, setActionError, inline ? 0 : insets.top + 48);
  const entries = useMemo<readonly OwnerStreamEntry[]>(() => state.sessions.map(session =>
    Object.freeze({view: ownerStreamView(session, runtime.text), artwork: runtime.artwork(session.image)})), [state.sessions, runtime]);
  // Compact rows never draw artwork, so the full list maps only the facts each row says.
  const allEntries = useMemo<readonly OwnerStreamEntry[]>(() => expanded ? streamRows.map(session => Object.freeze({view: ownerStreamView(session, runtime.text)})) : [], [expanded, streamRows, runtime]);
  const alerts = useMemo<readonly OwnerAlertView[]>(() => state.alerts.map((alert, index) => ownerAlertView(alert, index, t, defaultI18n.has)), [state.alerts, t]);
  const [fixView, setFixView] = useState<OwnerAlertView>(), [refusal, setRefusal] = useState('');
  const jobs = useMemo<readonly OwnerJobView[]>(() => state.jobs.map(job => ownerJobView(job, t, defaultI18n.has)), [state.jobs, t]);
  const health = ownerHealthLine(state.health, t);
  const rawStream = streamMenu ? raw(streamMenu.id) : undefined;
  const menuItems: readonly MenuEntry[] = rawStream ? [
    // The rebuilt server lets the owner stop a stream with a message (definition 12, What a person can do); it
    // takes no remote pause or seek of someone else's playback, so the menu offers Stop alone, as web does.
    {id: 'stop', label: t('web.nowPlaying.stop'), icon: 'i-stop', destructive: true, onSelect: () => {const view = streamMenu; setStreamMenu(undefined); if (view) {setMessage(''); setStopView(view);}}},
  ] : [];
  // The rebuilt server takes no job commands; the sheet only names the job and its state.
  const jobItems: readonly MenuEntry[] = [];
  const stopSession = stopView ? raw(stopView.id) : undefined;
  const view: OwnerDashboardViewProps = {t, role: owner ? 'owner' : 'administrator', serverName: ports.serverName(), topInset: inline ? 0 : insets.top, inline,
    health: {degraded: health.facts.degraded, status: owner ? health.status : '', line: health.line, ...(health.uptimeSeconds === undefined ? {} : {uptimeSeconds: health.uptimeSeconds})},
    updated: ownerUpdatedLabel(telemetry.reading?.observedAt ?? (state.observedAt ? Date.parse(state.observedAt) : undefined), now, t), loading: state.loading || busy, error: actionError ?? state.error,
    streams: entries, streamTotals: totals, expanded: expanded && compact, allStreams: allEntries, allError: all.error,
    onShowAll: () => setExpanded(true), onShowFewer: () => setExpanded(false), onRetryAll: () => setAllRevision(value => value + 1),
    people: totals.streams ? ownerPeopleLine(totals, t) : t('next.shell.streams', {count: 0}), alerts, jobs,
    update, onCheckUpdate: () => {void updates?.check();},
    onReleaseNotes: () => {const url = update?.notesURL; if (url && controller.lifetime.current()) void Linking.openURL(url).catch(error => {if (controller.lifetime.current()) runtime.reportError(error);});},
    telemetry: telemetry.reading, telemetryError: telemetry.error, healthWindow, healthNote: ownerIsNewServer(health.uptimeSeconds) ? t('next.dashboard.healthLater') : undefined,
    summary: statistics.summary, statisticsError: statistics.error, viewingPeriod,
    onBack: () => ports.context.navigation.back(controller.lifetime.authority), refreshControl: pull, onRetry: refresh,
    onHealthWindow: setHealthWindow, onViewingPeriod: setViewingPeriod,
    onFixPermissions: alertView => {setRefusal(''); setFixView(alertView);},
    onJobMenu: jobView => {setJobMenu(jobView); setActionError(undefined);},
    onStop: streamView => {setMessage(''); setStopView(streamView);}, onStreamMenu: streamView => {setStreamMenu(streamView); setActionError(undefined);}};
  const confirmStop = () => {const target = stopView; if (target) void run(async () => {await reader.stop(target.id, stopMessage(message)); setStopView(undefined);});};
  const confirmFix = () => {
    void (async () => {
      setBusy(true); setRefusal('');
      // A refusal is shown in the server's own words, so the owner sees why nothing changed.
      try {await reader.fixPermissions(); setFixView(undefined); await controller.refresh();}
      catch (error) {if (error instanceof ApiError && error.message) setRefusal(error.message); else setActionError(error);}
      finally {setBusy(false);}
    })();
  };
  return {view, t, busy,
    streamMenu: {open: Boolean(streamMenu), close: () => setStreamMenu(undefined), title: streamMenu?.title,
      description: streamMenu ? `${streamMenu.viewer.username || streamMenu.viewer.name} · ${streamMenu.viewer.device}` : undefined, items: menuItems},
    jobMenu: {open: Boolean(jobMenu), close: () => setJobMenu(undefined), title: jobMenu?.title, description: jobMenu?.subtitle ?? jobMenu?.status, items: jobItems},
    stop: {open: Boolean(stopView), dismiss: () => {if (!busy) setStopView(undefined);}, cancel: () => setStopView(undefined), confirm: confirmStop,
      title: stopSession ? t('web.nowPlaying.stopTitle', {title: stopSession.title || t('web.nowPlaying.untitled')}) : t('web.nowPlaying.stop'),
      description: stopSession ? t('web.nowPlaying.stopBody', {viewer: sessionViewer(stopSession, t)}) : undefined, message, setMessage, maxLength: STOP_MESSAGE_MAX},
    fix: {open: Boolean(fixView), dismiss: () => {if (!busy) setFixView(undefined);}, cancel: () => setFixView(undefined), confirm: confirmFix, refusal,
      description: fixView?.owner && fixView.path ? dataFolderConfirmText(fixView.owner, fixView.path) : undefined}};
}
