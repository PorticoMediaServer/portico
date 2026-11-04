import {serverNow} from '@core/server-clock.ts';
import type {LogDetailMinutes} from '@core/server-administration.ts';
import {useEffect, useRef, useState, useSyncExternalStore, type ReactNode} from 'react';
import {Tabs as TabsPrimitive} from 'radix-ui';
import {defaultI18n, type MessageId} from '@i18n';
import type {ReportTransport} from '@core/client-reports.ts';
import {settingsRowIndex} from '@core/presentation/settings-spec.ts';
import {Button} from '../../../../kit/controls';
import {SettingsGroup} from '../../../../kit/settings';
import {SettingsRow} from '../../../../kit/shell/SettingsSurfaces';
import {FeedbackPanel} from './Feedback';
import {ReportsPanel} from './Reports';
import {SupportPanel} from './Support';
import {MessagesPanel} from './Messages';
import {SettingsMessagesSource, type MessageConnection} from './messages-source';
import {SettingsFeedbackSource} from './feedback-source';
import {SettingsSupportSource} from './support-source';
import {SettingsReportsSource} from './reports-source';
import {troubleshootingParts, type TroubleshootingPart} from './parts';
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
  const tabs = TABS.filter(tab => parts.has(tab.id));
  const [view, setView] = useState<View | undefined>(initialView && parts.has(initialView) ? initialView : undefined), opener = useRef<View | undefined>(undefined);
  const openers = useRef<Partial<Record<View, HTMLButtonElement | null>>>({});
  useEffect(() => {if (!view && opener.current) openers.current[opener.current]?.focus();}, [view]);
  if (!view) return <div className={styles.landing}>
    <SettingsGroup title={t('next.settingsSpec.server.troubleshooting.groups.0.title')}>
      {parts.has('detailWindow') && <DetailWindowRow source={messages} detailMinutes={detailMinutes} reportError={reportError}/>}
      {tabs.map(tab => {
        const row = settingsRowIndex.get(`troubleshooting.${tab.id}`)!.row;
        return <SettingsRow key={tab.id} label={t(row.label)} help={row.help && t(row.help)}>
          <Button ref={element => {openers.current[tab.id] = element;}} variant="glass" size="small" icon="i-right" onClick={() => {if (isCurrent()) {opener.current = tab.id;setView(tab.id);}}}>{t(row.control.label ?? row.label)}</Button>
        </SettingsRow>;
      })}
    </SettingsGroup>
    {parts.has('support') && <SupportPanel source={support} subscribe={subscribe} reportError={reportError} exportOnly advancedRows={options} problems={problems} advancedSummary={advancedSummary}/>}
  </div>;
  return <TabsPrimitive.Root className={styles.page} data-view={view} value={view} onValueChange={value => {const tab = tabs.find(entry => entry.id === value);if (tab) setView(tab.id);}}>
    <Button variant="glass" size="small" icon="i-left" onClick={() => setView(undefined)}>{t('next.settingsSpec.server.troubleshooting.title')}</Button>
    <TabsPrimitive.List className={styles.tabs}>{tabs.map(tab => <TabsPrimitive.Trigger key={tab.id} value={tab.id}
      className={styles.tab}>{t(tab.label)}</TabsPrimitive.Trigger>)}</TabsPrimitive.List>
    <TabsPrimitive.Content value={view}>
    {view === 'messages' ? <MessagesPanel source={messages} detailMinutes={detailMinutes} canDetail={parts.has('detailWindow')} reportError={reportError} />
      : view === 'feedback' ? <FeedbackPanel source={feedback} subscribe={subscribe} reportError={reportError} />
      : view === 'reports' ? <ReportsPanel source={reports} subscribe={subscribe} isCurrent={isCurrent} downloadBase={downloadBase} reportError={reportError} />
        : <SupportPanel source={support} subscribe={subscribe} reportError={reportError} hideExport />}
    </TabsPrimitive.Content>
  </TabsPrimitive.Root>;
}

function DetailWindowRow({source, detailMinutes, reportError}: {source: SettingsMessagesSource; detailMinutes?: LogDetailMinutes; reportError: (error: unknown) => void}) {
  const state = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot), [now, setNow] = useState(serverNow());
  useEffect(() => {void source.load();const timer = setInterval(() => setNow(serverNow()), 30_000);return () => {clearInterval(timer);source.pause();};}, [source]);
  const until = state.settings?.debugWindowUntil, detailed = !!until && Date.parse(until) > now;
  const row = settingsRowIndex.get('troubleshooting.detailWindow')!.row;
  return <SettingsRow label={t(row.label)} help={detailed ? t('settings.server.detailWindow.until', {time: defaultI18n.dateTime(until!)}) : row.help && t(row.help)}>
    <Button size="small" variant="glass" disabled={!state.settings || state.changing} onClick={() => {void source.detail(!detailed, detailMinutes).catch(reportError);}}>
      {t(detailed ? 'settings.server.detailWindow.stop' : 'settings.server.detailWindow.start')}
    </Button>
  </SettingsRow>;
}

const OWNER_PARTS = troubleshootingParts('owner');

export function createTroubleshootingSurfaces(options: TroubleshootingSurfacesOptions): TroubleshootingSurfaces {
  const parts = troubleshootingParts(options.tier);
  const messages = new SettingsMessagesSource(options.transport, options.messageConnection, options.isCurrent, options.reportError);
  const reports = new SettingsReportsSource(options.transport, options.serverId, options.viewerId, options.isCurrent, options.reportError);
  const feedback = new SettingsFeedbackSource(options.transport, options.serverId, options.viewerId, options.isCurrent, options.reportError);
  const support = new SettingsSupportSource(options.transport, options.serverId, options.viewerId, options.isCurrent, options.reportError);
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
