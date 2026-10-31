import type {OwnSetup, SetupState} from './setup-state.ts';
import type {SetupClaimHandoff} from './setup-claim-client.ts';

/** The two protected records of one first-run setup on one server, and how to judge them against what the server says.
 * Pure: storage, compare-and-swap writes and the diagnostic note belong to each platform's store (B-DESIGN §6.3). */

/** The setup the person started here: the bootstrap token and who it is for. */
export type SetupRecord = Readonly<{
  version: 2; serverId: string; requestId: string; setupToken: string; tokenAt: number; name: string; authMode: 'hosted' | 'local';
}>;

/** One Portico Account approval attempt: the PKCE half that never leaves this tab or app, and the signed handoff once bound. */
export type SetupAttempt = Readonly<{
  version: 2; serverId: string; requestId: string; attemptId: string; origin: string; hostedOrigin: string;
  installationId: string; state: string; verifier: string; codeChallenge: string; createdAt: number; expiresAt: string;
  handoff?: SetupClaimHandoff;
}>;

/** What `read()` of a store returns. `unreadable` is set when any stored bytes were malformed or from an older version. */
export type SetupLocal = Readonly<{record?: SetupRecord; attempt?: SetupAttempt; unreadable?: true}>;

/** What GET /v1/setup/state said, read with the record's token. `unknown` is a status or phase this app doesn't know
 * (a newer server). `delivery.slow` is the server's own "taking longer" fact, when it sends one. */
export type SetupReading = Readonly<{
  status: SetupState['status'] | 'unknown';
  retryAfterSeconds?: number;
  setup?: (OwnSetup & Readonly<{delivery?: Readonly<{slow?: boolean}>}>) | 'unknown';
}>;

export type Unreadable = Readonly<{unreadable: true}>;
export const unreadable: Unreadable = Object.freeze({unreadable: true});
export const isUnreadable = (value: unknown): value is Unreadable => !!value && typeof value === 'object' && (value as {unreadable?: unknown}).unreadable === true;

const isObject = (v: unknown): v is Record<string, unknown> => !!v && typeof v === 'object' && !Array.isArray(v);
const text = (v: unknown): v is string => typeof v === 'string' && v.length > 0;
const stamp = (v: unknown): v is number => typeof v === 'number' && Number.isSafeInteger(v) && v >= 0;
const signed = (v: unknown): v is {payload: string; signature: string} => isObject(v) && text(v.payload) && text(v.signature);

function handoffOf(raw: unknown): SetupClaimHandoff | undefined {
  if (!isObject(raw) || !isObject(raw.expected) || !isObject(raw.binding) || !text(raw.transactionId) || !text(raw.expiresAt) || !signed(raw.claim) || !signed(raw.start)) return undefined;
  return raw as unknown as SetupClaimHandoff;
}

/** Never throws. Unreadable, older or malformed input is `{unreadable: true}`. */
export function parseSetupRecord(raw: unknown): SetupRecord | Unreadable {
  try {
    if (!isObject(raw) || raw.version !== 2 || !text(raw.serverId) || !text(raw.requestId) || !text(raw.setupToken) || !stamp(raw.tokenAt)
      || typeof raw.name !== 'string' || (raw.authMode !== 'hosted' && raw.authMode !== 'local')) return unreadable;
    return Object.freeze({version: 2, serverId: raw.serverId, requestId: raw.requestId, setupToken: raw.setupToken, tokenAt: raw.tokenAt, name: raw.name, authMode: raw.authMode});
  } catch {
    return unreadable;
  }
}

/** Never throws. A present handoff that is malformed makes the whole attempt unreadable: a half-bound attempt is never used. */
export function parseSetupAttempt(raw: unknown): SetupAttempt | Unreadable {
  try {
    if (!isObject(raw) || raw.version !== 2 || !text(raw.serverId) || !text(raw.requestId) || !text(raw.attemptId) || !text(raw.origin) || !text(raw.hostedOrigin)
      || !text(raw.installationId) || !text(raw.state) || !text(raw.verifier) || !text(raw.codeChallenge) || !stamp(raw.createdAt) || !text(raw.expiresAt)) return unreadable;
    const base = {version: 2 as const, serverId: raw.serverId, requestId: raw.requestId, attemptId: raw.attemptId, origin: raw.origin, hostedOrigin: raw.hostedOrigin,
      installationId: raw.installationId, state: raw.state, verifier: raw.verifier, codeChallenge: raw.codeChallenge, createdAt: raw.createdAt, expiresAt: raw.expiresAt};
    if (raw.handoff === undefined) return Object.freeze(base);
    const handoff = handoffOf(raw.handoff);
    return handoff ? Object.freeze({...base, handoff}) : unreadable;
  } catch {
    return unreadable;
  }
}

