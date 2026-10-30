import {dependsOn} from '@core/freshness-hub.ts';
import {createLibraryRouteProof} from './library-route-proof';
import type {LibraryRouteProof} from './library-route-proof';
import type {HttpLocalApi, LocalSession} from '@core/index.ts';
import {parseLibraryList} from '@core/library-list.ts';
import {LocalSessionRefresher} from '@core/session-refresh.ts';
import {selectedViewerRecord} from '@core/viewer-selection.ts';
import {PreferencesReader} from '@core/preferences-reader.ts';
import {appearancePreferences, regionPreferences} from '@core/preference-effects.ts';
import type {PreferenceSnapshot} from '@core/preferences.ts';
import {parseDirectSnapshot, type DirectSnapshot} from '@core/profile-management.ts';
import {HomeService} from '@core/home-service.ts';
import {homeShellFence} from '@core/home.ts';
import {createShellFactOwner, attachShellFactFeed, bindShellFactWake, shellAccount, type ShellFactWitnesses} from './facts';
import type {ShellFactsSource} from './fact-source';
import {InboxService} from '@core/inbox.ts';
import {createNotificationScope} from '@core/notification-scope.ts';
import {EventsClient} from '@core/playback-v1/events.ts';
import {localV1Http} from '@core/playback-v1/local-http.ts';
import {runEventFeed} from '@core/event-feed.ts';
import {fetchGuideSourceSummaries} from '@core/guide/directory.ts';
import {routePath} from '../../../packages/contracts/src/routes.ts';
import {browserViewerStorage} from '../platform/viewer-selection-storage';
import {browserServerDisplay} from '../platform/server-display';
import {motionTier} from '../platform/motion-tier';
import {negotiateServer,parseServerSignIn,signInLabelKey} from '@core/sign-in.ts';
import {defaultI18n} from '@i18n';
import {lastKnownShell, rememberShell} from './last-known';
import {browserAppSessionActions,signOutBrowserPorticoAccount,type BrowserAppSessionActions} from '../platform/app-sessions';
import {mountBrowserUserMutations, browserConnectionEnvironment, restoreBrowserConnection, trackBrowserConnection, forgetBrowserConnection} from '../platform/server-connections';
import {displayableTransportCode, launchFailure, retryDelayMs, serverIsStarting} from './connection-retry';
import {bootRecoveryDeadline, bootRecoveryDelay, documentStartedAt} from './boot-state';
import {browserEventStream, browserFeedEnvironment} from '../platform/event-feed';
import {freshnessFor, type FreshnessRuntime} from '../platform/freshness';
import {authenticatedArtwork, type ShellArtwork} from './artwork';
import type {ShellBindings} from './bindings';
import type {ShellIdentity} from './Shell';

export type ShellFacts = Readonly<{identity: ShellIdentity; preferences: PreferenceSnapshot; serverName: string}>;
export type ShellAssemblyContext = Readonly<{
  api: HttpLocalApi; session: () => LocalSession; auth: LocalSessionRefresher; events: EventsClient;
  freshness: FreshnessRuntime; home: HomeService; inbox: InboxService; artwork: ShellArtwork;
  libraryRouteProof?: LibraryRouteProof; libraryRouteSignal?: AbortSignal;
  facts: ShellFacts; factsSource?: ShellFactsSource; homeAuthority?: () => void;
  /** Read the library directory again now (an inline Try again after it did not answer). */
  retryLibraries?: () => void; observeDirect?: (snapshot: DirectSnapshot) => void; preferences?: PreferencesReader; epoch: number; viewerFence?: string; isCurrent: () => boolean;
  signOut: () => void; signOutPorticoAccount?: () => void; switchProfile:(profileId:string,pin:string,signal:AbortSignal)=>Promise<void>; switchServer:(serverId:string,expectedRevision?:number)=>Promise<void>; savedServers:readonly {id:string;label:string}[];
}>;
export type ShellAssembly = Readonly<{
  getBinding: () => ShellBindings;
  subscribe: (listener: () => void) => () => void;
  /** Real playback decides whether the background event feed must remain open. */
  busy: () => boolean; onBusy: (listener: () => void) => () => void;
  retainsMedia?: () => boolean; onRetention?: (listener: () => void) => () => void;
  dispose: () => void;
}>;
export type ShellAssembler = (context: ShellAssemblyContext) => Promise<ShellAssembly>;
/** states.html#3 facts. Only values this launch already holds: the address the person
 * saved, the cached server name, their role and the real failure code. */
