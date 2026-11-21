import {useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState} from 'react';
import {createProfileAvatarOpeningTransport} from '../../../../../../packages/client-core/src/query/profile-security-transport';
import {readDirectAccount} from '../../../../../../packages/client-core/src/query/connection-operations.ts';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {presentError} from '../../../../../../packages/client-core/src/presentation/errors';
import {ManagedProfilesClient, parseDirectSnapshot, type ManagedProfile, type ProfileArt, type ProfileDeletion} from '../../../../../../packages/client-core/src/profile-management';
import {IdentityClient} from '../../../../../../packages/client-core/src/identity-client';
import {HttpHostedApi} from '../../../../../../packages/client-core/src';
import type {ProfileAvatar} from '../../../../../../packages/client-core/src/identity-restrictions';
import {nativeAccounts} from '../../../platform/accounts';
import {nativeAppSessions} from '../../../platform/app-session-storage';
import {switchNativeProfile} from '../../../platform/native-session-lifecycle';
import {NativeProfileRecovery, temporaryRecoveryApi} from '../../../platform/profile-recovery';
import {hostedUrl} from '../../../platform/account-authority';
import {pickProfilePicture, profileAvatarUpload, profilePicturePickerAvailable, profilePictureSource} from '../../../platform/profile-avatars';
import {nativeNetworkFetch} from '../../../platform/server-connections';
import type {NativeShellBootstrapContext} from '../../../shell/bootstrap';
import type {NativeShellRoute} from '../../../shell/routes';
import {sameAuthority, type ShellAuthority} from '../../../shell/model';
import {NativeScreenLifetime} from '../../../screens/settings-live/lifetime';
import {registerBack} from '../../../kit/navigation/back';
import {useTheme} from '../../../kit/theme';

const t = defaultI18n.t;

export type ProfilesProps = {context: NativeShellBootstrapContext; route: NativeShellRoute; onBack: () => void; embedded?: boolean};

