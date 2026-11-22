import React from 'react';
import {Button, Select, Switch} from '../controls';
import {BottomSheet} from '../overlays';
import {SettingsRow} from '../shell';
import {StorageNote, StorageSheetBody} from '../settings/StorageParts';
import type {NativeShellRuntime} from '../../shell/runtime';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import {useNewRule} from '../../pages/shared/live/useLiveRecording';

/** New recording rule: a profile that may record, a day and a programme from the guide, and New episodes only. */
export function NewRuleSheet({context, runtime, open, onOpenChange, onSaved}: {context: NativeShellBootstrapContext; runtime: NativeShellRuntime; open: boolean; onOpenChange: (open: boolean) => void; onSaved: () => void}) {
  const view = useNewRule({context, runtime, open, onSaved}), {t, busy} = view;
  return <BottomSheet open={open} onOpenChange={next => {if (!next && !busy) onOpenChange(false);}} title={t('web.recordingRules.newTitle')} scrollable testID="live-new-rule"
    footer={<><Button variant="glass" disabled={busy} onPress={() => onOpenChange(false)}>{t('action.cancel')}</Button>
      <Button testID="live-new-rule-save" loading={busy} disabled={!view.canSave} onPress={view.save}>{t('action.save')}</Button></>}>
    <StorageSheetBody>
      {view.error !== undefined && <StorageNote alert>{view.error}</StorageNote>}
      <Select label={t('web.recordingRules.recordFor')} value={view.profileId} disabled={busy} options={view.profiles} onValueChange={view.setProfileId}/>
      <Select label={t('web.recordingRules.guideDate')} value={view.day} disabled={busy} options={view.days} onValueChange={view.setDay}/>
      <Select label={t('web.recordingRules.seriesFromGuide')} value={view.programme} disabled={busy || !view.profileId} options={view.programmes} onValueChange={view.setProgramme}/>
      {view.more && <Button size="compact" variant="glass" disabled={busy} onPress={view.more}>{t('web.live.empty.moreTitle')}</Button>}
      <SettingsRow first label={t('guide.newOnly')} control={<Switch accessibilityLabel={t('guide.newOnly')} checked={view.newOnly} disabled={busy} onCheckedChange={view.setNewOnly}/>}/>
    </StorageSheetBody>
  </BottomSheet>;
}
