import {MetadataFacts} from '../../../../kit/MetadataFacts';
import {useLayoutEffect, useMemo, useEffect, useRef, useState, useSyncExternalStore} from 'react';
import {defaultI18n, type MessageId} from '@i18n';
import {Tabs as TabsPrimitive} from 'radix-ui';
import {type ClientReport} from '@core/client-reports.ts';
import type {EvidenceRecord} from '@core/console.ts';
import type {CatalogueFailure} from '@core/freshness.ts';
import {sentence, when} from '@core/server-admin/console-words.ts';
import {Button} from '../../../../kit/controls';
import {Icon} from '../../../../kit/Icon';
import {Dialog} from '../../../../kit/overlays/Dialog';
import {Menu} from '../../../../kit/overlays/Popover';
import {SettingsAction} from '../../../../kit/settings';
import type {RecordLane, SettingsReportsSource} from './reports-source';
import styles from './troubleshooting.module.css';

const t = defaultI18n.t;

export type ReportsPanelProps = Readonly<{
  source: SettingsReportsSource;
  subscribe: (listener: () => void) => () => void;
  isCurrent: () => boolean;
  /** The selected server's own origin, so a downloaded report comes from the server that holds it. */
  downloadBase: string;
  reportError: (error: unknown) => void;
}>;

const LANES: readonly Readonly<{id: RecordLane; label: MessageId}>[] = Object.freeze([
  {id: 'runtime', label: 'web.logs.laneServer'},
  {id: 'client', label: 'web.logs.laneClient'},
  {id: 'audit', label: 'web.logs.laneAudit'},
]);
const LEVELS: Readonly<Record<string, MessageId>> = Object.freeze({info: 'next.settings.levelInfo', warn: 'next.settings.levelWarn', error: 'next.settings.levelError', debug: 'next.settings.levelDebug'});
const levelLabel = (severity: string | undefined) => t(LEVELS[severity ?? ''] ?? 'next.settings.levelInfo');
const senderIcon = (deviceClass: string): 'i-phone' | 'i-tv' | 'i-laptop' | 'i-monitor' =>
  /phone|tablet|mobile/i.test(deviceClass) ? 'i-phone' : /tv|console/i.test(deviceClass) ? 'i-tv' : /laptop/i.test(deviceClass) ? 'i-laptop' : 'i-monitor';
const sender = (report: ClientReport) => [report.platform, report.deviceClass, report.appVersion].filter(Boolean).join(' ') || t('server.reports.unknownSender');

/** The drawn Reports tab: client reports, the titles a rebuild set aside, and the server's records by lane. */
export function ReportsPanel(props: ReportsPanelProps) {
  const state = useSyncExternalStore(props.subscribe, props.source.getSnapshot, props.source.getSnapshot);
  const [reading, setReading] = useState<{source: SettingsReportsSource; report: ClientReport}>(), [confirm, setConfirm] = useState<{source: SettingsReportsSource; report: ClientReport}>();
  const confirmation = useRef<typeof confirm>(undefined), owner = useRef(props.source);
  if (owner.current !== props.source) {owner.current = props.source;confirmation.current = undefined;}
  useLayoutEffect(() => {return () => {confirmation.current = undefined;};}, [props.source]);
  const closeConfirmation = () => {confirmation.current = undefined;setConfirm(undefined);};
  useEffect(() => {void props.source.load();}, [props.source]);
  if (state.phase === 'error') return <p className={styles.failure} role="alert"><span>{t('next.states.pageFailedBody')}</span><button type="button" onClick={() => {void props.source.retry();}}>{t('next.shell.tryAgain')}</button></p>;
  if (state.phase === 'idle' || state.phase === 'loading' && !state.reports.length) return <p role="status">{t('next.settings.loading')}</p>;
  return <div className={styles.reports}>
    <section className={styles.panel}>
      <div className={styles.panelHead}><h3>{t('web.diagnostics.clientReports')}</h3></div>
      <p className={styles.lede}>{t('server.reports.lede')}</p>
      {state.reports.length ? <ul className={styles.rows}>
        {state.reports.map(report => <ReportRow key={report.id} report={report} source={props.source} disabled={state.phase === 'loading'} onRead={() => setReading({source: props.source, report})} onDelete={() => {const next = {source: props.source, report};confirmation.current = next;setConfirm(next);}} />)}
      </ul> : <p className={styles.none}>{t('server.reports.none')}</p>}
      {state.next && <div className={styles.pager}><Button variant="glass" size="small" onClick={() => {void props.source.more();}}>{t('server.showMore')}</Button></div>}
    </section>
    {state.setAside && state.setAside.failures.length > 0 && <SetAside failures={state.setAside.failures} more={state.setAside.more} onCheck={() => {void props.source.checkSetAside();}} />}
    {state.records && <Records records={state.records.items} lane={state.records.lane} next={state.records.next} onLane={lane => {void props.source.selectLane(lane);}}
      onOlder={() => {void props.source.moreRecords();}} onLatest={() => {void props.source.latestRecords();}} />}
    {reading?.source === props.source && <ReadReport key={reading.report.id} report={reading.report} source={props.source} onClose={() => setReading(undefined)} />}
    {confirm?.source === props.source && confirmation.current === confirm && <Dialog open onOpenChange={open => {if (!open) closeConfirmation();}} title={t('server.reports.deleteTitle')}
      footer={<><Button size="small" onClick={closeConfirmation}>{t('next.settings.cancel')}</Button><SettingsAction size="small" appearance="danger"
        onClick={() => {if (confirmation.current !== confirm || owner.current !== props.source) return;const selected = confirm.report;closeConfirmation();void props.source.remove(selected.id, selected);}}>{t('web.diagnostics.clientReportDelete')}</SettingsAction></>}>
      <p>{t('server.reports.deleteBody')}</p>
    </Dialog>}
  </div>;
}

