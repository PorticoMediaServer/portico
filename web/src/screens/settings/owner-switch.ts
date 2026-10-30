import {AccountMoveClient, type AccountMove, type AccountMoveConfirmation, type MoveClientOptions} from '@core/account-move.ts';
import {HttpLocalApi, type LocalSession} from '@core/index.ts';
import type {DirectSignIn} from '@core/profile-management.ts';
import type {SignInKind} from '@core/app-sessions.ts';
import {passwordStrength} from '@core/password-strength.ts';
import {LocalMoveCustody} from '../../platform/local-move';
import {routePath} from '../../../../packages/contracts/src/routes.ts';
import {RouteError, routeOrigin} from '@core/route-identity.ts';
import {createBrowserOwnerClaim, type BrowserOwnerClaim, type OwnerClaimAttempt} from '../../platform/owner-claim';
import {ownerSwitchContextAllowed} from '../../platform/secure-pending';

type Claim = Pick<BrowserOwnerClaim, 'prepare' | 'cancel' | 'cancelOperation'>;
type Snapshot = Readonly<{busy: boolean; preview?: AccountMove; confirmed: boolean}>;
const empty: Snapshot = Object.freeze({busy: false, confirmed: false});
/** The calling surface's own liveness. Its exact opening/ticket supplies an abort
 * signal that is retired synchronously with that surface, so a confirmation can
 * never send credentials after the dialog that collected them has gone. */
export type OwnerMoveCaller = Readonly<{
  isCurrent: () => boolean;
  signal?: AbortSignal;
}>;
export type OwnerSwitchOptions = Readonly<{
  serverId: string; hostedOrigin: string; isCurrent: () => boolean;
  /** The app's saved-selection revision, read for the confirmed local move. */
  selectionRevision?: () => Promise<number>;
  retireLateGrant?: MoveClientOptions['retireLateGrant'];
  secureContextAllowed?: () => boolean; createClaim?: () => Claim;
}>;
const ended = () => new DOMException('The owner switch ended.', 'AbortError');

