import {useCallback, useEffect, useId, useLayoutEffect, useMemo, useRef, useState, type CSSProperties, type ReactNode} from 'react';
import {jobName} from '@core/server-admin/console-catalogue.ts';
import type {MessageId} from '@i18n';
import {DASHBOARD_LOAD_RANGES, DASHBOARD_LOAD_SERIES, type DashboardStatus} from '@core/admin/dashboard.ts';
import type {TelemetryReading} from '@core/telemetry.ts';
import {defaultI18n} from '@i18n';
import {Button} from '../../kit/controls';
import {ToastHost, type ToastData} from '../../kit/overlays';
import {Icon} from '../../kit/Icon';
import type {DashboardAlert, DashboardBindings, DashboardServerHeader, DashboardSessions} from './bindings';
import {fmtMbps, fmtPct} from './format';
import {loadTileValue, orderedSessions, sessionStopTitle} from './model';
import {useDashboardAccounts, useDashboardAuthority, useDashboardRead, type ReadState} from './use-read';
import {Range, ReadFeedback, Section, t} from './shared';
import type {ChartSeries} from './Chart';
import {Chart} from './Chart';
import {SessionCard} from './SessionCard';
import {BusySessions} from './BusySessions';
import {PhoneHealth} from './PhoneHealth';
import {DashboardQuiet} from './DashboardQuiet';
import quietStyles from './dashboard-quiet.module.css';
import {Analytics} from './Analytics';
import {useDashboardReport, type DashboardReportController} from './use-report';
import {readNowPlaying} from './pages';
import {PhonePage, PhoneStatistics, type DashboardPhoneChrome} from './phone';
import {ProgressRing} from '../../kit/shell/Activity';
import {useLayoutMotion} from '../../kit/layout/motion';
import {useLayoutPresence} from '../../kit/layout/presence';
import {useListRemovalFocus} from '../../kit/layout/use-list-removal-focus';
import styles from './dashboard.module.css';
const bindingLifetimes = new WeakMap<DashboardBindings, number>();
let nextBindingLifetime = 0;
function bindingLifetime(bindings: DashboardBindings) {let id = bindingLifetimes.get(bindings);if (id === undefined) {id = ++nextBindingLifetime;bindingLifetimes.set(bindings, id);}return id;}

