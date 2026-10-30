import React, {useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState} from 'react';
import {BackHandler, Dimensions, Platform, Text, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {presentError} from '../../../../../packages/client-core/src/presentation/errors';
import {ManagedProfilesClient, parseDirectSnapshot, type ManagedProfile, type ProfileArt, type ProfileDeletion} from '../../../../../packages/client-core/src/profile-management';
import {routePath} from '../../../../../packages/contracts/src/routes';
import {IdentityClient} from '../../../../../packages/client-core/src/identity-client';
import type {ProfileRestrictions, RatingSystem} from '../../../../../packages/client-core/src/identity-restrictions';
import {HttpHostedApi} from '../../../../../packages/client-core/src';
import type {SetupPreview} from '../../../../../packages/client-core/src/setup-code';
import type {TVConsentApprover} from '../../../../../packages/client-core/src/tv-consent';
import type {AppServerSession} from '../../../../../packages/client-core/src/app-sessions';
import {routeOrigin} from '../../../../../packages/client-core/src/route-identity';
import {nativeAccounts} from '../../platform/accounts';
import {nativeAppSessions, subscribeNativeAppSessions} from '../../platform/app-session-storage';
import {nativeProfileSelection} from '../../platform/profile-selection';
import {NativeProfileRecovery, temporaryRecoveryApi} from '../../platform/profile-recovery';
import {hostedUrl} from '../../platform/account-authority';
import {pickProfilePicture, profileAvatarUpload, profilePicturePickerAvailable, profilePictureSource} from '../../platform/profile-avatars';
import type {ProfileAvatar} from '../../../../../packages/client-core/src/identity-restrictions';
import {nativeNetworkFetch} from '../../platform/server-connections';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import type {NativeShellRoute} from '../../shell/routes';
import {NativeScreenLifetime} from '../settings-live/lifetime';
import {ProfileColorPicker, ProfileRestrictionsFields} from '../settings-live/ProfileEditorFields';
import {sameAuthority, type ShellAuthority} from '../../shell/model';
import {nativeShellRoutes, type NativeRouteFactory} from '../../shell/route-registry';
import {StateLayout, StateView} from '../../kit/states';
import {ProfileFace, ProfilePicker} from '../../kit/artwork';
import {Button, Focusable, TextField} from '../../kit/controls';
import {BottomSheet, Dialog} from '../../kit/overlays';
import {Icon} from '../../kit/Icon';
import {offlineDownloads, offlineOwnerKey} from '../../../platform/offline-downloads';
import {useTheme} from '../../kit/theme';
import {AccountEntry} from './AccountEntry';
import {createAccountDownloadsEntry, type AccountDownloadsEntry} from './downloads-entry';
import {PhonePINEntry} from './PhonePINEntry';
import {completeTVLinkCode, formatTVLinkCode} from './account-form-values';
import {PhoneProfileRecovery} from './PhoneProfileRecovery';
import {PhoneServerChooser} from './PhoneServerChooser';
import {TVTrustedCertificates} from './TVTrustedCertificates';
import {TVServerChooser} from './TVServerChooser';
import {removeCapturedServerDownloads} from './sign-out-downloads';
import {TVProfilePINForm, TVForgotPINHandoff, TVWhosWatching} from '../../tv/auth';

const t = defaultI18n.t;
type Panel = 'servers' | 'add' | 'sign-out-server' | 'sign-out-account' | 'consent-kind' | 'consent-code' | 'consent-review' | 'consent-done';

/** Genuine account destinations for the current authority. Shell media routes and Home
 * remain with their existing owners. Runtime disposal retires every private view below. */
export function AccountRoute({route, context}: {route: NativeShellRoute; context: NativeShellBootstrapContext}) {
  const back = () => {context.navigation.back(context.authority());};
  return route.kind === 'profiles' ? <ProfileRoute context={context} route={route} onBack={back}/> : <ServersRoute context={context} initialPanel={route.kind === 'servers' ? route.initialPanel : undefined} onBack={back}/>;
}

type ProfileLease = Readonly<{
  route: NativeShellRoute;
  revision: number;
  authority: ShellAuthority;
  sessionFamilyId: string;
  accountId: string;
  profileId: string;
  lifetime: NativeScreenLifetime;
  live: {value: boolean};
  current: () => boolean;
}>;
type ProfileManagementOpening = Readonly<{id: string}>;
type ProfileEditorOpening = Readonly<{
  kind: 'rename' | 'pin' | 'remove-pin' | 'forget' | 'limits';
  profile: ManagedProfile;
  lease: ProfileLease;
  management?: ProfileManagementOpening;
}>;
type ProfileAddOpening = Readonly<{lease: ProfileLease}>;
type ProfileDeleteOpening = Readonly<{preview: ProfileDeletion; management: ProfileManagementOpening}>;

const profileAbort = () => Object.assign(new Error('profile_command_retired'), {name: 'AbortError'});

export function ProfileRoute({context, route, onBack, embedded = false}: {context: NativeShellBootstrapContext; route: NativeShellRoute; onBack: () => void; embedded?: boolean}) {
  const theme = useTheme(), [profile, setProfile] = useState<ManagedProfile>(), [pin, setPin] = useState(''), [busy, setBusy] = useState(false), [error, setError] = useState<unknown>();
  const [forgotPIN, setForgotPIN] = useState(false), [recovery, setRecovery] = useState<{controller: NativeProfileRecovery; id: string; name: string}>();
  const [profiles, setProfiles] = useState<readonly ManagedProfile[]>(context.account.profiles);
  const [management, setManagement] = useState<ProfileManagementOpening>(), [deletion, setDeletion] = useState<ProfileDeleteOpening>(), [deleteName, setDeleteName] = useState('');
  const [retirement, setRetirement] = useState<'removing' | 'removed'>();
  const managementRef = useRef<typeof management>(undefined), deletionRef = useRef<typeof deletion>(undefined), deleteNameRef = useRef('');
  const managedId = management?.id;
  const setManagedId = (id?: string) => {const next = id ? {id} : undefined; managementRef.current = next; setManagement(next);};
  const managementCurrent = () => !embedded || !!management && managementRef.current === management;
  const closeDeletion = () => {deletionRef.current = undefined; deleteNameRef.current = ''; setDeletion(undefined); setDeleteName(''); setRetirement(undefined);};
  const profileFocus = useRef(new WeakMap<ProfileLease, string>()).current;
  const [editor, setEditor] = useState<ProfileEditorOpening>(), editorRef = useRef<ProfileEditorOpening | undefined>(undefined);
  const edit = editor?.kind === 'rename' || editor?.kind === 'pin' ? editor : undefined;
  const removePin = editor?.kind === 'remove-pin' ? editor.profile : undefined, forgetProfile = editor?.kind === 'forget' ? editor.profile : undefined;
  const limitsProfile = editor?.kind === 'limits' ? editor.profile : undefined;
  const [nameDraft, setNameDraft] = useState(''), [pinDraft, setPinDraft] = useState(''), [pinAgain, setPinAgain] = useState('');
  const editDraftRef = useRef({name: '', pin: '', again: ''});
  const [addition, setAddition] = useState<ProfileAddOpening>(), additionRef = useRef<ProfileAddOpening | undefined>(undefined);
  const [addName, setAddName] = useState(''), addNameRef = useRef('');
  const [addArt, setAddArt] = useState<ProfileArt>('blue'), [artDraft, setArtDraft] = useState<ProfileArt>('blue');
  const [actionBusy, setActionBusy] = useState(false), [problem, setProblem] = useState(''), [notice, setNotice] = useState('');
  const [lease, setLease] = useState<ProfileLease>();
  const activeTicket = useRef<AbortController | undefined>(undefined);
  const retireEditor = () => {editorRef.current = undefined; setEditor(undefined); editDraftRef.current = {name: '', pin: '', again: ''}; setNameDraft(''); setPinDraft(''); setPinAgain('');};
  const retireAddition = () => {additionRef.current = undefined; setAddition(undefined); addNameRef.current = ''; setAddName('');};
  const selection = useRef<ReturnType<typeof nativeProfileSelection> | undefined>(undefined), alive = useRef(true), starting = useRef(false);
  useLayoutEffect(() => {
    const session = context.session(), lifetime = new NativeScreenLifetime(context), live = {value: true};
    const authority = Object.freeze({...context.authority()}), snapshot = context.navigation.getSnapshot();
    const sessionFamilyId = session?.sessionFamilyId ?? '', accountId = context.account.account.id, profileId = session?.viewer.profileId ?? '';
    const next: ProfileLease = Object.freeze({
      route, revision: snapshot.revision, authority, sessionFamilyId, accountId, profileId, lifetime, live,
      current: () => live.value && lifetime.current() && !!sessionFamilyId
        && context.session()?.sessionFamilyId === sessionFamilyId
        && context.session()?.viewer.accountId === accountId
        && context.session()?.viewer.profileId === profileId
        && context.session()?.viewer.serverId === authority.serverId
        && context.navigation.getSnapshot().revision === snapshot.revision
        && context.navigation.getSnapshot().route === snapshot.route,
    });
    setLease(next);
    const retire = () => {live.value = false; lifetime.dispose(); activeTicket.current?.abort();};
    const stopNav = context.navigation.subscribe(() => {
      const now = context.navigation.getSnapshot();
      if (now.revision !== snapshot.revision || now.route !== snapshot.route) retire();
    });
    const stopAuth = context.subscribeAuthority(() => {if (!sameAuthority(authority, context.authority())) retire();});
    return () => {
      stopNav(); stopAuth(); retire(); setLease(current => current === next ? undefined : current);
      retireEditor(); retireAddition(); setManagedId(undefined); closeDeletion();
    };
  }, [context, route]);
  const scopedTransport = useCallback((ticket: {lease: ProfileLease; controller: AbortController}) => ({
    request: async <T,>(path: string, method?: string, body?: unknown, _signal?: AbortSignal): Promise<T> => {
      if (!ticket.lease.current() || ticket.controller.signal.aborted) throw profileAbort();
      const raw = await ticket.lease.lifetime.request(next => context.api.request<T>(path, method, body, next), ticket.controller.signal);
      if (!ticket.lease.current() || ticket.controller.signal.aborted) throw profileAbort();
      return raw;
    },
  }), [context]);
  const scopedManager = (ticket: {lease: ProfileLease; controller: AbortController}) => new ManagedProfilesClient(scopedTransport(ticket), {authority: 'local', accountId: context.account.account.id, serverId: context.account.serverId});
  const scopedRaw = useCallback((ticket: {lease: ProfileLease; controller: AbortController}) => ({
    requestRaw: async (path: string, method: string, options: Readonly<{headers?: Readonly<Record<string, string>>; body?: unknown; signal?: AbortSignal}> = {}) => {
      if (!ticket.lease.current() || ticket.controller.signal.aborted) throw profileAbort();
      const raw = await ticket.lease.lifetime.request(next => context.api.requestRaw(path, method, {...options, signal: next}), ticket.controller.signal);
      if (!ticket.lease.current() || ticket.controller.signal.aborted) throw profileAbort();
      return raw;
    },
  }), [context]);
  const scopedIdentity = useCallback((ticket: {lease: ProfileLease; controller: AbortController}) => new IdentityClient(scopedTransport(ticket), profileAvatarUpload(scopedRaw(ticket))), [scopedRaw, scopedTransport]);
  const limitsLease = useMemo(() => editor?.kind === 'limits' ? {...editor.lease, current: () => editorRef.current === editor && editor.lease.current()
    && (!embedded || !!editor.management && managementRef.current === editor.management)} : undefined, [editor, embedded]);
  const identityForLimits = useCallback((controller: AbortController) => new IdentityClient(scopedTransport({lease: limitsLease!, controller}), profileAvatarUpload(scopedRaw({lease: limitsLease!, controller}))), [limitsLease, scopedRaw, scopedTransport]);
  const [avatars, setAvatars] = useState<readonly ProfileAvatar[]>([]);
  const avatarsLease = useRef<ProfileLease | undefined>(undefined);
  useEffect(() => {
    if (!lease || !lease.current()) {avatarsLease.current = undefined; setAvatars([]); return;}
    const controller = new AbortController(), identity = scopedIdentity({lease, controller});
    avatarsLease.current = lease;
    void identity.avatars(controller.signal).then(found => {if (lease.current() && !controller.signal.aborted) setAvatars(found);}, () => {});
    return () => {controller.abort(); if (avatarsLease.current === lease) {avatarsLease.current = undefined; setAvatars([]);}};
  }, [lease, scopedIdentity]);
  useEffect(() => {alive.current = true; const session = context.session();
    if (session) selection.current = nativeProfileSelection(session.viewer.serverId, session.sessionFamilyId);
    return () => {alive.current = false; selection.current?.dispose();};
  }, [context]);
  // TV (F-PROFILE-28): a wrong PIN keeps the filled boxes in the error colour for 400 ms, then empties them.
  const wrongPinTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const cancelWrongPin = () => {if (wrongPinTimer.current) {clearTimeout(wrongPinTimer.current); wrongPinTimer.current = undefined;}};
  useEffect(() => cancelWrongPin, []);
  // R-16: a stale context or a missing controller says "this changed" (with Refresh), never nothing.
  const staleProfiles = () => Object.assign(new Error('native_profiles_changed'), {code: 'stale'});
  const choose = async (id: string, code?: string) => {if (busy) return;
    if (!context.current() || !selection.current) {setError(staleProfiles()); return;}
    setBusy(true); setError(undefined);
    try {await selection.current.select(id, code);} catch (failure) {if (alive.current) {
      if (theme.isTV && code !== undefined) {cancelWrongPin(); wrongPinTimer.current = setTimeout(() => {wrongPinTimer.current = undefined; if (alive.current) setPin('');}, 400);} else setPin('');
      setError(failure);
    }} finally {if (alive.current) setBusy(false);}
  };
  // E-mobile-25: "This changed…" always comes with a way to read the list again.
  const refreshProfiles = async () => {
    if (busy || !context.current()) return; setBusy(true);
    try {
      const raw = await context.api.request(routePath('get_direct_account'), 'GET');
      const account = parseDirectSnapshot(raw, {authority: 'local', serverId: context.account.serverId, accountId: context.account.account.id}, true);
      if (alive.current) {setProfiles(account.profiles); setError(undefined);}
    } catch (failure) {if (alive.current) setError(failure);} finally {if (alive.current) setBusy(false);}
  };
  const errorRefresh = error && presentError(error, 'profiles').action === 'refresh'
    ? <Button variant="text" disabled={busy} onPress={() => {void refreshProfiles();}}>{t('action.refresh')}</Button> : null;
  const current = context.session();
  const active = embedded ? profiles.find(value => value.id === managedId) : current ? profiles.find(value => value.id === current.viewer.profileId) : undefined;
  const manage = context.account.canManage && context.account.capabilities.manageAccount && !context.account.account.disabled
    && current?.viewer.profileId === context.account.account.primaryProfileId;
  const editorCurrent = (held: ProfileEditorOpening | undefined = editor): held is ProfileEditorOpening => !!held && editorRef.current === held && manage && held.lease.current()
    && (!embedded || !!held.management && managementRef.current === held.management);
  const additionCurrent = (held: ProfileAddOpening | undefined = addition): held is ProfileAddOpening => !!held && additionRef.current === held && manage && held.lease.current();
  const deletionCurrent = (held: ProfileDeleteOpening | undefined = deletion): held is ProfileDeleteOpening => !!held && deletionRef.current === held && manage && !!lease?.current() && managementRef.current === held.management;
  const profileActionsCurrent = () => manage && !!lease?.current() && managementCurrent() && !activeTicket.current;
  const openEditor = (kind: ProfileEditorOpening['kind']) => {
    if (activeTicket.current) return;
    if (!active || !lease || !profileActionsCurrent()) {setProblem(presentError(staleProfiles(), 'profiles').body); return;}
    const next = {kind, profile: active, lease, management};
    retireEditor(); editorRef.current = next; setEditor(next);
    editDraftRef.current = {name: active.name, pin: '', again: ''}; setNameDraft(active.name); setArtDraft(active.art); setProblem(''); setNotice('');
  };
  const openAddition = () => {
    // The Add controls are not rendered at the 8-profile limit, so that case never reaches here.
    if (activeTicket.current || profiles.length >= 8) return;
    if (!manage || !lease?.current()) {setProblem(presentError(staleProfiles(), 'profiles').body); return;}
    const next = {lease}; retireAddition(); additionRef.current = next; setAddition(next); setAddArt('blue'); setProblem(''); setNotice('');
  };
  const closeAddition = () => {if (additionCurrent()) {activeTicket.current?.abort(); retireAddition();}};
  const closeEdit = () => {if (editorCurrent()) {activeTicket.current?.abort(); retireEditor();}};
  const runAction = async (work: (clients: {manager: ManagedProfilesClient; identity: IdentityClient}) => Promise<unknown>, message?: string, admitted?: () => boolean) => {
    const captured = lease;
    if (!manage || !captured || !captured.current() || activeTicket.current || (admitted ? !admitted() : embedded && !managementCurrent())) return false;
    const controller = new AbortController(), ticket = {lease: captured, controller};
    activeTicket.current = controller;
    const manager = scopedManager(ticket), identity = scopedIdentity(ticket);
    setActionBusy(true); setProblem(''); setNotice('');
    try {
      await work({manager, identity});
      if (!captured.current() || controller.signal.aborted) return false;
      const next = await manager.list(controller.signal);
      if (!captured.current() || controller.signal.aborted) return false;
      setProfiles(next);
      if (message) setNotice(message);
      return true;
    } catch (failure) {
      if (controller.signal.aborted || !captured.current() || !alive.current) return false;
      setProblem(presentError(failure, 'profiles').body); return false;
    } finally {
      if (activeTicket.current === controller) activeTicket.current = undefined;
      if (alive.current) setActionBusy(false);
    }
  };
  useEffect(() => () => {recovery?.controller.dispose();}, [recovery]);
  const startRecovery = async () => {
    const session = context.session(), target = profile;
    if (busy || starting.current || !session || !target || !context.current()) return;
    starting.current = true;
    try {
      const selected = await nativeAppSessions.selected();
      if (!selected || !context.current()) return;
      if (selected.connection.pin.serverId !== session.viewer.serverId
        || selected.connection.session.sessionFamilyId !== session.sessionFamilyId) return;
      if (!context.account.profiles.some(value => value.id === target.id)) return;
      const serverPin = selected.connection.pin;
      const authorityCurrent = () => {
        const now = context.session(), live = context.api.getRouteConnection()?.pin;
        return context.current() && !!now
          && now.sessionFamilyId === session.sessionFamilyId
          && now.viewer.serverId === session.viewer.serverId
          && now.viewer.accountId === session.viewer.accountId
          && now.viewer.profileId === session.viewer.profileId
          && now.viewer.authority === session.viewer.authority
          && !!live && live.serverId === serverPin.serverId && live.publicKey === serverPin.publicKey && live.fingerprint === serverPin.fingerprint;
      };
      setRecovery({id: target.id, name: target.name, controller: new NativeProfileRecovery({api: context.api, snapshot: context.account, profileId: target.id, selectedFamilyId: session.sessionFamilyId, kind: selected.signIn, authorityCurrent,
        temporary: grant => temporaryRecoveryApi(context.api, grant),
        portico: async () => {
          const credentials = nativeAccounts.accountCredentials(); await credentials.synchronize();
          const account = await credentials.accessSession();
          if (!account) throw new Error('Sign in to your Portico Account first.');
          return {hosted: new HttpHostedApi(hostedUrl, account.accessToken, nativeNetworkFetch), origin: hostedUrl,
            accountCurrent: () => {const live = credentials.getSnapshot().session; return !!live && live.account.id === account.account.id && live.familyId === account.familyId;}};
        }})});
    } catch (failure) {if (alive.current) setError(failure);}
    finally {starting.current = false;}
  };
  const back = () => {cancelWrongPin(); activeTicket.current?.abort(); setPinDraft(''); setPinAgain(''); if (recovery) {recovery.controller.dispose(); setRecovery(undefined); setPin('');} else if (forgotPIN) {setForgotPIN(false); setPin(''); setError(undefined);} else if (profile) {setProfile(undefined); setPin('');} else onBack();};
  const avatarSource = (profileId: string) => {
    if (!lease || !lease.current() || avatarsLease.current !== lease) return undefined;
    const avatar = avatars.find(value => value.profileId === profileId);
    const picture = profilePictureSource(context.api, current?.accessToken, avatar, 90);
    return picture && typeof picture === 'object' && 'uri' in picture && typeof picture.uri === 'string'
      ? {uri: picture.uri, cacheKey: `avatar:${profileId}:${avatar?.version ?? 0}`, ...(picture.headers ? {headers: picture.headers as Readonly<Record<string, string>>} : {})}
      : undefined;
  };
  const submitRename = () => {
    const target = edit?.kind === 'rename' ? edit.profile : undefined;
    const admitted = () => editorCurrent(edit) && editDraftRef.current.name === nameDraft;
    if (!target || !nameDraft.trim() || !admitted()) return;
    void runAction(({manager}) => manager.edit(target, {name: nameDraft.trim(), art: artDraft}), undefined, admitted).then(ok => {if (ok) closeEdit();});
  };
  const submitPin = () => {
    const target = edit?.kind === 'pin' ? edit.profile : undefined;
    const admitted = () => editorCurrent(edit) && editDraftRef.current.pin === pinDraft && editDraftRef.current.again === pinAgain;
    if (!target || !admitted()) return;
    if (!/^\d{4}$/.test(pinDraft) || pinDraft !== pinAgain) {setProblem(t('profile.pinMismatch')); return;}
    void runAction(({manager}) => manager.edit(target, {pin: pinDraft}), t('profile.pinSaved', {name: target.name}), admitted).then(ok => {if (ok) closeEdit();});
  };
  const removePinAction = () => {
    const target = removePin;
    if (!target || !editorCurrent()) return;
    void runAction(({manager}) => manager.edit(target, {pin: ''}), t('profile.pinRemoved', {name: target.name}), () => editorCurrent()).then(ok => {if (ok) closeEdit();});
  };
  const submitAdd = () => {
    const name = addName.trim();
    const admitted = () => additionCurrent() && addNameRef.current === addName;
    if (!name || !admitted()) return;
    void runAction(({manager}) => manager.create(name, addArt), t('profile.added', {name}), admitted).then(ok => {if (ok && additionCurrent()) retireAddition();});
  };
  const forgetAction = () => {
    const target = forgetProfile;
    if (!target || !editorCurrent()) return;
    void runAction(({manager}) => manager.forgetTrust(target.id), t('profile.devicesForgotten', {name: target.name}), () => editorCurrent()).then(ok => {if (ok) closeEdit();});
  };

  const backRef = useRef(back), busyRef = useRef(busy);
  backRef.current = back; busyRef.current = busy;
  useEffect(() => {if (embedded) return; const subscription = BackHandler.addEventListener('hardwareBackPress', () => {if (!busyRef.current) backRef.current(); return true;}); return () => subscription.remove();}, [embedded]);
  if (recovery) return <PhoneProfileRecovery controller={recovery.controller} profileName={recovery.name} username={context.account.account.username}
    onOpen={value => {const id = recovery.id; recovery.controller.dispose(); setRecovery(undefined); setPin(value); void choose(id, value);}}
    onClose={() => {recovery.controller.dispose(); setRecovery(undefined); setPin('');}}/>;
  if (theme.isTV && profile && forgotPIN) return <TVForgotPINHandoff profileName={profile.name}
    url={new URL('/settings/profile', routeOrigin(context.api.baseUrl)).href} body="next.auth.tv.forgotPINBody" onBack={back}/>;
  if (theme.isTV && profile) return <TVProfilePINForm
    profile={{id: profile.id, name: profile.name, initial: [...profile.name][0] ?? '', color: profile.art, primary: profile.primary, pinRequired: true}}
    code={pin} onCodeChange={setPin} busy={busy} error={error ? <Text accessibilityRole="alert" style={[theme.type.caption, {color: theme.colors.danger}]}>{presentError(error, 'profiles').body}</Text> : undefined}
    onSubmit={value => {void choose(profile.id, value);}} onBack={back}
    onForgotPIN={() => {if (!busy) {cancelWrongPin(); setPin(''); setError(undefined); setForgotPIN(true);}}}/>;
  const moveProfile = (target: ManagedProfile, direction: -1 | 1) => {
    if (!profileActionsCurrent()) return;
    const index = profiles.indexOf(target), next = index + direction;
    if (index < 0 || next < 0 || next >= profiles.length) return;
    const order = [...profiles]; [order[index], order[next]] = [order[next], order[index]];
    void runAction(({manager}) => manager.order(order));
  };
  const managementActions = manage && lease && active ? <View testID="profile-actions" style={{marginTop: 18}}>
          <Text style={[theme.type.captionStrong, {color: theme.colors.textTertiary, marginBottom: 8}]}>{[active.name, active.primary ? t('profile.main') : active.id === current?.viewer.profileId ? t('profile.current') : ''].filter(Boolean).join(' · ')}</Text>
          <View style={{borderRadius: 14, overflow: 'hidden', backgroundColor: theme.colors.raised}}>
            {profilePicturePickerAvailable() ? <ProfileActionRow testID="profile-picture" label={t(avatars.some(value => value.profileId === active.id) ? 'profile.changePicture' : 'profile.addPicture')} onPress={() => {
              void runAction(async ({identity}) => {
                const picked = await pickProfilePicture(lease!);
                if (!picked) return;
                await identity.uploadAvatar(active.id, picked.bytes, picked.contentType);
                const next = await identity.avatars();
                if (lease!.current()) setAvatars(next);
              });
            }}/> : null}
            {avatars.some(value => value.profileId === active.id) ? <ProfileActionRow testID="profile-remove-picture" label={t('profile.removePicture')} onPress={() => {
              void runAction(async ({identity}) => {await identity.removeAvatar(active.id); const next = await identity.avatars(); if (lease!.current()) setAvatars(next);});
            }}/> : null}
            <ProfileActionRow testID="profile-rename" label={t('profile.rename')} onPress={() => openEditor('rename')}/>
            <ProfileActionRow testID="profile-limits" label={t('profile.limits')} description={t('profile.limitsHelp')} onPress={() => openEditor('limits')}/>
            <ProfileActionRow testID="profile-pin" label={t(active.pinRequired ? 'profile.changePin' : 'profile.setPin')} onPress={() => openEditor('pin')}/>
            {active.pinRequired ? <ProfileActionRow testID="profile-remove-pin" label={t('profile.removePin')} onPress={() => openEditor('remove-pin')}/>: null}
            <ProfileActionRow testID="profile-forget-devices" label={t('profile.forgetDevices')} onPress={() => openEditor('forget')}/>
            {embedded && profiles.indexOf(active) > 0 && <ProfileActionRow testID="profile-move-up" label={t('web.profile.moveUp')} onPress={() => moveProfile(active, -1)}/>}
            {embedded && profiles.indexOf(active) < profiles.length - 1 && <ProfileActionRow testID="profile-move-down" label={t('web.profile.moveDown')} onPress={() => moveProfile(active, 1)}/>}
            {embedded && !active.primary && <Button variant="danger" size="compact" disabled={actionBusy} onPress={() => {
              if (!management || !profileActionsCurrent()) return;
              void runAction(async ({manager}) => {const preview = await manager.previewDelete(active); if (lease.current() && managementCurrent()) {const next = {preview, management}; deletionRef.current = next; deleteNameRef.current = ''; setDeletion(next); setDeleteName('');}});
            }}>{t('profile.delete')}</Button>}
          </View>
          {problem ? <Text accessibilityRole="alert" style={[theme.type.caption, {color: theme.colors.danger, marginTop: 8}]}>{problem}</Text> : null}
          {notice ? <Text accessibilityLiveRegion="polite" style={[theme.type.caption, {color: theme.colors.textSecondary, marginTop: 8}]}>{notice}</Text> : null}
        </View> : null;
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
      {problem && !managedId ? <Text accessibilityRole="alert" style={[theme.type.caption, {color: theme.colors.danger}]}>{problem}</Text> : null}
      <BottomSheet open={!!managedId && !!active && manage} onOpenChange={open => {if (!open && !activeTicket.current && managementCurrent() && !retirement) {retireEditor(); setManagedId(undefined); closeDeletion();}}} title={active?.name} dismissOnBack={!actionBusy} dismissOnGesture={!actionBusy} dismissOnOutsidePress={!actionBusy}>{managementActions}</BottomSheet>
    </View> : theme.isTV && !profile ? <TVWhosWatching testID="accounts-profile-switch">
      {/* F-PROFILE-7: only the faces take focus; Back is the remote key. A picker has no Add or Manage profiles (design 6212c813); owners do that in Settings on web or phone. */}
      <ProfilePicker focusScope={lease?.live.value && lease.route === route ? `tv.account.profiles.${JSON.stringify([lease.authority, lease.revision])}` : undefined} focusBusy={busy} title={t('profiles.title')} profiles={profiles.map(value => ({id: value.id, name: value.name, initial: [...value.name][0] ?? '', color: value.art, primary: value.primary, pinRequired: value.pinRequired, disabled: busy, source: avatarSource(value.id)}))}
        selectedId={current?.viewer.profileId} preferredProfileId={lease ? profileFocus.get(lease) : undefined}
        onFocusChange={id => {if (id && lease) profileFocus.set(lease, id);}} onSelect={id => {const value = profiles.find(item => item.id === id)!;
          if (id === current?.viewer.profileId) onBack(); else if (value.pinRequired) {setProfile(value); setPin(''); setError(undefined);} else void choose(id);}}/>
      {!!error && <Text accessibilityRole="alert" style={[theme.type.caption, {color: theme.colors.danger, marginTop: 16}]}>{presentError(error, 'profiles').body}</Text>}
      {errorRefresh}
    </TVWhosWatching> : <StateLayout testID="accounts-profile-switch" title={profile ? t('profile.pinTitle', {name: profile.name}) : t('profiles.title')}
      beforeActions={<View style={{alignSelf: 'stretch', marginBottom: 20}}>{profile ? <><PhonePINEntry value={pin} onChange={setPin} onComplete={value => {void choose(profile.id, value);}} busy={busy} error={!!error}/>
        <View style={{alignItems: 'center', marginTop: 8}}><Button variant="text" disabled={busy} onPress={() => {void startRecovery();}}>{t('pin.forgot')}</Button></View></> : <>
        <ProfilePicker focusScope={theme.isTV && lease?.live.value && lease.route === route ? `tv.account.profiles.${JSON.stringify([lease.authority, lease.revision])}` : undefined} focusBusy={busy} profiles={profiles.map(value => ({id: value.id, name: value.name, initial: [...value.name][0] ?? '', color: value.art, primary: value.primary, pinRequired: value.pinRequired, disabled: busy, source: avatarSource(value.id)}))}
          selectedId={current?.viewer.profileId} preferredProfileId={theme.isTV && lease ? profileFocus.get(lease) : undefined}
          onFocusChange={id => {if (id && lease) profileFocus.set(lease, id);}} showTitle={false} onSelect={id => {const value = profiles.find(item => item.id === id)!;
            if (id === current?.viewer.profileId) onBack(); else if (value.pinRequired) {setProfile(value); setPin(''); setError(undefined);} else void choose(id);}}/>
        {manage && profiles.length < 8 ? <View style={{flexDirection: 'row', marginTop: 12}}><Button size="compact" variant="glass" icon="i-plus" onPress={openAddition}>{t('profile.add')}</Button></View> : null}
        {managementActions}
        </>}
        {!!error && <Text accessibilityRole="alert" style={[theme.type.caption, {color: theme.colors.danger}]}>{presentError(error, 'profiles').body}</Text>}
        {errorRefresh}</View>}
      secondaryAction={{id: 'back', label: t('action.back'), disabled: busy, onPress: back}}/>}
    <Dialog testID="profile-add" open={additionCurrent()} onOpenChange={open => {if (!open) closeAddition();}} title={t('profile.addTitle')}
      footer={<View style={{flexDirection: 'row', gap: 8}}><Button focusId="cancel" variant="glass" disabled={actionBusy} onPress={closeAddition}>{t('action.cancel')}</Button><Button focusId="save" loading={actionBusy} disabled={!addName.trim()} onPress={submitAdd}>{t('action.save')}</Button></View>}>
      <TextField label={t('profile.name')} value={addName} onChangeText={value => {if (additionCurrent() && !activeTicket.current) {addNameRef.current = value; setAddName(value);}}} maxLength={120} testID="profile-add-name"/>
      {!theme.isTV && <ProfileColorPicker value={addArt} onChange={value => {if (additionCurrent() && !activeTicket.current) setAddArt(value);}} disabled={actionBusy} testID="profile-add-colors"/>}
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
      {problem ? <Text accessibilityRole="alert" style={[theme.type.caption, {color: theme.colors.danger, marginTop: 12}]}>{problem}</Text> : null}
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
      {problem ? <Text accessibilityRole="alert" style={[theme.type.caption, {color: theme.colors.danger}]}>{problem}</Text> : null}
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
  /** Private witness: the opening names only the id and revision it was drawn for. The sheet's
   * copy and its own save guard keep reading the original profile prop. */
  readonly profile: Readonly<Pick<ManagedProfile, 'id' | 'revision'>>;
  readonly lease: ProfileLease;
  controller?: AbortController;
  loadedRevision?: number;
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
  const profileId = profile.id, profileRevision = profile.revision;
  const current = (candidate: ProfileLimitsOwner) => candidate === ownerRef.current && !candidate.retired && candidate.profile.id === profileId
    && candidate.profile.revision === profileRevision && candidate.lease.current();

  useLayoutEffect(() => {
    const next: ProfileLimitsOwner = {profile: {id: profileId, revision: profileRevision}, lease, retired: false};
    const controller = new AbortController();
    next.controller = controller;
    ownerRef.current = next;
    setOwner(next);
    draftRef.current = undefined; busyRef.current = false;
    setRestrictions(undefined); setSystems(undefined); setDraft(undefined); setError(undefined); setBusy(false);
    const live = () => next === ownerRef.current && !next.retired && next.profile.id === profileId
      && next.profile.revision === profileRevision && next.lease.current();
    const identity = identityFor(controller);
    Promise.all([identity.restrictions(profileId, controller.signal), identity.ratingSystems(controller.signal)]).then(([currentRestrictions, list]) => {
      if (!live() || currentRestrictions.profileId !== next.profile.id) return;
      next.loadedRevision = currentRestrictions.revision;
      draftRef.current = currentRestrictions;
      setRestrictions(currentRestrictions); setSystems(list); setDraft(currentRestrictions);
    }, failure => {if (live() && !controller.signal.aborted) setError(failure);});
    return () => {
      next.retired = true;
      next.controller?.abort();
      if (ownerRef.current === next) ownerRef.current = undefined;
      setOwner(value => value === next ? undefined : value);
    };
  }, [identityFor, lease, profileId, profileRevision, attempt]);

  const updateDraft = (change: Partial<ProfileRestrictions>) => {
    if (!owner || !current(owner) || busyRef.current || !draft || draftRef.current !== draft) return;
    const next = {...draft, ...change}; draftRef.current = next; setDraft(next);
  };
  const close = () => {if (owner && current(owner) && !busyRef.current) onClose();};
  const save = () => {
    const sheet = owner, currentRestrictions = restrictions, chosen = draft;
    if (!sheet || !current(sheet) || !currentRestrictions || !chosen || currentRestrictions.profileId !== sheet.profile.id
      || currentRestrictions.revision !== sheet.loadedRevision || sheet.profile.revision !== profile.revision || draftRef.current !== chosen || busyRef.current) return;
    const controller = new AbortController();
    sheet.controller?.abort();
    sheet.controller = controller;
    busyRef.current = true; setBusy(true); setError(undefined);
    const identity = identityFor(controller);
    void identity.saveRestrictions(currentRestrictions, {
      ratingSystem: chosen.ratingSystem, maximumAgeRating: chosen.maximumAgeRating, allowUnrated: chosen.allowUnrated, blockedLabels: chosen.blockedLabels,
      allowDownloads: chosen.allowDownloads, allowLiveTv: chosen.allowLiveTv, allowDvr: chosen.allowDvr, allowWatchTogether: chosen.allowWatchTogether,
    }).then(next => {
      if (!current(sheet) || controller.signal.aborted || next.profileId !== sheet.profile.id || next.revision <= (sheet.loadedRevision ?? 0)) return;
      sheet.loadedRevision = next.revision;
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
export function ServersRoute({context, fallbackDownloads, initialPanel = 'servers', onBack, onComplete, onSetupComplete}: {context?: NativeShellBootstrapContext; fallbackDownloads?: AccountDownloadsEntry; initialPanel?: 'servers' | 'consent-kind'; onBack?: () => void; onComplete?: () => void; onSetupComplete?: (serverId: string) => void}) {
  const theme = useTheme(), [panel, setPanel] = useState<Panel>(initialPanel), [servers, setServers] = useState<Awaited<ReturnType<typeof nativeAccounts.snapshot>>>(), [busy, setBusy] = useState(false), [error, setError] = useState<unknown>();
  const [captured, setCaptured] = useState<{session: AppServerSession; name: string}>(), [accountScope, setAccountScope] = useState<{accountId: string; familyId: string}>(), [code, setCode] = useState('');
  const [preview, setPreview] = useState<Readonly<SetupPreview>>(), [consentKind, setConsentKind] = useState<'local' | 'portico'>(), [done, setDone] = useState('');
  const [removeDownloads, setRemoveDownloads] = useState(false);
  const [addGeneration, setAddGeneration] = useState(0);
  type AddOpening = {active: boolean; generation: number; context?: NativeShellBootstrapContext; fallback?: AccountDownloadsEntry; downloads?: AccountDownloadsEntry; disposeDownloads?: () => void};
  const addOwner = useRef<AddOpening | undefined>(undefined), [committedAdd, setCommittedAdd] = useState<AddOpening>();
  const panelRef = useRef(panel); panelRef.current = panel;
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
  const close = () => {retireAdd(); approver.current?.dispose(); approver.current = undefined; setPreview(undefined); setCode(''); setError(undefined); setPanel('servers');};
  const cancel = () => {if (panel === 'servers') onBack?.(); else close();};
  useEffect(() => {if (panel === 'add') return; const subscription = BackHandler.addEventListener('hardwareBackPress', () => {
    if (busy) return true;
    if (panel === 'servers' && !onBack) return false;
    cancel(); return true;
  }); return () => subscription.remove();});
  const action = (id: string, label: string, work: () => Promise<void>, disabled = false) => ({id, label, disabled: busy || disabled, loading: busy, onPress: () => {void run(work);}});
  const errorView = error ? <Text accessibilityRole="alert" style={[theme.type.caption, {color: theme.colors.danger, marginBottom: 12}]}>{presentError(error, 'account').body}</Text> : null;
  const layout = (title: string, body?: string, before?: React.ReactNode, primary?: ReturnType<typeof action>, more?: React.ReactNode) => <StateLayout testID="accounts-servers" title={title} body={body}
    beforeActions={<>{before}{errorView}</>} primaryAction={primary} secondaryAction={panel !== 'servers' || onBack ? {id: 'back', label: t('action.back'), disabled: busy, onPress: cancel} : undefined}>{more}</StateLayout>;
  if (panel === 'add') return <AccountEntry key={addGeneration} downloads={addCurrent() ? committedAdd?.downloads : undefined} onSetupComplete={onSetupComplete} initialStage="address" onBack={() => {if (addCurrent()) close();}} onComplete={() => {if (addCurrent()) {close(); onComplete?.();}}}/>;
  if (panel === 'sign-out-account') return layout(t('accounts.signOut.portico'), undefined, undefined,
    action('sign-out-account', t('action.signOut'), async () => {await nativeAccounts.signOutPorticoAccount(accountScope!); close(); onComplete?.();}));
  if (panel === 'consent-kind') return layout(t('settings.linkTV'), t('linkTV.enterCode'), <View style={{gap: 12, alignSelf: 'stretch', marginBottom: 20}}>
    {context && servers?.servers.find(value => value.id === servers.selectedServerId)?.signIn === 'local' && <Button variant="glass" disabled={busy} onPress={() => {void run(async () => {
      approver.current = await nativeAccounts.localTVConsent(context.api, context.session); setConsentKind('local'); setPanel('consent-code');
    });}}>{t('accounts.session.local')}</Button>}
    <Button variant="glass" disabled={busy} onPress={() => {void run(async () => {approver.current = await nativeAccounts.porticoTVConsent(); setConsentKind('portico'); setPanel('consent-code');});}}>{t('account.portico')}</Button>
  </View>);
  if (panel === 'consent-code') return layout(t('settings.linkTV'), t(consentKind === 'portico' ? 'accounts.consent.portico' : 'accounts.consent.local'),
    <TextField label={t('linkTV.enterCode')} value={code} onChangeText={value => setCode(formatTVLinkCode(value))} autoCapitalize="characters" maxLength={9} autoCorrect={false} editable={!busy} containerStyle={{marginBottom: 20}}/>,
    action('review-tv', t('web.device.review'), async () => {setPreview(await approver.current!.review(code)); setPanel('consent-review');}, !completeTVLinkCode(code)));
  if (panel === 'consent-review') return layout(t('linkTV.askConfirm'), t('web.device.approveWarn'),
    <View style={{marginBottom: 20}}>{[preview!.deviceName, preview!.platform, preview!.appVersion, preview!.userCode, t(consentKind === 'portico' ? 'accounts.session.portico' : 'accounts.session.local')].map((value, index) => <Text key={index} style={[theme.type.body, {color: theme.colors.text}]}>{value}</Text>)}</View>,
    action('consent-approve', t('web.device.signItIn'), async () => {await approver.current!.decide(preview!, 'approve'); setDone(t('linkTV.approved', {device: preview!.deviceName})); setPanel('consent-done');}),
    <Button variant="text" disabled={busy} onPress={() => {void run(async () => {await approver.current!.decide(preview!, 'deny'); setDone(t('linkTV.declined')); setPanel('consent-done');});}}>{t('web.device.notMine')}</Button>);
  if (panel === 'consent-done') return layout(done, undefined, undefined, action('done', t('action.done'), async () => close()));
  const deviceNoun = Platform.isTV ? Platform.OS === 'ios' ? 'device.noun.appleTV' : String((Platform.constants as {Manufacturer?: string}).Manufacturer ?? '').toLowerCase() === 'amazon' ? 'device.noun.fireTV' : 'device.noun.androidTV'
    : Platform.OS === 'ios' ? (Platform as {isPad?: boolean}).isPad ? 'device.noun.iPad' : 'device.noun.iPhone' : Math.min(Dimensions.get('screen').width, Dimensions.get('screen').height) >= 600 ? 'device.noun.tablet' : 'device.noun.phone';
  const selectedServer = !!captured && servers?.selectedServerId === captured.session.connection.pin.serverId;
  const signOutSheet = captured ? <BottomSheet open={panel === 'sign-out-server'} onOpenChange={open => {if (!open) {setRemoveDownloads(false); close();}}} testID="accounts-sign-out-sheet"
    title={t('settings.signOutConfirm.title', {server: captured.name, device: t(deviceNoun)})}
    footer={<View style={{gap: 10}}>
      <Focusable focusId="sign-out-confirm" accessibilityRole="button" accessibilityLabel={t('action.signOut')} disabled={busy}
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
          await nativeAccounts.signOutServer(captured.session.connection.pin.serverId, captured.session.connection.session.sessionFamilyId);
          setRemoveDownloads(false); close(); onComplete?.();
        });}} focusRadius={12} focusTreatment="none"
        style={{height: 54, borderRadius: 12, backgroundColor: `${theme.colors.danger}24`, alignItems: 'center', justifyContent: 'center'}}>
        <Text style={[theme.type.button, {color: theme.colors.danger}]}>{t('action.signOut')}</Text>
      </Focusable>
      <Button focusId="sign-out-cancel" variant="text" disabled={busy} onPress={() => {setRemoveDownloads(false); close();}} textStyle={{color: theme.colors.textSecondary}}>{t('action.cancel')}</Button>
    </View>}>
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
  if (!Platform.isTV) return <><PhoneServerChooser certificates={<TVTrustedCertificates context={context}/>} snapshot={servers} busy={busy} error={errorView} onBack={onBack ? cancel : undefined}
    onSelect={value => {void run(async () => {await nativeAccounts.switchServer(value.id); onComplete?.();});}}
    onSignOut={value => {void run(async () => {
      const state = await nativeAppSessions.read(), found = state.servers.find(item => item.connection.pin.serverId === value.id);
      if (!found || found.connection.session.sessionFamilyId !== value.familyId || found.connection.pin.fingerprint !== value.fingerprint)
        throw Object.assign(new Error('native_server_changed'), {code: 'cancelled'});
      setCaptured({session: found, name: value.name}); setPanel('sign-out-server');
    });}}
    onRefresh={() => {void run(async () => {const value = await nativeAccounts.snapshot(); if (alive.current) setServers(value);});}}
    onAdd={() => {void run(async () => {openAdd();});}}
    footer={<View style={{alignSelf: 'stretch', gap: 8, marginTop: 10}}>
      <Button variant="text" disabled={busy} onPress={() => setPanel('consent-kind')}>{t('settings.linkTV')}</Button>
      {servers?.servers.some(value => value.signIn === 'portico') && <Button variant="text" disabled={busy} onPress={() => {void run(async () => {
        const credentials = nativeAccounts.accountCredentials(); await credentials.synchronize(); const session = credentials.getSnapshot().session;
        if (!session) throw Object.assign(new Error('native_account_signed_out'), {code: 'account_changed'});
        setAccountScope({accountId: session.account.id, familyId: session.familyId}); setPanel('sign-out-account');
      });}}>{t('accounts.signOut.portico')}</Button>}
    </View>}/>{signOutSheet}</>;
  return <><TVServerChooser snapshot={servers} busy={busy} error={errorView} onBack={onBack ? cancel : undefined}
    owner={!!context && context.current() && context.account.serverId === servers?.selectedServerId && context.account.account.role === 'owner'}
    onSelect={value => {void run(async () => {await nativeAccounts.switchServer(value.id); onComplete?.();});}}
    onSignOut={value => {void run(async () => {
      const state = await nativeAppSessions.read(), found = state.servers.find(item => item.connection.pin.serverId === value.id);
      if (!found || found.connection.session.sessionFamilyId !== value.familyId || found.connection.pin.fingerprint !== value.fingerprint)
        throw Object.assign(new Error('native_server_changed'), {code: 'cancelled'});
      setCaptured({session: found, name: value.name}); setPanel('sign-out-server');
    });}}
    onRefresh={() => {void run(async () => {const value = await nativeAccounts.snapshot(); if (alive.current) setServers(value);});}}
    onAdd={() => {void run(async () => {openAdd();});}}
    certificates={<TVTrustedCertificates context={context}/>}
    footer={<View style={{alignSelf: 'stretch', gap: 8, marginTop: 10}}>
    <Button variant="text" disabled={busy} onPress={() => setPanel('consent-kind')}>{t('settings.linkTV')}</Button>
    {servers?.servers.some(value => value.signIn === 'portico') && <Button variant="text" disabled={busy} onPress={() => {void run(async () => {
      const credentials = nativeAccounts.accountCredentials(); await credentials.synchronize(); const session = credentials.getSnapshot().session;
      if (!session) throw Object.assign(new Error('native_account_signed_out'), {code: 'account_changed'});
      setAccountScope({accountId: session.account.id, familyId: session.familyId}); setPanel('sign-out-account');
    });}}>{t('accounts.signOut.portico')}</Button>}
  </View>}/>{signOutSheet}</>;
}

const createAccountRoute: NativeRouteFactory = context => ({render: route => {
  if (route.kind !== 'profiles' && route.kind !== 'servers') throw new Error('native_account_route_mismatch');
  return <AccountRoute key={context.navigation.getSnapshot().revision} route={route} context={context}/>;
}});
nativeShellRoutes.registerRoute('profiles', createAccountRoute);
nativeShellRoutes.registerRoute('servers', createAccountRoute);
