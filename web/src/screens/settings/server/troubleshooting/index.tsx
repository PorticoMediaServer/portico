import {serverNow} from '@core/server-clock.ts';
import {LOG_DETAIL_MINUTES, type LogDetailMinutes} from '@core/server-administration.ts';
import {useEffect, useRef, useState, useSyncExternalStore, type ReactNode} from 'react';
import {Tabs as TabsPrimitive} from 'radix-ui';
import {defaultI18n, type MessageId} from '@i18n';
import type {ReportTransport} from '@core/client-reports.ts';
import {settingsRowIndex} from '@core/presentation/settings-spec.ts';
import {Button, Select} from '../../../../kit/controls';
import {SettingsGroup} from '../../../../kit/settings';
import {SettingsRow} from '../../../../kit/shell/SettingsSurfaces';
import {FeedbackPanel} from './Feedback';
import {ReportsPanel} from './Reports';
import {SupportPanel} from './Support';
import {MessagesPanel} from './Messages';
import {KEEP_MESSAGE_DAYS, SettingsMessagesSource, type KeepMessageDays, type MessageConnection} from './messages-source';
import {SettingsFeedbackSource} from './feedback-source';
import {SettingsSupportSource} from './support-source';
import {SettingsReportsSource} from './reports-source';
import {troubleshootingParts, type TroubleshootingPart} from './parts';
import {partError} from './part-error';
export {partError};
import type {ServerTier} from '@core/server-admin/route-access.ts';
import styles from './troubleshooting.module.css';

const t = defaultI18n.t;

export type TroubleshootingSurfacesOptions = Readonly<{
  /** The viewer's server tier: its parts are the ones whose calls that tier may make (parts.ts). */
  tier: ServerTier;
  transport: ReportTransport;
  messageConnection?: MessageConnection;
  serverId: string;
  /** The captured viewer fence, the same one the Settings controller uses. */
  viewerId: string;
  /** The selected server's own origin, for downloads it owns. */
  downloadBase: string;
  isCurrent: () => boolean;
  subscribe: (listener: () => void) => () => void;
  reportError: (error: unknown) => void;
}>;

export type TroubleshootingSurfaces = Readonly<{
  /** The drawn Troubleshooting page for the selected server, or nothing when this scope ended. */
  render: (options?: ReactNode, detailMinutes?: LogDetailMinutes, problems?: ReactNode, advancedSummary?: readonly string[]) => ReactNode;
  /** The parts this viewer's tier may use, so the route only builds what it can show. */
  parts: ReadonlySet<TroubleshootingPart>;
  dispose: () => void;
}>;

type View = 'messages' | 'feedback' | 'reports' | 'support';
const TABS: readonly Readonly<{id: View; label: MessageId}>[] = Object.freeze([
  {id: 'messages', label: 'web.logs.messages'},
  {id: 'feedback', label: 'next.settings.troubleshootingFeedback'},
  {id: 'reports', label: 'next.settings.troubleshootingReports'},
  {id: 'support', label: 'server.support'},
]);

/** The parts each viewer draws: it is offered when the tier has any of them. */
const TAB_PARTS: Readonly<Record<View, readonly TroubleshootingPart[]>> = Object.freeze({messages: ['messages'], feedback: ['feedback'], reports: ['reports', 'stuckFiles'], support: ['support']});
/** A tier with stuck files but not app logs (an administrator: spec "Messages and stuck files") opens a viewer of stuck files alone, named for them. */
const stuckOnly = (parts: ReadonlySet<TroubleshootingPart>) => parts.has('stuckFiles') && !parts.has('reports');

/** The Settings landing opens the viewers this tier may use: all four for the owner, Messages for an administrator. */
export type TroubleshootingPageProps = Readonly<{
  messages: SettingsMessagesSource; reports: SettingsReportsSource; feedback: SettingsFeedbackSource; support: SettingsSupportSource;
  subscribe: (listener: () => void) => () => void; isCurrent: () => boolean; downloadBase: string; reportError: (error: unknown) => void;
  /** An explicit viewer opening; otherwise show the Settings landing. */
  initialView?: View;
  /** The parts this viewer's tier may use; the owner's when absent. */
  parts?: ReadonlySet<TroubleshootingPart>;
  options?: ReactNode;
  detailMinutes?: LogDetailMinutes;
  problems?: ReactNode;
  advancedSummary?: readonly string[];
}>;

