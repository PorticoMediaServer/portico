import {serverNow} from '../../../../../../packages/client-core/src/server-clock';
import {useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {repeat} from '../../../../../../packages/client-core/src/timer';
import type {TelemetryReading} from '../../../../../../packages/client-core/src/telemetry';
import type {AdminSession} from '../../../../../../packages/client-core/src/playback-v1/types';
import {Linking} from 'react-native';
import {ApiError} from '../../../../../../packages/client-core/src/http';
import {stopMessage} from '../../../../../../packages/client-core/src/server-admin/now-playing';
import {SettingsUpdatesSource, type UpdatesSnapshot} from '../../../../../../packages/client-core/src/server-admin/updates-source';
import type {MenuEntry} from '../../../kit/overlays';
import {usePullRefresh} from '../../../kit/states/usePullRefresh';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeDashboardController} from '../../../screens/settings-live/dashboard-controller';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import {createNativeOwnerDashboardReader, type NativeOwnerDashboardReader} from '../../../screens/settings-live/owner-dashboard/reader';
import {
  appendOwnerSessions, ownerAlertView, ownerDerivedTotals, ownerHealthLine, ownerIsNewServer, ownerJobView, ownerPeopleLine, ownerStreamView, ownerUpdatedLabel, ownerUsesCompactRows,
  type OwnerAlertView, type OwnerHealthWindow, type OwnerJobView, type OwnerStreamView, type OwnerViewing, type OwnerViewingPeriod,
} from '../../../screens/settings-live/owner-dashboard/model';
import {ownerUpdateView} from '../../../kit/settings/dashboard/update-card';
import type {OwnerStreamEntry} from '../../../kit/settings/dashboard/streams';

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


/** Binds the one authorized dashboard snapshot to the drawn phone page; it never chooses a role. */
export function useOwnerDashboard({controller, ports, runtime, inline = false}: {controller: NativeDashboardController; ports: NativeSettingsLivePorts; runtime: NativeShellRuntime; inline?: boolean}) {
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot);
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
  const updateSnapshot = useSyncExternalStore(updates?.subscribe ?? noUpdatesSubscription, updates?.getSnapshot ?? noUpdatesSnapshot);
  const update = owner ? ownerUpdateView(updateSnapshot) : undefined;
  useEffect(() => repeat(async () => {setNow(serverNow());}, {everyMs: 30_000, immediate: false, backoff: {baseMs: 30_000, maxMs: 30_000}}).stop, []);
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
  const opened = useRef({stopView, fixView}), flight = useRef(false);
  useLayoutEffect(() => {opened.current = {stopView, fixView};}, [stopView, fixView]);
  const confirmStop = async () => {
    if (!stopView || !stopSession || opened.current.stopView !== stopView || flight.current || !controller.lifetime.current()) return;
    flight.current = true;
    setBusy(true);
    setActionError(undefined);
    try {
      await reader.stop(stopView.id, stopMessage(message));
      if (controller.lifetime.current()) {setStopView(undefined); await controller.refresh();}
    } catch (error) {
      if (controller.lifetime.current()) setActionError(error);
    } finally {
      flight.current = false;
      if (controller.lifetime.current()) setBusy(false);
    }
  };
  const confirmPermissions = async () => {
    if (!fixView || opened.current.fixView !== fixView || flight.current || !controller.lifetime.current()) return;
    flight.current = true;
    setBusy(true);
    setRefusal('');
    try {
      await reader.fixPermissions();
      if (controller.lifetime.current()) {setFixView(undefined); await controller.refresh();}
    } catch (error) {
      if (!controller.lifetime.current()) return;
      if (error instanceof ApiError && error.message) setRefusal(error.message);
      else setActionError(error);
    } finally {
      flight.current = false;
      if (controller.lifetime.current()) setBusy(false);
    }
  };
  const releaseNotes = () => {
    const url = update?.notesURL;
    if (url && controller.lifetime.current()) void Linking.openURL(url).catch(error => {
      if (controller.lifetime.current()) runtime.reportError(error);
    });
  };
  return {t, owner, state, insets, health, telemetry, statistics, healthWindow, setHealthWindow, viewingPeriod, setViewingPeriod, now,
    expanded, setExpanded, compact, all, setAllRevision, entries, allEntries, totals, alerts, jobs, update, updates, busy, pull,
    actionError, setActionError, refresh, releaseNotes, confirmStop, confirmPermissions, streamMenu, setStreamMenu, jobMenu, setJobMenu, stopView, setStopView,
    message, setMessage, stopSession, fixView, setFixView, refusal, setRefusal, menuItems, jobItems};
}
