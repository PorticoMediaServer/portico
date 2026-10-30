import type {LibraryContentApi} from '@core/library-content.ts';
import {HttpLocalApi, type LocalSession} from '@core/index.ts';
import {parseLocalSession} from '@core/local-session.ts';
import {parseDirectSnapshot} from '@core/profile-management.ts';
import {initializeServer, type PendingAccountSetup, type SetupDraft, type SetupDraftStore} from '@core/setup-onboarding.ts';
import type {ServerSignIn} from '@core/sign-in.ts';
import {sameServerPin, type ServerPin} from '@core/route-identity.ts';
import {rememberNativeSession} from '@core/server-connections.ts';
import {installationIdentity} from '@core/installation.ts';
import {CodedError} from '@core/server-messages.ts';
import {setupAddressAllowed} from '@core/setup-address.ts';
import {decodePostLibrariesResponse} from '../../../packages/contracts/src/decoders.ts';
import {routeBody, routePath, encodedRoutePath} from '../../../packages/contracts/src/routes.ts';
import {browserAppSessions, browserConnectionEnvironment, restoreBrowserConnection} from './server-connections';
import {createBrowserSetupClaim, type BrowserSetupClaim, type SetupClaimRecovery} from './setup-claim';
import {setupBrowserPending} from './setup-pending';
import {SetupStageFailure, type SetupFailureStage} from './setup-failure';
import {randomId} from '@core/random-id.ts';

export type SetupTarget = Readonly<{server: ServerSignIn; api: HttpLocalApi; pin: ServerPin; draft: SetupDraftStore; dispose: () => void; admittedOwner?: true}>;
type CompositionPorts = Readonly<{
  sessions: typeof browserAppSessions; environment: typeof browserConnectionEnvironment; restore: typeof restoreBrowserConnection;
  remember: typeof rememberNativeSession; claim: () => Pick<BrowserSetupClaim,'prepare'|'cancel'> & Partial<Pick<BrowserSetupClaim,'discardStale'>>;
  draft: (id: string) => SetupDraftStore & {assertAvailable: () => void};
}>;
const defaultPorts: CompositionPorts = {sessions: browserAppSessions, environment: browserConnectionEnvironment, restore: restoreBrowserConnection,
  remember: rememberNativeSession, claim: createBrowserSetupClaim, draft: id => setupBrowserPending<SetupDraft>('portico.initialize.' + id)};
const changed = () => new CodedError('cancelled', 'The pending setup or selected server changed.');
const sameDraft = (a: SetupDraft | undefined, b: SetupDraft | undefined) => JSON.stringify(a) === JSON.stringify(b);
type HostedOpening = {readonly server: SetupTarget; readonly name: string; readonly requestId: string; readonly abort: AbortController;
  readonly origin: string; readonly pin: ServerPin; readonly route: NonNullable<ReturnType<HttpLocalApi['getRouteConnection']>>;
  live: boolean; busy: boolean; draftCaptured: boolean; draft?: Readonly<SetupDraft>; setup?: PendingAccountSetup;
  recovery?: SetupClaimRecovery; failure?: object};

/** The screen owns form state; this lifetime owns real credential adoption and
 * the initialized owner's API. No setup bearer may create a library. */
