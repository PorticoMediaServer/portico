import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {presentError} from '../../../../../../packages/client-core/src/presentation/errors';
import {nextProfileArt} from '../../../../../../packages/client-core/src/profile-management';
import {Button, TextField, FormError} from '../../../kit/controls';
import {BottomSheet, Dialog} from '../../../kit/overlays';
import {ProfileColorPicker} from '../../../kit/settings/ProfileEditorFields';
import {PhonePINEntry} from '../../../kit/accounts/PhonePINEntry';
import {ProfileActionRow} from '../../../kit/accounts/ProfileActionRow';
import {ProfileActionsBlock, ProfileDeletionBody, ProfileDeletionText, ProfileDialogFooter, ProfileEmptyBody, ProfileFieldLabel, ProfileList, ProfileListRow,
  ProfileManagementBlock, ProfileNote, ProfileNotice, ProfilePinFields, ProfileRepeatField} from '../../../kit/accounts/ProfilesParts';
import {profilePicturePickerAvailable} from '../../../platform/profile-avatars';
import {profilePinFailureText} from '../../../screens/accounts/profile-pin-failure';
import {TVProfilePINForm, TVForgotPINHandoff, TVProfileSwitchPage} from '../../../tv/auth';
import {ProfilePINPage} from '../../touch/accounts/ProfilePINPage';
import {ProfileSwitchPage, ProfileSwitchStatus} from '../../touch/accounts/ProfileSwitchPage';
import {ProfileRecoveryPage} from '../../touch/accounts/ProfileRecoveryPage';
import {ProfilesPage as TVProfilesPage} from '../../tv/accounts/ProfilesPage';
// Named through pages/shared (not `./`) so the screen harness loads these siblings for real when a test loads this page.
import {ProfileLimitsSheet} from '../../../pages/shared/accounts/ProfileLimitsSheet';
import {useProfiles, type ProfilesProps} from '../../../pages/shared/accounts/useProfiles';

const t = defaultI18n.t;

/** Profiles on this server: on TV the switch picker, its PIN page and Forgot PIN (one page over the shell); on phones and
 * tablets the switch page and a profile's PIN page; and, embedded in Settings, managing profiles (add, rename, colour,
 * picture, PIN, limits, forget devices, order, delete). Every action and its guard is `useProfiles`'s. */
