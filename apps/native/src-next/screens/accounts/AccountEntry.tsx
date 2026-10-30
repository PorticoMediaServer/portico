import {accountExpectation, nativeAccountRead} from '../../platform/account-work';
import React, {useCallback, useEffect, useLayoutEffect, useRef, useState} from 'react';
import {AppState, BackHandler, Linking, NativeModules, Platform, Share, Image, Text, View} from 'react-native';
import {useTVMenuCapture} from '../../platform/tv-menu';
import QRCode from 'react-native-qrcode-svg';
import {HttpHostedApi, type HostedServer} from '../../../../../packages/client-core/src';
import {ServerSignInProbe, moveOfferDays, type ServerSignIn} from '../../../../../packages/client-core/src/sign-in';
import {routeOrigin} from '../../../../../packages/client-core/src/route-identity';
import type {NativeInvitationEntry} from '../../platform/portico-invitation';
import type {PublicInvitation} from '../../../../../packages/client-core/src/direct-account';
import type {ManagedProfile} from '../../../../../packages/client-core/src/profile-management';
import {MFARequiredError, type IdentityOptions, type IdentityTransaction, type Registration} from '../../../../../packages/client-core/src/hosted-security';
import type {SetupCodeSnapshot} from '../../../../../packages/client-core/src/setup-code';
import {presentError} from '../../../../../packages/client-core/src/presentation/errors';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {StateLayout} from '../../kit/states';
import {Button, Focusable, TextField} from '../../kit/controls';
import {Icon} from '../../kit/Icon';
import {ProfilePicker} from '../../kit/artwork';
import {BootWaitLine} from '../../shell/StartupBoot';
import {negotiationRefusal} from '../../platform/negotiation';
import {ServerNeedsUpdate} from './ServerNeedsUpdate';
import {useTheme} from '../../kit/theme';
import {nativeAccounts} from '../../platform/accounts';
import {NativeServerSignIn} from '../../platform/server-sign-in';
import {NativeSetupEnded} from '../../platform/setup-outcome';
import {HostedSignInUnavailable} from '../../platform/hosted-sign-in-unavailable';
import {NativePorticoSignIn, porticoUsernameUnavailable, type NativeFactorWitness} from '../../platform/portico-sign-in';
import {nativeNetworkFetch, browseNearby} from '../../platform/server-connections';
import {createNativePorticoTVSignIn} from '../../platform/tv-sign-in';
import {createTVApprovalCredentialOpening, type TVApprovalCredentialOpening} from './tv-approval-credentials';
import {createTVApprovalFinalization, type TVApprovalFinalization} from './tv-approval-finalization';
import {createTVCodeOpening, type TVCodeCapture, type TVCodeKind, type TVCodeOpening} from './tv-code-opening';
import {hostedUrl} from '../../platform/account-authority';
import {TVCredentialForm, TVCodePresenter, TVVerificationForm, TVRequiredSetupHandoff, TVFrontDoor, TVProfilePINForm, TVForgotPINHandoff, TVWhosWatching} from '../../tv/auth';
import {TVIdentityCodeForm} from '../../tv/auth/TVIdentityCodeForm';
import type {NativeProfileRecovery} from '../../platform/profile-recovery';
import {passwordStrength} from '../../../../../packages/client-core/src/password-strength';
import {parseJoinLink} from '../../../../../packages/client-core/src/join-link';
import {formatShortInvitationCode, parseShortInvitationCode} from '../../../../../packages/client-core/src/short-invitation-code';
import type {NativeSignInHint} from '../../platform/sign-in-ended';
import {PhoneOwnerReset} from './PhoneOwnerReset';
import type {ResetFileLocation} from '../../../../../packages/client-core/src/account-reset';
import {PhoneSetupApproval} from './PhoneSetupApproval';
import {PhoneFrontDoor} from './PhoneFrontDoor';
import {PhoneNoServers} from './PhoneNoServers';
import {qrScannerAvailable, scanQRCode} from '../../platform/qr-scanner';
import type {AccountDownloadsEntry} from './downloads-entry-model';
import type {NativeInvitationLink} from '../../platform/invitation-links';
import {PhoneAccountWorkspace} from './PhoneAccountWorkspace';
import {NativeAccountWorkspace} from '../../platform/account-workspace';
import {TVNoServers} from './TVNoServers';
import {TVOfflineServers} from './TVOfflineServers';
import {PhoneAccountMoveNotice} from './PhoneAccountMoveNotice';
import {PhoneMovePassword} from './PhoneMovePassword';
import {PhoneMoveCode} from './PhoneMoveCode';
import {EndedMoveCodeAction} from './EndedMoveCodeAction';
import {DirectoryAccessRefused} from './DirectoryAccessRefused';
import {directoryAccessRefused, directoryClockSkew, directoryIdentityUnconfirmed} from './directory-refusal';
import {PhonePINEntry} from './PhonePINEntry';
import {PhoneAccountSignIn} from './PhoneAccountSignIn';
import {LegalAgreement} from './LegalAgreement';
import {PhoneAccountForm} from './PhoneAccountForm';
import {PasswordStrengthMeter} from './PasswordStrengthMeter';
import {PhoneProfileRecovery} from './PhoneProfileRecovery';
import {directoryFailure, directoryOfflineBody, directoryRows, nextRetryDelay} from './directory-chooser';

export type NativeAccountEntryStage = 'welcome' | 'ended-sign-in' | 'owner-reset' | 'owner-reset-code' | 'owner-reset-done' | 'setup-ended' | 'setup-confirm' | 'setup-choice' | 'setup-review' | 'setup-hosted' | 'setup-local' | 'address' | 'identity-code' | 'local' | 'factor' | 'uses-portico' | 'portico' | 'forgot' | 'forgot-sent' | 'register' | 'verify' | 'provider' | 'contact' | 'servers' | 'account-sign-out' | 'profiles' | 'profile-pin' | 'profile-forgot-pin' | 'tv' | 'tv-credentials' | 'tv-required-setup' | 'member-move' | 'invitation-code' | 'invitation-local' | 'invitation-portico' | 'invitation-sign-out' | 'invitation-entry' | 'invitation-account' | 'account-workspace';
type MemberMove = Awaited<ReturnType<NativeServerSignIn['createMemberMove']>>;
type MemberMoveSnapshot = ReturnType<MemberMove['getSnapshot']>;
type PINRecovery = {controller: NativeProfileRecovery; profile: ManagedProfile; operation: NativeServerSignIn; retired: boolean};
type RequiredSetup = Readonly<{kind: 'required-setup'; requirement: 'password' | 'two-step'; serverName: string; serverOrigin: string}>;
type Result = {kind: 'profiles'; profiles: readonly ManagedProfile[]} | {kind: 'challenge'; expiresAt: string} | {kind: 'complete'} | RequiredSetup;
const t = defaultI18n.t;
type SetupHostedOpening = Readonly<{source: NativeServerSignIn; life: AbortController; owner: {live: boolean}; url?: string}>;
type SetupResumePorts = Readonly<{opening: SetupHostedOpening; run: (work: () => Promise<void>) => void;
  finish: (opening: SetupHostedOpening | undefined) => Promise<void>; current: (opening: SetupHostedOpening | undefined) => boolean}>;
/** Resume the drawn setup-hosted opening through the exact ports of the commit that drives it:
 * the same run() and completion the Continue button draws with, so the runner keeps the busy,
 * stage and recovery it captured and no port is re-read from a later render. A resume is owned
 * by the opening it was drawn for, and only while the app is active. */
function resumeSetupHosted(opening: SetupHostedOpening | undefined, ports: SetupResumePorts | undefined, appActive: boolean) {
  if (!opening || !appActive || !ports || ports.opening !== opening || !ports.current(opening)) return;
  ports.run(() => ports.finish(opening));
}

/** Current normal entry, drawn in connect39 onward. The separately labelled historical
 * setup entry is not imported. Every button below dispatches an actual native/core command. */
