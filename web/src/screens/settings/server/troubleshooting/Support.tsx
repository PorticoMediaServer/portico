import {useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore, type ReactNode} from 'react';
import {defaultI18n, type MessageId} from '@i18n';
import {CAPABILITY_ROWS, capabilityReasonId, type ServerCapability} from '@core/server-admin/capabilities.ts';
import {Button, Select, Switch} from '../../../../kit/controls';
import {SettingsAction, SettingsAdvanced, SettingsGroup} from '../../../../kit/settings';
import {SettingsRow} from '../../../../kit/shell/SettingsSurfaces';
import {SettingsSupportSource, SUPPORT_COMPONENTS, type SupportComponent} from './support-source';
import {DEFAULT_SUPPORT_COMPONENTS, SUPPORT_WINDOWS} from './support-options';
import styles from './troubleshooting.module.css';

const t = defaultI18n.t;

export type SupportPanelProps = Readonly<{
  source: SettingsSupportSource;
  subscribe: (listener: () => void) => () => void;
  reportError: (error: unknown) => void;
  /** The Settings landing owns export controls; Support keeps its existing viewers. */
  exportOnly?: boolean; hideExport?: boolean; advancedRows?: ReactNode; problems?: ReactNode;
  advancedSummary?: readonly string[];
}>;

const KNOWN: ReadonlySet<string> = new Set(CAPABILITY_ROWS);
const WINDOWS = SUPPORT_WINDOWS.map((entry, index) => ({...entry, label: `next.settingsSpec.server.troubleshooting.advanced.groups.1.rows.0.control.options.${index}.label` as MessageId}));
const PART_LABEL: Readonly<Record<SupportComponent, MessageId>> = Object.freeze({
  snapshot: 'web.logs.includeSnapshot', runtime: 'web.logs.includeServer', client: 'web.logs.includeClient', audit: 'web.logs.includeAudit',
});
const capabilityLabel = (capability: string) => KNOWN.has(capability) ? t(`web.capabilities.${capability}` as MessageId) : t('web.capabilities.otherLabel', {id: capability});

