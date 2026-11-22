import {accountExpectation, nativeAccountRead} from '../../../platform/account-work';
import {useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState} from 'react';
import {AppState, Linking, NativeModules, Platform, Share} from 'react-native';
import {useTVMenuCapture} from '../../../platform/tv-menu';
import type {HostedServer} from '../../../../../../packages/client-core/src';
import {ServerSignInProbe, type ServerSignIn} from '../../../../../../packages/client-core/src/sign-in';
import {firstAnswering, serverAddressCandidates} from '../../../../../../packages/client-core/src/server-address';
import {routeOrigin} from '../../../../../../packages/client-core/src/route-identity';
import type {NativeInvitationEntry} from '../../../platform/portico-invitation';
import type {PublicInvitation} from '../../../../../../packages/client-core/src/direct-account';
import type {ManagedProfile} from '../../../../../../packages/client-core/src/profile-management';
import {MFARequiredError, type IdentityOptions, type IdentityTransaction, type Registration} from '../../../../../../packages/client-core/src/hosted-security';
import type {SetupCodeSnapshot} from '../../../../../../packages/client-core/src/setup-code';
import {presentError} from '../../../../../../packages/client-core/src/presentation/errors';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {negotiationRefusal} from '../../../platform/negotiation';
import {useTheme} from '../../../kit/theme';
import {nativeAccounts} from '../../../platform/accounts';
import {NativeServerSignIn} from '../../../platform/server-sign-in';
import {HostedSignInUnavailable} from '../../../platform/hosted-sign-in-unavailable';
import {NativePorticoSignIn, type NativeFactorWitness} from '../../../platform/portico-sign-in';
import {nativeNetworkFetch, browseNearby} from '../../../platform/server-connections';
import {createNativePorticoTVSignIn} from '../../../platform/tv-sign-in';
import {createTVApprovalCredentialOpening, type TVApprovalCredentialOpening} from '../../../screens/accounts/tv-approval-credentials';
import {createTVApprovalFinalization, type TVApprovalFinalization} from '../../../screens/accounts/tv-approval-finalization';
import {createTVCodeOpening, type TVCodeCapture, type TVCodeKind, type TVCodeOpening} from '../../../screens/accounts/tv-code-opening';
import {useCodeRetry} from '../../../tv/auth/code-retry';
import type {NativeProfileRecovery} from '../../../platform/profile-recovery';
import type {NativeSignInHint} from '../../../platform/sign-in-ended';
import type {ResetFileLocation} from '../../../../../../packages/client-core/src/account-reset';
import type {AccountDownloadsEntry} from '../../../screens/accounts/downloads-entry-model';
import type {NativeInvitationLink} from '../../../platform/invitation-links';
import {NativeAccountWorkspace} from '../../../platform/account-workspace';
import {afterEntry} from '../../../shell/after-entry';
import {directoryFailure, directoryRows, nextRetryDelay} from '../../../screens/accounts/directory-chooser';
import {registerBack} from '../../../kit/navigation/back';