export type LaunchUnreachable = Readonly<{
  serverName?: string;
  address: string;
  owner: boolean;
  category: 'unreachable' | 'offline' | 'timeout' | 'starting';
  code?: string;
  /** Failed connections since the first one, starting at 1. */
  attempt: number;
  firstFailureAt: number;
  /** When the countdown last (re)started; the ring's 100% mark. */
  startedAt: number;
  nextRetryAt: number;
  retrying: boolean;
}>;
export type ShellBootstrapSnapshot = Readonly<{phase: 'restoring' | 'signed-out' | 'ready' | 'error' | 'unreachable'; binding?: ShellBindings; context?: ShellAssemblyContext; entry?: boolean; error?: unknown; unreachable?: LaunchUnreachable;
  startedAt?: number; waiting?: Readonly<{serverName?: string}>}>;

/** The saved address without credentials or paths; display only. */
function savedHost(value: unknown): string {
  try { const url = new URL(String(value)); return url.host; } catch { return ''; }
}

function viewerId(session: LocalSession): string { const v = session.viewer; return JSON.stringify([v.authority, v.accountId, v.profileId]); }
export function sameShellAuthority(a: LocalSession, b: LocalSession): boolean {
  return a.sessionFamilyId === b.sessionFamilyId && (['serverId', 'authority', 'accountId', 'profileId', 'role'] as const).every(key => a.viewer[key] === b.viewer[key]);
}

/** A cache discriminator from admitted authority, never a substitute Home fence. */
export function shellArtworkAuthorityKey(session: LocalSession, epoch: number): string {
  return JSON.stringify([session.viewer.serverId, session.sessionFamilyId, viewerId(session), session.viewer.role, epoch]);
}
/** Subscribe before the initial load. The first actual document establishes the
 * baseline without remounting Settings; a later missing/replaced privacy fence retires it.
 * A library added or removed changes only the viewer fence: the shell stays, and
 * `viewerFence` follows the current document (H-11). */
export function watchHomeViewerFence(home: Pick<HomeService, 'getSnapshot' | 'subscribe'>, isCurrent: () => boolean, replaced: () => void) {
  let shell: string | undefined, fence: string | undefined, closed = false, off = () => {};
  const dispose = () => {if (closed) return; closed = true; off();};
  const read = () => {
    if (closed) return;
    if (!isCurrent()) {dispose(); return;}
    const document = home.getSnapshot().document, next = document && homeShellFence(document);
    if (shell === undefined) {if (next) {shell = next; fence = document!.viewerFence;} return;}
    if (next !== shell) {dispose(); replaced(); return;}
    fence = document!.viewerFence;
  };
  off = home.subscribe(read); read();
  return {get viewerFence() {return !closed && isCurrent() ? fence : undefined;}, dispose};
}