/** The drawn Support tab: permissions, what this server can do, the export and what was left out. */
export function SupportPanel(props: SupportPanelProps) {
  const {source, reportError} = props;
  const [pathChoice,setPathChoice] = useState<Readonly<{source:SettingsSupportSource;value:boolean}>>();
  const choice = useRef<typeof pathChoice>(undefined), scope = useRef<SettingsSupportSource|undefined>(source);
  if (scope.current !== source) {scope.current=source;choice.current=undefined;}
  useLayoutEffect(()=>{scope.current=source;choice.current=undefined;return()=>{scope.current=undefined;choice.current=undefined;};},[source]);
  const state = useSyncExternalStore(props.subscribe, props.source.getSnapshot, props.source.getSnapshot);
  const [busy, setBusy] = useState(false), [fixed, setFixed] = useState(false);
  const [window, setWindow] = useState('24h'), [parts, setParts] = useState<readonly SupportComponent[]>(DEFAULT_SUPPORT_COMPONENTS);
  const [advanced, setAdvanced] = useState(false);
  useEffect(() => {void source.load();}, [source]);
  useEffect(() => {const error=state.error as {status?:number;code?:string}|undefined;if(state.phase==='error'&&error&&(error.status===401||error.status===403||['unauthorized','forbidden','not_permitted'].includes(error.code??'')))reportError(error);}, [state.phase,state.error,reportError]);
  const supportNotice = state.phase === 'error' ? <p className={styles.failure} role="alert"><span>{t('next.states.pageFailedBody')}</span><button type="button" onClick={() => {void props.source.retry();}}>{t('next.shell.tryAgain')}</button></p>
    : state.phase === 'idle' || state.phase === 'loading' ? <p role="status">{t('next.settings.loading')}</p> : undefined;
  if (supportNotice && !props.exportOnly) return supportNotice;
  const exportReady = state.export, frozen = state.preparing || !!state.pendingExport;
  const shownParts = state.pendingExport?.components ?? parts;
  const shownWindow = state.pendingExport ? WINDOWS.find(entry=>entry.ms===state.pendingExport!.windowMs)?.id : window;
  const includePaths = state.pendingExport?.includePaths ?? (pathChoice?.source===source && choice.current===pathChoice && pathChoice.value && parts.includes('snapshot'));
  const choosePaths = (value:boolean) => {if(scope.current!==source)return;const next={source,value};choice.current=next;setPathChoice(next);};
  const prepare = () => {
    if(scope.current!==source)return;
    const captured=pathChoice, pending=source.getSnapshot().pendingExport;
    const selected=!!pending?.includePaths || !pending && !!captured && captured.source===source && choice.current===captured && captured.value && parts.includes('snapshot');
    // Consume the opt-in before any source notification can re-enter. A pending
    // request displays its captured choice, but a later export starts unchecked.
    if(choice.current===captured){choice.current=undefined;setPathChoice(current=>current===captured?undefined:current);}
    void source.prepare(WINDOWS.find(entry=>entry.id===window)!.ms,parts,selected).catch(reportError);
  };
  const exportOptions = <SettingsGroup title={t('next.settingsSpec.server.troubleshooting.advanced.groups.1.title')} advanced>
    <SettingsRow label={t('web.logs.timeWindow')}><Select aria-label={t('web.logs.timeWindow')} value={shownWindow ?? window}
      disabled={frozen} options={WINDOWS.map(entry => ({value: entry.id, label: t(entry.label)}))} onValueChange={setWindow}/></SettingsRow>
    {SUPPORT_COMPONENTS.map(component => <SettingsRow key={component} label={t(PART_LABEL[component])}>
      <Switch aria-label={t(PART_LABEL[component])} checked={shownParts.includes(component)} disabled={frozen} onCheckedChange={checked => {
        setParts(current => checked ? [...new Set([...current, component])] : current.filter(part => part !== component));
        if (component === 'snapshot' && !checked) choosePaths(false);
      }}/>
    </SettingsRow>)}
  </SettingsGroup>;
  return <div className={`${styles.reports} ${styles.support}`}>
    {supportNotice}
    {!props.exportOnly && state.permissions && <section className={styles.permissions}>
      <div className={styles.panelHead}><h3>{t('web.maintenance.permissionsTitle')}</h3></div>
      <p className={styles.lede}>{t('web.maintenance.permissionsBody')}</p>
      <div><Button variant="glass" size="small" disabled={busy} onClick={() => {
        setBusy(true);
        void props.source.fixPermissions().then(done => {if (done) setFixed(true);}).catch(props.reportError).finally(() => setBusy(false));
      }}>{t('web.maintenance.fixPermissions')}</Button>
        {fixed && <span className={styles.fixed} role="status">{t('web.maintenance.permissionsFixed')}</span>}</div>
    </section>}
    {!props.exportOnly && <section className={styles.panel}>
      <div className={styles.panelHead}><h3>{t('settings.server.capabilities')}</h3></div>
      <p className={styles.lede}>{t('web.capabilities.help')}</p>
      <ul className={styles.rows}>{state.capabilities.map(capability => <CapabilityRow key={capability.capability} capability={capability} />)}</ul>
    </section>}
    {!props.hideExport && !supportNotice && <section className={styles.panel}>
      <div className={styles.panelHead}><h3>{t('web.logs.exportTitle')}</h3></div>
      <p className={styles.lede}>{t('web.logs.exportLede')}</p>
      {!props.exportOnly && exportOptions}
      {(state.supportsPaths || state.pendingExport?.includePaths) && <div className={styles.exportRow}><label className={styles.check}>
        <input type="checkbox" checked={!!includePaths} disabled={frozen || !parts.includes('snapshot') || !state.supportsPaths} onChange={event=>choosePaths(event.currentTarget.checked)}/>
        {t('next.settingsSpec.server.troubleshooting.groups.1.rows.0.children.0.label')}</label><p className={styles.note}>{t('web.logs.pathsHelp')}</p></div>}
      {state.prepareError && <p role="alert" className={styles.failure}>{t('error.unconfirmedChange')}</p>}
      <div className={styles.exportRow}><span className={styles.fieldLabel}>{t('web.logs.prepareExport')}</span>
        <SettingsAction size="small" appearance="primary" loading={state.preparing} disabled={state.preparing || !parts.length}
          onClick={prepare}>{t(state.pendingExport?'action.tryAgain':'web.logs.prepare')}</SettingsAction></div>
      {exportReady && <div className={styles.exportReady}>
        <b>{t('web.logs.readyToDownload')}</b>
        {exportReady.manifest.includePaths && <p className={styles.note}>{t('web.logs.pathsRequested')}</p>}
        <p className={styles.note}>{t('web.logs.readyHelp', {included: exportReady.manifest.included.join(', ') || t('web.logs.exportNothing'),
          excluded: exportReady.manifest.excluded.length ? exportReady.manifest.excluded.join(', ') : t('web.logs.exportNothing'),
          redaction: exportReady.manifest.redaction, expires: defaultI18n.dateTime(exportReady.expiresAt)})}</p>
        <Button variant="glass" size="small" onClick={() => {
          void props.source.download().then(bundle => {
            if (!props.source.canDownload(bundle.id)) return;
            const blob = new Blob([JSON.stringify(bundle, null, 2)], {type: 'application/json'});
            const url = URL.createObjectURL(blob), link = document.createElement('a');
            try {if (props.source.canDownload(bundle.id)) {link.href = url;link.download = 'portico-support.json';link.click();}} finally {URL.revokeObjectURL(url);}
          }).catch(props.reportError);
        }}>{t('web.logs.downloadJson')}</Button>
      </div>}
    </section>}
    {props.exportOnly && <>{props.problems}<SettingsAdvanced open={advanced} onOpenChange={setAdvanced} summary={[
      ...(props.advancedSummary ?? []), ...(!supportNotice && shownWindow && shownWindow !== '24h' ? [t('web.logs.timeWindow') + ': ' + t(WINDOWS.find(entry => entry.id === shownWindow)!.label)] : []),
      ...(!supportNotice ? SUPPORT_COMPONENTS.filter(part => shownParts.includes(part) !== DEFAULT_SUPPORT_COMPONENTS.includes(part)).map(part => t(PART_LABEL[part])) : []),
    ]}>
      {props.advancedRows}{!supportNotice && exportOptions}
    </SettingsAdvanced></>}
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

function CapabilityRow({capability}: Readonly<{capability: ServerCapability}>) {
  const available = capability.available;
  return <li className={styles.capabilityRow}>
    <div className={styles.reportText}><b>{capabilityLabel(capability.capability)}</b>
      {!available && capability.code && <small>{t(capabilityReasonId(capability.code) as MessageId)}</small>}
      {!available && capability.code && <details className={styles.technical}><summary>{t('web.capabilities.technical')}</summary>
        <p className={styles.note}><code>{capability.code}</code>{capability.detail && <span> {capability.detail}</span>}</p>
      </details>}
    </div>
    <span className={styles.status} data-status={available ? 'resolved' : 'closed'}>{t(available ? 'web.capabilities.available' : 'web.capabilities.unavailable')}</span>
  </li>;
}
