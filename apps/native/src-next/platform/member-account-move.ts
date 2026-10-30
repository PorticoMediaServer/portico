import {decode} from '../../../../packages/contracts/src/runtime';
import type {RouteRequest} from '../../../../packages/contracts/src/routes';
import {ApiError, type LocalSession} from '../../../../packages/client-core/src';
import {MemberAccountMoveClient, accountMoveLink, consumeAccountMoveFragment, type MoveCodePreview, type MoveCollisionDetails, type MoveCredential} from '../../../../packages/client-core/src/account-move';
import type {DirectSignIn, ProfileAPI} from '../../../../packages/client-core/src/profile-management';
import {requestIdentityAssertion, requestPorticoChallenge, type HostedRequestApi} from '../../../../packages/client-core/src/portico-servers';
import {routeOrigin, type RouteCrypto, type ServerPin} from '../../../../packages/client-core/src/route-identity';
import {CodedError} from '../../../../packages/client-core/src/server-messages';
import type {ServerSignIn} from '../../../../packages/client-core/src/sign-in';
import {hostedUrl} from './account-authority';

export type NativeMoveHostedCapture = Readonly<{
  api: HostedRequestApi; origin: string; accountId: string; familyId: string;
  isCurrent: () => boolean; assertCurrent: () => Promise<void>;
}>;

/** Construct only inside the verified NativeServerSignIn operation. The async
 * fence compares its captured pin, installation, store revision and viewer;
 * the synchronous fence is also supplied to the canonical command client. */
export type NativeMemberMovePorts<Result> = Readonly<{
  api: ProfileAPI; server: ServerSignIn; pin: ServerPin; crypto: RouteCrypto;
  /** Captured verified issuer; absent legacy callers refuse links. */
  origin?: string;
  installationId: string; signal: AbortSignal;
  isCurrent: () => boolean; assertCurrent: () => Promise<void>;
  retainedAppFamilyId?: string; operationAccountFamilyId: () => string | undefined;
  /** Closed over the original issuing API/pin. Never a selected/default API. */
  retireLateGrant: (fresh: LocalSession) => Promise<void>;
  /** The SAME operation's normal DirectSignIn/challenge/profile handling. */
  adopt: (result: DirectSignIn, hosted?: NativeMoveHostedCapture) => Promise<Readonly<{result: Result; isCurrent: () => boolean}>>;
  captureHosted: () => Promise<NativeMoveHostedCapture>;
  release: () => void;
}>;

export type NativeMemberMoveSnapshot<Result> = Readonly<{
  phase: 'password' | 'code' | 'portico' | 'local' | 'collision' | 'adopted';
  busy: boolean; username?: string; collision?: MoveCollisionDetails; result?: Result; error?: unknown;
}>;
type Proof = Readonly<{credential: MoveCredential; expiresAt: string; preview?: MoveCodePreview}>;
const cancelled = () => new CodedError('cancelled', 'This account move request ended.');
const invalidProof = () => new CodedError('move_code_invalid', 'The move proof expired.');

/** Operation-local member move. A ticket/code never enters snapshots, storage,
 * navigation, diagnostics or a later operation, and is never renewed/replayed. */
export class NativeMemberAccountMove<Result> {
  private readonly ports: NativeMemberMovePorts<Result>;
  private readonly initial: 'password' | 'code';
  private state: NativeMemberMoveSnapshot<Result>;
  private readonly listeners = new Set<() => void>();
  private epoch = 0;
  private disposed = false;
  private proof?: Proof;
  private client?: MemberAccountMoveClient;
  private abort?: AbortController;
  private accepted?: Readonly<{epoch: number; result: Result; isCurrent: () => boolean}>;

  constructor(ports: NativeMemberMovePorts<Result>, initial: 'password' | 'code') {
    if (ports.server.id !== ports.pin.serverId || !ports.installationId || !ports.isCurrent()
      || ports.server.move === null || initial === 'password' && (ports.server.signIn !== 'portico' || ports.server.move !== 'to_portico')) throw cancelled();
    this.ports = Object.freeze({...ports, pin: Object.freeze({...ports.pin}), server: Object.freeze({...ports.server})}); this.initial = initial;
    this.state = Object.freeze({phase: initial, busy: false});
    ports.signal.addEventListener('abort', this.dispose, {once: true});
    if (ports.signal.aborted) this.dispose();
  }
  getSnapshot = () => this.state;
  /** Read-only eligibility for UI work on this exact immutable issuing operation. */
  isCurrentOpening = () => this.current(this.epoch);

