import React, {useCallback, useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore} from 'react';
import {StateLayout} from '../kit/states';
import {CoverFan} from '../kit/artwork';
import {Focusable} from '../kit/controls';
import {TVDesignCanvas} from '../kit/overlays/TVDesignCanvas';
import {Icon} from '../kit/Icon';
import {useTheme} from '../kit/theme';
import {presentError} from '../../../../packages/client-core/src/presentation/errors';
import {deviceOnline, onNetworkChange} from '../platform/server-connections';
import {createNativeShellBindings} from './assembly';
import {restoreNativeShellRuntime, type RestoredNativeShellRuntime} from './bootstrap';
import {restoreTrace} from './restore-trace';
import {logBootMilestone} from './boot-timing';
import {negotiationRefusal} from '../platform/negotiation';
import {ServerNeedsUpdate} from '../screens/accounts/ServerNeedsUpdate';
import {NativeShell} from './NativeShell';
import {nativeAccounts} from '../platform/accounts';
import {readNativeSelectedViewer, subscribeNativeAppSessions} from '../platform/app-session-storage';
import {AccountEntry} from '../screens/accounts/AccountEntry';
import {ServersRoute} from '../screens/accounts/AccountRoutes';
import {defaultI18n} from '../../../../packages/i18n/src';
import {NativeSignInEnded} from '../platform/sign-in-ended';
import {NativeIdentityFailure} from '../platform/identity-failure';
import {PhoneFirstLibrary} from '../screens/accounts/PhoneFirstLibrary';
import {ServerIdentityFailure} from '../screens/accounts/ServerIdentityFailure';
import {nativeInvitationLinks, type NativeInvitationLink} from '../platform/invitation-links';
import {nativeShareLinks} from '../platform/share-links';
import {NativeShareLinks} from '../screens/share/NativeShareLinks';
import {beginNativeInstallation} from '../platform/installation';
import {createRememberedDownloadsOpening, type RememberedDownloadsOpening} from './remembered-downloads-opening';
import {RememberedDownloadsScreen, RememberedDownloadsShortcut} from '../screens/downloads/RememberedDownloadsScreen';
import {downloadEntryIdentity} from '../screens/downloads/presentation';
import {AccessibilityInfo, AppState, Image, Platform, Text, View, findNodeHandle} from 'react-native';
import {NativeSettingsAppearance} from '../screens/settings/NativeSettingsAppearance';
import {homeActionDiagnostics} from '../platform/home-action-diagnostics';
import {acknowledgeSecureRecordsRecovered, isSecureRecordsLost, secureRecordsRecoveredNotice} from '../platform/secure-records';
import {SecureRecordsRecovery} from './SecureRecordsRecovery';
import {takeLaunchStartedAt} from '../platform/boot-host';
import {StartupBoot} from './StartupBoot';
import {createNativeStartupRecovery, startupRetrySeconds, startupWait, type NativeStartupWait} from './startup-recovery';

type RestoreState = Readonly<{phase: 'loading' | 'ready' | 'ended'; runtime?: RestoredNativeShellRuntime; error?: unknown}>;
type RecoveryPause = 'accounts' | 'downloads' | 'invitation';
type RootOpening = {attempt: number; active: boolean; describe?: () => Record<string, unknown>; source?: RememberedDownloadsOpening; input?: NativeInvitationLink; finishInput?: () => void; current?: () => boolean; recoveryCurrent?: () => boolean; retire?: () => void; restart?: () => void; retry?: () => boolean; canRecover?: () => boolean; pauseRecovery?: (reason: RecoveryPause) => void; resumeRecovery?: (reason: RecoveryPause) => void};

/** The copy and the captured recovery read the same native path fact. */
const subscribePathChange = (listener: () => void) => onNetworkChange(() => listener());