type ShellAccount = Pick<ShellIdentity, 'username' | 'profileName' | 'profileArt' | 'primaryProfile' | 'profiles' | 'canManageProfiles' | 'accountFacts' | 'capabilities'>;
export async function currentShellProfile(api: HttpLocalApi, session: LocalSession, signal: AbortSignal): Promise<ShellAccount> {
  const v = session.viewer;
  if (v.authority !== 'local') throw new Error('Profiles must come from this server.');
  {
    const snapshot = parseDirectSnapshot(await api.request(routePath('get_direct_account'), 'GET', undefined, signal), {authority: 'local', accountId: v.accountId, serverId: v.serverId}, true);
    const profile = snapshot.profiles.find(candidate => candidate.id === v.profileId);
    if (!profile) throw new Error('The selected profile is no longer present.');
    if (!profile.primary && Object.values(snapshot.capabilities).some(Boolean)) throw new Error('Management is available only on the main profile.');
    return {capabilities: snapshot.capabilities, username: snapshot.account.username, profileName: profile.name, profileArt: profile.art, primaryProfile: profile.primary, canManageProfiles: snapshot.canManage,
      profiles: snapshot.profiles.map(value => ({id: value.id, name: value.name, initial: Array.from(value.name.trim())[0] ?? '', art: value.art, pinRequired: value.pinRequired, primary: value.primary})),
      accountFacts: []};
  }

}
/** Initial admitted observations and their complete private proof witnesses. */
/** The one automatic re-read of a library directory that did not answer at sign-in. */
export const LIBRARY_DIRECTORY_RETRY_MS = 2000;
export async function readShellIdentity(api: HttpLocalApi, session: LocalSession, signal: AbortSignal) {
  const [libraries, profile, system, guide] = await Promise.all([
    // One slow or failed library directory never stops the shell: it is unavailable, never empty,
    // and the shell's directory interest reads it again. Authority answers still stop sign-in.
    api.request(routePath('get_libraries'), 'GET', undefined, signal).then(parseLibraryList).then(value => value.items, error => {
      if (signal.aborted || [401, 403].includes((error as {status?: unknown} | null)?.status as number)) throw error;
      return undefined;
    }),
    api.request(routePath('get_direct_account'), 'GET', undefined, signal).then(raw => parseDirectSnapshot(raw, {authority:'local',accountId:session.viewer.accountId,serverId:session.viewer.serverId})), api.request(routePath('get_system'), 'GET', undefined, signal).then(parseServerSignIn),
    fetchGuideSourceSummaries(api, session.viewer.serverId, Intl.DateTimeFormat().resolvedOptions().timeZone, signal).catch(error => {
      if ((error as {status?: number}).status === 403) return undefined;
      throw error;
    }),
  ]);
  if (system.id !== session.viewer.serverId) throw new Error('The server identity changed.');
  negotiateServer(system);
  return {libraries: libraries ?? [], ...(libraries ? {} : {librariesUnavailable: true as const}), direct: profile, profile: shellAccount(profile, session.viewer.profileId), system, guide, liveAvailable: !!guide?.sources.length};
}
async function readFacts(api: HttpLocalApi, session: LocalSession, preferences: PreferencesReader, inbox: InboxService, signal: AbortSignal): Promise<{facts: ShellFacts; witnesses: ShellFactWitnesses}> {
  const [read, settings] = await Promise.all([readShellIdentity(api, session, signal), preferences.preferences('web')]);
  const {libraries, profile, system, liveAvailable} = read, librariesUnavailable = 'librariesUnavailable' in read;
  rememberShell(session.sessionFamilyId, system.name);
  await inbox.load();
  signal.throwIfAborted();
  return {witnesses: {system: read.system, direct: read.direct, guide: read.guide && {viewerFence:read.guide.viewerFence, revision:read.guide.revision}}, facts: {identity: {viewer: session.viewer, libraries, ...(librariesUnavailable ? {librariesUnavailable: true as const} : {}), serverName: system.name, serverVersion: system.version, serverApiVersion: system.apiVersion, signIn:system.signIn, ...profile,
    accountFacts:[{id:'sign-in',role:'status',text:defaultI18n.t(signInLabelKey(system.signIn))}],
    liveAvailable, unread: inbox.getSnapshot().unread, showNotificationDot: settings.effective['notifications.badges'] === true}, preferences: settings, serverName: system.name}};
}

