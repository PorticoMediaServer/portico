import React, {useEffect, useMemo, useState} from 'react';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {PlaySummary} from '../../../../../../packages/client-core/src/play-history';
import type {TelemetryReading} from '../../../../../../packages/client-core/src/telemetry';
import type {AdminSession} from '../../../../../../packages/client-core/src/playback-v1/types';
import {offersScrubber, sessionViewer, STOP_MESSAGE_MAX, stopMessage, transportOffers, type TransportCommand} from '../../../../../../packages/client-core/src/server-admin/now-playing';
import type {Job} from '../../../../../../packages/client-core/src/console';
import {View} from 'react-native';
import {Button, TextField} from '../../../kit/controls';
import {BottomSheet, Menu, type MenuEntry} from '../../../kit/overlays';
import {useTheme} from '../../../kit/theme';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeDashboardController} from '../dashboard-controller';
import type {NativeSettingsLivePorts} from '../ports';
import {createNativeOwnerDashboardReader, type NativeOwnerDashboardReader} from './reader';
import {
  appendOwnerSessions, ownerAlertView, ownerDerivedTotals, ownerHealthLine, ownerIsNewServer, ownerJobView, ownerPeopleLine, ownerStreamView, ownerUpdatedLabel, ownerUsesCompactRows,
  type OwnerAlertView, type OwnerHealthWindow, type OwnerJobView, type OwnerStreamView, type OwnerViewingPeriod,
} from './model';
import {OwnerMoveSheet} from './move-sheet';
import {OwnerDashboardPage} from './page';
import type {OwnerStreamEntry} from './streams';

type TelemetryState = Readonly<{reading?: TelemetryReading; error?: unknown}>;
type SummaryState = Readonly<{summary?: PlaySummary; error?: unknown}>;
type AllStreams = Readonly<{items: readonly AdminSession[]; loading: boolean; error?: unknown}>;

function useOwnerTelemetry(reader: NativeOwnerDashboardReader, window: OwnerHealthWindow, enabled: boolean) {
  const [state, setState] = useState<TelemetryState>({}), [revision, setRevision] = useState(0);
  useEffect(() => {
    if (!enabled) {setState({}); return;}
    let live = true;
    const load = () => {void reader.telemetry(window).then(reading => {if (live) setState({reading});}, error => {if (live) setState(current => ({...current, error}));});};
    load();
    const timer = setInterval(load, window === '10m' ? 15_000 : 60_000);
    return () => {live = false; clearInterval(timer);};
  }, [reader, window, enabled, revision]);
  return Object.freeze({...state, reload: () => setRevision(value => value + 1)});
}

function useOwnerStatistics(reader: NativeOwnerDashboardReader, period: OwnerViewingPeriod, enabled: boolean) {
  const [state, setState] = useState<SummaryState>({}), [revision, setRevision] = useState(0);
  useEffect(() => {
    if (!enabled) {setState({}); return;}
    let live = true;
    void reader.playSummary(period).then(summary => {if (live) setState({summary});}, error => {if (live) setState(current => ({...current, error}));});
    return () => {live = false;};
  }, [reader, period, enabled, revision]);
  return Object.freeze({...state, reload: () => setRevision(value => value + 1)});
}

const transportIcon: Record<TransportCommand, 'i-pause' | 'i-play' | 'i-rew' | 'i-ffw' | 'i-restart'> = {pause: 'i-pause', resume: 'i-play', back: 'i-rew', forward: 'i-ffw', startOver: 'i-restart'};

