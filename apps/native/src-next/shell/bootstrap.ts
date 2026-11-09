import {readLibraries, readLibraryDirectoryResponse} from '../../../../packages/client-core/src/query/catalogue-reads';
import {readViewerIdentity} from '../../../../packages/client-core/src/query/connection-operations.ts';
import type {RefObject} from 'react';
import {AppState, Dimensions, Platform, NativeEventEmitter, NativeModules, type View} from 'react-native';
import type {PlaybackFence} from '../../../../packages/player-core/src';
import {NativeScreenLifetime} from '../screens/settings-live/lifetime';
import {sendNativeDeviceReport} from '../screens/settings-live/device-client-report';
import {bindNativeTogether} from '../screens/settings-live/together-playback';
import {audiobookPreferences} from '../../../../packages/client-core/src/audiobook-preferences';
import {routePath, type RouteResponse} from '../../../../packages/contracts/src/routes';
import {HomeService} from '../../../../packages/client-core/src/home-service';
import {viewerQueryScope} from '../../../../packages/client-core/src/query/viewer-queries';
import type {DirectSnapshot} from '../../../../packages/client-core/src/profile-management';
import {IdentityClient} from '../../../../packages/client-core/src/identity-client';
import {currentPreferenceSnapshot, type PreferenceSnapshot} from '../../../../packages/client-core/src/preferences';
import {parseLibraryList, type LibraryDirectoryFences} from '../../../../packages/client-core/src/library-list';
import {createRoleProbe} from './role-probe';
import {LocalSessionRefresher} from '../../../../packages/client-core/src/session-refresh';
import {EventsClient} from '../../../../packages/client-core/src/playback-v1/events';
import {localV1Http} from '../../../../packages/client-core/src/playback-v1/local-http';
import {runEventFeed} from '../../../../packages/client-core/src/event-feed';
import {ambientArtworkSource} from '../../../../packages/client-core/src/artwork';
import {resolveShellMediaUrl} from '../../../../packages/client-core/src/shell-media';
import type {HttpLocalApi, LocalSession, Library} from '../../../../packages/client-core/src';
import {beginRestoreTrace, markRestore} from './restore-trace';
import {noteHomeRead} from './home-read-log';
import {trackNativeConnection} from '../platform/server-connections';
import {NativeProfileGated, nativeProfileGated, type NativeConnected} from '../platform/session-lifecycle-native';
import {nativeRememberedProfile, nativeSessionLifecycle} from '../platform/native-session-lifecycle';
import {nativeManageGate, nativeManageRefused} from '../platform/manage-gate';
import {beginNativeInstallation} from '../platform/installation';
import {NativeOutboxStore} from '../platform/outbox-store';
import {NativeStopStore, nativeStopScope, retainNativeStopScope} from '../platform/user-mutations';
import {NativeQueueStore} from '../platform/queue-storage';
import {bindNativeDeviceSettings, nativeDeviceSettings} from '../platform/device-settings';
import {bindNativeServerPreferences} from '../platform/server-preferences';
import {rememberNativeServerDisplay} from '../platform/server-display';
import {nativeAppSessions, readNativeSelectedViewer} from '../platform/app-session-storage';
import {nativeSignInEnded, rememberNativeSignInHint, type NativeSignInHint, type NativeSignInEnded} from '../platform/sign-in-ended';
import {nativeIdentityFailure, nativeRouteIdentityFailure, type NativeIdentityFailure, type NativeIdentityServer} from '../platform/identity-failure';
import {profilePictureSource} from '../platform/profile-avatars';
import {nativeAmbientSource, type ProfileOption} from '../kit/artwork';
import {LastLibraryMemory} from './library-memory';
import {createNativeShellPlaybackAdapter} from '../platform/playback';
import {nativePlaybackSettings} from './playback-settings';
import {rememberNativePlaybackPreferences} from './remembered-playback-settings';
import {rememberNativeReadingPreferences} from './remembered-reading-settings';
import {LyricsService} from '../../../../packages/client-core/src/lyrics';
import {musicAudioEffects} from '../../../../packages/client-core/src/music-preferences';
import {NativePlayOn} from '../platform/play-on/controller';
import {installationIdentity} from '../../../../packages/client-core/src/installation';
import {authorityKey, type ShellAuthority} from './model';
import {nativeReadRouteAllowed} from '../screens/read/platform';
import {nativeRuntimeRegionText} from '../screens/settings/runtime-region';
import {nativeAppearanceCardSizePercent, nativeAppearanceMotion, nativeAppearanceShowBackdrops, nativeAppearanceTextSize} from '../screens/settings/appearance-policy';
import {bindNativeTopShelfLocale} from '../screens/settings/top-shelf-locale';
import {nativeScreenServices} from '../screens/native-screen-services';
import type {NativeShellRuntime, NativeShellSnapshot} from './runtime';
import {NativeShellNavigation} from './routes';
import {NativeWatchOutlet} from './watch-outlet';
import {NativeShellBootstrapServices, nativeShellDirectViewer, nativeShellPermissionDenied} from './bootstrap-services';
import {createNativeShellAccountBinding} from './account-binding';
import {homeActionDiagnostics} from '../platform/home-action-diagnostics';
import {NativeReadOrder, NativeShellFreshness, nativeQueuedRefresh} from './freshness';
import {retainNativeDownloadsDevice} from '../screens/downloads/device';
import {bindNativeTopShelf} from '../platform/top-shelf';
import {bindNativeAppleActivity} from '../platform/apple-activity';
import {readWatchlist} from '../../../../packages/client-core/src/saved';

export type NativeRememberedProfile = Readonly<{
  /** True while this device opens the profile that is open now without asking. */
  remembered: () => boolean;
  subscribe: (listener: () => void) => () => void;
  /** Clears this device's marker; the open profile stays open. Rejects when the marker could not be cleared. */
  forget: () => Promise<void>;
}>;