/** Restore only the captured canonical server selection; current account entry owns sign-in. */
export function RestoredShell() {
  const theme = useTheme();
  const online = useSyncExternalStore(subscribePathChange, deviceOnline, deviceOnline);
  const incoming = useSyncExternalStore(nativeInvitationLinks.subscribe, nativeInvitationLinks.getSnapshot, nativeInvitationLinks.getSnapshot);
  useLayoutEffect(() => nativeInvitationLinks.acquire(), []);
  useLayoutEffect(() => nativeShareLinks.acquire(), []);
  const [attempt, setAttempt] = useState(0), [state, setState] = useState<RestoreState>({phase: 'loading'});
  const [waiting, setWaiting] = useState(() => startupWait(0, online));
  const [firstLibrary, setFirstLibrary] = useState<string>();
  const [serverDisplay, setServerDisplay] = useState<{attempt: number; name?: string}>();
  const [showAccounts, setShowAccounts] = useState(false), [retained, setRetained] = useState(false);
  const [downloads, setDownloads] = useState<RememberedDownloadsOpening>();
  type DownloadView = {source: RememberedDownloadsOpening; serial: number};
  const [downloadView, setDownloadView] = useState<DownloadView>();
  const viewOwner = useRef<DownloadView | undefined>(undefined), viewSerial = useRef(0);
  const [recovered, setRecovered] = useState<{opening: RootOpening; notice: object}>();
  const recoveryTitle = useRef<Text>(null);
  const root = useRef<RootOpening | undefined>(undefined);
  if (root.current && root.current.attempt !== attempt) root.current.retire?.();
  useLayoutEffect(() => {
    const ticket: RootOpening = {attempt, active: true}; root.current = ticket;
    const abort = new AbortController(); let owned: RestoredNativeShellRuntime | undefined;
    const installation = beginNativeInstallation(abort.signal);
    homeActionDiagnostics.lifetime?.('root-begin', ticket, installation);
    let alive = true;
    const ownsRoot = () => alive && ticket.active && root.current === ticket && !abort.signal.aborted;
    const rootCurrent = () => ownsRoot() && installation.current();
    ticket.current = rootCurrent;
    // Reset/restart completion only; never a runtime, server or session admission.
    ticket.recoveryCurrent = ownsRoot;
    let recovery: ReturnType<typeof createNativeStartupRecovery> | undefined;
    const pauses = new Set<RecoveryPause>();
    ticket.retire = () => {
      if (!ticket.active) return;
      homeActionDiagnostics.lifetime?.('root-retire', ticket, installation);
      ticket.active = false; alive = false;
      const previous = owned, previousSource = ticket.source; owned = undefined; ticket.source = undefined;
      if (root.current === ticket) viewOwner.current = undefined;
      recovery?.dispose();
      // Revoke original IO synchronously, before render can expose a successor.
      for (const release of [() => abort.abort(), () => previousSource?.dispose(), () => previous?.dispose()]) {
        try {release();} catch { /* All original capabilities are retired; finish remaining cleanup. */ }
      }
    };
    const restart = () => {
      if (!rootCurrent()) return;
      homeActionDiagnostics.lifetime?.('root-restart', ticket, installation);
      // The callback is captured now; a deferred updater cannot bump a newer root.
      setAttempt(value => root.current === ticket && value === ticket.attempt ? value + 1 : value);
      ticket.retire?.();
    };
    ticket.restart = restart;
    ticket.describe = () => ({owns: ownsRoot(), installation: installation.current(), runtime: !!owned, restored, pauses: [...pauses]});
    const publishState = (next: RestoreState) => {if (rootCurrent()) setState(previous => rootCurrent() ? next : previous);};
    const showRecovered = () => {const notice = secureRecordsRecoveredNotice(); if (notice && rootCurrent()) setRecovered(previous => rootCurrent() ? {opening: ticket, notice} : previous);};
    viewOwner.current = undefined;
    setDownloadView(previous => rootCurrent() ? undefined : previous);
    setDownloads(previous => rootCurrent() ? undefined : previous);
    let fallbackFlight: Promise<void> | undefined;
    const fallback = () => {
      if (fallbackFlight || !rootCurrent() || owned) return;
      let captured: RememberedDownloadsOpening | undefined;
      fallbackFlight = createRememberedDownloadsOpening(installation, {signal: abort.signal, isCurrent: () => rootCurrent() && !owned,
        onOpen: source => {
          if (!rootCurrent() || owned || ticket.source !== source || !source.current() || viewOwner.current) return;
          ticket.pauseRecovery?.('downloads');
          const view = {source, serial: ++viewSerial.current}; viewOwner.current = view; setDownloadView(previous => rootCurrent() && viewOwner.current === view ? view : previous);
        }, onRetired: () => {
          if (!captured || ticket.source !== captured || !rootCurrent()) return;
          ticket.source = undefined; setDownloads(previous => rootCurrent() && previous === captured ? undefined : previous);
          if (viewOwner.current?.source === captured) {viewOwner.current = undefined; setDownloadView(previous => rootCurrent() && previous?.source === captured ? undefined : previous); ticket.resumeRecovery?.('downloads');}
        },
      }).then(source => {captured = source;
        if (!rootCurrent() || owned || source && !source.current()) {source?.dispose(); return;}
        ticket.source = source; setDownloads(previous => rootCurrent() && ticket.source === source ? source : previous);
      }, () => { /* Offline is optional; the original restore refusal remains visible. */ });
    };
    let selection: string | undefined, reads = 0, fenced = false, settledFence = false, restored = false, entrySelectionChanged = false;
    ticket.finishInput = () => {
      if (!rootCurrent()) return;
      if (fenced && settledFence && !owned && !ticket.input?.current()) restart();
      else ticket.resumeRecovery?.('invitation');
    };
    const checkSelection = async () => {
      if (!rootCurrent()) return;
      const read = ++reads;
      try {
        const viewer = await readNativeSelectedViewer();
        if (!rootCurrent() || read !== reads) return;
        const key = viewer ? JSON.stringify([viewer.session.viewer.serverId, viewer.session.viewer.accountId, viewer.session.viewer.profileId, viewer.session.sessionFamilyId]) : '';
        if (selection !== undefined && key !== selection) {
          // A pending restore or a ready runtime must be fenced. Once the entry is settled
          // and owns sign-in, the new selection is its own publication: its onComplete
          // starts the restore, so aborting here would unmount it mid-publish.
          const entryOwnsSelection = pauses.has('accounts') || pauses.has('invitation');
          if (owned || !restored && !entryOwnsSelection) {homeActionDiagnostics.lifetime?.('root-selection-changed', ticket, installation);restart(); return;}
          if (entryOwnsSelection) entrySelectionChanged = true;
        }
        selection = key;
      } catch (error) {if (rootCurrent()) {homeActionDiagnostics.lifetime?.('root-selection-read-failed', ticket, installation);homeActionDiagnostics.error('restore-error', error); restored = true; recovery?.finish(); const previous = owned; owned = undefined; previous?.dispose(); if (rootCurrent()) publishState({phase: 'ended', error});}}
    };
    const unsubscribe = subscribeNativeAppSessions(() => {if (rootCurrent()) void checkSelection();});
    // A ready shell refreshes when a publication settles. While an entry is publishing
    // (no owned runtime yet) the entry's own onComplete starts the restore, so a settled
    // bump here would abort the entry mid-publication and can leave the shell empty.
    const unsettled = nativeAccounts.onSettled(() => {
      if (!rootCurrent()) return;
      if (fenced) settledFence = true;
      // A link opened over the ready shell owns its existing invitation/auth
      // continuation until explicit completion or cancellation, just like an
      // entry mounted without a shell. Do not unmount it mid-publication.
      if (ticket.input?.current()) return;
      if (owned || fenced) {homeActionDiagnostics.lifetime?.('root-account-settled', ticket, installation);restart();}
    });
    // A fence retires the running shell runtime. With no runtime there is nothing to
    // abandon: the entry that is publishing stays mounted and reports completion itself.
    const unfence = nativeAccounts.onFence(() => {
      if (!rootCurrent() || !owned) return;
      homeActionDiagnostics.lifetime?.('root-account-fence', ticket, installation);
      fenced = true; settledFence = false; const previous = owned; owned = undefined; previous.dispose();
      // Keep the original root waiting for the existing settled publication;
      // never start a replacement restore in the middle of account publication.
      if (rootCurrent()) publishState({phase: 'loading'});
    });
    void checkSelection();
    // The first opening starts when the person started the app (the platform's launch time).
    // This opening's clock is never reset by its automatic or manual retries.
    const started = takeLaunchStartedAt();
    if (rootCurrent()) {publishState({phase: 'loading'}); setShowAccounts(previous => rootCurrent() ? false : previous); setServerDisplay(previous => rootCurrent() ? {attempt} : previous);}
    if (rootCurrent()) void nativeAccounts.snapshot().then(value => {if (rootCurrent()) {
      setRetained(previous => rootCurrent() ? value.servers.length > 0 : previous);
      const selected = value.servers.find(server => server.id === value.selectedServerId);
      setServerDisplay(previous => rootCurrent() ? {attempt, ...(selected?.name ? {name: selected.name} : {})} : previous);
    }}, () => {});
    if (rootCurrent()) recovery = createNativeStartupRecovery<RestoredNativeShellRuntime | undefined>({
      startedAt: started, current: rootCurrent, online: deviceOnline(), foreground: AppState.currentState === 'active',
      restore: signal => restoreNativeShellRuntime(createNativeShellBindings, {signal, installationOwner: installation, onSessionRenewed: () => {
        if (rootCurrent() && !signal.aborted) {homeActionDiagnostics.lifetime?.('root-renewal', ticket, installation);restart();}
      }, onAuthorityEnded: error => {
        if (rootCurrent() && !signal.aborted) {homeActionDiagnostics.lifetime?.('root-authority-ended', ticket, installation);homeActionDiagnostics.error('restore-error', error);
          restored = true; recovery?.finish(); owned = undefined; publishState({phase: 'ended', error}); if (rootCurrent()) fallback();}
      }}),
      discard: runtime => runtime?.dispose(),
      // Step names and offsets only (restore-trace.ts): which await a slow restore was still on.
      onAttemptTimeout: () => {try {console.warn(`portico_restore_timeout ${JSON.stringify(restoreTrace())}`);} catch { /* passive */ }},
      retryable: error => !(error instanceof NativeIdentityFailure) && !(error instanceof NativeSignInEnded) && !isSecureRecordsLost(error)
        && ['offline', 'unreachable', 'timeout'].includes(presentError(error, 'home').category),
      onWait: value => {if (rootCurrent()) setWaiting(previous => rootCurrent() ? value : previous);},
      onReady: runtime => {
        if (runtime) logBootMilestone('restored', {restore: restoreTrace()});
        restored = true; owned = runtime;
        // E-tv-21: a runtime that retires is unmounted in the same batch, whoever retired it.
        // A sign-out, renewal or fence has already moved on; anything else restores again.
        // The check waits one microtask: a handler retiring it on purpose (sign-out, renewal,
        // fence) runs in the same call and clears `owned` first. Render reads of the retired
        // runtime meanwhile return its last values, never a throw.
        runtime?.retirementSignal?.addEventListener('abort', () => queueMicrotask(() => {
          if (!rootCurrent() || owned !== runtime) return;
          homeActionDiagnostics.lifetime?.('root-runtime-retired', ticket, installation);
          owned = undefined; publishState({phase: 'loading'}); restart();
        }), {once: true});
        if (runtime) {const previous = ticket.source; ticket.source = undefined; previous?.dispose(); setDownloads(value => rootCurrent() ? undefined : value);}
        publishState({phase: runtime ? 'ready' : 'ended', runtime}); showRecovered(); if (rootCurrent() && !runtime) fallback();
      },
      onError: (error, retrying) => {
        homeActionDiagnostics.error('restore-error', error); restored = !retrying;
        publishState({phase: retrying ? 'loading' : 'ended', error}); showRecovered();
        if (rootCurrent() && !isSecureRecordsLost(error)) fallback();
      },
    });
    ticket.canRecover = () => rootCurrent() && !owned && !pauses.size && !viewOwner.current && !ticket.input?.current();
    // A retained recovery button cannot replace an already-open chooser, link or download.
    ticket.retry = () => !ticket.canRecover?.() || recovery?.retryNow() === true;
    ticket.pauseRecovery = reason => {if (rootCurrent()) {pauses.add(reason); recovery?.pause();}};
    ticket.resumeRecovery = reason => {if (rootCurrent()) {pauses.delete(reason); if (!pauses.size) {
      // Entry owns its publication until completion. If it instead goes Back
      // after changing selection, never resume the former captured installation.
      if (entrySelectionChanged) restart(); else recovery?.resume();
    }}};
    const path = onNetworkChange(value => recovery?.networkChanged(value));
    const app = AppState.addEventListener('change', value => recovery?.foregroundChanged(value === 'active'));
    return () => {ticket.retire?.(); installation.dispose(); unsubscribe(); unfence(); unsettled(); path(); app.remove();};
  }, [attempt]);
  useLayoutEffect(() => {
    const ticket = root.current;
    if (incoming?.current()) ticket?.pauseRecovery?.('invitation');
    else ticket?.resumeRecovery?.('invitation');
  }, [attempt, incoming]);
  const renderedRoot = root.current;
  const renderedCurrent = () => !!renderedRoot?.current?.();
  const focusRecoveryTitle = useCallback(() => {
    if (!renderedRoot?.current?.() || !renderedRoot.canRecover?.()) return;
    const target = recoveryTitle.current && findNodeHandle(recoveryTitle.current);
    if (target != null) AccessibilityInfo.setAccessibilityFocus(target);
  }, [renderedRoot]);
  // A finished sign-in or pick always opens Home from the live root (E-mobile-01): a completion
  // is never dropped because the root that drew the entry has since been replaced or retired.
  const complete = () => {
    const live = root.current;
    try {console.warn(`portico_home_handoff ${JSON.stringify({rendered: live === renderedRoot, ...live?.describe?.(), restore: restoreTrace()})}`);} catch { /* passive */ }
    if (live?.current?.()) {live.restart?.(); return;}
    if (live && !live.recoveryCurrent?.()) return;
    live?.retire?.(); setAttempt(value => value + 1);
  };
  const retry = () => {if (renderedCurrent() && !renderedRoot?.retry?.()) complete();};
  const setupComplete = (serverId: string) => {if (renderedCurrent()) setFirstLibrary(previous => renderedCurrent() ? serverId : previous);};
  const chooseAccounts = () => {if (renderedCurrent() && renderedRoot?.canRecover?.()) {renderedRoot.pauseRecovery?.('accounts'); setShowAccounts(previous => renderedCurrent() ? true : previous);}};
  const serverName = serverDisplay?.attempt === attempt ? serverDisplay.name : undefined;
  const bootHidden = useCallback(() => {logBootMilestone('boot-hidden', {ready: state.phase === 'ready'}); focusRecoveryTitle();}, [focusRecoveryTitle, state.phase]);
  const renderContent = () => {
  // A successful confirmed reset invalidates installation metadata. Only its
  // original closed recovery UI may remain to say restart/reopen; no old shell
  // object is made current by this branch.
  if (isSecureRecordsLost(state.error) && renderedRoot?.attempt === attempt && renderedRoot.recoveryCurrent?.())
    return <SecureRecordsRecovery key={attempt} opening={renderedRoot}/>;
  if (renderedRoot && !renderedCurrent()) return null;
  if (recovered && recovered.opening === renderedRoot && renderedCurrent()) return <StateLayout testID="native-secure-records-recovered"
    title={defaultI18n.t('web.hostedAccount.ended')} body={defaultI18n.t('web.hostedAccount.signInAgain')}
    primaryAction={{id: 'continue', label: defaultI18n.t('action.continue'), onPress: () => {
      if (!renderedCurrent()) return; acknowledgeSecureRecordsRecovered(recovered.notice);
      setRecovered(previous => previous === recovered && renderedCurrent() ? undefined : previous);
    }}}/>;
  if (renderedRoot) renderedRoot.input = incoming?.current() ? incoming : undefined;
  if (incoming?.current()) return <AccountEntry key={'incoming-invitation:' + incoming.id} incomingInvitation={incoming} onSetupComplete={setupComplete}
    onBack={() => {if (!renderedCurrent() || !incoming.current()) return; incoming.dismiss(); renderedRoot?.finishInput?.();}}
    onComplete={() => {if (!renderedCurrent() || !incoming.current()) return; incoming.dismiss(); complete();}}/>;
  const remembered = downloads && root.current?.active && root.current.attempt === attempt && downloads.current() ? downloads : undefined;
  const opened = downloadView;
  if (opened && remembered === opened.source && viewOwner.current === opened) return <RememberedDownloadsScreen key={opened.serial} source={opened.source} onBack={() => {
    if (!renderedCurrent() || viewOwner.current !== opened || !opened.source.current()) return; viewOwner.current = undefined;
    setDownloadView(previous => renderedCurrent() && previous === opened ? undefined : previous);
    renderedRoot?.resumeRecovery?.('downloads');
  }}/>;
  const withDownloads = (content: React.ReactNode, shortcut = true) => shortcut && remembered
    ? <View style={{flex: 1}}>{content}<RememberedDownloadsShortcut source={remembered}/></View> : content;
  if (state.runtime && state.phase === 'ready') return <NativeSettingsAppearance source={state.runtime}><>{firstLibrary && state.runtime.firstLibraryContext?.current() && state.runtime.firstLibraryContext.authority().serverId === firstLibrary && state.runtime.firstLibraryContext.account.account.role === 'owner' && state.runtime.firstLibraryContext.account.account.primaryProfileId === state.runtime.firstLibraryContext.authority().profileId ? <PhoneFirstLibrary context={state.runtime.firstLibraryContext} onDone={() => {if (!renderedCurrent()) return; const runtime = state.runtime;
    setFirstLibrary(previous => renderedCurrent() ? undefined : previous); if (renderedCurrent()) void runtime?.refresh();}}/> : <><NativeShell runtime={state.runtime}/>{!theme.isTV && <NativeShareLinks runtime={state.runtime} online={online !== false} onRestart={complete}/>}</>}</></NativeSettingsAppearance>;
  if (state.phase === 'loading' && waiting.stage === 'boot') return null;
  // A read may still be in flight at 15 s (or 1 s offline). The recovery page is
  // then visible without cancelling or starting a second copy of that read.
  const restoreError = state.error ?? (state.phase === 'loading' ? {code: 'network'} : undefined);
  if (showAccounts || !restoreError && retained) return withDownloads(<ServersRoute fallbackDownloads={remembered?.entry} onSetupComplete={setupComplete} onComplete={complete} onBack={restoreError ? () => {if (renderedCurrent()) {
    setShowAccounts(previous => renderedCurrent() ? false : previous); renderedRoot?.resumeRecovery?.('accounts');
  }} : undefined}/>, false);
  // O-159: a saved server that this app can no longer talk to says so by name.
  const refusal = negotiationRefusal(restoreError);
  if (refusal) return withDownloads(<ServerNeedsUpdate refusal={refusal} onBack={chooseAccounts} backLabel={defaultI18n.t('next.states.switchServer')} testID="native-recovery-server-update"/>);
  if (restoreError instanceof NativeIdentityFailure) {
    const failure = restoreError;
    return withDownloads(<ServerIdentityFailure failure={failure} onRetry={retry} onChoose={chooseAccounts}
      onForget={async () => {if (renderedCurrent()) await nativeAccounts.forgetServer(failure.server);}} onError={error => {if (renderedCurrent()) setState(previous => renderedCurrent() ? {phase: 'ended', error} : previous);}}/>);
  }
  if (restoreError instanceof NativeSignInEnded) return withDownloads(<AccountEntry downloads={remembered?.entry} key={restoreError.hint.serverId + ':' + restoreError.hint.familyId} endedSignIn={restoreError.hint} onSetupComplete={setupComplete} onComplete={complete} onBack={chooseAccounts}/>);
  if (restoreError) {
    const error = presentError(restoreError, 'home', {deviceOnline: online});
    const recoveryWait = state.phase === 'loading' ? waiting : undefined;
    // states.html 4 and 7a: compact phones draw the connection and app-update composition.
    // Identity and sign-in failures were handled above and never reach this branch.
    if (theme.platform === 'phone' && (error.category === 'offline' || error.category === 'unreachable' || error.category === 'timeout'
      || error.category === 'unsupported-version')) {
      return withDownloads(<CompactRecovery presented={error} serverName={serverName} downloads={remembered} waiting={recoveryWait} titleRef={recoveryTitle} onRetry={retry} onChoose={chooseAccounts}/>, false);
    }
    // DESIGN "TV focus": a page's starting focus is its main action. The drawn state layout
    // (connect.html 10) starts on Try again, so the recovery declares it instead of relying
    // on the platform's first focusable after a failed restore.
    const restore = error.action === 'try-again' || error.action === 'refresh';
    const connection = error.category === 'offline' || error.category === 'unreachable' || error.category === 'timeout';
    if (connection) return withDownloads(<View style={{flex: 1, backgroundColor: theme.colors.ink}}><TVDesignCanvas><StateLayout testID="native-recovery-connection" titleRef={recoveryTitle} backdropShape="side" shell={<RecoveryBrand/>}
      title={error.category === 'offline' ? theme.isTV ? defaultI18n.t('next.boot.tvOffline') : defaultI18n.t('next.states.offlinePhone')
        : serverName ? defaultI18n.t('next.states.cantReach', {server: serverName}) : defaultI18n.t('next.states.cantReachServer')}
      icon={error.category === 'offline' ? 'i-wifioff' : 'i-srvoff'} tone="warning"
      beforeActions={recoveryWait ? <RecoveryRetry waiting={recoveryWait}/> : undefined}
      primaryAction={{id: 'restore', label: defaultI18n.t('next.states.tryNow'), icon: 'i-retry', onPress: retry, hasTVPreferredFocus: theme.isTV}}
      secondaryAction={{id: 'accounts', label: defaultI18n.t('next.states.switchServer'), icon: 'i-server', onPress: chooseAccounts}}/></TVDesignCanvas></View>);
    return withDownloads(<View style={{flex: 1, backgroundColor: theme.colors.ink}}><TVDesignCanvas><StateLayout testID="native-recovery-error" titleRef={recoveryTitle} backdropShape="side" shell={<RecoveryBrand/>}
      title={error.title} body={error.body} tone="error"
      primaryAction={restore ? {id: 'restore', label: error.actionLabel, onPress: retry, hasTVPreferredFocus: theme.isTV} : undefined}
      secondaryAction={{id: 'accounts', label: defaultI18n.t('portico.landing.chooseAnother'), onPress: chooseAccounts, hasTVPreferredFocus: theme.isTV && !restore}}/></TVDesignCanvas></View>);
  }
  return withDownloads(<AccountEntry downloads={remembered?.entry} onSetupComplete={setupComplete} onComplete={complete}/>);
  };
  const content = renderContent();
  const boot = content === null && state.phase === 'loading' && waiting.stage === 'boot';
  return <StartupBoot active={boot} waiting={waiting} serverName={serverName} onHidden={bootHidden}>{content}</StartupBoot>;
}

