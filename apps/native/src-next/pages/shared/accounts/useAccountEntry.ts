import {useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState} from 'react';
import {AppState, Linking, NativeModules, Platform, Share} from 'react-native';
import type {ServerSignIn} from '../../../../../../packages/client-core/src/sign-in';
import type {ManagedProfile} from '../../../../../../packages/client-core/src/profile-management';
import {MFARequiredError, type IdentityOptions, type IdentityTransaction} from '../../../../../../packages/client-core/src/hosted-security';
import {presentError} from '../../../../../../packages/client-core/src/presentation/errors';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {negotiationRefusal} from '../../../platform/negotiation';
import {useTheme} from '../../../kit/theme';
import {nativeAccounts} from '../../../platform/accounts';
import type {NativeServerSignIn} from '../../../platform/server-sign-in';
import {NativePorticoSignIn} from '../../../platform/portico-sign-in';
import {useTVMenuCapture} from '../../../platform/tv-menu';
import type {NativeSignInHint} from '../../../platform/sign-in-ended';
import {NativeAccountWorkspace} from '../../../platform/account-workspace';
import {afterEntry} from '../../../shell/after-entry';
import {registerBack} from '../../../kit/navigation/back';
// Named through pages/shared (not `./`) so the screen harness loads entry helpers and sub-hooks for real.
import type {
  AccountEntryProps,
  AccountIntent,
  EntryResult,
  NativeAccountEntryStage,
  PINRecovery,
  ProfilePINOpening,
  RequiredSetup,
  WorkspaceOpening,
} from '../../../pages/shared/accounts/entry/entry-types';
import {
  isPasswordInvalid,
  isProviderOffered,
  profileFace,
  setupElsewhereRefusal,
} from '../../../pages/shared/accounts/entry/entry-helpers';
import {inspectServerOrigin} from '../../../pages/shared/accounts/entry/inspectServer';
import {useNearbyServers} from '../../../pages/shared/accounts/entry/useNearbyServers';
import {useEntryOpenings} from '../../../pages/shared/accounts/entry/useEntryOpenings';
import {useInvitationFlow} from '../../../pages/shared/accounts/entry/useInvitationFlow';
import {useMemberMove} from '../../../pages/shared/accounts/entry/useMemberMove';
import {useDirectoryFlow} from '../../../pages/shared/accounts/entry/useDirectoryFlow';
import {useTVCodeFlow} from '../../../pages/shared/accounts/entry/useTVCodeFlow';

export type {AccountEntryProps, NativeAccountEntryStage, PINRecovery} from '../../../pages/shared/accounts/entry/entry-types';
export {profileFace, setupElsewhereRefusal};

const t = defaultI18n.t;