/** Initiation only. Return routing and the helper's already-admitted result belong to Entry. */
export class SettingsOwnerSwitch {
  private readonly moves: AccountMoveClient;
  private claim?: Claim;
  private state = empty;
  private readonly listeners = new Set<() => void>();
  private request?: AbortController;
  private epoch = 0;
  private closed = false;
  private deliveredAttemptId?: string;
  private move: AccountMove | null = null;
  private moveReading = false;
  private readonly moveListeners = new Set<() => void>();
  private readonly origin: string;
  private readonly routes: ReturnType<HttpLocalApi['getRouteConnection']>;
  /** One captured server/sign-in context, stable across rebuilt sources. */
  readonly contextId: string;
  constructor(private readonly api: HttpLocalApi, private readonly options: OwnerSwitchOptions) {
    this.origin = api.baseUrl;this.routes = api.getRouteConnection();
    this.contextId = JSON.stringify([options.serverId, this.origin, options.hostedOrigin]);
    this.moves = new AccountMoveClient(api, {serverId: options.serverId, isCurrent: this.current, retireLateGrant: this.retire});
  }
  private retire = async (session: LocalSession) => {
    if (this.options.retireLateGrant) return this.options.retireLateGrant(session);
    const temporary = new HttpLocalApi(this.origin, session.accessToken);
    if (this.routes) temporary.useRoutes(this.routes);
    await temporary.request(routePath('delete_sessions_current'), 'DELETE', undefined, AbortSignal.timeout(5000)).catch(() => {});
  };
  private current = () => !this.closed && this.options.isCurrent();
  getSnapshot = () => this.current() ? this.state : empty;
  subscribe = (listener: () => void) => {this.listeners.add(listener);return () => {this.listeners.delete(listener);};};
  private publish(state: Snapshot) {if (this.current()) {this.state = Object.freeze(state);for (const listener of this.listeners) listener();}}
  private guard() {
    if (!this.current()) throw ended();
    if (!(this.options.secureContextAllowed ?? ownerSwitchContextAllowed)()) throw new RouteError('secure_context_required', 'This continuation requires a secure browser context.');
    if (!this.options.hostedOrigin.startsWith('https:') || routeOrigin(this.options.hostedOrigin) !== this.options.hostedOrigin) throw new Error('Invalid owner handoff origin.');
  }
  private operation() {
    this.guard();
    if (this.state.busy) throw new Error('An owner switch request is already running.');
    // The real factory checks encryption, IDB and native locks before even a tab locator.
    this.claim ??= (this.options.createClaim ?? createBrowserOwnerClaim)();
    const epoch = ++this.epoch, request = this.request = new AbortController();
    this.publish({...this.state, busy: true});
    return {signal: request.signal, check: () => {this.guard();if (request.signal.aborted || epoch !== this.epoch) throw ended();},
      finish: () => {if (this.current() && epoch === this.epoch) this.publish({...this.state, busy: false});}};
  }
  /** The drawn Change group has both directions. The target is the viewer's choice,
   * never assumed: local and Portico moves use the same captured preview/confirm
   * controller and the same currentness guards. */
  async previewDirection(to: SignInKind, startOver: boolean): Promise<AccountMove> {
    if (this.state.preview || this.state.confirmed) throw new Error('Close the current owner switch first.');
    if (to !== 'local' && to !== 'portico') throw new Error('Invalid sign-in direction.');
    const operation = this.operation();
    try {
      const preview = await this.moves.preview(to, startOver, operation.signal);operation.check();
      if (preview.to !== to || preview.from === to || preview.stage !== 'preview' || preview.startOver !== startOver) throw new Error('The owner move changed.');
      this.publish({busy: true, confirmed: false, preview});return preview;
    } finally {operation.finish();}
  }
  async preview(startOver: boolean): Promise<AccountMove> { return this.previewDirection('portico', startOver); }
  /** A move to local ends in the server's own signed-in grant. The new local
   * owner's username and password are the caller's own draft (the server validates
   * them against the canonical policy); none is synthesised here. The returned
   * custody is the single-use grant plus the issuer facts captured at confirmation,
   * and it is retired through that original issuer if any step before admission
   * fails; this controller never stores or adopts a session. An optional caller
   * liveness/abort is joined before IO so a retired dialog cannot send credentials,
   * and a caller that retires after the response still has its fresh grant retired. */
  async confirmLocal(credentials: Readonly<{username: string; password: string}>, confirmation: string, caller?: OwnerMoveCaller): Promise<LocalMoveCustody> {
    const preview = this.state.preview;
    if (!preview || preview.to !== 'local' || this.state.confirmed) throw new Error('A fresh local move preview is required.');
    // Read the caller's draft once, before any await.
    const username = typeof credentials.username === 'string' ? credentials.username.trim() : '';
    const password = typeof credentials.password === 'string' ? credentials.password : '';
    if (!username || username.length > 64 || username !== credentials.username) throw new Error('Enter the local owner username.');
    if (!password || password.length > 72 || !passwordStrength(password).acceptable) throw new Error('Choose a stronger local owner password.');
    const pin = this.api.getRouteConnection()?.pin;
    if (!pin) throw new Error('The server identity is not current.');
    if (!this.options.selectionRevision) throw new Error('The saved server selection is unavailable.');
    const operation = this.operation();
    const callerLive = () => !caller || (caller.isCurrent() && caller.signal?.aborted !== true);
    let custody: LocalMoveCustody | undefined;
    try {
      if (!callerLive()) throw ended();
      const revision = await this.options.selectionRevision();
      operation.check();
      // The dialog can retire while the first read is in flight; refuse before IO.
      if (!callerLive()) throw ended();
      const signal = caller?.signal ? AbortSignal.any([operation.signal, caller.signal]) : operation.signal;
      const result: AccountMoveConfirmation = await this.moves.confirm({moveId: preview.id, expectedRevision: preview.revision, confirmation, username, password}, signal);
      // Register the returned grant immediately: from here every later guard or
      // publication failure still has custody to retire.
      if (result.kind === 'local' && result.signIn.kind === 'signed-in') {
        const grant = result.signIn;
        custody = new LocalMoveCustody({grant, issuer: Object.freeze({origin: this.api.baseUrl, pin}), serverId: this.options.serverId, selectionRevision: revision},
          () => this.retire(grant.session));
      }
      operation.check();
      if (!callerLive()) throw ended();
      if (!custody) throw new Error('Unexpected owner move continuation.');
      const confirmedCustody = custody;
      if (await this.options.selectionRevision() !== revision) throw new Error('The server selection changed.');
      operation.check();
      if (!callerLive()) throw ended();
      this.publish({busy: false, confirmed: false});
      operation.check();
      this.refreshMove();
      return confirmedCustody;
    } catch (error) {
      if (custody) await custody.retire();
      throw error;
    } finally {operation.finish();}
  }
  async confirmAndPrepare(confirmation: string): Promise<string> {
    const preview = this.state.preview;
    if (!preview || this.state.confirmed) throw new Error('A fresh owner move preview is required.');
    const operation = this.operation();
    try {
      const result = await this.moves.confirm({moveId: preview.id, expectedRevision: preview.revision, confirmation, username: '', password: ''}, operation.signal);
      if (result.kind !== 'portico') {
        if (result.signIn.kind === 'signed-in') await this.retire(result.signIn.session);
        throw new Error('Unexpected owner move continuation.');
      }
      operation.check();const move = result.move;
      if (move.id !== preview.id || move.revision !== preview.revision || move.from !== 'local' || move.to !== 'portico' || move.stage !== 'preview' || move.startOver !== preview.startOver) throw new Error('The owner move changed.');
      this.publish({busy: true, preview: move, confirmed: true});
      return await this.prepare(operation);
    } finally {operation.finish();}
  }
  /** Begin the distinct change-owner claim on a Portico-account server. The
   * current owner approves a new Portico Account on Hosted; this controller
   * prepares only the existing owner claim handoff, never a move. */
  async prepareChangeOwner(caller?: OwnerMoveCaller, attempt: OwnerClaimAttempt = {}): Promise<string> {
    this.deliveredAttemptId = undefined;
    const operation = this.operation();
    try {
      operation.check();
      const url = await this.claim!.prepare(this.api, this.options.hostedOrigin, 'change-owner', 'admin', caller, attempt);
      if (caller && (!caller.isCurrent() || caller.signal?.aborted === true)) {
        await attempt.lease?.cancel().catch(() => {});
        throw new DOMException('The change owner opening ended.', 'AbortError');
      }
      try {operation.check();} catch (error) {await attempt.lease?.cancel().catch(() => {});throw error;}
      this.deliveredAttemptId = attempt.lease?.attemptId;
      return url;
    } finally {operation.finish();}
  }
  async retryPreparation(): Promise<string> {
    if (!this.state.confirmed) throw new Error('The owner move has not been confirmed.');
    const operation = this.operation();
    try {return await this.prepare(operation);} finally {operation.finish();}
  }
  private async prepare(operation: ReturnType<SettingsOwnerSwitch['operation']>): Promise<string> {
    const attempt: OwnerClaimAttempt = {};
    operation.check();const url = await this.claim!.prepare(this.api, this.options.hostedOrigin, 'switch', 'admin', undefined, attempt);
    try {operation.check();} catch (error) {await attempt.lease?.cancel().catch(() => {});throw error;}
    this.deliveredAttemptId = attempt.lease?.attemptId;
    return url;
  }
  /** The move actually in flight, from the same owner-fenced read the rows use. */
  getMove = (): AccountMove | null => {
    const move = this.current() ? this.move : null;
    return move && (move.stage === 'moving_to_local' || move.stage === 'moving_to_portico') ? move : null;
  };
  subscribeMove = (listener: () => void) => {this.moveListeners.add(listener);return () => {this.moveListeners.delete(listener);};};
  private publishMove(move: AccountMove | null) {
    if (!this.current()) return;
    this.move = move;
    for (const listener of this.moveListeners) listener();
  }
  /** One read of the current move. A refusal or outage leaves the page without a
   * claim of its own, so the drawn rows stay exactly as they were. */
  refreshMove(signal?: AbortSignal) {
    if (!this.current() || this.moveReading) return;
    this.moveReading = true;
    void this.moves.current(signal).then(move => {this.publishMove(move);}, () => undefined)
      .finally(() => {this.moveReading = false;});
  }
  private finishing = false;
  /** The origin this server was actually proved at, or nothing. A page that cannot
   * prove it (a hosted shell, for instance) must never mint a link to another one. */
  issuerOrigin(): string | undefined {
    if (!this.current()) return undefined;
    try {
      const origin = routeOrigin(this.origin);
      if (origin !== this.origin) return undefined;
      if (this.routes) return this.routes.logicalOrigin === origin ? origin : undefined;
      const host = new URL(origin).hostname;
      return host === '127.0.0.1' || host === '[::1]' || host === 'localhost' ? origin : undefined;
    } catch { return undefined; }
  }
  /** Finish now: exactly the move the caller rendered, with its own opening ticket.
   * A stale caller or a replaced move never finishes a successor, and the finish
   * joins this controller's owned lifetime, so cancel/dispose retire it. */
  async finishMove(rendered: AccountMove, caller?: OwnerMoveCaller) {
    if (this.finishing) throw new Error('An owner move finish is already running.');
    const move = this.move, callerLive = () => !caller || (caller.isCurrent() && caller.signal?.aborted !== true);
    if (!this.current() || this.state.busy || !callerLive() || !move || move.id !== rendered.id || move.revision !== rendered.revision ||
      (move.stage !== 'moving_to_local' && move.stage !== 'moving_to_portico')) throw new Error('There is no move in progress to finish.');
    this.finishing = true;
    // The owned controller is the one cancel()/dispose() abort, not a private stray.
    this.request?.abort();
    const own = this.request = new AbortController();
    const live = () => this.current() && this.request === own && !own.signal.aborted && callerLive();
    const signal = caller?.signal ? AbortSignal.any([own.signal, caller.signal]) : own.signal;
    try {
      await this.moves.finish(move, signal);
      const now = this.move;
      // Publication requires the same rendered move, the same caller and this ticket.
      if (!live() || !now || now.id !== rendered.id || now.revision !== rendered.revision) return;
      this.publishMove(null);
      this.refreshMove();
    } finally {
      this.finishing = false;
      if (this.request === own) this.request = undefined;
    }
  }
  /** The exact people still waiting, for the codes host; owner-fenced server read. */
  waiting(signal?:AbortSignal){return this.moves.waiting(signal);}
  /** One memory-only receipt for the given exact targets; admits nobody. */
  codes(accountIds:readonly string[],signal?:AbortSignal,rendered?:AccountMove){
    if(rendered&&this.getMove()!==rendered)throw ended();
    return this.moves.codes(accountIds,signal,rendered);
  }
  async cancelPreview() {
    const preview = this.state.preview;
    if (!preview || this.state.confirmed) throw new Error('Only an unconfirmed move preview can be canceled.');
    const operation = this.operation();
    try {await this.moves.cancelPreview(preview, operation.signal);operation.check();await this.cancel();}
    finally {operation.finish();}
  }
  async cancelOperation() {
    const operation = this.operation();
    try {await this.claim!.cancelOperation(this.api);operation.check();await this.cancel();}
    finally {operation.finish();}
  }
  async cancel() {
    const epoch = ++this.epoch;this.request?.abort();this.moves.cancel();this.deliveredAttemptId = undefined;
    await this.claim?.cancel();if (this.current() && epoch === this.epoch) this.publish(empty);
  }
  dispose() {
    this.closed = true;++this.epoch;this.request?.abort();this.moves.dispose();this.state = empty;this.listeners.clear();
    // A full-document approval handoff must survive teardown in the original origin.
    if (!this.deliveredAttemptId) void this.claim?.cancel().catch(() => {});
  }
}