export type NativeAccountEntryStage = 'welcome' | 'tv-move-handoff' | 'ended-sign-in' | 'owner-reset' | 'owner-reset-code' | 'owner-reset-done' | 'setup-confirm' | 'setup' | 'address' | 'identity-code' | 'local' | 'factor' | 'uses-portico' | 'portico' | 'forgot' | 'forgot-sent' | 'register' | 'verify' | 'provider' | 'contact' | 'servers' | 'account-sign-out' | 'profiles' | 'profile-pin' | 'profile-forgot-pin' | 'tv' | 'tv-credentials' | 'tv-required-setup' | 'member-move' | 'invitation-code' | 'invitation-local' | 'invitation-portico' | 'invitation-sign-out' | 'invitation-entry' | 'invitation-account' | 'account-workspace';
type AccountIntent = Readonly<{kind: 'provider'; id: 'apple' | 'google'} | {kind: 'continue' | 'forgot' | 'create'}>;
type MemberMove = Awaited<ReturnType<NativeServerSignIn['createMemberMove']>>;
type MemberMoveSnapshot = ReturnType<MemberMove['getSnapshot']>;
type ProfilePINOpening = Readonly<{profile: ManagedProfile; operation: NativeServerSignIn}>;
export type PINRecovery = {controller: NativeProfileRecovery; profile: ManagedProfile; operation: NativeServerSignIn; retired: boolean};
type RequiredSetup = Readonly<{kind: 'required-setup'; requirement: 'password' | 'two-step'; serverName: string; serverOrigin: string}>;
type Result = {kind: 'profiles'; profiles: readonly ManagedProfile[]; canRemember?: boolean} | {kind: 'challenge'; expiresAt: string} | {kind: 'complete'} | RequiredSetup;
const t = defaultI18n.t;
/** TV reached a server nobody has set up. It is set up from a phone or a browser, not here. */
const SETUP_ELSEWHERE = 'setup_elsewhere';
export const setupElsewhereRefusal = (error: unknown): boolean => (error as {code?: unknown} | null)?.code === SETUP_ELSEWHERE;
/** A profile's face on the pickers and the PIN page: its first letter on the profile's own colour. */
export const profileFace = (value: ManagedProfile) => ({initial: [...value.name][0] ?? '', color: value.art});
export type AccountEntryProps = {downloads?: AccountDownloadsEntry; incomingInvitation?: NativeInvitationLink; onBack?: () => void; onComplete: () => void; onSetupComplete?: (serverId: string) => void; onStageChange?: (stage: NativeAccountEntryStage) => void; initialStage?: 'welcome' | 'address'; initialAddress?: string; endedSignIn?: NativeSignInHint;
  /** Launch with no saved server: a Portico Account kept on this device opens its server list, not welcome. */
  resumeAccount?: boolean};

