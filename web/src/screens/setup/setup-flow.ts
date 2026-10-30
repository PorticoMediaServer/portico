import type {LibraryContentApi} from '@core/library-content.ts';
import {routePath} from '../../../../packages/contracts/src/routes.ts';
import {initializeServer, resumeServerSetup, type PendingAccountSetup, type SetupDraft, type SetupDraftStore, type SetupSession} from '@core/setup-onboarding.ts';
import type {HttpLocalApi, LocalSession} from '@core/index.ts';
import type {ServerPin} from '@core/route-identity.ts';
import type {ServerSignIn} from '@core/sign-in.ts';
import {SetupStageFailure} from '../../platform/setup-failure.ts';
import {defaultLibraryName, defaultSetupServerName, localPasswordValid, setupErrorMessage, setupNameValid, validLocalUsername, type SetupLibraryKind, type SetupMode} from './setup-model.ts';

export type SetupServer = Readonly<{
  server: ServerSignIn;
  /** Set only by the trusted composition after it revalidated the admitted owner
   * for auth3's return; a plain setupRequired=false answer still refuses. */
  admittedOwner?: true;
  api: HttpLocalApi;
  pin: ServerPin;
  draft: SetupDraftStore;
  dispose: () => void;
}>;

/** Everything this screen cannot own: the verified pairing, the protected
 * browser journal, the guarded hosted continuation and shell admission. */
export type SetupPorts = Readonly<{
  connect: (signal: AbortSignal) => Promise<SetupServer>;
  acceptLocal: (server: SetupServer, session: LocalSession) => Promise<void>;
  continueHosted: (server: SetupServer, input: Readonly<{name: string}>) => Promise<void>;
  resumeHosted?: (server: SetupServer, pending: PendingAccountSetup) => Promise<void>;
  /** Bound to the admitted owner, never the pre-admission setup bearer. */
  addLibrary: (input: Readonly<{kind: SetupLibraryKind; name: string; path: string; folders?: readonly string[]}>) => Promise<void>;
  libraryFolders?: LibraryContentApi;
  complete: () => void;
  hostname?: () => string;
}>;

export type SetupPhase = 'loading' | 'form' | 'library' | 'waiting' | 'error';
export type SetupSnapshot = Readonly<{
  phase: SetupPhase;
  serverName: string;
  serverId?: string;
  mode: SetupMode;
  name: string;
  username: string;
  password: string;
  confirm: string;
  kind: SetupLibraryKind;
  libraryName: string;
  path: string;
  folders?: readonly string[];
  folderClasses?: Readonly<Record<string, 'local' | 'network'>>;
  busy: boolean;
  error?: string;
  connectError?: unknown;
  /** The exact staged failure, kept for the panel and its own callbacks. */
  failure?: SetupStageFailure;
}>;

const timeout = 30_000;
const closed: SetupSnapshot = Object.freeze({phase: 'loading', serverName: '', mode: 'hosted', name: '', username: '', password: '',
  confirm: '', kind: 'movie', libraryName: '', path: '', busy: false});

/** auth1/1c and auth3: one short form, then the first library. The server's
 * protected setup draft resumes before the form is drawn. */
