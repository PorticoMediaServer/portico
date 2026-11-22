import React from 'react';
import {Button, Select, Switch} from '../../../kit/controls';
import {SectionCard} from '../../../kit/frames';
import {BottomSheet} from '../../../kit/overlays';
import {SettingsRow} from '../../../kit/shell';
import {StorageActions, StorageNote} from '../../../kit/settings/StorageParts';
import {LiveFieldCard} from '../../../kit/live/LiveSourceParts';
import {RowButtons, TunerState} from '../../../kit/live/RecordingOwnerParts';
import {NewRuleSheet} from '../../../kit/live/NewRuleSheet';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeShellBootstrapContext} from '../../../shell/bootstrap';
import {FolderExplorer} from '../../../kit/settings/FolderExplorer';
import {useLiveRecording} from '../../shared/live/useLiveRecording';

/** Live TV & Channels › Recording: tuners and what they are doing, who may record, recording rules, then the recording
 * defaults, folder, how long recordings are kept and how much space they may use. Each choice saves when it is made. */
export function LiveRecordingTab({context, runtime}: {context: NativeShellBootstrapContext; runtime: NativeShellRuntime}) {
  const view = useLiveRecording({context, runtime}), {t, busy, defaults} = view;
  return <>
    {view.error !== undefined && <SectionCard plain><StorageNote alert onPage>{view.error}</StorageNote></SectionCard>}
    <SectionCard label={t('web.dvrSettings.tuners')} help={view.tuners && !view.tuners.length ? t('web.dvrSettings.noTuners') : undefined}>
      {view.tuners?.map((row, index) => <SettingsRow key={row.key} first={index === 0} label={row.name} description={row.using} control={<TunerState text={row.status.text} tone={row.status.tone}/>}/>)}
    </SectionCard>
    <SectionCard label={t('web.dvrSettings.whoCanRecord')} help={t('web.liveTv.recordingAccountHelp')}>
      {view.accountGroups.flatMap((account, index) => [
        <SettingsRow key={account.key} first={index === 0} label={account.name} description={account.help}
          control={<Switch accessibilityLabel={account.name} checked={account.allowed} disabled={busy || account.locked} onCheckedChange={account.set}/>}/>,
        ...account.profiles.map(row => <SettingsRow key={`${account.key}:${row.key}`} dependent label={row.name} description={row.help}
          control={<Switch accessibilityLabel={row.name} checked={row.on} disabled={busy || row.locked} onCheckedChange={row.set}/>}/>)])}
    </SectionCard>
    <SectionCard plain label={t('web.recordingRules.title')}>
      <StorageActions onPage><Button testID="live-recording-new-rule" size="compact" variant="glass" disabled={busy} onPress={() => view.setAdding(true)}>{t('web.recordingRules.newRule')}</Button></StorageActions>
      {!view.rules.length && <StorageNote onPage>{t('web.recordingRules.noRules')}</StorageNote>}
    </SectionCard>
    {view.rules.length > 0 && <SectionCard>
      {view.rules.map((rule, index) => <SettingsRow key={rule.key} first={index === 0} label={rule.title} description={rule.facts}
        control={<RowButtons><Button size="xs" variant="glass" disabled={busy} onPress={rule.togglePause}>{rule.pauseLabel}</Button>
          <Button size="xs" variant="glass" disabled={busy} onPress={rule.remove}>{t('web.recordingRules.remove')}</Button></RowButtons>}/>)}
    </SectionCard>}
    {defaults && <>
      <SectionCard plain label={t('web.dvrSettings.defaults')}><LiveFieldCard>
        <Select label={defaults.extraTime.label} value={defaults.extraTime.value} disabled={busy} options={defaults.extraTime.options} onValueChange={defaults.extraTime.choose}/>
        <Select label={t('server.live.tunerConflict')} description={t('server.live.tunerConflictHelp')} value={defaults.conflict.value} disabled={busy} options={defaults.conflict.options} onValueChange={defaults.conflict.choose}/>
      </LiveFieldCard></SectionCard>
      <SectionCard>
        <SettingsRow first label={t('web.liveTv.recordingFolder')} description={defaults.folder}
          control={<Button size="xs" variant="glass" disabled={busy} onPress={() => view.setChoosing(true)}>{t('server.live.choose')}</Button>}/>
      </SectionCard>
      <SectionCard plain><LiveFieldCard>
        <Select label={defaults.keep.label} value={defaults.keep.value} disabled={busy} options={defaults.keep.options} onValueChange={defaults.keep.choose}/>
        <Select label={defaults.space.label} value={defaults.space.value} disabled={busy} options={defaults.space.options} onValueChange={defaults.space.choose}/>
      </LiveFieldCard>
      {defaults.overview && <StorageNote onPage>{defaults.overview}</StorageNote>}
      </SectionCard>
      <FolderExplorer open={view.choosing} onOpenChange={view.setChoosing} api={view.api} title={t('web.liveTv.recordingFolder')} chosen={[]} multiple={false}
        onUse={folders => {const next = folders[0]; if (next) defaults.useFolder(next.path);}} testID="live-recording-folder"/>
    </>}
    <NewRuleSheet context={context} runtime={runtime} open={view.adding} onOpenChange={view.setAdding} onSaved={() => {view.setAdding(false); view.load();}}/>
    <BottomSheet open={!!view.removing} onOpenChange={open => {if (!open) view.closeRemove();}} title={view.removing ? t('web.recordingRules.removeTitle', {name: view.removing.title}) : undefined} description={t('web.recordingRules.removeBody')}
      footer={<><Button variant="glass" disabled={busy} onPress={view.closeRemove}>{t('action.cancel')}</Button>
        <Button variant="danger" loading={busy} onPress={view.confirmRemove}>{t('web.recordingRules.remove')}</Button></>}>
      <SettingsRow first label={t('web.recordingRules.cancelBookings')} control={<Switch accessibilityLabel={t('web.recordingRules.cancelBookings')} checked={view.cancelBookings} disabled={busy} onCheckedChange={view.setCancelBookings}/>}/>
    </BottomSheet>
  </>;
}