export class BrowserSetupComposition {
  private active = true;
  private readonly abort = new AbortController();
  private readonly captures = new WeakMap<SetupTarget, {revision: number; installationId: string}>();
  private owner?: {api: HttpLocalApi; session: LocalSession};
  private claim?: Pick<BrowserSetupClaim,'prepare'|'cancel'> & Partial<Pick<BrowserSetupClaim,'discardStale'>>;
  private handoffDelivered = false;
  private hosted?: HostedOpening;
  constructor(private readonly hostedOrigin: string, private readonly navigate: (url: string) => void = url => location.assign(url), private readonly ports: CompositionPorts = defaultPorts,
    private readonly openingCurrent: () => boolean = () => true) {}
  private current() {
    let opening = false; try {opening = this.openingCurrent();} catch { /* A throwing authority refuses. */ }
    if (!this.active || this.abort.signal.aborted || !opening) throw changed();
  }
  private retireHosted() {const old = this.hosted;if (old) {old.live = false;old.abort.abort();this.hosted = undefined;}}
  private install() {const value = installationIdentity(); if (!value) throw changed(); return value.installationId;}
  private async assert(server: SetupTarget) {
    this.current(); const capture = this.captures.get(server);
    if (!capture || capture.installationId !== this.install() || !sameServerPin(server.pin, server.api.getRouteConnection()?.pin ?? {serverId: '', publicKey: '', fingerprint: ''})) throw changed();
    const selected = await this.ports.sessions().read(); this.current();
    if (selected.revision !== capture.revision) throw changed();
    return capture;
  }
  /** Capture before the first pairing await. An unrelated selection cannot be
   * overwritten by a late setup response, including a completed-server resume. */
  connect = async (connect: (signal: AbortSignal) => Promise<SetupTarget>, signal: AbortSignal): Promise<SetupTarget> => {
    this.current(); this.retireHosted(); const state = await this.ports.sessions().read(), install = this.install(); this.current();
    const server = await connect(AbortSignal.any([signal, this.abort.signal]));
    try {
      this.current(); const current = await this.ports.sessions().read(); this.current();
      if (current.revision !== state.revision || install !== this.install()) throw changed();
      if (server.server.setupRequired) {
        this.captures.set(server, {revision: current.revision, installationId: install}); return server;
      }
      // A callback resumes auth3 only through a genuine saved owner session.
      const selected = current.servers.find(value => value.connection.pin.serverId === current.selectedServerId);
      if (!selected || !sameServerPin(selected.connection.pin, server.pin) || selected.connection.session.viewer.role !== 'owner') throw changed();
      const restored = await this.ports.restore(selected.connection.session); this.current();
      if (!restored) throw changed();
      try {
        const after = await this.ports.sessions().read(); this.current();
        const live = after.servers.find(value => value.connection.pin.serverId === after.selectedServerId);
        if (!live || !sameServerPin(live.connection.pin, server.pin) || live.connection.session.sessionFamilyId !== restored.session.sessionFamilyId || restored.session.viewer.role !== 'owner') throw changed();
        await this.ownerSnapshot(restored.api, restored.session); this.current();
        const target: SetupTarget = {...server, api: restored.api, admittedOwner: true, dispose: () => {server.dispose(); restored.api.getRouteConnection()?.dispose();}};
        this.captures.set(target, {revision: after.revision, installationId: install});
        this.owner = restored; return target;
      } catch (error) {restored.api.getRouteConnection()?.dispose(); throw error;}
    } catch (error) {server.dispose(); throw error;}
  };
  private async ownerSnapshot(api: HttpLocalApi, session: LocalSession) {
    if (session.viewer.role !== 'owner') throw changed();
    const snapshot = parseDirectSnapshot(await api.request(routePath('get_direct_account'), 'GET', undefined, this.abort.signal),
      {authority: 'local', serverId: session.viewer.serverId, accountId: session.viewer.accountId}, true);
    if (snapshot.account.role !== 'owner' || snapshot.account.disabled || !snapshot.capabilities.manageServer || snapshot.account.primaryProfileId !== session.viewer.profileId) throw changed();
    return snapshot;
  }
  acceptLocal = async (server: SetupTarget, value: LocalSession): Promise<void> => {
    const capture = await this.assert(server), session = parseLocalSession(value, {serverId: server.pin.serverId, authority: 'local'});
    if (session.viewer.role !== 'owner' || Date.parse(session.expiresAt) <= Date.now()) throw changed();
    const draft = await server.draft.read(); await this.assert(server);
    if (!draft || draft.authMode !== 'local' || draft.serverId !== server.pin.serverId) throw changed();
    const sessions = this.ports.sessions(), environment = this.ports.environment(server.pin.serverId);
    let adopted = false;
    try {
      await this.ports.remember(server.api, session, {...environment, storage: sessions.connectionStorage(server.pin.serverId,
        {signIn: 'local', expectedRevision: capture.revision})}, undefined, () => this.active && this.install() === capture.installationId);
      this.current();
      const selected = await sessions.selected(); this.current();
      if (!selected || selected.signIn !== 'local' || !sameServerPin(selected.connection.pin, server.pin) || selected.connection.session.sessionFamilyId !== session.sessionFamilyId) throw changed();
      await this.ownerSnapshot(server.api, session); this.current();
      await server.draft.change(old => {
        this.current(); if (old?.requestId !== draft.requestId || old.setupToken !== draft.setupToken || old.authMode !== 'local') throw changed(); return undefined;
      });
      this.owner = {api: server.api, session}; adopted = true;
    } finally {
      if (!adopted) {
        await sessions.connectionStorage(server.pin.serverId).change(old => old?.session.sessionFamilyId === session.sessionFamilyId ? undefined : old).catch(() => {});
        const retire = new HttpLocalApi(server.api.baseUrl, session.accessToken), route = server.api.getRouteConnection(); if (route) retire.useRoutes(route);
        await retire.request(routePath('delete_sessions_current'), 'DELETE', undefined, AbortSignal.timeout(5000)).catch(() => {});
      }
    }
  };
  private hostedCurrent(held: HostedOpening) {
    this.current();
    if (this.hosted !== held || !held.live || held.abort.signal.aborted || held.server.api.baseUrl !== held.origin
      || held.server.api.getRouteConnection() !== held.route || held.route.getSnapshot().phase !== 'ready'
      || !sameServerPin(held.server.pin, held.pin) || !sameServerPin(held.route.pin, held.pin)) throw changed();
  }
  private async assertHosted(held: HostedOpening) {
    this.hostedCurrent(held); await this.assert(held.server); this.hostedCurrent(held);
    if (held.draftCaptured) {
      const now = await this.ports.draft(held.server.pin.serverId).read(); this.hostedCurrent(held);
      if (!sameDraft(now, held.draft)) throw changed();
    }
  }
  private hostedFailure(held: HostedOpening, stage: SetupFailureStage, error: unknown): SetupStageFailure {
    const token = {}, recovery = held.recovery;
    held.failure = token;
    const run = async (local: boolean) => {
      this.hostedCurrent(held);
      if (held.busy || held.failure !== token) throw changed();
      held.busy = true; held.failure = undefined;
      try {
        await this.assertHosted(held);
        if (local) {
          if (!recovery) throw changed();
          await recovery.useLocal(); await this.assertHosted(held);
          this.retireHosted();
        } else if (recovery) {
          const url = await recovery.retry(); await this.assertHosted(held);
          this.navigate(url); this.handoffDelivered = true;
        } else await this.prepareHosted(held);
      } catch (next) {
        this.hostedCurrent(held);
        throw next instanceof SetupStageFailure ? next : this.hostedFailure(held, stage, next);
      } finally {held.busy = false;}
    };
    return new SetupStageFailure(stage, error, () => run(false), recovery ? () => run(true) : undefined);
  }
  private async prepareHosted(held: HostedOpening): Promise<void> {
    let stage: SetupFailureStage = held.setup ? 'claim' : 'browser-start';
    const server = held.server, store = this.ports.draft(server.pin.serverId);
    const signal = AbortSignal.any([this.abort.signal, held.abort.signal]);
    try {
      // The same rule the server and Hosted apply; a public http address is refused before any request.
      if (!setupAddressAllowed(held.origin)) throw new CodedError('setup_source_not_local', 'Setup has to start from a device on the same network as this server.');
      store.assertAvailable(); await this.assertHosted(held);
      if (!held.draftCaptured) {
        const draft = await store.read(); await this.assertHosted(held);
        held.draft = draft ? Object.freeze({...draft}) : undefined; held.draftCaptured = true;
      }
      const original = held.draft;
      if (original && original.authMode !== 'hosted') throw changed();
      if (held.setup && (!original || original.serverId !== held.setup.serverId || original.requestId !== held.setup.requestId)) throw changed();
      const issue = original ? {serverId: original.serverId, setupToken: original.setupToken}
        : await server.api.request<{serverId: string; setupToken: string}>(routePath('start_browser_setup'), 'POST', {}, signal);
      await this.assertHosted(held);
      if (issue.serverId !== server.pin.serverId || typeof issue.setupToken !== 'string' || !issue.setupToken) throw changed();
      stage = 'initialize';
      const guarded: SetupDraftStore = {
        read: async () => {await this.assertHosted(held);const value = await store.read();await this.assertHosted(held);return value;},
        change: async update => {
          await this.assertHosted(held);
          await store.change(old => {
            this.hostedCurrent(held);
            if (!sameDraft(old, held.draft)) throw changed();
            const next = update(old);
            if (!next || next.serverId !== server.pin.serverId || next.requestId !== (original?.requestId ?? held.requestId)
              || next.setupToken !== issue.setupToken || next.authMode !== 'hosted' || next.name !== (original?.name ?? held.name)) throw changed();
            held.draft = Object.freeze({...next}); return next;
          });
          await this.assertHosted(held);
        },
      };
      const setup = await initializeServer(server.api, guarded, {setupToken: issue.setupToken, name: held.name, authMode: 'hosted', interactive: true}, signal,
        async () => original?.requestId ?? held.requestId);
      await this.assertHosted(held);
      if (!('claimRequired' in setup) || setup.serverId !== server.pin.serverId || setup.requestId !== held.draft?.requestId
        || held.setup && (held.setup.requestId !== setup.requestId || held.setup.revision !== setup.revision)) throw changed();
      held.setup = Object.freeze({...setup}); stage = 'claim';
      const claim = this.claim ??= this.ports.claim();
      const url = await claim.prepare(server.api, this.hostedOrigin, {setup: held.setup, signal,
        assertOriginal: () => this.assertHosted(held), isCurrent: () => {
          try {this.hostedCurrent(held);return true;} catch {return false;}
        }, capture: recovery => {this.hostedCurrent(held);held.recovery = recovery;}});
      await this.assertHosted(held);
      this.navigate(url); this.handoffDelivered = true;
    } catch (error) {this.hostedCurrent(held);throw this.hostedFailure(held, stage, error);}
  }
  private async startHosted(server: SetupTarget, name: string, setup?: PendingAccountSetup) {
    this.current(); if (this.hosted?.busy) throw changed(); this.retireHosted();
    const route = server.api.getRouteConnection(); if (!route || route.getSnapshot().phase !== 'ready') throw changed();
    const held: HostedOpening = {server, name, requestId: setup?.requestId ?? randomId(), abort: new AbortController(), live: true, busy: true,
      origin: server.api.baseUrl, pin: Object.freeze({...server.pin}), route,
      draftCaptured: false, ...(setup ? {setup: Object.freeze({...setup})} : {})};
    this.hosted = held;
    try {await this.prepareHosted(held);} finally {held.busy = false;}
  }
  /** The server's own setup page found a saved account setup whose attempt expired unapproved:
   * drop it so the page shows the ordinary start form instead of resuming a dead continuation. */
  discardStaleDraft = async (server: SetupTarget): Promise<boolean> => {
    this.current(); await this.assert(server);
    const discard = (this.claim ??= this.ports.claim()).discardStale;
    if (!discard) return false;
    const done = await this.claim!.discardStale!(server.api);
    this.current();
    return done;
  };
  continueHosted = (server: SetupTarget, input: Readonly<{name: string}>): Promise<void> => this.startHosted(server, input.name);
  resumeHosted = (server: SetupTarget, pending: PendingAccountSetup): Promise<void> => this.startHosted(server, server.server.name, pending);
  /** Only the admitted owner's fixed folder-listing endpoint, never setup bearer IO. */
  readonly libraryFolders: LibraryContentApi = {request: async <T>(path: string, method = 'GET', body?: unknown, signal?: AbortSignal): Promise<T> => {
    const route = routePath('get_admin_filesystem');
    if (method !== 'GET' || body !== undefined || path !== route && !path.startsWith(route + '?')) throw changed();
    this.current(); const owner = this.owner; if (!owner || signal?.aborted) throw changed();
    const controller = new AbortController(), abort = () => controller.abort();
    this.abort.signal.addEventListener('abort', abort, {once: true}); signal?.addEventListener('abort', abort, {once: true});
    const assert = async () => {
      for (let pass = 0; pass < 2; pass++) {
        this.current(); if (controller.signal.aborted || this.owner !== owner) throw changed();
        const selected = await this.ports.sessions().selected(); this.current();
        if (controller.signal.aborted || this.owner !== owner || !selected || selected.connection.session.sessionFamilyId !== owner.session.sessionFamilyId || selected.connection.session.viewer.accountId !== owner.session.viewer.accountId || selected.connection.session.viewer.profileId !== owner.session.viewer.profileId || !sameServerPin(selected.connection.pin, owner.api.getRouteConnection()!.pin) || selected.connection.session.viewer.role !== 'owner') throw changed();
        owner.api.setAccessToken(selected.connection.session.accessToken);
        if (pass === 0) await this.ownerSnapshot(owner.api, owner.session);
      }
      this.current(); if (controller.signal.aborted || this.owner !== owner) throw changed();
    };
    try {
      await assert();
      const result = await owner.api.request<T>(path, 'GET', undefined, controller.signal);
      await assert(); return result;
    } finally {this.abort.signal.removeEventListener('abort', abort); signal?.removeEventListener('abort', abort);}
  }};
  addLibrary = async (input: Readonly<{kind: string; name: string; path: string; folders?: readonly string[]}>): Promise<void> => {
    this.current(); const owner = this.owner; if (!owner) throw changed();
    const assert = async () => {
      this.current(); const selected = await this.ports.sessions().selected(); this.current();
      if (!selected || selected.connection.session.sessionFamilyId !== owner.session.sessionFamilyId || !sameServerPin(selected.connection.pin, owner.api.getRouteConnection()!.pin) || selected.connection.session.viewer.role !== 'owner') throw changed();
      owner.api.setAccessToken(selected.connection.session.accessToken);
    };
    await assert(); await this.ownerSnapshot(owner.api, owner.session); await assert();
    const library = decodePostLibrariesResponse(await owner.api.request(routePath('post_libraries'), 'POST', routeBody('post_libraries', {kind: input.kind, name: input.name, ...(input.folders?.length ? {folders: input.folders} : {path: input.path})}), this.abort.signal));
    await assert();
    await owner.api.request(encodedRoutePath('post_libraries_id_scans', {id: encodeURIComponent(library.id)}), 'POST', {}, this.abort.signal);
    await assert();
  };
  dispose = () => {
    if (!this.active) return; this.active = false; this.retireHosted(); this.abort.abort(); this.owner = undefined;
    if (!this.handoffDelivered) void this.claim?.cancel().catch(() => {});
  };
}
