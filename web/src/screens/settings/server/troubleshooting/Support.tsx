import {useEffect, useState, useSyncExternalStore, type ReactNode} from 'react';
import {defaultI18n, type MessageId} from '@i18n';
import type {Capability} from '../../../../../../packages/contracts/src/next/api.ts';
import type {OwnerSupportOptions} from '@core/server-admin/support-next.ts';
import {Button, Select, Switch} from '../../../../kit/controls';
import {SettingsAction, SettingsAdvanced, SettingsGroup} from '../../../../kit/settings';
import {SettingsRow} from '../../../../kit/shell/SettingsSurfaces';
import {hardwareMethodName} from '../../hardware/hardware-rows';
import {SettingsSupportSource, SUPPORT_COMPONENTS, type SupportComponent} from './support-source';
import {DEFAULT_SUPPORT_COMPONENTS, SUPPORT_WINDOWS} from './support-options';
import styles from './troubleshooting.module.css';

const t = defaultI18n.t;

export type SupportPanelProps = Readonly<{
  source: SettingsSupportSource;
  subscribe: (listener: () => void) => () => void;
  reportError: (error: unknown) => void;
  /** The Settings landing owns export controls; Support keeps its existing viewers. */
  exportOnly?: boolean; hideExport?: boolean; advancedRows?: ReactNode;
  advancedSummary?: readonly string[];
}>;

const WINDOWS = SUPPORT_WINDOWS.map((entry, index) => ({...entry, label: `next.settingsSpec.server.troubleshooting.advanced.groups.1.rows.0.control.options.${index}.label` as MessageId}));
const PART_LABEL: Readonly<Record<SupportComponent, MessageId>> = Object.freeze({
  snapshot: 'web.logs.includeSnapshot', runtime: 'web.logs.includeServer', client: 'web.logs.includeClient', audit: 'web.logs.includeAudit',
});
/** The server's names for what it checks (`server_capabilities.go`), in words. A name this app does not know is shown as sent. */
const NAMES: Readonly<Record<string, MessageId>> = Object.freeze({
  ffmpeg: 'web.capabilities.ffmpeg', graphics: 'web.capabilities.graphics', vulkan: 'web.capabilities.vulkan',
  data: 'web.capabilities.folder.data', conversions: 'web.capabilities.folder.conversions', backups: 'web.capabilities.folder.backups', recordings: 'web.capabilities.folder.recordings',
  decoder_sandbox: 'web.capabilities.decoder_sandbox', decoder_network_isolation: 'web.capabilities.decoder_network_isolation',
});
const STATES: Readonly<Record<string, Readonly<{label: MessageId; tone: string}>>> = Object.freeze({
  works: {label: 'web.capabilities.works', tone: 'resolved'}, does_not_work: {label: 'web.capabilities.doesNotWork', tone: 'in-progress'},
  checking: {label: 'web.capabilities.checking', tone: 'closed'}, not_used: {label: 'web.capabilities.notUsed', tone: 'closed'},
});
const capabilityLabel = (c: Capability) => NAMES[c.name] ? t(NAMES[c.name]!) : hardwareMethodName(c.name === 'qsv' ? 'quick_sync' : c.name) ?? c.name;