export function TroubleshootingPage({messages, reports, feedback, support, subscribe, isCurrent, downloadBase, reportError, options, detailMinutes, problems, advancedSummary, initialView, parts = OWNER_PARTS}: TroubleshootingPageProps) {
  const stuck = stuckOnly(parts);
  const tabs = TABS.filter(tab => TAB_PARTS[tab.id].some(part => parts.has(part))).map(tab => tab.id === 'reports' && stuck ? {...tab, label: 'next.settings.troubleshootingStuckFiles' as MessageId} : tab);
  const [view, setView] = useState<View | undefined>(initialView && tabs.some(tab => tab.id === initialView) ? initialView : undefined), opener = useRef<View | undefined>(undefined);
  const openers = useRef<Partial<Record<View, HTMLButtonElement | null>>>({});
  useEffect(() => {if (!view && opener.current) openers.current[opener.current]?.focus();}, [view]);
  if (!view) return <div className={styles.landing}>
    <SettingsGroup title={t('next.settingsSpec.server.troubleshooting.groups.0.title')}>
      {parts.has('detailWindow') && <DetailWindowRow source={messages} detailMinutes={detailMinutes} reportError={reportError}/>}
      {parts.has('logSettings') && <KeepMessagesRow source={messages} reportError={reportError}/>}
      {tabs.map(tab => {
        const row = settingsRowIndex.get(`troubleshooting.${tab.id}`)!.row, stuckRow = tab.id === 'reports' && stuck;
        return <SettingsRow key={tab.id} label={stuckRow ? t('next.settings.troubleshootingStuckFiles') : t(row.label)} help={stuckRow ? t('next.settings.troubleshootingStuckFilesHelp') : row.help && t(row.help)}>
          <Button ref={element => {openers.current[tab.id] = element;}} variant="glass" size="small" icon="i-right" onClick={() => {if (isCurrent()) {opener.current = tab.id;setView(tab.id);}}}>{stuckRow ? t('next.settings.troubleshootingOpenStuckFiles') : t(row.control.label ?? row.label)}</Button>
        </SettingsRow>;
      })}
    </SettingsGroup>
    {parts.has('support') && <SupportPanel source={support} subscribe={subscribe} reportError={reportError} exportOnly advancedRows={options} problems={problems} advancedSummary={advancedSummary}/>}
  </div>;
  return <TabsPrimitive.Root className={styles.page} data-view={view} value={view} onValueChange={value => {const tab = tabs.find(entry => entry.id === value);if (tab) setView(tab.id);}}>
    <Button className={styles.back} variant="glass" size="small" icon="i-left" onClick={() => setView(undefined)}>{t('next.settingsSpec.server.troubleshooting.title')}</Button>
    <TabsPrimitive.List className={styles.tabs}>{tabs.map(tab => <TabsPrimitive.Trigger key={tab.id} value={tab.id}
      className={styles.tab}>{t(tab.label)}</TabsPrimitive.Trigger>)}</TabsPrimitive.List>
    <TabsPrimitive.Content value={view}>
    {view === 'messages' ? <MessagesPanel source={messages} detailMinutes={detailMinutes} canDetail={parts.has('detailWindow')} reportError={reportError} />
      : view === 'feedback' ? <FeedbackPanel source={feedback} subscribe={subscribe} reportError={reportError} />
      : view === 'reports' ? <ReportsPanel source={reports} subscribe={subscribe} isCurrent={isCurrent} downloadBase={downloadBase} reportError={reportError} appLogs={parts.has('reports')} />
        : <SupportPanel source={support} subscribe={subscribe} reportError={reportError} hideExport />}
    </TabsPrimitive.Content>
  </TabsPrimitive.Root>;
}

const DETAIL_LENGTH = (minutes: LogDetailMinutes) => `next.settingsSpec.server.troubleshooting.advanced.groups.0.rows.1.control.options.${LOG_DETAIL_MINUTES.indexOf(minutes)}.label` as MessageId;

