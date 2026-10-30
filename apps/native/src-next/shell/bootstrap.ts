import type {RefObject} from 'react';
import {AppState, Dimensions, Platform, NativeEventEmitter, NativeModules, type View} from 'react-native';
import type {PlaybackFence} from '../../../../packages/player-core/src';
import {NativeScreenLifetime} from '../screens/settings-live/lifetime';
import {sendNativeDeviceReport} from '../screens/settings-live/device-client-report';
import {bindNativeTogether} from '../screens/settings-live/together-playback';
import {audiobookPreferences} from '../../../../packages/client-core/src/audiobook-preferences';
import {routePath, type RouteResponse} from '../../../../packages/contracts/src/routes';
import {HomeService} from '../../../../packages/client-core/src/home-service';
import {homeShellFence} from '../../../../packages/client-core/src/home';
import type {DirectSnapshot} from '../../../../packages/client-core/src/profile-management';
import {IdentityClient} from '../../../../packages/client-core/src/identity-client';
import {currentPreferenceSnapshot, type PreferenceSnapshot} from '../../../../packages/client-core/src/preferences';
import {parseLibraryList} from '../../../../packages/client-core/src/library-list';
import {createRoleProbe} from './role-probe';
import {purgeStaleRoutes} from '../platform/stale-routes';
import {drainRevocations} from '../platform/session-revocation';
import {LocalSessionRefresher} from '../../../../packages/client-core/src/session-refresh';
import {EventsClient} from '../../../../packages/client-core/src/playback-v1/events';
import {localV1Http} from '../../../../packages/client-core/src/playback-v1/local-http';
import {runEventFeed} from '../../../../packages/client-core/src/event-feed';
import {ambientArtworkSource} from '../../../../packages/client-core/src/artwork';
import type {HttpLocalApi, LocalSession, Library} from '../../../../packages/client-core/src';
import {beginRestoreTrace, markRestore} from './restore-trace';
import {noteHomeRead} from './home-read-log';
import {restoreNativeConnection, readNativeSelectedViewer, trackNativeConnection} from '../platform/session';
import {beginNativeInstallation} from '../platform/installation';
import {NativeStopResultConsumer} from '../platform/native-stop-consumer';
import {NativeUserMutationStore,retainNativeUserMutationSelection} from '../platform/user-mutations';
import {NativeQueueStore} from '../platform/queue-storage';
import {bindNativeDeviceSettings, nativeDeviceSettings} from '../platform/device-settings';
import {bindNativeServerPreferences} from '../platform/server-preferences';
import {rememberNativeServerDisplay} from '../platform/server-display';
import {readNativeServerDisplays} from '../platform/server-display';
import {nativeAppSessions, nativeAppSessionStorage} from '../platform/app-session-storage';
import {nativeAccounts} from '../platform/accounts';
import {hostedUrl} from '../platform/account-authority';
import {nativeSignInEnded, rememberNativeSignInHint, type NativeSignInHint, type NativeSignInEnded, readNativeSignInHint} from '../platform/sign-in-ended';
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