/** The JSON Read and Download endpoints return the same stored ClientUpload.
 * Fetch through the owner transport, then save that checked record locally. */
function useReportDownload(source: SettingsReportsSource, report: ClientReport, admit?: () => boolean) {
  const [failed, setFailed] = useState(false);
  const life = useMemo<{active: boolean; request?: AbortController}>(() => { /* Preserve the original ownership/invalidation keys. */ void source; void report; return ({active: true}); }, [source, report]);
  useLayoutEffect(() => {life.active = true;return () => {life.active = false;life.request?.abort();};}, [life]);
  const download = async () => {
    if (!life.active || life.request || !source.listed(report) || admit && !admit()) return;
    const request = new AbortController();life.request = request;setFailed(false);
    const current = () => life.active && life.request === request && !request.signal.aborted && source.listed(report) && (!admit || admit());
    try {
      const value = await source.read(report.id, request.signal, report);
      if (!current()) return;
      if (value.body === undefined) throw new Error('report_body_unavailable');
      const url = URL.createObjectURL(new Blob([JSON.stringify(value)], {type: 'application/json'}));
      const link = document.createElement('a');link.href = url;link.download = 'portico-client-report.json';
      try {if (current()) {document.body.appendChild(link);link.click();}} finally {link.remove();URL.revokeObjectURL(url);}
    } catch {if (current()) setFailed(true);}
    finally {if (life.request === request) life.request = undefined;}
  };
  return {download, failed};
}

function ReportRow({report, source, disabled, onRead, onDelete}: Readonly<{report: ClientReport; source: SettingsReportsSource; disabled: boolean; onRead: () => void; onDelete: () => void}>) {
  const [open, setOpen] = useState<object>(), active = useRef<object | undefined>(undefined), anchor = useRef<HTMLButtonElement>(null);
  const {download, failed} = useReportDownload(source, report);
  const title = sender(report), whenText = defaultI18n.dateTime(report.receivedAt), size = defaultI18n.bytes(report.bytes);
  const close = () => {active.current = undefined;setOpen(undefined);};
  const choose = (action: () => void) => {if (!open || active.current !== open || disabled || !source.listed(report)) return;action();close();};
  useLayoutEffect(() => {active.current = undefined;setOpen(undefined);return () => {active.current = undefined;};}, [source, report]);
  return <li className={styles.reportRow}>
    <Icon name={senderIcon(report.deviceClass)} size={20} />
    <div className={styles.reportText}><b>{title}</b><small>{whenText} {size}</small>{failed && <small role="alert">{t('next.states.pageFailedBody')}</small>}</div>
    <Button variant="glass" size="small" disabled={disabled} onClick={() => {if (source.listed(report)) onRead();}}>{t('server.reports.read')}</Button>
    <Button ref={anchor} size="small" variant="icon" icon="i-more" iconOnly aria-label={t('next.home.moreFor', {title})} disabled={disabled} onClick={() => {if (open) close();else if (source.listed(report)) {const next = {};active.current = next;setOpen(next);}}} />
    <Menu open={!!open && !disabled} onOpenChange={value => {if (!value) close();}} anchorRef={anchor} title={title} returnFocusRef={anchor} items={[
      {id: 'download', icon: 'i-download', label: t('web.diagnostics.clientReportDownload'), onSelect: () => choose(() => {void download();})},
      {id: 'delete', icon: 'i-trash', destructive: true, label: t('web.diagnostics.clientReportDelete'), onSelect: () => choose(onDelete)},
    ]} />
  </li>;
}

