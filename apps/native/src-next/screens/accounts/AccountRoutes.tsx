import {createProfileAvatarOpeningTransport} from '../../../../../packages/client-core/src/query/profile-security-transport';
import {readDirectAccount} from '../../../../../packages/client-core/src/query/connection-operations.ts';
import React, {useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {Dimensions, Platform, Text, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {presentError} from '../../../../../packages/client-core/src/presentation/errors';
import {profilePinFailureText} from './profile-pin-failure';
import {ManagedProfilesClient, nextProfileArt, parseDirectSnapshot, type ManagedProfile, type ProfileArt, type ProfileDeletion} from '../../../../../packages/client-core/src/profile-management';
import {routePath} from '../../../../../packages/contracts/src/routes';
import {IdentityClient} from '../../../../../packages/client-core/src/identity-client';
import type {ProfileRestrictions, RatingSystem} from '../../../../../packages/client-core/src/identity-restrictions';
import {HttpHostedApi} from '../../../../../packages/client-core/src';
import type {SetupPreview} from '../../../../../packages/client-core/src/setup-code';
import type {TVConsentApprover} from '../../../../../packages/client-core/src/tv-consent';
import type {AppServerSession} from '../../../../../packages/client-core/src/app-sessions';
import {nativeAccounts} from '../../platform/accounts';
import {nativeAppSessions, subscribeNativeAppSessions} from '../../platform/app-session-storage';
import {switchNativeProfile} from '../../platform/native-session-lifecycle';
import {NativeProfileRecovery, temporaryRecoveryApi} from '../../platform/profile-recovery';
import {hostedUrl} from '../../platform/account-authority';
import {pickProfilePicture, profileAvatarUpload, profilePicturePickerAvailable, profilePictureSource} from '../../platform/profile-avatars';
import type {ProfileAvatar} from '../../../../../packages/client-core/src/identity-restrictions';
import {nativeNetworkFetch} from '../../platform/server-connections';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import type {NativeServersPanel, NativeShellRoute} from '../../shell/routes';
import {NativeScreenLifetime} from '../settings-live/lifetime';
import {ProfileColorPicker, ProfileRestrictionsFields} from '../../kit/settings/ProfileEditorFields';
import {sameAuthority, type ShellAuthority} from '../../shell/model';
import {nativeShellRoutes, type NativeRouteFactory} from '../../shell/route-registry';
import {TVAccountFields, TVAuthLayout} from '../../kit/frames/TVAuthLayout';
import {StateLayout, StateView} from '../../kit/states';
import {ProfileFace, ProfilePicker} from '../../kit/artwork';
import {Button, Focusable, TextField, FormError} from '../../kit/controls';
import {BottomSheet, Dialog} from '../../kit/overlays';
import {Icon} from '../../kit/Icon';
import {offlineDownloads, offlineOwnerKey} from '../../../platform/offline-downloads';
import {useTheme} from '../../kit/theme';
import {AccountEntry} from './AccountEntry';
import {createAccountDownloadsEntry, type AccountDownloadsEntry} from './downloads-entry';
import {PhonePINEntry} from '../../kit/accounts/PhonePINEntry';
import {ProfilePINPage} from '../../pages/touch/accounts/ProfilePINPage';
import {ProfileSwitchPage, ProfileSwitchStatus} from '../../pages/touch/accounts/ProfileSwitchPage';
import {completeTVLinkCode, formatTVLinkCode} from './account-form-values';
import {ProfileRecoveryPage} from '../../pages/touch/accounts/ProfileRecoveryPage';
import {ServerChooserPage} from '../../pages/touch/accounts/ServerChooserPage';
import {TrustedCertificatesPage} from '../../pages/shared/accounts/TrustedCertificatesPage';
import {TVServerChooserPage, TVServerSettingsPanel} from '../../pages/tv/accounts/ServerChooserPage';
import {removeCapturedServerDownloads} from './sign-out-downloads';
import {ProfilesPage as TVProfilesPage} from '../../pages/tv/accounts/ProfilesPage';
import {TVProfilePINForm, TVForgotPINHandoff, TVProfileSwitchPage} from '../../tv/auth';
import {registerBack} from '../../kit/navigation/back';
import {useProfiles, type ProfileLease, type ProfilesProps} from '../../pages/shared/accounts/useProfiles';

const t = defaultI18n.t;
type Panel = 'servers' | 'add' | 'sign-out-server' | 'forget-server' | 'sign-out-account' | 'consent-kind' | 'consent-code' | 'consent-review' | 'consent-done';

/** Genuine account destinations for the current authority. Shell media routes and Home
 * remain with their existing owners. Runtime disposal retires every private view below. */
export function AccountRoute({route, context}: {route: NativeShellRoute; context: NativeShellBootstrapContext}) {
  const back = () => {context.navigation.back(context.authority());};
  return route.kind === 'profiles' ? <ProfileRoute context={context} route={route} onBack={back}/> : <ServersRoute context={context} initialPanel={route.kind === 'servers' ? route.initialPanel : undefined} onBack={back}/>;
}

export function ProfileRoute({context, route, onBack, embedded = false}: ProfilesProps) {
  const theme = useTheme(), view = useProfiles({context, route, onBack, embedded});
  const {canRefresh, pickPicture, removePicture, actionBusy, active, activeTicket, addArt, addName, addNameRef, additionCurrent, alive, artDraft, avatarSource, avatars, back, backRef, busy, busyRef, cancelWrongPin, choose, closeAddition, closeDeletion, closeEdit, current, deleteName, deleteNameRef, deletion, deletionCurrent, deletionRef, edit, editDraftRef, editorCurrent, error, forgetAction, forgetProfile, forgotPIN, identityForLimits, lease, limitsLease, limitsProfile, localPictures, manage, managedId, management, managementCurrent, moveProfile, nameDraft, notice, openAddition, openEditor, pin, pinAgain, pinDraft, problem, profile, profileActionsCurrent, profileFocus, profiles, recovery, refreshProfiles, removePin, removePinAction, retireEditor, retirement, runAction, setAddArt, setAddName, setArtDraft, setAvatars, setBusy, setDeleteName, setDeletion, setError, setForgotPIN, setManagedId, setNameDraft, setNotice, setPin, setPinAgain, setPinDraft, setProblem, setProfile, setRecovery, setRetirement, startRecovery, submitAdd, submitPin, submitRename, tvLeave} = view;
  const errorRefresh = canRefresh
    ? <Button variant="text" disabled={busy} onPress={() => {void refreshProfiles();}}>{t('action.refresh')}</Button> : null;
  if (recovery) return <ProfileRecoveryPage controller={recovery.controller} profileName={recovery.name} username={context.account.account.username}
    onOpen={value => {const id = recovery.id; recovery.controller.dispose(); setRecovery(undefined); setPin(value); void choose(id, value);}}
    onClose={() => {recovery.controller.dispose(); setRecovery(undefined); setPin('');}}/>;
  // TV: the picker, its PIN page and the forgotten-PIN page are one page Modal over the shell (TVProfileSwitchPage).
  if (theme.isTV && !embedded) return <TVProfileSwitchPage leave={tvLeave} onBack={() => {if (!busyRef.current) backRef.current();}} onGone={onBack}>
    {profile && forgotPIN ? <TVForgotPINHandoff profileName={profile.name}
    serverAddress={context.api.baseUrl} server={{id: context.account.serverId, signIn: context.account.account.hostedAccountId ? 'portico' : 'local'}} onBack={back}/>
    : profile ? <TVProfilePINForm
    profile={{id: profile.id, name: profile.name, initial: [...profile.name][0] ?? '', color: profile.art, primary: profile.primary, pinRequired: true}}
    code={pin} onCodeChange={setPin} busy={busy} error={error ? <FormError placement="inline">{profilePinFailureText(error) ?? presentError(error, 'profiles').body}</FormError> : undefined}
    onSubmit={value => {void choose(profile.id, value);}} onBack={back}
    onForgotPIN={() => {if (!busy) {cancelWrongPin(); setPin(''); setError(undefined); setForgotPIN(true);}}}/>
    : <TVProfilesPage serverName={context.serverName} picker={{
      // Only the faces take focus. Profile management stays in Settings on touch.
      focusScope: lease?.live.value && lease.route === route ? `tv.account.profiles.${JSON.stringify([lease.authority, lease.revision])}` : undefined,
      focusBusy: busy, profiles: profiles.map(value => ({id: value.id, name: value.name, initial: [...value.name][0] ?? '', color: value.art,
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
  const managementActions = manage && lease && active ? <View testID="profile-actions" style={{marginTop: 18}}>
          <Text style={[theme.type.captionStrong, {color: theme.colors.textTertiary, marginBottom: 8}]}>{[active.name, active.primary ? t('profile.main') : active.id === current?.viewer.profileId ? t('profile.current') : ''].filter(Boolean).join(' · ')}</Text>
          <View style={{borderRadius: 14, overflow: 'hidden', backgroundColor: theme.colors.raised}}>
            {!localPictures && <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{t('profile.pictureInPorticoAccount')}</Text>}
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
          </View>
          {problem ? <FormError>{problem}</FormError> : null}
          {notice ? <Text accessibilityLiveRegion="polite" style={[theme.type.caption, {color: theme.colors.textSecondary, marginTop: 8}]}>{notice}</Text> : null}
        </View> : null;
  const failureLine = error ? <FormError placement="inline">{profilePinFailureText(error) ?? presentError(error, 'profiles').body}</FormError> : undefined;
  return <>
    {embedded ? <View testID="settings-profile-management" style={{gap: 8}}>
      <View style={{borderRadius: 14, overflow: 'hidden', backgroundColor: theme.colors.raised}}>
        {profiles.map(value => <Focusable key={value.id} testID={`settings-profile-${value.id}`} accessibilityRole="button" accessibilityLabel={value.name} disabled={!manage || actionBusy} onPress={() => {if (!manage || !lease?.current() || activeTicket.current) return; retireEditor(); closeDeletion(); setManagedId(value.id); setProblem(''); setNotice('');}} focusKind="row" style={{minHeight: 62, paddingHorizontal: 14, paddingVertical: 8, flexDirection: 'row', alignItems: 'center', gap: 12}}>
          <ProfileFace initial={[...value.name][0] ?? ''} color={value.art} size={44} locked={value.pinRequired} source={avatarSource(value.id)} scaleOnFocus={false}/>
          <View style={{flex: 1}}><Text style={[theme.type.body, {color: theme.colors.text, fontWeight: '700'}]}>{value.name}</Text><Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{[value.primary ? t('profile.main') : '', value.id === current?.viewer.profileId ? t('profile.current') : ''].filter(Boolean).join(' · ')}</Text></View>
          {manage && <Icon name="i-more" size={20} color={theme.colors.textTertiary}/>}
        </Focusable>)}
        {manage && profiles.length < 8 && <ProfileActionRow testID="settings-profile-add" label={t('profile.add')} onPress={openAddition}/>}
      </View>
      {problem && !managedId ? <FormError placement="inline">{problem}</FormError> : null}
      <BottomSheet open={!!managedId && !!active && manage} onOpenChange={open => {if (!open && !activeTicket.current && managementCurrent() && !retirement) {retireEditor(); setManagedId(undefined); closeDeletion();}}} title={active?.name} dismissOnBack={!actionBusy} dismissOnGesture={!actionBusy} dismissOnOutsidePress={!actionBusy}>{managementActions}</BottomSheet>
    </View> : profile ? <ProfilePINPage testID="accounts-profile-switch" profile={{name: profile.name, face: {initial: [...profile.name][0] ?? '', color: profile.art, source: avatarSource(profile.id)}}}
      value={pin} onChange={setPin} onSubmit={value => {void choose(profile.id, value);}} busy={busy} rejected={!!error}
      status={failureLine}
      onForgot={() => {void startRecovery();}} onBack={back}/>
    : <ProfileSwitchPage view={view} status={<ProfileSwitchStatus canRefresh={canRefresh} busy={busy} onRefresh={() => {void refreshProfiles();}}
        failure={error ? profilePinFailureText(error) ?? presentError(error, 'profiles').body : undefined}/>}/>}
    <Dialog testID="profile-add" open={additionCurrent()} onOpenChange={open => {if (!open) closeAddition();}} title={t('profile.addTitle')}
      footer={<View style={{flexDirection: 'row', gap: 8}}><Button focusId="cancel" variant="glass" disabled={actionBusy} onPress={closeAddition}>{t('action.cancel')}</Button><Button focusId="save" loading={actionBusy} disabled={!addName.trim()} onPress={submitAdd}>{t('action.save')}</Button></View>}>
      <TextField label={t('profile.name')} value={addName} onChangeText={value => {if (additionCurrent() && !activeTicket.current) {addNameRef.current = value; setAddName(value);}}} maxLength={120} testID="profile-add-name"/>
      {!theme.isTV && <ProfileColorPicker value={addArt ?? nextProfileArt(profiles)} onChange={value => {if (additionCurrent() && !activeTicket.current) setAddArt(value);}} disabled={actionBusy} testID="profile-add-colors"/>}
    </Dialog>
    <Dialog testID="profile-rename" open={edit?.kind === 'rename' && editorCurrent()} onOpenChange={open => {if (!open) closeEdit();}} title={t('profile.renameTitle', {name: edit?.profile.name ?? ''})}
      footer={<View style={{flexDirection: 'row', gap: 8}}><Button focusId="cancel" variant="glass" disabled={actionBusy} onPress={closeEdit}>{t('action.cancel')}</Button><Button focusId="save" loading={actionBusy} disabled={!nameDraft.trim()} onPress={submitRename}>{t('action.save')}</Button></View>}>
      <TextField label={t('profile.name')} value={nameDraft} onChangeText={value => {if (edit?.kind === 'rename' && editorCurrent() && !activeTicket.current) {editDraftRef.current = {...editDraftRef.current, name: value}; setNameDraft(value);}}} maxLength={120} testID="profile-rename-name"/>
      {!theme.isTV && <ProfileColorPicker value={artDraft} onChange={value => {if (edit?.kind === 'rename' && editorCurrent() && !activeTicket.current) setArtDraft(value);}} disabled={actionBusy} testID="profile-rename-colors"/>}
    </Dialog>
    <Dialog testID="profile-pin-edit" open={edit?.kind === 'pin' && editorCurrent()} onOpenChange={open => {if (!open) closeEdit();}} title={t('profile.pinTitle', {name: edit?.profile.name ?? ''})}
      footer={<View style={{flexDirection: 'row', gap: 8}}><Button focusId="cancel" variant="glass" disabled={actionBusy} onPress={closeEdit}>{t('action.cancel')}</Button><Button focusId="save" loading={actionBusy} disabled={pinDraft.length !== 4 || pinAgain.length !== 4} onPress={submitPin}>{t('action.save')}</Button></View>}>
      {theme.isTV ? <>
        <TextField label={t('profile.pinChoose')} value={pinDraft} onChangeText={value => {if (edit?.kind === 'pin' && editorCurrent() && !activeTicket.current) {editDraftRef.current = {...editDraftRef.current, pin: value}; setPinDraft(value);}}} keyboardType="number-pad" secureTextEntry maxLength={4} testID="profile-pin-new"/>
        <TextField label={t('profile.pinRepeat')} value={pinAgain} onChangeText={value => {if (edit?.kind === 'pin' && editorCurrent() && !activeTicket.current) {editDraftRef.current = {...editDraftRef.current, again: value}; setPinAgain(value);}}} keyboardType="number-pad" secureTextEntry maxLength={4} testID="profile-pin-again" containerStyle={{marginTop: 12}}/>
      </> : <View style={{gap: 12}}>
        <Text style={[theme.type.captionStrong, {color: theme.colors.text}]}>{t('profile.pinChoose')}</Text>
        <PhonePINEntry testID="profile-pin-new" label={t('profile.pinChoose')} value={pinDraft} onChange={value => {if (edit?.kind === 'pin' && editorCurrent() && !activeTicket.current) {editDraftRef.current = {...editDraftRef.current, pin: value}; setPinDraft(value);}}} busy={actionBusy}/>
        <Text style={[theme.type.captionStrong, {color: theme.colors.text}]}>{t('profile.pinRepeat')}</Text>
        <PhonePINEntry testID="profile-pin-again" label={t('profile.pinRepeat')} value={pinAgain} onChange={value => {if (edit?.kind === 'pin' && editorCurrent() && !activeTicket.current) {editDraftRef.current = {...editDraftRef.current, again: value}; setPinAgain(value);}}} busy={actionBusy} autoFocus={false}/>
      </View>}
      {problem ? <FormError>{problem}</FormError> : null}
    </Dialog>
    <Dialog testID="profile-remove-pin" open={!!removePin && editorCurrent()} onOpenChange={open => {if (!open) closeEdit();}} title={t('profile.removePinConfirm.title', {name: removePin?.name ?? ''})} description={t('profile.removePinConfirm.body', {name: removePin?.name ?? ''})}
      footer={<View style={{flexDirection: 'row', gap: 8}}><Button focusId="cancel" variant="glass" disabled={actionBusy} onPress={closeEdit}>{t('action.cancel')}</Button><Button focusId="remove-pin" loading={actionBusy} onPress={removePinAction}>{t('profile.removePin')}</Button></View>}><View/></Dialog>
    <Dialog testID="profile-forget-devices" open={!!forgetProfile && editorCurrent()} onOpenChange={open => {if (!open) closeEdit();}} title={t('profile.forgetDevices')}
      footer={<View style={{flexDirection: 'row', gap: 8}}><Button focusId="cancel" variant="glass" disabled={actionBusy} onPress={closeEdit}>{t('action.cancel')}</Button><Button focusId="forget-devices" loading={actionBusy} onPress={forgetAction}>{t('profile.forgetDevices')}</Button></View>}><View/></Dialog>
    <BottomSheet open={deletionCurrent()} title={t('profile.delete')} onOpenChange={open => {if (!open && !activeTicket.current && deletionCurrent()) closeDeletion();}} dismissOnBack={!actionBusy} dismissOnGesture={!actionBusy} dismissOnOutsidePress={!actionBusy}
      footer={retirement === 'removed'
        ? <View style={{flexDirection: 'row', gap: 8}}><Button testID="profile-delete-done" focusId="done" variant="glass" onPress={() => {if (deletionCurrent() && !activeTicket.current) {closeDeletion(); setManagedId(undefined);}}}>{t('action.done')}</Button></View>
        : <View style={{flexDirection: 'row', gap: 8}}><Button variant="glass" disabled={actionBusy} onPress={() => {if (deletionCurrent() && !activeTicket.current) closeDeletion();}}>{t('action.cancel')}</Button><Button variant="danger" loading={actionBusy} disabled={!deletionCurrent() || deleteName !== (deletion?.preview.name ?? profiles.find(value => value.id === deletion?.preview.profileId)?.name)} onPress={() => {
          const captured = deletion;
          if (!deletionCurrent(captured) || captured.preview.primary || deleteNameRef.current !== deleteName || deleteName !== (captured.preview.name ?? profiles.find(value => value.id === captured.preview.profileId)?.name)) return;
          // The accepted retirement is waited out on the same preview: only `removed` ends it,
          // and the sheet stays open saying so after the profile leaves the refreshed list.
          void runAction(({manager}) => manager.delete(captured.preview, {onStatus: status => {
            if (alive.current && deletionCurrent(captured)) setRetirement(status.state === 'removed' ? 'removed' : 'removing');
          }}), undefined, () => deletionCurrent(captured));
        }}>{t('profile.delete')}</Button></View>}>
      <View style={{gap: 4}}>
        {retirement === 'removed'
          ? <Text accessibilityLiveRegion="polite" style={[theme.type.body, {color: theme.colors.textSecondary}]}>{t('profile.deleteConfirm.removed')}</Text>
          : <><Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{t('profile.deleteConfirm.body')}</Text>
            {retirement === 'removing' ? <Text testID="profile-delete-retirement" accessibilityLiveRegion="polite" style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{t('profile.deleteConfirm.removing')}</Text> : null}</>}
        {retirement !== 'removed' && deletion?.preview.playlists ? <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{t('profile.deleteConfirm.playlists', {count: deletion.preview.playlists})}</Text> : null}
        {retirement !== 'removed' && deletion?.preview.savedResources ? <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{t('profile.deleteConfirm.savedItems', {count: deletion.preview.savedResources})}</Text> : null}
      </View>
      {retirement !== 'removed' ? <TextField label={t('profile.name')} description={deletion?.preview.name ?? profiles.find(value => value.id === deletion?.preview.profileId)?.name} value={deleteName} onChangeText={value => {if (deletionCurrent() && !activeTicket.current) {deleteNameRef.current = value; setDeleteName(value);}}} editable={!actionBusy}/> : null}
      {problem ? <FormError placement="inline">{problem}</FormError> : null}
    </BottomSheet>
    {limitsProfile && limitsLease && editorCurrent() ? <ProfileLimitsSheet profile={limitsProfile} lease={limitsLease} serverOwner={context.account.account.role === 'owner'} identityFor={identityForLimits} onClose={closeEdit} onSaved={() => {if (editorCurrent()) {setNotice(t('limits.saved', {name: limitsProfile.name})); closeEdit();}}}/> : null}
  </>;
}

function ProfileActionRow({label, description, onPress, testID}: {label: string; description?: string; onPress: () => void; testID?: string}) {
  const theme = useTheme();
  return <Focusable testID={testID} accessibilityRole="button" accessibilityLabel={label} accessibilityHint={description} onPress={onPress} focusKind="row" focusRadius={0}
    style={{minHeight: 48, paddingHorizontal: 14, flexDirection: 'row', alignItems: 'center', gap: 12, borderTopWidth: 1, borderTopColor: theme.colors.lineSoft}}>
    <View style={{flex: 1}}><Text style={[theme.type.body, {color: theme.colors.text}]}>{label}</Text>{description ? <Text style={[theme.type.caption, {color: theme.colors.textTertiary, marginTop: 2}]}>{description}</Text> : null}</View>
    <Icon name="i-right" size={18} color={theme.colors.lineStrong}/>
  </Focusable>;
}

type ProfileLimitsOwner = {
  /** Private witness: the opening names only the profile it was drawn for. The sheet's
   * copy and its own save guard keep reading the original profile prop. */
  readonly profile: Readonly<Pick<ManagedProfile, 'id'>>;
  readonly lease: ProfileLease;
  controller?: AbortController;
  retired: boolean;
};

/** `serverOwner`: only the server owner may switch Recordings on (Who can record); anyone who
 * manages the profile may switch it off. */
function ProfileLimitsSheet({profile, lease, serverOwner, identityFor, onClose, onSaved}: {profile: ManagedProfile; lease: ProfileLease; serverOwner: boolean; identityFor: (controller: AbortController) => IdentityClient; onClose: () => void; onSaved: () => void}) {
  const [owner, setOwner] = useState<ProfileLimitsOwner>();
  const ownerRef = useRef<ProfileLimitsOwner | undefined>(undefined);
  const [restrictions, setRestrictions] = useState<ProfileRestrictions>(), [systems, setSystems] = useState<readonly RatingSystem[]>();
  const [draft, setDraft] = useState<ProfileRestrictions>(), [error, setError] = useState<unknown>(), [busy, setBusy] = useState(false), [attempt, setAttempt] = useState(0);
  const draftRef = useRef<ProfileRestrictions | undefined>(undefined), busyRef = useRef(false);
  const profileId = profile.id;
  const current = (candidate: ProfileLimitsOwner) => candidate === ownerRef.current && !candidate.retired && candidate.profile.id === profileId
    && candidate.lease.current();

  useLayoutEffect(() => {
    const next: ProfileLimitsOwner = {profile: {id: profileId}, lease, retired: false};
    const controller = new AbortController();
    next.controller = controller;
    ownerRef.current = next;
    setOwner(next);
    draftRef.current = undefined; busyRef.current = false;
    setRestrictions(undefined); setSystems(undefined); setDraft(undefined); setError(undefined); setBusy(false);
    const live = () => next === ownerRef.current && !next.retired && next.profile.id === profileId
      && next.lease.current();
    const identity = identityFor(controller);
    Promise.all([identity.restrictions(profileId, controller.signal), identity.ratingSystems(controller.signal)]).then(([currentRestrictions, list]) => {
      if (!live() || currentRestrictions.profileId !== next.profile.id) return;
      draftRef.current = currentRestrictions;
      setRestrictions(currentRestrictions); setSystems(list); setDraft(currentRestrictions);
    }, failure => {if (live() && !controller.signal.aborted) setError(failure);});
    return () => {
      next.retired = true;
      next.controller?.abort();
      if (ownerRef.current === next) ownerRef.current = undefined;
      setOwner(value => value === next ? undefined : value);
    };
  }, [identityFor, lease, profileId, attempt]);

  const updateDraft = (change: Partial<ProfileRestrictions>) => {
    if (!owner || !current(owner) || busyRef.current || !draft || draftRef.current !== draft) return;
    const next = {...draft, ...change}; draftRef.current = next; setDraft(next);
  };
  const close = () => {if (owner && current(owner) && !busyRef.current) onClose();};
  const save = () => {
    const sheet = owner, currentRestrictions = restrictions, chosen = draft;
    if (!sheet || !current(sheet) || !currentRestrictions || !chosen || currentRestrictions.profileId !== sheet.profile.id
      || sheet.profile.id !== profile.id || draftRef.current !== chosen || busyRef.current) return;
    const controller = new AbortController();
    sheet.controller?.abort();
    sheet.controller = controller;
    busyRef.current = true; setBusy(true); setError(undefined);
    const identity = identityFor(controller);
    void identity.saveRestrictions(currentRestrictions, {
      ratingSystem: chosen.ratingSystem, maximumAgeRating: chosen.maximumAgeRating, allowUnrated: chosen.allowUnrated, blockedLabels: chosen.blockedLabels,
      allowDownloads: chosen.allowDownloads, allowLiveTv: chosen.allowLiveTv, allowDvr: chosen.allowDvr, allowWatchTogether: chosen.allowWatchTogether,
    }).then(next => {
      if (!current(sheet) || controller.signal.aborted || next.profileId !== sheet.profile.id) return;
      draftRef.current = next; busyRef.current = false;
      setRestrictions(next); setDraft(next); setBusy(false); onSaved();
    }, failure => {
      if (!current(sheet) || controller.signal.aborted) return;
      busyRef.current = false;
      setError(failure); setBusy(false);
    }).finally(() => {
      if (sheet.controller === controller) sheet.controller = undefined;
    });
  };
  return <BottomSheet open onOpenChange={open => {if (!open) close();}} title={t('limits.title', {name: profile.name})} description={t('limits.subtitle')}
    testID="profile-limits" footer={<View style={{flexDirection: 'row', gap: 8}}><Button focusId="cancel" variant="glass" disabled={busy} onPress={close}>{t('action.cancel')}</Button><Button focusId="save" loading={busy} disabled={!draft || !owner || !current(owner)} onPress={save}>{t('limits.save')}</Button></View>}>
    {error ? <StateView title={presentError(error, 'profiles').title} body={presentError(error, 'profiles').body} primaryAction={{id: 'retry', label: t('action.tryAgain'), onPress: () => setAttempt(value => value + 1)}}/> : !draft ? <StateView title={t('status.loading')}/> : <View style={{gap: 14}}>
      <ProfileRestrictionsFields draft={draft} systems={systems ?? []} disabled={busy} recordingLocked={!serverOwner && !restrictions?.allowDvr} current={() => !!owner && current(owner)} onChange={updateDraft}/>
    </View>}

  </BottomSheet>;
}

/** Also usable when no selected server can restore: retained servers remain reachable. */
/** `embedded`: a TV's Switch server Settings page (connect-24) draws the chooser inside Settings, which owns Back; Use a local
 * server and Sign out of Portico Account open this route on their own panel (`openPanel`), and leaving that panel leaves the route. */
export function ServersRoute(props: {
  context?: NativeShellBootstrapContext; fallbackDownloads?: AccountDownloadsEntry; initialPanel?: 'servers' | NativeServersPanel;
  embedded?: boolean; openPanel?: (panel: NativeServersPanel) => void; onBack?: () => void; onComplete?: () => void;
  onSetupComplete?: (serverId: string) => void;
}) {
  const {context, fallbackDownloads, initialPanel = 'servers', embedded = false, openPanel, onBack, onComplete, onSetupComplete} = props;
  const theme = useTheme(), [panel, setPanel] = useState<Panel>(initialPanel), [servers, setServers] = useState<Awaited<ReturnType<typeof nativeAccounts.snapshot>>>(), [busy, setBusy] = useState(false), [error, setError] = useState<unknown>();
  const [captured, setCaptured] = useState<{session: AppServerSession; name: string}>(), [code, setCode] = useState('');
  // Opened straight on Sign out of Portico Account (from a TV's Settings page): the account signed in now is the one asked about.
  const [accountScope, setAccountScope] = useState<{accountId: string; familyId: string} | undefined>(() => {
    if (initialPanel !== 'sign-out-account') return undefined;
    const session = nativeAccounts.accountCredentials().getSnapshot().session;
    return session ? {accountId: session.account.id, familyId: session.familyId} : undefined;
  });
  const [preview, setPreview] = useState<Readonly<SetupPreview>>(), [consentKind, setConsentKind] = useState<'local' | 'portico'>(), [done, setDone] = useState('');
  const [removeDownloads, setRemoveDownloads] = useState(false);
  // Embedded: a second press of Sign out of Portico Account while the account is being checked opens nothing more.
  const openingSignOut = useRef(false);
  const [addGeneration, setAddGeneration] = useState(0);
  // "Sign out of Portico Account" is offered whenever this app holds one, whatever servers are saved (ruling, 13 Nov).
  const accountCredentials = nativeAccounts.accountCredentials();
  const accountHeld = useSyncExternalStore(accountCredentials.subscribe, () => accountCredentials.getSnapshot().session !== undefined, () => false);
  useEffect(() => {void accountCredentials.synchronize().catch(() => {});}, [accountCredentials]);
  type AddOpening = {active: boolean; generation: number; context?: NativeShellBootstrapContext; fallback?: AccountDownloadsEntry; downloads?: AccountDownloadsEntry; disposeDownloads?: () => void};
  const addOwner = useRef<AddOpening | undefined>(undefined), [committedAdd, setCommittedAdd] = useState<AddOpening>();
  const panelRef = useRef(panel), addIntent = useRef({generation: addGeneration, context});
  panelRef.current = panel; addIntent.current = {generation: addGeneration, context};
  if (addOwner.current && (panel !== 'add' || addOwner.current.context !== context || addOwner.current.generation !== addGeneration || addOwner.current.fallback !== fallbackDownloads)) addOwner.current.active = false;
  useLayoutEffect(() => {
    if (panel !== 'add') return;
    const opening: AddOpening = {active: true, generation: addGeneration, context, fallback: fallbackDownloads}; addOwner.current = opening;
    if (context && !Platform.isTV) {try {opening.downloads = createAccountDownloadsEntry(context,
      () => opening.active && addOwner.current === opening && panelRef.current === 'add'); opening.disposeDownloads = opening.downloads.dispose;} catch {opening.downloads = undefined;}}
    if (!context && !Platform.isTV) opening.downloads = fallbackDownloads;
    setCommittedAdd(opening);
    return () => {opening.active = false; if (addOwner.current === opening) addOwner.current = undefined; opening.disposeDownloads?.();};
  }, [panel, context, addGeneration, fallbackDownloads]);
  const addCurrent = () => !!committedAdd?.active && addOwner.current === committedAdd && panelRef.current === 'add';
  const retireAdd = () => {const opening = addOwner.current; addOwner.current = undefined; if (opening) {opening.active = false; opening.disposeDownloads?.();}};
  const openAdd = () => {if (panelRef.current !== 'servers') return; retireAdd(); setAddGeneration(value => value + 1); setPanel('add');};
  const approver = useRef<TVConsentApprover | undefined>(undefined), alive = useRef(true);
  useEffect(() => {alive.current = true; let read = 0;
    const update = () => {const owned = ++read; void nativeAccounts.snapshot().then(value => {if (alive.current && owned === read) setServers(value);}, failure => {if (alive.current) setError(failure);});};
    update(); const off = subscribeNativeAppSessions(update);
    return () => {alive.current = false; off(); approver.current?.dispose();};
  }, []);
  const run = async (work: () => Promise<void>) => {if (busy || context && !context.current()) return; setBusy(true); setError(undefined);
    try {await work();} catch (failure) {if (alive.current) setError(failure);} finally {if (alive.current) setBusy(false);}
  };
  // A route opened on Use a local server or Sign out of Portico Account from Settings goes back there, not to a second chooser.
  const leaveOnClose = initialPanel === 'add' || initialPanel === 'sign-out-account'
    || Platform.isTV && initialPanel === 'consent-kind';
  const close = () => {
    retireAdd(); approver.current?.dispose(); approver.current = undefined;
    setPreview(undefined); setCode(''); setError(undefined);
    if (leaveOnClose) onBack?.(); else setPanel('servers');};
  const cancel = () => {if (panel === 'servers') onBack?.(); else close();};
  useEffect(() => {if (panel === 'add' || embedded && panel === 'servers') return; const subscription = registerBack(() => {
    if (busy) return true;
    if (panel === 'servers' && !onBack) return false;
    cancel(); return true;
  }, {scope: 'surface'}); return () => subscription();});
  const action = (id: string, label: string, work: () => Promise<void>, disabled = false) => ({id, label, disabled: busy || disabled, loading: busy, onPress: () => {void run(work);}});
  const errorView = error ? <FormError placement="block">{presentError(error, 'account').body}</FormError> : null;
  const AccountLayout = theme.isTV ? TVAuthLayout : StateLayout;
  const layout = (title: string, body?: string, before?: React.ReactNode, primary?: ReturnType<typeof action>, more?: React.ReactNode) => <AccountLayout testID="accounts-servers" title={title} body={body}
    beforeActions={<>{before}{errorView}</>} primaryAction={primary} secondaryAction={panel !== 'servers' || onBack ? {id: 'back', label: t('action.back'), disabled: busy, onPress: cancel} : undefined}>{more}</AccountLayout>;
  if (panel === 'add') return <AccountEntry key={addGeneration} downloads={addCurrent() ? committedAdd?.downloads : undefined} onSetupComplete={onSetupComplete} initialStage="address" onBack={() => {if (addCurrent()) close();}} onComplete={() => {
    // Successful publication can retire the borrowed Downloads entry before this callback.
    if (!alive.current || panelRef.current !== 'add' || !addOwner.current || addIntent.current.generation !== addGeneration || addIntent.current.context !== context) return;
    close(); onComplete?.();
  }}/>;
  if (panel === 'sign-out-account') return layout(t('accounts.signOut.portico'), undefined, undefined,
    action('sign-out-account', t('action.signOut'), async () => {
      if (!accountScope) throw Object.assign(new Error('native_account_signed_out'), {code: 'account_changed'});
      await nativeAccounts.signOutPorticoAccount(accountScope); close(); onComplete?.();
    }));
  if (panel === 'consent-kind') return layout(t('settings.linkTV'), t('linkTV.enterCode'), <View style={{gap: 12, alignSelf: 'stretch', marginBottom: 20}}>
    {context && servers?.servers.find(value => value.id === servers.selectedServerId)?.signIn === 'local' && <Button variant="glass" disabled={busy} onPress={() => {void run(async () => {
      approver.current = await nativeAccounts.localTVConsent(context.api, context.session); setConsentKind('local'); setPanel('consent-code');
    });}}>{t('accounts.session.local')}</Button>}
    <Button variant="glass" disabled={busy} onPress={() => {void run(async () => {approver.current = await nativeAccounts.porticoTVConsent(); setConsentKind('portico'); setPanel('consent-code');});}}>{t('account.portico')}</Button>
  </View>);
  if (panel === 'consent-code') {
    const codeField = <TextField label={t('linkTV.enterCode')} value={code} onChangeText={value => setCode(formatTVLinkCode(value))}
      autoCapitalize="characters" maxLength={9} autoCorrect={false} editable={!busy}/>;
    return layout(t('settings.linkTV'), t(consentKind === 'portico' ? 'accounts.consent.portico' : 'accounts.consent.local'),
      theme.isTV ? <TVAccountFields>{codeField}</TVAccountFields> : <View style={{marginBottom: 20}}>{codeField}</View>,
      action('review-tv', t('web.device.review'), async () => {setPreview(await approver.current!.review(code)); setPanel('consent-review');}, !completeTVLinkCode(code)));
  }
  if (panel === 'consent-review') return layout(t('linkTV.askConfirm'), t('web.device.approveWarn'),
    <View style={{marginBottom: 20}}>{[preview!.deviceName, preview!.platform, preview!.appVersion, preview!.userCode, t(consentKind === 'portico' ? 'accounts.session.portico' : 'accounts.session.local')].map((value, index) => <Text key={index} style={[theme.type.body, {color: theme.colors.text}]}>{value}</Text>)}</View>,
    action('consent-approve', t('web.device.signItIn'), async () => {await approver.current!.decide(preview!, 'approve'); setDone(t('linkTV.approved', {device: preview!.deviceName})); setPanel('consent-done');}),
    <Button variant="text" disabled={busy} onPress={() => {void run(async () => {await approver.current!.decide(preview!, 'deny'); setDone(t('linkTV.declined')); setPanel('consent-done');});}}>{t('web.device.notMine')}</Button>);
  if (panel === 'consent-done') return layout(done, undefined, undefined, action('done', t('action.done'), async () => close()));
  const deviceNoun = Platform.isTV ? Platform.OS === 'ios' ? 'device.noun.appleTV' : String((Platform.constants as {Manufacturer?: string}).Manufacturer ?? '').toLowerCase() === 'amazon' ? 'device.noun.fireTV' : 'device.noun.androidTV'
    : Platform.OS === 'ios' ? (Platform as {isPad?: boolean}).isPad ? 'device.noun.iPad' : 'device.noun.iPhone' : Math.min(Dimensions.get('screen').width, Dimensions.get('screen').height) >= 600 ? 'device.noun.tablet' : 'device.noun.phone';
  const selectedServer = !!captured && servers?.selectedServerId === captured.session.connection.pin.serverId;
  // Sign out and Forget share one confirmation: the same downloads choice, a different title, line and action.
  const forgetting = panel === 'forget-server';
  const signOutSheet = captured ? <BottomSheet open={panel === 'sign-out-server' || forgetting} onOpenChange={open => {if (!open) {setRemoveDownloads(false); close();}}} testID="accounts-sign-out-sheet"
    title={forgetting ? t('connection.identity.forgetTitle', {server: captured.name}) : t('settings.signOutConfirm.title', {server: captured.name, device: t(deviceNoun)})}
    footer={<View style={{gap: 10}}>
      <Focusable focusId="sign-out-confirm" accessibilityRole="button" accessibilityLabel={forgetting ? t('connection.identity.forgetAction') : t('action.signOut')} disabled={busy}
        onPress={() => {void run(async () => {
          if (removeDownloads && selectedServer) await removeCapturedServerDownloads(captured, {
            selected: () => nativeAppSessions.selected(),
            entries: () => offlineDownloads.getSnapshot().entries,
            remove: async entry => {
              const owner = offlineOwnerKey(captured.session.connection.session.viewer);
              const live = offlineDownloads.getSnapshot().entries.find(value => value.id === entry.id
                && (value.receipt ? offlineOwnerKey(value.receipt.claims.viewer) : value.owner) === owner);
              if (live) await offlineDownloads.remove(live);
            },
          });
          const pin = captured.session.connection.pin, familyId = captured.session.connection.session.sessionFamilyId;
          // Forget is local and never contacts the server, so it works for a sign-in that ended or a server that's gone.
          if (forgetting) await nativeAccounts.forgetServer({serverId: pin.serverId, familyId, fingerprint: pin.fingerprint});
          else await nativeAccounts.signOutServer(pin.serverId, familyId);
          setRemoveDownloads(false); close(); onComplete?.();
        });}} focusRadius={12} focusTreatment="none"
        style={{height: 54, borderRadius: 12, backgroundColor: `${theme.colors.danger}24`, alignItems: 'center', justifyContent: 'center'}}>
        <Text style={[theme.type.button, {color: theme.colors.danger}]}>{forgetting ? t('connection.identity.forgetAction') : t('action.signOut')}</Text>
      </Focusable>
      <Button focusId="sign-out-cancel" variant="text" disabled={busy} onPress={() => {setRemoveDownloads(false); close();}} textStyle={{color: theme.colors.textSecondary}}>{t('action.cancel')}</Button>
    </View>}>
    {forgetting && <Text style={[theme.type.body, {color: theme.colors.textSecondary, marginBottom: 6}]}>{t('accounts.servers.forgetBody', {server: captured.name})}</Text>}
    <Text style={[theme.type.body, {color: theme.colors.textSecondary, marginBottom: 14}]}>{t('settings.signOutConfirm.bodyDownloads')}</Text>
    {selectedServer && <Focusable focusId="sign-out-remove-downloads" accessibilityRole="checkbox" accessibilityState={{checked: removeDownloads}} accessibilityLabel={t('settings.signOutConfirm.removeDownloads')}
      onPress={() => setRemoveDownloads(value => !value)} focusRadius={6} focusTreatment="none" style={{flexDirection: 'row', alignItems: 'center', gap: 12, paddingVertical: 8}}>
      <View style={{width: 24, height: 24, borderRadius: 6, borderWidth: 1.5, alignItems: 'center', justifyContent: 'center',
        borderColor: removeDownloads ? theme.colors.accentStrong : theme.colors.lineStrong, backgroundColor: removeDownloads ? theme.colors.accentStrong : 'transparent'}}>
        {removeDownloads && <Icon name="i-check" size={16} color={theme.colors.ink}/>}
      </View>
      <Text style={[theme.type.body, {color: theme.colors.text, flex: 1}]}>{t('settings.signOutConfirm.removeDownloads')}</Text>
    </Focusable>}
  </BottomSheet> : null;
  if (!Platform.isTV) return <><ServerChooserPage certificates={<TrustedCertificatesPage context={context}/>} snapshot={servers} busy={busy} error={errorView} onBack={onBack ? cancel : undefined}
    onSelect={value => {void run(async () => {await nativeAccounts.switchServer(value.id); onComplete?.();});}}
    onSignOut={value => {void run(async () => {
      const state = await nativeAppSessions.read(), found = state.servers.find(item => item.connection.pin.serverId === value.id);
      if (!found || found.connection.session.sessionFamilyId !== value.familyId || found.connection.pin.fingerprint !== value.fingerprint)
        throw Object.assign(new Error('native_server_changed'), {code: 'cancelled'});
      setCaptured({session: found, name: value.name}); setPanel('sign-out-server');
    });}}
    onForget={value => {void run(async () => {
      const state = await nativeAppSessions.read(), found = state.servers.find(item => item.connection.pin.serverId === value.id);
      if (!found || found.connection.session.sessionFamilyId !== value.familyId || found.connection.pin.fingerprint !== value.fingerprint)
        throw Object.assign(new Error('native_server_changed'), {code: 'cancelled'});
      setCaptured({session: found, name: value.name}); setPanel('forget-server');
    });}}
    onRefresh={() => {void run(async () => {const value = await nativeAccounts.snapshot(); if (alive.current) setServers(value);});}}
    onAdd={() => {void run(async () => {openAdd();});}}
    footer={<View style={{alignSelf: 'stretch', gap: 8, marginTop: 10}}>
      <Button variant="text" disabled={busy} onPress={() => setPanel('consent-kind')}>{t('settings.linkTV')}</Button>
      {accountHeld && <Button variant="text" disabled={busy} onPress={() => {void run(async () => {
        const credentials = nativeAccounts.accountCredentials(); await credentials.synchronize(); const session = credentials.getSnapshot().session;
        if (!session) throw Object.assign(new Error('native_account_signed_out'), {code: 'account_changed'});
        setAccountScope({accountId: session.account.id, familyId: session.familyId}); setPanel('sign-out-account');
      });}}>{t('accounts.signOut.portico')}</Button>}
    </View>}/>{signOutSheet}</>;
  // On a TV's Switch server Settings page (embedded), the panels that fill the screen open this route on their own panel.
  // They open outside run(): a busy turn ending would ask for the chooser's start while the opened route is on top.
  const opens = embedded ? openPanel : undefined;
  const signOutAccount = async (open: (session: {accountId: string; familyId: string}) => void) => {
    const credentials = nativeAccounts.accountCredentials();
    await credentials.synchronize();
    const session = credentials.getSnapshot().session;
    if (!session) throw Object.assign(new Error('native_account_signed_out'), {code: 'account_changed'});
    open({accountId: session.account.id, familyId: session.familyId});
  };
  const openSignOutAccount = (scope: {accountId: string; familyId: string}) => {setAccountScope(scope); setPanel('sign-out-account');};
  // Embedded: the route opened on Sign out of Portico Account reads the scope itself once this has synchronized.
  const signOutFromSettings = (open: (panel: NativeServersPanel) => void) => {
    if (openingSignOut.current) return;
    openingSignOut.current = true;
    setError(undefined);
    signOutAccount(() => {if (alive.current && (!context || context.current())) open('sign-out-account');})
      .catch(failure => {if (alive.current) setError(failure);})
      .finally(() => {openingSignOut.current = false;});
  };
  const owner = !!context && context.current() && context.account.serverId === servers?.selectedServerId
    && context.account.account.role === 'owner';
  const tvProps: React.ComponentProps<typeof TVServerChooserPage> = {snapshot: servers, busy, error: errorView,
    onBack: onBack ? cancel : undefined, owner,
    onSelect: value => {void run(async () => {await nativeAccounts.switchServer(value.id); onComplete?.();});},
    onSignOut: value => {void run(async () => {
      const state = await nativeAppSessions.read(), found = state.servers.find(item => item.connection.pin.serverId === value.id);
      if (!found || found.connection.session.sessionFamilyId !== value.familyId || found.connection.pin.fingerprint !== value.fingerprint)
        throw Object.assign(new Error('native_server_changed'), {code: 'cancelled'});
      setCaptured({session: found, name: value.name}); setPanel('sign-out-server');
    });},
    onForget: value => {void run(async () => {
      const state = await nativeAppSessions.read(), found = state.servers.find(item => item.connection.pin.serverId === value.id);
      if (!found || found.connection.session.sessionFamilyId !== value.familyId || found.connection.pin.fingerprint !== value.fingerprint)
        throw Object.assign(new Error('native_server_changed'), {code: 'cancelled'});
      setCaptured({session: found, name: value.name}); setPanel('forget-server');
    });},
    onRefresh: () => {void run(async () => {const value = await nativeAccounts.snapshot(); if (alive.current) setServers(value);});},
    onAdd: opens ? () => opens('add') : () => {void run(async () => {openAdd();});},
    certificates: (bind, refreshServers) => <TrustedCertificatesPage context={context} refreshRef={bind('certificates-refresh')}
      refreshDown={refreshServers}/>,
    footer: bind => <View style={{alignSelf: 'stretch', gap: 8, marginTop: 10}}>
      <Button ref={bind('link-tv')} focusId="link-tv" variant="link" disabled={busy}
        onPress={opens ? () => opens('consent-kind') : () => setPanel('consent-kind')}>
        {t('settings.linkTV')}
      </Button>
      {accountHeld && <Button focusId="sign-out-portico" variant="link" disabled={busy}
        onPress={opens ? () => signOutFromSettings(opens) : () => {void run(() => signOutAccount(openSignOutAccount));}}>
        {t('accounts.signOut.portico')}
      </Button>}
    </View>};
  return <>{embedded ? <TVServerSettingsPanel {...tvProps}/> : <TVServerChooserPage {...tvProps}/>}{signOutSheet}</>;
}

const createAccountRoute: NativeRouteFactory = context => ({render: route => {
  if (route.kind !== 'profiles' && route.kind !== 'servers') throw new Error('native_account_route_mismatch');
  return <AccountRoute key={context.navigation.getSnapshot().revision} route={route} context={context}/>;
}});
nativeShellRoutes.registerRoute('profiles', createAccountRoute);
nativeShellRoutes.registerRoute('servers', createAccountRoute);