type RecoveryPresented = ReturnType<typeof presentError>;

/** The drawn "Your downloads still play" row: real retained entries, the root's own opening. */
function RecoveryDownloads({source}: {source: RememberedDownloadsOpening}) {
  const theme = useTheme(), t = defaultI18n.t;
  const subscribe = useCallback((listener: () => void) => {
    try {return source.current() ? source.device.subscribe(listener) : () => {};} catch {return () => {};}
  }, [source]);
  const read = useCallback(() => {try {return source.current() ? source.device.getSnapshot() : undefined;} catch {return undefined;}}, [source]);
  const snapshot = useSyncExternalStore(subscribe, read);
  if (!snapshot) return null;
  const entries = snapshot.entries.filter(entry => {try {
    if (!source.current() || entry.state !== 'ready' || source.device.playabilityReason(entry) !== null) return false;
    const kind = entry.kind ?? entry.info?.kind ?? '';
    const admitted = ['movie', 'episode', 'song', 'audiobook_file'].includes(kind) ? !!source.player?.current()
      : ['ebook', 'comic'].includes(kind) ? source.canRead?.(entry) === true : false;
    return admitted && source.current() && source.device.playabilityReason(entry) === null;
  } catch {return false;}});
  if (!entries.length) return null;
  const covers = entries.slice(0, 3).flatMap(entry => entry.artworkUrl
    ? [{uri: entry.artworkUrl, cacheKey: JSON.stringify([downloadEntryIdentity(entry), entry.artworkUrl])}] : []);
  const first = entries[0]!, more = entries.length - 1;
  return <Focusable testID="recovery-downloads" accessibilityRole="button" accessibilityLabel={t('next.states.native.downloadsPlay')} focusRadius={14}
    onPress={() => {if (source.current()) source.entry.open();}}
    style={{minHeight: 74, width: '100%', marginBottom: 14, paddingHorizontal: 14, justifyContent: 'center', borderRadius: 14,
      backgroundColor: `${theme.colors.raised}D9`, borderWidth: 1, borderColor: `${theme.colors.text}14`}}>
    <View style={{flexDirection: 'row', alignItems: 'center', gap: 12}}>
      <View style={{width: 64, height: 50}}>{covers.length ? <CoverFan covers={covers} shape="poster" width={64} height={50}/> : null}</View>
      <View style={{flex: 1}}>
        <Text numberOfLines={1} style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{t('next.states.native.downloadsPlay')}</Text>
        <Text numberOfLines={1} style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{more > 0
          ? t('next.states.native.downloadsMore', {title: first.title, count: more}) : first.title}</Text>
      </View>
      <Icon name="i-right" size={20} color={theme.colors.textTertiary}/>
    </View>
  </Focusable>;
}

