/**
 * A sign-in is its server, account, profile and session family, never its access token. A routine
 * refresh rotates the token and advances the generation inside the same family: code that holds a
 * sign-in follows that rotation instead of treating it as a different sign-in
 * (docs/ENGINEERING-RULES.md). Compare sign-ins here, not with accessToken === / !==.
 */
import type {LocalSession} from './index.ts';
import {releaseQuietly} from './cleanup.ts';

/** Who a stored or live session is, minus its secrets. */
export type SessionAuthority = Readonly<{
  serverId: string;
  authority: LocalSession['viewer']['authority'];
  accountId: string;
  profileId: string;
  sessionFamilyId: string;
  tokenGeneration: string;
}>;

export function sessionAuthority(session: LocalSession): SessionAuthority {
  const {serverId, authority, accountId, profileId} = session.viewer;
  return Object.freeze({serverId, authority, accountId, profileId, sessionFamilyId: session.sessionFamilyId, tokenGeneration: session.tokenGeneration});
}

/** same: same family, generation and token. rotated: same family, newer generation (adopt it).
 *  stale: same family, older generation (keep yours). replaced: another sign-in, or a record that
 *  can't be right (same generation with another token, or an unreadable generation). */
export type AuthorityChange = 'same' | 'rotated' | 'stale' | 'replaced';

const generation = (value: string): bigint | undefined => {
  if (!/^\d{1,30}$/.test(value)) return undefined;
  return BigInt(value);
};

export function compareSessionAuthority(held: LocalSession, current: LocalSession): AuthorityChange {
  const a = held.viewer, b = current.viewer;
  if (a.serverId !== b.serverId || a.authority !== b.authority || a.accountId !== b.accountId || a.profileId !== b.profileId || held.sessionFamilyId !== current.sessionFamilyId) return 'replaced';
  const was = generation(held.tokenGeneration), now = generation(current.tokenGeneration);
  if (was === undefined || now === undefined) return 'replaced';
  if (now > was) return 'rotated';
  if (now < was) return 'stale';
  return held.accessToken === current.accessToken ? 'same' : 'replaced';
}

/** The sign-in a lifecycle owner currently holds, as long-lived code binds to it. */
export type AuthorityTuple = Readonly<{serverId: string; accountId: string; profileId?: string; familyId: string; epoch?: number}>;

export function authorityTupleOf(session: LocalSession): AuthorityTuple {
  return Object.freeze({serverId: session.viewer.serverId, accountId: session.viewer.accountId, profileId: session.viewer.profileId, familyId: session.sessionFamilyId});
}

export function sameAuthorityTuple(a: AuthorityTuple | undefined, b: AuthorityTuple | undefined): boolean {
  return !!a && !!b && a.serverId === b.serverId && a.accountId === b.accountId && a.profileId === b.profileId && a.familyId === b.familyId && a.epoch === b.epoch;
}

/** Implemented by the session lifecycle. It notifies on every change of sign-in; token rotation
 *  inside the same family does not change the tuple. */
export interface AuthoritySource {
  authority(): AuthorityTuple | undefined;
  subscribe(listener: () => void): () => void;
}

export class AuthorityChangedError extends Error {
  constructor() {
    super('The sign-in changed.');
    this.name = 'AuthorityChangedError';
  }
}

export type AuthorityBinding = Readonly<{
  /** True while the source still holds the tuple captured at bind time. */
  isCurrent(): boolean;
  /** Throws AuthorityChangedError once it isn't. */
  assertCurrent(): void;
  /** Aborts on the first change of sign-in, or on dispose. */
  signal: AbortSignal;
  /** Stops watching and aborts the signal. Never throws. */
  dispose(): void;
}>;

/** Binds work to the sign-in the source holds now. A source holding nothing gives a binding that is
 *  already ended. */
export function bindAuthority(source: AuthoritySource): AuthorityBinding {
  const bound = source.authority(), controller = new AbortController();
  let unsubscribe: (() => void) | undefined;
  const end = () => {
    if (unsubscribe) {const release = unsubscribe; unsubscribe = undefined; releaseQuietly('authority binding', release);}
    if (!controller.signal.aborted) releaseQuietly('authority binding abort', () => controller.abort());
  };
  // A change seen by a read ends the binding at once, so isCurrent and the signal never disagree.
  const holds = () => {
    if (controller.signal.aborted) return false;
    if (sameAuthorityTuple(bound, source.authority())) return true;
    end();
    return false;
  };
  if (!bound) end();
  else {
    const release = source.subscribe(() => {holds();});
    // A source may notify inside subscribe, before release is known.
    if (controller.signal.aborted) releaseQuietly('authority binding', release);
    else unsubscribe = release;
    // A change between the first read and subscribing is caught here.
    holds();
  }
  return Object.freeze({
    isCurrent: holds,
    assertCurrent() {if (!holds()) throw new AuthorityChangedError();},
    signal: controller.signal,
    dispose: end,
  });
}