/** One report's own log, retained only by its exact open dialog. */
function ReadReport({report, source, onClose}: Readonly<{report: ClientReport; source: SettingsReportsSource; onClose: () => void}>) {
  const [body, setBody] = useState<string>(), [failed, setFailed] = useState(false), done = useRef<HTMLButtonElement>(null);
  const life = useMemo<{request?: AbortController}>(() => { /* Preserve the original ownership/invalidation keys. */ void source; void report; return ({}); }, [source, report]);
  useLayoutEffect(() => {const request = new AbortController();life.request = request;return () => {request.abort();if (life.request === request) life.request = undefined;};}, [life]);
  const current = () => !!life.request && !life.request.signal.aborted && source.listed(report);
  const {download, failed: downloadFailed} = useReportDownload(source, report, current);
  const close = () => {life.request?.abort();life.request = undefined;setBody(undefined);onClose();};
  useEffect(() => {
    const request = life.request;
    if (!request) return;
    void source.read(report.id, request.signal, report).then(value => {if (life.request === request && !request.signal.aborted) setBody(value.body ?? '');})
      .catch(() => {if (life.request === request && !request.signal.aborted) setFailed(true);});
  }, [report, source, life]);
  const text = failed ? t('next.states.pageFailedBody') : body === undefined ? t('next.settings.loading') : body || t('server.reports.empty');
  return <Dialog open onOpenChange={open => {if (!open) close();}} size="lg" title={t('server.reports.title')} initialFocusRef={done} returnFocusRef={done}
    footer={<><Button size="small" disabled={!current()} onClick={() => {void download();}}>{t('web.diagnostics.clientReportDownload')}</Button><Button ref={done} size="small" onClick={close}>{t('next.settings.done')}</Button></>}>
    <p className={styles.reportMeta}><MetadataFacts parts={[sender(report), defaultI18n.dateTime(report.receivedAt), defaultI18n.bytes(report.bytes)]} /></p>
    {downloadFailed && <p role="alert" className={styles.failure}>{t('next.states.pageFailedBody')}</p>}
    <pre className={styles.log}>{text}</pre>
  </Dialog>;
}

/** Titles a catalogue rebuild could not finish; the server tries them again by itself. */
function SetAside({failures, more, onCheck}: Readonly<{failures: readonly CatalogueFailure[]; more: boolean; onCheck: () => void}>) {
  return <section className={styles.panel}>
    <div className={styles.panelHead}><h3>{t('server.setAside.title')} <span className={styles.tag}>{t('next.settings.administratorsSee')}</span></h3></div>
    <p className={styles.lede}>{t('server.setAside.lede')}</p>
    <ul className={styles.rows}>{failures.map((failure, index) => <li key={`${failure.domain}-${failure.entityId ?? index}`} className={styles.setAsideRow}>
      <Icon name="i-alert" size={20} />
      <div className={styles.reportText}><b>{failure.title || t('server.setAside.untitled')}</b>
        <small>{t('server.setAside.tried', {count: failure.attempts, when: defaultI18n.dateTime(failure.failedAt)})}</small></div>
      <p className={styles.reason}>{failure.reason}</p>
    </li>)}</ul>
    {more && <p className={styles.note}>{t('server.setAside.more')}</p>}
    <div><Button variant="glass" size="small" onClick={onCheck}>{t('server.setAside.check')}</Button></div>
  </section>;
}

/** What the server noted, by lane, a page at a time. */
function Records({records, lane, next, onLane, onOlder, onLatest}: Readonly<{records: readonly EvidenceRecord[]; lane: RecordLane; next?: string; onLane: (lane: RecordLane) => void; onOlder: () => void; onLatest: () => void}>) {
  return <TabsPrimitive.Root value={lane} onValueChange={value => {const entry = LANES.find(entry => entry.id === value);if (entry) onLane(entry.id);}} asChild><section className={styles.panel}>
    <div className={styles.panelHead}><h3>{t('settings.server.records')}</h3></div>
    <p className={styles.lede}>{t('next.settings.recordsHelp')}</p>
    <TabsPrimitive.List className={styles.lanes} aria-label={t('settings.server.records')}>{LANES.map(entry => <TabsPrimitive.Trigger key={entry.id} value={entry.id}
      className={styles.lane}>{t(entry.label)}</TabsPrimitive.Trigger>)}</TabsPrimitive.List>
    <TabsPrimitive.Content value={lane} className={styles.lanePanel}>
    {records.length ? <ul className={styles.rows}>{records.map(record => <li key={record.sequence} className={styles.recordRow}>
      <span className={styles.level} data-level={record.severity ?? 'info'}>{levelLabel(record.severity)}</span>
      <div className={styles.reportText}><b>{sentence(record.code ?? record.action ?? '—')}{record.component ? <span className={styles.component}> {sentence(record.component)}</span> : null}</b>
        <small><MetadataFacts parts={[when(record.at), ...Object.entries(record.fields ?? {}).map(([key, value]) => `${sentence(key)} ${value}`)]} /></small></div>
      <span className={styles.sequence}>#{record.sequence}</span>
    </li>)}</ul> : <p className={styles.none}>{t('settings.server.records.none')}</p>}
    <div className={styles.pager}><Button variant="glass" size="small" onClick={onLatest}>{t('next.settings.recordsLatest')}</Button>
      <Button variant="glass" size="small" disabled={!next} onClick={onOlder}>{t('next.settings.recordsOlder')}</Button></div>
    </TabsPrimitive.Content>
  </section></TabsPrimitive.Root>;
}