export type NativeShellBootstrapContext = Readonly<{
  api: HttpLocalApi;
  home: HomeService;
  events: EventsClient;
  session: () => LocalSession | undefined;
  /** This device's "Remember this profile on this device" choice for the profile open now (the avatar menu's Forget remembered profile). */
  rememberedProfile?: NativeRememberedProfile;
  authority: () => ShellAuthority;
  subscribeAuthority: (listener: () => void) => () => void;
  preferences: () => PreferenceSnapshot;
  subscribePreferences: (listener: () => void) => () => void;
  appVersion: string;
  serverName: string;
  current: () => boolean;
  playback: ReturnType<typeof createNativeShellPlaybackAdapter>;
  watch: NativeWatchOutlet;
  account: DirectSnapshot;
  libraries: () => readonly Library[];
  /** Called after each shell publish, which includes every committed library read; compare the list before acting. */
  subscribeLibraries?: (listener: () => void) => () => void;
  /** Fresh authenticated directory, committed only inside this restored viewer. */
  readLibraries?: (signal: AbortSignal) => Promise<readonly Library[]>;
  navigation: NativeShellNavigation;
}>;
/** Only the integrated family/action owners supply these implementations; never fallback no-ops. */
export type NativeShellBindings = Pick<NativeShellRuntime, 'notice' | 'actions' | 'mixes' | 'musicDiscover' | 'listeningPlaylist' | 'listeningQueuePolicy' | 'listeningSongDetail' | 'selection' | 'setHomeWindow' | 'homeMenuRow' | 'renderSelection' | 'renderRoutes' | 'stacks' | 'renderWatch' | 'libraryMenu' | 'renderDestination' | 'renderHomeEmpty' | 'renderDownloads' | 'openServers' | 'connectionFacts' | 'error' | 'reportError' | 'onDestination' | 'scrollToTop' | 'registerScrollToTop' | 'registerTVPageBack' | 'registerTVRouteBack' | 'registerTVRailBack' | 'onArtworkError' | 'castControl' | 'ownerActivity'> & {
  account: NativeShellSnapshot['account'];
  getState: () => Pick<NativeShellSnapshot, 'destination' | 'railDestination' | 'contentVisible' | 'selectionActive' | 'homeObscured' | 'homeVisible'>;
  attach: (runtime: NativeShellRuntime) => void;
  subscribe: (listener: () => void) => () => void;
  dispose: () => void;
};
export type NativeShellBindingsFactory = (context: NativeShellBootstrapContext) => Promise<NativeShellBindings> | NativeShellBindings;
export type RestoredNativeShellRuntime = NativeShellRuntime & {readonly firstLibraryContext?: NativeShellBootstrapContext; readonly appearanceMotion?: 'follow' | 'reduce'; readonly appearanceTextSize?: 'standard' | 'large'; readonly appearanceCardSizePercent?: 85 | 100 | 125; readonly appearanceShowBackdrops?: boolean; dispose: () => void; refresh: () => Promise<void>};

function cancelled(): Error {return Object.assign(new Error('native_shell_cancelled'), {name: 'AbortError'});}
/** The one automatic re-read of a library directory that did not answer at sign-in (web: LIBRARY_DIRECTORY_RETRY_MS). */
export const NATIVE_LIBRARY_DIRECTORY_RETRY_MS = 2000;
/** P2-61 HOME-RESILIENCE: a slow or failed library directory never stops the shell. It is
 * unavailable, never empty; only an abort or an authority answer (401/403) still stops. */
export function tolerateLibraryDirectory(error: unknown, signal: AbortSignal): undefined {
  const status = (error as {status?: unknown} | null)?.status;
  if (signal.aborted || status === 401 || status === 403 || nativeShellPermissionDenied(error)) throw error;
  return undefined;
}
// CC-03: the server may change this account's role; client-core reconciles it into the
// session, so a role change is a rebuild of role-dependent services, not another viewer.
const sameViewer = (a: LocalSession, b: LocalSession): boolean => a.sessionFamilyId === b.sessionFamilyId && (['serverId', 'accountId', 'profileId', 'authority'] as const).every(key => a.viewer[key] === b.viewer[key]);

/** Uses the existing encrypted native sign-in, signed routes and current membership verification.
 * It registers the persisted installation before restoring credentials; it creates no sign-in UI. */
