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
    <TextField type="number" variant="setting" value={value} min={0} max={max} step={1} required disabled={state.loading || state.saving} aria-label={t(id)} onChange={event => {const value = event.currentTarget.valueAsNumber;if (controller.isActive() && Number.isSafeInteger(value) && value >= 0 && value <= max) set(value);}}/>{unit && <span className={styles.unit}>{t(unit)}</span>}</LiveSettingRow>;
  if (advanced) return draft && <div className={styles.sources}>
    {number('web.liveTv.keepRecordings', 'web.liveTv.keepRecordingsHelp', draft.retentionDays, 0, 3650, retentionDays => controller.edit({retentionDays}), 'settings.unit.days')}
    {number('web.liveTv.episodesPerSeries', 'web.liveTv.episodesPerSeriesHelp', draft.episodeLimit, 0, 100000, episodeLimit => controller.edit({episodeLimit}))}
    {number('web.liveTv.leaveFree', 'web.liveTv.leaveFreeHelp', draft.floorBytes / 1e9, 10, 9007199, value => controller.edit({floorBytes: value * 1e9}), 'web.liveTv.gbUnit')}
    {number('web.liveTv.capUpTo', 'web.liveTv.capUpToHelp', draft.capBytes / 1e9, 0, 9007199, value => controller.edit({capBytes: value * 1e9}), 'web.liveTv.gbUnit')}
  </div>;
  const measured = !!storage?.measurement.measuredAt;
  const retention = draft ? matchingPreset(retentionPresets, draft) : 'custom', space = draft ? matchingPreset(spacePresets, draft) : 'custom';
  return <section className={styles.section}><div className={styles.heading}><h3>{t('channels.recordings')}</h3></div>
    {!!state.error && <p role="alert" className={styles.error}>{errorMessage(state.error)}<Button size="small" disabled={state.saving} onClick={() => {if (draft && storage && JSON.stringify(draft) !== JSON.stringify(storage.policy)) void controller.save();else void controller.load();}}>{t('action.tryAgain')}</Button></p>}
    {!storage && state.loading && <p role="status">{t('status.loading')}</p>}
    {storage && draft && <div className={styles.sources}>
      <LiveSettingRow label={t('next.settingsSpec.server.live.groups.6.rows.2.label')} value={retention} defaultValue="all" reset={() => controller.edit(retentionPresets.all)}>
        <Select value={retention} disabled={state.saving || state.loading} aria-label={t('next.settingsSpec.server.live.groups.6.rows.2.label')}
          options={['all', 'd30', 'd90', 'n5', 'n10', 'custom'].map((value, index) => ({value, disabled: value === 'custom', label: t(`next.settingsSpec.server.live.groups.6.rows.2.control.options.${index}.label` as MessageId)}))}
          onValueChange={value => {if (value in retentionPresets) controller.edit(retentionPresets[value as keyof typeof retentionPresets]);}}/>
      </LiveSettingRow>
      <LiveSettingRow label={t('next.settingsSpec.server.live.groups.7.rows.1.label')} help={measured ? `${t('server.live.usedByRecordings')}: ${defaultI18n.bytes(storage.measurement.usedBytes)}. ${t('server.live.freeOnVolume')}: ${defaultI18n.bytes(storage.measurement.freeBytes)}.` : t('server.usage.notMeasuredYet')} value={space} defaultValue="free10" reset={() => controller.edit(spacePresets.free10)}>
        <Select value={space} disabled={state.saving || state.loading} aria-label={t('next.settingsSpec.server.live.groups.7.rows.1.label')}
          options={['free10', 'up250', 'up500', 'up1000', 'custom'].map((value, index) => ({value, disabled: value === 'custom', label: t(`next.settingsSpec.server.live.groups.7.rows.1.control.options.${index}.label` as MessageId)}))}
          onValueChange={value => {if (value in spacePresets) controller.edit(spacePresets[value as keyof typeof spacePresets]);}}/>
      </LiveSettingRow>
      {measured && !storage.measurement.writeHealthy && <p role="alert" className={styles.error}>{t('web.liveTv.notWritable')}</p>}
    </div>}
  </section>;
}