export type ProfileLease = Readonly<{
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
export type ProfileManagementOpening = Readonly<{id: string}>;
type ProfileEditorOpening = Readonly<{
  kind: 'rename' | 'pin' | 'remove-pin' | 'forget' | 'limits';
  profile: ManagedProfile;
  lease: ProfileLease;
  management?: ProfileManagementOpening;
}>;
type ProfileAddOpening = Readonly<{lease: ProfileLease}>;
type ProfileDeleteOpening = Readonly<{preview: ProfileDeletion; management: ProfileManagementOpening}>;

const profileAbort = () => Object.assign(new Error('profile_command_retired'), {name: 'AbortError'});

/** Switching and managing the account's profiles (Settings > Switch profile; the Profiles panel in wider Settings):
 * the route lease every command is scoped to, the switch with its PIN and forgotten-PIN recovery, and the owner's
 * add, rename, picture, PIN, limits, forget-devices, order and delete commands. Each form draws it its own way. */
export function useProfiles({context, route, onBack, embedded = false}: ProfilesProps) {
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
  // A new profile's colour is sent only once chosen; until then the server's pick (the first unused) is shown.
  const [addArt, setAddArt] = useState<ProfileArt>(), [artDraft, setArtDraft] = useState<ProfileArt>('blue');
  const [actionBusy, setActionBusy] = useState(false), [problem, setProblem] = useState(''), [notice, setNotice] = useState('');
  const [lease, setLease] = useState<ProfileLease>();
  const activeTicket = useRef<AbortController | undefined>(undefined);
  const retireEditor = () => {editorRef.current = undefined; setEditor(undefined); editDraftRef.current = {name: '', pin: '', again: ''}; setNameDraft(''); setPinDraft(''); setPinAgain('');};
  const retireAddition = () => {additionRef.current = undefined; setAddition(undefined); addNameRef.current = ''; setAddName('');};
  const alive = useRef(true), starting = useRef(false);
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
  const scopedRaw = useCallback((ticket: {lease: ProfileLease; controller: AbortController}) => createProfileAvatarOpeningTransport(context.api, {
    current: () => ticket.lease.current(), signal: ticket.controller.signal, cancelled: profileAbort,
    run: (work, signal) => ticket.lease.lifetime.request(work, signal),
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
  useEffect(() => {alive.current = true; return () => {alive.current = false;};}, [context]);
  // TV (F-PROFILE-28): a wrong PIN keeps the filled boxes in the error colour for 400 ms, then empties them.
  const wrongPinTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const cancelWrongPin = () => {if (wrongPinTimer.current) {clearTimeout(wrongPinTimer.current); wrongPinTimer.current = undefined;}};
  useEffect(() => cancelWrongPin, []);
  // R-16: a stale context or a missing controller says "this changed" (with Refresh), never nothing.
  const staleProfiles = () => Object.assign(new Error('native_profiles_changed'), {code: 'stale'});
  const choose = async (id: string, code?: string) => {if (busy) return;
    const session = context.session();
    if (!context.current() || !session) {setError(staleProfiles()); return;}
    setBusy(true); setError(undefined);
    // The session lifecycle makes the switch: a profile with a PIN always asks for it.
    try {await switchNativeProfile(session.sessionFamilyId, id, code);} catch (failure) {if (alive.current) {
      if (theme.isTV && code !== undefined) {cancelWrongPin(); wrongPinTimer.current = setTimeout(() => {wrongPinTimer.current = undefined; if (alive.current) setPin(current => current === code ? '' : current);}, 400);} else setPin('');
      setError(failure);
    }} finally {if (alive.current) setBusy(false);}
  };
  // E-mobile-25: "This changed…" always comes with a way to read the list again.
  const refreshProfiles = async () => {
    if (busy || !context.current()) return; setBusy(true);
    try {
      const raw = await readDirectAccount(context.api);
      const account = parseDirectSnapshot(raw, {authority: 'local', serverId: context.account.serverId, accountId: context.account.account.id}, true);
      if (alive.current) {setProfiles(account.profiles); setError(undefined);}
    } catch (failure) {if (alive.current) setError(failure);} finally {if (alive.current) setBusy(false);}
  };
  const canRefresh = !!error && presentError(error, 'profiles').action === 'refresh';
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
    const next = {lease}; retireAddition(); additionRef.current = next; setAddition(next); setAddArt(undefined); setProblem(''); setNotice('');
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
  const back = () => {cancelWrongPin(); activeTicket.current?.abort(); setPinDraft(''); setPinAgain(''); if (recovery) {recovery.controller.dispose(); setRecovery(undefined); setPin('');} else if (forgotPIN) {setForgotPIN(false); setPin(''); setError(undefined);} else if (profile) {setProfile(undefined); setPin(''); setError(undefined);} else (tvLeave.current ?? onBack)();};
  const avatarSource = (profileId: string) => {
    if (!lease || !lease.current() || avatarsLease.current !== lease) return undefined;
    const avatar = avatars.find(value => value.profileId === profileId);
    const picture = profilePictureSource(context.api, current?.accessToken, avatar, 90);
    return picture && typeof picture === 'object' && 'uri' in picture && typeof picture.uri === 'string'
      ? {uri: picture.uri, cacheKey: `avatar:${profileId}:${avatar?.url ?? ''}`, ...(picture.headers ? {headers: picture.headers as Readonly<Record<string, string>>} : {})}
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

  const backRef = useRef(back), busyRef = useRef(busy), tvLeave = useRef<() => void>(undefined);
  backRef.current = back; busyRef.current = busy;
  useEffect(() => {if (embedded) return; const subscription = registerBack(() => {if (!busyRef.current) backRef.current(); return true;}, {scope: 'surface'}); return () => subscription();}, [embedded]);
  const moveProfile = (target: ManagedProfile, direction: -1 | 1) => {
    if (!profileActionsCurrent()) return;
    const index = profiles.indexOf(target), next = index + direction;
    if (index < 0 || next < 0 || next >= profiles.length) return;
    const order = [...profiles]; [order[index], order[next]] = [order[next], order[index]];
    void runAction(({manager}) => manager.order(order));
  };
  const localPictures = context.signIn !== 'portico' && !context.account.account.hostedAccountId;
  // The picture rows' work, shared by every form that draws them.
  const pickPicture = (target: ManagedProfile) => {
    if (!lease) return;
    const held = lease;
    void runAction(async ({identity}) => {
      const picked = await pickProfilePicture(held);
      if (!picked) return;
      await identity.uploadAvatar(target.id, picked.bytes, picked.contentType);
      const next = await identity.avatars();
      if (held.current()) setAvatars(next);
    });
  };
  const removePicture = (target: ManagedProfile) => {
    if (!lease) return;
    const held = lease;
    void runAction(async ({identity}) => {await identity.removeAvatar(target.id); const next = await identity.avatars(); if (held.current()) setAvatars(next);});
  };
  // The page's own words and choices, for the forms drawn from kit parts.
  const [origin] = useState(() => context.navigation.getSnapshot().destination);
  const backLabel = origin?.kind === 'settings' ? t('next.shell.settings') : t('action.back');
  const picturePicker = localPictures && profilePicturePickerAvailable();
  const hasPicture = (id: string) => avatars.some(value => value.profileId === id);
  const select = (id: string) => {
    const value = profiles.find(item => item.id === id);
    if (!value) return;
    if (id === current?.viewer.profileId) back();
    else if (value.pinRequired) {setProfile(value); setPin(''); setError(undefined);}
    else void choose(id);
  };
  return {backLabel, picturePicker, hasPicture, select, actionBusy, active, activeTicket, addArt, addName, addNameRef, addition, additionCurrent, additionRef, alive, artDraft, avatarSource, avatars, avatarsLease, back, backRef, busy, busyRef, canRefresh, cancelWrongPin, choose, closeAddition, closeDeletion, closeEdit, current, deleteName, deleteNameRef, deletion, deletionCurrent, deletionRef, edit, editDraftRef, editor, editorCurrent, editorRef, error, forgetAction, forgetProfile, forgotPIN, identityForLimits, lease, limitsLease, limitsProfile, localPictures, manage, managedId, management, managementCurrent, managementRef, moveProfile, nameDraft, notice, openAddition, openEditor, pickPicture, pin, pinAgain, pinDraft, problem, profile, profileActionsCurrent, profileFocus, profiles, recovery, refreshProfiles, removePicture, removePin, removePinAction, retireAddition, retireEditor, retirement, runAction, scopedIdentity, scopedManager, scopedRaw, scopedTransport, setActionBusy, setAddArt, setAddName, setAddition, setArtDraft, setAvatars, setBusy, setDeleteName, setDeletion, setEditor, setError, setForgotPIN, setLease, setManagedId, setManagement, setNameDraft, setNotice, setPin, setPinAgain, setPinDraft, setProblem, setProfile, setProfiles, setRecovery, setRetirement, staleProfiles, startRecovery, starting, submitAdd, submitPin, submitRename, tvLeave, wrongPinTimer};
}