export async function restoreNativeShellRuntime(makeBindings: NativeShellBindingsFactory, options: {signal?: AbortSignal; installationOwner?: ReturnType<typeof beginNativeInstallation>; adopted?: NativeConnected; onAuthorityEnded: (error?: NativeIdentityFailure | NativeSignInEnded | NativeProfileGated) => void; onSessionRenewed?: () => void}): Promise<RestoredNativeShellRuntime | undefined> {
  // A root may retain its original installation through a failed online restore
  // for a separately receipt-admitted offline opening. Never create a competing
  // generation or dispose an installation borrowed from that root.
  const ownsInstallation = !options.installationOwner;
  const installation = options.installationOwner ?? beginNativeInstallation(options.signal);
  let alive = true, ready = false, api: HttpLocalApi | undefined, home: HomeService | undefined;
  let refresher: LocalSessionRefresher | undefined, bindings: NativeShellBindings | undefined;
  let playback: ReturnType<typeof createNativeShellPlaybackAdapter> | undefined;
  let navigation: NativeShellNavigation | undefined;
  let watch: NativeWatchOutlet | undefined;
  let session: LocalSession | undefined;
  let services: NativeShellBootstrapServices | undefined;
  let snapshot: NativeShellSnapshot | undefined, preferences: PreferenceSnapshot | undefined, account: DirectSnapshot | undefined;
  let avatars: Awaited<ReturnType<IdentityClient['avatars']>> = [], libraries: readonly Library[] = [], librariesUnavailable = false;
  let installationSettings: unknown;
  let originalExpiresAt: string | undefined;
  let signInHint: NativeSignInHint | undefined;
  let identityServer: NativeIdentityServer | undefined;
  const cleanups: (() => void)[] = [], authorityListeners = new Set<() => void>(), preferenceListeners = new Set<() => void>(), listeners = new Set<() => void>();
  const signal = new AbortController();
  let accessorRetirementReported = false;
  homeActionDiagnostics.lifetime?.('bootstrap-begin', signal.signal, installation);
  const dispose = () => {
    if (alive) homeActionDiagnostics.lifetime?.('bootstrap-dispose', signal.signal, installation);
    alive = false; if (ownsInstallation) installation.dispose(); signal.abort(); refresher?.stop(); services?.dispose(); home?.dispose();
    watch?.dispose(); playback?.dispose(); bindings?.dispose(); navigation?.dispose();
    for (const cleanup of cleanups.splice(0)) cleanup();
    api?.getRouteConnection()?.dispose(); api?.setAccessToken(''); session = undefined;
    lastSnapshot = snapshot ?? lastSnapshot; lastPreferences = preferences ?? lastPreferences;
    snapshot = undefined; preferences = undefined; account = undefined; avatars = []; libraries = []; librariesUnavailable = false; installationSettings = undefined;
    for (const listener of [...authorityListeners]) listener();
    authorityListeners.clear(); preferenceListeners.clear(); listeners.clear();
  };
  const end = (error?: NativeIdentityFailure | NativeSignInEnded | NativeProfileGated) => {if (alive) {homeActionDiagnostics.lifetime?.('bootstrap-authority-ended', signal.signal, installation);dispose(); options.onAuthorityEnded(error);}};
  // Role-dependent services (owner pages, inbox audience, privileged actions) restart from the
  // committed selection through the ordinary renewal path; nothing privileged survives a demotion.
  // The viewer's cache is scoped by role (viewerQueryScope), and admin documents live in it: a role
  // change empties it now, before anything restarts, so nothing read under the old role is shown.
  let viewerCache: {dispose(): void} | undefined;
  const rebuild = () => {if (!alive) return; viewerCache?.dispose(); homeActionDiagnostics.lifetime?.('bootstrap-role-changed', signal.signal, installation); if (options.onSessionRenewed) options.onSessionRenewed(); else end();};
  // A refused privileged read after a demotion is a role change, not a sign-out. Ask the
  // server who this viewer is now; only a changed role rebuilds, a proven refusal ends, and a
  // transient failure keeps the shell.
  const endUnlessRoleChanged = createRoleProbe({alive: () => alive, viewer: () => session?.viewer, rebuild, end,
    probe: () => api ? readViewerIdentity<RouteResponse<'get_me'>>(api, signal.signal) : undefined});
  // A PIN change elsewhere (profile_pin_reentry) is neither a sign-out nor a role change: the device
  // asks for this profile's PIN again (C-DESIGN §2). Any other refusal goes through the role probe.
  // A management refusal (profile_pin_manage) ends nothing: the manage gate asks for the PIN.
  const refused = (cause?: unknown) => {if (nativeManageRefused(cause)) return; if (nativeProfileGated(cause)) end(new NativeProfileGated()); else endUnlessRoleChanged();};
  const abort = () => {if (alive) homeActionDiagnostics.lifetime?.('bootstrap-owner-abort', signal.signal, installation);dispose();};
  options.signal?.addEventListener('abort', abort, {once: true});
  cleanups.push(() => options.signal?.removeEventListener('abort', abort));
  // E-tv-21: render reads a retired runtime may still make before RestoredShell unmounts its tree.
  // They get the last frozen values and the disposal runs outside render; async paths still reject.
  let lastSnapshot: NativeShellSnapshot | undefined, lastPreferences: PreferenceSnapshot | undefined;
  const live = () => alive && installation.current() && !options.signal?.aborted && !signal.signal.aborted;
  const retireOutsideRender = () => {if (alive) queueMicrotask(() => {if (alive && !live()) {homeActionDiagnostics.lifetime?.('bootstrap-accessor-retired', signal.signal, installation); dispose();}});};
  const renderRead = <T,>(current: () => T | undefined, last: () => T | undefined): T => {
    if (live()) {const value = current(); if (value !== undefined) return value;}
    else retireOutsideRender();
    const value = last();
    if (value === undefined) throw cancelled();
    return value;
  };
  const assertCurrent = () => {if (!alive || !installation.current() || options.signal?.aborted || signal.signal.aborted) {if (!accessorRetirementReported) {accessorRetirementReported = true;homeActionDiagnostics.lifetime?.('bootstrap-accessor-retired', signal.signal, installation);} dispose(); throw cancelled();}};
  const currentPreferences = () => {assertCurrent(); if (!preferences) throw cancelled(); return preferences;};
  const renderPreferences = () => renderRead(() => preferences ?? undefined, () => lastPreferences ?? preferences);
  const currentAccount = () => {assertCurrent(); if (!account) throw cancelled(); return account;};
  try {
    assertCurrent();
    beginRestoreTrace(); markRestore('installation');
    let original: NonNullable<Awaited<ReturnType<typeof installation.prepare>>['saved']['viewer']>;
    let installed: Awaited<ReturnType<typeof installation.prepare>>['identity'];
    let selected: NonNullable<Awaited<ReturnType<typeof nativeAppSessions.selected>>>;
    let restored: {api: HttpLocalApi; session: LocalSession};
    let env: Awaited<ReturnType<typeof installation.prepare>>['env'];
    // The session lifecycle connects and proves the sign-in (C-DESIGN §3); the shell takes that
    // connection as it is. Opened without one, the shell asks the lifecycle's ports for it, lending
    // its installation so no competing generation begins.
    let adopted = options.adopted;
    if (!adopted) {
      const {saved} = await installation.prepare(); assertCurrent();
      if (!saved.viewer) {dispose(); return undefined;}
      const serverId = saved.viewer.session.viewer.serverId;
      const {ports} = nativeSessionLifecycle();
      ports.lendInstallation(installation);
      markRestore('connection');
      try {adopted = await ports.reconnect(serverId, signal.signal);}
      catch (error) {ports.classifyConnect(error); throw ports.failure(serverId) ?? error;}
      assertCurrent();
      if (!adopted) throw cancelled();
      api = adopted.api;
    }
    const viewer = adopted.prepared.saved.viewer;
    if (!viewer) {dispose(); return undefined;}
    original = viewer; originalExpiresAt = original.session.expiresAt; installed = adopted.prepared.identity; env = adopted.prepared.env;
    nativeShellDirectViewer(original.session.viewer);
    selected = adopted.selected; identityServer = adopted.server; signInHint = adopted.hint;
    restored = {api: adopted.api, session: adopted.session}; api = adopted.api;
    api = restored.api;
    api.setOutboxSession(restored.session);
    markRestore('outbox');
    // The system audio player sends its own stop (platform/user-mutations.ts): it needs to know the
    // sign-in in use, and its finished stops cleared away.
    const stopStore = new NativeStopStore(), stopScope = nativeStopScope(api.baseUrl, api.getOutboxSession());
    if (!stopScope) throw cancelled();
    const reportStopError = (error: unknown) => console.warn('[listening-stop]', error);
    await retainNativeStopScope(stopStore, stopScope, reportStopError); assertCurrent();
    const clearStops = () => {void stopStore.clearFinished().catch(reportStopError);};
    clearStops();
    const nativeStops = NativeModules.PorticoPlaybackModule ? new NativeEventEmitter(NativeModules.PorticoPlaybackModule).addListener('PorticoUserMutationStatus', clearStops) : undefined;
    // What a person does out of reach of the server is kept and sent later; coming back to the app is a moment to try.
    const outbox = api.configureOutbox(new NativeOutboxStore()), outboxApi = api;
    const awake = AppState.addEventListener('change', state => {if (state === 'active') outbox.wake();});
    cleanups.push(() => {nativeStops?.remove(); awake.remove(); outbox.dispose(); outboxApi.setOutboxSession(undefined);});
    api.setDeviceClass(Platform.isTV ? 'television' : 'mobile');
    const local = api;
    trackNativeConnection(local);
    // O-169: signed in, the server gives the owner's home networks; the route connection keeps
    // them, so a VPN or tailnet range the owner added counts as home for routing from now on.
    void local.connection(signal.signal).then(facts => local.getRouteConnection()?.learnSignedInFacts(facts), () => {});
    // Any screen's request refused for a PIN changed elsewhere asks for the PIN, as the shell's own reads do.
    // Only a request sent with this shell's sign-in counts: its first token or one it renewed in
    // place (same viewer). A switch builds a new shell, so a late reply from before can't gate it.
    const ownTokens = new Set([restored.session.accessToken]);
    cleanups.push(local.subscribeProfileGate(({error, accessToken}) => {if (accessToken && ownTokens.has(accessToken)) queueMicrotask(() => refused(error));}));
    // PR-1: a management request refused for the main profile's PIN asks for it; the shell keeps running.
    cleanups.push(local.subscribeManageGate(({accessToken}) => {if (accessToken && ownTokens.has(accessToken)) queueMicrotask(() => {if (alive) nativeManageGate.refused();});}));
    session = restored.session;
    const accountBinding = createNativeShellAccountBinding(selected, session, {
      current: () => alive && installation.current() && !signal.signal.aborted,
      session: () => session, pin: () => local.getRouteConnection()?.pin, readSelected: () => nativeAppSessions.selected(),
    });
    services = new NativeShellBootstrapServices(local, {viewer: session.viewer, deviceClass: Platform.isTV ? 'television' : 'mobile',
      signal: signal.signal, current: () => alive && installation.current(), accountBinding, onAuthorityEnded: cause => refused(cause), onRoleChanged: () => rebuild()});
    const readers = services;
    home = readers.home;
    const documentService = home;
    const identities = new IdentityClient(readers);
    markRestore('first-reads');
    // The feed's position before the first reads: what changes while they are on their way is
    // replayed as hints once the feed runs, instead of being missed until the next change.
    const events = new EventsClient({http: localV1Http(local)});
    await events.prime(signal.signal);
    assertCurrent();
    const [librariesResult, initialAccount, initialAvatars, initialPreferences, server] = await Promise.all([
      readLibraries(readers, signal.signal).then(value => value, error => tolerateLibraryDirectory(error, signal.signal)),
      readers.readAccount(),
      identities.avatars(signal.signal),
      readers.readPreferences(Platform.isTV ? 'television' : 'mobile', signal.signal),
      rememberNativeServerDisplay(local, {serverId: session.viewer.serverId,
        signal: signal.signal, isCurrent: () => alive && installation.current()}),
      (noteHomeRead('restore'), documentService.load()),
    ]);
    assertCurrent();
    const document = documentService.getSnapshot().document;
    if (!document) throw documentService.getSnapshot().error ?? Object.assign(new Error('invalid_home'), {code: 'invalid_home'});
    if (document.serverId !== session.viewer.serverId) throw Object.assign(new Error('identity_mismatch'), {code: 'identity_mismatch'});
    account = initialAccount;
    signInHint = Object.freeze({serverId: selected.connection.pin.serverId, accountId: session.viewer.accountId, familyId: session.sessionFamilyId,
      fingerprint: selected.connection.pin.fingerprint, origin: original.serverUrl, serverName: server.name, username: initialAccount.account.username, signIn: selected.signIn});
    markRestore('save-hint');
    await rememberNativeSignInHint(signInHint).catch(() => {}); assertCurrent();
    avatars = initialAvatars;
    const profile = account.profiles.find(value => value.id === session!.viewer.profileId);
    if (!profile) throw Object.assign(new Error('profile_unavailable'), {code: 'access_refused'});
    preferences = initialPreferences;
    const records = new NativeQueueStore();
    markRestore('settings');
    installationSettings = await records.change('preferences:v1', raw => ({record: raw, value: raw}));
    assertCurrent();
    let unread = 0;
    let viewerFence = document.viewerFence, directoryPrivacy = librariesResult?.fences?.privacyFence;
    const authority: ShellAuthority = Object.freeze({serverId: session.viewer.serverId, accountId: session.viewer.accountId, profileId: session.viewer.profileId,
      // H-11: the whole shell is keyed on this authority. Only a new family, profile, PIN, privacy
      // fence or sign-out may change it; a token renewal sliding the inactivity horizon must not,
      // and neither may a library added or removed. That only moves `viewerFence`, read live by
      // Home-data checks, while this object (and everything keyed on it) stays the same.
      epoch: JSON.stringify([session.sessionFamilyId, profile.revision, profile.pinRevision]), get viewerFence() {return viewerFence;},
      // Who the viewer is and what they may see: the library directory's privacy fence (client
      // data step 5). The key never follows the live viewer fence: a directory that did not answer
      // at start keys the shell on the Home fence it opened with, and its first answer is adopted.
      privacyFence: directoryPrivacy ?? 'opened:' + document.viewerFence});
    /** A later directory answer for a different viewer or what they may see ends this shell. */
    const privacyMoved = (fences: LibraryDirectoryFences | undefined): boolean => {
      if (!fences) return false;
      if (directoryPrivacy === undefined) {directoryPrivacy = fences.privacyFence; return false;}
      return fences.privacyFence !== directoryPrivacy;
    };
    let publishedFence = viewerFence;
    // Subscribed before any Home reader, so every later subscriber already sees the new fence.
    cleanups.push(documentService.subscribe(() => {
      const next = documentService.getSnapshot().document;
      if (alive && next && next.serverId === authority.serverId) viewerFence = next.viewerFence;
    }));
    const scopeIdentity = authorityKey(authority);
    let invalidReported = false;
    const current = () => {
      const installationCurrent = installation.current();
      const valid = alive && installationCurrent && !!session && session.viewer.serverId === authority.serverId && session.viewer.accountId === authority.accountId && session.viewer.profileId === authority.profileId;
      // The accessor is read while ShellSession renders, and end() disposes the
      // runtime (retiring the playback Surface). This DEV-only line names which
      // check failed and the caller stack so the first cause can be fixed rather
      // than deferred. No behaviour changes here.
      if (!valid && alive && typeof __DEV__ !== 'undefined' && __DEV__ && !invalidReported) {
        invalidReported = true;
        console.warn('portico_shell_current_invalid', JSON.stringify({installation: installationCurrent, session: !!session,
          server: session?.viewer.serverId === authority.serverId, account: session?.viewer.accountId === authority.accountId,
          profile: session?.viewer.profileId === authority.profileId, generation: installation.generation}), new Error().stack);
      }
      if (!valid && alive) {homeActionDiagnostics.lifetime?.('bootstrap-current-invalid', signal.signal, installation);end();}
      return valid;
    };
    const profileOption: ProfileOption = Object.freeze({id: profile.id, name: profile.name, initial: [...profile.name.trim()][0] ?? '', color: profile.art, primary: profile.primary, pinRequired: profile.pinRequired});
    libraries = Object.freeze(librariesResult?.items ?? []); librariesUnavailable = librariesResult === undefined;
    let hasLiveTV = false;
    navigation = new NativeShellNavigation(authority, () => authority,
      (route, origin) => nativeReadRouteAllowed(route, libraries, Platform.isTV, origin), {tabStacks: !Platform.isTV});
    const readOrder = {libraries: new NativeReadOrder(), unread: new NativeReadOrder(), guide: new NativeReadOrder(), identity: new NativeReadOrder()};
    const contextBase: Omit<NativeShellBootstrapContext, 'playback' | 'watch'> = {api: local, home: documentService, events, session: () => current() ? session : undefined, rememberedProfile: nativeRememberedProfile(() => current() ? session : undefined), authority: () => authority,
      subscribeAuthority: listener => {authorityListeners.add(listener); return () => {authorityListeners.delete(listener);};},
      preferences: currentPreferences, subscribePreferences: listener => {preferenceListeners.add(listener); return () => {preferenceListeners.delete(listener);};},
      appVersion: installed.appVersion, serverName: server.name, current,
      get account() {return currentAccount();}, libraries: () => current() ? libraries : [], navigation,
      subscribeLibraries: listener => {listeners.add(listener); return () => {listeners.delete(listener);};},
      readLibraries: async requestSignal => {
        assertCurrent();
        const ticket = readOrder.libraries.begin();
        const result = parseLibraryList(await readLibraryDirectoryResponse(readers, requestSignal));
        assertCurrent(); if (requestSignal.aborted) throw cancelled();
        if (privacyMoved(result.fences)) {end(); throw cancelled();}
        const selected = await readNativeSelectedViewer(); assertCurrent();
        if (requestSignal.aborted || !selected || selected.serverUrl !== original.serverUrl || !sameViewer(selected.session, session!)) throw cancelled();
        try {await accountBinding.assertCurrent();}
        catch (error) {if (nativeShellPermissionDenied(error)) refused(error); throw error;}
        assertCurrent(); if (requestSignal.aborted) throw cancelled();
        readOrder.libraries.apply(ticket, () => {libraries = Object.freeze(result.items); librariesUnavailable = false; publish();}); return libraries;
      }};
    let playOn: NativePlayOn | undefined;
    playback = createNativeShellPlaybackAdapter({api: local, authority: contextBase.authority, session: contextBase.session, subscribeAuthority: contextBase.subscribeAuthority, events,
      startFailurePresentation: true,
      captureStartContext: () => {const original = navigation!.getSnapshot();return () => current() && navigation!.getSnapshot() === original;},
      subscribeStartContext: listener => {
        const off = navigation!.subscribe(listener);listeners.add(listener);
        return () => {off();listeners.delete(listener);};
      },
      ...(!Platform.isTV ? {openDestinations: (_fence: PlaybackFence, anchorRef?: RefObject<View | null>) => {playOn?.open(anchorRef); return Promise.resolve();}} : {}),
      settings: () => {
        // The sample-rate match is this device's own engine value, never the profile's.
        let matching: 'smart' | 'strict' | 'off' | undefined;
        try {const value = nativeDeviceSettings(context).read('music.sampleRateMatching'); matching = value === 'smart' || value === 'strict' || value === 'off' ? value : undefined;} catch {matching = undefined;}
        return {...nativePlaybackSettings(currentPreferences().effective, installationSettings), audiobook: audiobookPreferences(currentPreferences()),
          audioEffects: {...musicAudioEffects(currentPreferences()), ...(matching ? {sampleRateMatching: matching} : {})}};
      }, subscribeSettings: contextBase.subscribePreferences, appVersion: contextBase.appVersion});
    playOn = new NativePlayOn({api: local, session: () => current() ? session : undefined,
      deviceName: () => installationIdentity()?.name ?? (Platform.OS === 'ios' ? 'iPhone' : 'Android phone'),
      playback: () => playback, current, reportError: error => {if (current()) console.warn('[playOn]', error);}});
    cleanups.push(() => playOn?.dispose());
    watch = new NativeWatchOutlet(() => current() ? {authority, fence: playback!.getSnapshot().fence, window: Dimensions.get('window')} : undefined);
    cleanups.push(playback.subscribe(watch.invalidate));
    const windowSubscription = Dimensions.addEventListener('change', watch.invalidate);
    cleanups.push(() => windowSubscription.remove());
    const context: NativeShellBootstrapContext = {...contextBase, playback, watch, get account() {return currentAccount();}};
    cleanups.push(bindNativeTogether(context, Platform.isTV));
    const text = nativeRuntimeRegionText(context);
    bindNativeServerPreferences(context, {read: readers.readPreferences, apply: readers.applyPreferences, accept: next => {
      assertCurrent();
      const confirmed = currentPreferenceSnapshot(preferences, next);
      if (confirmed !== preferences) {preferences = confirmed; for (const listener of preferenceListeners) listener();}
      return confirmed;
    }});
    bindNativeDeviceSettings(context, {storage: records, readRecord: () => installationSettings, acceptRecord: raw => {
      assertCurrent(); installationSettings = raw; for (const listener of [...preferenceListeners]) listener();
    }});
    markRestore('bindings');
    bindings = await makeBindings(context); assertCurrent();
    homeActionDiagnostics.libraryMenu('bindings', bindings);
    markRestore('publish');
    const selectedBeforePublish = await readNativeSelectedViewer(); assertCurrent();
    if (!selectedBeforePublish || !sameViewer(selectedBeforePublish.session, session) || selectedBeforePublish.serverUrl !== original.serverUrl) throw cancelled();
    await accountBinding.assertCurrent(); assertCurrent();
    const bound = bindings;
    const publish = () => {
      if (!current()) return;
      const avatar = avatars.find(value => value.profileId === profile.id), picture = profilePictureSource(local, session!.accessToken, avatar, Platform.isTV ? 80 : 90);
      // Frame 28: "Show a dot for new notifications" turns the dot off without hiding the count.
      const badges = preferences?.effective['notifications.badges'];
      snapshot = Object.freeze({authority, ...bound.getState(), mediaExpanded: playback!.getSnapshot().dock.local?.expanded === true, libraries, librariesUnavailable, hasLiveTV, serverName: server.name, unreadNotificationCount: unread,
        profile: {...profileOption, ...(picture && typeof picture === 'object' && 'uri' in picture && picture.uri ? {source: {uri: picture.uri, headers: picture.headers, cacheKey: JSON.stringify([scopeIdentity, avatar?.version, picture.uri])}} : {})}, unreadNotifications: unread > 0 && badges !== false, account: bound.account});
      for (const listener of [...listeners]) listener();
    };
    publish();
    cleanups.push(context.subscribePreferences(publish));
    refresher = new LocalSessionRefresher({env, api: local, session, onRotated: renewed => {
      if (!current() || !sameViewer(session!, renewed)) {end(); return;}
      ownTokens.add(renewed.accessToken);
      local.setOutboxSession(renewed);
      if (renewed.viewer.role !== session!.viewer.role) {session = renewed; rebuild(); return;}
      if (renewed.authorizationHorizon !== session!.authorizationHorizon) {
        // Local renewal slides its inactivity horizon. Re-enter the normal
        // bootstrap from the committed selection instead of treating a new
        // authorization epoch as either signed out or already admitted.
        const localAdvance = selected.signIn === 'local'
          && renewed.installationId === session!.installationId && renewed.deviceId === session!.deviceId
          && BigInt(renewed.tokenGeneration) > BigInt(session!.tokenGeneration)
          && Date.parse(renewed.authorizationHorizon) > Date.parse(session!.authorizationHorizon);
        if (!localAdvance) {end(); return;}
        session = renewed;
        void readNativeSelectedViewer().then(selectedViewer => {
          if (!alive || session !== renewed) return;
          if (!current() || !selectedViewer || selectedViewer.serverUrl !== original.serverUrl
            || !sameViewer(selectedViewer.session, renewed)
            || selectedViewer.session.installationId !== renewed.installationId
            || selectedViewer.session.deviceId !== renewed.deviceId
            || selectedViewer.session.tokenGeneration !== renewed.tokenGeneration
            || selectedViewer.session.authorizationHorizon !== renewed.authorizationHorizon) {end(); return;}
          // H-11: the same family, viewer, installation and device, with a newer token generation
          // and a later horizon, persisted as the committed selection: the session renews in place.
          // Nothing is torn down (no shell remount, no player release): Justin's no-tear-down rule.
          publish();
        }, () => end());
        return;
      }
      // The refresher atomically owns the protected rotating credential. A second blind write
      // to selected-viewer storage could overwrite a newer profile while awaiting the bridge.
      session = renewed; publish();
    }, onSignedOut: error => {if (alive) homeActionDiagnostics.lifetime?.('bootstrap-session-ended', signal.signal, installation);end(nativeSignInEnded(error, signInHint, session?.expiresAt ?? originalExpiresAt));}});
    const refresh = nativeQueuedRefresh(async () => {
      assertCurrent();
      const round = new AbortController(), abortRound = () => round.abort();
      const tickets = {libraries: readOrder.libraries.begin(), unread: readOrder.unread.begin(), guide: readOrder.guide.begin(), identity: readOrder.identity.begin()};
      signal.signal.addEventListener('abort', abortRound, {once: true});
      try {
        const [selection, me, nextLibraries, nextFacts, nextAccount, nextAvatars] = await Promise.all([
          readNativeSelectedViewer(), readViewerIdentity<RouteResponse<'get_me'>>(readers, round.signal),
          readLibraries(readers, round.signal).then(value => value, error => tolerateLibraryDirectory(error, round.signal)), readers.read(round.signal),
          readers.readAccount(round.signal), identities.avatars(round.signal),
        ]);
        assertCurrent();
        const nextProfile = nextAccount.profiles.find(value => value.id === authority.profileId);
        if (!selection || selection.serverUrl !== original.serverUrl || !sameViewer(selection.session, session!)
          || (['accountId', 'profileId', 'serverId', 'authority'] as const).some(key => me.viewer[key] !== session!.viewer[key])
          || !nextProfile || nextProfile.revision !== profile.revision || nextProfile.pinRevision !== profile.pinRevision) {end(); return;}
        if (me.viewer.role !== session!.viewer.role || nextAccount.account.role !== currentAccount().account.role) {rebuild(); return;}
        // Revalidate Home before committing the other private facts. Same-scope read failures
        // retain the entire published body; a changed Home fence retires it in the transport.
        // A sheet over Home still leaves its document on screen; only a hidden Home waits.
        if (AppState.currentState === 'active' && bound.getState().homeVisible && !playback!.getSnapshot().dock.local?.expanded) {
          noteHomeRead('refresh-round'); await documentService.revalidate({signal: round.signal}); assertCurrent();
        } else freshness.invalidateHome();
        const selectedBeforeRefreshPublish = await readNativeSelectedViewer(); assertCurrent();
        if (!selectedBeforeRefreshPublish || selectedBeforeRefreshPublish.serverUrl !== original.serverUrl || !sameViewer(selectedBeforeRefreshPublish.session, session!)) {end(); return;}
        // Observe the native record after all network/identity awaits. An older
        // parallel read must not overwrite a device setting committed meanwhile.
        const nextInstallation = await records.change('preferences:v1', raw => ({record: raw, value: raw})); assertCurrent();
        await accountBinding.assertCurrent(); assertCurrent();
        preferences = currentPreferenceSnapshot(preferences, nextFacts.preferences); installationSettings = nextInstallation;
        // A directory that did not answer keeps the libraries already known (or stays unavailable).
        if (privacyMoved(nextLibraries?.fences)) {end(); return;}
        if (nextLibraries) readOrder.libraries.apply(tickets.libraries, () => {libraries = Object.freeze(nextLibraries.items); librariesUnavailable = false;});
        text.refreshDeviceRegion();
        readOrder.identity.apply(tickets.identity, () => {account = nextAccount; avatars = nextAvatars;});
        readOrder.unread.apply(tickets.unread, () => {unread = nextFacts.unread;});
        readOrder.guide.apply(tickets.guide, () => {hasLiveTV = nextFacts.hasLiveTV;});
        for (const listener of [...preferenceListeners]) listener();
        publish();
      } catch (error) {
        if (nativeShellPermissionDenied(error)) refused(error);
        throw error;
      } finally {round.abort(); signal.signal.removeEventListener('abort', abortRound);}
    }, current);
    const resourceCurrent = async (requestSignal: AbortSignal) => {
      assertCurrent(); if (requestSignal.aborted) throw cancelled();
      await accountBinding.assertCurrent(); assertCurrent();
      if (requestSignal.aborted) throw cancelled();
    };
    const guarded = (read: (requestSignal: AbortSignal) => Promise<void>) => async ({signal: requestSignal}: {signal: AbortSignal}) => {
      try {await resourceCurrent(requestSignal); await read(requestSignal);}
      catch (error) {if (nativeShellPermissionDenied(error)) refused(error); throw error;}
    };
    const freshness = new NativeShellFreshness({
      hasHome: () => !!documentService.getSnapshot().document,
      home: async context => {
        try {
          await resourceCurrent(context.signal);
          noteHomeRead('freshness'); await documentService.revalidate({...context, settle: async () => {await context.settle(); await resourceCurrent(context.signal);}});
        } catch (error) {if (nativeShellPermissionDenied(error)) refused(error); throw error;}
      },
      libraries: guarded(async requestSignal => {
        await contextBase.readLibraries!(requestSignal);
      }),
      preferences: guarded(async requestSignal => {
        const next = await readers.readPreferences(Platform.isTV ? 'television' : 'mobile', requestSignal);
        await resourceCurrent(requestSignal);
        const confirmed = currentPreferenceSnapshot(preferences, next);
        if (confirmed !== preferences) {preferences = confirmed; for (const listener of [...preferenceListeners]) listener(); publish();}
      }),
      unread: guarded(async requestSignal => {
        const ticket = readOrder.unread.begin();
        const next = await readers.readUnread(requestSignal, true); await resourceCurrent(requestSignal);
        readOrder.unread.apply(ticket, () => {unread = next; publish();});
      }),
      guide: guarded(async requestSignal => {
        const ticket = readOrder.guide.begin();
        const next = await readers.readGuideAvailability(requestSignal, true); await resourceCurrent(requestSignal);
        readOrder.guide.apply(ticket, () => {hasLiveTV = next; publish();});
      }),
      identity: guarded(async requestSignal => {
        const ticket = readOrder.identity.begin();
        const [me, nextAccount, nextAvatars] = await Promise.all([
          readViewerIdentity<RouteResponse<'get_me'>>(readers, requestSignal),
          readers.readAccount(requestSignal), identities.avatars(requestSignal),
        ]);
        await resourceCurrent(requestSignal);
        const nextProfile = nextAccount.profiles.find(value => value.id === authority.profileId);
        if ((['accountId', 'profileId', 'serverId', 'authority'] as const).some(key => me.viewer[key] !== session!.viewer[key])
          || !nextProfile || nextProfile.revision !== profile.revision || nextProfile.pinRevision !== profile.pinRevision) {end(); return;}
        if (me.viewer.role !== session!.viewer.role || nextAccount.account.role !== currentAccount().account.role) {rebuild(); return;}
        readOrder.identity.apply(ticket, () => {account = nextAccount; avatars = nextAvatars; publish();});
      }),
    }, {scope: viewerQueryScope(session.viewer, scopeIdentity), api: readers, screensApi: local, writes: local.onConfirmedWrite});
    viewerCache = freshness.queries;
    const updateHomeFreshness = () => {
      if (!current()) return;
      const state = bound.getState();
      freshness.setHomeOpen(state.homeVisible && !playback!.getSnapshot().dock.local?.expanded);
    };
    freshness.setActive(AppState.currentState === 'active'); updateHomeFreshness(); freshness.attach(events);
    // The directory did not answer at sign-in: one automatic try after a short wait; after that the
    // hub's backoff and Home's Try again take over. Nothing blocks meanwhile.
    if (librariesUnavailable) {
      const timer = setTimeout(() => {if (current()) freshness.invalidateLibraries();}, NATIVE_LIBRARY_DIRECTORY_RETRY_MS);
      cleanups.push(() => clearTimeout(timer));
    }
    cleanups.push(() => freshness.dispose(), bound.subscribe(updateHomeFreshness), playback.subscribe(updateHomeFreshness));
    const routeChanged = () => {
      if (!alive) return;
      const route = local.getRouteConnection()?.getSnapshot();
      const failure = route && identityServer ? nativeRouteIdentityFailure(route, identityServer) : undefined;
      if (failure) {end(failure); return;}
      publish();
    };
    cleanups.push(bound.subscribe(publish), local.subscribeRoute(routeChanged), documentService.subscribe(() => {
      const next = documentService.getSnapshot().document;
      if (!ready || !current() || !next) return;
      if (next.serverId !== authority.serverId) {end(); return;}
      // A library added or removed: same shell and player, Home-data checks follow the new fence.
      if (publishedFence !== viewerFence) {publishedFence = viewerFence; publish();}
    }));
    let expanded = playback.getSnapshot().dock.local?.expanded === true;
    cleanups.push(playback.subscribe(() => {const next = playback!.getSnapshot().dock.local?.expanded === true; if (next !== expanded) {expanded = next; publish();}}));
    const refreshQuietly = () => {if (current()) void refresh().catch(error => {if (current()) bound.reportError(error);});};
    const app = AppState.addEventListener('change', value => {
      freshness.setActive(value === 'active');
      if (value === 'active') {refresher?.wake(); refreshQuietly();}
    });
    cleanups.push(() => app.remove(), runEventFeed(events, undefined, {visible: () => AppState.currentState === 'active', onVisibility: listener => {const subscription = AppState.addEventListener('change', listener); return () => subscription.remove();},
      busy: () => !!playback?.getSnapshot().dock.local, onBusy: playback.subscribe}));
    ready = true;
    // Justin's approved startup order: render the admitted Home before its
    // auxiliary badges. Each failed read retries independently in the hub.
    freshness.loadAuxiliary();
    // Preserve only already-admitted canonical settings for later receipt-only
    // playback. A failed optional cache never changes normal admission.
    const rememberPlaybackSettings = () => {
      if (current()) void rememberNativePlaybackPreferences(context, currentPreferences().effective, installationSettings).catch(() => {});
    };
    cleanups.push(context.subscribePreferences(rememberPlaybackSettings)); rememberPlaybackSettings();
    const rememberReadingSettings = () => {
      if (current()) void rememberNativeReadingPreferences(context, currentPreferences()).catch(() => {});
    };
    cleanups.push(context.subscribePreferences(rememberReadingSettings)); rememberReadingSettings();
    const resolve = (path: string) => current() ? resolveShellMediaUrl(local, path) : undefined;
    const skipBackMs = currentPreferences().effective['playback.skipBackMs'];
    if (typeof skipBackMs !== 'number' || !Number.isFinite(skipBackMs) || skipBackMs < 0) throw Object.assign(new Error('invalid_playback_preference'), {code: 'invalid_response'});
    const playbackSkipSeconds = (direction: 'back' | 'forward') => {
      const preferences = renderPreferences();
      if (playback?.getSnapshot().dock.local?.kind === 'audiobook') {
        const selected = audiobookPreferences(preferences);
        return (direction === 'back' ? selected.skipBackMs : selected.skipForwardMs) / 1000;
      }
      // Keep the existing video/music dock step; book settings are independent.
      const value = preferences.effective['playback.skipBackMs'];
      if (typeof value !== 'number' || !Number.isFinite(value) || value < 0) throw new Error('invalid_playback_preference');
      return value / 1000;
    };
    const runtime: RestoredNativeShellRuntime = {...bound, retirementSignal: signal.signal, playback, watch, firstLibraryContext: context, playOn,
      retryLibraries: () => {if (current()) freshness.invalidateLibraries();},
      reachability: Object.freeze({getSnapshot: () => freshness.hub.getReachability(), subscribe: (listener: () => void) => freshness.hub.subscribeReachability(listener)}),
      shareScope: () => current() && session ? Object.freeze({serverId: authority.serverId, hosted: session.viewer.authority === 'hosted', origin: local.baseUrl}) : undefined,
      reportPlaybackProblem: async consent => {
        const lifetime = new NativeScreenLifetime(context);
        try {await sendNativeDeviceReport({context, lifetime}, consent);} finally {lifetime.dispose();}
      }, get appearanceMotion() {return nativeAppearanceMotion(renderPreferences());}, get appearanceTextSize() {return nativeAppearanceTextSize(renderPreferences());}, get appearanceCardSizePercent() {return nativeAppearanceCardSizePercent(renderPreferences());}, get appearanceShowBackdrops() {return nativeAppearanceShowBackdrops(renderPreferences());}, get skipBackSeconds() {return playbackSkipSeconds('back');}, get skipForwardSeconds() {return playbackSkipSeconds('forward');}, home: documentService, memory: new LastLibraryMemory(records), text,
      subscribe: listener => {if (!live()) {retireOutsideRender(); return () => {};} listeners.add(listener); return () => {listeners.delete(listener);};}, getSnapshot: () => renderRead(() => snapshot, () => lastSnapshot ?? snapshot),
      // The real pushed route and revision, for a guarded control that must retire when the page
      // it opened on is left. The navigation snapshot is stable between publishes.
      navigation: Object.freeze({subscribe: context.navigation.subscribe, getSnapshot: context.navigation.getSnapshot, presentationStack: context.navigation.presentationStack}),
      lyrics: target => {
        // A reader exists only for the current local authority; callers ask inside that scope.
        assertCurrent();
        if (!local) throw cancelled();
        return Object.freeze({enabled: currentPreferences().effective['playback.showSyncedLyrics'] !== false,
          service: new LyricsService(local, {serverId: authority.serverId, viewerId: JSON.stringify([authority.serverId, authority.accountId, authority.profileId])}, target)});
      },
      lyricsEnabled: () => current() && currentPreferences().effective['playback.showSyncedLyrics'] !== false,
      artwork: path => {
        if (!path || !current()) return undefined;
        const uri = resolve(path);
        return uri ? {uri, cacheKey: JSON.stringify([scopeIdentity, uri]), headers: {Authorization: `Bearer ${session!.accessToken}`}} : undefined;
      }, ambient: path => current() ? nativeAmbientSource(ambientArtworkSource(path, {serverId: authority.serverId, viewerId: authority.profileId, authorityKey: scopeIdentity}), resolve, {Authorization: `Bearer ${session!.accessToken}`}) : undefined,
      dispose, refresh};
    // Spreading the bindings evaluates their account getter. Keep that private menu
    // behind the live authority too, instead of retaining the copied menu on retirement.
    // A retired runtime keeps showing the menu it last drew until its tree unmounts (E-tv-21).
    let lastAccountMenu: typeof bound.account | undefined;
    Object.defineProperty(runtime, 'account', {get: () => renderRead(() => (lastAccountMenu = bound.account), () => lastAccountMenu)});
    bound.attach(runtime);
    homeActionDiagnostics.libraryMenu('attached', runtime);
    const retireTopShelfLocale = bindNativeTopShelfLocale(context, runtime.text, error => nativeScreenServices(context).reportError(error));
    if (retireTopShelfLocale) cleanups.push(retireTopShelfLocale);
    const retireTopShelf = bindNativeTopShelf({current, baseUrl: local.baseUrl,
      viewer: () => current() && session ? {...session.viewer, installationId: session.installationId} : undefined,
      tokenApi: local,
      subscribePlayback: context.playback.subscribe,
      playbackKey: () => {const fence = context.playback.getSnapshot().fence; return !fence ? undefined : fence.kind === 'channel' ? `channel:${fence.channelId}` : `item:${fence.itemId}`;}},
    error => nativeScreenServices(context).reportError(error));
    if (retireTopShelf) cleanups.push(retireTopShelf);
    // The same Saved scope the Saved pages read with (destinations/lifetime.ts).
    const savedScope = Object.freeze({serverId: authority.serverId, viewerId: JSON.stringify([session.viewer.authority, authority.accountId, authority.profileId])});
    const retireAppleActivity = bindNativeAppleActivity({current,
      home: () => context.home.getSnapshot().document, subscribeHome: context.home.subscribe,
      readSaved: signal => readWatchlist(local, savedScope, signal),
      playing: () => {const state = context.playback.getSnapshot(), entry = state.entry;
        return state.fence && state.fence.kind !== 'channel' && entry && ['movie', 'episode', 'extra'].includes(entry.kind) ? entry : undefined;},
      subscribePlayback: context.playback.subscribe}, error => nativeScreenServices(context).reportError(error));
    if (retireAppleActivity) cleanups.push(retireAppleActivity);
    const downloads = retainNativeDownloadsDevice(context);
    cleanups.push(downloads.dispose);
    void downloads.open().catch(error => {if (current()) bound.reportError(error);});
    markRestore('ready');
    return runtime;
  } catch (error) {const ended = nativeSignInEnded(error, signInHint, session?.expiresAt ?? originalExpiresAt); dispose(); throw nativeIdentityFailure(error, identityServer) ?? ended ?? error;}
}