/** Everything the sign-in flow holds and does: its stages, the server and account operations, and their guards. */
export function useAccountEntry({downloads, incomingInvitation, onBack, onComplete, onSetupComplete, onStageChange, initialStage = 'welcome', initialAddress = '', endedSignIn, resumeAccount}: AccountEntryProps) {
  const latestEndedHint = useRef(endedSignIn); latestEndedHint.current = endedSignIn;
  const preparedAuthSource = useRef<NativePorticoSignIn | undefined>(undefined);
  const [providerLoading, setProviderLoading] = useState<NativePorticoSignIn>();
  const theme = useTheme();
  const [stage, setStage] = useState<NativeAccountEntryStage>(endedSignIn ? 'ended-sign-in' : initialStage);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>();
  const [accountEnded, setAccountEnded] = useState(false);
  const [address, setAddress] = useState(endedSignIn?.origin ?? initialAddress);
  const [username, setUsername] = useState(endedSignIn?.signIn === 'local' ? endedSignIn.username : '');
  const [password, setPassword] = useState('');
  const [code, setCode] = useState('');
  const wrongCodeTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  useEffect(() => () => {if (wrongCodeTimer.current) {clearTimeout(wrongCodeTimer.current); wrongCodeTimer.current = undefined;}}, [stage]);

  const [setupAddress, setSetupAddress] = useState('');
  const [acceptTerms, setAcceptTerms] = useState(false);
  const [setupPasswordConfirm, setSetupPasswordConfirm] = useState('');
  const [name, setName] = useState('');
  const [email, setEmail] = useState('');
  const [masked, setMasked] = useState('');
  const [server, setServer] = useState<ServerSignIn>();
  const [profiles, setProfiles] = useState<readonly ManagedProfile[]>([]);
  const [profile, setProfile] = useState<ManagedProfile>();
  const [canRemember, setCanRemember] = useState(false);
  const [remember, setRemember] = useState(true);
  const profileFocus = useRef(new WeakMap<NativeServerSignIn, string>()).current;
  const [identityCode, setIdentityCode] = useState('');
  const [identityWrong, setIdentityWrong] = useState(false);
  const [identityAddress, setIdentityAddress] = useState('');
  const endedTarget = useRef<Readonly<{serverId: string; fingerprint: string}> | undefined>(undefined);
  const identityOrigin = useRef<string | undefined>(undefined);
  const [appActive, setAppActive] = useState(AppState.currentState === 'active');
  const [options, setOptions] = useState<IdentityOptions>();
  const [transaction, setTransaction] = useState<IdentityTransaction>();
  const [recovery, setRecovery] = useState(false);
  const [recoveryCode, setRecoveryCode] = useState('');
  const [requiredSetup, setRequiredSetup] = useState<RequiredSetup>();
  const [pinRecovery, setPinRecovery] = useState<PINRecovery>();

  const lifetime = useRef(true);
  const operation = useRef<NativeServerSignIn | undefined>(undefined);
  const account = useRef<NativePorticoSignIn | undefined>(undefined);
  const pinOpening = useRef<ProfilePINOpening | undefined>(undefined);
  const pinRecoveryRef = useRef<PINRecovery | undefined>(undefined);
  const profileRef = useRef(profile); profileRef.current = profile;
  const retireRecovery = useCallback((expected = pinRecoveryRef.current) => {
    if (!expected || pinRecoveryRef.current !== expected || expected.retired) return false;
    expected.retired = true; pinRecoveryRef.current = undefined; expected.controller.dispose(); setPinRecovery(undefined); return true;
  }, []);
  const activeRef = useRef(appActive);
  const busyRef = useRef(busy), stageRef = useRef(stage), onBackRef = useRef(onBack), backRef = useRef<() => void>(() => {});
  busyRef.current = busy; stageRef.current = stage; onBackRef.current = onBack;
  const contact = useRef<string | undefined>(undefined), factorKind = useRef<'portico' | 'local'>('local');
  const accountWorkspace = useRef<WorkspaceOpening | undefined>(undefined);
  const retireAccountWorkspace = useCallback(() => {
    const ticket = accountWorkspace.current; accountWorkspace.current = undefined;
    if (ticket) {ticket.active = false; ticket.source?.dispose();}
  }, []);
  const accountScope = useRef<{accountId: string; familyId: string} | undefined>(undefined);
  const acting = useRef(false);

  // Stage-scoped form openings, owner recovery/reset, and downloads observation.
  const openings = useEntryOpenings({stage, transaction, name, setName, downloads, account, factorKind});
  const {retireOwnerRecoveryContact, retireOwnerReset, ownerResetIntent, setResetLocation} = openings;

  // Publish only the public stage before layout observers (including address-stage discovery) collect this commit.
  useLayoutEffect(() => {onStageChange?.(stage);}, [onStageChange, stage]);

  // Nearby LAN server discovery on the `address` stage.
  const {nearby, nearbyStatus, setNearbyRefresh, nearbySource} = useNearbyServers(stage, stageRef);

  // Each visit to a stage is counted; an error is shown only on the visit it was raised in.
  const visit = useRef(0), [errorVisit, setErrorVisit] = useState<number>();
  useLayoutEffect(() => {visit.current++;}, [stage]);
  useLayoutEffect(() => {setErrorVisit(error === undefined ? undefined : visit.current);}, [error]);
  const stageError = error !== undefined && errorVisit === visit.current ? error : undefined;

  const consume = useCallback((result: EntryResult) => {
    if (!lifetime.current) return;
    if (result.kind === 'required-setup') {
      setPassword(''); setCode(''); setRecoveryCode(''); setRecovery(false); setProfiles([]); setProfile(undefined); setCanRemember(false); setRemember(true);
      setTransaction(undefined); setOptions(undefined); setName(''); setEmail(''); setMasked('');
      setIdentityCode(''); setIdentityWrong(false); setIdentityAddress(''); identityOrigin.current = undefined;
      accountScope.current = undefined; contact.current = undefined;
      if (Platform.isTV) {setRequiredSetup(result); setStage('tv-required-setup');}
      else {
        const source = operation.current, entered = stageRef.current;
        void Linking.openURL(new URL('/sign-in', result.serverOrigin).href).catch(err => {
          if (lifetime.current && operation.current === source && stageRef.current === entered) setError(err);
        });
      }
    }
    else if (result.kind === 'profiles') {setServer(operation.current!.serverDescriptor()); setProfiles(result.profiles); setCanRemember(result.canRemember === true); setRemember(true); setStage('profiles');}
    else if (result.kind === 'challenge') {factorKind.current = 'local'; setCode(''); setRecovery(false); setStage('factor');}
    else onComplete();
  }, [onComplete]);

  const publishPreparedAccount = (source: NativePorticoSignIn, available: IdentityOptions, current: () => boolean) => {
    if (!lifetime.current || account.current !== source || !current()) return false;
    preparedAuthSource.current = source; setOptions(available); return true;
  };

  // TV activation code session & approval finalization.
  const directoryRef = useRef<() => Promise<void>>(async () => {});
  const inspectRef = useRef<(origin?: string) => Promise<NativeServerSignIn | undefined>>(async () => undefined);
  const tvFlow = useTVCodeFlow({
    stage, busy, stageError, address, lifetime, activeRef, busyRef, acting, stageRef,
    operation, account, preparedAuthSource, directoryRef, inspectRef, consume,
    setStage, setBusy, setError, setOptions, setPassword, setCode, setRecovery, setRecoveryCode, setRequiredSetup,
  });
  const {tvState, tvKind, tvOpening, adoptingTV, tvDoorTried, noCodeNow, renderedTV, retireTVOpening, startTV, returnToTVCode} = tvFlow;

  // Member move flow.
  const memberMoveFlow = useMemberMove({
    stage, server, endedSignIn, latestEndedHint, lifetime, stageRef, operation, account, preparedAuthSource,
    accountScope, retireOwnerRecoveryContact, retireOwnerReset, retireRecovery, publishPreparedAccount, consume,
    setServer, setPassword, setCode, setStage,
  });
  const {memberMoveRef, memberOperation, memberFactorOperation, disposeMemberMove, stopMemberMove} = memberMoveFlow;

  // Invitation entry, preview, and OS link handling.
  const cancelDirectoryRef = useRef<() => void>(() => {});
  const cancelDirectoryBridge = useCallback(() => cancelDirectoryRef.current(), []);
  const invitationFlow = useInvitationFlow({
    incomingInvitation, lifetime, busyRef, acting, stageRef, operation, account, preparedAuthSource,
    stopMemberMove, cancelDirectory: cancelDirectoryBridge,
    setBusy, setStage, setAddress, setUsername, setCode, setPassword, setSetupPasswordConfirm, setError,
  });
  const {
    invitationIntent, invitationEpoch, invitationOpening, invitationEntryOpening, invitationSwitch,
    retireInvitationEntry, retireInvitationSwitch, retireInvitation, disposeInvitationOnUnmount, setInvitationPreview, openInvitationEntry,
  } = invitationFlow;

  // Portico server directory, connection attempts, and automatic retry countdown.
  const resetRef = useRef<() => void>(() => {});
  const directoryFlow = useDirectoryFlow({
    stage, busy, error, appActive, activeRef, lifetime, busyRef, acting, adoptingTV, stageRef,
    operation, account, accountScope, invitationSwitch, invitationIntent, memberMoveRef, memberOperation,
    resetRef, inspectRef, retireInvitationSwitch, retireOwnerRecoveryContact, retireOwnerReset, retireRecovery,
    consume, setStage, setBusy, setError, setPassword, setServer, setAccountEnded,
  });
  const {
    target, targetRef, retryDelay, directoryAttempt,
    stopRetry, resetDirectory, cancelDirectory, directory, connectDirectory, chooseAnother,
  } = directoryFlow;
  cancelDirectoryRef.current = cancelDirectory;
  directoryRef.current = directory;

  useLayoutEffect(() => {
    lifetime.current = true;
    return () => {
      retireTVOpening(false, undefined, false);
      lifetime.current = false; retireAccountWorkspace(); disposeInvitationOnUnmount();
    };
  }, [disposeInvitationOnUnmount, retireAccountWorkspace, retireTVOpening]);

  useEffect(() => {
    lifetime.current = true;
    return () => {
      lifetime.current = false; retireInvitation(); disposeMemberMove();
      retireOwnerRecoveryContact(); retireOwnerReset(); retireRecovery(); stopRetry();
      operation.current?.dispose(); account.current?.dispose();
    };
  }, [disposeMemberMove, retireInvitation, retireOwnerRecoveryContact, retireOwnerReset, retireRecovery, stopRetry]);

  useEffect(() => {
    const subscription = AppState.addEventListener('change', state => {
      activeRef.current = state === 'active';
      if (!activeRef.current) {
        retireOwnerRecoveryContact(); stopRetry();
        const opening = tvOpening.current;
        if (Platform.isTV && stageRef.current === 'tv' && opening) {
          retireTVOpening(false, opening); stageRef.current = 'welcome'; setStage('welcome');
        }
      }
      setAppActive(activeRef.current);
    });
    return () => subscription.remove();
  }, [retireOwnerRecoveryContact, retireTVOpening, stopRetry, tvOpening]);

  const stillPIN = useCallback((opening: ProfilePINOpening) => lifetime.current && pinOpening.current === opening
    && operation.current === opening.operation && stageRef.current === 'profile-pin', []);
  const openPIN = (next: ManagedProfile, source: NativeServerSignIn) => {
    pinOpening.current = {profile: next, operation: source};
    stageRef.current = 'profile-pin';
    setProfile(next); setCode(''); setError(undefined); setStage('profile-pin');
  };

  const run = useCallback(async (work: () => Promise<void>) => {
    if (busy || acting.current || adoptingTV.current || tvOpening.current?.pending() || !lifetime.current) return;
    acting.current = true; setBusy(true); setError(undefined); setAccountEnded(false);
    const startedInvitationEpoch = invitationEpoch.current;
    const startedPIN = stage === 'profile-pin' ? pinOpening.current : undefined;
    try {await work();} catch (failure) {
      if (lifetime.current && invitationEpoch.current === startedInvitationEpoch && (!startedPIN || stillPIN(startedPIN))) {
        if (endedTarget.current) {setPassword(''); setCode('');}
        if (failure instanceof MFARequiredError) {factorKind.current = 'portico'; setCode(''); setRecovery(false); setStage('factor');}
        else {
          if (stage === 'profile-pin' && Platform.isTV || (Platform.isTV && stage === 'factor' && !recovery)) {
            if (wrongCodeTimer.current) clearTimeout(wrongCodeTimer.current);
            wrongCodeTimer.current = setTimeout(() => {
              wrongCodeTimer.current = undefined;
              if (startedPIN ? stillPIN(startedPIN) : lifetime.current && stageRef.current === stage) setCode('');
            }, 400);
          } else if (stage === 'profile-pin') setCode('');
          setError(failure);
        }
      }
    } finally {
      if (invitationEpoch.current === startedInvitationEpoch) {
        acting.current = false;
        if (lifetime.current) setBusy(adoptingTV.current || tvOpening.current?.pending() === true);
      }
    }
  }, [adoptingTV, busy, invitationEpoch, recovery, stage, stillPIN, tvOpening]);

  const reset = useCallback(() => {
    pinOpening.current = undefined; retireTVOpening(true); retireAccountWorkspace(); retireInvitationEntry(); retireInvitationSwitch();
    invitationIntent.current = undefined; retireInvitation(); ownerResetIntent.current = undefined; endedTarget.current = undefined;
    retireOwnerRecoveryContact(); retireOwnerReset(); resetDirectory(); memberFactorOperation.current = undefined;
    stopMemberMove(); retireRecovery(); stageRef.current = initialStage;
    acting.current = false; preparedAuthSource.current = undefined;
    operation.current?.dispose(); operation.current = undefined; account.current?.dispose(); account.current = undefined;
    setBusy(false); setAccountEnded(false); setPassword(''); setSetupPasswordConfirm(''); setCode('');
    setIdentityCode(''); setIdentityWrong(false); setIdentityAddress(''); identityOrigin.current = undefined;
    setError(undefined); setStage(initialStage);
  }, [initialStage, invitationIntent, memberFactorOperation, ownerResetIntent, resetDirectory, retireAccountWorkspace, retireInvitation, retireInvitationEntry, retireInvitationSwitch, retireOwnerRecoveryContact, retireOwnerReset, retireRecovery, retireTVOpening, stopMemberMove]);
  resetRef.current = reset;

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
    if (!current()) {if (account.current === next) {next.dispose(); account.current = undefined;} return;}
    if (publishPreparedAccount(next, available, current)) {stageRef.current = 'portico'; setStage('portico');}
  };

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

  const tvDoor = Platform.isTV && tvKind === 'portico' && initialStage === 'welcome' && !onBack;
  const tvDoorRef = useRef(false); tvDoorRef.current = tvDoor && stage === 'tv';
  const resume = useRef(() => {}); resume.current = () => {if (lifetime.current && stageRef.current === 'welcome' && !busyRef.current && !acting.current) void run(portico);};
  const resumeWelcome = resumeAccount && (!tvDoor || stage === 'welcome' && appActive && !stageError);
  useEffect(() => {
    if (!resumeWelcome) return;
    let active = true; const credentials = nativeAccounts.accountCredentials();
    void credentials.synchronize().then(() => {if (active && (credentials.getSnapshot().session || tvDoor)) resume.current();}, () => {});
    return () => {active = false;};
  }, [resumeWelcome, tvDoor]);

  const requestReset = async () => {
    const source = account.current;
    if (!source) return;
    const originalStage = stageRef.current;
    const current = () => lifetime.current && account.current === source && stageRef.current === originalStage;
    await source.forgot(current);
    if (current()) {setPassword(''); setCode(''); setStage('portico');}
  };

  const inspect = (origin = address) => inspectServerOrigin({
    origin, lifetime, stageRef, operation, endedTarget, identityOrigin,
    ownerResetIntent, ownerResetOpening: openings.ownerResetOpening,
    invitationIntent, invitationOpening, invitationEntryOpening, invitationSwitch,
    retireOwnerRecoveryContact, retireOwnerReset, retireRecovery, startTV,
    setStage, setServer, setIdentityAddress, setIdentityCode, setIdentityWrong, setSetupAddress,
    setName, setUsername, setPassword, setSetupPasswordConfirm, setCode, setResetLocation, setInvitationPreview,
  });
  inspectRef.current = inspect;

  const confirmCode = async (value = identityCode) => {
    const matched = await operation.current!.confirmIdentityCode(value);
    if (!lifetime.current) return;
    if (!matched) {setIdentityWrong(true); return;}
    const capturedOrigin = identityOrigin.current; if (!capturedOrigin) return;
    setIdentityCode(''); setIdentityWrong(false); await inspect(capturedOrigin);
  };
  const choose = async (id: string, enteredPin?: string) => {await operation.current!.chooseProfile(id, enteredPin, canRemember && remember); onComplete();};
  const submitTVFactor = async (value: string) => {
    if (factorKind.current === 'portico') {await account.current!.factor(value); await directory();}
    else consume(await (operation.current!.directoryChallengePending() ? operation.current!.completeDirectorySecondFactor(value) : operation.current!.completeSecondFactor(value)));
  };

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
    if (stage === 'local' && !Platform.isTV) {const kept = address; reset(); setAddress(kept); stageRef.current = 'address'; setStage('address'); return;}
    if (stage === 'member-move') {stopMemberMove(); setStage(server?.signIn === 'local' ? 'local' : 'uses-portico'); return;}
    if (stage === 'tv-move-handoff') {setStage('uses-portico'); return;}
    if (pinRecoveryRef.current) {retireRecovery(); setCode(''); return;}
    if (stage === 'servers' && target) {cancelDirectory(); return;}
    if (stage === 'profile-forgot-pin' && profile && operation.current) {openPIN(profile, operation.current); return;}
    if (stage === 'forgot' || stage === 'forgot-sent') {setPassword(''); setError(undefined); setStage('portico'); return;}
    if (stage === 'profile-pin') {
      pinOpening.current = undefined; stageRef.current = 'profiles';
      setCode(''); setProfile(undefined); setError(undefined); setStage('profiles'); return;
    }
    if (Platform.isTV && (stage === 'tv-credentials' || stage === 'factor' || stage === 'tv-required-setup')) {void run(returnToTVCode); return;}
    if (stage === initialStage) onBack?.(); else reset();
  };
  backRef.current = back;

  useEffect(() => {
    const listener = registerBack(() => {
      if (stageRef.current === 'tv' && !adoptingTV.current) {
        if (tvDoorRef.current && !busyRef.current) return false;
        tvDoorTried.current = false; backRef.current(); return true;
      }
      if (busyRef.current || adoptingTV.current) return true;
      if (stageRef.current === 'welcome' && !onBackRef.current) return false;
      backRef.current(); return true;
    }, {scope: 'surface'});
    return () => listener();
  }, [adoptingTV, tvDoorTried]);
  useTVMenuCapture(Platform.isTV && (stage !== 'welcome' || !!onBack) && !(stage === 'tv' && tvDoor));

  const action = (id: string, label: string, work: () => Promise<void>, disabled = false) => ({id, label, disabled: busy || disabled, loading: busy, onPress: () => {void run(work);}});
  const codeBack = () => {if (renderedTV?.current()) back();};
  const backAction = stage !== 'welcome' || onBack ? {id: 'back', label: t('action.back'), disabled: busy, onPress: stage === 'tv' ? codeBack : back} : undefined;
  const failure = stageError ? presentError(stageError, stage === 'profile-pin' ? 'profiles' : 'sign-in') : undefined;
  const passwordInvalid = isPasswordInvalid(stage, stageError);
  const setupTarget = useMemo(() => server && setupAddress ? Object.freeze({origin: setupAddress, server, expected: endedTarget.current}) : undefined, [server, setupAddress]);
  const refusal = negotiationRefusal(error);
  const tvDevice = Platform.isTV, localNetworkNote = Platform.OS === 'ios';
  const openLegalLink = (url: string) => {void run(async () => {await Linking.openURL(url);});};
  const openEndedSignInPage = (hint: NativeSignInHint) => {void run(async () => {await Linking.openURL(new URL('/sign-in', hint.origin).href);});};
  const openLocalSignInPage = (source: NativeServerSignIn) => {
    void run(async () => {if (source === operation.current && lifetime.current && stageRef.current === 'local') await Linking.openURL(new URL('/sign-in', source.serverOrigin()).href);});
  };
  const providerOffered = (value: IdentityOptions['providers'][number]) => isProviderOffered(value, NativeModules.PorticoAccountAuth);
  const shareDownloadPage = async () => {await Share.share({message: 'https://getportico.tv/download/'});};

  return {
    latestEndedHint, preparedAuthSource, providerLoading, setProviderLoading, theme, stage, setStage, busy, error, setError,
    accountEnded, address, setAddress, username, setUsername, password, setPassword, code, setCode, wrongCodeTimer,
    ...openings,
    setupAddress, acceptTerms, setAcceptTerms, setupPasswordConfirm, setSetupPasswordConfirm,
    name, setName, email, setEmail, masked, setMasked, server, profiles, profile, canRemember, remember, setRemember,
    profileFocus, identityCode, setIdentityCode, identityWrong, setIdentityWrong, identityAddress,
    ...invitationFlow,
    endedTarget,
    servers: directoryFlow.servers, target, retryIn: directoryFlow.retryIn, options, transaction, setTransaction,
    tvState, tvKind, recovery, setRecovery, recoveryCode, setRecoveryCode, requiredSetup, setRequiredSetup,
    directoryList: directoryFlow.directoryList,
    ...memberMoveFlow,
    pinRecovery, setPinRecovery,
    nearby, nearbyStatus, setNearbyRefresh, nearbySource,
    lifetime, directoryAttempt, retryDelay, operation, account, pinOpening, pinRecoveryRef, profileRef, retireRecovery,
    targetRef, busyRef, stageRef, backRef, stageError, contact, factorKind, accountWorkspace, retireAccountWorkspace,
    accountScope, acting, stillPIN, openPIN, run, reset, cancelDirectory, openAccountWorkspace,
    back, tvDoor, consume, directory, connectDirectory, chooseAnother, startTV, publishPreparedAccount, portico,
    intentRunner, providerNotice, setProviderNotice, startAccount, tvDoorTried, noCodeNow,
    requestReset, inspect, confirmCode, choose, submitTVFactor, action, renderedTV, codeBack, backAction, failure,
    passwordInvalid, directoryView: directoryFlow.directoryView, hostedWaiting: directoryFlow.hostedWaiting,
    offlineRows: directoryFlow.offlineRows, allDirectoryOffline: directoryFlow.allDirectoryOffline,
    setupTarget, refusal, tvDevice, openLegalLink, openEndedSignInPage, openLocalSignInPage, providerOffered,
    localNetworkNote, shareDownloadPage,
  };
}

export type AccountEntryView = ReturnType<typeof useAccountEntry>;