/** Restores only genuine protected state. No credentials or business data appear in source. */
export class ShellBootstrap {
  private startedAt: number;
  private started = false;
  private state: ShellBootstrapSnapshot;
  private readonly listeners = new Set<() => void>();
  private epoch = 0;
  private controller?: AbortController;
  private retire?: () => void;
  private running?: Promise<void>;
  private disposed = false;
  private retryTimer?: ReturnType<typeof setTimeout>;
  private waitWakers?: () => void;
  private recoveryWait?: () => void;
  private failure?: {attempt: number; firstFailureAt: number; startedAt: number; nextRetryAt: number};
  private launch?: {address: string; owner: boolean; serverId: string; family?: string};
  /** Exactly the error the restore call raised; a later bug must never read as "server away". */
  private transportError?: unknown;
  constructor(private readonly assemble: ShellAssembler, private readonly environment: {
    /** Override the document launch epoch in deterministic hosts/tests. */
    startedAt?: number;
    selected: Pick<typeof browserViewerStorage, 'read'>;
    restore: typeof restoreBrowserConnection;
    actions?: () => BrowserAppSessionActions;
    /** Closed Account Settings return: inspect the original access only. */
    readonlyAccess?: true;
  } = {selected: browserViewerStorage, restore: restoreBrowserConnection}) {
    this.startedAt = environment.startedAt ?? documentStartedAt();
    this.state = Object.freeze({phase: 'restoring', startedAt: this.startedAt});
  }
  getSnapshot = () => this.state;
  subscribe = (listener: () => void) => { if (this.disposed) return () => {}; this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };
  private publish(next: ShellBootstrapSnapshot) { if (this.disposed && next.phase !== 'signed-out') return; this.state = Object.freeze(next); for (const listener of this.listeners) listener(); }
  private fence() {
    ++this.epoch; this.controller?.abort(); this.controller = undefined;
    this.retire?.(); this.retire = undefined;
  }
  /** Suspend only the mounted runtime. Durable sessions stay until an explicit
   * admission/sign-out decides their fate. */
  suspendForAccountEntry = () => {if (this.disposed) return;this.clearWait();this.fence();this.publish({phase: 'signed-out', entry: true});};
  resumeAfterAccountEntry = async () => {
    const expected = this.epoch;
    await this.running;
    // A disposed or superseded entry may not wake a private runtime later.
    if (this.epoch === expected) await this.start();
  };
  private restart = () => {
    this.fence(); this.publish({phase: 'restoring'});
    const pending = this.running, expected = this.epoch;
    void Promise.resolve(pending).then(() => { if (this.epoch === expected && this.state.phase === 'restoring') return this.start(); });
  };
  start = (retry = false): Promise<void> => {
    if (this.disposed) return Promise.resolve();
    if (this.running) return this.running;
    this.transportError = undefined;
    this.fence();
    // Retries share the original launch deadline. A later explicit opening gets
    // its own wait, but never recreates the document's cold-start picture.
    if (!retry) {
      if (this.started) this.startedAt = Date.now();
      this.started = true;
      this.clearWait(); this.launch = undefined; this.publish({phase: 'restoring', startedAt: this.startedAt});
    }
    else if (this.failure && this.state.phase === 'unreachable') this.publish({phase: 'unreachable', unreachable: {...this.state.unreachable!, retrying: true}});
    const epoch = this.epoch, controller = this.controller = new AbortController();
    const current = () => this.epoch === epoch && !controller.signal.aborted;
    const task = this.restore(epoch, controller.signal, current).then(() => { if (current()) this.clearWait(); }).catch(error => {
      if (!current()) return;
      if (serverIsStarting(error)) {this.waitAfterFailure(error, 'starting');return;}
      const category = this.transportError === error ? launchFailure(error, globalThis.navigator?.onLine) : 'other';
      if (category !== 'other') { this.waitAfterFailure(error, category); return; }
      this.clearWait(); this.fence(); this.publish({phase: 'error', error});
    }).finally(() => { if (this.running === task) this.running = undefined; });
    this.running = task; return task;
  };

  /** Failures and retries stay on the drawn page; the saved credential is untouched. */
  private waitAfterFailure(error: unknown, category: 'unreachable' | 'offline' | 'timeout' | 'starting') {
    const now = Date.now();
    const attempt = (this.failure?.attempt ?? 0) + 1;
    const firstFailureAt = this.failure?.firstFailureAt ?? now;
    const nextRetryAt = now + retryDelayMs(attempt - 1);
    const wait = {attempt, firstFailureAt, startedAt: now, nextRetryAt};
    this.failure = wait;
    const show = category === 'starting' || this.state.phase === 'unreachable'
      || bootRecoveryDelay(this.startedAt, now, category === 'offline' || globalThis.navigator?.onLine === false) === 0;
    this.publish({phase: show ? 'unreachable' : 'restoring', startedAt: this.startedAt,
      waiting: this.state.waiting, unreachable: this.unreachable(wait, category, error)});
    if (show) { this.recoveryWait?.(); this.recoveryWait = undefined; }
    clearTimeout(this.retryTimer);
    this.retryTimer = setTimeout(() => { this.retryTimer = undefined; void this.start(true); }, Math.max(0, nextRetryAt - Date.now()));
    this.armWaitWakers();
  }

