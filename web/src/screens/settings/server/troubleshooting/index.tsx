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
import styles from './troubleshooting.module.css';

const t = defaultI18n.t;

export type TroubleshootingSurfacesOptions = Readonly<{
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
  dispose: () => void;
}>;

type View = 'messages' | 'feedback' | 'reports' | 'support';
const TABS: readonly Readonly<{id: View; label: MessageId}>[] = Object.freeze([
  {id: 'messages', label: 'web.logs.messages'},
  {id: 'feedback', label: 'next.settings.troubleshootingFeedback'},
  {id: 'reports', label: 'next.settings.troubleshootingReports'},
  {id: 'support', label: 'server.support'},
]);

/** The Settings landing opens the four existing owner-scoped viewers. */
export type TroubleshootingPageProps = Readonly<{
  messages: SettingsMessagesSource; reports: SettingsReportsSource; feedback: SettingsFeedbackSource; support: SettingsSupportSource;
  subscribe: (listener: () => void) => () => void; isCurrent: () => boolean; downloadBase: string; reportError: (error: unknown) => void;
  /** An explicit viewer opening; otherwise show the Settings landing. */
  initialView?: View;
  options?: ReactNode;
  detailMinutes?: LogDetailMinutes;
  problems?: ReactNode;
  advancedSummary?: readonly string[];
}>;

export function TroubleshootingPage({messages, reports, feedback, support, subscribe, isCurrent, downloadBase, reportError, options, detailMinutes, problems, advancedSummary, initialView}: TroubleshootingPageProps) {
  const [view, setView] = useState<View | undefined>(initialView), opener = useRef<View | undefined>(undefined);
  const openers = useRef<Partial<Record<View, HTMLButtonElement | null>>>({});
  useEffect(() => {if (!view && opener.current) openers.current[opener.current]?.focus();}, [view]);
  if (!view) return <div className={styles.landing}>
    <SettingsGroup title={t('next.settingsSpec.server.troubleshooting.groups.0.title')}>
      <DetailWindowRow source={messages} detailMinutes={detailMinutes} reportError={reportError}/>
      {TABS.map(tab => {
        const row = settingsRowIndex.get(`troubleshooting.${tab.id}`)!.row;
        return <SettingsRow key={tab.id} label={t(row.label)} help={row.help && t(row.help)}>
          <Button ref={element => {openers.current[tab.id] = element;}} variant="glass" size="small" icon="i-right" onClick={() => {if (isCurrent()) {opener.current = tab.id;setView(tab.id);}}}>{t(row.control.label ?? row.label)}</Button>
        </SettingsRow>;
      })}
    </SettingsGroup>
    <SupportPanel source={support} subscribe={subscribe} reportError={reportError} exportOnly advancedRows={options} problems={problems} advancedSummary={advancedSummary}/>
  </div>;
  return <TabsPrimitive.Root className={styles.page} data-view={view} value={view} onValueChange={value => {const tab = TABS.find(entry => entry.id === value);if (tab) setView(tab.id);}}>
    <Button variant="glass" size="small" icon="i-left" onClick={() => setView(undefined)}>{t('next.settingsSpec.server.troubleshooting.title')}</Button>
    <TabsPrimitive.List className={styles.tabs}>{TABS.map(tab => <TabsPrimitive.Trigger key={tab.id} value={tab.id}
      className={styles.tab}>{t(tab.label)}</TabsPrimitive.Trigger>)}</TabsPrimitive.List>
    <TabsPrimitive.Content value={view}>
    {view === 'messages' ? <MessagesPanel source={messages} detailMinutes={detailMinutes} reportError={reportError} />
      : view === 'feedback' ? <FeedbackPanel source={feedback} subscribe={subscribe} reportError={reportError} />
      : view === 'reports' ? <ReportsPanel source={reports} subscribe={subscribe} isCurrent={isCurrent} downloadBase={downloadBase} reportError={reportError} />
        : <SupportPanel source={support} subscribe={subscribe} reportError={reportError} hideExport />}
    </TabsPrimitive.Content>
  </TabsPrimitive.Root>;
}

function DetailWindowRow({source, detailMinutes, reportError}: {source: SettingsMessagesSource; detailMinutes?: LogDetailMinutes; reportError: (error: unknown) => void}) {
  const state = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot), [now, setNow] = useState(Date.now());
  useEffect(() => {void source.load();const timer = setInterval(() => setNow(Date.now()), 30_000);return () => {clearInterval(timer);source.pause();};}, [source]);
  const until = state.settings?.debugWindowUntil, detailed = !!until && Date.parse(until) > now;
  const row = settingsRowIndex.get('troubleshooting.detailWindow')!.row;
  return <SettingsRow label={t(row.label)} help={detailed ? t('settings.server.detailWindow.until', {time: defaultI18n.dateTime(until!)}) : row.help && t(row.help)}>
    <Button size="small" variant="glass" disabled={!state.settings || state.changing} onClick={() => {void source.detail(!detailed, detailMinutes).catch(reportError);}}>
      {t(detailed ? 'settings.server.detailWindow.stop' : 'settings.server.detailWindow.start')}
    </Button>
  </SettingsRow>;
}

export function createTroubleshootingSurfaces(options: TroubleshootingSurfacesOptions): TroubleshootingSurfaces {
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
      downloadBase={options.downloadBase} reportError={options.reportError} options={messageOptions} detailMinutes={detailMinutes} problems={problems} advancedSummary={advancedSummary} /> : undefined,
    dispose: () => {messages.dispose();reports.dispose();feedback.dispose();support.dispose();},
  };
}
