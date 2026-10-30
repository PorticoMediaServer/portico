import {useEffect, useSyncExternalStore} from 'react';
import {defaultI18n, type MessageId} from '@i18n';
import {serverChoices} from '@core/presentation/settings-structure.ts';
import type {DVRDefaults, DVRDocument} from '@core/administration.ts';
import {errorMessage} from '@core/presentation/errors.ts';
import {Button, Select, Switch, TextField} from '../../../../kit/controls';
import {LiveSettingRow} from '../../../live/owner/LiveSettingRow';
import type {AutoSaveForm} from '../../../live/owner/auto-save-form';
import {layoutPresets, matchingPreset, paddingPresets} from '../../../live/owner/settings-presets';
import styles from '../../../live/owner/owner.module.css';
import layout from './recording-defaults.module.css';
const t = defaultI18n.t;
export function RecordingDefaults({form, current, advanced = false}: {advanced?: boolean; form: AutoSaveForm<DVRDefaults, DVRDocument>; current: () => boolean}) {
  const state = useSyncExternalStore(form.subscribe, form.getSnapshot, form.getSnapshot), draft = state.draft;
  useEffect(() => {if (!advanced && current()) void form.load();}, [form, current, advanced]);
  if (!current()) return null;
  const set = (patch: Partial<DVRDefaults>) => {if (current() && !state.saving) form.set(patch);};
  const number = (id: MessageId, value: number, baseline: number, min: number, max: number, change: (value: number) => void, help?: MessageId, unit?: MessageId) => <LiveSettingRow label={t(id)} help={help && t(help)} value={value} defaultValue={baseline} reset={() => change(baseline)}>
    <TextField variant="setting" type="number" aria-label={t(id)} required min={min} max={max} step={1} value={value} disabled={state.saving} onChange={event => {const value = event.currentTarget.valueAsNumber;if (current() && Number.isSafeInteger(value) && value >= min && value <= max) change(value);}}/>{unit && <span className={styles.unit}>{t(unit)}</span>}</LiveSettingRow>;
  if (advanced) return draft && <>
    <section className={styles.section}><div className={styles.heading}><h3>{t('next.settingsSpec.server.live.advanced.groups.3.title')}</h3></div><div className={styles.sources}>
      {number('web.dvr.startEarly', draft.prePaddingMs / 60000, 1, 0, 30, value => set({prePaddingMs: value * 60000}), 'web.dvrSettings.startEarlyHelp', 'settings.unit.minutes')}
      {number('web.dvrSettings.finishLate', draft.postPaddingMs / 60000, 3, 0, 120, value => set({postPaddingMs: value * 60000}), 'web.dvrSettings.finishLateHelp', 'settings.unit.minutes')}
    </div></section>
    <section className={styles.section}><div className={styles.heading}><h3>{t('next.settingsSpec.server.live.advanced.groups.4.title')}</h3></div><div className={styles.sources}>
      <LiveSettingRow label={t('next.settingsSpec.server.live.advanced.groups.4.rows.0.label')} value={matchingPreset(layoutPresets, draft)} defaultValue="own" reset={() => set(layoutPresets.own)}>
        <Select value={matchingPreset(layoutPresets, draft)} disabled={state.saving} aria-label={t('next.settingsSpec.server.live.advanced.groups.4.rows.0.label')}
          options={['own', 'season', 'flat', 'custom'].map((value, index) => ({value, disabled: value === 'custom', label: t(`next.settingsSpec.server.live.advanced.groups.4.rows.0.control.options.${index}.label` as MessageId)}))}
          onValueChange={value => {if (value in layoutPresets) set(layoutPresets[value as keyof typeof layoutPresets]);}}/>
      </LiveSettingRow>
      <LiveSettingRow label={t('web.dvrSettings.folderShows')} help={state.status?.folderTokens.map(token => `{${token.token}}`).join(', ')} value={draft.folderTemplate} defaultValue="" reset={() => set({folderTemplate: ''})}><TextField variant="setting" value={draft.folderTemplate} maxLength={400} disabled={state.saving} aria-label={t('web.dvrSettings.folderShows')} onChange={event => set({folderTemplate: event.currentTarget.value})}/></LiveSettingRow>
      <LiveSettingRow label={t('web.dvrSettings.folderMovies')} help={t('next.settingsSpec.server.live.advanced.groups.4.rows.2.help')} value={draft.movieFolderTemplate} defaultValue="" reset={() => set({movieFolderTemplate: ''})}><TextField variant="setting" value={draft.movieFolderTemplate} maxLength={400} disabled={state.saving} aria-label={t('web.dvrSettings.folderMovies')} onChange={event => set({movieFolderTemplate: event.currentTarget.value})}/></LiveSettingRow>
      <LiveSettingRow label={t('next.settingsSpec.server.live.advanced.groups.4.rows.3.label')} help={t('next.settingsSpec.server.live.advanced.groups.4.rows.3.help')} value={draft.keepPolicy.mode} defaultValue="keep-all" reset={() => set({keepPolicy: {...draft.keepPolicy, mode: 'keep-all'}})}><Select value={draft.keepPolicy.mode} disabled={state.saving} aria-label={t('next.settingsSpec.server.live.advanced.groups.4.rows.3.label')}
        options={[{value: 'keep-all', label: t('web.logs.everything')}, {value: 'keep-count', label: t('web.dvrSettings.keepNewest')}, {value: 'keep-days', label: t('web.dvrSettings.keepDays')}]}
        onValueChange={mode => set({keepPolicy: {...draft.keepPolicy, mode: mode as DVRDefaults['keepPolicy']['mode']}})}/></LiveSettingRow>
      {draft.keepPolicy.mode === 'keep-count' && number('web.dvrSettings.howMany', draft.keepPolicy.keepCount, 5, 1, 1000, keepCount => set({keepPolicy: {...draft.keepPolicy, keepCount}}))}
      {draft.keepPolicy.mode === 'keep-days' && number('web.dvrSettings.howManyDays', draft.keepPolicy.keepDays, 30, 1, 3650, keepDays => set({keepPolicy: {...draft.keepPolicy, keepDays}}), undefined, 'settings.unit.days')}
    </div></section>
  </>;
  const padding = draft && matchingPreset(paddingPresets, draft);
  const conversion = draft ? serverChoices('conversionModes', state.status, t, draft.conversion.mode) : [];
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
      <LiveSettingRow label={t('server.live.tunerConflict')} help={t('server.live.tunerConflictHelp')} value={draft.liveTunerConflict} defaultValue="keep-watching" reset={() => set({liveTunerConflict: 'keep-watching'})}><Select className={layout.select} containerClassName={layout.selectField} value={draft.liveTunerConflict} disabled={state.saving} aria-label={t('server.live.tunerConflict')}
        options={[{value: 'keep-watching', label: t('server.live.tunerConflict.keep')}, {value: 'stop-watching', label: t('server.live.tunerConflict.stop')}]}
        onValueChange={value => set({liveTunerConflict: value as DVRDefaults['liveTunerConflict']})}/></LiveSettingRow>
      {(conversion.length > 1 || draft.conversion.mode !== 'none') && <>
        <LiveSettingRow label={t('web.dvrSettings.afterRecording')} value={draft.conversion.mode} defaultValue="none" reset={() => set({conversion: {...draft.conversion, mode: 'none', deleteOriginal: false}})}><Select className={layout.select} containerClassName={layout.selectField} value={draft.conversion.mode} disabled={state.saving} aria-label={t('web.dvrSettings.afterRecording')}
          options={conversion} onValueChange={mode => set({conversion: {...draft.conversion, mode: mode as DVRDefaults['conversion']['mode'], ...(mode === 'none' ? {deleteOriginal: false} : {})}})}/></LiveSettingRow>
        {draft.conversion.mode !== 'none' && <LiveSettingRow label={t('web.dvrSettings.removeOriginal')} value={draft.conversion.deleteOriginal} defaultValue={false} reset={() => set({conversion: {...draft.conversion, deleteOriginal: false}})}><Switch checked={draft.conversion.deleteOriginal} disabled={state.saving} aria-label={t('web.dvrSettings.removeOriginal')} onCheckedChange={deleteOriginal => set({conversion: {...draft.conversion, deleteOriginal}})}/></LiveSettingRow>}
      </>}
    </div>}
  </section>;
}