  /** The first failed probe stays invisible. A slow in-flight probe can also
   * reach the presentation deadline; it keeps its original request and guards. */
  private armRecovery(current: () => boolean) {
    this.recoveryWait?.(); this.recoveryWait = undefined;
    if (this.state.phase !== 'restoring') return;
    const deadline = bootRecoveryDeadline(this.startedAt, () => {
      if (!current() || this.state.phase !== 'restoring') return;
      const now = Date.now(), offline = globalThis.navigator?.onLine === false;
      const facts = this.state.unreachable ?? this.unreachable({attempt: 0, firstFailureAt: now, startedAt: now, nextRetryAt: now}, offline ? 'offline' : 'timeout');
      this.publish({phase: 'unreachable', unreachable: {...facts, ...(offline ? {category: 'offline' as const} : {}), retrying: !!this.running}});
    });
    const update = () => deadline.update(globalThis.navigator?.onLine === false);
    globalThis.addEventListener?.('offline', update);
    globalThis.addEventListener?.('online', update);
    this.recoveryWait = () => {
      deadline.dispose();
      globalThis.removeEventListener?.('offline', update);
      globalThis.removeEventListener?.('online', update);
    };
    update();
  }

  /** states.html#3 / desktop-app.html#15: a retry also runs at once when the
   * window comes back into view, the computer wakes or the network returns. */
  private armWaitWakers() {
    this.disarmWaitWakers();
    const wake = () => {
      if (!this.disposed && this.failure && !this.running && (this.state.phase === 'restoring' || this.state.phase === 'unreachable')) void this.retryNow();
    };
    const visibility = () => { if (globalThis.document?.visibilityState !== 'hidden') wake(); };
    globalThis.document?.addEventListener('visibilitychange', visibility);
    globalThis.addEventListener?.('focus', wake);
    globalThis.addEventListener?.('online', wake);
    this.waitWakers = () => {
      globalThis.document?.removeEventListener('visibilitychange', visibility);
      globalThis.removeEventListener?.('focus', wake);
      globalThis.removeEventListener?.('online', wake);
    };
  }

  private disarmWaitWakers() {
    this.waitWakers?.();
    this.waitWakers = undefined;
  }

  private unreachable(wait: {attempt: number; firstFailureAt: number; startedAt: number; nextRetryAt: number}, category: 'unreachable' | 'offline' | 'timeout' | 'starting', error?: unknown): LaunchUnreachable {
    const saved = this.launch, code = displayableTransportCode((error as {code?: unknown} | null)?.code);
    const known = saved?.family ? lastKnownShell(saved.family) : undefined;
    return Object.freeze({
      ...(saved ? {serverName: saved.serverId ? browserServerDisplay().name(saved.serverId) : undefined, address: saved.address, owner: saved.owner} : {address: '', owner: false}),
      ...(known ? {serverName: known.serverName} : {}),
      category,
      ...(code ? {code} : {}),
      ...wait, retrying: false,
    });
  }

  /** A retry from the drawn page asks now and restarts the countdown. */
  retryNow = (): Promise<void> => {
    if (this.disposed) return Promise.resolve();
    if (this.retryTimer) { clearTimeout(this.retryTimer); this.retryTimer = undefined; }
    return this.start(true);
  };

  private clearWait() {
    this.disarmWaitWakers();
    this.recoveryWait?.(); this.recoveryWait = undefined;
    if (this.retryTimer) { clearTimeout(this.retryTimer); this.retryTimer = undefined; }
    this.failure = undefined;
  }