export function AccountEntry({downloads, incomingInvitation, onBack, onComplete, onSetupComplete, onStageChange, initialStage = 'welcome', initialAddress = '', endedSignIn}: {downloads?: AccountDownloadsEntry; incomingInvitation?: NativeInvitationLink; onBack?: () => void; onComplete: () => void; onSetupComplete?: (serverId: string) => void; onStageChange?: (stage: NativeAccountEntryStage) => void; initialStage?: 'welcome' | 'address'; initialAddress?: string; endedSignIn?: NativeSignInHint}) {
  const latestEndedHint = useRef(endedSignIn); latestEndedHint.current = endedSignIn;
  const preparedAuthSource = useRef<NativePorticoSignIn | undefined>(undefined);
  // The original phone provider opening owns its waiting paint across the OS
  // handoff, callback read and session adoption. It is paint, never authority.
  const [providerLoading, setProviderLoading] = useState<NativePorticoSignIn>();
  const theme = useTheme(), [stage, setStage] = useState<NativeAccountEntryStage>(endedSignIn ? 'ended-sign-in' : initialStage), [busy, setBusy] = useState(false), [error, setError] = useState<unknown>();
  const [address, setAddress] = useState(endedSignIn?.origin ?? initialAddress), [username, setUsername] = useState(endedSignIn?.signIn === 'local' ? endedSignIn.username : ''), [password, setPassword] = useState(''), [code, setCode] = useState('');
  const wrongCodeTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  useEffect(() => () => {if (wrongCodeTimer.current) {clearTimeout(wrongCodeTimer.current); wrongCodeTimer.current = undefined;}}, [stage]);
  // Root 43: only the server's confirmed public-handle collision may mark this
  // field, and only for the form opening that asked. The opening retires with its
  // stage, so a retained Submit or a late refusal cannot mark a successor form.
  const [usernameUnavailable, setUsernameUnavailable] = useState(false), [passwordShown, setPasswordShown] = useState(false);
  const [registerOpening, setRegisterOpening] = useState<Readonly<{source: NativePorticoSignIn; owner: {live: boolean}}>>();
  const [verifyOpening, setVerifyOpening] = useState<Readonly<{source: NativePorticoSignIn; registration: Registration}>>();
  const verifyOpeningRef = useRef(verifyOpening); verifyOpeningRef.current = verifyOpening;
  // The provider completion's opening: the exact transaction this form was drawn for.
  const [providerOpening, setProviderOpening] = useState<Readonly<{transaction: IdentityTransaction; source: NativePorticoSignIn; owner: {live: boolean}; life: AbortController}>>();
  const [providerUsernameUnavailable, setProviderUsernameUnavailable] = useState(false);
  // The phone Portico factor form's opening: its exact issued witness, source and life.
  const [factorOpening, setFactorOpening] = useState<Readonly<{source: NativePorticoSignIn; witness: NativeFactorWitness; owner: {live: boolean}; life: AbortController}>>();
  type SetupReview = Awaited<ReturnType<NativeServerSignIn['reviewHostedSetup']>>;
  const [setupReview, setSetupReview] = useState<SetupReview>();
  const setupReviewTicket = useRef<{source: NativeServerSignIn; controller: AbortController; review?: SetupReview} | undefined>(undefined);
  // The retirement commands below own only refs and stable setters, so they keep one identity
  // for the life of this entry. Effects refer to them by that identity instead of reopening on
  // every render.
  const retireSetupReview = useCallback(() => {const ticket = setupReviewTicket.current; setupReviewTicket.current = undefined; ticket?.controller.abort(); ticket?.review?.approval.dispose(); setSetupReview(undefined);}, []);
  type OwnerRecoveryOpening = {active: boolean; contact?: {open: () => Promise<void>; dispose: () => void}; finish?: () => void};
  const ownerRecoveryContact = useRef<OwnerRecoveryOpening | undefined>(undefined);
  const ownerRecoveryMounted = useRef(false);
  const retireOwnerRecoveryContact = useCallback(() => {
    const ticket = ownerRecoveryContact.current; ownerRecoveryContact.current = undefined;
    if (ticket) {ticket.active = false; ticket.contact?.dispose(); ticket.finish?.();}
  }, []);
  const [resetLocation, setResetLocation] = useState<ResetFileLocation>();
  const ownerResetIntent = useRef<Readonly<{origin: string; serverId: string}> | undefined>(undefined);
  const ownerResetOpening = useRef<{source: NativeServerSignIn; controller: AbortController} | undefined>(undefined);
  const retireOwnerReset = useCallback(() => {const ticket = ownerResetOpening.current; ownerResetOpening.current = undefined; ticket?.controller.abort(); ticket?.source.dispose();}, []);
  const [setupEndedReason, setSetupEndedReason] = useState<'declined' | 'cancelled'>('cancelled');
  const [setupMode, setSetupMode] = useState<'hosted' | 'local'>('hosted');
  const [setupAddress, setSetupAddress] = useState('');
  const [setupApprovalURL, setSetupApprovalURL] = useState<string>();
  const [setupHostedOpening, setSetupHostedOpening] = useState<SetupHostedOpening>();
  // The drawn opening's resume ports for the commit that owns it (see resumeSetupHosted).
  const setupResume = useRef<SetupResumePorts | undefined>(undefined);
  const [setupPasswordConfirm, setSetupPasswordConfirm] = useState(''), [name, setName] = useState(''), [email, setEmail] = useState(''), [masked, setMasked] = useState('');
  const [server, setServer] = useState<ServerSignIn>(), [profiles, setProfiles] = useState<readonly ManagedProfile[]>([]), [profile, setProfile] = useState<ManagedProfile>();
  const profileFocus = useRef(new WeakMap<NativeServerSignIn, string>()).current;
  const [identityCode, setIdentityCode] = useState(''), [identityWrong, setIdentityWrong] = useState(false), [identityAddress, setIdentityAddress] = useState('');
  const invitationIntent = useRef<(NonNullable<ReturnType<typeof NativeServerSignIn.parseInvitationEntry>> & Partial<Pick<NativeInvitationEntry, 'fingerprint' | 'signIn' | 'expiresAt'>> & {publicPreview?: boolean}) | undefined>(undefined);
  const invitationEpoch = useRef(0);
  const endedTarget = useRef<Readonly<{serverId: string; fingerprint: string}> | undefined>(undefined);
  const identityOrigin = useRef<string | undefined>(undefined);
  const [servers, setServers] = useState<HostedServer[]>([]), [target, setTarget] = useState<HostedServer>(), [retryIn, setRetryIn] = useState(20), [appActive, setAppActive] = useState(AppState.currentState === 'active'), [options, setOptions] = useState<IdentityOptions>(), [transaction, setTransaction] = useState<IdentityTransaction>();
  const [tvView, setTVView] = useState<{opening: TVCodeOpening; state: SetupCodeSnapshot<unknown>}>();
  const tvState: SetupCodeSnapshot<unknown> = tvView?.state ?? {phase: 'idle'};
  const [tvKind, setTVKind] = useState<'local' | 'portico'>('portico'), [recovery, setRecovery] = useState(false), [recoveryCode, setRecoveryCode] = useState('');
  const [requiredSetup, setRequiredSetup] = useState<RequiredSetup>();
  const [directoryList, setDirectoryList] = useState<'idle' | 'loading' | 'ready' | 'failed'>('idle');
  const [memberMove, setMemberMove] = useState<MemberMove>(), [moveSnapshot, setMoveSnapshot] = useState<MemberMoveSnapshot>();
  const memberMoveRef = useRef<MemberMove | undefined>(undefined), memberOperation = useRef<NativeServerSignIn | undefined>(undefined), memberFactorOperation = useRef<NativeServerSignIn | undefined>(undefined);
  const stopMemberMove = useCallback(() => {memberMoveRef.current?.dispose(); memberMoveRef.current = undefined; memberOperation.current = undefined; setMemberMove(undefined); setMoveSnapshot(undefined);}, []);
  useEffect(() => {if (!memberMove) return; const publish = () => setMoveSnapshot(memberMove.getSnapshot()); publish(); return memberMove.subscribe(publish);}, [memberMove]);
  const [pinRecovery, setPinRecovery] = useState<PINRecovery>();
  const [invitationPreview, setInvitationPreview] = useState<PublicInvitation>();
  type InvitationOpening = {source: NativeServerSignIn; active: boolean} & (
    {kind: 'local'; controller: ReturnType<NativeServerSignIn['createLocalInvitation']>}
    | {kind: 'portico'; controller: Awaited<ReturnType<NativeServerSignIn['preparePorticoInvitation']>>});
  const invitationOpening = useRef<InvitationOpening | undefined>(undefined);
  type InvitationSwitch = {entry: NativeInvitationEntry; active: boolean; scope?: Readonly<{accountId: string; familyId: string}>; username?: string};
  const invitationEntryOpening = useRef<{active: boolean; returnAddress?: string; external?: NativeInvitationLink} | undefined>(undefined);
  const invitationScan = useRef<AbortController | undefined>(undefined);
  const [invitationScanning, setInvitationScanning] = useState(false), [scannedInvitationLink, setScannedInvitationLink] = useState<string>();
  const retireInvitationEntry = useCallback(() => {invitationScan.current?.abort(); invitationScan.current = undefined; setInvitationScanning(false); if (invitationEntryOpening.current) invitationEntryOpening.current.active = false; invitationEntryOpening.current = undefined;}, []);
  const invitationSwitch = useRef<InvitationSwitch | undefined>(undefined);
  const retireInvitationSwitch = useCallback(() => {if (invitationSwitch.current) invitationSwitch.current.active = false; invitationSwitch.current = undefined;}, []);
  const retireInvitation = useCallback(() => {const ticket = invitationOpening.current; if (ticket) {ticket.active = false; ticket.controller.dispose(); invitationOpening.current = undefined;} setInvitationPreview(undefined);}, []);

  const [nearby, setNearby] = useState<readonly {origin: string; server: ServerSignIn}[]>([]);
  const [nearbyStatus, setNearbyStatus] = useState<'loading' | 'ready' | 'failed'>('loading'), [nearbyRefresh, setNearbyRefresh] = useState(0);
  const nearbySource = useRef<{active: boolean} | undefined>(undefined);
  const lifetime = useRef(true), directoryAttempt = useRef(0), retryDelay = useRef(20), operation = useRef<NativeServerSignIn | undefined>(undefined), account = useRef<NativePorticoSignIn | undefined>(undefined);
  const pinRecoveryRef = useRef<PINRecovery | undefined>(undefined), profileRef = useRef(profile);
  profileRef.current = profile;
  const retireRecovery = useCallback((expected = pinRecoveryRef.current) => {
    if (!expected || pinRecoveryRef.current !== expected || expected.retired) return false;
    expected.retired = true; pinRecoveryRef.current = undefined; expected.controller.dispose(); setPinRecovery(undefined); return true;
  }, []);
  const targetRef = useRef(target), activeRef = useRef(appActive), retryEpoch = useRef(0), remainingRetry = useRef(20);
  const retryTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const directoryConnectRef = useRef<(value: HostedServer, automatic?: boolean, expectedTarget?: HostedServer) => void>(() => {});
  const stopRetry = useCallback(() => {++retryEpoch.current; if (retryTimer.current !== undefined) clearTimeout(retryTimer.current); retryTimer.current = undefined;}, []);
  const busyRef = useRef(busy), stageRef = useRef(stage), onBackRef = useRef(onBack), backRef = useRef<() => void>(() => {});
  // Exact witness of the drawn name draft, so the provider opening can read the committed draft
  // it was drawn against without reopening its owner on every keystroke. It mirrors state and
  // carries no authority of its own.
  const nameDraft = useRef(name);
  // An opaque revision, advanced by every editable register field (the password
  // included), so a Submit drawn against an older draft cannot send its old values.
  const registerOpeningRef = useRef(registerOpening), providerOpeningRef = useRef(providerOpening), registerDraftRev = useRef(0);
  // The transaction whose provider-suggested name has already been seeded into this draft.
  const providerNameSeed = useRef<string | undefined>(undefined);
  registerOpeningRef.current = registerOpening; providerOpeningRef.current = providerOpening;
  busyRef.current = busy; stageRef.current = stage; onBackRef.current = onBack;
  const tvOpening = useRef<TVCodeOpening | undefined>(undefined);
  const retireTVOpening = useCallback((cancel = false, expected = tvOpening.current, publish = true) => {
    if (!expected) return;
    expected.retire(cancel);
    if (tvOpening.current !== expected) return;
    tvOpening.current = undefined; adoptingTV.current = false;
    if (publish && lifetime.current) setBusy(acting.current);
  }, []);
  const contact = useRef<string | undefined>(undefined), factorKind = useRef<'portico' | 'local'>('local');
  type WorkspaceOpening = {active: boolean; username: string; scope: {accountId: string; familyId: string}; source?: NativeAccountWorkspace; createSource?: () => NativeAccountWorkspace};
  const accountWorkspace = useRef<WorkspaceOpening | undefined>(undefined);
  const retireAccountWorkspace = useCallback(() => {const ticket = accountWorkspace.current; accountWorkspace.current = undefined; if (ticket) {ticket.active = false; ticket.source?.dispose();}}, []);
  type DownloadsOpening = {source: AccountDownloadsEntry; active: boolean};
  const downloadsOwner = useRef<DownloadsOpening | undefined>(undefined), [downloadValue, setDownloadValue] = useState<{opening: DownloadsOpening; available: boolean}>();
  if (downloadsOwner.current?.source !== downloads && downloadsOwner.current) downloadsOwner.current.active = false;
  useLayoutEffect(() => {
    if (!downloads) {setDownloadValue(undefined); return;}
    const opening: DownloadsOpening = {source: downloads, active: true}; downloadsOwner.current = opening;
    const update = () => {if (!opening.active || downloadsOwner.current !== opening) return;
      const available = downloads.current() && downloads.getSnapshot().available; setDownloadValue({opening, available});};
    const off = downloads.subscribe(update); update();
    return () => {opening.active = false; if (downloadsOwner.current === opening) downloadsOwner.current = undefined; off();};
  }, [downloads]);
  const accountScope = useRef<{accountId: string; familyId: string} | undefined>(undefined);
  const acting = useRef(false), adoptingTV = useRef(false);
  useLayoutEffect(() => {
    lifetime.current = true;
    return () => {
      retireTVOpening(false, tvOpening.current, false);
      lifetime.current = false; retireAccountWorkspace(); retireInvitationEntry(); if (invitationIntent.current) operation.current?.dispose(); const switching = invitationSwitch.current; retireInvitationSwitch();
      if (switching) account.current?.dispose();
      const ticket = invitationOpening.current;
      if (ticket) {
        ticket.active = false; invitationOpening.current = undefined;
        // The source preserves an already publishing canonical adoption until its
        // atomic handoff finishes; all retained UI actions retire synchronously.
        ticket.source.dispose();
      }
    };
    // The retirement commands keep one identity for this entry, so this opening still retires
    // only at the unmount commit.
  }, [retireTVOpening, retireAccountWorkspace, retireInvitationEntry, retireInvitationSwitch]);
  // Publish only the public stage before layout observers can collect this commit.
  // Credentials, server/profile data and commands never leave through this port.
  useLayoutEffect(() => {onStageChange?.(stage);}, [onStageChange, stage]);
  // This public browser contact retires during the unmount commit, before passive
  // account cleanup; a late proof cannot navigate after this view has left.
  useLayoutEffect(() => {
    ownerRecoveryMounted.current = true;
    return () => {ownerRecoveryMounted.current = false; retireOwnerRecoveryContact();};
  }, [retireOwnerRecoveryContact]);
  useEffect(() => {lifetime.current = true; return () => {lifetime.current = false; retireInvitation(); memberMoveRef.current?.dispose(); retireOwnerRecoveryContact(); retireOwnerReset(); retireSetupReview(); retireRecovery(); stopRetry(); operation.current?.dispose(); account.current?.dispose();};},
    // Every command here keeps one identity for this entry; the mount and unmount commit alone decide.
    [retireInvitation, retireOwnerRecoveryContact, retireOwnerReset, retireRecovery, retireSetupReview, stopRetry]);
  useLayoutEffect(() => {if (stage !== 'tv') retireTVOpening();}, [stage, retireTVOpening]);
  useLayoutEffect(() => {if (stage !== 'servers') retireOwnerRecoveryContact();}, [stage, retireOwnerRecoveryContact]);
  // Open one signup form per entry to the register stage. Its layout cleanup retires
  // that exact opening before the next effect or the unmount commit finishes, so a
  // late answer cannot mark a form that has already left.
  useLayoutEffect(() => {
    setUsernameUnavailable(false); setPasswordShown(false);
    if (stage !== 'register') {setRegisterOpening(undefined); return;}
    const source = account.current;
    if (!source) {setRegisterOpening(undefined); return;}
    const owner = {live: true};
    setRegisterOpening(Object.freeze({source, owner}));
    return () => {owner.live = false;};
  }, [stage]);
  // One provider completion opening per drawn transaction, carrying the source it was
  // drawn with and its own lifetime; retired synchronously with its stage.
  // The drawn name draft's committed witness. Publishing it in the layout turn means an
  // abandoned render cannot replace the draft the opening reads, and the opening copies it
  // once at its own effect run below.
  useLayoutEffect(() => {nameDraft.current = name;}, [name]);
  useLayoutEffect(() => {
    if (stage !== 'provider' || !transaction) {setProviderOpening(undefined); return;}
    const source = account.current;
    if (!source) {setProviderOpening(undefined); return;}
    const owner = {live: true}, life = new AbortController();
    setProviderOpening(Object.freeze({transaction, source, owner, life}));
    setProviderUsernameUnavailable(false);
    // The phone's first-time provider step considers this transaction once, on its first
    // eligible opening, whether or not a name is suggested. Only an empty draft takes the
    // suggested name, and only an actual seed advances the revision that retires an older
    // Submit; a cleared draft on a later refresh is never seeded over. The draft is read
    // once, from the witness this commit published, so an edit never reopens this provider
    // owner and a later render cannot lend it a newer draft.
    const draft = nameDraft.current;
    if (!Platform.isTV && transaction.needsUsername && providerNameSeed.current !== transaction.transactionId) {
      providerNameSeed.current = transaction.transactionId;
      const suggested = transaction.suggestedName;
      if (suggested && !draft) {
        registerDraftRev.current++;
        setName(suggested);
      }
    }
    return () => {owner.live = false; life.abort();};
  }, [stage, transaction]);
  // One factor opening per phone Portico challenge; its layout cleanup retires and
  // aborts it so a retained handler or late settlement cannot act on a successor form.
  useLayoutEffect(() => {
    if (stage !== 'factor' || factorKind.current !== 'portico' || Platform.isTV) {setFactorOpening(undefined); return;}
    const source = account.current, witness = source?.factorChallengeIdentity();
    if (!source || !witness) {setFactorOpening(undefined); return;}
    const owner = {live: true}, life = new AbortController();
    setFactorOpening(Object.freeze({source, witness, owner, life}));
    return () => {owner.live = false; life.abort();};
  }, [stage]);
  useEffect(() => {const subscription = AppState.addEventListener('change', state => {
    activeRef.current = state === 'active';
    if (!activeRef.current) {
      retireOwnerRecoveryContact(); stopRetry();
      const opening = tvOpening.current;
      if (stageRef.current === 'tv' && opening) {
        retireTVOpening(false, opening); stageRef.current = 'welcome'; setStage('welcome');
      }
    }
    setAppActive(activeRef.current);
  }); return () => subscription.remove();}, [retireOwnerRecoveryContact, retireTVOpening, stopRetry]);
  useLayoutEffect(() => {
    if (stage !== 'address') return;
    const ticket = {active: true}; nearbySource.current = ticket;
    const current = () => ticket.active && nearbySource.current === ticket && stageRef.current === 'address';
    setNearbyStatus('loading');
    const abort = new AbortController(), probes = new Set<ServerSignInProbe>();
    setNearby([]);
    void browseNearby('servers', abort.signal).then(async values => {
      if (!current() || abort.signal.aborted) return;
      const found = values.flatMap(value => {try {
        if (!value || typeof value !== 'object' || !('baseUrl' in value) || typeof value.baseUrl !== 'string') return [];
        if (!('serverId' in value) || typeof value.serverId !== 'string') return [];
        return [{origin: routeOrigin(value.baseUrl), serverId: value.serverId}];
      } catch {return [];}});
      // Discovery supplies addresses, not display names. Ask each server's public system
      // endpoint; never substitute a hostname or make a Portico Services request.
      const unique = found.filter((value, index) => found.findIndex(item => item.serverId === value.serverId) === index);
      for (let index = 0; index < unique.length && current() && !abort.signal.aborted; index += 4) {
        const rows = await Promise.all(unique.slice(index, index + 4).map(async value => {
          if (!current() || abort.signal.aborted) return;
          const probe = new ServerSignInProbe(nativeNetworkFetch, 3000); probes.add(probe);
          try {const descriptor = await probe.inspect(value.origin);
            return descriptor?.id === value.serverId ? {origin: value.origin, server: descriptor} : undefined;
          } finally {probe.dispose(); probes.delete(probe);}
        }));
        if (current() && !abort.signal.aborted) setNearby(previous => [...previous, ...rows.flatMap(value => value ? [value] : [])]);
      }
      if (current() && !abort.signal.aborted) setNearbyStatus('ready');
    }).catch(() => {if (current() && !abort.signal.aborted) setNearbyStatus('failed');});
    return () => {ticket.active = false; abort.abort(); for (const probe of probes) probe.dispose();};
  }, [stage, nearbyRefresh]);
  // The runner's identity carries the exact busy, stage and recovery it drew with; effects that
  // drive it read the port of the commit that drew them rather than a newer render's runner.
  const run = useCallback(async (work: () => Promise<void>) => {
    if (busy || acting.current || adoptingTV.current || tvOpening.current?.pending() || !lifetime.current) return; acting.current = true; setBusy(true); setError(undefined);
    const startedOperation = operation.current, startedInvitationEpoch = invitationEpoch.current;
    try {await work();} catch (failure) {if (lifetime.current && invitationEpoch.current === startedInvitationEpoch) {
      if (failure instanceof NativeSetupEnded) {
        if (!startedOperation || operation.current !== startedOperation || !['setup-choice', 'setup-review', 'setup-hosted'].includes(stageRef.current)) return;
        retireSetupReview(); setPassword(''); setCode(''); setSetupEndedReason(failure.reason); setError(undefined); setStage('setup-ended'); return;
      }
      if (endedTarget.current) {setPassword(''); setCode('');}
      if (failure instanceof MFARequiredError) {factorKind.current = 'portico'; setCode(''); setRecovery(false); setStage('factor');}
      else {
        // TV: the filled boxes stay in the error colour for 400 ms (F-PROFILE-28) before they empty; focus stays.
        if (stage === 'profile-pin' && Platform.isTV || (Platform.isTV && stage === 'factor' && !recovery)) {
          if (wrongCodeTimer.current) clearTimeout(wrongCodeTimer.current);
          wrongCodeTimer.current = setTimeout(() => {wrongCodeTimer.current = undefined; if (lifetime.current && stageRef.current === stage) setCode('');}, 400);
        } else if (stage === 'profile-pin') setCode('');
        setError(failure);
      }
    }} finally {if (invitationEpoch.current === startedInvitationEpoch) {acting.current = false; if (lifetime.current) setBusy(adoptingTV.current || tvOpening.current?.pending() === true);}}
  }, [busy, recovery, retireSetupReview, stage]);
  const reset = useCallback(() => {retireTVOpening(true); retireAccountWorkspace(); retireInvitationEntry(); retireInvitationSwitch(); invitationIntent.current = undefined; retireInvitation(); ownerResetIntent.current = undefined; endedTarget.current = undefined; retireOwnerRecoveryContact(); retireOwnerReset(); retireSetupReview(); setDirectoryList('idle'); memberFactorOperation.current = undefined; stopMemberMove(); retireRecovery(); stopRetry(); targetRef.current = undefined; stageRef.current = initialStage; ++directoryAttempt.current; acting.current = false; preparedAuthSource.current = undefined; operation.current?.dispose(); operation.current = undefined; account.current?.dispose(); account.current = undefined;
    setPassword(''); setSetupReview(undefined); setSetupApprovalURL(undefined); setSetupPasswordConfirm(''); setCode(''); setIdentityCode(''); setIdentityWrong(false); setIdentityAddress(''); identityOrigin.current = undefined; setTarget(undefined); setError(undefined); retryDelay.current = 20; setRetryIn(20); setStage(initialStage);},
  [initialStage, retireAccountWorkspace, retireInvitation, retireInvitationEntry, retireInvitationSwitch, retireOwnerRecoveryContact, retireOwnerReset, retireRecovery, retireSetupReview, stopMemberMove, stopRetry, retireTVOpening]);
  const cancelDirectory = useCallback(() => {retireOwnerRecoveryContact(); retireOwnerReset(); retireSetupReview(); setDirectoryList('idle'); retireRecovery(); stopRetry(); targetRef.current = undefined; ++directoryAttempt.current; operation.current?.dispose(); operation.current = undefined; acting.current = false; setBusy(false); setTarget(undefined); setError(undefined);}, [retireOwnerRecoveryContact, retireOwnerReset, retireRecovery, retireSetupReview, stopRetry]);
  const openAccountWorkspace = (scope: Readonly<{accountId: string; familyId: string}> | undefined, isCurrent: () => boolean) => {
    if (!isCurrent() || busyRef.current || acting.current || adoptingTV.current || !scope) return;
    const session = nativeAccounts.accountCredentials().getSnapshot().session; if (!session) return;
    cancelDirectory(); retireAccountWorkspace();
    const ticket: WorkspaceOpening = {active: true, username: session.account.username, scope: {...scope}}; accountWorkspace.current = ticket;
    stageRef.current = 'account-workspace';
    ticket.createSource = () => {
      if (!ticket.active || accountWorkspace.current !== ticket || !lifetime.current || stageRef.current !== 'account-workspace')
        throw Object.assign(new Error('native_account_changed'), {code: 'account_changed'});
      ticket.source?.dispose();
      return ticket.source = new NativeAccountWorkspace(nativeAccounts, ticket.scope,
        () => ticket.active && accountWorkspace.current === ticket && lifetime.current && stageRef.current === 'account-workspace');
    };
    setStage('account-workspace');
  };
  const openInvitationEntry = useCallback((returnAddress?: string, external?: NativeInvitationLink) => {
    if (!lifetime.current || busyRef.current || acting.current || external && !external.current()) return;
    const externalTarget = external ? NativeServerSignIn.parseInvitationEntry(external.raw, '') : undefined;
    if (external && !externalTarget) return;
    retireInvitationEntry(); retireInvitationSwitch(); retireInvitation(); stopMemberMove(); cancelDirectory(); operation.current = undefined;
    account.current?.dispose(); account.current = undefined; preparedAuthSource.current = undefined;
    invitationIntent.current = undefined; invitationEntryOpening.current = {active: true, returnAddress, external};
    setAddress(externalTarget?.server ?? returnAddress ?? ''); setUsername(''); setCode(''); setPassword(''); setSetupPasswordConfirm(''); setError(undefined); setScannedInvitationLink(undefined); setInvitationScanning(false);
    stageRef.current = 'invitation-entry'; setStage('invitation-entry');
  }, [cancelDirectory, retireInvitation, retireInvitationEntry, retireInvitationSwitch, stopMemberMove]);
  useLayoutEffect(() => {
    if (!incomingInvitation || Platform.isTV || !incomingInvitation.current()) return;
    // Replacement is a fresh entry even when the retired link had an in-flight
    // prepare. Its old run must never clear this entry's pending/error state.
    lifetime.current = true; acting.current = false; busyRef.current = false; setBusy(false);
    openInvitationEntry(undefined, incomingInvitation);
    let active = true;
    const retire = () => {
      if (!active) return; active = false; invitationEpoch.current++;
      // A replacement OS link invalidates the old consumer before React commits
      // its successor, including requests already awaiting server responses.
      lifetime.current = false; retireInvitationEntry(); retireInvitation();
      operation.current?.dispose(); account.current?.dispose();
    };
    const off = incomingInvitation.onRetired(retire);
    if (!incomingInvitation.current()) retire();
    return () => {off(); retire();};
    // The entry command and its retirements keep one identity for this entry, so the link alone reopens it.
  }, [incomingInvitation, openInvitationEntry, retireInvitation, retireInvitationEntry]);
  const back = () => {
    if (stage === 'tv') {if (!adoptingTV.current) reset(); return;}
    if (stage === 'invitation-entry') {
      const external = invitationEntryOpening.current?.external;
      if (external) {if (external.current()) onBack?.(); return;}

      const origin = invitationEntryOpening.current?.returnAddress; retireInvitationEntry(); invitationIntent.current = undefined;
      if (origin) {stageRef.current = 'address'; setStage('address'); void run(async () => {await inspect(origin);});} else reset(); return;
    }
    if (stage === 'invitation-account') {retireInvitationSwitch(); reset(); return;}
    if (stage === 'account-workspace') {retireAccountWorkspace(); stageRef.current = 'servers'; setStage('servers'); void run(directory); return;}
    if (stage === 'invitation-sign-out') {
      const ticket = invitationSwitch.current; if (!ticket?.active) return;
      retireInvitationSwitch(); stageRef.current = 'address'; setStage('address');
      void run(async () => {await inspect(ticket.entry.server);}); return;
    }
    if (stage === 'invitation-code' || stage === 'invitation-local' || stage === 'invitation-portico') {invitationIntent.current = undefined; retireInvitation(); cancelDirectory(); setPassword(''); setCode(''); setAddress(''); stageRef.current = 'address'; setStage('address'); return;}
    if (stage === 'owner-reset' || stage === 'owner-reset-code' || stage === 'owner-reset-done') {reset(); setCode(''); setResetLocation(undefined); setStage('address'); return;}
    if (stage === 'setup-review') {retireSetupReview(); setError(undefined); setStage('setup-choice'); return;}
    if (stage === 'setup-local') {setPassword(''); setSetupPasswordConfirm(''); setError(undefined); setStage('setup-choice'); return;}
    if (stage === 'member-move') {stopMemberMove(); setStage(server?.signIn === 'local' ? 'local' : 'uses-portico'); return;}
    if (pinRecoveryRef.current) {retireRecovery(); setCode(''); return;}
    if (stage === 'servers' && target) {cancelDirectory(); return;}
    if (stage === 'profile-forgot-pin') {setCode(''); setError(undefined); setStage('profile-pin'); return;}
    // Account help returns to the Portico Account form that opened it, not the front door.
    if (stage === 'forgot' || stage === 'forgot-sent') {setPassword(''); setError(undefined); setStage('portico'); return;}
    if (theme.isTV && stage === 'profile-pin') {setCode(''); setProfile(undefined); setError(undefined); setStage('profiles'); return;}
    if (Platform.isTV && (stage === 'tv-credentials' || stage === 'factor' || stage === 'tv-required-setup')) {void run(returnToTVCode); return;}
    if (stage === initialStage) onBack?.(); else reset();
  };
  backRef.current = back;
  useEffect(() => {const listener = BackHandler.addEventListener('hardwareBackPress', () => {
    if (stageRef.current === 'tv' && !adoptingTV.current) {backRef.current(); return true;}
    if (busyRef.current || adoptingTV.current) return true;
    if (stageRef.current === 'welcome' && !onBackRef.current) return false;
    backRef.current(); return true;
  }); return () => listener.remove();}, []);
  useTVMenuCapture(Platform.isTV && (stage !== 'welcome' || !!onBack));
  const consume = useCallback((result: Result) => {
    if (!lifetime.current) return;
    if (result.kind === 'required-setup') {
      if (!Platform.isTV) throw Object.assign(new Error('native_required_account_setup'), {code: 'access_refused'});
      setPassword(''); setCode(''); setRecoveryCode(''); setRecovery(false); setProfiles([]); setProfile(undefined);
      setTransaction(undefined); setOptions(undefined); setName(''); setEmail(''); setMasked('');
      setIdentityCode(''); setIdentityWrong(false); setIdentityAddress(''); identityOrigin.current = undefined;
      accountScope.current = undefined; contact.current = undefined;
      setRequiredSetup(result); setStage('tv-required-setup');
    }
    else if (result.kind === 'profiles') {setServer(operation.current!.serverDescriptor()); setProfiles(result.profiles); setStage('profiles');}
    else if (result.kind === 'challenge') {factorKind.current = 'local'; setCode(''); setRecovery(false); setStage('factor');}
    else {if (stageRef.current === 'setup-local' || stageRef.current === 'setup-hosted') onSetupComplete?.(operation.current!.serverDescriptor().id); onComplete();}
  }, [onComplete, onSetupComplete]);
  useEffect(() => {
    if (stage !== 'member-move' || !memberMove || moveSnapshot?.phase !== 'adopted'
      || moveSnapshot.result?.kind !== 'challenge' || memberOperation.current !== operation.current) return;
    const result = memberMove.takeAcceptedResult();
    if (result?.kind === 'challenge') {memberFactorOperation.current = operation.current; consume(result);}
  }, [stage, memberMove, moveSnapshot, consume]);
  const directory = async () => {
    const handoff = invitationSwitch.current;
    if (handoff?.active && invitationIntent.current === handoff.entry) {
      const credentials = nativeAccounts.accountCredentials(); await credentials.synchronize();
      if (!lifetime.current || invitationSwitch.current !== handoff || !handoff.active || invitationIntent.current !== handoff.entry) return;
      const session = credentials.getSnapshot().session;
      if (!session) return;
      accountScope.current = {accountId: session.account.id, familyId: session.familyId};
      retireInvitationSwitch(); account.current?.dispose(); account.current = undefined; await inspect(handoff.entry.server); return;
    }
    const move = memberMoveRef.current;
    if (move && memberOperation.current === operation.current && move.getSnapshot().phase === 'portico') {
      await move.redeemPortico();
      if (lifetime.current && memberMoveRef.current === move && memberOperation.current === operation.current) setStage('member-move');
      return;
    }
    const attempt = ++directoryAttempt.current;
    const credentials = nativeAccounts.accountCredentials(), session = credentials.getSnapshot().session;
    if (!session) {reset(); return;}
    const expected = accountExpectation(session);
    const current = () => {
      const live = credentials.getSnapshot().session;
      return lifetime.current && attempt === directoryAttempt.current && live?.account.id === expected.accountId && live.familyId === expected.familyId;
    };
    if (!current()) return;
    accountScope.current = {accountId: expected.accountId, familyId: expected.familyId};
    stopRetry(); targetRef.current = undefined; setTarget(undefined); setError(undefined); setPassword('');
    setServers([]); setDirectoryList('loading'); setStage('servers');
    try {
      const out = await nativeAccountRead(credentials, issued => new HttpHostedApi(hostedUrl, issued.accessToken, nativeNetworkFetch).servers(),
        {expected, isCurrent: current});
      if (!current()) return;
      if (!out || !Array.isArray(out.items) || out.items.some(value => !value || typeof value.id !== 'string' || !value.id ||
        typeof value.name !== 'string' || !value.name || typeof value.baseUrl !== 'string' || typeof value.publicKey !== 'string' ||
        !Number.isSafeInteger(value.policyRevision) || value.policyRevision < 0) || new Set(out.items.map(value => value.id)).size !== out.items.length)
        throw Object.assign(new Error('native_directory_invalid'), {code: 'invalid_response'});
      setServers(out.items); setDirectoryList('ready');
    } catch (failure) {
      if (!current()) {
        if (lifetime.current && attempt === directoryAttempt.current) reset();
        return;
      }
      const presentation = presentError(failure, 'portico-account');
      if (presentation.silent) {setDirectoryList('idle'); return;}
      if (['offline', 'unreachable', 'timeout', 'busy', 'unavailable', 'invalid-response', 'rate-limited'].includes(presentation.category)) {
        setDirectoryList('failed'); return;
      }
      setDirectoryList('idle'); throw failure;
    }
  };
  const connectDirectory = (value: HostedServer, automatic = false, expectedTarget = value) => {
    if (busyRef.current || acting.current || adoptingTV.current || !lifetime.current) return;
    if (automatic && (stageRef.current !== 'servers' || targetRef.current !== expectedTarget || !activeRef.current || !servers.includes(value))) return;
    stopRetry(); targetRef.current = value;
    if (!automatic) {retryDelay.current = 20; setRetryIn(20);}
    remainingRetry.current = retryDelay.current;
    const attempt = ++directoryAttempt.current;
    setTarget(value); setError(undefined); acting.current = true; setBusy(true);
    void (async () => {
      let next: NativeServerSignIn | undefined;
      try {
        retireOwnerRecoveryContact(); retireOwnerReset(); retireSetupReview(); retireRecovery(); operation.current?.dispose(); next = operation.current = new NativeServerSignIn();
        const result = await next.connectPortico(value);
        if (!lifetime.current || attempt !== directoryAttempt.current) return;
        if (result.kind !== 'complete') setServer(next.serverDescriptor()); consume(result);
      } catch (failure) {
        if (lifetime.current && attempt === directoryAttempt.current) {
          if (failure instanceof HostedSignInUnavailable) {retryDelay.current = Math.max(retryDelay.current, failure.retryAfterSeconds); remainingRetry.current = retryDelay.current; setRetryIn(remainingRetry.current);}
          setError(failure);
        }
      } finally {
        if (!lifetime.current) acting.current = false;
        else if (attempt === directoryAttempt.current) {acting.current = false; setBusy(false);}
      }
    })();
  };
  directoryConnectRef.current = connectDirectory;
  const chooseAnother = () => cancelDirectory();
  async function openTV<T>(capture: TVCodeCapture<T, NativeServerSignIn | undefined>,
    accept: (grant: T, current: () => boolean) => Promise<(() => void | Promise<void>) | undefined>,
    approval?: Readonly<{watch: (current: () => boolean, retired: () => void) => TVApprovalCredentialOpening; create: (grant: T, current: () => boolean) => TVApprovalFinalization; finish: () => Promise<void>}>) {
    const opening: TVCodeOpening = createTVCodeOpening(capture, {
      owns: value => tvOpening.current === value,
      current: original => lifetime.current && activeRef.current && stageRef.current === 'tv'
        && (original.kind !== 'local' || operation.current === original.source),
      publish: (owner, state) => setTVView({opening: owner, state}),
      busy: (pending, adopting) => {adoptingTV.current = adopting; setBusy(pending || acting.current);},
      error: setError,
      accept: async (original, grant, current) => {
        if (!current()) return;
        const finish = await accept(grant, current);
        if (!current() || !finish) return;
        const completed = await original.service.complete(grant);
        if (!current() || !completed) return;
        retireTVOpening(false, opening);
        await finish();
      },
      watchApproval: approval?.watch,
      approval: approval ? (_original, grant, current) => approval.create(grant, current) : undefined,
      approved: approval ? async () => {retireTVOpening(false, opening); setError(undefined); await approval.finish();} : undefined,
      approvalRetired: () => {
        if (tvOpening.current !== opening || !lifetime.current || stageRef.current !== 'tv') return;
        retireTVOpening(false, opening); stageRef.current = 'welcome'; setStage('welcome');
      },
      replace: async (original, current) => {if (current()) await startTV(original.kind, original.source);},
      credentials: async (original, current) => {await typeTVCredentials(original.kind, current, opening);},
    });
    retireTVOpening();
    tvOpening.current = opening; stageRef.current = 'tv';
    setTVKind(capture.kind); setTVView({opening, state: {phase: 'idle'}}); setError(undefined);
    // The opening, not the caller's generic run(), owns this durable await.
    // Back may leave it while storage is pending without a late run-finally
    // clearing a successor's busy state.
    setStage('tv'); void opening.start();
  }
  const startTV = async (kind: TVCodeKind, originalSource = operation.current) => {
    if (!lifetime.current) return;
    if (kind === 'local') {
      const source = originalSource;
      if (!source || operation.current !== source || !lifetime.current) return;
      return openTV({kind, source, service: source.localTVSignIn()}, async (grant, current) => {
        if (!current()) return;
        const result = await source.acceptLocalTVSession(grant);
        if (current()) return () => consume(result);
      });
    }
    const service = createNativePorticoTVSignIn(), credentials = nativeAccounts.accountCredentials();
    let credentialOpening: TVApprovalCredentialOpening | undefined;
    return openTV({kind, source: undefined, service}, async () => undefined, {
      watch: (current, retired) => credentialOpening = createTVApprovalCredentialOpening(credentials, {current, retired}),
      create: (grant, current) => createTVApprovalFinalization({service, grant, credentials, credentialOpening, current,
        adopt: (original, callerCurrent) => nativeAccounts.adoptPorticoTVSession(original, callerCurrent)}),
      finish: directory,
    });
  };
  const publishPreparedAccount = (source: NativePorticoSignIn, available: IdentityOptions, current: () => boolean) => {
    if (!lifetime.current || account.current !== source || !current()) return false;
    preparedAuthSource.current = source; setOptions(available); return true;
  };
  // Services is opened only by an explicit Portico Account action. The front
  // door and local entry never prepare an account client in an effect.
  const portico = async () => {
    const enteredStage = stage, enteredEpoch = invitationEpoch.current;
    const current = () => lifetime.current && stageRef.current === enteredStage && invitationEpoch.current === enteredEpoch;
    if (!current()) return;
    const credentials = nativeAccounts.accountCredentials(); await credentials.synchronize();
    if (!current()) return;
    if (credentials.getSnapshot().session) return directory();
    if (Platform.isTV) return startTV('portico');
    account.current?.dispose(); preparedAuthSource.current = undefined;
    const next = account.current = new NativePorticoSignIn();
    const available = await next.prepare();
    if (!current()) {
      if (account.current === next) {next.dispose(); account.current = undefined;}
      return;
    }
    if (publishPreparedAccount(next, available, current)) {stageRef.current = 'portico'; setStage('portico');}
  };
  const beginMemberMove = async () => {
    const source = operation.current;
    if (!source || !server || server.move !== 'to_portico' || Platform.isTV) return;
    stopMemberMove();
    const move = await source.createMemberMove('password');
    if (!lifetime.current || operation.current !== source) {move.dispose(); return;}
    memberMoveRef.current = move; memberOperation.current = source;
    setMemberMove(move); setMoveSnapshot(move.getSnapshot()); setStage('member-move');
  };
  const beginMemberCode = async () => {
    const source = operation.current;
    if (!source || !server || server.signIn !== 'local' || server.move !== 'to_local' || Platform.isTV) return;
    stopMemberMove();
    const move = await source.createMemberMove('code');
    if (!lifetime.current || operation.current !== source) {move.dispose(); return;}
    memberMoveRef.current = move; memberOperation.current = source;
    setMemberMove(move); setMoveSnapshot(move.getSnapshot()); setStage('member-move');
  };
  const beginEndedMemberCode = async (openingCurrent: () => boolean, signal: AbortSignal) => {
    const hint = endedSignIn;
    if (Platform.isTV || !hint || hint.signIn !== 'portico' || signal.aborted || !openingCurrent() || !lifetime.current || stageRef.current !== 'ended-sign-in') return;
    retireOwnerRecoveryContact(); retireOwnerReset(); retireSetupReview(); retireRecovery(); operation.current?.dispose();
    const source = new NativeServerSignIn(nativeAccounts, {expectedTarget: {serverId: hint.serverId, fingerprint: hint.fingerprint}});
    operation.current = source;
    const retire = () => source.dispose(); signal.addEventListener('abort', retire, {once: true});
    const current = () => !signal.aborted && openingCurrent() && lifetime.current && stageRef.current === 'ended-sign-in'
      && latestEndedHint.current === hint && operation.current === source;
    let handed = false;
    try {
      if (!current()) return;
      const descriptor = await source.inspect(hint.origin);
      if (!current()) return;
      if (!descriptor || descriptor.id !== hint.serverId || descriptor.signIn !== 'local' || descriptor.move !== 'to_local' || descriptor.setupRequired)
        throw Object.assign(new Error('native_move_identity_changed'), {code: 'identity_mismatch'});
      const pin = await source.inspectLocalIdentity(); if (!current()) return;
      await source.confirmLocal(pin); if (!current()) return;
      source.assertSignInTarget(hint.serverId, hint.fingerprint);
      const move = await source.createMemberMove('code');
      if (!current()) {move.dispose(); return;}
      stopMemberMove(); memberMoveRef.current = move; memberOperation.current = source;
      signal.removeEventListener('abort', retire); handed = true;
      setServer(source.serverDescriptor()); setMemberMove(move); setMoveSnapshot(move.getSnapshot());
      setPassword(''); setCode(''); stageRef.current = 'member-move'; setStage('member-move');
    } finally {
      signal.removeEventListener('abort', retire);
      if (!handed) {source.dispose(); if (operation.current === source) operation.current = undefined;}
    }
  };
  const createMoveAccount = async () => {
    const credentials = nativeAccounts.accountCredentials(); await credentials.synchronize();
    if (!lifetime.current || !memberMoveRef.current) return;
    const existing = credentials.getSnapshot().session;
    if (existing) {accountScope.current = {accountId: existing.account.id, familyId: existing.familyId}; setStage('account-sign-out'); return;}
    const move = memberMoveRef.current;
    preparedAuthSource.current = undefined; account.current?.dispose(); const next = account.current = new NativePorticoSignIn();
    const available = await next.prepare();
    if (publishPreparedAccount(next, available, () => !!move && memberMoveRef.current === move)) setStage('register');
  };
  const differentMoveAccount = async () => {
    const credentials = nativeAccounts.accountCredentials(); await credentials.synchronize();
    if (!lifetime.current || !memberMoveRef.current) return;
    const signedIn = credentials.getSnapshot().session;
    if (signedIn) {accountScope.current = {accountId: signedIn.account.id, familyId: signedIn.familyId}; setStage('account-sign-out');}
    else {memberMoveRef.current.cancel(); setStage('member-move');}
  };
  const requestReset = async () => {
    await account.current!.forgot(username);
    if (lifetime.current) {setPassword(''); setCode(''); setStage('forgot-sent');}
  };
  const inspect = async (origin = address) => {
    const resetIntent = ownerResetIntent.current, invite = invitationIntent.current;
    retireOwnerRecoveryContact(); retireOwnerReset(); retireSetupReview(); retireRecovery(); operation.current?.dispose(); const next = operation.current = new NativeServerSignIn(nativeAccounts, {requiredSetupHandoff: Platform.isTV, expectedTarget: endedTarget.current, expectedInvitation: invite ? {origin: invite.server, serverId: invite.id, fingerprint: invite.fingerprint, signIn: invite.signIn, expiresAt: invite.expiresAt} : undefined});
    const value = await next.inspect(origin); if (!lifetime.current) return;
    if (next.needsIdentityCode()) {
      routeOrigin(origin); identityOrigin.current = origin;
      setIdentityAddress(origin);
      setServer(undefined); setIdentityCode(''); setIdentityWrong(false); setStage('identity-code'); return;
    }
    if (!value) throw Object.assign(new Error('native_sign_in_authority_changed'), {code: 'identity_mismatch'});
    if (resetIntent && (ownerResetIntent.current !== resetIntent || routeOrigin(origin) !== resetIntent.origin || value.id !== resetIntent.serverId || value.signIn !== 'portico' || value.setupRequired))
      throw Object.assign(new Error('native_sign_in_authority_changed'), {code: 'identity_mismatch'});
    setServer(value);
    if (value.setupRequired && !Platform.isTV) {
      setSetupAddress(routeOrigin(origin)); setSetupMode(value.hostedConfigured ? 'hosted' : 'local');
      setName(value.name); setUsername(''); setPassword(''); setSetupPasswordConfirm(''); setStage('setup-confirm'); return;
    }
    if (value.setupRequired) throw Object.assign(new Error('native_setup_phone_required'), {code: 'access_refused'});
    if (value.signIn === 'portico') {
      if (invite?.publicPreview) {
        try {
          const result = await next.previewPorticoInvitation(invite.raw);
          if (!lifetime.current || operation.current !== next || invitationIntent.current !== invite) return;
          invitationIntent.current = result.entry; invitationSwitch.current = {entry: result.entry, active: true};
          setInvitationPreview(result.preview); stageRef.current = 'invitation-account'; setStage('invitation-account'); return;
        } catch (failure) {
          if (lifetime.current && operation.current === next && invitationIntent.current === invite) {
            invitationEntryOpening.current = {active: true}; stageRef.current = 'invitation-entry'; setStage('invitation-entry');
          }
          throw failure;
        }
      }
      if (invite) {
        const controller = await next.preparePorticoInvitation();
        if (!lifetime.current || operation.current !== next || invitationIntent.current !== invite) return;
        const ticket = {source: next, controller, active: true, kind: 'portico' as const}; invitationOpening.current = ticket;
        stageRef.current = 'invitation-code'; setStage('invitation-code');
        const preview = await controller.preview(invite.raw);
        if (!lifetime.current || operation.current !== next || invitationIntent.current !== invite || invitationOpening.current !== ticket || !ticket.active) return;
        invitationIntent.current = undefined; setInvitationPreview(preview); setCode(''); setPassword(''); setSetupPasswordConfirm('');
        stageRef.current = 'invitation-portico'; setStage('invitation-portico'); return;
      }
      if (resetIntent) {
        const ticket = {source: next, controller: new AbortController()}; ownerResetOpening.current = ticket;
        await next.prepareOwnerReset();
        if (lifetime.current && operation.current === next && ownerResetOpening.current === ticket && ownerResetIntent.current === resetIntent && !ticket.controller.signal.aborted) {
          ownerResetIntent.current = undefined; setCode(''); setResetLocation(undefined); setStage('owner-reset');
        }
      } else setStage('uses-portico');
      return;
    }
    await next.confirmLocal(await next.inspectLocalIdentity());
    if (invite) {
      if (!lifetime.current || operation.current !== next || invitationIntent.current !== invite) return;
      const controller = next.createLocalInvitation(), ticket = {source: next, controller, active: true, kind: 'local' as const}; invitationOpening.current = ticket;
      stageRef.current = 'invitation-code'; setStage('invitation-code');
      const preview = await controller.preview(invite.raw);
      if (!lifetime.current || operation.current !== next || invitationIntent.current !== invite || invitationOpening.current !== ticket || !ticket.active) return;
      invitationIntent.current = undefined; setInvitationPreview(preview); setCode(''); setPassword(''); setSetupPasswordConfirm(''); setUsername(''); setName('');
      stageRef.current = 'invitation-local'; setStage('invitation-local'); return;
    }
    if (lifetime.current) {setServer(next.serverDescriptor()); if (Platform.isTV) await startTV('local'); else setStage('local');}
    return next;
  };
  const confirmCode = async (value = identityCode) => {
    const matched = await operation.current!.confirmIdentityCode(value);
    if (!lifetime.current) return;
    if (!matched) {setIdentityWrong(true); return;}
    const capturedOrigin = identityOrigin.current; if (!capturedOrigin) return;
    setIdentityCode(''); setIdentityWrong(false); await inspect(capturedOrigin);
  };
  const choose = async (id: string, enteredPin?: string) => {await operation.current!.chooseProfile(id, enteredPin); onComplete();};
  const typeTVCredentials = async (kind: TVCodeKind, current: () => boolean, opening: TVCodeOpening) => {
    if (!current()) return;
    if (kind === 'portico') {
      preparedAuthSource.current = undefined;
      account.current?.dispose(); const next = account.current = new NativePorticoSignIn();
      const available = await next.prepare();
      if (!current() || account.current !== next) {
        if (account.current === next) {next.dispose(); account.current = undefined; preparedAuthSource.current = undefined;}
        return;
      }
      preparedAuthSource.current = next; setOptions(available);
    }
    if (!current()) return;
    retireTVOpening(false, opening); stageRef.current = 'tv-credentials';
    setPassword(''); setCode(''); setRecoveryCode(''); setRecovery(false); setStage('tv-credentials');
  };
  const returnToTVCode = async () => {
    account.current?.dispose(); account.current = undefined; preparedAuthSource.current = undefined;
    setPassword(''); setCode(''); setRecoveryCode(''); setRecovery(false); setRequiredSetup(undefined);
    if (tvKind === 'local') await inspect(address); else await startTV('portico');
  };
  const submitTVFactor = async (value: string) => {
    if (factorKind.current === 'portico') {await account.current!.factor(value); await directory();}
    else consume(await (operation.current!.directoryChallengePending() ? operation.current!.completeDirectorySecondFactor(value) : operation.current!.completeSecondFactor(value)));
  };
  const field = (label: string, value: string, change: (next: string) => void, secure = false, credential?: 'username' | 'password' | 'new-password' | 'email', invalid?: string, help?: string, placeholder?: string, oneTime = false) => <TextField key={label} label={label} value={value} onChangeText={change}
    appearance={theme.platform === 'phone' ? 'auth' : undefined} secureTextEntry={secure && (credential !== 'new-password' || !passwordShown)}
    keyboardType={oneTime && !theme.isTV ? 'number-pad' : credential === 'email' ? 'email-address' : undefined} inputMode={oneTime && !theme.isTV ? 'numeric' : undefined}
    textContentType={oneTime && !theme.isTV ? 'oneTimeCode' : credential === 'new-password' ? 'newPassword' : credential === 'email' ? 'emailAddress' : credential}
    autoComplete={oneTime && !theme.isTV ? 'one-time-code' : credential === 'password' ? 'current-password' : credential}
    autoCapitalize="none" autoCorrect={false} editable={!busy} error={invalid} description={help} placeholder={placeholder}
    trailing={credential === 'new-password' && label === t('auth.password') && !theme.isTV ? <Button testID="accounts-password-visibility" variant="text" size="compact" iconOnly icon={passwordShown ? 'i-eyeoff' : 'i-eye'} iconSize={18}
      accessibilityLabel={t(passwordShown ? 'accounts.signIn.hidePassword' : 'accounts.signIn.showPassword')} accessibilityState={{selected: passwordShown}}
      disabled={busy} style={{height: 40, width: 40, paddingHorizontal: 0}} onPress={() => {if (lifetime.current && stageRef.current === stage && !busyRef.current && !acting.current) setPasswordShown(value => !value);}}/> : undefined}/>;
  const codeField = (label: string, value: string, change: (next: string) => void) => field(label, value, change, false, undefined, undefined, undefined, undefined, true);
  const fields = (...children: React.ReactNode[]) => <View style={{alignSelf: 'stretch', width: theme.isTV ? 720 : undefined, gap: 14, marginBottom: 20}}>{children}</View>;
  const action = (id: string, label: string, work: () => Promise<void>, disabled = false) => ({id, label, disabled: busy || disabled, loading: busy, onPress: () => {void run(work);}});
  const renderedTV = stage === 'tv' ? tvView?.opening : undefined;
  const codeBack = () => {if (renderedTV?.current()) back();};
  const backAction = stage !== 'welcome' || onBack ? {id: 'back', label: t('action.back'), disabled: busy, onPress: stage === 'tv' ? codeBack : back} : undefined;
  const failure = error ? presentError(error, stage === 'profile-pin' ? 'profiles' : 'sign-in') : undefined;
  const status = failure && !failure.silent ? <Text accessibilityRole="alert" style={[theme.type.caption, {color: theme.colors.danger, marginBottom: 16}]}>{failure.body}</Text> : null;
  const directoryView = stage === 'servers' ? directoryFailure(target, error) : 'other';
  const hostedWaiting = stage === 'servers' && !!target && error instanceof HostedSignInUnavailable;
  const directoryScope = accountScope.current;
  useLayoutEffect(() => {
    if (stage !== 'servers' || !directoryScope) return;
    const credentials = nativeAccounts.accountCredentials(); let retired = false;
    const changed = () => {
      if (retired || !lifetime.current || stageRef.current !== 'servers' || accountScope.current !== directoryScope) return;
      const live = credentials.getSnapshot().session;
      if (live?.account.id === directoryScope.accountId && live.familyId === directoryScope.familyId) return;
      retired = true; reset(); // retire this view; canonical owner alone ends credentials
    };
    const off = credentials.subscribe(changed); changed(); return off;
    // reset keeps one identity for this entry, so the stage and scope alone reopen this view.
  }, [stage, directoryScope, reset]);
  const offlineRows = stage === 'servers' && directoryList === 'ready' ? directoryRows(servers) : [];
  const allDirectoryOffline = offlineRows.length > 0 && offlineRows.every(row => row.offline);
  useEffect(() => {
    if (!allDirectoryOffline || !lifetime.current || stageRef.current !== 'servers' || !directoryScope) return;
    const live = nativeAccounts.accountCredentials().getSnapshot().session;
    if (allDirectoryOffline && lifetime.current && stageRef.current === 'servers' && !target && !targetRef.current && !busy && !busyRef.current
      && !acting.current && !adoptingTV.current && appActive && activeRef.current && servers[0] && directoryScope
      && accountScope.current === directoryScope && live?.account.id === directoryScope.accountId && live.familyId === directoryScope.familyId)
      directoryConnectRef.current(servers[0]);
  }, [allDirectoryOffline, target, busy, appActive, servers, directoryScope]);
  useEffect(() => {
    if (stage !== 'servers' || !target || (directoryView !== 'waiting' && !(allDirectoryOffline && directoryView === 'unreachable') && !hostedWaiting) || busy || !appActive) return;
    const attempt = directoryAttempt.current, epoch = ++retryEpoch.current, capturedTarget = target, capturedScope = directoryScope;
    const current = () => lifetime.current && retryEpoch.current === epoch && directoryAttempt.current === attempt
      && stageRef.current === 'servers' && targetRef.current === capturedTarget && activeRef.current
      && !busyRef.current && !acting.current && !adoptingTV.current && capturedScope !== undefined && accountScope.current === capturedScope
      && nativeAccounts.accountCredentials().getSnapshot().session?.account.id === capturedScope.accountId
      && nativeAccounts.accountCredentials().getSnapshot().session?.familyId === capturedScope.familyId;
    const tick = () => {
      if (!current()) return;
      const seconds = remainingRetry.current;
      setRetryIn(seconds);
      if (seconds <= 0) {
        retryDelay.current = nextRetryDelay(retryDelay.current);
        const index = servers.indexOf(capturedTarget);
        const next = allDirectoryOffline && !hostedWaiting && index >= 0 ? servers[(index + 1) % servers.length] : capturedTarget;
        directoryConnectRef.current(next ?? capturedTarget, true, capturedTarget); return;
      }
      retryTimer.current = setTimeout(() => {if (!current()) return; remainingRetry.current -= 1; tick();}, 1000);
    };
    tick();
    const stopOwnedRetry = () => {if (retryEpoch.current === epoch) stopRetry();};
    return stopOwnedRetry;
  }, [stage, target, directoryView, hostedWaiting, busy, appActive, allDirectoryOffline, servers, directoryScope, stopRetry]);
  const setupOpeningSource = operation.current;
  useLayoutEffect(() => {
    const source = setupOpeningSource;
    if (stage !== 'setup-hosted' || !source) {setSetupHostedOpening(undefined); return;}
    const owner = {live: true}, life = new AbortController();
    setSetupHostedOpening(Object.freeze({source, owner, life, url: setupApprovalURL}));
    return () => {owner.live = false; life.abort();};
  }, [stage, server?.id, setupApprovalURL, setupOpeningSource]);
  const setupOpeningCurrent = useCallback((opening: typeof setupHostedOpening) => !!opening && opening.owner.live && !opening.life.signal.aborted
    && lifetime.current && stageRef.current === 'setup-hosted' && operation.current === opening.source, []);
  const finishSetupOpening = useCallback(async (opening: typeof setupHostedOpening) => {
    if (!opening || !setupOpeningCurrent(opening)) return;
    const current = () => setupOpeningCurrent(opening);
    try {
      const result = await opening.source.finishHostedSetup({signal: opening.life.signal, isCurrent: current});
      if (result && current()) consume(result);
    } catch (error) {if (current()) throw error;}
  }, [consume, setupOpeningCurrent]);
  // The automatic resume keeps the original trigger set (this stage, app activity, the drawn
  // opening) and drives the completion through the ports published by the commit that drew it:
  // the same run() and its captured busy/stage/recovery guards, the same completion and currency
  // check the Continue button draws with. The ports are published in the layout turn, never
  // during render, so an abandoned render cannot replace them; the cleanup clears only the port
  // it published, so it can never clear a successor's. The effect below copies the committed
  // port once at its own run and the async completion reads only that copy.
  useLayoutEffect(() => {
    const ports: SetupResumePorts | undefined = setupHostedOpening
      ? {opening: setupHostedOpening, run, finish: finishSetupOpening, current: setupOpeningCurrent} : undefined;
    setupResume.current = ports;
    return () => {if (setupResume.current === ports) setupResume.current = undefined;};
  }, [setupHostedOpening, run, finishSetupOpening, setupOpeningCurrent]);
  useEffect(() => {
    const ports = setupResume.current;
    if (stage === 'setup-hosted') resumeSetupHosted(setupHostedOpening, ports, appActive);
  }, [stage, appActive, setupHostedOpening]);
  const FormLayout = theme.platform === 'phone' ? PhoneAccountForm : StateLayout;
  // Pickers and directory/recovery states keep their authored centred composition.
  const EntryLayout = ['profiles', 'profile-pin', 'servers', 'account-sign-out', 'tv'].includes(stage) ? StateLayout : FormLayout;
  const formStep = theme.platform === 'phone' && stage.startsWith('setup-')
    ? <Text style={[theme.type.captionStrong, {fontSize: 13.5 * theme.textScale, color: theme.colors.textSecondary}]}>{t('accounts.setup.title')}</Text> : undefined;
  const layout = (title: string, body?: string, before?: React.ReactNode, primary?: ReturnType<typeof action>, more?: React.ReactNode) => <EntryLayout testID="accounts-entry" title={title} body={body} kicker={formStep} beforeActions={<>{before}{status}</>} primaryAction={primary} secondaryAction={backAction} avoidKeyboard={stage === 'profile-pin'}>{more}</EntryLayout>;
  // O-159: a server this app can't talk to is refused before any credential, by name.
  const refusal = negotiationRefusal(error);
  if (refusal) return <ServerNeedsUpdate refusal={refusal} onBack={() => {setError(undefined); reset();}}/>;
  const openLegalLink = (url: string) => {void run(async () => {await Linking.openURL(url);});};
  const legalFooter = <LegalAgreement style={[theme.type.caption, {fontSize: 12 * theme.textScale, color: theme.colors.textTertiary, textAlign: 'center', marginTop: 'auto', paddingTop: 24}]}
    linkStyle={{color: theme.colors.accentStrong}} onOpenLink={openLegalLink}/>;
  if (Platform.isTV && stage === 'tv-required-setup' && requiredSetup) return <TVRequiredSetupHandoff serverName={requiredSetup.serverName}
    requirement={requiredSetup.requirement} url={new URL('/settings/profile', requiredSetup.serverOrigin).href} busy={busy}
    copy={{body: requiredSetup.requirement === 'password' ? 'next.auth.tv.requiredPasswordBody' : 'next.auth.tv.requiredTwoStepBody', reference: 'next.auth.tv.handoffHint'}}
    onSignInAgain={() => {if (!busy) {setRequiredSetup(undefined); setError(undefined); setStage('tv-credentials');}}} onBack={back}/>;
  if (Platform.isTV && stage === 'tv-credentials') return <TVCredentialForm {...(tvKind === 'local'
    ? {kind: 'local' as const, serverName: server!.name}
    : {kind: 'portico' as const, usernamePlaceholder: 'auth.usernameOrEmailPlaceholder' as const})}
    username={username} password={password} onUsernameChange={setUsername} onPasswordChange={setPassword}
    busy={busy} error={status} onBack={back} onSubmit={() => {void run(async () => {
      if (tvKind === 'portico') {await account.current!.password(username, password); setPassword(''); await directory();}
      else {const result = await operation.current!.signInLocal(username, password); setPassword(''); consume(result);}
    });}}/>;
  if (Platform.isTV && stage === 'factor') return <TVVerificationForm kind={factorKind.current} code={code} onCodeChange={setCode}
    recovery={recovery} recoveryCode={recoveryCode} onRecoveryCodeChange={setRecoveryCode} onRecoveryChange={value => {setRecovery(value); setCode(''); setRecoveryCode(''); setError(undefined);}}
    busy={busy} error={status} onSubmit={value => {void run(() => submitTVFactor(value));}} onBack={back}/>;
  if (stage === 'ended-sign-in' && endedSignIn) return layout(endedSignIn.serverName, t('web.signIn.ended', {server: endedSignIn.serverName}),
    endedSignIn.signIn === 'local' ? fields(field(t('accounts.signIn.address'), address, setAddress), field(t('auth.username'), username, setUsername, false, 'username'),
      field(t('auth.password'), password, setPassword, true, 'password')) : undefined,
    endedSignIn.signIn === 'local' ? action('sign-in-again', t('auth.signIn'), async () => {
      if (!lifetime.current || stageRef.current !== 'ended-sign-in') return;
      endedTarget.current = Object.freeze({serverId: endedSignIn.serverId, fingerprint: endedSignIn.fingerprint});
      const source = await inspect(address);
      if (!source || !lifetime.current || operation.current !== source) return;
      source.assertSignInTarget(endedSignIn.serverId, endedSignIn.fingerprint);
      const result = await source.signInLocal(username, password);
      if (lifetime.current && operation.current === source) {setPassword(''); consume(result);}
    }, !address.trim() || !username.trim() || !password) : action('portico-again', t('accounts.signIn.continuePortico'), portico),
    <View style={{gap: 12}}>{!Platform.isTV && endedSignIn.signIn === 'portico' && <EndedMoveCodeAction scope={endedSignIn} busy={busy}
      isCurrent={() => lifetime.current && stageRef.current === 'ended-sign-in' && latestEndedHint.current === endedSignIn}
      onStart={(guard, signal) => run(() => beginEndedMemberCode(guard, signal))}/>}
      {endedSignIn.signIn === 'local' && <Button variant="text" disabled={busy} onPress={() => {
      if (lifetime.current && stageRef.current === 'ended-sign-in') void run(async () => {await Linking.openURL(new URL('/sign-in', endedSignIn.origin).href);});
    }}>{t('auth.forgotPassword')}</Button>}
      <Button variant="text" disabled={busy} onPress={() => {if (lifetime.current && stageRef.current === 'ended-sign-in') {setUsername(''); setPassword(''); reset(); onBack?.();}}}>{t('auth.useDifferentAccount')}</Button>
    </View>);
  if (stage === 'account-workspace' && accountWorkspace.current?.createSource) {
    const ticket = accountWorkspace.current;
    const current = () => ticket.active && accountWorkspace.current === ticket && lifetime.current && stageRef.current === 'account-workspace';
    const leave = (destination: 'servers' | 'address' | 'account-sign-out' | 'invitation-entry') => {
      if (!current()) return; const live = nativeAccounts.accountCredentials().getSnapshot().session;
      const valid = live?.account.id === ticket.scope.accountId && live.familyId === ticket.scope.familyId; retireAccountWorkspace();
      if (!valid) {reset(); return;}
      if (destination === 'invitation-entry') {openInvitationEntry(); return;}
      setPassword(''); setCode(''); setError(undefined);
      stageRef.current = destination; setStage(destination);
      if (destination === 'servers') void run(directory);
      if (destination === 'address') setAddress('');
    };
    return <PhoneAccountWorkspace createSource={ticket.createSource!} username={ticket.username}
      onDone={() => leave('servers')} onJoin={() => leave('invitation-entry')} onFind={() => leave('address')} onSignOut={() => leave('account-sign-out')}/>;
  }
  if (stage === 'welcome' && theme.isTV) return <TVFrontDoor busy={busy} error={status}
    onPortico={() => {void run(portico);}} onLocal={() => {if (!busy) setStage('address');}}/>;
  const authOptions = account.current && preparedAuthSource.current === account.current ? options : undefined;
  const availableProviders = authOptions?.providers.filter(value => value.enabled
    && (NativeModules.PorticoAccountAuth.canOpenBrowser || value.id === 'apple' && value.native
      && value.nativeClientIds.includes(NativeModules.PorticoAccountAuth.bundleId) && NativeModules.PorticoAccountAuth.canSignInApple)) ?? [];
  // Keep the same native ancestry and loading title when callback settlement
  // hands off to directory loading. No signed-out form or second loading mount
  // belongs between them. Decision forms (username/contact/MFA) remain distinct.
  if ((stage === 'portico' && providerLoading && providerLoading === account.current)
    || (stage === 'servers' && directoryList === 'loading'))
    return <StateLayout testID="accounts-server-list-loading" title={t('web.chooser.loading')}/>;
  if (stage === 'welcome' && theme.platform === 'phone') return <PhoneFrontDoor busy={busy} error={status}
    onInvitation={() => {if (lifetime.current && stageRef.current === 'welcome') openInvitationEntry();}}
    onPortico={() => {if (lifetime.current && stageRef.current === 'welcome') void run(portico);}}
    onLocal={() => {if (lifetime.current && stageRef.current === 'welcome' && !busyRef.current && !acting.current) {stageRef.current = 'address'; setStage('address');}}}/>;
  if (stage === 'welcome') return <StateLayout testID="accounts-welcome" title={t('web.signin.welcomeHome')} beforeActions={status}
    primaryAction={action('portico', t('accounts.signIn.portico'), portico)} secondaryAction={{id: 'local', label: t('accounts.signIn.local'), onPress: () => {setStage('address');}}}
    detailsAction={backAction}/>;
  if (stage === 'setup-confirm') {
    const source = operation.current;
    const current = () => !!source && operation.current === source && lifetime.current && stageRef.current === 'setup-confirm';
    return layout(t('accounts.setup.confirmTitle'), t('accounts.setup.confirmBody'),
      <View style={{alignSelf: 'stretch', gap: 12, marginBottom: 18}}>
        <Text style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{server!.name}</Text>
        <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{new URL(setupAddress).host}</Text>
        <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{t('accounts.setup.confirmWarning')}</Text>
      </View>, action('confirm-setup', t('accounts.setup.confirmAction'), async () => {
        if (!current() || !source) return;
        const checked = await source.prepareSetup();
        if (current()) {setServer(checked); setSetupAddress(source.serverOrigin()); setName(checked.name); setStage('setup-choice');}
      }));
  }
  if (stage === 'setup-choice') {
    const source = operation.current;
    const current = () => !!source && operation.current === source && lifetime.current && stageRef.current === 'setup-choice';
    return layout(t('accounts.setup.title'), t('accounts.setup.at', {address: new URL(setupAddress).host}),
      fields(field(t('web.general.serverName'), name, setName), <Text key="name-help" style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{t('web.setup.serverNameHelp')}</Text>,
        <Text key="sign-in-choice" style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{t('web.setup.howSignIn')}</Text>,
        <View key="modes" style={{gap: 12}}>{(['hosted', 'local'] as const).filter(mode => mode === 'local' || server?.hostedConfigured).map(mode =>
          <Focusable key={mode} accessibilityRole="radio" accessibilityState={{checked: setupMode === mode}} disabled={busy}
            onPress={() => {if (current()) setSetupMode(mode);}} style={{padding: 14, borderWidth: 1, borderRadius: 14, borderColor: setupMode === mode ? theme.colors.accentStrong : theme.colors.line}}>
            <Text style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{t(mode === 'hosted' ? 'accounts.settings.portico' : 'web.setup.localSignIn')}{mode === 'hosted' ? ' · ' + t('web.setup.recommended') : ''}</Text>
            <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{t(mode === 'hosted' ? 'web.setup.porticoBody' : 'web.setup.localSignInBody')}</Text>
          </Focusable>)}</View>),
      action('setup-continue', t('action.continue'), async () => {
        if (!current() || !source) return;
        if (setupMode === 'local') {setStage('setup-local'); return;}
        const url = await source.prepareHostedSetup(name); if (!current()) return;
        setSetupApprovalURL(url);
        const credentials = nativeAccounts.accountCredentials(); await credentials.synchronize(); if (!current()) return;
        if (url && credentials.getSnapshot().session) {
          retireSetupReview();
          const ticket = {source, controller: new AbortController(), review: undefined as SetupReview | undefined}; setupReviewTicket.current = ticket;
          const live = () => lifetime.current && setupReviewTicket.current === ticket && operation.current === source && !ticket.controller.signal.aborted;
          const review = await source.reviewHostedSetup(url, {signal: ticket.controller.signal, current: live,
            canApprove: () => live() && stageRef.current === 'setup-review' && ticket.review === setupReviewTicket.current?.review});
          if (!live()) {review.approval.dispose(); return;}
          ticket.review = review;
          if (review.display.approved) {retireSetupReview(); setStage('setup-hosted');} else {setSetupReview(review); setStage('setup-review');}
        } else {setStage('setup-hosted'); if (url) await Linking.openURL(url);}
      }, !name.trim()), <View style={{gap: 10}}>
        <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{t(setupMode === 'hosted' ? 'accounts.setup.nextHosted' : 'accounts.setup.nextLocal')}</Text>
        <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{t('accounts.setup.changeLater')}</Text>
      </View>);
  }
  if (stage === 'setup-review' && setupReview) {
    const ticket = setupReviewTicket.current;
    const current = () => !!ticket && setupReviewTicket.current === ticket && ticket.review === setupReview
      && operation.current === ticket.source && stageRef.current === 'setup-review' && !ticket.controller.signal.aborted && lifetime.current;
    return <PhoneSetupApproval display={setupReview.display} busy={busy} error={status}
      onClose={() => {if (current()) {retireSetupReview(); setError(undefined); setStage('setup-choice');}}}
      onApprove={() => {if (!current()) return; void run(async () => {
        if (!current()) return;
        try {await setupReview.approval.approve();} catch (error) {if (current()) throw error; return;}
        if (current()) {retireSetupReview(); setStage('setup-hosted');}
      });}}/>;
  }
  if (stage === 'setup-ended') {
    const source = operation.current;
    const current = () => !!source && operation.current === source && lifetime.current && stageRef.current === 'setup-ended';
    const restart = async (local: boolean) => {
      if (!current() || !source) return;
      await source.restartEndedSetup();
      if (current()) {setSetupApprovalURL(undefined); setSetupMode(local ? 'local' : 'hosted'); setStage(local ? 'setup-local' : 'setup-choice');}
    };
    return layout(t('accounts.setup.declinedTitle', {server: server!.name}), t(setupEndedReason === 'declined' ? 'accounts.setup.declinedBody' : 'accounts.setup.endedBody'),
      undefined, action('restart-setup', t('accounts.setup.startAgain'), () => restart(false)),
      <Button variant="glass" disabled={busy} onPress={() => {if (current()) void run(() => restart(true));}}>{t('accounts.setup.useLocalInstead')}</Button>);
  }
  if (stage === 'setup-hosted') return layout(t('web.claim.title', {server: server!.name}), t('accounts.setup.waiting'), undefined,
    action('setup-resume', t('action.continue'), () => finishSetupOpening(setupHostedOpening)),
    setupHostedOpening?.url && <Button variant="glass" disabled={busy} onPress={() => {const opening = setupHostedOpening;if (!opening || !setupOpeningCurrent(opening) || !opening.url) return;void run(async () => {if (setupOpeningCurrent(opening)) await Linking.openURL(opening.url!);});}}>{t('accounts.signIn.continuePortico')}</Button>);
  if (stage === 'setup-local') return layout(t('web.setup.ownerTitle'), t('web.setup.localSignInBody'),
    fields(field(t('auth.username'), username, setUsername, false, 'username'), field(t('auth.password'), password, setPassword, true, 'new-password'),
      <PasswordStrengthMeter key="strength" password={password}/>,
      field(t('accounts.signIn.confirmPassword'), setupPasswordConfirm, setSetupPasswordConfirm, true, 'new-password'),
      <Text key="recovery" style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{t('accounts.setup.localRecovery')}</Text>),
    action('create-owner', t('web.setup.createOwner'), async () => {
      const result = await operation.current!.setupLocal(name, username, password);
      setPassword(''); setSetupPasswordConfirm(''); consume(result);
    }, !name.trim() || !/^[A-Za-z0-9_.-]{3,64}$/.test(username.trim()) || !passwordStrength(password).acceptable || password !== setupPasswordConfirm));
  if (stage === 'address') {
    const ticket = nearbySource.current;
    const current = () => !!ticket?.active && nearbySource.current === ticket && lifetime.current && stageRef.current === 'address';
    return layout(t('accounts.signIn.local'), undefined, fields(field(t('accounts.signIn.address'), address, setAddress)), action('inspect', t('action.continue'), async () => {await inspect();}, !address.trim()),
      <View style={{alignSelf: 'stretch', gap: 10, marginTop: 18}}>
        <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{t('playOn.onThisNetwork')}</Text>
        {nearbyStatus === 'loading' && <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{t('status.loading')}</Text>}
        {nearbyStatus === 'failed' && <Text accessibilityRole="alert" style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{t('connection.discovery.failed')}</Text>}
        {nearbyStatus === 'ready' && nearby.length === 0 && <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{t('connection.discovery.empty')}</Text>}
        {nearby.map(value => <View key={value.origin} style={{gap: 4}}><Button variant="glass" disabled={busy} onPress={() => {
          if (!current() || busyRef.current) return;
          setAddress(value.origin); void run(async () => {await inspect(value.origin);});
        }}>{value.server.name}{value.server.setupRequired ? ' · ' + t('accounts.setup.confirmAction') : ''}</Button>
          <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{new URL(value.origin).host}</Text></View>)}
        {nearbyStatus !== 'loading' && <Button variant="text" disabled={busy} onPress={() => {
          if (!current() || busyRef.current) return; ticket!.active = false; setNearbyRefresh(value => value + 1);
        }}>{t('web.chooser.tryAgain')}</Button>}
      </View>);
  }
  if (stage === 'owner-reset' || stage === 'owner-reset-code' || stage === 'owner-reset-done') {
    const ticket = ownerResetOpening.current, source = ticket?.source, renderedStage = stage;
    const current = () => !!ticket && ownerResetOpening.current === ticket && operation.current === source
      && !ticket.controller.signal.aborted && lifetime.current && stageRef.current === renderedStage;
    const actionRun = (work: () => Promise<void>) => {if (!current()) return; void run(async () => {
      if (!current()) return;
      try {await work();} catch (error) {if (current()) throw error;}
    });};
    return <PhoneOwnerReset phase={stage === 'owner-reset' ? 'intro' : stage === 'owner-reset-code' ? 'code' : 'done'} serverName={server!.name}
      location={resetLocation} code={code} onCodeChange={value => {if (current()) setCode(value);}} busy={busy} error={status}
      onBack={() => {if (current()) back();}}
      onWrite={() => actionRun(async () => {
        if (!current() || !source || renderedStage !== 'owner-reset') return;
        const location = await source.requestOwnerReset();
        if (current()) {setResetLocation(location); setCode(''); setStage('owner-reset-code');}
      })}
      onVerify={() => actionRun(async () => {
        if (!current() || !source || renderedStage !== 'owner-reset-code') return;
        await source.verifyOwnerReset(code);
        if (current()) {setCode(''); setStage('owner-reset-done');}
      })}
      onContinue={() => actionRun(async () => {
        if (!current() || !source || renderedStage !== 'owner-reset-done') return;
        const accepted = await source.acceptOwnerReset();
        if (current()) consume(accepted);
      })}/>;
  }
  if (stage === 'uses-portico') {
    const source = operation.current;
    const current = () => !!source && operation.current === source && lifetime.current && stageRef.current === 'uses-portico';
    // During a move to Portico Accounts the notice says so before the old sign-in is offered (P2-66).
    // The days left come from the server's own move window (P2-66, Justin's copy).
    const moveDays = server?.move === 'to_portico' ? moveOfferDays(server.moveEndsAt) : undefined;
    const notice = server?.move === 'to_portico' ? t('accounts.move.toPortico', {server: server.name}) + ' ' + t('accounts.signIn.porticoNotice') : t('accounts.signIn.porticoNotice');
    return layout(t('accounts.signIn.usesPortico', {server: server!.name}), notice, undefined, action('portico', t('accounts.signIn.continuePortico'), portico),
      !Platform.isTV && <View style={{gap: 12}}>
        {moveDays !== undefined && <Button variant="text" disabled={busy} onPress={() => {void run(beginMemberMove);}}>{t('accounts.move.oldPasswordAction', {days: moveDays})}</Button>}
        <Button variant="text" disabled={busy} onPress={() => {
          if (!current() || !source) return;
          retireOwnerReset();
          const ticket = {source, controller: new AbortController()}; ownerResetOpening.current = ticket;
          const openingCurrent = () => current() && ownerResetOpening.current === ticket && !ticket.controller.signal.aborted;
          void run(async () => {
            if (!openingCurrent()) return;
            try {
              await source.prepareOwnerReset();
              if (openingCurrent()) {setCode(''); setResetLocation(undefined); setStage('owner-reset');}
            } catch (error) {if (openingCurrent()) {retireOwnerReset(); throw error;}}
          });
        }}>{t('accounts.signIn.cannotUsePortico')}</Button>
      </View>);
  }
  if (stage === 'member-move' && memberMove && moveSnapshot && memberOperation.current === operation.current) {
    if (moveSnapshot.phase === 'code' || moveSnapshot.phase === 'local') {
      const captured = memberMove, source = memberOperation.current;
      const current = () => !!source && lifetime.current && stageRef.current === 'member-move' && memberMoveRef.current === captured
        && memberOperation.current === source && operation.current === source;
      return <PhoneMoveCode controller={captured} isCurrent={current} onBack={back}/>;
    }
    if (moveSnapshot.phase === 'password') return <PhoneMovePassword controller={memberMove} serverName={server!.name} onBack={back}/>;
    if (moveSnapshot.phase === 'portico') return <PhoneAccountMoveNotice busy={busy || moveSnapshot.busy} state={{kind: 'to-portico', serverName: server!.name,
      onContinue: () => {void run(portico);}, onCreateAccount: () => {void run(createMoveAccount);}}}/>;
    if (moveSnapshot.phase === 'collision' && moveSnapshot.collision) return <PhoneAccountMoveNotice busy={busy || moveSnapshot.busy}
      state={{kind: 'collision', details: moveSnapshot.collision, onDifferentAccount: () => {void run(differentMoveAccount);}, onBack: back}}/>;
    if (moveSnapshot.phase === 'adopted' && moveSnapshot.result && moveSnapshot.result.kind !== 'challenge') return <PhoneAccountMoveNotice state={{kind: 'linked', onChooseProfile: () => {
      const result = memberMove.takeAcceptedResult(); if (!result || memberOperation.current !== operation.current) return; stopMemberMove(); consume(result);
    }}}/>;
  }
  if (stage === 'identity-code' && Platform.isTV) return <TVIdentityCodeForm server={identityAddress} code={identityCode} wrong={identityWrong} busy={busy}
    onChange={value => {setIdentityCode(value); setIdentityWrong(false);}} onSubmit={value => {void run(() => confirmCode(value));}}/>;
  if (stage === 'identity-code') return <FormLayout testID="accounts-identity-code" title={t('connection.identity.checkTitle', {server: identityAddress})} body={t('connection.identity.checkBody')}
    kicker={Platform.isTV ? undefined : <View><Text style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{identityAddress}</Text></View>}
    beforeActions={<>{fields(<TextField appearance={theme.platform === 'phone' ? 'auth' : undefined} label={t('connection.identity.codeLabel')} value={identityCode} autoCapitalize="characters" autoCorrect={false} editable={!busy}
      error={identityWrong ? t('connection.identity.codeWrong') : undefined} onChangeText={value => {
        const raw = value.toUpperCase().replace(/[\s-]/g, '').slice(0, 12), shown = raw.match(/.{1,4}/g)?.join('-') ?? '';
        setIdentityCode(shown); setIdentityWrong(false); if (Platform.isTV && raw.length === 12) void run(() => confirmCode(shown));
      }}/>) }{status}</>}
    primaryAction={Platform.isTV ? undefined : action('check-code', t('action.continue'), () => confirmCode(), identityCode.replace(/-/g, '').length !== 12)}
    secondaryAction={backAction} help={t('connection.identity.checkHome')}/>;
  if (stage === 'invitation-entry') {
    const ticket = invitationEntryOpening.current, external = ticket?.external;
    const parsed = external ? external.current() ? NativeServerSignIn.parseInvitationEntry(external.raw, address) : undefined
      : scannedInvitationLink ? NativeServerSignIn.parseInvitationEntry(scannedInvitationLink, address)
        : NativeServerSignIn.parseShortInvitationEntry(code, address);
    const current = () => !!ticket?.active && invitationEntryOpening.current === ticket && lifetime.current && stageRef.current === 'invitation-entry' && (!external || external.current());
    const change = (set: (value: string) => void, format = false) => (value: string) => {
      if (!current() || busyRef.current || external) return;
      ticket!.active = false; invitationEntryOpening.current = {active: true, returnAddress: ticket!.returnAddress};
      setScannedInvitationLink(undefined);
      set(format ? NativeServerSignIn.formatInvitationCode(value) : value); setError(undefined);
    };
    const scan = async () => {
      if (!current() || busyRef.current || external || invitationScan.current || !qrScannerAvailable()) return;
      const controller = new AbortController();
      invitationScan.current = controller; setInvitationScanning(true); setError(undefined);
      const guard = () => current() && invitationScan.current === controller && !controller.signal.aborted;
      try {
        const raw = await scanQRCode({signal: controller.signal, isCurrent: guard, title: t('accounts.move.scanQR'), cancelLabel: t('action.cancel')});
        if (!guard() || raw === undefined) return;
        const link = parseJoinLink(raw), short = link ? undefined : parseShortInvitationCode(raw);
        if (!link && !short) {setError(Object.assign(new Error('invitation_qr_invalid'), {code: 'invitation_qr_invalid'})); return;}
        // A scan only edits this form; Continue still runs the canonical preview.
        ticket!.active = false; invitationEntryOpening.current = {active: true, returnAddress: ticket!.returnAddress};
        if (link) {setAddress(link.server); setScannedInvitationLink(raw); setCode('');}
        else {setCode(formatShortInvitationCode(short)!); setScannedInvitationLink(undefined);}
        setError(undefined);
      } catch (failure) {if (guard()) setError(failure);}
      finally {if (invitationScan.current === controller) {invitationScan.current = undefined; if (lifetime.current) setInvitationScanning(false);}}
    };
    const submit = () => {if (!current() || busyRef.current || !parsed) return;
      ticket!.active = false; const intent = Object.freeze({...parsed, publicPreview: true}); invitationIntent.current = intent;
      void run(async () => {try {await inspect(intent.server);} catch (failure) {
        if (lifetime.current && invitationIntent.current === intent && stageRef.current === 'invitation-entry') invitationEntryOpening.current = {active: true, returnAddress: ticket!.returnAddress, external};
        throw failure;
      }});
    };
    const rootStatus = NativeServerSignIn.invitationUnavailable(error)
      ? <Text accessibilityRole="alert" style={[theme.type.caption, {color: theme.colors.danger}]}>{t('web.directJoin.entryUnavailable')}</Text> : status;
    return <FormLayout testID="accounts-invitation-entry" title={t('web.directJoin.title')} body={external ? undefined : t('web.directJoin.entryBody')}
      kicker={<Image source={require('../../../assets/wordmark.png')} accessibilityLabel={t('accounts.brand')} resizeMode="contain" style={{width: 74, height: 17}}/>}
      beforeActions={<View style={{alignSelf: 'stretch', gap: 14, marginBottom: 18}}>{rootStatus}
        <TextField appearance={theme.platform === 'phone' ? 'auth' : undefined} label={t('accounts.signIn.address')} value={address} onChangeText={change(setAddress)} autoCapitalize="none" autoCorrect={false} editable={!busy && !external && !scannedInvitationLink}/>
        {!external && !scannedInvitationLink && <TextField appearance={theme.platform === 'phone' ? 'auth' : undefined} label={t('web.directJoin.code')} placeholder={t('web.directJoin.codePlaceholder')} value={code} onChangeText={change(setCode, true)}
          autoCapitalize="characters" autoCorrect={false} autoComplete="off" editable={!busy} onSubmitEditing={submit}/>}
        {!external && qrScannerAvailable() ? <View style={{gap: 6}}>
          <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{t('services.start.qrPhone')}</Text>
          <Button variant="text" disabled={busy || invitationScanning} loading={invitationScanning || undefined} onPress={() => {void scan();}}>{t('accounts.move.scanQR')}</Button>
        </View> : null}
      </View>} primaryAction={{id: 'preview-invitation-entry', label: t('action.continue'), disabled: busy || !parsed, loading: busy, onPress: submit}}
      secondaryAction={{id: 'back', label: t('action.back'), disabled: busy, onPress: () => {if (current()) back();}}}/>;
  }
  if (stage === 'invitation-account') {
    const ticket = invitationSwitch.current, preview = invitationPreview;
    const current = () => !!ticket?.active && invitationSwitch.current === ticket && invitationIntent.current === ticket.entry
      && lifetime.current && stageRef.current === 'invitation-account';
    const begin = (register: boolean) => {if (!current() || busyRef.current) return; void run(async () => {
      if (!current()) return; const credentials = nativeAccounts.accountCredentials(); await credentials.synchronize();
      if (!current()) return;
      if (credentials.getSnapshot().session) {await directory(); return;}
      preparedAuthSource.current = undefined; account.current?.dispose(); const source = account.current = new NativePorticoSignIn(() => !!ticket?.active
        && invitationSwitch.current === ticket && invitationIntent.current === ticket.entry && lifetime.current);
      const available = await source.prepare(); if (!current() || account.current !== source) return;
      if (!publishPreparedAccount(source, available, current)) return;
      stageRef.current = register && available.registration ? 'register' : 'portico'; setStage(stageRef.current);
    });};
    return <FormLayout testID="accounts-invitation-account" title={t('accounts.invitation.to') + ' ' + (preview?.serverName ?? '')}
      body={preview ? t('accounts.invitation.porticoBody', {server: preview.serverName}) : undefined}
      beforeActions={<View style={{gap: 12, alignSelf: 'stretch', marginBottom: 18}}>{status}
        {preview && <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{preview.role === 'admin' ? t('accounts.invitation.admin') : t('accounts.invitation.member')}</Text>}
        {preview?.allLibraries && <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{t('server.people.allLibraries')}</Text>}
        {preview?.libraries.map((library, index) => <Text key={index} style={[theme.type.body, {color: theme.colors.textSecondary}]}>{library.name}</Text>)}
      </View>} primaryAction={{id: 'sign-in-to-join', label: t('auth.signInWithAccount'), disabled: busy || !preview, loading: busy, onPress: () => begin(false)}}
      secondaryAction={{id: 'create-account-to-join', label: t('auth.createAccount'), disabled: busy || !preview, onPress: () => begin(true)}}
      detailsAction={{id: 'back', label: t('action.back'), disabled: busy, onPress: () => {if (current()) back();}}}/>;
  }
  if (stage === 'invitation-sign-out') {
    const ticket = invitationSwitch.current;
    const current = () => !!ticket?.active && invitationSwitch.current === ticket && invitationIntent.current === ticket.entry
      && !!ticket.scope && lifetime.current && stageRef.current === 'invitation-sign-out';
    const cancel = () => {if (!current() || busyRef.current) return; retireInvitationSwitch(); stageRef.current = 'address'; setStage('address'); void run(async () => {await inspect(ticket!.entry.server);});};
    return <FormLayout testID="accounts-invitation-sign-out" title={t('accounts.signOut.portico')} body={ticket?.username}
      beforeActions={status} primaryAction={{id: 'switch-invitation-account', label: t('action.signOut'), disabled: busy, loading: busy, onPress: () => {
        if (!current()) return; void run(async () => {
          if (!current()) return; await nativeAccounts.signOutPorticoAccount(ticket!.scope!);
          if (!current()) return;
          setUsername(''); setPassword(''); setCode('');
          preparedAuthSource.current = undefined; account.current?.dispose(); const source = account.current = new NativePorticoSignIn(() => !!ticket?.active
            && invitationSwitch.current === ticket && invitationIntent.current === ticket.entry && lifetime.current);
          const available = await source.prepare();
          if (!current() || account.current !== source) return;
          if (!publishPreparedAccount(source, available, current)) return;
          stageRef.current = 'portico'; setStage('portico');
        });
      }}} secondaryAction={{id: 'cancel', label: t('action.cancel'), disabled: busy, onPress: cancel}}/>;
  }
  if (stage === 'invitation-code' || stage === 'invitation-local' || stage === 'invitation-portico') {
    const ticket = invitationOpening.current, renderedStage = stage, preview = invitationPreview;
    const current = () => !!ticket?.active && invitationOpening.current === ticket && operation.current === ticket.source
      && lifetime.current && stageRef.current === renderedStage;
    const change = (set: (value: string) => void) => (value: string) => {if (current()) set(value);};
    const back = () => {if (current() && !busyRef.current) backRef.current();};
    const failure = ticket?.controller.failure(error);
    const invitationStatus = failure === 'unavailable'
      ? <Text accessibilityRole="alert" style={[theme.type.caption, {color: theme.colors.danger, marginBottom: 16}]}>{t('web.directJoin.entryUnavailable')}</Text> : status;
    const differentAccount = () => {
      if (!current() || busyRef.current || ticket?.kind !== 'portico') return;
      try {
        const captured = ticket.controller.captureAccountSwitch(), handoff = {...captured, active: true};
        retireInvitation(); ticket.source.dispose(); operation.current = undefined;
        invitationIntent.current = captured.entry; invitationSwitch.current = handoff; setError(undefined); setPassword(''); setCode('');
        stageRef.current = 'invitation-sign-out'; setStage('invitation-sign-out');
      } catch (failure) {if (current()) setError(failure);}
    };

    if (failure === 'expired') return <StateLayout testID="accounts-invitation-expired" icon="i-warn" title={t('accounts.invitation.expiredTitle')} body={t('accounts.invitation.expiredBody')}
      primaryAction={{id: 'back', label: t('action.back'), disabled: busy, onPress: back}}/>;
    if (stage === 'invitation-code') return <FormLayout testID="accounts-invitation-code" title={t('web.directJoin.title')}
      body={t('services.start.joinBody')} beforeActions={<>{invitationStatus}<TextField appearance={theme.platform === 'phone' ? 'auth' : undefined} label={t('services.start.invitation')} value={code}
        onChangeText={change(setCode)} autoCapitalize="none" autoCorrect={false} autoComplete="off" editable={!busy}/></>}
      primaryAction={{id: 'preview-invitation', label: t('action.continue'), disabled: busy || !code.trim(), loading: busy, onPress: () => {
        if (!current()) return; void run(async () => {
          if (!current()) return; const value = await ticket!.controller.preview(code);
          if (!current()) return; invitationIntent.current = undefined; setInvitationPreview(value); setCode(''); const destination = ticket!.kind === 'portico' ? 'invitation-portico' : 'invitation-local'; stageRef.current = destination; setStage(destination);
        });
      }}} secondaryAction={{id: 'back', label: t('action.back'), disabled: busy, onPress: back}}/>;
    if (failure === 'other-account') return <StateLayout testID="accounts-invitation-other-account" icon="i-person"
      title={t('accounts.invitation.otherAccountTitle')} body={t('web.signIn.error.invitationOtherAccount', {server: preview?.serverName ?? server!.name})}
      primaryAction={{id: 'different-account', label: t('accounts.invitation.notYou'), disabled: busy, onPress: differentAccount}}
      secondaryAction={{id: 'back', label: t('action.back'), disabled: busy, onPress: back}}/>;
    if (!preview) return null;
    if (stage === 'invitation-portico' && ticket?.kind === 'portico') {
      const controller = ticket.controller;
      return <FormLayout testID="accounts-portico-invitation" title={t('accounts.invitation.to') + ' ' + preview.serverName}
        body={t('accounts.invitation.porticoBody', {server: preview.serverName})}
        beforeActions={<View style={{alignSelf: 'stretch', gap: 12, marginBottom: 18}}>{invitationStatus}
          <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{preview.role === 'admin' ? t('accounts.invitation.admin') : t('accounts.invitation.member')}</Text>
          <Text style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{t('accounts.invitation.libraries')}</Text>
          {preview.allLibraries && <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{t('server.people.allLibraries')}</Text>}
          {preview.libraries.map((library, index) => <Text key={index} style={[theme.type.body, {color: theme.colors.textSecondary}]}>{library.name}</Text>)}
          <Text style={[theme.type.body, {color: theme.colors.text}]}>{controller.username}</Text>
          <Button variant="text" disabled={busy} onPress={differentAccount}>{t('accounts.invitation.notYou')}</Button>
        </View>}
        primaryAction={{id: 'join-portico-invitation', label: t('accounts.invitation.joinAs', {username: controller.username}), disabled: busy, loading: busy,
          onPress: () => {if (!current()) return; void run(async () => {
            if (!current()) return; const result = await controller.join(preview);
            if (current()) consume(result);
          });}}} secondaryAction={{id: 'back', label: t('action.back'), disabled: busy, onPress: back}}/>;
    }
    if (ticket?.kind !== 'local') return null;
    const localController = ticket.controller;
    return <FormLayout testID="accounts-local-invitation" title={t('accounts.invitation.to') + ' ' + preview.serverName}
      body={t('accounts.invitation.localPhoneBody', {server: preview.serverName})}
      beforeActions={<View style={{alignSelf: 'stretch', gap: 12, marginBottom: 18}}>{invitationStatus}
        <Text style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{t('accounts.invitation.libraries')}</Text>
        {preview.allLibraries && <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{t('server.people.allLibraries')}</Text>}
        {preview.libraries.map((library, index) => <Text key={index} style={[theme.type.body, {color: theme.colors.textSecondary}]}>{library.name}</Text>)}
        <TextField appearance={theme.platform === 'phone' ? 'auth' : undefined} label={t('web.directJoin.name')} value={name} onChangeText={change(setName)} editable={!busy}/>
        <TextField appearance={theme.platform === 'phone' ? 'auth' : undefined} label={t('web.directJoin.username')} value={username} error={failure === 'username' ? t('web.directJoin.error.taken') : undefined} onChangeText={change(setUsername)} autoCapitalize="none" autoCorrect={false} editable={!busy}/>
        <TextField appearance={theme.platform === 'phone' ? 'auth' : undefined} label={t('auth.password')} value={password} onChangeText={change(setPassword)} secureTextEntry autoComplete="new-password" editable={!busy}/>
        <TextField appearance={theme.platform === 'phone' ? 'auth' : undefined} label={t('web.directJoin.confirm')} value={setupPasswordConfirm} onChangeText={change(setSetupPasswordConfirm)} secureTextEntry autoComplete="new-password" editable={!busy}/>
      </View>}
      primaryAction={{id: 'join-local-invitation', label: t('web.directJoin.submit'), loading: busy,
        disabled: busy || !/^[A-Za-z0-9_.-]{3,64}$/.test(username.trim()) || !passwordStrength(password).acceptable || password !== setupPasswordConfirm,
        onPress: () => {if (!current()) return; void run(async () => {
          if (!current()) return; const result = await localController.join(preview, username, password, name);
          if (!current()) return; setPassword(''); setSetupPasswordConfirm(''); consume(result);
        });}}} secondaryAction={{id: 'back', label: t('action.back'), disabled: busy, onPress: back}}/>;
  }
  if (stage === 'local') {
    const source = operation.current;
    return layout(server!.name, t('accounts.signIn.localBody'), fields(field(t('auth.username'), username, setUsername, false, 'username'), field(t('auth.password'), password, setPassword, true, 'password')),
    action('local-sign-in', t('auth.signIn'), async () => {consume(await operation.current!.signInLocal(username, password)); setPassword('');}, !username || !password),
    <View style={{alignSelf: 'stretch', gap: 8}}>
      <Button variant="text" disabled={busy} onPress={() => {
        if (!source || source !== operation.current || !lifetime.current || stageRef.current !== 'local') return;
        void run(async () => {if (source === operation.current && lifetime.current && stageRef.current === 'local') await Linking.openURL(new URL('/sign-in', source.serverOrigin()).href);});
      }}>{t('auth.forgotPassword')}</Button>
      <Button variant="text" disabled={busy} onPress={() => {void run(() => startTV('local'));}}>{t('auth.useCodeFromAnotherDevice')}</Button>
      {!Platform.isTV && !endedTarget.current && <Button variant="text" disabled={busy} onPress={() => {
        if (!source || source !== operation.current || !lifetime.current || stageRef.current !== 'local' || busyRef.current) return;
        try {openInvitationEntry(source.serverOrigin());} catch (failure) {setError(failure);}
      }}>{t('web.directJoin.have')}</Button>}
      {!Platform.isTV && server?.move === 'to_local' && <Button variant="text" disabled={busy} onPress={() => {void run(beginMemberCode);}}>{t('accounts.move.haveCode')}</Button>}
    </View>);
  }
  if (stage === 'factor' && factorKind.current === 'portico' && !Platform.isTV) {
    // The phone Portico factor form acts only through its own opening: the exact issued
    // witness, source, challenge and the draft revision it renders.
    const opening = factorOpening, revision = registerDraftRev.current, entered = recovery ? recoveryCode : code;
    // Settlement only needs this original form, source and draft; admission additionally
    // needs the exact challenge, which a successful factor clears before it resolves.
    const formLive = () => lifetime.current && stageRef.current === 'factor' && !!opening && opening.owner.live
      && account.current === opening.source && registerDraftRev.current === revision;
    const edit = (apply: (next: string) => void) => (next: string) => {registerDraftRev.current++; apply(next);};
    return layout(t('auth.enterVerificationCode'), t('services.signin.twoStep'),
      fields(recovery ? field(t('services.signin.recovery'), entered, edit(setRecoveryCode)) : codeField(t('auth.verificationCode'), entered, edit(setCode))),
      action('factor', t('action.continue'), async () => {
        const source = opening?.source;
        if (!opening || !source || !entered || !formLive() || source.factorChallengeIdentity() !== opening.witness) return;
        try {
          await source.factor(entered, {signal: opening.life.signal, isCurrent: formLive}, opening.witness);
        } catch (refusal) {
          // A retired form publishes nothing; only the live original may surface a refusal.
          if (!formLive()) return;
          throw refusal;
        }
        // Only this own accepted completion settles and navigates.
        if (!formLive()) return;
        setCode(''); setRecoveryCode(''); setRecovery(false);
        await directory();
      }, !entered),
      recovery ? undefined : <Button variant="text" disabled={busy} onPress={() => {if (!formLive()) return; registerDraftRev.current++; setRecoveryCode(''); setRecovery(true);}}>{`${t('services.signin.lostPhone')} ${t('services.signin.recovery')}`}</Button>);
  }
  if (stage === 'factor') return layout(t('auth.enterVerificationCode'), undefined, fields(codeField(t('auth.verificationCode'), code, setCode)), action('factor', t('action.continue'), async () => {
    if (memberFactorOperation.current && memberFactorOperation.current === operation.current) {
      const result = await memberFactorOperation.current.completeMemberMoveFactor(code); memberFactorOperation.current = undefined; stopMemberMove(); consume(result);
    } else consume(await (operation.current!.directoryChallengePending() ? operation.current!.completeDirectorySecondFactor(code) : operation.current!.completeSecondFactor(code)));
    setCode('');
  }, !code));
  if (stage === 'portico' && !theme.isTV) {
    const source = account.current, ticket = invitationSwitch.current;
    const current = () => !!source && lifetime.current && stageRef.current === 'portico' && account.current === source
      && preparedAuthSource.current === source && invitationSwitch.current === ticket
      && (!ticket || (ticket.active && invitationIntent.current === ticket.entry));
    return <PhoneAccountSignIn busy={busy} error={status} onOpenLink={openLegalLink} providers={availableProviders}
      passwordEnabled={!!authOptions?.password} registration={!!authOptions?.registration} recovery={authOptions?.recovery !== false}
      username={username} password={password} onUsernameChange={value => {if (current()) setUsername(value);}} onPasswordChange={value => {if (current()) setPassword(value);}}
      onProvider={id => {if (!current() || busy || !availableProviders.some(value => value.id === id)) return; void run(async () => {
        if (!current() || !source) return;
        setProviderLoading(source);
        try {
          const tx = await source.provider(id, current); if (!current()) return; setTransaction(tx);
          if (tx.status === 'verified' && !tx.needsUsername && !tx.mfaRequired) {
            const caller = new AbortController(); await source.finishProvider(undefined, undefined, undefined, {signal: caller.signal, isCurrent: current});
            if (current()) await directory();
          } else if (current()) setStage('provider');
        } catch (error) {if (current()) throw error;}
        finally {if (lifetime.current) setProviderLoading(value => value === source ? undefined : value);}
      });}}
      onContinue={() => {if (!current() || busy || !authOptions?.password || !username || !password || !source) return; void run(async () => {
        if (!current()) return; try {await source.password(username, password, current); if (current()) await directory();} catch (error) {if (current()) throw error;}
      });}}
      onForgot={() => {if (!current() || busy || !authOptions?.recovery) return; setPassword(''); setStage('forgot');}}
      onCreate={() => {if (!current() || busy || !authOptions?.registration) return; setPassword(''); setStage('register');}}
      onLocal={() => {if (!current() || busy) return; setPassword(''); setStage('address');}}/>;
  }
  if (stage === 'portico') return layout(t('accounts.signIn.portico'), t('auth.accountLead'), options?.password ? fields(field(t('auth.usernameOrEmail'), username, setUsername, false, 'username'), field(t('auth.password'), password, setPassword, true, 'password')) : undefined,
    options?.password ? action('account-password', t('auth.signIn'), async () => {await account.current!.password(username, password); await directory();}, !username || !password) : undefined,
    <View style={{alignSelf: 'stretch', gap: 10, marginTop: 10}}>{options?.providers.filter(value => value.enabled && (NativeModules.PorticoAccountAuth.canOpenBrowser || value.id === 'apple' && value.native && value.nativeClientIds.includes(NativeModules.PorticoAccountAuth.bundleId) && NativeModules.PorticoAccountAuth.canSignInApple)).map(value => <Button key={value.id} variant="glass" disabled={busy} onPress={() => {void run(async () => {
      const tx = await account.current!.provider(value.id); setTransaction(tx);
      if (tx.status === 'verified' && !tx.needsUsername && !tx.mfaRequired) {await account.current!.finishProvider(); await directory();} else setStage('provider');
    });}}>{t(value.id === 'google' ? 'auth.signInWithGoogle' : 'auth.signInWithApple')}</Button>)}
    {options?.registration && <Button variant="text" disabled={busy} onPress={() => {setPassword(''); setStage('register');}}>{t('auth.createAccount')}</Button>}
    {options?.recovery !== false && <Button variant="text" disabled={busy} onPress={() => {setPassword(''); setStage('forgot');}}>{t('auth.forgotPassword')}</Button>}
    <Button variant="text" disabled={busy} onPress={() => {setPassword(''); setStage('address');}}>{t('accounts.signIn.local')}</Button>
    <LegalAgreement style={[theme.type.caption, {color: theme.colors.textSecondary, textAlign: 'center', marginTop: 6}]}
      linkStyle={{color: theme.colors.accent, textDecorationLine: 'underline'}} onOpenLink={openLegalLink}/></View>);
  if (stage === 'forgot') return layout(t('auth.forgotPasswordTitle'), t('auth.resetLinkLede'),
    fields(field(t('auth.usernameOrEmail'), username, setUsername, false, 'username')),
    action('request-reset', t('auth.resetLinkAction'), requestReset, !username.trim()),
    <View style={{alignSelf: 'stretch', gap: 4, marginTop: 4}}>
      <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{t('auth.privacySameAnswer')}</Text>
      {/* collections 31 draws both privacy sentences on the phone's forgot screen. */}
      {theme.isTV ? null : <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{t('auth.noPasswordForSocial')}</Text>}
    </View>);
  if (stage === 'forgot-sent') return layout(t('auth.checkEmail'), t('auth.resetLinkSent'),
    <>
      {/* account 10 and 31 draw the link's lifetime under the lede, as web shows it. */}
      <Text style={[theme.type.caption, {color: theme.colors.textSecondary, marginBottom: 8}]}>{t('services.recovery.expiry')}</Text>
      <Text style={[theme.type.caption, {color: theme.colors.textSecondary, marginBottom: 16}]}>{t('auth.privacySameAnswer')}</Text>
    </>,
    action('back-to-sign-in', t('auth.backToSignIn'), async () => {if (lifetime.current) setStage('portico');}),
    <Text style={[theme.type.caption, {color: theme.colors.textSecondary, marginTop: 10}]}>{t('auth.noPasswordForSocial')}</Text>);
  if (stage === 'register') {
    // The opening this Submit was drawn for and the draft revision it drew. Both must
    // still be the live ones before the call and again when the answer arrives.
    const opening = registerOpening, revision = registerDraftRev.current, submitted = {username, name, email};
    const live = (source: NativePorticoSignIn) => lifetime.current && stageRef.current === 'register' && !!opening
      && opening.owner.live && registerOpeningRef.current === opening && opening.source === source && account.current === source
      && registerDraftRev.current === revision;
    const edit = (apply: (next: string) => void) => (next: string) => {registerDraftRev.current++; apply(next);};
    const usernameField = field(t('auth.username'), username, next => {setUsernameUnavailable(false); edit(setUsername)(next);}, false, 'username',
      !theme.isTV && usernameUnavailable ? t('web.signIn.error.accountTaken') : undefined, theme.isTV ? undefined : t('auth.usernameHelp'));
    const nameField = theme.isTV ? field(t('auth.yourName'), name, edit(setName))
      : field(`${t('auth.yourName')} · ${t('field.optional')}`, name, edit(setName), false, undefined, undefined, undefined, t('services.signup.sameUsername'));
    const emailField = field(t('auth.email'), email, edit(setEmail), false, 'email');
    return layout(t('auth.createYourAccount'), theme.isTV ? undefined : t('auth.oneAccount'), fields(
      ...(theme.isTV ? [usernameField, nameField, emailField] : [emailField, usernameField, nameField]),
      field(t('auth.password'), password, edit(setPassword), true, 'new-password'), !theme.isTV && <PasswordStrengthMeter key="strength" password={password}/>),
      action('register', t(theme.isTV ? 'auth.createAccount' : 'action.continue'), async () => {
        const source = opening?.source;
        if (!source || !live(source)) return;
        try {
          const value = await source.register(submitted.username, theme.isTV ? submitted.name : submitted.name.trim() || submitted.username.trim(), password, submitted.email);
          if (!live(source)) return;
          setVerifyOpening(Object.freeze({source, registration: value})); setPassword(''); setMasked(value.maskedAddress); setStage('verify');
        } catch (refusal) {
          if (!live(source)) return;
          // The drawn username-field state is only the confirmed public-handle collision;
          // every other refusal keeps the ordinary form error path unchanged.
          if (!theme.isTV && porticoUsernameUnavailable(refusal)) {setUsernameUnavailable(true); return;}
          throw refusal;
        }
      }, theme.isTV ? (!username || !name || !password || !email) : (!username || !password || !email)), theme.isTV ? undefined : legalFooter);
  }
  if (stage === 'verify') {
    const opening = verifyOpening, revision = registerDraftRev.current;
    const live = () => !!opening && lifetime.current && stageRef.current === 'verify' && verifyOpeningRef.current === opening
      && account.current === opening.source && registerDraftRev.current === revision;
    const verified = opening?.registration.contactVerified === true;
    const edit = (apply: (next: string) => void) => (next: string) => {if (!live()) return; registerDraftRev.current++; apply(next);};
    return layout(verified ? t('auth.chooseUsername') : t('auth.checkEmail'), verified ? undefined : t('auth.codeSentTo', {address: masked}),
      verified ? fields(field(t('auth.username'), username, next => {if (!live()) return; setUsernameUnavailable(false); edit(setUsername)(next);}, false, undefined,
        usernameUnavailable ? t('web.signIn.error.accountTaken') : undefined, t('auth.usernameHelp')),
        field(`${t('auth.yourName')} · ${t('field.optional')}`, name, edit(setName), false, undefined, undefined, undefined, t('services.signup.sameUsername')))
        : fields(codeField(t('auth.verificationCode'), code, edit(setCode))),
      action('verify', t(theme.isTV || verified ? 'action.continue' : 'auth.createAccount'), async () => {
        if (!opening || !live()) return;
        try {
          await opening.source.verifyRegistration(verified ? '' : code, opening.registration, {isCurrent: live}, verified ? {username, displayName: name} : undefined);
          if (live()) await directory();
        } catch (refusal) {
          if (!live()) return;
          const fresh = porticoUsernameUnavailable(refusal) ? opening.source.registrationAfterCollision(opening.registration) : undefined;
          if (fresh) {setVerifyOpening(Object.freeze({source: opening.source, registration: fresh})); setUsernameUnavailable(true); return;}
          throw refusal;
        }
      }, verified ? username.trim().length < 3 : !code), verified || theme.isTV ? undefined : <View style={{alignSelf: 'stretch', gap: 10, marginTop: 14}}>
        <Button testID="accounts-registration-resend" variant="text" disabled={busy} style={{alignSelf: 'center'}} onPress={() => {
          if (!opening || !live() || busyRef.current || acting.current) return;
          void run(async () => {
            if (!live()) return;
            try {
              const fresh = await opening.source.resendRegistration(opening.registration, {isCurrent: live});
              if (!live()) return;
              setVerifyOpening(Object.freeze({source: opening.source, registration: fresh})); setCode(''); setMasked(fresh.maskedAddress);
            } catch (failure) {if (live()) throw failure;}
          });
        }}>{t('auth.sendCodeAgain')}</Button>
        <Text style={[theme.type.caption, {color: theme.colors.textTertiary, textAlign: 'center'}]}>{t('services.signup.codeHelp')}</Text>
      </View>);
  }
  if (stage === 'provider') {
    // The transaction this form was drawn for and the draft it shows; both must still
    // be live before any completion or proof refresh and again after the answer.
    const opening = providerOpening, tx = transaction, revision = registerDraftRev.current;
    // The exact source is part of the opening, so a retained form can never borrow a
    // successor source; the predicate and its signal travel into the completion port.
    const live = () => lifetime.current && stageRef.current === 'provider' && !!opening && !!tx
      && opening.owner.live && providerOpeningRef.current === opening && opening.transaction === tx
      && account.current === opening.source && registerDraftRev.current === revision;
    const edit = (apply: (next: string) => void) => (next: string) => {registerDraftRev.current++; apply(next);};
    // The drawn first-time provider step names itself and the provider it is continuing.
    const providerName = tx?.provider === 'apple' ? t('accounts.provider.apple') : tx?.provider === 'google' ? t('accounts.provider.google') : undefined;
    const phoneStep = !!tx?.needsUsername && !theme.isTV;
    const heading = phoneStep ? t('auth.chooseUsername') : t('accounts.signIn.portico');
    const lede = phoneStep ? t('services.provider.lastThing') : undefined;
    const continuing = tx?.needsUsername && providerName && !theme.isTV
      ? <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{t('services.provider.heading', {provider: providerName})}</Text> : undefined;
    // The address the provider chose to share; no relay label is claimed without a producer flag.
    const sharedAddress = !theme.isTV && !!tx?.verifiedEmail && tx.needsUsername
      ? <Text selectable style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{`${t('auth.email')}: ${tx.verifiedEmail}`}</Text> : undefined;
    return layout(heading, lede, fields(
      continuing,
      sharedAddress,
      tx?.needsUsername && field(t('auth.username'), username, next => {setProviderUsernameUnavailable(false); edit(setUsername)(next);}, false, undefined,
        !theme.isTV && providerUsernameUnavailable ? t('web.signIn.error.accountTaken') : undefined,
        theme.isTV ? undefined : t('services.provider.usernameHelp')),
      tx?.needsUsername && (theme.isTV ? field(t('auth.yourName'), name, edit(setName))
        : field(`${t('auth.yourName')} · ${t('field.optional')}`, name, edit(setName), false, undefined, undefined, undefined, t('services.signup.sameUsername'))),
      tx?.mfaRequired && codeField(t('auth.verificationCode'), code, edit(setCode)), !tx?.contactVerified && field(t('auth.email'), email, edit(setEmail))),
      action('provider-complete', t('action.continue'), async () => {
        const source = opening?.source;
        if (!opening || !source || !live()) return;
        if (!tx?.contactVerified) {
          const challenge = await source.sendContact(email);
          if (!live()) return;
          contact.current = challenge.challengeId; setMasked(challenge.maskedAddress); setCode(''); setStage('contact');
        } else {
          try {
            await source.finishProvider(theme.isTV ? username || undefined : username.trim() || undefined,
              theme.isTV ? name || undefined : name.trim() || username.trim() || undefined, code || undefined,
              {signal: opening.life.signal, isCurrent: live});
          } catch (refusal) {
            // Only the confirmed public-handle collision marks this field, and only while
            // this very form and draft are still live; a stale refusal publishes nothing.
            if (!live()) return;
            if (!theme.isTV && porticoUsernameUnavailable(refusal)) {setProviderUsernameUnavailable(true); return;}
            throw refusal;
          }
          if (!live()) return;
          await directory();
        }
      }));
  }
  if (stage === 'contact') return layout(t('auth.checkEmail'), t('auth.codeSentTo', {address: masked}), fields(codeField(t('auth.verificationCode'), code, setCode)), action('contact', t('action.continue'), async () => {
    await account.current!.verifyContact(contact.current!, code); setTransaction(value => value && {...value, contactVerified: true}); setCode(''); setStage('provider');
  }, !code));
  if (stage === 'servers' && directoryList === 'failed') return <StateLayout testID="accounts-server-list-failed" icon="i-warn" tone="error"
    title={t('web.chooser.loadFailed')} body={t('web.chooser.loadFailedBody')}
    // connect#13: the one button that helps holds the starting focus.
    primaryAction={{id: 'retry-server-list', label: t('web.chooser.tryAgain'), icon: 'i-retry', loading: busy, disabled: busy, hasTVPreferredFocus: true, onPress: () => {void run(directory);}}}/>;
  if (stage === 'servers' && target && directoryIdentityUnconfirmed(error)) return <StateLayout testID="accounts-server-unconfirmed" icon="i-question"
    title={t('web.chooser.unconfirmedTitle', {server: target.name})} body={t('web.chooser.unconfirmedBody')}
    primaryAction={{id: 'retry-identity', label: t('web.chooser.tryAgain'), icon: 'i-retry', loading: busy, disabled: busy, onPress: () => connectDirectory(target, true)}}
    secondaryAction={{id: 'choose-another', label: t('web.chooser.otherServer'), disabled: busy, onPress: chooseAnother}}/>;
  const singleTVFailure = Platform.isTV && servers.length === 1 && !!target && directoryAccessRefused(error);
  const singlePhoneRefusal = !Platform.isTV && directoryList === 'ready' && servers.length === 1 && !!target
    && servers[0].id === target.id && directoryAccessRefused(error);
  if (stage === 'servers' && target && directoryClockSkew(error)) return <StateLayout testID="accounts-server-clock" icon="i-clock"
    title={t('web.chooser.clockTitle', {server: target.name})} body={t('web.chooser.clockBody')}
    primaryAction={{id: 'retry-clock', label: t('web.chooser.tryAgain'), icon: 'i-retry', loading: busy, disabled: busy, onPress: () => connectDirectory(target, true)}}
    secondaryAction={{id: 'choose-another', label: t('web.chooser.otherServer'), disabled: busy, onPress: chooseAnother}}/>;
  if (stage === 'servers' && target && directoryAccessRefused(error) && !singleTVFailure && !singlePhoneRefusal) return <DirectoryAccessRefused serverName={target.name} busy={busy}
    onOther={chooseAnother} onRefresh={() => {cancelDirectory(); void run(directory);}}/>;
  if (stage === 'servers' && target && directoryView === 'opening' && !allDirectoryOffline) return <StateLayout testID="accounts-server-opening" title={t('web.chooser.connecting', {server: target.name})}
    primaryAction={{id: 'choose-another', label: t('web.chooser.otherServer'), onPress: chooseAnother}}/>;
  if (allDirectoryOffline && !hostedWaiting && !directoryAccessRefused(error) && (!target || directoryView === 'opening' || directoryView === 'waiting' || directoryView === 'unreachable')) {
    const captured = servers, scope = accountScope.current, attempt = directoryAttempt.current;
    const current = () => !!scope && lifetime.current && stageRef.current === 'servers' && directoryAttempt.current === attempt && accountScope.current === scope
      && nativeAccounts.accountCredentials().getSnapshot().session?.account.id === scope.accountId
      && nativeAccounts.accountCredentials().getSnapshot().session?.familyId === scope.familyId;
    const capturedDownload = downloadValue?.opening;
    const openDownloads = () => {
      if (!current() || busyRef.current || !downloadValue?.available || !capturedDownload?.active || downloadsOwner.current !== capturedDownload
        || capturedDownload.source !== downloads || !capturedDownload.source.current() || !capturedDownload.source.getSnapshot().available) return;
      capturedDownload.source.open();
    };
    return <TVOfflineServers onDownloads={!!capturedDownload && capturedDownload.source === downloads && capturedDownload.active && downloadValue?.available && downloads?.current() ? openDownloads : undefined} username={nativeAccounts.accountCredentials().getSnapshot().session?.account.username}
      onSettings={() => openAccountWorkspace(scope, current)}
      onJoin={() => {if (current() && !busyRef.current) openInvitationEntry();}}
      onFind={() => {if (!current() || busyRef.current) return; cancelDirectory(); setAddress(''); setPassword(''); setCode(''); stageRef.current = 'address'; setStage('address');}}
      rows={offlineRows} scope={scope} revision={attempt} isCurrent={current} busy={busy} activeId={target?.id}
      retryIn={target ? retryIn : undefined} retryProgress={target ? (retryIn <= 0 ? 1 : 1 - retryIn / Math.max(retryDelay.current, 1)) : undefined}
      onRetry={id => {if (!current()) return; const row = captured.find(value => value.id === id); if (row) connectDirectory(row);}}
      onBack={() => {if (current()) back();}} onRefresh={() => {if (current()) {cancelDirectory(); void run(directory);}}}/>;
  }
  if (stage === 'servers' && target && directoryView === 'unreachable' && !singleTVFailure) {
    const body = directoryOfflineBody(target);
    return <StateLayout testID="accounts-server-unreachable" title={t('web.chooser.unreachableTitle', {server: target.name})} body={t(body.key, body.values)}
      primaryAction={{id: 'try-again', label: t('web.chooser.tryAgain'), loading: busy, onPress: () => connectDirectory(target, true)}}
      secondaryAction={{id: 'choose-another', label: t('web.chooser.otherServer'), onPress: chooseAnother}}/>;
  }
  if (hostedWaiting && target) {
    const captured = target, source = operation.current, attempt = directoryAttempt.current, scope = accountScope.current;
    const credentials = !Platform.isTV && scope ? nativeAccounts.accountCredentials() : undefined;
    const original = Object.freeze({baseUrl: captured.baseUrl, serverId: captured.id, publicKey: captured.publicKey});
    const current = () => {
      const live = credentials?.getSnapshot().session;
      return lifetime.current && stageRef.current === 'servers' && targetRef.current === captured &&
        operation.current === source && directoryAttempt.current === attempt && (Platform.isTV ||
        accountScope.current === scope && (!source || source.ownerRecoveryCurrent()) &&
        captured.baseUrl === original.baseUrl && captured.id === original.serverId && captured.publicKey === original.publicKey &&
        (!scope || live?.account.id === scope.accountId && live.familyId === scope.familyId));
    };
    return <StateLayout testID="accounts-hosted-waiting" icon="i-cloud-off" title={t('web.chooser.accountWaitingTitle')}
      body={t('web.chooser.accountWaitingBody', {server: captured.name})} retry={{progress: retryIn <= 0 ? 1 : 1 - retryIn / Math.max(retryDelay.current, 1), label: t('web.chooser.checkingAgain', {seconds: retryIn})}}
      // connect#11 draws Check now with the ring: the one action holds the starting focus.
      primaryAction={{id: 'check-hosted-now', label: t('web.chooser.checkNow'), loading: busy, disabled: busy, hasTVPreferredFocus: true, onPress: () => {if (current()) connectDirectory(captured, true);}}}
      secondaryAction={Platform.isTV ? {id: 'choose-another', label: t('web.chooser.otherServer'), onPress: () => {if (current()) chooseAnother();}}
        : {id: 'account-owner-help', label: t('accounts.signIn.cannotUsePortico'), disabled: busy, onPress: () => {
          if (!current() || busyRef.current || acting.current || adoptingTV.current) return;
          // Without the original authenticated directory lifetime, use ordinary typed entry.
          // A failed pin is never sent through this fallback or replaced with a newly read key.
          if (!scope || !source) {cancelDirectory(); setAddress(''); stageRef.current = 'address'; setStage('address'); return;}
          const accountId = scope.accountId, familyId = scope.familyId;
          stopRetry(); retireOwnerRecoveryContact();
          const ticket: OwnerRecoveryOpening = {active: true}; ownerRecoveryContact.current = ticket;
          const contactCurrent = () => {
            const live = credentials?.getSnapshot().session;
            return ownerRecoveryMounted.current && current() && activeRef.current && ticket.active && ownerRecoveryContact.current === ticket &&
              accountScope.current === scope && live?.account.id === accountId && live.familyId === familyId;
          };
          ticket.finish = () => {acting.current = false; if (ownerRecoveryMounted.current && lifetime.current) setBusy(adoptingTV.current);};
          acting.current = true; setBusy(true);
          void (async () => {
            try {
              if (!contactCurrent()) return;
              ticket.contact = NativeServerSignIn.ownerRecoveryContact(original, {accountId, familyId}, contactCurrent);
              await ticket.contact.open();
            } catch (failure) {if (contactCurrent()) setError(failure);}
            finally {
              if (ownerRecoveryContact.current === ticket) {
                retireOwnerRecoveryContact();
              }
            }
          })();
        }}}/>;
  }
  if (stage === 'servers' && target && directoryView === 'waiting') return <StateLayout testID="accounts-server-waiting" title={t('web.chooser.waitingTitle', {server: target.name})}
    body={t('web.chooser.waitingBody')} retry={{progress: retryIn <= 0 ? 1 : 1 - retryIn / Math.max(retryDelay.current, 1), label: t('web.chooser.checkingAgain', {seconds: retryIn})}}
    primaryAction={{id: 'check-now', label: t('web.chooser.checkNow'), loading: busy, onPress: () => connectDirectory(target, true)}}
    secondaryAction={{id: 'choose-another', label: t('web.chooser.otherServer'), onPress: chooseAnother}}/>;
  if (stage === 'servers' && (directoryList === 'ready' && servers.length === 0 || singleTVFailure || singlePhoneRefusal)) {
    const source = operation.current, attempt = directoryAttempt.current, scope = accountScope.current, capturedTarget = target;
    const current = () => !!scope && lifetime.current && stageRef.current === 'servers' && operation.current === source
      && directoryAttempt.current === attempt && accountScope.current === scope && (!capturedTarget || targetRef.current === capturedTarget)
      && nativeAccounts.accountCredentials().getSnapshot().session?.account.id === scope?.accountId
      && nativeAccounts.accountCredentials().getSnapshot().session?.familyId === scope?.familyId;
    const notice = (singleTVFailure || singlePhoneRefusal) && capturedTarget
      ? {title: t('web.chooser.refusedTitle', {server: capturedTarget.name}), body: t('web.chooser.refusedBody')} : undefined;
    const capturedDownload = downloadValue?.opening;
    const openDownloads = () => {
      if (!current() || busyRef.current || !downloadValue?.available || !capturedDownload?.active || downloadsOwner.current !== capturedDownload
        || capturedDownload.source !== downloads || !capturedDownload.source.current() || !capturedDownload.source.getSnapshot().available) return;
      capturedDownload.source.open();
    };
    const EmptyDirectory = Platform.isTV ? TVNoServers : PhoneNoServers;
    return <EmptyDirectory {...(!Platform.isTV ? {onDownloads: !!capturedDownload && capturedDownload.source === downloads && capturedDownload.active && downloadValue?.available && downloads?.current() ? openDownloads : undefined,
      username: nativeAccounts.accountCredentials().getSnapshot().session?.account.username,
      onSettings: () => openAccountWorkspace(scope, current)} : {})} isCurrent={current} notice={notice} busy={busy} error={notice ? undefined : status} onBack={() => {if (current()) back();}}
      onJoin={(raw, origin) => {
        if (!current() || busyRef.current || acting.current) return;
        const link = NativeServerSignIn.parseInvitationEntry(raw, origin);
        if (!link) {setError(Object.assign(new Error('invitation_incomplete'), {code: 'invitation_incomplete'})); return;}
        cancelDirectory(); invitationIntent.current = Object.freeze({...link}); setPassword(''); setCode(''); setAddress(link.server);
        stageRef.current = 'address'; setStage('address'); void run(async () => {await inspect(link.server);});
      }}
      onFind={() => {if (!current() || busyRef.current) return; cancelDirectory(); setAddress(''); setPassword(''); setCode(''); stageRef.current = 'address'; setStage('address');}}
      onRefresh={() => {if (current()) void run(directory);}}
      onShare={() => {if (!current() || busyRef.current) return; void run(async () => {
        if (!current()) return;
        // Public distribution page only: never share an account, invitation or setup URL.
        await Share.share({message: 'https://getportico.tv/download/'});
      });}}
      onSignOut={() => {if (current() && !busyRef.current) {stageRef.current = 'account-sign-out'; setStage('account-sign-out');}}}/>;
  }
  if (stage === 'servers') return layout(t('portico.landing.pickTitle'), servers.length ? t('accounts.session.portico') : t('web.hostedAccount.noServersBody'),
    <View style={{alignSelf: 'stretch', gap: 12, marginBottom: 18}}>{directoryRows(server && server.signIn === 'portico' ? servers.filter(value => value.id === server.id) : servers).map((row, index) => {
      const value = servers.find(item => item.id === row.id)!;
      // TV: the list has no primary action, so the first server takes the remote's focus on arrival
      // and again when a failed connection hands control back (P2-74 X-01, E-tv-01).
      return <Focusable key={row.id} focusTreatment="none" disabled={busy} hasTVPreferredFocus={Platform.isTV && index === 0 && !busy} accessibilityLabel={`${row.name}${row.owner ? `, ${t('accounts.settings.owner')}` : ''}${row.subtitle ? `, ${t(row.subtitle.key, row.subtitle.values)}` : ''}`}
        onPress={() => connectDirectory(value)}
        style={({focused}) => ({flexDirection: 'row', alignItems: 'center', gap: 14, paddingHorizontal: 16, paddingVertical: 14, borderRadius: 14, borderWidth: 1.5,
          borderColor: focused ? theme.colors.accentStrong : theme.colors.line, backgroundColor: focused ? `${theme.colors.text}14` : `${theme.colors.text}0A`,
          opacity: row.offline ? .55 : 1})}>
        <Icon name="i-server" size={22} color={theme.colors.textSecondary}/>
        <View style={{flex: 1, gap: 2}}>
          <View style={{flexDirection: 'row', alignItems: 'center', gap: 8}}><Text style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{row.name}</Text>
            {row.owner && <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{t('accounts.settings.owner')}</Text>}</View>
          {row.subtitle && <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{t(row.subtitle.key, row.subtitle.values)}</Text>}
        </View>
        <Icon name="i-right" size={20} color={theme.colors.textTertiary}/>
      </Focusable>;
    })}</View>, undefined,
    <Button variant="text" disabled={busy} onPress={() => setStage('account-sign-out')}>{t('accounts.signOut.portico')}</Button>);
  if (stage === 'account-sign-out') return layout(t('accounts.signOut.portico'), undefined, undefined,
    action('account-sign-out', t('action.signOut'), async () => {await nativeAccounts.signOutPorticoAccount(accountScope.current!); reset();}));
  if (pinRecovery) {
    const captured = pinRecovery;
    const current = () => lifetime.current && !captured.retired && pinRecoveryRef.current === captured
      && operation.current === captured.operation && profileRef.current === captured.profile && stageRef.current === 'profile-pin';
    return <PhoneProfileRecovery controller={captured.controller} profileName={captured.profile.name} username={username}
      onOpen={value => {
        if (!current() || captured.controller.getSnapshot().phase !== 'done' || !/^\d{4}$/.test(value) || busyRef.current || acting.current) return;
        retireRecovery(captured); setCode(value);
        void run(async () => {await captured.operation.chooseProfile(captured.profile.id, value); if (lifetime.current && operation.current === captured.operation) onComplete();});
      }}
      onClose={() => {if (!current()) return; retireRecovery(captured); setCode(''); setError(undefined);}}/>;
  }
  if (stage === 'profiles') {
    const originalOperation = operation.current!;
    // TV (F-PROFILE-7): one centred row of faces and nothing else; Back is the remote key.
    if (theme.isTV) return <TVWhosWatching testID="accounts-entry">
      <ProfilePicker focusScope={`tv.auth.profiles.${server!.id}`} focusBusy={busy} title={t('accounts.profiles.onServer', {server: server!.name})} profiles={profiles.map(value => ({id: value.id, name: value.name, initial: [...value.name][0] ?? '', color: value.art, primary: value.primary, pinRequired: value.pinRequired, disabled: busy}))} preferredProfileId={profileFocus.get(originalOperation)}
        onFocusChange={id => {if (id && operation.current === originalOperation) profileFocus.set(originalOperation, id);}}
        onSelect={id => {const value = profiles.find(item => item.id === id)!; if (value.pinRequired) {setProfile(value); setCode(''); setStage('profile-pin');} else void run(() => choose(id));}}/>
      {status}
      <BootWaitLine active={busy} serverName={server!.name} testID="accounts-profile-wait"/>
    </TVWhosWatching>;
    return layout(t('accounts.profiles.onServer', {server: server!.name}), t('accounts.session.' + server!.signIn as 'accounts.session.portico' | 'accounts.session.local'),
    <><ProfilePicker focusScope={`tv.auth.profiles.${server!.id}`} focusBusy={busy} profiles={profiles.map(value => ({id: value.id, name: value.name, initial: [...value.name][0] ?? '', color: value.art, primary: value.primary, pinRequired: value.pinRequired, disabled: busy}))} preferredProfileId={theme.isTV ? profileFocus.get(originalOperation) : undefined}
      onFocusChange={id => {if (id && operation.current === originalOperation) profileFocus.set(originalOperation, id);}} showTitle={false}
      onSelect={id => {const value = profiles.find(item => item.id === id)!; if (value.pinRequired) {setProfile(value); setCode(''); setStage('profile-pin');} else void run(() => choose(id));}}/>
    <BootWaitLine active={busy} serverName={server!.name} testID="accounts-profile-wait"/></>);
  }
  if (theme.isTV && stage === 'profile-forgot-pin') return <TVForgotPINHandoff profileName={profile!.name}
    url={new URL('/settings/profile', operation.current!.serverOrigin()).href} body="next.auth.tv.forgotPINBody" onBack={back}/>;
  if (theme.isTV && stage === 'profile-pin') return <TVProfilePINForm
    profile={{id: profile!.id, name: profile!.name, initial: [...profile!.name][0] ?? '', color: profile!.art, primary: profile!.primary, pinRequired: true}}
    code={code} onCodeChange={setCode} busy={busy} error={status} onBack={back}
    hints={busy ? <BootWaitLine active serverName={server?.name} testID="accounts-profile-wait"/> : undefined}
    onSubmit={value => {void run(() => choose(profile!.id, value));}}
    onForgotPIN={() => {if (!busy) {setCode(''); setError(undefined); setStage('profile-forgot-pin');}}}/>;
  if (stage === 'profile-pin') return layout(t('profile.pinTitle', {name: profile!.name}), undefined,
    <View style={{alignSelf: 'stretch'}}><PhonePINEntry value={code} onChange={setCode} onComplete={value => {void run(() => choose(profile!.id, value));}} busy={busy} error={!!error}/>
      <View style={{alignItems: 'center', marginTop: 8}}><Button variant="text" disabled={busy} onPress={() => {if (busy || !profile || !operation.current) return; try {setError(undefined); retireRecovery(); const next: PINRecovery = {profile, operation: operation.current, controller: operation.current.profileRecovery(profile.id), retired: false}; pinRecoveryRef.current = next; setPinRecovery(next);} catch (failure) {setError(failure);}}}>{t('pin.forgot')}</Button></View>
      <BootWaitLine active={busy} serverName={server?.name} testID="accounts-profile-wait"/></View>,
    action('profile-pin', t('action.continue'), () => choose(profile!.id, code), !/^\d{4}$/.test(code)));
  const consentBody = tvKind === 'local' && server
    ? tvState.verificationUri ? t('accounts.consent.local', {server: server.name, address: tvState.verificationUri}) : undefined
    : t('accounts.consent.portico');
  const approval = renderedTV?.approval();
  const retryApproval = approval?.phase === 'failed' ? () => {void renderedTV?.retryApproval();} : undefined;
  const newCode = (approval ? approval.phase === 'terminal' && renderedTV?.canTypeCredentials()
    : tvState.phase === 'error' || tvState.phase === 'denied' || error)
    ? () => {void renderedTV?.newCode();} : undefined;
  if (Platform.isTV) return <TVCodePresenter {...(tvKind === 'local' ? {mode: 'local' as const, serverName: server!.name} : {mode: 'portico' as const})} body={consentBody}
    verificationUri={tvState.verificationUri} verificationUriComplete={tvState.verificationUriComplete} userCode={tvState.userCode}
    status={<>{status}{(tvState.phase === 'denied' || tvState.phase === 'error') && <Text style={[theme.type.caption, {color: theme.colors.textTertiary, marginBottom: 28}]}>{t(tvState.phase === 'denied' ? 'activation.declined' : 'activation.failed')}</Text>}</>}
    busy={busy} onTypeCredentials={() => {void renderedTV?.credentials();}} onBack={codeBack} onNewCode={newCode} onRetry={retryApproval} retryAt={approval?.retryAt} credentialsDisabled={!!approval && !renderedTV?.canTypeCredentials()}/>;
  return layout(t('activation.title'), consentBody,
    <View style={{gap: 16, marginBottom: 20}}>{tvState.verificationUriComplete && <QRCode value={tvState.verificationUriComplete} size={theme.isTV ? 240 : 180}/>}
      <Text selectable style={[theme.type.title, {color: theme.colors.text}]}>{tvState.userCode ?? t('activation.preparingCode')}</Text>
      {tvState.verificationUri && <Text selectable style={[theme.type.body, {color: theme.colors.textSecondary}]}>{tvState.verificationUri}</Text>}
      <Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{t(tvState.phase === 'denied' ? 'activation.declined' : tvState.phase === 'error' ? 'activation.failed' : 'activation.codeRefreshes')}</Text></View>,
    retryApproval ? {id: 'retry-approval', label: t('action.tryAgain'), disabled: busy || (approval?.retryAt ?? 0) > Date.now(), loading: busy, onPress: retryApproval} : newCode ? {id: 'new-code', label: t('activation.getNewCode'), disabled: busy, loading: busy, onPress: newCode} : undefined);
}