export class SetupFlow {
  private state: SetupSnapshot = closed;
  private readonly listeners = new Set<() => void>();
  private server?: SetupServer;
  private controller?: AbortController;
  private started = false;
  private disposed = false;
  private localRecovery?: {readonly server: SetupServer; draft: Readonly<SetupDraft>};
  constructor(private readonly ports: SetupPorts) {}
  getSnapshot = () => this.state;
  subscribe = (listener: () => void) => {if (this.disposed) return () => {}; this.listeners.add(listener); return () => {this.listeners.delete(listener);};};
  private publish(patch: Partial<SetupSnapshot>) {if (this.disposed) return; this.state = Object.freeze({...this.state, ...patch}); for (const listener of [...this.listeners]) listener();}
  private operation() {this.controller?.abort(); const controller = this.controller = new AbortController(); return {signal: controller.signal, current: () => !this.disposed && this.controller === controller && !controller.signal.aborted};}
  private current(server: SetupServer, op: {current: () => boolean}) {return op.current() && this.server === server && !this.disposed;}
  start = async (): Promise<void> => {
    if (this.disposed || this.started) return;
    this.started = true;
    await this.connect();
  };
  retry = (): Promise<void> => {
    if (this.disposed || this.state.busy) return Promise.resolve();
    return this.connect();
  };
  private async connect(): Promise<void> {
    const op = this.operation();
    this.localRecovery = undefined;
    this.publish({phase: 'loading', busy: true, error: undefined, connectError: undefined, failure: undefined});
    try {
      const server = await this.ports.connect(op.signal);
      if (!op.current() || this.disposed) {server.dispose(); return;}
      this.server?.dispose();
      this.server = server;
      const hostname = this.ports.hostname?.() ?? (typeof location === 'undefined' ? '' : location.hostname);
      const name = defaultSetupServerName(server.server.name, hostname);
      this.publish({serverId: server.server.id, serverName: server.server.name, name, libraryName: defaultLibraryName('movie'), busy: false});
      if (!server.server.setupRequired) {
        if (server.admittedOwner === true) {this.publish({phase: 'library', busy: false}); return;}
        throw Object.assign(new Error('This server is already set up.'), {code: 'not_setup_required'});
      }
      const draft = await server.draft.read();
      if (!op.current()) return;
      if (draft) {
        const resumed = await resumeServerSetup(server.api, server.draft, op.signal);
        if (!op.current()) return;
        if ('claimRequired' in resumed) {
          this.publish({phase: 'waiting', busy: false});
          if (this.ports.resumeHosted) await this.ports.resumeHosted(server, resumed);
          return;
        }
        this.publish({busy: true});
        await this.ports.acceptLocal(server, resumed);
        if (!op.current()) return;
        this.publish({phase: 'library', busy: false});
        return;
      }
      this.publish({phase: 'form', busy: false});
    } catch (error) {
      if (!op.current()) return;
      this.publish({phase: 'error', busy: false, connectError: error, error: setupErrorMessage(error), failure: error instanceof SetupStageFailure ? error : undefined});
    }
  }
  setName = (name: string) => {if (!this.state.busy) this.publish({name});};
  setMode = (mode: SetupMode) => {if (!this.state.busy) this.publish({mode});};
  setUsername = (username: string) => {if (!this.state.busy) this.publish({username});};
  setPassword = (password: string) => {if (!this.state.busy) this.publish({password});};
  setConfirm = (confirm: string) => {if (!this.state.busy) this.publish({confirm});};
  setKind = (kind: SetupLibraryKind) => {if (!this.state.busy) this.publish({kind, libraryName: defaultLibraryName(kind)});};
  setLibraryName = (libraryName: string) => {if (!this.state.busy) this.publish({libraryName});};
  setPath = (path: string) => {if (!this.state.busy) this.publish({path, folders: undefined, folderClasses: undefined});};
  libraryFolderApi = () => !this.disposed && this.state.phase === 'library' ? this.ports.libraryFolders : undefined;
  setFolders = (folders: readonly string[], classifications?: Readonly<Record<string, 'local' | 'network'>>) => {
    if (this.disposed || this.state.phase !== 'library' || this.state.busy || folders.length > 64 || folders.some(path => !path || path.length > 8192)) return;
    const unique = Object.freeze([...new Set(folders)]);
    const classes: Record<string, 'local' | 'network'> = {};
    for (const path of unique) {const classification = classifications?.[path]; if (classification === 'local' || classification === 'network') classes[path] = classification;}
    this.publish({folders: unique, folderClasses: Object.freeze(classes), path: ''});
  };
  submitHosted = async (): Promise<void> => {
    const server = this.server;
    if (!server || this.state.phase !== 'form' || this.state.busy || this.state.mode !== 'hosted' || !setupNameValid(this.state.name)) return;
    const op = this.operation();
    this.publish({busy: true, error: undefined, failure: undefined});
    try {
      await this.ports.continueHosted(server, {name: this.state.name.trim()});
      if (this.current(server, op)) this.publish({phase: 'waiting', busy: false});
    } catch (error) {
      if (this.current(server, op)) this.publish({phase: 'error', busy: false, error: setupErrorMessage(error), failure: error instanceof SetupStageFailure ? error : undefined});
    }
  };
  /** Runs the exact rendered failure's own retry once, under this flow. */
  retryFailure = async (expected: SetupStageFailure): Promise<void> => {
    const server = this.server;
    if (!server || this.state.busy || this.state.failure !== expected || typeof expected.retry !== 'function') return;
    const op = this.operation();
    this.publish({busy: true, error: undefined});
    try {
      await expected.retry!();
      if (this.current(server, op) && this.state.failure === expected) this.publish({phase: 'waiting', busy: false});
    } catch (error) {
      if (this.current(server, op) && this.state.failure === expected)
        this.publish({phase: 'error', busy: false, error: setupErrorMessage(error), failure: error instanceof SetupStageFailure ? error : expected});
    }
  };
  /** Switches to local setup only after the failure's own guarded callback resolves. */
  useLocalFailure = async (expected: SetupStageFailure): Promise<void> => {
    const server = this.server;
    if (!server || this.state.busy || this.state.failure !== expected || typeof expected.useLocal !== 'function') return;
    const op = this.operation();
    this.publish({busy: true, error: undefined});
    try {
      const draft = await server.draft.read();
      if (!this.current(server, op) || this.state.failure !== expected) return;
      if (!draft || draft.version !== 1 || draft.serverId !== server.pin.serverId || draft.authMode !== 'hosted' || !draft.setupToken)
        throw Object.assign(new Error('The original setup changed.'), {code: 'cancelled'});
      const original = Object.freeze({...draft});
      await expected.useLocal!();
      if (!this.current(server, op) || this.state.failure !== expected) return;
      const after = await server.draft.read();
      if (!this.current(server, op) || this.state.failure !== expected) return;
      if (JSON.stringify(after) !== JSON.stringify(original)) throw Object.assign(new Error('The original setup changed.'), {code: 'cancelled'});
      this.localRecovery = {server, draft: original};
      this.publish({phase: 'form', mode: 'local', busy: false, failure: undefined, error: undefined});
    } catch (error) {
      if (this.current(server, op) && this.state.failure === expected)
        this.publish({phase: 'error', busy: false, error: setupErrorMessage(error), failure: error instanceof SetupStageFailure ? error : expected});
    }
  };
  /** The browser-start route refuses an already-pending onboarding row. A
   * recovered local form must keep its original protected proof instead. */
  private recoveredDraft(server: SetupServer, op: {current: () => boolean}, recovery: NonNullable<SetupFlow['localRecovery']>): SetupDraftStore {
    const current = () => {
      if (!this.current(server, op) || this.localRecovery !== recovery || recovery.server !== server)
        throw Object.assign(new Error('The original setup changed.'), {code: 'cancelled'});
    };
    const matches = (value: SetupDraft | undefined) => JSON.stringify(value) === JSON.stringify(recovery.draft);
    return {
      read: async () => {current(); const value = await server.draft.read(); current(); if (!matches(value)) throw Object.assign(new Error('The original setup changed.'), {code: 'cancelled'}); return value;},
      change: async update => {
        current();
        await server.draft.change(old => {
          current();
          if (!matches(old)) throw Object.assign(new Error('The original setup changed.'), {code: 'cancelled'});
          const next = update(old);
          if (!next || next.serverId !== recovery.draft.serverId || next.requestId !== recovery.draft.requestId ||
            next.setupToken !== recovery.draft.setupToken || next.authMode !== 'local')
            throw Object.assign(new Error('The original setup changed.'), {code: 'cancelled'});
          recovery.draft = Object.freeze({...next}); return next;
        });
        current();
      },
    };
  }
  submitLocal = async (): Promise<void> => {
    const server = this.server, state = this.state;
    if (!server || state.phase !== 'form' || state.busy || state.mode !== 'local' || !setupNameValid(state.name) ||
      !validLocalUsername(state.username) || !localPasswordValid(state.password, state.confirm)) return;
    const op = this.operation();
    this.publish({busy: true, error: undefined});
    try {
      const recovery = this.localRecovery, store = recovery ? this.recoveredDraft(server, op, recovery) : server.draft;
      const recovered = recovery ? await store.read() : undefined;
      if (!this.current(server, op)) return;
      const issue = recovered ? {serverId: recovered.serverId, setupToken: recovered.setupToken}
        : await server.api.request<{serverId?: unknown; setupToken?: unknown}>(routePath('start_browser_setup'), 'POST', {}, op.signal)
          .catch(error => {throw new SetupStageFailure('browser-start', error);});
      if (!op.current()) return;
      if (typeof issue?.serverId !== 'string' || issue.serverId !== server.server.id || typeof issue.setupToken !== 'string' || !issue.setupToken) {
        throw Object.assign(new Error('The setup answer did not match this server.'), {code: 'identity_mismatch'});
      }
      const issued: SetupSession = await initializeServer(server.api, store, {setupToken: issue.setupToken, name: state.name.trim(),
        username: state.username.trim(), password: state.password, authMode: 'local', interactive: true}, signalWithin(op.signal, timeout));
      if (!this.current(server, op)) return;
      this.publish({password: '', confirm: ''});
      if ('claimRequired' in issued) {this.publish({busy: false, phase: 'waiting'}); return;}
      await this.ports.acceptLocal(server, issued);
      if (!this.current(server, op)) return;
      this.localRecovery = undefined;
      this.publish({phase: 'library', busy: false, libraryName: defaultLibraryName(this.state.kind)});
    } catch (error) {
      if (this.current(server, op)) this.publish({busy: false, password: '', confirm: '', error: setupErrorMessage(error)});
    }
  };
  addLibrary = async (): Promise<void> => {
    const server = this.server, state = this.state;
    if (!server || state.phase !== 'library' || state.busy || !setupNameValid(state.libraryName) || !state.folders?.length && !state.path.trim()) return;
    const op = this.operation();
    this.publish({busy: true, error: undefined});
    if (!this.current(server, op) || this.state.phase !== 'library' || !this.state.busy) return;
    try {
      await this.ports.addLibrary({kind: state.kind, name: state.libraryName.trim(), path: state.path.trim(), ...(state.folders?.length ? {folders: Object.freeze([...state.folders])} : {})});
      if (this.current(server, op)) this.ports.complete();
    } catch (error) {
      if (this.current(server, op)) this.publish({busy: false, error: setupErrorMessage(error)});
    }
  };
  skip = () => {if (!this.disposed && this.state.phase === 'library' && !this.state.busy) this.ports.complete();};
  dispose = () => {
    if (this.disposed) return;
    this.disposed = true; this.controller?.abort(); this.controller = undefined;
    this.localRecovery = undefined;
    this.server?.dispose(); this.server = undefined;
    this.listeners.clear(); this.state = closed;
  };
}

function signalWithin(signal: AbortSignal, ms: number): AbortSignal {
  if (typeof AbortSignal.timeout !== 'function') return signal;
  const timed = AbortSignal.timeout(ms);
  const any = (AbortSignal as unknown as {any?: (signals: AbortSignal[]) => AbortSignal}).any;
  return any ? any([signal, timed]) : signal;
}