/** Binds the one authorized dashboard snapshot to the drawn phone page; it never chooses a role. */
export function NativeOwnerDashboardPhone({controller, ports, runtime, inline = false}: {controller: NativeDashboardController; ports: NativeSettingsLivePorts; runtime: NativeShellRuntime; inline?: boolean}) {
  const state = React.useSyncExternalStore(controller.subscribe, controller.getSnapshot);
  const theme = useTheme(), insets = useSafeAreaInsets(), t = runtime.text.t;
  const reader = useMemo(() => createNativeOwnerDashboardReader(controller.lifetime, ports), [controller, ports]);
  useEffect(() => () => reader.dispose(), [reader]);
  const [healthWindow, setHealthWindow] = useState<OwnerHealthWindow>('1h'), [viewingPeriod, setViewingPeriod] = useState<OwnerViewingPeriod>('7d');
  const [now, setNow] = useState(() => Date.now());
  const [streamMenu, setStreamMenu] = useState<OwnerStreamView>(), [stopView, setStopView] = useState<OwnerStreamView>(), [message, setMessage] = useState('');
  const [moveView, setMoveView] = useState<OwnerStreamView>();
  const [jobMenu, setJobMenu] = useState<OwnerJobView>(), [busy, setBusy] = useState(false), [actionError, setActionError] = useState<unknown>();
  const owner = state.admission === 'owner';
  const telemetry = useOwnerTelemetry(reader, healthWindow, owner), statistics = useOwnerStatistics(reader, viewingPeriod, owner);
  useEffect(() => {const timer = setInterval(() => setNow(Date.now()), 30_000); return () => clearInterval(timer);}, []);
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
        let items: readonly AdminSession[] = state.sessions.slice(0, 10), cursor = state.nextCursor;
        while (cursor) {
          const page = await reader.sessions(cursor);
          if (!live) return;
          items = appendOwnerSessions(items, page.items);
          cursor = page.nextCursor;
        }
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
  const refresh = () => {setActionError(undefined); setAllRevision(value => value + 1); telemetry.reload(); statistics.reload(); void controller.refresh();};
  const entries = useMemo<readonly OwnerStreamEntry[]>(() => state.sessions.map(session => {
    const art = session.item?.art.find(value => value.role === 'backdrop') ?? session.item?.art[0];
    return Object.freeze({view: ownerStreamView(session, runtime.text), ...(art ? {artwork: runtime.artwork(`/v1/items/${encodeURIComponent(art.id)}/art/${encodeURIComponent(art.kind)}`)} : {})});
  }), [state.sessions, runtime]);
  // Compact rows never draw artwork, so the full list maps only the facts each row says.
  const allEntries = useMemo<readonly OwnerStreamEntry[]>(() => expanded ? streamRows.map(session => Object.freeze({view: ownerStreamView(session, runtime.text)})) : [], [expanded, streamRows, runtime]);
  const alerts = useMemo<readonly OwnerAlertView[]>(() => state.alerts.map(alert => ownerAlertView(alert, t, defaultI18n.has)), [state.alerts, t]);
  const jobs = useMemo<readonly OwnerJobView[]>(() => state.jobs.map(job => ownerJobView(job, t, defaultI18n.has)), [state.jobs, t]);
  const health = ownerHealthLine(state.health, state.storage, t);
  const rawStream = streamMenu ? raw(streamMenu.id) : undefined;
  const transportWords: Record<TransportCommand, string> = {pause: t('action.pause'), resume: t('entry.resume'), back: t('player.skipBackSeconds', {seconds: 30}), forward: t('player.skipForwardSeconds', {seconds: 30}), startOver: t('action.startOver')};
  const menuItems: readonly MenuEntry[] = rawStream ? [
    ...transportOffers(rawStream).map((command): MenuEntry => ({id: command, label: transportWords[command], icon: transportIcon[command], disabled: busy, onSelect: () => {const view = streamMenu; setStreamMenu(undefined); if (view) void run(() => reader.transport(view.id, command));}})),
    ...(offersScrubber(rawStream) ? [{id: 'move', label: t('web.nowPlaying.moveTo'), icon: 'i-clock' as const, disabled: busy, onSelect: () => {const view = streamMenu; setStreamMenu(undefined); if (view) setMoveView(view);}}] : []),
    {id: 'stop', label: t('web.nowPlaying.stop'), icon: 'i-stop', destructive: true, onSelect: () => {const view = streamMenu; setStreamMenu(undefined); if (view) {setMessage(''); setStopView(view);}}},
  ] : [];
  const rawJob = jobMenu ? state.jobs.find(job => job.id === jobMenu.id) : undefined;
  const jobLabels: Record<Job['actions'][number], string> = {pause: t('action.pause'), resume: t('entry.resume'), retry: t('next.shell.tryAgain'), cancel: t('action.cancel')};
  const jobItems: readonly MenuEntry[] = rawJob ? rawJob.actions.map(action => ({id: action, label: jobLabels[action], destructive: action === 'cancel', disabled: busy, onSelect: () => {const target = jobMenu; setJobMenu(undefined); if (target) void run(() => reader.jobCommand(rawJob, action));}})) : [];
  const stopSession = stopView ? raw(stopView.id) : undefined;
  const moveSession = moveView ? raw(moveView.id) : undefined;
  return <>
    <OwnerDashboardPage t={t} role={owner ? 'owner' : 'administrator'} serverName={ports.serverName()} topInset={inline ? 0 : insets.top} inline={inline}
      health={{degraded: health.facts.degraded, status: health.status, line: health.line, ...(health.uptimeSeconds === undefined ? {} : {uptimeSeconds: health.uptimeSeconds})}}
      updated={ownerUpdatedLabel(telemetry.reading?.observedAt ?? state.health?.observedAt, now, t)} loading={state.loading || busy} error={actionError ?? state.error}
      streams={entries} streamTotals={totals} expanded={expanded && compact} allStreams={allEntries} allError={all.error}
      onShowAll={() => setExpanded(true)} onShowFewer={() => setExpanded(false)} onRetryAll={() => setAllRevision(value => value + 1)}
      people={totals.streams ? ownerPeopleLine(totals, t) : t('next.shell.streams', {count: 0})} alerts={alerts} jobs={jobs}
      telemetry={telemetry.reading} telemetryError={telemetry.error} healthWindow={healthWindow} healthNote={ownerIsNewServer(health.uptimeSeconds) ? t('next.dashboard.healthLater') : undefined}
      summary={statistics.summary} statisticsError={statistics.error} viewingPeriod={viewingPeriod}
      onBack={() => ports.context.navigation.back(controller.lifetime.authority)} onRefresh={refresh} onRetry={refresh}
      onHealthWindow={setHealthWindow} onViewingPeriod={setViewingPeriod}
      onAcknowledge={view => {const alert = state.alerts.find(value => value.id === view.id); if (alert) void run(() => reader.acknowledge(alert));}}
      onFixPermissions={view => {const alert = state.alerts.find(value => value.id === view.id); if (alert) void run(async () => {await reader.fixPermissions(ports.requestId()); await reader.acknowledge(alert);});}}
      onJobMenu={view => {setJobMenu(view); setActionError(undefined);}}
      onStop={view => {setMessage(''); setStopView(view);}} onStreamMenu={view => {setStreamMenu(view); setActionError(undefined);}}/>
    <Menu open={Boolean(streamMenu)} onOpenChange={open => {if (!open) setStreamMenu(undefined);}} presentation="sheet" testID="owner-stream-menu"
      title={streamMenu?.title} description={streamMenu ? `${streamMenu.viewer.username || streamMenu.viewer.name} · ${streamMenu.viewer.device}` : undefined} items={menuItems}/>
    <Menu open={Boolean(jobMenu)} onOpenChange={open => {if (!open) setJobMenu(undefined);}} presentation="sheet" testID="owner-job-menu" title={jobMenu?.title} description={jobMenu?.subtitle ?? jobMenu?.status} items={jobItems}/>
    <OwnerMoveSheet session={moveSession} t={t} busy={busy} onCancel={() => setMoveView(undefined)}
      onMove={position => {const view = moveView; if (view) void run(async () => {await reader.move(view.id, position); setMoveView(undefined);});}}/>
    <BottomSheet open={Boolean(stopView)} onOpenChange={open => {if (!open && !busy) setStopView(undefined);}} testID="owner-stop-sheet" keyboardAvoiding
      title={stopSession ? t('web.nowPlaying.stopTitle', {title: stopSession.title || t('web.nowPlaying.untitled')}) : t('web.nowPlaying.stop')}
      description={stopSession ? t('web.nowPlaying.stopBody', {viewer: sessionViewer(stopSession, t)}) : undefined}
      footer={<View style={{flexDirection: 'row', gap: 8}}><Button variant="glass" disabled={busy} onPress={() => setStopView(undefined)}>{t('action.cancel')}</Button><Button variant="glass" disabled={busy} loading={busy} textStyle={{color: theme.colors.record}} onPress={() => {const view = stopView; if (view) void run(async () => {await reader.stop(view.id, stopMessage(message)); setStopView(undefined);});}}>{t('web.nowPlaying.stopConfirm')}</Button></View>}>
      <TextField testID="owner-stop-message" label={t('web.nowPlaying.messageLabel')} description={t('web.nowPlaying.messageHelp')} placeholder={t('web.nowPlaying.messagePlaceholder')} value={message} onChangeText={setMessage} maxLength={STOP_MESSAGE_MAX} multiline/>
    </BottomSheet>
  </>;
}