export type DashboardScreenProps = Readonly<{
  bindings: DashboardBindings; embedded?: boolean; phone?: boolean; chrome?: DashboardPhoneChrome;
  initialSection?: 'streams' | 'activity' | 'updates';
}>;
export function DashboardScreen(props: DashboardScreenProps) {
  const b = props.bindings;
  return <DashboardView key={JSON.stringify([b.scope.serverId, b.scope.viewerId, b.role])} {...props} />;
}
function DashboardView({bindings: b, embedded = false, initialSection, phone = false, chrome}: DashboardScreenProps) {
  const current = useDashboardAuthority(b);
  const ref = useRef<HTMLDivElement>(null), allowed = current && b.isMainProfile() && (b.role === 'owner' || b.role === 'admin');
  const statusLoad = useCallback((signal: AbortSignal) => b.read.status(signal), [b]);
  const status = useDashboardRead(b, statusLoad, allowed && b.role === 'owner', 10000);
  const sessionsLoad = useCallback((signal: AbortSignal) => readNowPlaying(b, signal), [b]);
  const sessions = useDashboardRead(b, sessionsLoad, allowed, 10000);
  const liveLoad = useCallback((signal: AbortSignal) => b.read.telemetry('live', signal), [b]);
  const live = useDashboardRead(b, liveLoad, allowed && b.role === 'owner' && !phone, 2000);
  const [healthRange, setHealthRange] = useState<'10m' | '1h' | '24h'>('1h');
  const healthLoad = useCallback((signal: AbortSignal) => b.read.telemetry(healthRange, signal), [b, healthRange]);
  const health = useDashboardRead(b, healthLoad, allowed && b.role === 'owner' && phone, 2000);
  const serverLoad = useCallback((signal: AbortSignal) => b.read.server(signal), [b]);
  const server = useDashboardRead<DashboardServerHeader>(b, serverLoad, allowed && b.role === 'owner' && phone, 60000);
  const report = useDashboardReport(b, allowed && b.role === 'owner');
  const accounts = useDashboardAccounts(b, allowed && b.role === 'owner' && !phone);
  const [updatedAt, setUpdatedAt] = useState(() => Date.now());
  useEffect(() => {if (sessions.value && !sessions.loading) setUpdatedAt(Date.now());}, [sessions.value, sessions.loading]);
  useEffect(() => {if (!phone && initialSection) ref.current?.querySelector(`[data-dashboard-section="${initialSection}"]`)?.scrollIntoView({block: 'start'});}, [initialSection, phone]);
  if (!allowed) return null;
  const failed = sessions.failed || b.role === 'owner' && (status.failed || (phone ? health.failed : live.failed || accounts.failed) || report.state.failed);
  const retry = () => {sessions.refresh(); if (b.role === 'owner') {status.refresh(); if (phone) health.refresh(); else live.refresh(); report.state.refresh(); accounts.refresh(); server.refresh();}};
  /* Frames 10 and 10b: Now playing, then Alerts beside Background activity, then Health and the statistics, quiet or busy. */
  const groupedQuiet = !phone && !failed && b.role === 'owner';
  const sections = phone
    ? <><div data-dashboard-section="streams"><NowPlaying bindings={b} state={sessions} phone /></div>
        {b.role === 'owner' && <><Status bindings={b} status={status} phone /><Tasks status={status} phone /><PhoneHealth reading={health.value} loading={health.loading} failed={health.failed} range={healthRange} onRangeChange={range => {if (b.isCurrent()) setHealthRange(range);}} onRetry={health.refresh} /><div data-dashboard-section="activity"><PhoneStatistics bindings={b} report={report} /></div></>}</>
    : <>{b.role === 'owner' && <Status bindings={b} status={status} />}
        <div data-dashboard-section="streams"><NowPlaying bindings={b} state={sessions} /></div>
        {b.role === 'owner' && <><div className={quietStyles.slot} data-dashboard-quiet-slot="load"><Load bindings={b} report={report} live={live} /></div><div data-dashboard-section="activity"><Analytics bindings={b} report={report} accounts={accounts} /></div><div className={quietStyles.slot} data-dashboard-quiet-slot="tasks"><Tasks status={status} /></div></>}</>;
  if (phone) return <PhonePage chrome={chrome ?? {serverName: '', audience: b.role === 'owner' ? 'owner' : 'administrators'}} server={server.value}
    updatedAt={updatedAt} onBack={() => {if (history.length > 1) history.back(); else b.effects.openHome();}} onRefresh={retry} initialSection={initialSection}>
    {failed && !sessions.value ? <ReadFeedback openHome={b.effects.openHome} height={420} state={{failed: true, loading: false, refresh: retry}} /> : sections}
  </PhonePage>;
  return <div className={`${styles.dashboard} ${groupedQuiet ? quietStyles.grouped : ''}`} data-quiet-embedded={groupedQuiet && embedded || undefined} ref={ref}>{!embedded && <h2 className={styles.pageTitle}>{t('next.dashboard.title')}</h2>}
    {failed ? <ReadFeedback openHome={b.effects.openHome} state={{failed: true, loading: false, refresh: retry}} /> : sections}
  </div>;
}
type SessionsState = ReadState<DashboardSessions> & {refresh: () => void};
type LiveState = ReadState<TelemetryReading> & {refresh: () => void};
type SessionListProps = Readonly<{
  sessions: ReturnType<typeof orderedSessions>; bindings: DashboardBindings; phone: boolean;
  empty: ReactNode; onRefresh: (session: ReturnType<typeof orderedSessions>[number], removedId?: string) => void;
}>;
/** Presentation only: the shared list presence keeps the drawn session rows and their
 * departure paint. It owns no session authority and calls no command. */