/** The drawn Support tab: permissions, what this server can do, the export and what was left out. */
export function SupportPanel(props: SupportPanelProps) {
  const {source, reportError} = props;
  const state = useSyncExternalStore(props.subscribe, source.getSnapshot, source.getSnapshot);
  const [window, setWindow] = useState('24h'), [parts, setParts] = useState<readonly SupportComponent[]>(DEFAULT_SUPPORT_COMPONENTS);
  const [includePaths, setIncludePaths] = useState(false), [advanced, setAdvanced] = useState(false);
  useEffect(() => {void source.load();}, [source]);
  const supportNotice = state.phase === 'error' ? <p className={styles.failure} role="alert"><span>{t('next.states.pageFailedBody')}</span><button type="button" onClick={() => {void source.retry();}}>{t('next.shell.tryAgain')}</button></p>
    : state.phase === 'idle' || state.phase === 'loading' ? <p role="status">{t('next.settings.loading')}</p> : undefined;
  if (supportNotice && !props.exportOnly) return supportNotice;
  const permissions = state.facts?.permissions;
  const prepare = () => {
    const minutes = WINDOWS.find(entry => entry.id === window)!.ms / 60000 as OwnerSupportOptions['windowMinutes'];
    void source.prepare({windowMinutes: minutes, components: parts, includePaths}).catch(reportError);
  };
  const exportOptions = <SettingsGroup title={t('next.settingsSpec.server.troubleshooting.advanced.groups.1.title')} advanced>
    <SettingsRow label={t('web.logs.timeWindow')}><Select aria-label={t('web.logs.timeWindow')} value={window}
      disabled={state.preparing} options={WINDOWS.map(entry => ({value: entry.id, label: t(entry.label)}))} onValueChange={setWindow}/></SettingsRow>
    {SUPPORT_COMPONENTS.map(component => <SettingsRow key={component} label={t(PART_LABEL[component])}>
      <Switch aria-label={t(PART_LABEL[component])} checked={parts.includes(component)} disabled={state.preparing} onCheckedChange={checked =>
        setParts(current => checked ? [...new Set([...current, component])] : current.filter(part => part !== component))}/>
    </SettingsRow>)}
  </SettingsGroup>;
  return <div className={`${styles.reports} ${styles.support}`}>
    {supportNotice}
    {!props.exportOnly && permissions?.readableByOthers && <section className={styles.permissions}>
      <div className={styles.panelHead}><h3>{t('web.maintenance.permissionsTitle')}</h3></div>
      <p className={styles.lede}>{permissions.fixable ? t('web.maintenance.permissionsBody') : permissions.message ?? t('web.maintenance.permissionsBody')}</p>
      {permissions.fixable && <div><Button variant="glass" size="small" disabled={state.fixing} onClick={() => {void source.fixPermissions();}}>{t('web.maintenance.fixPermissions')}</Button></div>}
    </section>}
    {!props.exportOnly && state.fixed && !permissions?.readableByOthers && <p className={styles.fixed} role="status">{t('web.maintenance.permissionsFixed')}</p>}
    {!props.exportOnly && <section className={styles.panel}>
      <div className={styles.panelHead}><h3>{t('settings.server.capabilities')}</h3></div>
      <p className={styles.lede}>{t('web.capabilities.help')}</p>
      <ul className={styles.rows}>{state.facts?.capabilities.map(capability => <CapabilityRow key={`${capability.area}/${capability.name}/${capability.path ?? ''}`} capability={capability} />)}</ul>
    </section>}
    {!props.hideExport && !supportNotice && <section className={styles.panel}>
      <div className={styles.panelHead}><h3>{t('web.logs.exportTitle')}</h3></div>
      <p className={styles.lede}>{t('web.logs.exportLede')}</p>
      {!props.exportOnly && exportOptions}
      <div className={styles.exportRow}><label className={styles.check}>
        <input type="checkbox" checked={includePaths} disabled={state.preparing} onChange={event => setIncludePaths(event.currentTarget.checked)}/>
        {t('next.settingsSpec.server.troubleshooting.groups.1.rows.0.children.0.label')}</label><p className={styles.note}>{t('web.logs.pathsHelpNext')}</p></div>
      {state.prepareError && <p role="alert" className={styles.failure}>{t('error.unconfirmedChange')}</p>}
      <div className={styles.exportRow}><span className={styles.fieldLabel}>{t('web.logs.prepareExport')}</span>
        <SettingsAction size="small" appearance="primary" loading={state.preparing} disabled={state.preparing || !parts.length}
          onClick={prepare}>{t(state.prepareError ? 'action.tryAgain' : 'web.logs.prepare')}</SettingsAction></div>
      {state.downloaded && <p className={styles.note} role="status">{t('web.logs.exportDownloaded', {name: state.downloaded})}</p>}
    </section>}
    {props.exportOnly && <SettingsAdvanced open={advanced} onOpenChange={setAdvanced} summary={[
      ...(props.advancedSummary ?? []), ...(!supportNotice && window !== '24h' ? [t('web.logs.timeWindow') + ': ' + t(WINDOWS.find(entry => entry.id === window)!.label)] : []),
      ...(!supportNotice ? SUPPORT_COMPONENTS.filter(part => parts.includes(part) !== DEFAULT_SUPPORT_COMPONENTS.includes(part)).map(part => t(PART_LABEL[part])) : []),
    ]}>
      {props.advancedRows}{!supportNotice && exportOptions}
    </SettingsAdvanced>}
    {!props.exportOnly && <section className={styles.panel}>
      <div className={styles.panelHead}><h3>{t('settings.server.skippedParts')}</h3></div>
      <p className={styles.lede}>{t('settings.server.skippedPartsLede')}</p>
      {state.skipped.length ? <ul className={styles.rows}>{state.skipped.map(part => <li key={`${part.document}/${part.part}`} className={styles.recordRow}>
        <div className={styles.reportText}><b>{part.part}</b><small>{part.document}</small></div>
        <span className={styles.sequence}>{t('settings.server.skippedPartsCount', {count: part.count})}</span>
      </li>)}</ul> : <p className={styles.none}>{t('settings.server.skippedPartsNone')}</p>}
    </section>}
  </div>;
}

/** One thing the server checked: what it is, whether it works, and the server's own sentence on why not. */
function CapabilityRow({capability}: Readonly<{capability: Capability}>) {
  const state = STATES[capability.state] ?? STATES.checking!;
  return <li className={styles.capabilityRow}>
    <div className={styles.reportText}><b>{capabilityLabel(capability)}</b>
      {capability.detail && <small>{capability.detail}</small>}
      {capability.path && <small><code>{capability.path}</code></small>}
    </div>
    <span className={styles.status} data-status={state.tone}>{t(state.label)}</span>
  </li>;
}
