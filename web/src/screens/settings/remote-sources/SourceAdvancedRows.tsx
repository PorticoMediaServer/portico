import {defaultI18n} from '@i18n';
import {RadioGroup} from 'radix-ui';
import type {SourceAdvanced} from '@core/remote-sources.ts';
import {RichSelect, Switch} from '../../../kit/controls';
import {SettingNumber} from '../../../kit/settings';
import type {ReactNode} from 'react';
import type {SourceChoiceField} from '@core/remote-source-choice.ts';
import markerStyles from './source-advanced-markers.module.css';
import rowStyles from './source-advanced-rows.module.css';
import styles from './remote-sources.module.css';

const t = defaultI18n.t;
const formatBytes = defaultI18n.bytes;

/** The drawn Advanced groups (remote-sources.html#4b, #4c), over the values the
 * server actually applies: the folders' check interval, the start and watch
 * passes, the full re-check, the mount's real read cache, and the analysis
 * stages that run only under the source's own advanced-analysis consent, which
 * the single consent control above already states in its canonical words. Rows
 * the server cannot honour are not drawn. */
export type SourceAdvancedMarkers = Readonly<{
  /** Only fields the current projection publishes, intersected with the controls this group draws. */
  applicable: readonly SourceChoiceField[]; changed: readonly SourceChoiceField[];
  /** The caller-formatted baseline (preset) value for that field, from the owner's coherent pair;
   *  absent draws no line. This is not read from the live form. */
  value: (field: SourceChoiceField) => string | undefined;
  onReset?: (field: SourceChoiceField) => void;
}>;
export function SourceAdvancedRows({values, cacheSupported, transportSupported, cacheApplied, cacheError, cacheUsedBytes, observedInterval, disabled, onChange, markers}: Readonly<{
  values: SourceAdvanced; cacheSupported: boolean; transportSupported: boolean; cacheApplied: boolean; cacheError?: string;
  cacheUsedBytes?: number; observedInterval: number; disabled: boolean; onChange: (values: SourceAdvanced) => void;
  /** Absent keeps today's rows exactly: same controls, visibility and layout. */
  markers?: SourceAdvancedMarkers;
}>) {
  const set = <K extends keyof SourceAdvanced,>(key: K, value: SourceAdvanced[K]) => onChange({...values, [key]: value});
  /** A marked row is the same node, wrapped only when markers exist. */
  /** Only the six numeric rows can carry the authored .n.ch ring; selects, switches and the
   *  two-radio groups have no authored ring rule, so they keep the tint, bar and preset line.
   *  A field is marked only when the projection publishes it and it actually changed. */
  const changed = (field: SourceChoiceField) =>
    !!markers && markers.applicable.includes(field) && markers.changed.includes(field);
  /** One drawn Advanced row: the canonical label and help on the left, the control on the right. */
  const anatomy = (label: string, help: string | undefined, node: ReactNode) =>
    <div className={rowStyles.row} data-advanced-row><div className={rowStyles.l}><b>{label}</b>{help && <p>{help}</p>}</div><div className={rowStyles.control}>{node}</div></div>;
  const row = (field: SourceChoiceField, node: ReactNode) => {
    if (!markers) return node;
    if (!markers.applicable.includes(field)) return null;
    const isChanged = markers.changed.includes(field), preset = isChanged ? markers.value(field) : undefined;
    return <div key={field} className={markerStyles.row} data-advanced-row data-changed={isChanged || undefined}>{node}
      {isChanged && preset !== undefined && <span className={markerStyles.preset}><span>{t('next.remoteSources.presetValue', {value: preset})}</span>
        {markers.onReset && <button type="button" className={markerStyles.reset} disabled={disabled} onClick={() => markers.onReset?.(field)}>{t('action.reset')}</button>}</span>}
    </div>;
  };
  const intervals = [
    {value: '900', label: 'next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.0.rows.0.control.options.0.label'},
    {value: '3600', label: 'next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.0.rows.0.control.options.1.label'},
    {value: '21600', label: 'next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.0.rows.0.control.options.2.label'},
    {value: '86400', label: 'next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.0.rows.0.control.options.3.label'},
    {value: '604800', label: 'next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.0.rows.0.control.options.4.label'},
    {value: '0', label: 'next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.0.rows.0.control.options.5.label'},
  ] as const;
  const fullRechecks = [
    {value: '7', label: 'next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.0.rows.3.control.options.0.label'},
    {value: '14', label: 'next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.0.rows.3.control.options.1.label'},
    {value: '30', label: 'next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.0.rows.3.control.options.2.label'},
    {value: '0', label: 'next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.0.rows.3.control.options.3.label'},
  ] as const;
  const cacheSizes = ['0', '2', '4', '8', '16', '32', '64'] as const;
  const floors = ['1', '2', '4', '8', '16'] as const;
  const gb = (value: string) => Number(value) * 1024 * 1024 * 1024;
  const introsOptions = [
    {value: 'off', label: 'next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.1.rows.3.control.options.0.label'},
    {value: 'inherit', label: 'next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.1.rows.3.control.options.1.label'},
    {value: 'sound', label: 'next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.1.rows.3.control.options.2.label'},
    {value: 'picture', label: 'next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.1.rows.3.control.options.3.label'},
  ] as const;
  const offered = intervals.some(option => option.value === String(values.scanIntervalSeconds));
  const observedUnknown = observedInterval > 0 && !intervals.some(option => option.value === String(observedInterval));
  const intervalValue = observedUnknown && !offered ? String(observedInterval) : String(values.scanIntervalSeconds);
  const intervalOptions = [
    ...(observedUnknown ? [{value: String(observedInterval), label: t('next.remoteSources.advObserved', {minutes: Math.round(observedInterval / 60)}), disabled: true}] : []),
    ...intervals.map(option => ({value: option.value, label: t(option.label), description: t('next.remoteSources.advIntervalHint')})),
  ];
  return <>
    <p className={styles.advTitle}>{t('next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.0.title')}</p>
    <div className={rowStyles.group}>
    {row('scanIntervalSeconds', anatomy(t('next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.0.rows.0.label'), undefined, <RichSelect disabled={disabled} aria-label={t('next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.0.rows.0.label')}
      value={intervalValue}
      options={intervalOptions}
      onValueChange={value => set('scanIntervalSeconds', Number(value))}/>))}
    {observedUnknown && <p className={styles.note}>{t('next.remoteSources.advObservedHelp')}</p>}
    {row('watchForChanges', anatomy(t('next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.0.rows.1.label'), t('next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.0.rows.1.help'), <Switch aria-label={t('next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.0.rows.1.label')} checked={values.watchForChanges} disabled={disabled} onCheckedChange={value => set('watchForChanges', value)}/>))}
    {row('scanOnStart', anatomy(t('next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.0.rows.2.label'), t('next.remoteSources.advStartHint'), <Switch aria-label={t('next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.0.rows.2.label')} checked={values.scanOnStart} disabled={disabled} onCheckedChange={value => set('scanOnStart', value)}/>))}
    {row('fullRecheckDays', anatomy(t('next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.0.rows.3.label'), undefined, <RichSelect disabled={disabled} aria-label={t('next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.0.rows.3.label')}
      value={String(values.fullRecheckDays)}
      options={fullRechecks.map(option => ({value: option.value, label: t(option.label), description: t('next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.0.rows.3.help')}))}
      onValueChange={value => set('fullRecheckDays', Number(value))}/>))}

    {row('doubleCheckFolders', anatomy(t('next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.0.rows.4.label'), t('next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.0.rows.4.help'), <Switch aria-label={t('next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.0.rows.4.label')} checked={values.doubleCheckFolders} disabled={disabled} onCheckedChange={value => set('doubleCheckFolders', value)}/>))}

    </div>
    <p className={styles.advTitle}>{t('next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.1.title')}</p>
    <div className={styles.runSkip} role="group" aria-label={t('next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.1.rows.2.label')}>
      <div className={styles.runSkipHead}>
        <span/>
        <span>{t('next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.1.rows.2.control.columns.0.label')}</span>
        <span>{t('next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.1.rows.2.control.columns.1.label')}</span>
      </div>
      {([
        ['seekPreviews', 'next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.1.rows.2.control.items.0.label'],
        ['chapterImages', 'next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.1.rows.2.control.items.1.label'],
        ['waveforms', 'next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.1.rows.2.control.items.2.label'],
        ['loudness', 'next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.1.rows.2.control.items.3.label'],
        ['checksums', 'next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.1.rows.2.control.items.4.label'],
      ] as const).map(([key, label]) => row(key, <RadioGroup.Root key={key} className={[styles.runSkipRow,
        markers?.applicable.includes(key) && markers.changed.includes(key) ? markerStyles.analysisChanged : undefined].filter(Boolean).join(' ')}
        orientation="horizontal" aria-label={t(label)} value={String(values[key])} disabled={disabled}
        onValueChange={value => {if (!disabled && (value === 'true' || value === 'false')) set(key, value === 'true');}}>
        <span>{t(label)}</span>
        <RadioGroup.Item value="true" aria-label={`${t(label)}: ${t('next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.1.rows.2.control.columns.0.label')}`}
          className={styles.runSkipChoice} data-picked={values[key] || undefined} disabled={disabled}>{t('next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.1.rows.2.control.columns.0.label')}</RadioGroup.Item>
        <RadioGroup.Item value="false" aria-label={`${t(label)}: ${t('next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.1.rows.2.control.columns.1.label')}`}
          className={styles.runSkipChoice} data-picked={!values[key] || undefined} disabled={disabled}>{t('next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.1.rows.2.control.columns.1.label')}</RadioGroup.Item>
      </RadioGroup.Root>))}
    </div>
    {row('introsMode', <RichSelect disabled={disabled} aria-label={t('next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.1.rows.3.label')}
      value={values.introsMode} options={introsOptions.map(option => ({value: option.value, label: t(option.label), description: t('next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.1.rows.3.help')}))}
      onValueChange={value => set('introsMode', value as SourceAdvanced['introsMode'])}/>)}

    {cacheSupported && <>
      <p className={styles.advTitle}>{t('next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.2.title')}</p>
      <div className={rowStyles.group}>
      {row('cacheBytes', anatomy(t('next.settingsSpec.server.remote-sources.panels.source.tabs.1.groups.0.rows.1.label'), undefined, <RichSelect disabled={disabled} aria-label={t('next.settingsSpec.server.remote-sources.panels.source.tabs.1.groups.0.rows.1.label')}
        value={String(values.cacheBytes / (1024 * 1024 * 1024))}
        options={cacheSizes.map((size, index) => ({value: size, label: t(`next.settingsSpec.server.remote-sources.panels.source.tabs.1.groups.0.rows.1.control.options.${index}.label` as never)}))}
        onValueChange={value => set('cacheBytes', gb(value))}/>))}
      {row('cacheFloorBytes', anatomy(t('next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.2.rows.0.label'), undefined, <RichSelect disabled={disabled} aria-label={t('next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.2.rows.0.label')}
        value={String(values.cacheFloorBytes / (1024 * 1024 * 1024))}
        options={floors.map((size, index) => ({value: size, label: t(`next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.2.rows.0.control.options.${index}.label` as never)}))}
        onValueChange={value => set('cacheFloorBytes', gb(value))}/>))}
      </div>
      {cacheUsedBytes !== undefined && <p className={styles.note} role="status">{t('next.remoteSources.advCacheUsed', {used: formatBytes(cacheUsedBytes)})}</p>}
      {!cacheApplied && <p className={styles.note} role="status">{t('next.remoteSources.advCachePending')}</p>}
      {cacheError && <p className={styles.error} role="alert">{t(cacheError === 'source_changed' ? 'next.remoteSources.advCacheChanged'
        : cacheError === 'mount_unavailable' ? 'next.remoteSources.advCacheUnavailable'
          : 'next.remoteSources.advCacheFailed')}</p>}
    </>}
    {transportSupported && <>
      <p className={styles.advTitle}>{t('next.remoteSources.advRcloneSettings')}</p>
      <p className={styles.note}>{t('next.remoteSources.advRcloneHelp')}</p>
      <div className={rowStyles.group}>
        {row('dirCacheSeconds', anatomy(t('next.remoteSources.advKeepFolderLists'), undefined, <SettingNumber changed={changed('dirCacheSeconds')} label={t('next.remoteSources.advKeepFolderLists')} value={Math.round(values.dirCacheSeconds / 60)} min={0} max={1440}
          unit={t('next.remoteSources.advMinutesUnit')} disabled={disabled} onCommit={value => {if (value !== null) set('dirCacheSeconds', Math.round(value) * 60);}}/>))}
        {row('readBufferBytes', anatomy(t('next.remoteSources.advReadBuffer'), undefined, <SettingNumber changed={changed('readBufferBytes')} label={t('next.remoteSources.advReadBuffer')} value={Math.round(values.readBufferBytes / (1024 * 1024))} min={0} max={1024}
          unit={t('next.remoteSources.advMegabytesUnit')} disabled={disabled} onCommit={value => {if (value !== null) set('readBufferBytes', Math.round(value) * 1024 * 1024);}}/>))}
        {row('connectTimeoutSeconds', anatomy(t('next.remoteSources.advConnectTimeout'), undefined, <SettingNumber changed={changed('connectTimeoutSeconds')} label={t('next.remoteSources.advConnectTimeout')} value={values.connectTimeoutSeconds} min={0} max={600}
          unit={t('next.remoteSources.advSecondsUnit')} disabled={disabled} onCommit={value => {if (value !== null) set('connectTimeoutSeconds', Math.round(value));}}/>))}
        {row('idleTimeoutSeconds', anatomy(t('next.remoteSources.advIdleTimeout'), undefined, <SettingNumber changed={changed('idleTimeoutSeconds')} label={t('next.remoteSources.advIdleTimeout')} value={values.idleTimeoutSeconds} min={0} max={3600}
          unit={t('next.remoteSources.advSecondsUnit')} disabled={disabled} onCommit={value => {if (value !== null) set('idleTimeoutSeconds', Math.round(value));}}/>))}
        {row('retries', anatomy(t('next.remoteSources.advRetries'), undefined, <SettingNumber changed={changed('retries')} label={t('next.remoteSources.advRetries')} value={values.retries} min={0} max={10}
          unit={t('next.remoteSources.advTimesUnit')} disabled={disabled} onCommit={value => {if (value !== null) set('retries', Math.round(value));}}/>))}
        {row('lowLevelRetries', anatomy(t('next.remoteSources.advLowRetries'), undefined, <SettingNumber changed={changed('lowLevelRetries')} label={t('next.remoteSources.advLowRetries')} value={values.lowLevelRetries} min={0} max={10}
          unit={t('next.remoteSources.advTimesUnit')} disabled={disabled} onCommit={value => {if (value !== null) set('lowLevelRetries', Math.round(value));}}/>))}
      </div>
    </>}
  </>;
}
