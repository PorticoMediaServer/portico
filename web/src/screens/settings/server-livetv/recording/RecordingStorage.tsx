import {useEffect, useSyncExternalStore} from 'react';
import {defaultI18n, type MessageId} from '@i18n';
import {errorMessage} from '@core/presentation/errors.ts';
import {Button, Select, TextField} from '../../../../kit/controls';
import {LiveSettingRow} from '../../../live/owner/LiveSettingRow';
import {matchingPreset, retentionPresets, spacePresets} from '../../../live/owner/settings-presets';
import type {RecordingStorageController} from './storage-controller';
import styles from '../../../live/owner/owner.module.css';
const t = defaultI18n.t;
export function RecordingStorage({controller, advanced = false}: {advanced?: boolean; controller: RecordingStorageController}) {
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot, controller.getSnapshot), {storage, draft} = state;
  useEffect(() => {if (!advanced) void controller.load();}, [controller, advanced]);
  if (!controller.isActive()) return null;
  const number = (id: MessageId, help: MessageId, value: number, baseline: number, max: number, set: (value: number) => void, unit?: MessageId) => <LiveSettingRow label={t(id)} help={t(help)} value={value} defaultValue={baseline} reset={() => set(baseline)}>
    <TextField type="number" variant="setting" value={value} min={0} max={max} step={unit === 'web.liveTv.gibUnit' ? 'any' : 1} required disabled={state.loading || state.saving || state.uncertain} aria-label={t(id)} onChange={event => {const value = event.currentTarget.valueAsNumber;if (controller.isActive() && Number.isFinite(value) && (unit === 'web.liveTv.gibUnit' || Number.isInteger(value)) && value >= 0 && value <= max) set(value);}}/>{unit && <span className={styles.unit}>{t(unit)}</span>}</LiveSettingRow>;
  if (advanced) return draft && <div className={styles.sources}>
    {number('web.liveTv.keepRecordings', 'web.liveTv.keepRecordingsHelp', draft.keepDays, 0, 3650, keepDays => controller.edit({keepDays}), 'settings.unit.days')}
    {number('web.liveTv.episodesPerSeries', 'web.liveTv.episodesPerSeriesHelp', draft.keepNewest, 0, 100000, keepNewest => controller.edit({keepNewest}))}
    {number('web.liveTv.leaveFree', 'web.liveTv.leaveFreeHelp', draft.floorBytes / 1024**3, 10, 8388607, value => controller.edit({floorBytes: Math.round(value * 1024**3)}), 'web.liveTv.gibUnit')}
    {number('web.liveTv.capUpTo', 'web.liveTv.capUpToHelp', draft.capBytes / 1024**3, 0, 8388607, value => controller.edit({capBytes: Math.round(value * 1024**3)}), 'web.liveTv.gibUnit')}
  </div>;
  const measured = !!storage?.folderOk;
  const retention = draft ? matchingPreset(retentionPresets, draft) : 'custom', space = draft ? matchingPreset(spacePresets, draft) : 'custom';
  return <section className={styles.section}><div className={styles.heading}><h3>{t('channels.recordings')}</h3></div>
    {!!state.error && <p role="alert" className={styles.error}>{errorMessage(state.error)}<Button size="small" disabled={state.saving} onClick={() => {if (!state.uncertain && draft && state.document) void controller.save();else void controller.load();}}>{t('action.tryAgain')}</Button></p>}
    {!storage && state.loading && <p role="status">{t('status.loading')}</p>}
    {storage && draft && <div className={styles.sources}>
      <LiveSettingRow label={t('next.settingsSpec.server.live.groups.6.rows.2.label')} value={retention} defaultValue="all" reset={() => controller.edit(retentionPresets.all)}>
        <Select value={retention} disabled={state.saving || state.loading || state.uncertain} aria-label={t('next.settingsSpec.server.live.groups.6.rows.2.label')}
          options={['all', 'd30', 'd90', 'n5', 'n10', 'custom'].map((value, index) => ({value, disabled: value === 'custom', label: t(`next.settingsSpec.server.live.groups.6.rows.2.control.options.${index}.label` as MessageId)}))}
          onValueChange={value => {if (value in retentionPresets) controller.edit(retentionPresets[value as keyof typeof retentionPresets]);}}/>
      </LiveSettingRow>
      <LiveSettingRow label={t('next.settingsSpec.server.live.groups.7.rows.1.label')} help={measured ? `${t('server.live.usedByRecordings')}: ${defaultI18n.bytes(storage.usedBytes)}. ${t('server.live.freeOnVolume')}: ${defaultI18n.bytes(storage.freeOnDriveBytes)}.` : t('server.usage.notMeasuredYet')} value={space} defaultValue="free10" reset={() => controller.edit(spacePresets.free10)}>
        <Select value={space} disabled={state.saving || state.loading || state.uncertain} aria-label={t('next.settingsSpec.server.live.groups.7.rows.1.label')}
          options={['free10', 'up250', 'up500', 'up1000', 'custom'].map(value => ({value, disabled:value==='custom', label:value==='custom'?t('next.settingsSpec.server.live.groups.7.rows.1.control.options.4.label'):value==='free10'?t('web.liveTv.leaveFreeAmount',{size:defaultI18n.bytes(spacePresets.free10.floorBytes)}):t('web.liveTv.capAmount',{size:defaultI18n.bytes(spacePresets[value as keyof typeof spacePresets].capBytes)})}))}
          onValueChange={value => {if (value in spacePresets) controller.edit(spacePresets[value as keyof typeof spacePresets]);}}/>
      </LiveSettingRow>
      <p>{t('next.live.keepRule', {rule:storage.keepRule})}</p>
      {measured && <p>{t('web.dvr.hoursLeft', {count:storage.hoursLeft})}</p>}
      {!!storage.upcomingBytes && <p>{t('web.liveTv.space.forecast')}: {t('web.liveTv.space.forecastOver', {size:defaultI18n.bytes(storage.upcomingBytes),hours:storage.upcomingHours})}</p>}
    </div>}
  </section>;
}
