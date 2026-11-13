import {useEffect, useSyncExternalStore} from 'react';
import {defaultI18n, type MessageId} from '@i18n';
import type {OwnerDvrSettings,OwnerDvrDocument} from '@core/server-admin/owner-dvr-settings.ts';
import {errorMessage} from '@core/presentation/errors.ts';
import {Button, Select, TextField} from '../../../../kit/controls';
import {LiveSettingRow} from '../../../live/owner/LiveSettingRow';
import type {AutoSaveForm} from '../../../live/owner/auto-save-form';
import {matchingPreset} from '../../../live/owner/settings-presets';
import styles from '../../../live/owner/owner.module.css';
import layout from './recording-defaults.module.css';
const t = defaultI18n.t;
const paddingPresets={standard:{beforeMs:60000,afterMs:180000},exact:{beforeMs:0,afterMs:0},generous:{beforeMs:300000,afterMs:600000},sport:{beforeMs:60000,afterMs:900000}} as const;
export function RecordingDefaults({form, current, advanced = false}: {advanced?: boolean; form: AutoSaveForm<OwnerDvrSettings,OwnerDvrDocument['status']>; current: () => boolean}) {
  const state = useSyncExternalStore(form.subscribe, form.getSnapshot, form.getSnapshot), draft = state.draft;
  useEffect(() => {if (!advanced && current()) void form.load();}, [form, current, advanced]);
  if (!current()) return null;
  const set = (patch: Partial<OwnerDvrSettings>) => {if (current() && !state.saving) form.set(patch);};
  const number = (id: MessageId, value: number, baseline: number, min: number, max: number, change: (value: number) => void, help?: MessageId, unit?: MessageId) => <LiveSettingRow label={t(id)} help={help && t(help)} value={value} defaultValue={baseline} reset={() => change(baseline)}>
    <TextField variant="setting" type="number" aria-label={t(id)} required min={min} max={max} step={1} value={value} disabled={state.saving} onChange={event => {const value = event.currentTarget.valueAsNumber;if (current() && Number.isSafeInteger(value) && value >= min && value <= max) change(value);}}/>{unit && <span className={styles.unit}>{t(unit)}</span>}</LiveSettingRow>;
  if (advanced) return draft && <>
    <section className={styles.section}><div className={styles.heading}><h3>{t('next.settingsSpec.server.live.advanced.groups.3.title')}</h3></div><div className={styles.sources}>
      {number('web.dvr.startEarly', draft.beforeMs / 60000, 1, 0, 30, value => set({beforeMs: value * 60000}), 'web.dvrSettings.startEarlyHelp', 'settings.unit.minutes')}
      {number('web.dvrSettings.finishLate', draft.afterMs / 60000, 3, 0, 120, value => set({afterMs: value * 60000}), 'web.dvrSettings.finishLateHelp', 'settings.unit.minutes')}
    </div></section>
  </>;
  const padding = draft && matchingPreset(paddingPresets, draft);
  return <section className={styles.section}><div className={styles.heading}><h3>{t('web.dvrSettings.defaults')}</h3></div>
    {state.loading && !draft && <p role="status">{t('status.loading')}</p>}
    {!!state.loadError && <p role="alert" className={styles.error}>{errorMessage(state.loadError)}<Button size="small" onClick={() => {if (current()) void form.load();}}>{t('action.tryAgain')}</Button></p>}
    {!!state.saveError && <p role="alert" className={styles.error}>{errorMessage(state.saveError)}<Button size="small" disabled={state.saving} onClick={() => {if (current()) void form.save();}}>{t('action.tryAgain')}</Button></p>}
    {!!state.problem && <p role="alert" className={styles.error}>{t(state.problem as MessageId)}</p>}
    {draft && <div className={`${styles.sources} ${layout.rows}`}>
      <LiveSettingRow label={t('next.settingsSpec.server.live.groups.6.rows.0.label')} value={padding} defaultValue="standard" reset={() => set(paddingPresets.standard)}>
        <Select value={padding} disabled={state.saving} aria-label={t('next.settingsSpec.server.live.groups.6.rows.0.label')}
          options={['standard', 'exact', 'generous', 'sport', 'custom'].map((value, index) => ({value, disabled: value === 'custom', label: t(`next.settingsSpec.server.live.groups.6.rows.0.control.options.${index}.label` as MessageId)}))}
          onValueChange={value => {if (value in paddingPresets) set(paddingPresets[value as keyof typeof paddingPresets]);}}/>
      </LiveSettingRow>
      <LiveSettingRow label={t('server.live.tunerConflict')} help={t('server.live.tunerConflictHelp')} value={(draft.stopViewers?'stop-watching':'keep-watching')} defaultValue="keep-watching" reset={() => set({stopViewers:false})}><Select className={layout.select} containerClassName={layout.selectField} value={(draft.stopViewers?'stop-watching':'keep-watching')} disabled={state.saving} aria-label={t('server.live.tunerConflict')}
        options={[{value: 'keep-watching', label: t('server.live.tunerConflict.keep')}, {value: 'stop-watching', label: t('server.live.tunerConflict.stop')}]}
        onValueChange={value => set({stopViewers:value==='stop-watching'})}/></LiveSettingRow>
    </div>}
  </section>;
}