  /** A link supplies input only to this already-proved issuer. Never follow its URL. */
  readCodeInput = (raw: string): string | undefined => {
    if (!this.isCurrentOpening()) return;
    if (/^[A-Za-z0-9_-]{43}$/.test(raw)) return raw;
    if (!this.ports.origin || raw.length > 8192) return;
    try {
      const url = new URL(raw);
      if (url.origin !== routeOrigin(this.ports.origin)) return;
      // Decode only this private URL object; no navigation/history/storage write.
      const link = consumeAccountMoveFragment(url, {replaceState: () => {}});
      if (!link || link.id !== undefined && link.id !== this.ports.server.id
        || raw !== accountMoveLink(this.ports.origin, link.code, link.id)) return;
      return this.isCurrentOpening() ? link.code : undefined;
    } catch { return; }
  };

  takeAcceptedResult = (): Result | undefined => {
    const accepted = this.accepted; this.accepted = undefined;
    return accepted && !this.disposed && accepted.epoch === this.epoch && !this.ports.signal.aborted && accepted.isCurrent()
      ? accepted.result : undefined;
  };
  subscribe = (listener: () => void) => {if (this.disposed) return () => {}; this.listeners.add(listener); return () => {this.listeners.delete(listener);};};
  private current(epoch: number, hosted?: NativeMoveHostedCapture) {
    return !this.disposed && epoch === this.epoch && !this.ports.signal.aborted && this.ports.isCurrent() && (!hosted || hosted.isCurrent());
  }
  private async fence(epoch: number, hosted?: NativeMoveHostedCapture) {
    if (!this.current(epoch, hosted)) throw cancelled();
    await this.ports.assertCurrent();
    if (!this.current(epoch, hosted)) throw cancelled();
    if (hosted) {
      await hosted.assertCurrent();
      if (!this.current(epoch, hosted)) throw cancelled();
    }
  }
  private publish(patch: Partial<NativeMemberMoveSnapshot<Result>>) {
    if (this.disposed) return;
    this.state = Object.freeze({...this.state, ...patch});
    for (const listener of [...this.listeners]) listener();
  }
  private fresh(session: LocalSession) {
    return session.viewer.serverId === this.ports.pin.serverId && session.installationId === this.ports.installationId
      && session.sessionFamilyId !== this.ports.retainedAppFamilyId
      && session.sessionFamilyId !== this.ports.operationAccountFamilyId();
  }
  private async retireFresh(session: LocalSession) {
    // The canonical client supplies only the freshly returned grant. Protect
    // both pre-existing families even if the server returns a confused answer.
    if (!this.fresh(session)) return;
    await this.ports.retireLateGrant(session);
  }
  private async command(work: (epoch: number, signal: AbortSignal, client: MemberAccountMoveClient) => Promise<void>) {
    if (this.disposed || this.state.busy || !this.ports.isCurrent()) return;
    const epoch = ++this.epoch, abort = this.abort = new AbortController();
    const client = this.client = new MemberAccountMoveClient(this.ports.api, {
      serverId: this.ports.server.id, isCurrent: () => this.current(epoch), retireLateGrant: session => this.retireFresh(session),
    });
    this.publish({busy: true, error: undefined, collision: undefined});
    try {await this.fence(epoch); await work(epoch, abort.signal, client);}
    catch (error) {
      if (this.current(epoch)) this.publish({error, ...(!this.proof ? {phase: this.initial, username: undefined} : {}), ...(error instanceof ApiError && error.code === 'move_collision' && error.current
        ? {phase: 'collision' as const, collision: error.current} : {})});
    } finally {
      client.dispose();
      if (this.client === client) this.client = undefined;
      if (this.abort === abort) this.abort = undefined;
      if (epoch === this.epoch) this.publish({busy: false});
    }
  }
  proveOldPassword(username: string, password: string) {
    if (this.state.phase !== 'password' || this.ports.server.signIn !== 'portico' || this.ports.server.move !== 'to_portico') return Promise.resolve();
    return this.command(async (epoch, signal, client) => {
      this.proof = undefined;
      const ticket = await client.password(username, password, signal);
      await this.fence(epoch);
      if (Date.parse(ticket.expiresAt) <= Date.now()) throw invalidProof();
      this.proof = Object.freeze({credential: {ticket: ticket.ticket}, expiresAt: ticket.expiresAt});
      this.publish({phase: 'portico'});
    });
  }
  previewCode(code: string) {
    if (this.state.phase !== 'code') return Promise.resolve();
    return this.command(async (epoch, signal, client) => {
      this.proof = undefined;
      const preview = await client.previewCode(code, signal);
      await this.fence(epoch);
      if (preview.signIn !== this.ports.server.signIn) throw new CodedError('identity_mismatch', 'The server sign-in changed.');
      if (Date.parse(preview.expiresAt) <= Date.now()) throw invalidProof();
      this.proof = Object.freeze({credential: {code}, expiresAt: preview.expiresAt, preview});
      this.publish({phase: preview.signIn === 'local' ? 'local' : 'portico', username: preview.username});
    });
  }
  private takeProof() {
    const proof = this.proof; this.proof = undefined;
    if (!proof || Date.parse(proof.expiresAt) <= Date.now()) throw invalidProof();
    return proof;
  }
  private async adopt(result: DirectSignIn, epoch: number, hosted?: NativeMoveHostedCapture) {
    let handedOff = false;
    try {
      await this.fence(epoch, hosted);
      if (!this.current(epoch, hosted)) throw cancelled();
      if (result.kind === 'signed-in' && !this.fresh(result.session))
        throw new CodedError('identity_mismatch', 'The account move did not issue a fresh session.');
      handedOff = true;
      const adopted = await this.ports.adopt(result, hosted);
      // Once handed off, only that normal operation owns grant cleanup. Do
      // not revoke an adopted profile/account family if the page retires.
      if (!this.disposed && epoch === this.epoch && !this.ports.signal.aborted
        && (!hosted || hosted.isCurrent()) && adopted.isCurrent()) {
        this.accepted = {epoch, result: adopted.result, isCurrent: () => (!hosted || hosted.isCurrent()) && adopted.isCurrent()};
        this.publish({phase: 'adopted', result: adopted.result});
      }
    } catch (error) {
      if (!handedOff && result.kind === 'signed-in') await this.retireFresh(result.session);
      throw error;
    }
  }
  redeemLocal(username: string, password: string) {
    if (this.state.phase !== 'local' || this.ports.server.signIn !== 'local') return Promise.resolve();
    return this.command(async (epoch, signal, client) => {
      const proof = this.takeProof();
      if (!proof.credential.code || proof.preview?.signIn !== 'local') throw invalidProof();
      const result = await client.redeemLocal(proof.credential.code, username, password, signal);
      await this.adopt(result, epoch);
    });
  }
  redeemPortico() {
    if (this.state.phase !== 'portico' || this.ports.server.signIn !== 'portico' || this.ports.server.move !== 'to_portico') return Promise.resolve();
    return this.command(async (epoch, signal) => {
      const proof = this.takeProof();
      // Capture the official Hosted origin/account/family ONCE for this command.
      const captured = await this.ports.captureHosted();
      const hosted: NativeMoveHostedCapture = Object.freeze({...captured});
      await this.fence(epoch, hosted);
      if (routeOrigin(hosted.origin) !== routeOrigin(hostedUrl) || !hosted.accountId || !hosted.familyId) throw cancelled();
      const challenge = await requestPorticoChallenge(this.ports.api, this.ports.server.id, signal, this.ports.crypto);
      await this.fence(epoch, hosted);
      const identity = await requestIdentityAssertion(hosted.api, hosted.origin, this.ports.server.id, challenge, 'interactive', signal);
      await this.fence(epoch, hosted);
      if (Date.parse(proof.expiresAt) <= Date.now()) throw invalidProof();
      const client = new MemberAccountMoveClient(this.ports.api, {serverId: this.ports.server.id,
        isCurrent: () => this.current(epoch, hosted), retireLateGrant: session => this.retireFresh(session)});
      try {
        // Validate the optional certificate against the official request schema;
        // the Hosted assertion transport intentionally exposes it as unknown.
        const checked = decode('PostAuthMovePorticoRequest', {...proof.credential, porticoIdentity: identity}) as RouteRequest<'post_auth_move_portico'>;
        const result = await client.redeemPortico(proof.credential, checked.porticoIdentity, signal);
        await this.adopt(result, epoch, hosted);
      } finally {client.dispose();}
    });
  }
  cancel = () => {
    ++this.epoch; this.proof = undefined; this.accepted = undefined; this.abort?.abort(); this.client?.dispose();
    this.abort = undefined; this.client = undefined;
    this.publish({phase: this.initial, busy: false, username: undefined, collision: undefined, result: undefined, error: undefined});
  };
  dispose = () => {
    if (this.disposed) return;
    this.cancel(); this.disposed = true; this.ports.signal.removeEventListener('abort', this.dispose); this.ports.release(); this.listeners.clear();
  };
}