/** "Record more detail" (console rule 67): for 30 minutes unless another length is chosen here; it switches itself off. */
function DetailWindowRow({source, detailMinutes, reportError}: {source: SettingsMessagesSource; detailMinutes?: LogDetailMinutes; reportError: (error: unknown) => void}) {
  const state = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot), [now, setNow] = useState(serverNow());
  const [minutes, setMinutes] = useState<LogDetailMinutes>(detailMinutes ?? 30);
  useEffect(() => {void source.load();const timer = setInterval(() => setNow(serverNow()), 30_000);return () => {clearInterval(timer);source.pause();};}, [source]);
  const until = state.detailUntil, detailed = !!until && Date.parse(until) > now;
  const row = settingsRowIndex.get('troubleshooting.detailWindow')!.row;
  return <SettingsRow label={t(row.label)} help={detailed ? t('settings.server.detailWindow.until', {time: defaultI18n.dateTime(until!)}) : row.help && t(row.help)}>
    {!detailed && <Select aria-label={t('settings.server.detailWindow.length')} value={String(minutes)} disabled={state.changing}
      onValueChange={value => {const chosen = Number(value) as LogDetailMinutes; if ((LOG_DETAIL_MINUTES as readonly number[]).includes(chosen)) setMinutes(chosen);}}
      options={LOG_DETAIL_MINUTES.map(value => ({value: String(value), label: t(DETAIL_LENGTH(value))}))} />}
    <Button size="small" variant="glass" disabled={state.phase !== 'ready' || state.changing} onClick={() => {void source.detail(!detailed, minutes).catch(reportError);}}>
      {t(detailed ? 'settings.server.detailWindow.stop' : 'settings.server.detailWindow.start')}
    </Button>
  </SettingsRow>;
}

/** How long messages are kept (console rule 68): 30 days unless the owner chooses 7 or 14. */
function KeepMessagesRow({source, reportError}: {source: SettingsMessagesSource; reportError: (error: unknown) => void}) {
  const state = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot);
  if (!state.keepDays) return null;
  return <SettingsRow label={t('settings.server.keepMessages')} help={t('settings.server.keepMessages.help')}>
    <Select aria-label={t('settings.server.keepMessages')} value={String(state.keepDays)} disabled={state.changing}
      onValueChange={value => {const days = Number(value) as KeepMessageDays; if ((KEEP_MESSAGE_DAYS as readonly number[]).includes(days)) void source.keep(days).catch(reportError);}}
      options={KEEP_MESSAGE_DAYS.map(days => ({value: String(days), label: t('settings.server.keepMessages.days', {days})}))} />
  </SettingsRow>;
}

const OWNER_PARTS = troubleshootingParts('owner');

export function createTroubleshootingSurfaces(options: TroubleshootingSurfacesOptions): TroubleshootingSurfaces {
  const parts = troubleshootingParts(options.tier), ownError = partError(options.reportError);
  const messages = new SettingsMessagesSource(options.transport, options.messageConnection, options.isCurrent, ownError, {keeping: parts.has('logSettings')});
  const reports = new SettingsReportsSource(options.transport, options.serverId, options.viewerId, options.isCurrent, ownError,
    {reports: parts.has('reports'), stuckFiles: parts.has('stuckFiles')});
  const feedback = new SettingsFeedbackSource(options.transport, options.serverId, options.viewerId, options.isCurrent, ownError);
  const support = new SettingsSupportSource(options.transport, options.serverId, options.viewerId, options.isCurrent, ownError);
  const subscribe = (listener: () => void) => {
    const offs = [options.subscribe(listener), messages.subscribe(listener), reports.subscribe(listener), feedback.subscribe(listener), support.subscribe(listener)];
    return () => {for (const off of offs) off();};
  };
  return {
    render: (messageOptions, detailMinutes, problems, advancedSummary) => options.isCurrent() ? <TroubleshootingPage messages={messages} reports={reports} feedback={feedback} support={support} subscribe={subscribe} isCurrent={options.isCurrent}
      downloadBase={options.downloadBase} reportError={options.reportError} options={messageOptions} detailMinutes={detailMinutes} problems={problems} advancedSummary={advancedSummary} parts={parts} /> : undefined,
    parts,
    dispose: () => {messages.dispose();reports.dispose();feedback.dispose();support.dispose();},
  };
}
