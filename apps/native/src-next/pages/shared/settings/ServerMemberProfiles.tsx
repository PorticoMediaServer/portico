import React from 'react';
import type {AccessMember} from '../../../../../../packages/client-core/src/server-administration';
import type {MemberProfile} from '../../../../../../packages/client-core/src/member-profiles';
import {Button, TextField} from '../../../kit/controls';
import {PanelBlock, SectionCard, StateRow} from '../../../kit/frames';
import {BottomSheet} from '../../../kit/overlays';
import {OverlayFootRow} from '../../../kit/overlays/surface';
import {MemberProfilePortrait, MemberProfileRow} from '../../../kit/settings/MemberProfileParts';
import {ProfileColorPicker, ProfileRestrictionsFields} from '../../../kit/settings/ProfileEditorFields';
import {SettingsHelp} from '../../../kit/settings/SettingsParts';
import {SettingsRow} from '../../../kit/shell';
import {StateView} from '../../../kit/states';
import type {NativeMemberProfilesSource} from '../../../screens/settings-live/server/member-profiles-source';
import type {NativePeopleSource} from '../../../screens/settings-live/server/people-source';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {useMemberProfileEditor, useMemberProfiles, useMemberProfilesSource} from './useMemberProfiles';

export function ServerMemberProfiles({people, person, runtime}: {people: NativePeopleSource; person: AccessMember; runtime: NativeShellRuntime}) {
  const source = useMemberProfilesSource({people, person, runtime});
  return source ? <Profiles source={source} runtime={runtime}/> : <SectionCard><StateRow kind="loading"/></SectionCard>;
}

function Profiles({source, runtime}: {source: NativeMemberProfilesSource; runtime: NativeShellRuntime}) {
  const {state, retry} = useMemberProfiles(source), {t} = runtime.text;
  return <>
    <SectionCard>
      {state.items?.map(profile => <MemberProfileRow key={profile.id} testID={`people-profile-${profile.id}`}
        name={profile.name} art={profile.art} locked={profile.pinRequired} picture={size => source.picture(profile, size)}
        facts={[profile.primary ? t('profile.main') : '', profile.pinRequired ? t('profile.protected') : ''].filter(Boolean)}>
        {source.canManage(profile) && <Button testID={`people-profile-manage-${profile.id}`} variant="glass" size="compact"
          onPress={() => source.manage(profile)}>{t('next.settingsSpec.server.people.panels.person.tabs.2.groups.0.rows.0.control.itemAction.label')}</Button>}
      </MemberProfileRow>)}
      {state.loading && <StateRow kind="loading"/>}
    </SectionCard>
    {state.changed && <SettingsHelp>{t('error.changed')}</SettingsHelp>}
    {state.error !== undefined && <StateView variant="inline" {...runtime.error(state.error, retry, 'server-console')}/>}
    {state.managing && <MemberProfileEditor key={`${state.managing.id}:${state.managing.revision}`} source={source} profile={state.managing} runtime={runtime}/>}
  </>;
}

function MemberProfileEditor({source, profile, runtime}: {source: NativeMemberProfilesSource; profile: MemberProfile; runtime: NativeShellRuntime}) {
  const model = useMemberProfileEditor({source, profile, runtime}), {t} = runtime.text;
  const {state, action, name, art, draft} = model;
  return <>
    <BottomSheet testID="people-profile-editor" open title={model.title} description={action?.kind === 'limits' ? t('limits.subtitle') : undefined}
      defaultDetent={1} keyboardAvoiding dismissOnBack={!state.busy} dismissOnGesture={!state.busy} dismissOnOutsidePress={!state.busy}
      onOpenChange={open => {if (!open) model.dismiss();}} footer={action ? <OverlayFootRow layout="halves">
        <Button variant="glass" disabled={state.busy} onPress={model.dismiss}>{t('action.cancel')}</Button>
        <Button testID="people-profile-save" loading={state.busy}
          disabled={state.busy || action.kind === 'rename' && (!name.trim() || name.trim() === profile.name || /[\x00-\x1f\x7f]/.test(name))
            || action.kind === 'colour' && art === profile.art || action.kind === 'limits' && !model.changed}
          onPress={model.save}>{t(action.kind === 'limits' ? 'limits.save' : 'action.save')}</Button>
      </OverlayFootRow> : undefined}>
      {!action && <>
        <MemberProfilePortrait name={profile.name} art={profile.art} locked={profile.pinRequired} picture={size => source.picture(profile, size)}/>
        {model.localPictures && <PanelBlock><Button testID="people-profile-picture" variant="glass"
          disabled={!source.canPickPicture(profile)} loading={state.busy} onPress={model.choosePicture}>
          {t(profile.avatar ? 'profile.changePicture' : 'profile.addPicture')}</Button></PanelBlock>}
        {source.localPictures() && profile.avatar && <PanelBlock><Button testID="people-profile-remove-picture" variant="glass"
          disabled={!source.canManage(profile)} onPress={model.removePicture}>{t('profile.removePicture')}</Button></PanelBlock>}
        <SectionCard>
          <SettingsRow first testID="people-profile-rename" label={t('profile.rename')} navigates onPress={state.busy ? undefined : () => model.open('rename')}/>
          <SettingsRow testID="people-profile-colour" label={t('next.settingsSpec.account.profile.groups.1.rows.1.label')}
            navigates onPress={state.busy ? undefined : () => model.open('colour')}/>
          <SettingsRow testID="people-profile-limits" label={t('profile.limits')} navigates onPress={state.busy ? undefined : () => model.open('limits')}/>
          {profile.pinRequired && <SettingsRow label={t('profile.protected')} control={<Button testID="people-profile-remove-pin"
            variant="danger" size="compact" disabled={state.busy} onPress={model.askRemovePin}>{t('profile.removePin')}</Button>}/>}
        </SectionCard>
      </>}
      {action?.kind === 'rename' && <PanelBlock><TextField testID="people-profile-name" label={t('profile.name')}
        value={name} maxLength={120} editable={!state.busy} onChangeText={model.setName}/></PanelBlock>}
      {action?.kind === 'colour' && <PanelBlock><ProfileColorPicker testID="people-profile-colours" value={art}
        disabled={state.busy} onChange={model.setArt}/></PanelBlock>}
      {action?.kind === 'limits' && <PanelBlock><ProfileRestrictionsFields draft={draft} systems={state.systems} disabled={state.busy}
        recordingLocked={source.context.account.account.role !== 'owner' && !profile.restrictions.allowDvr} current={model.current}
        onChange={model.changeDraft}/></PanelBlock>}
      {state.changed && <SettingsHelp>{t('error.changed')}</SettingsHelp>}
      {state.error !== undefined && <StateView variant="inline" {...runtime.error(state.error, model.retry, 'server-console')}/>}
    </BottomSheet>
    <BottomSheet testID="people-profile-pin-confirmation" open={model.pinOpen} fitContent
      title={t('profile.removePinConfirm.title', {name: profile.name})} description={t('profile.removePinConfirm.body', {name: profile.name})}
      onOpenChange={open => {if (!open) model.cancelRemovePin();}} footer={<OverlayFootRow layout="halves">
        <Button variant="glass" disabled={state.busy} onPress={model.cancelRemovePin}>{t('action.cancel')}</Button>
        <Button testID="people-profile-confirm-remove-pin" variant="danger" disabled={state.busy} onPress={model.removePin}>{t('profile.removePin')}</Button>
      </OverlayFootRow>}>{null}</BottomSheet>
  </>;
}
