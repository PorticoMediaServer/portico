import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {AccountNotice} from '../../../kit/accounts/AccountParts';
import {ProfilePicker} from '../../../kit/artwork';
import {Button, FormError} from '../../../kit/controls';
import {SectionCard, SettingsPageFrame, SheetFrame} from '../../../kit/frames';
import {SettingsRow} from '../../../kit/shell';
import {useTheme} from '../../../kit/theme';
import type {useProfiles} from '../../shared/accounts/useProfiles';

const t = defaultI18n.t;
const profileLimit = 8;

/** Profiles (phone-app 20, Settings > Switch profile): on a phone a page in the Settings sheet, "‹ Settings · Profiles";
 * on a tablet the same page on its own. The intro, the faces with Add profile ("Up to 8"), then the current profile's
 * card: Rename, Add a picture, Set a PIN, Limits and permissions, Forget remembered devices. Its PIN page, recovery
 * and dialogs stay with the route that owns the profile commands. */
export function ProfileSwitchPage({view, status}: {view: ReturnType<typeof useProfiles>; status?: React.ReactNode}) {
  const theme = useTheme(), {active, current, manage} = view;
  const canAdd = manage && view.profiles.length < profileLimit;
  const page = <SettingsPageFrame testID="accounts-profile-switch" title={t('profiles.title')} intro={t('profiles.intro')}
    back={{label: view.backLabel, testID: 'accounts-profile-switch-back', disabled: view.busy, onPress: view.back}}>
    <SectionCard plain>
      <ProfilePicker testID="profile-picker" layout="phone" showTitle={false} focusBusy={view.busy} selectedId={current?.viewer.profileId}
        profiles={view.pickerProfiles}
        onSelect={view.select} {...(canAdd ? {onAdd: view.openAddition, addNote: t('profile.addLimit', {count: profileLimit})} : {})}/>
      {status}
    </SectionCard>
    {manage && view.lease && active && <SectionCard
      label={[active.name, active.primary ? t('profile.main') : active.id === current?.viewer.profileId ? t('profile.current') : ''].filter(Boolean).join(' · ')}
      help={view.localPictures ? undefined : t('profile.pictureInPorticoAccount')}>
      <SettingsRow first navigates testID="profile-rename" label={t('profile.rename')} onPress={() => view.openEditor('rename')}/>
      {view.picturePicker && <SettingsRow navigates testID="profile-picture" onPress={() => view.pickPicture(active)}
        label={t(view.hasPicture(active.id) ? 'profile.changePicture' : 'profile.addPicture')}/>}
      {view.localPictures && view.hasPicture(active.id) && <SettingsRow navigates testID="profile-remove-picture" label={t('profile.removePicture')}
        onPress={() => view.removePicture(active)}/>}
      <SettingsRow navigates testID="profile-pin" label={t(active.pinRequired ? 'profile.changePin' : 'profile.setPin')}
        description={active.pinRequired ? undefined : t('profile.setPinHelp', {name: active.name})} onPress={() => view.openEditor('pin')}/>
      {active.pinRequired && <SettingsRow navigates testID="profile-remove-pin" label={t('profile.removePin')} onPress={() => view.openEditor('remove-pin')}/>}
      <SettingsRow navigates testID="profile-limits" label={t('profile.limits')} description={t('profile.limitsHelp')} onPress={() => view.openEditor('limits')}/>
      <SettingsRow navigates testID="profile-forget-devices" label={t('profile.forgetDevices')} onPress={() => view.openEditor('forget')}/>
    </SectionCard>}
    {(view.problem || view.notice) && <SectionCard plain>
      {view.problem ? <FormError placement="inline">{view.problem}</FormError> : <AccountNotice>{view.notice}</AccountNotice>}
    </SectionCard>}
  </SettingsPageFrame>;
  return theme.platform === 'phone'
    ? <SheetFrame kind="full" testID="accounts-profile-switch-sheet" open onOpenChange={open => {if (!open && !view.busy) view.back();}}>{page}</SheetFrame>
    : page;
}

/** The switch's own failure under the faces, with Refresh when the list changed. */
export function ProfileSwitchStatus({failure, canRefresh, busy, onRefresh}: {failure?: string; canRefresh: boolean; busy: boolean; onRefresh: () => void}) {
  return <>
    {failure && <FormError placement="inline">{failure}</FormError>}
    {canRefresh && <Button variant="text" disabled={busy} onPress={onRefresh}>{t('action.refresh')}</Button>}
  </>;
}
