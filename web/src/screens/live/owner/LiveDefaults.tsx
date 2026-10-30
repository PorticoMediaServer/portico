import {useSyncExternalStore} from 'react';
import {defaultI18n, type MessageId} from '@i18n';
import type {LiveDefaults as Defaults, parseLiveDefaultsDocument} from '@core/administration.ts';
import {Button, Select, Switch, TextField} from '../../../kit/controls';
import type {AutoSaveForm} from './auto-save-form';
import {bufferingPresets, matchingPreset} from './settings-presets';
import {LiveSettingRow} from './LiveSettingRow';
import styles from './owner.module.css';
const t = defaultI18n.t;
type DefaultsForm = AutoSaveForm<Defaults, ReturnType<typeof parseLiveDefaultsDocument>>;

export function LiveDefaults({form, isCurrent, advanced = false}: {form: DefaultsForm; isCurrent: () => boolean; advanced?: boolean}) {
  const state = useSyncExternalStore(form.subscribe, form.getSnapshot, form.getSnapshot), draft = state.draft;
  if (!draft || !isCurrent()) return null;
  const set = (patch: Partial<Defaults>) => {if (isCurrent() && !state.saving) form.set(patch);};
  const number = (label: MessageId, value: number, baseline: number, min: number, max: number, change: (value: number) => void, unit?: MessageId, help?: MessageId) =>
    <LiveSettingRow label={t(label)} help={help && t(help)} value={value} defaultValue={baseline} reset={() => change(baseline)}><TextField type="number" required min={min} max={max} step={1} value={value}
      variant="setting" aria-label={t(label)} disabled={state.saving} onChange={event => {const value = event.currentTarget.valueAsNumber;if (Number.isSafeInteger(value) && value >= min && value <= max) change(value);}}/>{unit && <span className={styles.unit}>{t(unit)}</span>}</LiveSettingRow>;
  const buffering = matchingPreset(bufferingPresets, draft);
  if (advanced) return <>
    <section className={styles.section}><div className={styles.heading}><h3>{t('next.settingsSpec.server.live.advanced.groups.0.title')}</h3></div><div className={styles.sources}>
      {number('web.dvrSettings.buffer', draft.streamBufferMs / 1000, 8, 0, 120, value => set({streamBufferMs: value * 1000}), 'settings.unit.seconds', 'web.dvrSettings.bufferHelp')}
      {number('web.dvrSettings.retryWindow', draft.retryWindowMs / 1000, 60, 5, 600, value => set({retryWindowMs: value * 1000}), 'settings.unit.seconds', 'web.dvrSettings.retryWindowHelp')}
      {number('web.dvrSettings.guideDays', draft.guideDays, 7, 1, 21, guideDays => set({guideDays}), 'settings.unit.days')}
      <LiveSettingRow label={t('web.dvrSettings.userAgent')} help={t('web.dvrSettings.userAgentHelp')} value={draft.userAgent} defaultValue="" reset={() => set({userAgent: ''})}><TextField value={draft.userAgent} maxLength={200} variant="setting" disabled={state.saving}
        aria-label={t('web.dvrSettings.userAgent')} onChange={event => set({userAgent: event.currentTarget.value})}/></LiveSettingRow>
      <LiveSettingRow label={t('web.dvrSettings.discovery')} help={t('web.dvrSettings.discoveryHelp')} value={draft.discoveryEnabled} defaultValue={true} reset={() => set({discoveryEnabled: true})}><Switch checked={draft.discoveryEnabled} disabled={state.saving} aria-label={t('web.dvrSettings.discovery')}
        onCheckedChange={discoveryEnabled => set({discoveryEnabled})}/></LiveSettingRow>
    </div></section>
    <section className={styles.section}><div className={styles.heading}><h3>{t('server.live.limits')}</h3></div><div className={styles.sources}>
      {number('server.live.limitChannels', draft.importLimits.channels, 50000, 1, 100000, channels => set({importLimits: {...draft.importLimits, channels}}))}
      {number('server.live.limitProgrammes', draft.importLimits.programmes, 5000000, 1, 10000000, programmes => set({importLimits: {...draft.importLimits, programmes}}))}
      {number('server.live.limitSize', draft.importLimits.bytes / 1048576, 512, 1, 1024, bytes => set({importLimits: {...draft.importLimits, bytes: bytes * 1048576}}), 'settings.unit.mb')}
    </div></section>
  </>;
  return <section className={styles.section}><div className={styles.heading}><h3>{t('web.dvrSettings.liveDefaults')}</h3></div><div className={styles.sources}>
    <LiveSettingRow label={t('next.settingsSpec.server.live.groups.1.rows.0.label')} value={buffering} defaultValue="balanced" reset={() => set(bufferingPresets.balanced)}>
      <Select value={buffering} disabled={state.saving} aria-label={t('next.settingsSpec.server.live.groups.1.rows.0.label')}
        options={['balanced', 'smooth', 'live', 'custom'].map((value, index) => ({value, disabled: value === 'custom', label: t(`next.settingsSpec.server.live.groups.1.rows.0.control.options.${index}.label` as MessageId)}))}
        onValueChange={value => {if (value in bufferingPresets) set(bufferingPresets[value as keyof typeof bufferingPresets]);}}/>
    </LiveSettingRow>
    <LiveSettingRow label={t('next.settingsSpec.server.live.groups.1.rows.1.label')} value={draft.guideDays} defaultValue={7} reset={() => set({guideDays: 7})}>
      <Select value={[3, 7, 14].includes(draft.guideDays) ? String(draft.guideDays) : 'custom'} disabled={state.saving} aria-label={t('next.settingsSpec.server.live.groups.1.rows.1.label')}
        options={[...[3, 7, 14].map((value, index) => ({value: String(value), label: t(`next.settingsSpec.server.live.groups.1.rows.1.control.options.${index}.label` as MessageId)})), {value: 'custom', disabled: true, label: t('next.settingsSpec.server.live.groups.1.rows.0.control.options.3.label')}]}
        onValueChange={value => {const guideDays = Number(value);if ([3, 7, 14].includes(guideDays)) set({guideDays});}}/>
    </LiveSettingRow>
    {draft.timeshiftMinutes !== undefined && <LiveSettingRow label={t('next.settingsSpec.server.live.groups.1.rows.2.label')} help={t('next.settingsSpec.server.live.groups.1.rows.2.help')} value={draft.timeshiftMinutes} defaultValue={30} reset={() => set({timeshiftMinutes: 30})}>
      <Select value={String(draft.timeshiftMinutes)} disabled={state.saving} aria-label={t('next.settingsSpec.server.live.groups.1.rows.2.label')}
        options={[10, 30, 60, 120].map((value, index) => ({value: String(value), label: t(`next.settingsSpec.server.live.groups.1.rows.2.control.options.${index}.label` as MessageId)}))}
        onValueChange={value => {const minutes = Number(value);if (minutes === 10 || minutes === 30 || minutes === 60 || minutes === 120) set({timeshiftMinutes: minutes});}}/>
    </LiveSettingRow>}
    <LiveSettingRow label={t('next.settingsSpec.server.live.groups.1.rows.3.label')} value={draft.logoImport} defaultValue={true} reset={() => set({logoImport: true})}><Switch checked={draft.logoImport} disabled={state.saving} aria-label={t('next.settingsSpec.server.live.groups.1.rows.3.label')} onCheckedChange={logoImport => set({logoImport})}/></LiveSettingRow>
  </div>{!!state.saveError && <Button size="small" disabled={state.saving} onClick={() => {if (isCurrent()) void form.save();}}>{t('action.tryAgain')}</Button>}</section>;
}