/** states.html 4a and 7a, compact phone only. The server name is the saved display; no version line
 * is drawn because the restore error carries no real minimum version, and no store button exists
 * without a real supported update target. */
function CompactRecovery({presented, serverName, downloads, waiting, titleRef, onRetry, onChoose}: {
  presented: RecoveryPresented; serverName?: string; downloads?: RememberedDownloadsOpening; waiting?: NativeStartupWait; titleRef: React.Ref<Text>; onRetry: () => void; onChoose: () => void;
}) {
  const t = defaultI18n.t;
  // The opening can exist with nothing playable on this phone, so the action follows the
  // entry's own stable availability rather than the opening's presence.
  const subscribeDownloads = useCallback((listener: () => void) => downloads ? downloads.entry.subscribe(listener) : () => {}, [downloads]);
  const readDownloads = useCallback(() => downloads ? downloads.entry.getSnapshot().available : false, [downloads]);
  const downloadsAvailable = useSyncExternalStore(subscribeDownloads, readDownloads);
  if (presented.category === 'unsupported-version') {
    return <StateLayout testID="native-recovery-version" icon="i-update" tone="accent" contentTop={286}
      title={t('next.states.native.updateApp')}
      body={serverName ? t(Platform.OS === 'ios' ? 'next.states.native.updateIOSBody' : 'next.states.native.updateAndroidBody', {server: serverName}) : presented.body}
      primaryAction={downloads && downloadsAvailable ? {id: 'downloads', label: t('action.watchDownloads'), icon: 'i-dl',
        onPress: () => {if (downloads.current() && downloads.entry.getSnapshot().available) downloads.entry.open();}} : undefined}
      secondaryAction={{id: 'servers', label: t('next.states.switchServer'), onPress: onChoose}}/>;
  }
  const offline = presented.category === 'offline';
  return <StateLayout testID="native-recovery-connection" titleRef={titleRef} icon={offline ? 'i-wifioff' : 'i-srvoff'} tone="warning" contentTop={offline ? undefined : 208}
    shell={<RecoveryBrand/>}
    title={offline ? t('next.states.offlinePhone') : serverName ? t('next.states.cantReach', {server: serverName}) : t('next.states.cantReachServer')}
    beforeActions={<>{waiting && <RecoveryRetry waiting={waiting}/>}{downloads && <RecoveryDownloads source={downloads}/>}</>}
    primaryAction={{id: 'retry', label: t('next.states.tryNow'), icon: 'i-retry', onPress: onRetry}}
    detailsAction={{id: 'servers', label: t('next.states.switchServer'), onPress: onChoose}} detailsPresentation="link"/>;
}