export type NativeShellBootstrapContext = Readonly<{
  api: HttpLocalApi;
  home: HomeService;
  events: EventsClient;
  session: () => LocalSession | undefined;
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
  /** Fresh authenticated directory, committed only inside this restored viewer. */
  readLibraries?: (signal: AbortSignal) => Promise<readonly Library[]>;
  navigation: NativeShellNavigation;
}>;
/** Only the integrated family/action owners supply these implementations; never fallback no-ops. */
export type NativeShellBindings = Pick<NativeShellRuntime, 'notice' | 'actions' | 'mixes' | 'musicDiscover' | 'listeningPlaylist' | 'listeningQueuePolicy' | 'listeningSongDetail' | 'selection' | 'setHomeWindow' | 'homeMenuRow' | 'renderSelection' | 'renderRoutes' | 'stacks' | 'renderWatch' | 'libraryMenu' | 'renderDestination' | 'renderHomeEmpty' | 'renderDownloads' | 'renderPendingChanges' | 'openServers' | 'connectionFacts' | 'error' | 'reportError' | 'onDestination' | 'scrollToTop' | 'registerScrollToTop' | 'registerTVPageBack' | 'registerTVRailBack' | 'onArtworkError' | 'castControl'> & {
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
/** Queued sign-outs start this long after a restore, never during the reads that open Home. */
export const REVOCATION_DRAIN_DELAY_MS = 10_000;
export async function restoreNativeShellRuntime(makeBindings: NativeShellBindingsFactory, options: {signal?: AbortSignal; installationOwner?: ReturnType<typeof beginNativeInstallation>; onAuthorityEnded: (error?: NativeIdentityFailure | NativeSignInEnded) => void; onSessionRenewed?: () => void}): Promise<RestoredNativeShellRuntime | undefined> {
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
  const end = (error?: NativeIdentityFailure | NativeSignInEnded) => {if (alive) {homeActionDiagnostics.lifetime?.('bootstrap-authority-ended', signal.signal, installation);dispose(); options.onAuthorityEnded(error);}};
  // Role-dependent services (owner pages, inbox audience, privileged actions) restart from the
  // committed selection through the ordinary renewal path; nothing privileged survives a demotion.
  const rebuild = () => {if (!alive) return; homeActionDiagnostics.lifetime?.('bootstrap-role-changed', signal.signal, installation); if (options.onSessionRenewed) options.onSessionRenewed(); else end();};
  // A refused privileged read after a demotion is a role change, not a sign-out. Ask the
  // server who this viewer is now; only a changed role rebuilds, a proven refusal ends, and a
  // transient failure keeps the shell.
  const endUnlessRoleChanged = createRoleProbe({alive: () => alive, viewer: () => session?.viewer, rebuild, end,
    probe: () => api ? api.request<RouteResponse<'get_me'>>(routePath('get_me'), 'GET', undefined, signal.signal) : undefined});
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
    const {saved, connection, env, identity: installed} = await installation.prepare(); assertCurrent();
    if (!saved.viewer) {dispose(); return undefined;}
    const original = saved.viewer; originalExpiresAt = original.session.expiresAt;
    nativeShellDirectViewer(original.session.viewer);
    // E-mobile-01: routes to the retired `c-…` direct names can never answer; forget them once.
    markRestore('sessions');
    const beforePurge = await nativeAppSessions.read(); assertCurrent();
    if (purgeStaleRoutes(beforePurge) !== beforePurge) {await nativeAppSessionStorage.change(record => purgeStaleRoutes(record)).catch(() => {}); assertCurrent();}
    markRestore('selected');
    const selected = await nativeAppSessions.selected(); assertCurrent();
    if (!selected || !sameViewer(original.session, selected.connection.session) || selected.connection.logicalOrigin !== original.serverUrl) throw cancelled();
    markRestore('server-name');
    const [display] = await readNativeServerDisplays([selected]); assertCurrent();
    identityServer = Object.freeze({serverId: selected.connection.pin.serverId, familyId: selected.connection.session.sessionFamilyId,
      fingerprint: selected.connection.pin.fingerprint, signIn: selected.signIn, name: display.name});
    markRestore('sign-in-hint');
    signInHint = await readNativeSignInHint(original.session, original.serverUrl, selected.connection.pin.fingerprint, selected.signIn).catch(() => undefined); assertCurrent();
    // A server-local session renews locally even while Portico Services is unreachable.
    const hostedCredentials = connection?.hosted?.origin === hostedUrl ? () => nativeAccounts.accountCredentials() : undefined;
    markRestore('connection');
    const restored = await restoreNativeConnection(connection?.hosted?.origin ?? original.serverUrl, hostedCredentials, original.session, env, signal.signal);
    api = restored?.api; assertCurrent();
    // Sessions this device replaced are ended over the route that is live now (bounded, background).
    const liveRoute = api?.getRouteConnection();
    // Queued sign-outs wait until Home has had its turn: they share the session-refresh lock and
    // this route with the reads that open Home (E-mobile-01).
    if (liveRoute) {const drain = setTimeout(() => {if (!signal.signal.aborted) void drainRevocations(liveRoute.pin.serverId, liveRoute).catch(() => {});}, REVOCATION_DRAIN_DELAY_MS); signal.signal.addEventListener('abort', () => clearTimeout(drain), {once: true});}
    if (!restored || !sameViewer(original.session, restored.session)) throw Object.assign(new Error('native_viewer_changed'), {code: 'identity_mismatch'});
    markRestore('viewer');
    const reread = await readNativeSelectedViewer(); assertCurrent();
    if (!reread || reread.serverUrl !== original.serverUrl || !sameViewer(reread.session, restored.session)) throw cancelled();
    api = restored.api;
    api.setUserMutationSession(restored.session);
    const mutationStore = new NativeUserMutationStore();
    let reportMutationError = (error: unknown) => console.warn('[user-mutations]', error);
    const mutationScope=api.getUserMutationScope();if(!mutationScope)throw cancelled();
    markRestore('mutations');
    await retainNativeUserMutationSelection(mutationStore,mutationScope,error=>reportMutationError(error)); assertCurrent();
    const mutations = api.configureUserMutations(mutationStore);
    const mutationApi = api;
    const stopConsumer=new NativeStopResultConsumer({coordinator:mutations,scope:mutationApi.getUserMutationScope,
      consumeLocal:async key=>{const bridge=NativeModules.PorticoPlaybackModule;if(typeof bridge?.consumeListeningStop!=='function')throw Object.assign(new Error('mutation_consumer_unavailable'),{code:'mutation_consumer_unavailable'});return await bridge.consumeListeningStop(key)===true;},
      consumeResult:key=>mutationApi.consumeUserMutation(key),report:error=>reportMutationError(error)});
    const nativeMutations = NativeModules.PorticoPlaybackModule ? new NativeEventEmitter(NativeModules.PorticoPlaybackModule).addListener('PorticoUserMutationStatus', (status: {state?: string; code?: string}) => {
      if (['not_queued', 'review', 'quarantined'].includes(status.state ?? '')) reportMutationError(Object.assign(new Error(status.code ?? 'mutation_not_queued'), {code: status.code ?? 'mutation_not_queued'}));
      void mutations.reconcile().catch(reportMutationError);
    }) : undefined;
    cleanups.push(() => {nativeMutations?.remove(); stopConsumer.dispose(); mutations.dispose(); mutationApi.setUserMutationSession(undefined);});
    api.setDeviceClass(Platform.isTV ? 'television' : 'mobile');
    const local = api;
    trackNativeConnection(local);
    session = restored.session;
    const accountBinding = createNativeShellAccountBinding(selected, session, {
      current: () => alive && installation.current() && !signal.signal.aborted,
      session: () => session, pin: () => local.getRouteConnection()?.pin, readSelected: () => nativeAppSessions.selected(),
    });
    services = new NativeShellBootstrapServices(local, {viewer: session.viewer, deviceClass: Platform.isTV ? 'television' : 'mobile',
      signal: signal.signal, current: () => alive && installation.current(), accountBinding, onAuthorityEnded: () => endUnlessRoleChanged(), onRoleChanged: () => rebuild()});
    const readers = services;
    home = readers.home;
    const documentService = home;
    const identities = new IdentityClient(readers);
    markRestore('first-reads');
    const [librariesResult, initialAccount, initialAvatars, initialPreferences, server] = await Promise.all([
      readers.request(routePath('get_libraries'), 'GET', undefined, signal.signal).then(parseLibraryList).then(value => value.items, error => tolerateLibraryDirectory(error, signal.signal)),
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
    let viewerFence = document.viewerFence;
    const authority: ShellAuthority = Object.freeze({serverId: session.viewer.serverId, accountId: session.viewer.accountId, profileId: session.viewer.profileId,
      // H-11: the whole shell is keyed on this authority. Only a new family, profile, PIN, privacy
      // fence or sign-out may change it; a token renewal sliding the inactivity horizon must not,
      // and neither may a library added or removed. That only moves `viewerFence`, read live by
      // Home-data checks, while this object (and everything keyed on it) stays the same.
      epoch: JSON.stringify([session.sessionFamilyId, profile.revision, profile.pinRevision]), get viewerFence() {return viewerFence;},
      ...(document.privacyFence !== undefined ? {privacyFence: document.privacyFence} : {})});
    let publishedFence = viewerFence;
    // Subscribed before any Home reader, so every later subscriber already sees the new fence.
    cleanups.push(documentService.subscribe(() => {
      const next = documentService.getSnapshot().document;
      if (alive && next && next.serverId === authority.serverId && homeShellFence(next) === (authority.privacyFence ?? viewerFence)) viewerFence = next.viewerFence;
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
    libraries = Object.freeze(librariesResult ?? []); librariesUnavailable = librariesResult === undefined;
    let hasLiveTV = false;
    const events = new EventsClient({http: localV1Http(local)});
    navigation = new NativeShellNavigation(authority, () => authority,
      (route, origin) => nativeReadRouteAllowed(route, libraries, Platform.isTV, origin), {tabStacks: !Platform.isTV});
    const readOrder = {libraries: new NativeReadOrder(), unread: new NativeReadOrder(), guide: new NativeReadOrder(), identity: new NativeReadOrder()};
    const contextBase: Omit<NativeShellBootstrapContext, 'playback' | 'watch'> = {api: local, home: documentService, events, session: () => current() ? session : undefined, authority: () => authority,
      subscribeAuthority: listener => {authorityListeners.add(listener); return () => {authorityListeners.delete(listener);};},
      preferences: currentPreferences, subscribePreferences: listener => {preferenceListeners.add(listener); return () => {preferenceListeners.delete(listener);};},
      appVersion: installed.appVersion, serverName: server.name, current,
      get account() {return currentAccount();}, libraries: () => current() ? libraries : [], navigation,
      readLibraries: async requestSignal => {
        assertCurrent();
        const ticket = readOrder.libraries.begin();
        const result = parseLibraryList(await readers.request(routePath('get_libraries'), 'GET', undefined, requestSignal));
        assertCurrent(); if (requestSignal.aborted) throw cancelled();
        const selected = await readNativeSelectedViewer(); assertCurrent();
        if (requestSignal.aborted || !selected || selected.serverUrl !== original.serverUrl || !sameViewer(selected.session, session!)) throw cancelled();
        try {await accountBinding.assertCurrent();}
        catch (error) {if (nativeShellPermissionDenied(error)) endUnlessRoleChanged(); throw error;}
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
    reportMutationError = error => bound.reportError(error);
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
      local.setUserMutationSession(renewed);
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
          readNativeSelectedViewer(), readers.request<RouteResponse<'get_me'>>(routePath('get_me'), 'GET', undefined, round.signal),
          readers.request(routePath('get_libraries'), 'GET', undefined, round.signal).then(parseLibraryList).then(value => value, error => tolerateLibraryDirectory(error, round.signal)), readers.read(round.signal),
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
        if (nextLibraries) readOrder.libraries.apply(tickets.libraries, () => {libraries = Object.freeze(nextLibraries.items); librariesUnavailable = false;});
        text.refreshDeviceRegion();
        readOrder.identity.apply(tickets.identity, () => {account = nextAccount; avatars = nextAvatars;});
        readOrder.unread.apply(tickets.unread, () => {unread = nextFacts.unread;});
        readOrder.guide.apply(tickets.guide, () => {hasLiveTV = nextFacts.hasLiveTV;});
        for (const listener of [...preferenceListeners]) listener();
        publish();
      } catch (error) {
        if (nativeShellPermissionDenied(error)) endUnlessRoleChanged();
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
      catch (error) {if (nativeShellPermissionDenied(error)) endUnlessRoleChanged(); throw error;}
    };
    const freshness = new NativeShellFreshness({
      hasHome: () => !!documentService.getSnapshot().document,
      home: async context => {
        try {
          await resourceCurrent(context.signal);
          noteHomeRead('freshness'); await documentService.revalidate({...context, settle: async () => {await context.settle(); await resourceCurrent(context.signal);}});
        } catch (error) {if (nativeShellPermissionDenied(error)) endUnlessRoleChanged(); throw error;}
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
          readers.request<RouteResponse<'get_me'>>(routePath('get_me'), 'GET', undefined, requestSignal),
          readers.readAccount(requestSignal), identities.avatars(requestSignal),
        ]);
        await resourceCurrent(requestSignal);
        const nextProfile = nextAccount.profiles.find(value => value.id === authority.profileId);
        if ((['accountId', 'profileId', 'serverId', 'authority'] as const).some(key => me.viewer[key] !== session!.viewer[key])
          || !nextProfile || nextProfile.revision !== profile.revision || nextProfile.pinRevision !== profile.pinRevision) {end(); return;}
        if (me.viewer.role !== session!.viewer.role || nextAccount.account.role !== currentAccount().account.role) {rebuild(); return;}
        readOrder.identity.apply(ticket, () => {account = nextAccount; avatars = nextAvatars; publish();});
      }),
    });
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
      if (next.serverId !== authority.serverId || homeShellFence(next) !== (authority.privacyFence ?? authority.viewerFence)) {end(); return;}
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
    const resolve = (path: string) => current() ? local.mediaUrl(path) : undefined;
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
      reportPlaybackProblem: async consent => {
        const lifetime = new NativeScreenLifetime(context);
        try {await sendNativeDeviceReport({context, lifetime}, consent);} finally {lifetime.dispose();}
      }, get appearanceMotion() {return nativeAppearanceMotion(renderPreferences());}, get appearanceTextSize() {return nativeAppearanceTextSize(renderPreferences());}, get appearanceCardSizePercent() {return nativeAppearanceCardSizePercent(renderPreferences());}, get appearanceShowBackdrops() {return nativeAppearanceShowBackdrops(renderPreferences());}, get skipBackSeconds() {return playbackSkipSeconds('back');}, get skipForwardSeconds() {return playbackSkipSeconds('forward');}, home: documentService, memory: new LastLibraryMemory(records), text,
      subscribe: listener => {if (!live()) {retireOutsideRender(); return () => {};} listeners.add(listener); return () => {listeners.delete(listener);};}, getSnapshot: () => renderRead(() => snapshot, () => lastSnapshot ?? snapshot),
      // The real pushed route and revision, for a guarded control that must retire when the page
      // it opened on is left. The navigation snapshot is stable between publishes.
      navigation: Object.freeze({subscribe: context.navigation.subscribe, getSnapshot: context.navigation.getSnapshot}),
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
    const downloads = retainNativeDownloadsDevice(context);
    cleanups.push(downloads.dispose);
    void downloads.open().catch(error => {if (current()) bound.reportError(error);});
    markRestore('ready');
    return runtime;
  } catch (error) {const ended = nativeSignInEnded(error, signInHint, session?.expiresAt ?? originalExpiresAt); dispose(); throw nativeIdentityFailure(error, identityServer) ?? ended ?? error;}
}