  private async restore(epoch: number, signal: AbortSignal, isCurrent: () => boolean): Promise<void> {
    const selected = await this.environment.selected.read();
    if (!isCurrent()) return;
    if (!selected) { this.publish({phase: 'signed-out'}); return; }
    // Only what this saved launch already holds: the address used and the saved role.
    const viewer = selected.session?.viewer;
    this.launch = {address: savedHost(selected.serverUrl), owner: viewer?.role === 'owner', serverId: viewer?.serverId ?? '', family: selected.session.sessionFamilyId};
    if (this.state.phase === 'restoring') {
      const known = lastKnownShell(selected.session.sessionFamilyId);
      const serverName = known?.serverName ?? (viewer?.serverId ? browserServerDisplay().name(viewer.serverId) : undefined);
      this.publish({...this.state, startedAt: this.startedAt, waiting: {serverName}});
      this.armRecovery(isCurrent);
    }
    let restored: Awaited<ReturnType<typeof restoreBrowserConnection>>;
    try { restored = await this.environment.restore(selected.session); }
    catch (error) { this.transportError = error; throw error; }
    if (!isCurrent()) { restored?.api.getRouteConnection()?.dispose(); return; }
    if (!restored) { this.publish({phase: 'signed-out'}); return; }
    const {api} = restored;
    let session = selectedViewerRecord(api.baseUrl, restored.session).session;
    if (session.viewer.role === 'account') { this.publish({phase: 'signed-out'}); api.getRouteConnection()?.dispose(); return; }
    const mutations = mountBrowserUserMutations(api, session);
    trackBrowserConnection(api);
    const cleanups: (() => void)[] = [() => {api.setUserMutationSession(undefined); mutations.dispose();}];
    const close = () => { for (const cleanup of cleanups.splice(0).reverse()) { try { cleanup(); } catch { /* Retire every other captured owner too. */ } } };
    this.retire = close;
    let signingOut = false;
    cleanups.push(() => { if (!signingOut) api.getRouteConnection()?.dispose(); });
    const signOut = () => {
      if (!isCurrent()) return;
      const captured = session;
      signingOut = true;
      this.fence(); this.publish({phase: 'signed-out'});
      // Remove the exact local credential before remote cleanup; switching never signs out peers.
      void (this.environment.actions?.()??browserAppSessionActions()).signOutServer(captured).catch(error=>this.publish({phase:'error',error})).finally(()=>api.getRouteConnection()?.dispose());
    };
    const auth = new LocalSessionRefresher({api, env: browserConnectionEnvironment(session.viewer.serverId), session,
      onRotated: next => {
        if (!isCurrent()) return;
        const authorityChanged = !sameShellAuthority(session, next); session = next;
        if (authorityChanged) this.restart();
      },
      onSignedOut: () => {
        if (!isCurrent()) return;
        const captured = session;
        this.fence(); this.publish({phase: 'signed-out'});
        void browserViewerStorage.clearIfFamily?.(captured.sessionFamilyId).catch(() => {});
      },
    });
    if (this.environment.readonlyAccess) auth.stop();
    cleanups.push(() => auth.stop());
    const events = new EventsClient({http: localV1Http(api), openStream: browserEventStream(api, session.viewer.serverId + ':' + viewerId(session), () => session.accessToken)});
    cleanups.push(() => events.stop());
    const freshness = freshnessFor(api, {serverId: session.viewer.serverId, viewerId: viewerId(session)});
    cleanups.push(() => { freshness.pool.dispose(); freshness.hub.dispose(); });
    const preferences = new PreferencesReader(api, {serverId: session.viewer.serverId, viewerId: viewerId(session)});
    cleanups.push(() => preferences.dispose());
    const notificationApi = {request: <T,>(path: string, method?: string, body?: unknown, requestSignal?: AbortSignal) => {
      const readSignal = requestSignal ? AbortSignal.any([requestSignal, signal]) : signal;
      const request = () => api.request<T>(path, method, body, readSignal);
      return !method || method === 'GET' ? events.resyncReads.run(readSignal, request) : request();
    }};
    const notificationScope = createNotificationScope(notificationApi, {serverId: session.viewer.serverId, signal, isCurrent,
      onRetired: () => { if (isCurrent()) this.restart(); }});
    cleanups.push(() => notificationScope.dispose());
    let manifestFence: string | undefined;
    let factOwner: ReturnType<typeof createShellFactOwner> | undefined;
    const libraryRouteProof = createLibraryRouteProof({session: () => session, isCurrent,
      directoryVersion: () => freshness.hub.version(dependsOn.libraries),
      homeVersion: () => JSON.stringify([dependsOn.libraries, dependsOn.list('members'), dependsOn.settings('server'), dependsOn.settings('preferences')].map(key => freshness.hub.version(key))),
      request: () => factOwner?.request('directory')});
    cleanups.push(libraryRouteProof.dispose);
    const home = new HomeService(api, {observation: {
      serverId: session.viewer.serverId, isCurrent,
      retire: () => {if (isCurrent()) this.restart();},
      mismatch: () => factOwner?.authority(),
      observe: observation => {
        if (!isCurrent()) return 'retire';
        // The shell restarts on a privacy change only (H-11); a library added or removed refreshes Home.
        const shellFence = observation.privacyFence ?? observation.viewerFence;
        if (manifestFence === undefined) manifestFence = shellFence;
        else if (manifestFence !== shellFence) {this.restart(); return 'retire';}
        // Home retirement is classified first; only accepted same-epoch manifests
        // reach the route proof observer, never custom-layout row membership.
        libraryRouteProof.observeHome(observation);
        return isCurrent() ? 'accept' : 'retire';
      },
    }}), inbox = new InboxService(notificationScope);
    cleanups.push(() => home.dispose(), () => inbox.dispose());
    const {facts: initialFacts, witnesses} = await readFacts(api, session, preferences, inbox, signal);
    if (!isCurrent()) return;
    factOwner = createShellFactOwner({api, session: () => session, auth, preferences, home, hub: freshness.hub,
      initial: initialFacts, witnesses, libraryRouteProof, isCurrent, retire: () => {if (isCurrent()) this.restart();}, readonlyAccess: this.environment.readonlyAccess});
    cleanups.push(factOwner.dispose);
    // The directory did not answer at sign-in: one automatic try after a short wait; after that the
    // freshness hub's own backoff and the inline Try again take over. Nothing blocks meanwhile.
    if (initialFacts.identity.librariesUnavailable) {
      const timer = setTimeout(() => {if (isCurrent()) factOwner?.request('directory');}, LIBRARY_DIRECTORY_RETRY_MS);
      cleanups.push(() => clearTimeout(timer));
    }
    const factsSource = factOwner.source;
    const facts = () => factsSource.getSnapshot();
    freshness.hub.attach(events);
    const homeFence = watchHomeViewerFence(home, isCurrent, () => this.restart());
    cleanups.push(homeFence.dispose);
    const authorityKey = shellArtworkAuthorityKey(session, epoch);
    const artwork = authenticatedArtwork({api, auth, identity: {serverId: session.viewer.serverId, viewerId: viewerId(session), authorityKey}, currentToken: () => session.accessToken, isCurrent});
    cleanups.push(() => artwork.dispose());
    const signOutPorticoAccount=initialFacts.identity.signIn==='portico'?()=>{if(!isCurrent()||facts().identity.signIn!=='portico')return;signingOut=true;this.fence();this.publish({phase:'signed-out'});void signOutBrowserPorticoAccount().catch(error=>this.publish({phase:'error',error})).finally(()=>api.getRouteConnection()?.dispose());}:undefined;
    const switchProfile=async(profileId:string,pin:string,signal:AbortSignal)=>{if(!isCurrent())throw new Error('The viewer changed.');await (this.environment.actions?.()??browserAppSessionActions()).switchProfile(api,session,profileId,pin,signal,isCurrent);if(isCurrent())this.restart();};
    const display=browserServerDisplay();display.remember(api,facts().serverName);
    const saved=(await (this.environment.actions?.()??browserAppSessionActions()).store.read()).servers;
    const savedServers=await display.choices(saved.map(value=>({pin:value.connection.pin,origin:value.connection.logicalOrigin,signIn:value.signIn})),signal);
    if (!isCurrent()) return;
    const context: ShellAssemblyContext = {api, session: () => session, auth, events, freshness, home, inbox, artwork, factsSource, libraryRouteProof: libraryRouteProof.source, homeAuthority: factOwner.authority, observeDirect: factOwner.observeDirect, retryLibraries: () => {if (isCurrent()) factOwner?.request('directory');}, get facts() {return facts();}, preferences, epoch, get viewerFence() {return homeFence.viewerFence;}, isCurrent, signOut,signOutPorticoAccount,switchProfile,switchServer:this.selectServer,get savedServers() {return savedServers.map(value => value.id === session.viewer.serverId ? {...value, label: facts().serverName} : value);}};
    const assembly = await this.assemble(context);
    if (!isCurrent()) { assembly.dispose(); return; }
    cleanups.push(() => assembly.dispose());
    const publishBinding = () => {
      if (!isCurrent()) return;
      const binding = assembly.getBinding();
      this.publish({phase: 'ready', context, binding: {...binding, shell: {...binding.shell,
        identity: {...binding.shell.identity, unread: inbox.getSnapshot().unread, showNotificationDot: (preferences.getSnapshot('web') ?? facts().preferences).effective['notifications.badges'] === true}}}});
    };
    cleanups.push(inbox.subscribe(publishBinding));
    cleanups.push(assembly.subscribe(publishBinding));
    cleanups.push(events.on('notification.changed', () => { void inbox.refreshUnread(); }));
    const restart = () => { if (isCurrent()) this.restart(); };
    let regionKey = JSON.stringify(regionPreferences(facts().preferences));
    cleanups.push(preferences.subscribe(() => {
      const next = preferences.getSnapshot('web'); if (!next || !isCurrent()) return;
      const key = JSON.stringify(regionPreferences(next));
      if (key !== regionKey) { regionKey = key; restart(); } else publishBinding();
    }));
    // Classified hints reach their canonical interests through hub.attach above,
    // retaining revision/address dedupe rather than invalidating a second time.
    cleanups.push(events.on('settings.changed', event => {
      const resource = event.resource?.id;
      if (resource !== 'preferences' && resource !== 'server') restart();
    }));
    cleanups.push(attachShellFactFeed(events, freshness.hub, isCurrent, () => {
      factOwner?.all(); void inbox.refreshUnread();
    }));
    cleanups.push(factsSource.subscribe(() => {
      if (!isCurrent()) return;
      display.remember(api, facts().serverName);
      rememberShell(session.sessionFamilyId, facts().serverName);
      publishBinding();
    }));
    const retention = () => factOwner?.setRetained(assembly.retainsMedia?.() ?? assembly.busy());
    cleanups.push(assembly.onRetention?.(retention) ?? (() => {})); retention();
    cleanups.push(bindShellFactWake(globalThis.document, window, {isCurrent,
      retained: () => assembly.retainsMedia?.() ?? assembly.busy(), wakeAuth: () => auth.wake(), invalidate: () => factOwner?.all()}));
    const motion = globalThis.matchMedia('(prefers-reduced-motion: reduce)');
    const applyMotion = () => {
      const root = globalThis.document.documentElement, reduced = appearancePreferences(preferences.getSnapshot('web') ?? facts().preferences, motion.matches).reducedMotion;
      root.dataset.reduceMotion = String(reduced); root.dataset.motionTier = motionTier(reduced);
    };
    applyMotion(); motion.addEventListener('change', applyMotion);
    cleanups.push(preferences.subscribe(applyMotion));
    cleanups.push(() => motion.removeEventListener('change', applyMotion));
    cleanups.push(runEventFeed(events, freshness.hub, browserFeedEnvironment(assembly.busy, assembly.onBusy, assembly.retainsMedia, assembly.onRetention)));
    void home.load().catch(error => {if (isCurrent()) this.publish({phase:'error', error});});
    publishBinding();
  }
  selectServer = async (serverId:string,expectedRevision?:number) => {if(this.disposed)return;this.clearWait();this.fence();const epoch=this.epoch;this.publish({phase:'restoring'});try{await (this.environment.actions?.()??browserAppSessionActions()).switchServer(serverId,()=>!this.disposed&&this.epoch===epoch,expectedRevision);if(!this.disposed&&this.epoch===epoch)await this.start();}catch(error){if(!this.disposed&&this.epoch===epoch)this.publish({phase:'error',error});}};
  dispose = () => { if (this.disposed) return; this.disposed = true; this.clearWait(); this.fence(); this.publish({phase: 'signed-out'}); this.listeners.clear(); };
}