/** Only the countdown text ticks once per second; the root and ready shell do not. */
function RecoveryRetry({waiting}: {waiting: NativeStartupWait}) {
  const theme = useTheme(), [now, setNow] = useState(Date.now);
  useEffect(() => {
    let active = true, timer: ReturnType<typeof setTimeout> | undefined;
    const tick = () => {
      if (!active) return;
      const at = Date.now(); setNow(at);
      if (waiting.retryAt !== undefined && at < waiting.retryAt) timer = setTimeout(tick, Math.min(1_000, waiting.retryAt - at));
    };
    tick(); return () => {active = false; if (timer !== undefined) clearTimeout(timer);};
  }, [waiting.retryAt]);
  return <Text style={[theme.type.bodyStrong, {color: theme.colors.textSecondary, textAlign: theme.isTV ? 'left' : 'center', marginBottom: theme.isTV ? 40 : 22, fontVariant: ['tabular-nums']}]}>
    {waiting.retryAt === undefined ? defaultI18n.t(waiting.offline && !waiting.pending ? 'error.offlineRetrying' : 'next.states.retrying')
      : defaultI18n.t('next.states.retryIn', {seconds: startupRetrySeconds(waiting.retryAt, now)})}
  </Text>;
}

function RecoveryBrand() {
  const theme = useTheme(), width = theme.isTV ? 174 : 74;
  return <Image accessible={false} source={require('../../assets/wordmark.png')} resizeMode="contain"
    style={{position: 'absolute', left: theme.isTV ? 120 : 24, top: theme.isTV ? 72 : 64, width, height: width * 405 / 1774}}/>;
}
