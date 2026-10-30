import type {DirectSignIn} from '@core/profile-management.ts';
import type {ServerPin} from '@core/route-identity.ts';

/** The facts a completed owner move to local carries. The grant is single-use and
 * never enters a URL, storage or public state; the issuer facts are the ones
 * captured when the server confirmed the move. */
export type LocalMove = Readonly<{
  grant: Extract<DirectSignIn, {kind: 'signed-in'}>;
  issuer: Readonly<{origin: string; pin: ServerPin}>;
  serverId: string;
  /** The app's saved-selection revision at confirmation; not the move revision. */
  selectionRevision: number;
}>;

/** One-shot custody of a fresh grant. It is owned until admission transfers it;
 * after that the entry's accept owns cleanup, so a repeated claim, admission or
 * discard must never retire the now-adopted family. */
export class LocalMoveCustody {
  private state: 'owned' | 'transferring' | 'transferred' | 'retired' = 'owned';
  private readonly facts: LocalMove;
  private readonly revoke: () => Promise<void>;
  constructor(facts: LocalMove, revoke: () => Promise<void>) {this.facts = Object.freeze(facts);this.revoke = revoke;}
  /** Which server and new local account this move signs in to. Not a credential. */
  get target(): Readonly<{serverId: string; accountId: string}> {return Object.freeze({serverId: this.facts.serverId, accountId: this.facts.grant.snapshot.account.id});}
  /** The facts, exactly once, while this custody is still owned. */
  claim = (): LocalMove | undefined => {
    if (this.state !== 'owned') return undefined;
    this.state = 'transferring';
    return this.facts;
  };
  /** Admission takes the grant here: accept owns it from now on and retire is
   * disarmed. Returns whether this call was the one that took it, so admission can
   * refuse a custody that a concurrent retire already shut. There is deliberately
   * no way back to owned: a claim is final. */
  transfer = (): boolean => {
    if (this.state !== 'transferring') return false;
    this.state = 'transferred';
    return true;
  };
  /** Revoke only while still ours; never after transfer, and only once. */
  retire = async (): Promise<void> => {
    if (this.state === 'transferred' || this.state === 'retired') return;
    this.state = 'retired';
    await this.revoke().catch(() => {});
  };
}

/** In-memory, single-use handoff owned outside the retiring Settings view. */
let pending: LocalMoveCustody | undefined;
export function handOffLocalMove(custody: LocalMoveCustody): void {
  const previous = pending; pending = custody;
  // A replaced handoff is retired only while it is still unclaimed.
  if (previous && previous !== custody) void previous.retire();
}
export function takeLocalMove(): LocalMoveCustody | undefined {const value = pending; pending = undefined; return value;}
export function discardLocalMove(): void {const value = pending; pending = undefined; if (value) void value.retire();}