/** Everything the sign-in flow holds and does: its stages, the server and account operations, and their guards. */
export function useAccountEntry({downloads, incomingInvitation, onBack, onComplete, onSetupComplete, onStageChange, initialStage = 'welcome', initialAddress = '', endedSignIn, resumeAccount}: AccountEntryProps) {
  const latestEndedHint = useRef(endedSignIn); latestEndedHint.current = endedSignIn;
  const preparedAuthSource = useRef<NativePorticoSignIn | undefined>(undefined);
  // The original phone provider opening owns its waiting paint across the OS
  // handoff, callback read and session adoption. It is paint, never authority.
  const [providerLoading, setProviderLoading] = useState<NativePorticoSignIn>();
  const theme = useTheme(), [stage, setStage] = useState<NativeAccountEntryStage>(endedSignIn ? 'ended-sign-in' : initialStage), [busy, setBusy] = useState(false), [error, setError] = useState<unknown>();
  // Portico Services refused to renew the account's sign-in while it was shown; welcome says so.
  const [accountEnded, setAccountEnded] = useState(false);
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
  const [setupAddress, setSetupAddress] = useState('');
  // A new Portico Account needs "I am 13 or older and I agree to the Terms of Use" ticked (definition 01 rule 55).
  const [acceptTerms, setAcceptTerms] = useState(false);
  const [setupPasswordConfirm, setSetupPasswordConfirm] = useState(''), [name, setName] = useState(''), [email, setEmail] = useState(''), [masked, setMasked] = useState('');
  const [server, setServer] = useState<ServerSignIn>(), [profiles, setProfiles] = useState<readonly ManagedProfile[]>([]), [profile, setProfile] = useState<ManagedProfile>();
  // "Remember this profile on this device": offered only where the server allows it; the tick carries from the faces to a PIN page and applies after a correct PIN.
  const [canRemember, setCanRemember] = useState(false), [remember, setRemember] = useState(true);
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
  const pinOpening = useRef<ProfilePINOpening | undefined>(undefined);
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
  // The shared status line belongs to the stage visit its error was raised on: moving on (say, from a
  // refused Portico sign-in to Use a local server), or coming back later, never carries the old sentence along.
  // Each visit to a stage is counted; an error is shown only on the visit it was raised in. The visit
  // effect runs first, so an error raised in the same render as a stage change still shows there.
  const visit = useRef(0), [errorVisit, setErrorVisit] = useState<number>();
  useLayoutEffect(() => {visit.current++;}, [stage]);
  useLayoutEffect(() => {setErrorVisit(error === undefined ? undefined : visit.current);}, [error]);
  const stageError = error !== undefined && errorVisit === visit.current ? error : undefined;
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
  useEffect(() => {lifetime.current = true; return () => {lifetime.current = false; retireInvitation(); memberMoveRef.current?.dispose(); retireOwnerRecoveryContact(); retireOwnerReset(); retireRecovery(); stopRetry(); operation.current?.dispose(); account.current?.dispose();};},
    // Every command here keeps one identity for this entry; the mount and unmount commit alone decide.
    [retireInvitation, retireOwnerRecoveryContact, retireOwnerReset, retireRecovery, stopRetry]);
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
  const stillPIN = useCallback((opening: ProfilePINOpening) => lifetime.current && pinOpening.current === opening
    && operation.current === opening.operation && stageRef.current === 'profile-pin', []);
  const openPIN = (next: ManagedProfile, source: NativeServerSignIn) => {
    pinOpening.current = {profile: next, operation: source};
    stageRef.current = 'profile-pin';
    setProfile(next); setCode(''); setError(undefined); setStage('profile-pin');
  };
  // The runner's identity carries the exact busy, stage and recovery it drew with; effects that
  // drive it read the port of the commit that drew them rather than a newer render's runner.
  const run = useCallback(async (work: () => Promise<void>) => {
    if (busy || acting.current || adoptingTV.current || tvOpening.current?.pending() || !lifetime.current) return; acting.current = true; setBusy(true); setError(undefined); setAccountEnded(false);
    const startedInvitationEpoch = invitationEpoch.current;
    const startedPIN = stage === 'profile-pin' ? pinOpening.current : undefined;
    try {await work();} catch (failure) {if (lifetime.current && invitationEpoch.current === startedInvitationEpoch && (!startedPIN || stillPIN(startedPIN))) {
      if (endedTarget.current) {setPassword(''); setCode('');}
      if (failure instanceof MFARequiredError) {factorKind.current = 'portico'; setCode(''); setRecovery(false); setStage('factor');}
      else {
        // TV: the filled boxes stay in the error colour for 400 ms (F-PROFILE-28) before they empty; focus stays.
        if (stage === 'profile-pin' && Platform.isTV || (Platform.isTV && stage === 'factor' && !recovery)) {
          if (wrongCodeTimer.current) clearTimeout(wrongCodeTimer.current);
          wrongCodeTimer.current = setTimeout(() => {
            wrongCodeTimer.current = undefined;
            if (startedPIN ? stillPIN(startedPIN) : lifetime.current && stageRef.current === stage) setCode('');
          }, 400);
        } else if (stage === 'profile-pin') setCode('');
        setError(failure);
      }
    }} finally {if (invitationEpoch.current === startedInvitationEpoch) {acting.current = false; if (lifetime.current) setBusy(adoptingTV.current || tvOpening.current?.pending() === true);}}
  }, [busy, recovery, stage, stillPIN]);
  const reset = useCallback(() => {pinOpening.current = undefined; retireTVOpening(true); retireAccountWorkspace(); retireInvitationEntry(); retireInvitationSwitch(); invitationIntent.current = undefined; retireInvitation(); ownerResetIntent.current = undefined; endedTarget.current = undefined; retireOwnerRecoveryContact(); retireOwnerReset(); setDirectoryList('idle'); memberFactorOperation.current = undefined; stopMemberMove(); retireRecovery(); stopRetry(); targetRef.current = undefined; stageRef.current = initialStage; ++directoryAttempt.current; acting.current = false; preparedAuthSource.current = undefined; operation.current?.dispose(); operation.current = undefined; account.current?.dispose(); account.current = undefined;
    // A retired attempt never clears busy itself (its finally sees a newer attempt), so the reset does.
    setBusy(false); setAccountEnded(false); setPassword(''); setSetupPasswordConfirm(''); setCode(''); setIdentityCode(''); setIdentityWrong(false); setIdentityAddress(''); identityOrigin.current = undefined; setTarget(undefined); setError(undefined); retryDelay.current = 20; setRetryIn(20); setStage(initialStage);},
  [initialStage, retireAccountWorkspace, retireInvitation, retireInvitationEntry, retireInvitationSwitch, retireOwnerRecoveryContact, retireOwnerReset, retireRecovery, stopMemberMove, stopRetry, retireTVOpening]);
  const cancelDirectory = useCallback(() => {retireOwnerRecoveryContact(); retireOwnerReset(); setDirectoryList('idle'); retireRecovery(); stopRetry(); targetRef.current = undefined; ++directoryAttempt.current; operation.current?.dispose(); operation.current = undefined; acting.current = false; setBusy(false); setTarget(undefined); setError(undefined);}, [retireOwnerRecoveryContact, retireOwnerReset, retireRecovery, stopRetry]);
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
    afterEntry.clear();
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
    if (stage === 'setup') {setError(undefined); setStage('setup-confirm'); return;}
    // Phone and tablet: Back from a server's own sign-in returns to Find your server with its address kept (auth.html 5-6).
    if (stage === 'local' && !Platform.isTV) {const kept = address; reset(); setAddress(kept); stageRef.current = 'address'; setStage('address'); return;}
    if (stage === 'member-move') {stopMemberMove(); setStage(server?.signIn === 'local' ? 'local' : 'uses-portico'); return;}
    if (stage === 'tv-move-handoff') {setStage('uses-portico'); return;}
    if (pinRecoveryRef.current) {retireRecovery(); setCode(''); return;}
    if (stage === 'servers' && target) {cancelDirectory(); return;}
    if (stage === 'profile-forgot-pin' && profile && operation.current) {openPIN(profile, operation.current); return;}
    // Account help returns to the Portico Account form that opened it, not the front door.
    if (stage === 'forgot' || stage === 'forgot-sent') {setPassword(''); setError(undefined); setStage('portico'); return;}
    // "‹ Profiles" (phone, frame 14) and TV Back both return to Who's watching.
    if (stage === 'profile-pin') {
      pinOpening.current = undefined; stageRef.current = 'profiles';
      setCode(''); setProfile(undefined); setError(undefined); setStage('profiles'); return;
    }
    if (Platform.isTV && (stage === 'tv-credentials' || stage === 'factor' || stage === 'tv-required-setup')) {void run(returnToTVCode); return;}
    if (stage === initialStage) onBack?.(); else reset();
  };
  // On TV the activation code is the first screen (owner, 17 Nov): Back there leaves the app.
  const tvDoor = Platform.isTV && tvKind === 'portico' && initialStage === 'welcome' && !onBack;
  const tvDoorRef = useRef(false); tvDoorRef.current = tvDoor && stage === 'tv';
  backRef.current = back;
  useEffect(() => {const listener = registerBack(() => {
    if (stageRef.current === 'tv' && !adoptingTV.current) {
      // Idle, Back leaves the app; while something is under way it cancels that and a fresh code starts.
      if (tvDoorRef.current && !busyRef.current) return false;
      tvDoorTried.current = false; backRef.current(); return true;
    }
    if (busyRef.current || adoptingTV.current) return true;
    if (stageRef.current === 'welcome' && !onBackRef.current) return false;
    backRef.current(); return true;
  }, {scope: 'surface'}); return () => listener();}, []);
  useTVMenuCapture(Platform.isTV && (stage !== 'welcome' || !!onBack) && !(stage === 'tv' && tvDoor));
  const consume = useCallback((result: Result) => {
    if (!lifetime.current) return;
    if (result.kind === 'required-setup') {
      setPassword(''); setCode(''); setRecoveryCode(''); setRecovery(false); setProfiles([]); setProfile(undefined); setCanRemember(false); setRemember(true);
      setTransaction(undefined); setOptions(undefined); setName(''); setEmail(''); setMasked('');
      setIdentityCode(''); setIdentityWrong(false); setIdentityAddress(''); identityOrigin.current = undefined;
      accountScope.current = undefined; contact.current = undefined;
      if (Platform.isTV) {setRequiredSetup(result); setStage('tv-required-setup');}
      else {
        const source = operation.current, entered = stageRef.current;
        void Linking.openURL(new URL('/sign-in', result.serverOrigin).href).catch(error => {
          if (lifetime.current && operation.current === source && stageRef.current === entered) setError(error);
        });
      }
    }
    else if (result.kind === 'profiles') {setServer(operation.current!.serverDescriptor()); setProfiles(result.profiles); setCanRemember(result.canRemember === true); setRemember(true); setStage('profiles');}
    else if (result.kind === 'challenge') {factorKind.current = 'local'; setCode(''); setRecovery(false); setStage('factor');}
    else onComplete();
  }, [onComplete]);
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
      // Signed in: name both accounts and stop on the confirm. Nothing moves until Move is pressed.
      await move.preparePortico();
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
      const out = await nativeAccountRead(credentials, issued => nativeAccounts.porticoServers(issued),
        {expected, isCurrent: current});
      if (!current()) return;
      if (!out || !Array.isArray(out.items) || out.items.some(value => !value || typeof value.id !== 'string' || !value.id ||
        typeof value.name !== 'string' || !value.name || typeof value.baseUrl !== 'string' || typeof value.publicKey !== 'string' ||
        !Number.isSafeInteger(value.policyRevision) || value.policyRevision < 0) || new Set(out.items.map(value => value.id)).size !== out.items.length)
        throw Object.assign(new Error('native_directory_invalid'), {code: 'invalid_response'});
      setServers(out.items); setDirectoryList('ready');
    } catch (failure) {
      if (!current()) {
        if (lifetime.current && attempt === directoryAttempt.current) {reset(); if (credentials.getSnapshot().ended) setAccountEnded(true);}
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
        retireOwnerRecoveryContact(); retireOwnerReset(); retireRecovery(); operation.current?.dispose(); next = operation.current = new NativeServerSignIn();
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
  // Restore a saved Portico Account before a signed-out TV opens its activation code.
  // auth.html frame 4 is the first screen on phone and tablet. It draws without Services; the first Portico Account
  // action (Apple, Google, Continue, Forgot password?, Create account) prepares the account client, as the old front
  // door's button did (section 11), and is then carried out on the prepared page.
  const pendingIntent = useRef<AccountIntent | undefined>(undefined), intentRunner = useRef<((intent: AccountIntent) => void) | undefined>(undefined);
  const [providerNotice, setProviderNotice] = useState<'apple' | 'google'>();
  useEffect(() => {
    if (stage !== 'portico') {if (stage !== 'welcome') pendingIntent.current = undefined; return;}
    const intent = pendingIntent.current;
    if (busy || !intent || !intentRunner.current) return;
    pendingIntent.current = undefined; intentRunner.current(intent);
  }, [stage, busy, options]);
  const startAccount = (intent: AccountIntent) => {
    if (!lifetime.current || stageRef.current !== 'welcome' || busyRef.current || acting.current) return;
    setProviderNotice(undefined); pendingIntent.current = intent; void run(portico);
  };
  // On TV the activation code is the first screen: the door starts it by itself (the owner's ruling, 17 Nov, replacing
  // the two-button door) at launch and after a return from Sign in directly to a server. A refused or failed code
  // stays on the door with its reason, so nothing retries in a loop.
  const tvDoorTried = useRef(false);
  // A Portico code that could not be created keeps the Activate card, saying why. A failed create is retried by the code
  // service itself (about 5, 10, 20 s, then every 30 s at most, one request at a time); when the opening itself failed
  // nothing would retry, so the screen asks again every 30 seconds through newCode, which refuses a second request
  // while one is in flight.
  const selfRetrying = tvState.phase === 'creating' && !!tvState.message;
  // The approval watch exists from the start in phase 'ready'; only its later phases mean a code was used.
  const approvalNow = tvView?.opening.approval();
  const noCodeNow = Platform.isTV && stage === 'tv' && tvKind === 'portico' && !tvState.userCode && (!approvalNow || approvalNow.phase === 'ready')
    && (selfRetrying || tvState.phase === 'error' || stageError !== undefined);
  useCodeRetry(noCodeNow && !selfRetrying && !busy, () => {
    const opening = tvView?.opening; if (opening?.current() && !busyRef.current && !opening.pending()) void opening.newCode();
  });
  useEffect(() => {if (stage === 'address') tvDoorTried.current = false;}, [stage]);
  const resume = useRef(() => {}); resume.current = () => {if (lifetime.current && stageRef.current === 'welcome' && !busyRef.current && !acting.current) void run(portico);};
  const resumeWelcome = resumeAccount && (!tvDoor || stage === 'welcome' && appActive && !stageError);
  useEffect(() => {
    if (!resumeWelcome) return;
    let active = true; const credentials = nativeAccounts.accountCredentials();
    void credentials.synchronize().then(() => {if (active && (credentials.getSnapshot().session || tvDoor)) resume.current();}, () => {});
    return () => {active = false;};
  }, [resumeWelcome, tvDoor]);
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
    // The owner's move code: for a move to local sign-in, and (P2-66) a move to Portico Accounts.
    if (!source || !server || !(server.signIn === 'local' && server.move === 'to_local' || server.signIn === 'portico' && server.move === 'to_portico') || Platform.isTV) return;
    stopMemberMove();
    const move = await source.createMemberMove('code');
    if (!lifetime.current || operation.current !== source) {move.dispose(); return;}
    memberMoveRef.current = move; memberOperation.current = source;
    setMemberMove(move); setMoveSnapshot(move.getSnapshot()); setStage('member-move');
  };
  const beginEndedMemberCode = async (openingCurrent: () => boolean, signal: AbortSignal) => {
    const hint = endedSignIn;
    if (Platform.isTV || !hint || hint.signIn !== 'portico' || signal.aborted || !openingCurrent() || !lifetime.current || stageRef.current !== 'ended-sign-in') return;
    retireOwnerRecoveryContact(); retireOwnerReset(); retireRecovery(); operation.current?.dispose();
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
    const source = account.current;
    if (!source) return;
    const originalStage = stageRef.current;
    const current = () => lifetime.current && account.current === source && stageRef.current === originalStage;
    await source.forgot(current);
    if (current()) {setPassword(''); setCode(''); setStage('portico');}
  };
  const inspect = async (origin = address) => {
    const resetIntent = ownerResetIntent.current, invite = invitationIntent.current;
    retireOwnerRecoveryContact(); retireOwnerReset(); retireRecovery(); operation.current?.dispose(); const next = operation.current = new NativeServerSignIn(nativeAccounts, {requiredSetupHandoff: Platform.isTV, expectedTarget: endedTarget.current, expectedInvitation: invite ? {origin: invite.server, serverId: invite.id, fingerprint: invite.fingerprint, signIn: invite.signIn, expiresAt: invite.expiresAt} : undefined});
    const found = await firstAnswering(serverAddressCandidates(origin), candidate => next.inspect(candidate));
    const value = found.value; if (!lifetime.current) return;
    const inspected = found.origin;
    if (next.needsIdentityCode()) {
      routeOrigin(inspected); identityOrigin.current = inspected;
      setIdentityAddress(inspected);
      setServer(undefined); setIdentityCode(''); setIdentityWrong(false); setStage('identity-code'); return;
    }
    if (!value) throw Object.assign(new Error('native_sign_in_authority_changed'), {code: 'identity_mismatch'});
    if (resetIntent && (ownerResetIntent.current !== resetIntent || routeOrigin(inspected) !== resetIntent.origin || value.id !== resetIntent.serverId || value.signIn !== 'portico' || value.setupRequired))
      throw Object.assign(new Error('native_sign_in_authority_changed'), {code: 'identity_mismatch'});
    setServer(value);
    if (value.setupRequired && !Platform.isTV) {
      setSetupAddress(routeOrigin(inspected));
      setName(value.name); setUsername(''); setPassword(''); setSetupPasswordConfirm(''); setStage('setup-confirm'); return;
    }
    if (value.setupRequired) throw Object.assign(new Error('native_setup_phone_required'), {code: SETUP_ELSEWHERE});
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
  const choose = async (id: string, enteredPin?: string) => {await operation.current!.chooseProfile(id, enteredPin, canRemember && remember); onComplete();};
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
  const action = (id: string, label: string, work: () => Promise<void>, disabled = false) => ({id, label, disabled: busy || disabled, loading: busy, onPress: () => {void run(work);}});
  const renderedTV = stage === 'tv' ? tvView?.opening : undefined;
  const codeBack = () => {if (renderedTV?.current()) back();};
  const backAction = stage !== 'welcome' || onBack ? {id: 'back', label: t('action.back'), disabled: busy, onPress: stage === 'tv' ? codeBack : back} : undefined;
  const failure = stageError ? presentError(stageError, stage === 'profile-pin' ? 'profiles' : 'sign-in') : undefined;
  const passwordInvalid = stage === 'local' && (stageError as {code?: string} | undefined)?.code === 'local_credentials_invalid';
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
      if (credentials.getSnapshot().ended) setAccountEnded(true);
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
  // One setup target per server shown for setup: SetupPage keeps one controller for it.
  const setupTarget = useMemo(() => server && setupAddress ? Object.freeze({origin: setupAddress, server, expected: endedTarget.current}) : undefined, [server, setupAddress]);
  // O-159: a server this app can't talk to is refused before any credential, by name.
  const refusal = negotiationRefusal(error);
  const tvDevice = Platform.isTV, localNetworkNote = Platform.OS === 'ios';
  const openLegalLink = (url: string) => {void run(async () => {await Linking.openURL(url);});};
  const openEndedSignInPage = (hint: NativeSignInHint) => {void run(async () => {await Linking.openURL(new URL('/sign-in', hint.origin).href);});};
  const openLocalSignInPage = (source: NativeServerSignIn) => {
    void run(async () => {if (source === operation.current && lifetime.current && stageRef.current === 'local') await Linking.openURL(new URL('/sign-in', source.serverOrigin()).href);});
  };
  // A provider is offered where this app can open the browser for it, or Apple signs in natively.
  const providerOffered = (value: IdentityOptions['providers'][number]) => value.enabled && (NativeModules.PorticoAccountAuth.canOpenBrowser || value.id === 'apple' && value.native
    && value.nativeClientIds.includes(NativeModules.PorticoAccountAuth.bundleId) && NativeModules.PorticoAccountAuth.canSignInApple);
  // Public distribution page only: never share an account, invitation or setup URL.
  const shareDownloadPage = async () => {await Share.share({message: 'https://getportico.tv/download/'});};
  return {latestEndedHint, preparedAuthSource, providerLoading, setProviderLoading, theme, stage, setStage, busy, error, setError, accountEnded, address, setAddress, username, setUsername, password, setPassword, code, setCode, wrongCodeTimer, usernameUnavailable, setUsernameUnavailable, passwordShown, setPasswordShown, registerOpening, verifyOpening, setVerifyOpening, verifyOpeningRef, providerOpening, providerUsernameUnavailable, setProviderUsernameUnavailable, factorOpening, resetLocation, setResetLocation, ownerResetOpening, setupAddress, acceptTerms, setAcceptTerms, setupPasswordConfirm, setSetupPasswordConfirm, name, setName, email, setEmail, masked, setMasked, server, profiles, profile, canRemember, remember, setRemember, profileFocus, identityCode, setIdentityCode, identityWrong, setIdentityWrong, identityAddress, invitationIntent, endedTarget, servers, target, retryIn, options, transaction, setTransaction, tvState, tvKind, recovery, setRecovery, recoveryCode, setRecoveryCode, requiredSetup, setRequiredSetup, directoryList, memberMove, moveSnapshot, memberMoveRef, memberOperation, memberFactorOperation, stopMemberMove, pinRecovery, setPinRecovery, invitationPreview, setInvitationPreview, invitationOpening, invitationEntryOpening, invitationScan, invitationScanning, setInvitationScanning, scannedInvitationLink, setScannedInvitationLink, invitationSwitch, retireInvitationSwitch, retireInvitation, nearby, nearbyStatus, setNearbyRefresh, nearbySource, lifetime, directoryAttempt, retryDelay, operation, account, pinOpening, pinRecoveryRef, profileRef, retireRecovery, targetRef, busyRef, stageRef, backRef, registerOpeningRef, providerOpeningRef, registerDraftRev, stageError, contact, factorKind, accountWorkspace, retireAccountWorkspace, downloadsOwner, downloadValue, accountScope, acting, stillPIN, openPIN, run, reset, cancelDirectory, openAccountWorkspace, openInvitationEntry, back, tvDoor, consume, directory, connectDirectory, chooseAnother, startTV, publishPreparedAccount, portico, intentRunner, providerNotice, setProviderNotice, startAccount, tvDoorTried, noCodeNow, beginMemberMove, beginMemberCode, beginEndedMemberCode, createMoveAccount, differentMoveAccount, requestReset, inspect, confirmCode, choose, submitTVFactor, action, renderedTV, codeBack, backAction, failure, passwordInvalid, directoryView, hostedWaiting, offlineRows, allDirectoryOffline, setupTarget, refusal, tvDevice, openLegalLink, openEndedSignInPage, openLocalSignInPage, providerOffered, localNetworkNote, shareDownloadPage};
}