/** Reads both stored values into one `SetupLocal`; either being unreadable marks the whole read unreadable. A missing value is simply absent. */
export function readSetupLocal(rawRecord: unknown, rawAttempt: unknown): SetupLocal {
  const record = rawRecord === undefined || rawRecord === null ? undefined : parseSetupRecord(rawRecord);
  const attempt = rawAttempt === undefined || rawAttempt === null ? undefined : parseSetupAttempt(rawAttempt);
  if (isUnreadable(record) || isUnreadable(attempt)) return Object.freeze({unreadable: true});
  return Object.freeze({...(record ? {record} : {}), ...(attempt ? {attempt} : {})});
}

/** One platform's protected storage for the two records (B-DESIGN §6.3). */
export interface SetupStore {
  /** Never throws on bad bytes: unreadable, older or malformed values come back as `unreadable`. */
  read(): Promise<SetupLocal>;
  /** Compare-and-swap. A slot is touched only when `next` names it (a value writes, `null` deletes), and is checked against `expect`:
   * the stored record's requestId, the stored attempt's attemptId, `undefined` meaning none (or unreadable). Every touched slot
   * must match or nothing is written and the answer is false: a successor, written by another tab or flow, is preserved. */
  change(expect: Readonly<{requestId?: string; attemptId?: string}>, next: Readonly<{record?: SetupRecord | null; attempt?: SetupAttempt | null}>): Promise<boolean>;
}

/** In memory, for tests and for adapters that keep nothing across restarts. `initial` may be `{unreadable: true}`. */
export function createMemorySetupStore(initial: SetupLocal = {}): SetupStore & Readonly<{peek(): SetupLocal}> {
  let local: SetupLocal = Object.freeze({...initial});
  return {
    peek: () => local,
    async read() {return local;},
    async change(expect, next) {
      const touchesRecord = next.record !== undefined, touchesAttempt = next.attempt !== undefined;
      if (touchesRecord && local.record?.requestId !== expect.requestId) return false;
      if (touchesAttempt && local.attempt?.attemptId !== expect.attemptId) return false;
      const record = touchesRecord ? next.record ?? undefined : local.record, attempt = touchesAttempt ? next.attempt ?? undefined : local.attempt;
      local = Object.freeze({...(record ? {record} : {}), ...(attempt ? {attempt} : {})}); // a successful change clears `unreadable`
      return true;
    },
  };
}

export type SetupReconcile = 'keep' | 'retire' | 'retire-attempt';

const attemptLive = (attempt: SetupAttempt, record: SetupRecord, now: number): boolean => {
  const expires = Date.parse(attempt.expiresAt);
  return attempt.requestId === record.requestId && attempt.serverId === record.serverId && Number.isFinite(expires) && expires > now;
};

/** What to do with the saved setup, given what the server says when asked with the record's own token (B-DESIGN §6.3).
 * `retire` removes the record and the attempt; `retire-attempt` removes the attempt alone; both are compare-and-swap on the requestId.
 * Nothing here ever decides to start, resume or switch anything. */
export function reconcileSetup(local: SetupLocal, server: SetupReading, now: number): SetupReconcile {
  if (local.unreadable) return 'retire';                                       // R-13: unreadable, older or malformed
  const {record, attempt} = local;
  if (!record) return attempt ? 'retire-attempt' : 'keep';                    // an attempt with no record to answer to
  if (server.status === 'unknown' || server.setup === 'unknown') return 'keep'; // a newer server: never destroy on a guess
  if (server.status === 'complete') return 'retire';                          // an account exists; the record's job is over
  const own = server.setup;
  if (!own) return 'retire';                                                  // R-14: open, or a token the server doesn't accept
  if (own.requestId !== record.requestId) return 'retire';                    // R-14: a request the server doesn't know
  if (own.phase === 'expired') return 'retire';                               // never approved, window passed
  if (record.authMode === 'local') return 'retire';                           // a local setup never has a Portico phase
  if (attempt && !attemptLive(attempt, record, now)) return 'retire-attempt'; // a lapsed or foreign attempt; the live record stays
  return 'keep';                                                              // live own
}