function SessionList({sessions, bindings: b, phone, empty, onRefresh}: SessionListProps) {
  const motion = useLayoutMotion();
  const sessionKey = useCallback((session: {id: string}) => session.id, []);
  const focus = useListRemovalFocus(b);
  const presence = useLayoutPresence(sessions, sessionKey, motion.reduced, 180, 320, focus);
  const attach = useCallback((element: HTMLDivElement | null) => {focus.root.current = element;(presence.root as {current: HTMLDivElement | null}).current = element;}, [focus.root, presence.root]);
  return <div className={phone ? styles.phoneSessionStack : styles.sessionStack} ref={attach} tabIndex={-1} aria-label={t('next.dashboard.now')}>
    {presence.present.map(session => {
      const key = session.id, leaving = presence.leaving.has(key);
      return <div key={key} className={styles.sessionSlot} data-layout-item={key} data-session-id={key} data-leaving={leaving || undefined} inert={leaving} aria-hidden={leaving || undefined}>
        <SessionCard session={session} bindings={b} layout={phone ? 'phone' : 'desktop'} retained={leaving} refresh={id => onRefresh(session, id)} />
      </div>;
    })}
    {!presence.present.length && empty}
  </div>;
}

function NowPlaying({bindings: b, state, phone = false}: {bindings: DashboardBindings; state: SessionsState; phone?: boolean}) {
  const [toast, showToast] = useState<ToastData | null>(null);
  const sessions = useMemo(() => orderedSessions(state.value?.items ?? []), [state.value?.items]), totals = state.value?.totals;
  const refresh = (session: ReturnType<typeof orderedSessions>[number], id?: string) => {
    if (id) showToast({id, message: t('next.dashboard.stopped', {title: sessionStopTitle(session)}), icon: 'i-check', durationMs: 3200});
    state.refresh();
  };
  const empty = phone
    ? <div className={styles.phoneCalm}><span className={styles.phoneCalmLead}><Icon name="i-play" size={18} /></span><b>{t('next.dashboard.nobody')}</b>{b.role === 'owner' && <button type="button" className={styles.phoneCalmLink} onClick={b.effects.openHistory}>{t('next.dashboard.history')}</button>}</div>
    : <p className={styles.empty}>{t('next.dashboard.nobody')}</p>;
  const count = totals && (phone
    ? totals.streams > 0
      ? <><span>{t('next.dashboard.peopleCount', {count: totals.people})}</span><i className={styles.dotSep} aria-hidden="true" /><small>{t('next.dashboard.locations', {home: totals.atHome, away: totals.away})}</small></>
      : <span>{t('next.shell.streams', {count: 0})}</span>
    : totals.streams > 0 ? <><span>{t('next.dashboard.streams', {count: totals.streams})}</span><small>{t('next.dashboard.locations', {home: totals.atHome, away: totals.away})}</small></>
      : !state.loading && !state.failed && state.value && totals.streams === 0 && state.value.items.length === 0 ? <span>{t('next.shell.streams', {count: 0})}</span> : null);
  return <Section phone={phone} title={t('next.dashboard.now')} extra={count}>
    <ReadFeedback openHome={b.effects.openHome} state={state} />{state.value && (phone
      ? <BusySessions key={bindingLifetime(b)} sessions={sessions} totals={totals} bindings={b} refresh={refresh} empty={empty}/>
      : <SessionList key={bindingLifetime(b)} sessions={sessions} bindings={b} phone={phone} empty={empty} onRefresh={refresh} />)}
    <ToastHost toast={toast} onDismiss={() => showToast(null)} />
  </Section>;
}
const seriesNames: Record<string, MessageId> = {awayMbps: 'next.dashboard.away', homeMbps: 'next.dashboard.home', processCPU: 'next.dashboard.portico', cpu: 'next.dashboard.system', processMemory: 'next.dashboard.portico', memory: 'next.dashboard.system', hardwareConversions: 'next.dashboard.hardware', softwareConversions: 'next.dashboard.software'};
type LoadKind = keyof typeof DASHBOARD_LOAD_SERIES;
const loadKinds = Object.keys(DASHBOARD_LOAD_SERIES) as LoadKind[];
function loadSeries(reading: TelemetryReading | undefined, kind: LoadKind): ChartSeries[] {
  if (!reading) return [];
  return DASHBOARD_LOAD_SERIES[kind].metrics.filter(metric => reading.status[metric]?.status !== 'unavailable' && reading.series[metric]?.length).map(metric => ({key: metric, label: t(seriesNames[metric]), points: reading.series[metric]}));
}
function Load({bindings: b, report, live, phone = false}: {bindings: DashboardBindings; report: DashboardReportController; live: LiveState; phone?: boolean}) {
  const [kind, setKind] = useState<LoadKind>('bandwidth'), id = useId(), range = report.query.window;
  const historical = {...report.state, value: report.state.value?.load};
  useEffect(() => {if (live.value && !loadSeries(live.value, kind).length) {const available = loadKinds.find(k => loadSeries(live.value, k).length); if (available) setKind(available);}}, [live.value, kind]);
  const detail = range === 'live' ? live : historical, series = loadSeries(detail.value, kind);
  const format = (value: number, selected = kind) => selected === 'bandwidth' ? fmtMbps(value) : selected === 'conversions' ? t(range === 'live' ? 'next.dashboard.running' : 'next.dashboard.averageCount', {value: range === 'live' ? String(Math.round(value)) : value.toFixed(1)}) : fmtPct(value);
  return <Section phone={phone} title={t('next.dashboard.load')}>
    {phone && <div className={styles.phoneRange}><Range phone load label={t('next.dashboard.load')} value={range} values={DASHBOARD_LOAD_RANGES} onChange={value => report.change({window: value as typeof range})} /></div>}
    <ReadFeedback height={118} openHome={b.effects.openHome} state={live} /><div className={phone ? styles.phoneTiles : styles.tiles} role="tablist" aria-label={t('next.dashboard.load')}>{loadKinds.map(k => {
      const available = loadSeries(live.value, k); if (!available.length) return null;
      const current = loadTileValue(live.value!, k);
      return <button type="button" className={styles.tile} key={k} id={`${id}-${k}`} role="tab" aria-selected={kind === k} aria-controls={`${id}-panel`} tabIndex={kind === k ? 0 : -1} onClick={() => setKind(k)} onKeyDown={event => {
        if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return; event.preventDefault();
        const keys = loadKinds.filter(key => loadSeries(live.value, key).length), next = keys[(keys.indexOf(k) + (event.key === 'ArrowLeft' ? keys.length - 1 : 1)) % keys.length]; setKind(next); document.getElementById(`${id}-${next}`)?.focus();
      }}><span>{t(`next.dashboard.${k}`)}</span>{current !== undefined && <strong>{k === 'conversions' ? String(Math.round(current)) : defaultI18n.number(current, {minimumFractionDigits: 1, maximumFractionDigits: 1})}<small>{t(k === 'bandwidth' ? 'next.dashboard.unit.mbps' : k === 'conversions' ? 'next.dashboard.unit.running' : 'next.dashboard.unit.percent')}</small></strong>}<small>{available.map(s => <span key={s.key} data-series={s.key}><i />{k === 'bandwidth' ? `${s.label} ${s.points.at(-1)!.v.toFixed(1)}` : k === 'conversions' ? t(s.key === 'hardwareConversions' ? 'next.dashboard.hardwareCount' : 'next.dashboard.processorCount', {value: Math.round(s.points.at(-1)!.v)}) : s.key === 'cpu' || s.key === 'memory' ? t('next.dashboard.systemCurrent', {value: s.points.at(-1)!.v.toFixed(1)}) : t('next.dashboard.portico')}</span>)}</small><Chart interval={live.value!} kind={k} title={t(`next.dashboard.${k}`)} series={available} format={n => format(n, k)} step={k === 'conversions'} compact /></button>;
    })}</div>
    <div className={`${styles.card} ${styles.loadDetail}`} style={{'--load-index': loadKinds.indexOf(kind)} as CSSProperties} role="tabpanel" id={`${id}-panel`} aria-labelledby={`${id}-${kind}`}><div className={styles.heading}><h4>{t(`next.dashboard.${kind}`)}</h4>{!phone && <div className={styles.filters}><Range load value={range} values={DASHBOARD_LOAD_RANGES} onChange={value => report.change({window: value as typeof range})} /></div>}</div>
      <ReadFeedback height={250} openHome={b.effects.openHome} state={detail} />{detail.value && <Chart interval={detail.value} kind={kind} title={t(`next.dashboard.${kind}`)} series={series} format={format} formatAverage={value => kind === 'conversions' ? value.toFixed(1) : format(value)} step={kind === 'conversions' && range === 'live'} />}
    </div>
  </Section>;
}
const alertCopy: Record<string, MessageId> = {update_available: 'next.dashboard.updateAvailable', upload_budget_exhausted: 'next.dashboard.uploadAlert', state_permissions: 'next.dashboard.permissionsAlert', storage_nearly_full: 'next.dashboard.storageAlert', backup_failed: 'next.dashboard.backupAlert', library_unreachable: 'next.dashboard.sourceAlert', remote_unreachable: 'next.dashboard.sourceAlert', secure_renewal_failed: 'next.dashboard.certificateAlert'};
const alertDestinations: Record<string, {section: string; label: MessageId}> = {
  streaming: {section: 'streaming', label: 'next.dashboard.streaming'},
  networking: {section: 'remote', label: 'next.dashboard.remote'},
  storage: {section: 'storage', label: 'next.dashboard.storage'},
  backups: {section: 'storage', label: 'next.dashboard.storage'},
  'library-source': {section: 'libraries', label: 'next.dashboard.libraries'},
  'remote-source': {section: 'remote-sources', label: 'next.dashboard.remoteSources'},
};
type StatusState = ReadState<DashboardStatus> & {refresh: () => void};
function Status({bindings: b, status, phone = false}: {bindings: DashboardBindings; status: StatusState; phone?: boolean}) {
  const quiet = !status.loading && !status.failed && !!status.value && status.value.alerts.length === 0;
  const alerts = status.value?.alerts.filter(alert => alertCopy[alert.code] && (alert.code !== 'update_available' || !!alert.params.latest) && (alert.code !== 'upload_budget_exhausted' || alert.count !== undefined)) ?? [];
  const [busy, setBusy] = useState<string | undefined>();
  const retry = async (alert: DashboardAlert) => {if (!b.effects.retryCertificate || busy) return; setBusy(alert.code); try {await b.effects.retryCertificate(); if (b.isCurrent()) status.refresh();} catch (error) {b.effects.reportError(error);} finally {setBusy(undefined);}};
  const action = (alert: DashboardAlert, update: boolean, destination: {section: string; label: MessageId}) => <>
    {alert.code === 'secure_renewal_failed' && b.effects.retryCertificate && <Button size="xs" variant="glass" disabled={!!busy} onClick={() => void retry(alert)}>{t('next.dashboard.retry')}</Button>}
    {!update && <Button size="xs" onClick={() => b.effects.openSettings(destination.section)}>{t(destination.label)}{!phone && <Icon name="i-right" size={16} />}</Button>}
  </>;
  if (phone) return <>{status.loading || status.failed || !!status.value ? <div data-dashboard-section="updates">
    <Section phone title={t('next.dashboard.alerts')} extra={alerts.length ? <span>{defaultI18n.number(alerts.length)}</span> : undefined}>
      <ReadFeedback height={56} openHome={b.effects.openHome} state={status} />
      {quiet && <DashboardQuiet kind="alerts" phone />}
      {!!alerts.length && <div className={styles.phoneAlerts}>{alerts.map((alert, index) => {
        const update = alert.action.kind === 'update', destination = alertDestinations[alert.action.kind] ?? {section: 'troubleshooting', label: 'next.dashboard.troubleshooting' as const}, text = t(alertCopy[alert.code], {version: alert.params.latest ?? '', count: alert.count ?? 0});
        const severity = alert.severity.toLowerCase(), severityKey = ['warning', 'critical', 'error', 'info', 'notice'].includes(severity) ? severity : 'info';
        return <article className={styles.phoneAlert} key={`${alert.code}-${index}`} data-major={alert.major || undefined} data-info={update || undefined}>
          <span className={styles.phoneSeverity} data-severity={severityKey}>{t(`server.severity.${severityKey}` as MessageId)}</span>
          <b>{text}</b>{alert.params.reason && <p>{alert.params.reason}</p>}{alert.params.subject && <p>{alert.params.subject}</p>}
          <div className={styles.phoneAlertActions}>{action(alert, update, destination)}</div>
        </article>;
      })}</div>}
    </Section></div> : null}</>;
  return <>{(status.loading || status.failed || !!alerts.length || quiet) && <div data-dashboard-section="updates">{quiet && <Section title={t('next.dashboard.alerts')}><DashboardQuiet kind="alerts" /></Section>}<ReadFeedback height={56} openHome={b.effects.openHome} state={status} />{!!alerts.length && <section className={styles.alerts} aria-label={t('next.dashboard.alerts')}>{alerts.map((alert, index) => {
    const update = alert.action.kind === 'update', destination = alertDestinations[alert.action.kind] ?? {section: 'troubleshooting', label: 'next.dashboard.troubleshooting' as const}, text = t(alertCopy[alert.code], {version: alert.params.latest ?? '', count: alert.count ?? 0});
    return <div className={styles.alert} key={`${alert.code}-${index}`} data-major={alert.major || undefined}><span className={styles.alertIcon} data-info={update || undefined}><Icon name={update ? 'i-arrow-up' : 'i-alert'} size={18} /></span><b>{text}</b>{alert.params.reason && <span>{alert.params.reason}</span>}{alert.params.subject && <span>{alert.params.subject}</span>}
      {action(alert, update, destination)}
    </div>;
  })}</section>}</div>}
  </>;
}