export function ProfilesRoutePage({context, route, onBack, embedded = false}: ProfilesProps) {
  const view = useProfiles({context, route, onBack, embedded}), tv = view.tv, face = view.face;
  const {canRefresh, pickPicture, removePicture, actionBusy, active, activeTicket, addArt, addName, addNameRef, additionCurrent, alive, artDraft, avatarSource, avatars, back, backRef, busy, busyRef, cancelWrongPin, choose, closeAddition, closeDeletion, closeEdit, current, deleteName, deleteNameRef, deletion, deletionCurrent, deletionRef, edit, editDraftRef, editorCurrent, error, forgetAction, forgetProfile, forgotPIN, identityForLimits, lease, limitsLease, limitsProfile, localPictures, manage, managedId, management, managementCurrent, moveProfile, nameDraft, notice, openAddition, openEditor, pin, pinAgain, pinDraft, problem, profile, profileActionsCurrent, profileFocus, profiles, recovery, refreshProfiles, removePin, removePinAction, retireEditor, retirement, runAction, setAddArt, setAddName, setArtDraft, setAvatars, setBusy, setDeleteName, setDeletion, setError, setForgotPIN, setManagedId, setNameDraft, setNotice, setPin, setPinAgain, setPinDraft, setProblem, setProfile, setRecovery, setRetirement, startRecovery, submitAdd, submitPin, submitRename, tvLeave} = view;
  const errorRefresh = canRefresh
    ? <Button variant="text" disabled={busy} onPress={() => {void refreshProfiles();}}>{t('action.refresh')}</Button> : null;
  if (recovery) return <ProfileRecoveryPage controller={recovery.controller} profileName={recovery.name} username={context.account.account.username}
    onOpen={value => {const id = recovery.id; recovery.controller.dispose(); setRecovery(undefined); setPin(value); void choose(id, value);}}
    onClose={() => {recovery.controller.dispose(); setRecovery(undefined); setPin('');}}/>;
  // TV: the picker, its PIN page and the forgotten-PIN page are one page Modal over the shell (TVProfileSwitchPage).
  if (tv && !embedded) return <TVProfileSwitchPage leave={tvLeave} onBack={() => {if (!busyRef.current) backRef.current();}} onGone={onBack}>
    {profile && forgotPIN ? <TVForgotPINHandoff profileName={profile.name}
    serverAddress={context.api.baseUrl} server={{id: context.account.serverId, signIn: context.account.account.hostedAccountId ? 'portico' : 'local'}} onBack={back}/>
    : profile ? <TVProfilePINForm
    profile={{id: profile.id, name: profile.name, ...face(profile), primary: profile.primary, pinRequired: true}}
    code={pin} onCodeChange={setPin} busy={busy} error={error ? <FormError placement="inline">{profilePinFailureText(error) ?? presentError(error, 'profiles').body}</FormError> : undefined}
    onSubmit={value => {void choose(profile.id, value);}} onBack={back}
    onForgotPIN={() => {if (!busy) {cancelWrongPin(); setPin(''); setError(undefined); setForgotPIN(true);}}}/>
    : <TVProfilesPage serverName={context.serverName} picker={{
      // Only the faces take focus. Profile management stays in Settings on touch.
      focusScope: lease?.live.value && lease.route === route ? `tv.account.profiles.${JSON.stringify([lease.authority, lease.revision])}` : undefined,
      focusBusy: busy, profiles: profiles.map(value => ({id: value.id, name: value.name, ...face(value),
        primary: value.primary, pinRequired: value.pinRequired, disabled: busy, source: avatarSource(value.id)})),
      selectedId: current?.viewer.profileId, preferredProfileId: lease ? profileFocus.get(lease) : undefined,
      onFocusChange: id => {if (id && lease) profileFocus.set(lease, id);},
      onSelect: id => {
        const value = profiles.find(item => item.id === id)!;
        if (id === current?.viewer.profileId) {
          back();
        } else if (value.pinRequired) {
          setProfile(value);
          setPin('');
          setError(undefined);
        } else {
          void choose(id);
        }
      },
    }} status={<>
      {!!error && <FormError>{profilePinFailureText(error) ?? presentError(error, 'profiles').body}</FormError>}
      {errorRefresh}
    </>}/>}
  </TVProfileSwitchPage>;
  const managementActions = manage && lease && active ? <ProfileActionsBlock heading={[active.name, active.primary ? t('profile.main') : active.id === current?.viewer.profileId ? t('profile.current') : ''].filter(Boolean).join(' · ')}
          after={<>
          {problem ? <FormError>{problem}</FormError> : null}
          {notice ? <ProfileNotice>{notice}</ProfileNotice> : null}
          </>}>
            {!localPictures && <ProfileNote>{t('profile.pictureInPorticoAccount')}</ProfileNote>}
            <ProfileActionRow testID="profile-rename" label={t('profile.rename')} onPress={() => openEditor('rename')}/>
            {localPictures && profilePicturePickerAvailable() ? <ProfileActionRow testID="profile-picture" label={t(avatars.some(value => value.profileId === active.id) ? 'profile.changePicture' : 'profile.addPicture')} onPress={() => pickPicture(active)}/> : null}
            {localPictures && avatars.some(value => value.profileId === active.id) ? <ProfileActionRow testID="profile-remove-picture" label={t('profile.removePicture')} onPress={() => removePicture(active)}/> : null}
            <ProfileActionRow testID="profile-pin" label={t(active.pinRequired ? 'profile.changePin' : 'profile.setPin')} description={active.pinRequired ? undefined : t('profile.setPinHelp', {name: active.name})} onPress={() => openEditor('pin')}/>
            {active.pinRequired ? <ProfileActionRow testID="profile-remove-pin" label={t('profile.removePin')} onPress={() => openEditor('remove-pin')}/>: null}
            <ProfileActionRow testID="profile-limits" label={t('profile.limits')} description={t('profile.limitsHelp')} onPress={() => openEditor('limits')}/>
            <ProfileActionRow testID="profile-forget-devices" label={t('profile.forgetDevices')} onPress={() => openEditor('forget')}/>
            {embedded && profiles.indexOf(active) > 0 && <ProfileActionRow testID="profile-move-up" label={t('web.profile.moveUp')} onPress={() => moveProfile(active, -1)}/>}
            {embedded && profiles.indexOf(active) < profiles.length - 1 && <ProfileActionRow testID="profile-move-down" label={t('web.profile.moveDown')} onPress={() => moveProfile(active, 1)}/>}
            {embedded && !active.primary && <Button variant="danger" size="compact" disabled={actionBusy} onPress={() => {
              if (!management || !profileActionsCurrent()) return;
              void runAction(async ({manager}) => {const preview = await manager.previewDelete(active); if (lease.current() && managementCurrent()) {const next = {preview, management}; deletionRef.current = next; deleteNameRef.current = ''; setDeletion(next); setDeleteName('');}});
            }}>{t('profile.delete')}</Button>}
        </ProfileActionsBlock> : null;
  const failureLine = error ? <FormError placement="inline">{profilePinFailureText(error) ?? presentError(error, 'profiles').body}</FormError> : undefined;
  return <>
    {embedded ? <ProfileManagementBlock>
      <ProfileList>
        {profiles.map(value => <ProfileListRow key={value.id} testID={`settings-profile-${value.id}`} name={value.name} disabled={!manage || actionBusy}
          onPress={() => {if (!manage || !lease?.current() || activeTicket.current) return; retireEditor(); closeDeletion(); setManagedId(value.id); setProblem(''); setNotice('');}}
          detail={[value.primary ? t('profile.main') : '', value.id === current?.viewer.profileId ? t('profile.current') : ''].filter(Boolean).join(' · ')}
          initial={[...value.name][0] ?? ''} art={value.art} locked={value.pinRequired} source={avatarSource(value.id)} more={manage}/>)}
        {manage && profiles.length < 8 && <ProfileActionRow testID="settings-profile-add" label={t('profile.add')} onPress={openAddition}/>}
      </ProfileList>
      {problem && !managedId ? <FormError placement="inline">{problem}</FormError> : null}
      <BottomSheet open={!!managedId && !!active && manage} onOpenChange={open => {if (!open && !activeTicket.current && managementCurrent() && !retirement) {retireEditor(); setManagedId(undefined); closeDeletion();}}} title={active?.name} dismissOnBack={!actionBusy} dismissOnGesture={!actionBusy} dismissOnOutsidePress={!actionBusy}>{managementActions}</BottomSheet>
    </ProfileManagementBlock> : profile ? <ProfilePINPage testID="accounts-profile-switch" profile={{name: profile.name, face: {...face(profile), source: avatarSource(profile.id)}}}
      value={pin} onChange={setPin} onSubmit={value => {void choose(profile.id, value);}} busy={busy} rejected={!!error}
      status={failureLine}
      onForgot={() => {void startRecovery();}} onBack={back}/>
    : <ProfileSwitchPage view={view} status={<ProfileSwitchStatus canRefresh={canRefresh} busy={busy} onRefresh={() => {void refreshProfiles();}}
        failure={error ? profilePinFailureText(error) ?? presentError(error, 'profiles').body : undefined}/>}/>}
    <Dialog testID="profile-add" open={additionCurrent()} onOpenChange={open => {if (!open) closeAddition();}} title={t('profile.addTitle')}
      footer={<ProfileDialogFooter><Button focusId="cancel" variant="glass" disabled={actionBusy} onPress={closeAddition}>{t('action.cancel')}</Button><Button focusId="save" loading={actionBusy} disabled={!addName.trim()} onPress={submitAdd}>{t('action.save')}</Button></ProfileDialogFooter>}>
      <TextField label={t('profile.name')} value={addName} onChangeText={value => {if (additionCurrent() && !activeTicket.current) {addNameRef.current = value; setAddName(value);}}} maxLength={120} testID="profile-add-name"/>
      {!tv && <ProfileColorPicker value={addArt ?? nextProfileArt(profiles)} onChange={value => {if (additionCurrent() && !activeTicket.current) setAddArt(value);}} disabled={actionBusy} testID="profile-add-colors"/>}
    </Dialog>
    <Dialog testID="profile-rename" open={edit?.kind === 'rename' && editorCurrent()} onOpenChange={open => {if (!open) closeEdit();}} title={t('profile.renameTitle', {name: edit?.profile.name ?? ''})}
      footer={<ProfileDialogFooter><Button focusId="cancel" variant="glass" disabled={actionBusy} onPress={closeEdit}>{t('action.cancel')}</Button><Button focusId="save" loading={actionBusy} disabled={!nameDraft.trim()} onPress={submitRename}>{t('action.save')}</Button></ProfileDialogFooter>}>
      <TextField label={t('profile.name')} value={nameDraft} onChangeText={value => {if (edit?.kind === 'rename' && editorCurrent() && !activeTicket.current) {editDraftRef.current = {...editDraftRef.current, name: value}; setNameDraft(value);}}} maxLength={120} testID="profile-rename-name"/>
      {!tv && <ProfileColorPicker value={artDraft} onChange={value => {if (edit?.kind === 'rename' && editorCurrent() && !activeTicket.current) setArtDraft(value);}} disabled={actionBusy} testID="profile-rename-colors"/>}
    </Dialog>
    <Dialog testID="profile-pin-edit" open={edit?.kind === 'pin' && editorCurrent()} onOpenChange={open => {if (!open) closeEdit();}} title={t('profile.pinTitle', {name: edit?.profile.name ?? ''})}
      footer={<ProfileDialogFooter><Button focusId="cancel" variant="glass" disabled={actionBusy} onPress={closeEdit}>{t('action.cancel')}</Button><Button focusId="save" loading={actionBusy} disabled={pinDraft.length !== 4 || pinAgain.length !== 4} onPress={submitPin}>{t('action.save')}</Button></ProfileDialogFooter>}>
      {tv ? <>
        <TextField label={t('profile.pinChoose')} value={pinDraft} onChangeText={value => {if (edit?.kind === 'pin' && editorCurrent() && !activeTicket.current) {editDraftRef.current = {...editDraftRef.current, pin: value}; setPinDraft(value);}}} keyboardType="number-pad" secureTextEntry maxLength={4} testID="profile-pin-new"/>
        <ProfileRepeatField label={t('profile.pinRepeat')} value={pinAgain} onChangeText={value => {if (edit?.kind === 'pin' && editorCurrent() && !activeTicket.current) {editDraftRef.current = {...editDraftRef.current, again: value}; setPinAgain(value);}}} keyboardType="number-pad" secureTextEntry maxLength={4} testID="profile-pin-again"/>
      </> : <ProfilePinFields>
        <ProfileFieldLabel>{t('profile.pinChoose')}</ProfileFieldLabel>
        <PhonePINEntry testID="profile-pin-new" label={t('profile.pinChoose')} value={pinDraft} onChange={value => {if (edit?.kind === 'pin' && editorCurrent() && !activeTicket.current) {editDraftRef.current = {...editDraftRef.current, pin: value}; setPinDraft(value);}}} busy={actionBusy}/>
        <ProfileFieldLabel>{t('profile.pinRepeat')}</ProfileFieldLabel>
        <PhonePINEntry testID="profile-pin-again" label={t('profile.pinRepeat')} value={pinAgain} onChange={value => {if (edit?.kind === 'pin' && editorCurrent() && !activeTicket.current) {editDraftRef.current = {...editDraftRef.current, again: value}; setPinAgain(value);}}} busy={actionBusy} autoFocus={false}/>
      </ProfilePinFields>}
      {problem ? <FormError>{problem}</FormError> : null}
    </Dialog>
    <Dialog testID="profile-remove-pin" open={!!removePin && editorCurrent()} onOpenChange={open => {if (!open) closeEdit();}} title={t('profile.removePinConfirm.title', {name: removePin?.name ?? ''})} description={t('profile.removePinConfirm.body', {name: removePin?.name ?? ''})}
      footer={<ProfileDialogFooter><Button focusId="cancel" variant="glass" disabled={actionBusy} onPress={closeEdit}>{t('action.cancel')}</Button><Button focusId="remove-pin" loading={actionBusy} onPress={removePinAction}>{t('profile.removePin')}</Button></ProfileDialogFooter>}><ProfileEmptyBody/></Dialog>
    <Dialog testID="profile-forget-devices" open={!!forgetProfile && editorCurrent()} onOpenChange={open => {if (!open) closeEdit();}} title={t('profile.forgetDevices')}
      footer={<ProfileDialogFooter><Button focusId="cancel" variant="glass" disabled={actionBusy} onPress={closeEdit}>{t('action.cancel')}</Button><Button focusId="forget-devices" loading={actionBusy} onPress={forgetAction}>{t('profile.forgetDevices')}</Button></ProfileDialogFooter>}><ProfileEmptyBody/></Dialog>
    <BottomSheet open={deletionCurrent()} title={t('profile.delete')} onOpenChange={open => {if (!open && !activeTicket.current && deletionCurrent()) closeDeletion();}} dismissOnBack={!actionBusy} dismissOnGesture={!actionBusy} dismissOnOutsidePress={!actionBusy}
      footer={retirement === 'removed'
        ? <ProfileDialogFooter><Button testID="profile-delete-done" focusId="done" variant="glass" onPress={() => {if (deletionCurrent() && !activeTicket.current) {closeDeletion(); setManagedId(undefined);}}}>{t('action.done')}</Button></ProfileDialogFooter>
        : <ProfileDialogFooter><Button variant="glass" disabled={actionBusy} onPress={() => {if (deletionCurrent() && !activeTicket.current) closeDeletion();}}>{t('action.cancel')}</Button><Button variant="danger" loading={actionBusy} disabled={!deletionCurrent() || deleteName !== (deletion?.preview.name ?? profiles.find(value => value.id === deletion?.preview.profileId)?.name)} onPress={() => {
          const captured = deletion;
          if (!deletionCurrent(captured) || captured.preview.primary || deleteNameRef.current !== deleteName || deleteName !== (captured.preview.name ?? profiles.find(value => value.id === captured.preview.profileId)?.name)) return;
          // The accepted retirement is waited out on the same preview: only `removed` ends it,
          // and the sheet stays open saying so after the profile leaves the refreshed list.
          void runAction(({manager}) => manager.delete(captured.preview, {onStatus: status => {
            if (alive.current && deletionCurrent(captured)) setRetirement(status.state === 'removed' ? 'removed' : 'removing');
          }}), undefined, () => deletionCurrent(captured));
        }}>{t('profile.delete')}</Button></ProfileDialogFooter>}>
      <ProfileDeletionText>
        {retirement === 'removed'
          ? <ProfileDeletionBody live>{t('profile.deleteConfirm.removed')}</ProfileDeletionBody>
          : <><ProfileDeletionBody>{t('profile.deleteConfirm.body')}</ProfileDeletionBody>
            {retirement === 'removing' ? <ProfileNote testID="profile-delete-retirement" live>{t('profile.deleteConfirm.removing')}</ProfileNote> : null}</>}
        {retirement !== 'removed' && deletion?.preview.playlists ? <ProfileNote>{t('profile.deleteConfirm.playlists', {count: deletion.preview.playlists})}</ProfileNote> : null}
        {retirement !== 'removed' && deletion?.preview.savedResources ? <ProfileNote>{t('profile.deleteConfirm.savedItems', {count: deletion.preview.savedResources})}</ProfileNote> : null}
      </ProfileDeletionText>
      {retirement !== 'removed' ? <TextField label={t('profile.name')} description={deletion?.preview.name ?? profiles.find(value => value.id === deletion?.preview.profileId)?.name} value={deleteName} onChangeText={value => {if (deletionCurrent() && !activeTicket.current) {deleteNameRef.current = value; setDeleteName(value);}}} editable={!actionBusy}/> : null}
      {problem ? <FormError placement="inline">{problem}</FormError> : null}
    </BottomSheet>
    {limitsProfile && limitsLease && editorCurrent() ? <ProfileLimitsSheet profile={limitsProfile} lease={limitsLease} serverOwner={context.account.account.role === 'owner'} identityFor={identityForLimits} onClose={closeEdit} onSaved={() => {if (editorCurrent()) {setNotice(t('limits.saved', {name: limitsProfile.name})); closeEdit();}}}/> : null}
  </>;
}