function Tasks({status, phone = false}: {status: StatusState; phone?: boolean}) {
  const quiet = !status.loading && !status.failed && !!status.value && status.value.scans.length === 0
    && status.value.tasks.items.length === 0 && status.value.tasks.count === 0;
  const tasks = status.value?.tasks.items.filter(task => task.state === 'running' && defaultI18n.has(`server.job.${task.kind}`)) ?? [];
  if (!tasks.length && !status.value?.scans.length) return quiet
    ? phone
      ? <Section phone title={t('next.dashboard.backgroundActivity')}><p className={styles.phoneQuietLine}><DashboardQuiet kind="tasks" phone /></p></Section>
      : <Section title={t('next.dashboard.backgroundActivity')} extra={<DashboardQuiet kind="tasks" />}><></></Section>
    : null;
  if (phone) {
    const scan = (scan: NonNullable<StatusState['value']>['scans'][number]) => {
      const fraction = scan.total !== undefined && scan.total > 0 ? Math.min(1, scan.analyzed / scan.total) : undefined;
      return <div className={styles.phoneTask} key={scan.libraryId}><ProgressRing icon="i-library" fraction={fraction} /><div><b>{t('next.dashboard.scanning', {library: scan.libraryName})}</b>{scan.total !== undefined && <small>{t('next.dashboard.taskProgressFiles', {done: scan.analyzed, total: scan.total})}</small>}{scan.current && <small className={styles.phoneFile}>{scan.current}</small>}</div>{fraction !== undefined && <span className={styles.phonePercent}>{t('next.dashboard.percent', {value: Math.round(fraction * 100)})}</span>}</div>;
    };
    const task = (task: NonNullable<StatusState['value']>['tasks']['items'][number]) => {
      const fraction = task.total !== undefined && task.total > 0 && task.processed !== undefined ? Math.min(1, task.processed / task.total) : undefined;
      return <div className={styles.phoneTask} key={task.id}><ProgressRing icon={task.kind === 'trickplay' ? 'i-image' : 'i-server'} fraction={fraction} /><div><b>{task.kind === 'trickplay' && task.libraryName ? t('next.dashboard.makingPreviews', {library: task.libraryName}) : <>{jobName(t, defaultI18n.has, task.kind)}{task.libraryName ? `: ${task.libraryName}` : ''}</>}</b>{task.total !== undefined && task.processed !== undefined && <small>{t(task.unit === 'episodes' ? 'next.dashboard.taskProgressEpisodes' : task.unit === 'files' ? 'next.dashboard.taskProgressFiles' : 'next.dashboard.taskProgress', {done: task.processed, total: task.total})}</small>}</div>{fraction !== undefined && <span className={styles.phonePercent}>{t('next.dashboard.percent', {value: Math.round(fraction * 100)})}</span>}</div>;
    };
    return <Section phone title={t('next.dashboard.backgroundActivity')}><div className={styles.phoneTasks}>
      {!!status.value?.scans.length && <><p className={styles.phoneSub}>{t('next.dashboard.libraryScans')}</p>{status.value.scans.map(scan)}</>}
      {!!tasks.length && <><p className={styles.phoneSub}>{t('next.dashboard.tasks')}</p>{tasks.map(task)}</>}
    </div></Section>;
  }
  return <div className={styles.tasks}><Section title={t('next.dashboard.tasks')}><div className={`${styles.card} ${styles.taskCard}`}>{status.value?.scans.map(scan => <div className={styles.task} key={scan.libraryId}><span className={styles.taskIcon}><Icon name="i-library" size={18} /></span><div><b>{t('next.dashboard.scanning', {library: scan.libraryName})}</b>{scan.total !== undefined && <small>{t('next.dashboard.taskProgressFiles', {done: scan.analyzed, total: scan.total})}</small>}</div>{scan.total !== undefined && scan.total > 0 && <><progress aria-label={scan.libraryName} value={scan.analyzed} max={scan.total} /><span>{t('next.dashboard.percent', {value: Math.round(scan.analyzed / scan.total * 100)})}</span></>}</div>)}{tasks.map(task => <div className={styles.task} key={task.id}><span className={styles.taskIcon}><Icon name={task.kind === 'trickplay' ? 'i-image' : 'i-server'} size={18} /></span><div><b>{task.kind === 'trickplay' && task.libraryName ? t('next.dashboard.makingPreviews', {library: task.libraryName}) : <>{jobName(t, defaultI18n.has, task.kind)}{task.libraryName ? `: ${task.libraryName}` : ''}</>}</b>{task.total !== undefined && task.processed !== undefined && <small>{t(task.unit === 'episodes' ? 'next.dashboard.taskProgressEpisodes' : task.unit === 'files' ? 'next.dashboard.taskProgressFiles' : 'next.dashboard.taskProgress', {done: task.processed, total: task.total})}</small>}</div>{task.total !== undefined && task.total > 0 && task.processed !== undefined && <><progress aria-label={task.libraryName || t('next.dashboard.tasks')} value={task.processed} max={task.total} /><span>{t('next.dashboard.percent', {value: Math.round(task.processed / task.total * 100)})}</span></>}</div>)}</div></Section></div>;
}
