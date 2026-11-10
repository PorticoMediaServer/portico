import {nextDelay, systemScheduler, type Scheduler} from './timer.ts';

/**
 * The writes a person makes while the server is out of reach: watched, favourite, rating, My List,
 * list and playlist edits, and the final position of a stop. Each is kept on the device until the
 * server answers it.
 *
 * An entry's id is its idempotency key, so sending it again is safe. A 2xx answer means it is saved
 * and the entry is deleted. No answer, a 5xx, a redirect, a 429 or an ended sign-in keeps it for another try.
 * Entries are sent in the order they were made. One the server keeps failing on (a 5xx) holds back only
 * the later edits to the same item or list; the rest go ahead.
 * Any other 4xx deletes it and the person is told once. Entries belong to an account and profile:
 * they are sent only while that profile is the one signed in, wait across a new sign-in, and are
 * dropped (and reported) after 30 days. An empty outbox does nothing at all.
 */
export type OutboxScope = Readonly<{serverId: string; authority: string; accountId: string; profileId: string}>;
/** `headers` holds only what the request itself needs again (`If-Match`, a patch `Content-Type`), never a credential. */
export type OutboxWrite = Readonly<{id: string; method: string; path: string; body?: unknown; headers?: Readonly<Record<string, string>>}>;
export type OutboxEntry = OutboxWrite & Readonly<{scope: OutboxScope; createdAt: number; attempts: number}>;
/** `retryAfterMs` is the wait the server asked for, already read from its Retry-After header. */
export type OutboxAnswer = Readonly<{status: number; headers?: Readonly<Record<string, string>>; body?: unknown; retryAfterMs?: number}>;
export interface OutboxStore {
  list(): Promise<OutboxEntry[]>;
  put(entry: OutboxEntry): Promise<void>;
  delete(id: string): Promise<void>;
}
export type OutboxOptions = Readonly<{
  store: OutboxStore;
  /** The account and profile whose token is in use right now, if any. */
  scope: () => OutboxScope | undefined;
  /** Sends one entry with that profile's token and its id as `Idempotency-Key`. Rejects when nothing answered. */
  send: (entry: OutboxEntry, signal: AbortSignal) => Promise<OutboxAnswer>;
  /** An edit that will never be saved: the server refused it, or it is older than 30 days. Called once per edit. */
  onDropped: (entry: OutboxEntry, why: 'refused' | 'expired', answer?: OutboxAnswer) => void;
  scheduler?: Scheduler;
  wallNow?: () => number;
  random?: () => number;
}>;

export const OUTBOX_MAX_AGE_MS = 30 * 24 * 60 * 60 * 1000;
const BACKOFF = {baseMs: 2000, maxMs: 5 * 60 * 1000};
const STORE_RETRY_MS = 30_000;

/** The edit is saved on this device and will be sent when the server can be reached. Not a failure. */
export class OutboxWaiting extends Error {
  readonly code = 'sync_pending';
  constructor() { super('Saved on this device. It will sync when your server can be reached.'); this.name = 'OutboxWaiting'; }
}

const storeFailed = (cause: unknown) => Object.assign(new Error('This device could not keep the change.', {cause}), {code: 'outbox_store_failed'});
type Caller = {promise: Promise<OutboxAnswer>; resolve: (answer: OutboxAnswer) => void; reject: (error: unknown) => void};
export const sameOutboxScope = (a: OutboxScope, b: OutboxScope) =>
  a.serverId === b.serverId && a.authority === b.authority && a.accountId === b.accountId && a.profileId === b.profileId;
/** An answer settles an entry when it is a 2xx, or a 4xx that is about the write itself. Anything else (5xx, a redirect,
 * 429, 408, an ended sign-in) says nothing about the write, and the entry is kept. */
const settles = (status: number) => status >= 200 && status < 300 || status >= 400 && status < 500 && ![401, 408, 429].includes(status);
/** What an entry changes: the item, list or other thing its path names (`/v1/items/{id}`, `/v1/playlists/{id}`). */
const targetOf = (entry: OutboxEntry) => entry.path.split('?')[0]!.split('/').slice(0, 4).join('/');

export class Outbox {
  private entries: OutboxEntry[] = [];
  private callers = new Map<string, Caller>();
  private listeners = new Set<() => void>();
  private loaded?: Promise<void>;
  private running = false;
  private stopped = false;
  private timer: unknown;
  private retryAt = 0;
  private serverAskedToWait = false;
  /** Entries the server failed on (5xx), and when each is tried again. Kept in memory: after a restart each is simply tried once more. */
  private failed = new Map<string, number>();
  private inFlight?: AbortController;
  private readonly clock: Scheduler;
  private readonly options: OutboxOptions;

  constructor(options: OutboxOptions) { this.options = options; this.clock = options.scheduler ?? systemScheduler; }

  /** Reads what an earlier run of the app left waiting. */
  start(): Promise<void> {
    this.loaded ??= this.options.store.list().then(saved => {
      this.entries = saved.sort((a, b) => a.createdAt - b.createdAt);
      this.changed();
      this.wake();
    }, error => { this.loaded = undefined; throw storeFailed(error); });
    return this.loaded;
  }

  /**
   * Keeps the write and sends it. Resolves with the server's first answer, a refusal included: the
   * caller reads it like any answer. Rejects with `OutboxWaiting` when nothing answered: the write
   * is kept and this outbox now owns it. Rejects with code `outbox_store_failed` when it could not be kept.
   * A caller that gives up (`signal`) hears nothing more; a later refusal is then reported by `onDropped`.
   */
  async add(write: OutboxWrite, signal?: AbortSignal): Promise<OutboxAnswer> {
    await this.start();
    const first = this.callers.get(write.id);
    if (first) return first.promise; // a second tap while the first is still on its way
    if (this.entries.some(entry => entry.id === write.id)) { this.wake(); throw new OutboxWaiting(); }
    const scope = this.options.scope();
    if (this.stopped || !scope) throw Object.assign(new Error('Sign in to save this change.'), {code: 'signed_out'});
    const entry: OutboxEntry = {...write, scope, createdAt: this.wallNow(), attempts: 0};
    let resolve!: Caller['resolve'], reject!: Caller['reject'];
    const promise = new Promise<OutboxAnswer>((yes, no) => { resolve = yes; reject = no; });
    const caller = {promise, resolve, reject};
    this.callers.set(entry.id, caller);
    signal?.addEventListener('abort', () => { if (this.callers.get(entry.id) === caller) this.callers.delete(entry.id); reject(signal.reason); }, {once: true});
    this.options.store.put(entry).then(() => {
      this.entries.push(entry);
      this.changed();
      if (this.stopped) this.tellWaiting(); else this.wake();
    }, error => { this.callers.delete(entry.id); reject(storeFailed(error)); });
    return promise;
  }

  /** Try now: a new entry, a sign-in, or the network coming back. A wait the server asked for is kept. */
  wake(): void {
    if (!this.serverAskedToWait) this.retryAt = 0;
    void this.pump();
  }

  /** How many of this profile's edits are waiting to sync. A screen subscribes to hear the number change. */
  readonly waiting = (): number => {
    const scope = this.options.scope();
    return scope ? this.entries.filter(entry => sameOutboxScope(entry.scope, scope)).length : 0;
  };
  readonly subscribe = (listener: () => void): (() => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };

  dispose(): void {
    this.stopped = true;
    this.clock.clearTimeout(this.timer);
    this.inFlight?.abort();
    this.tellWaiting();
    this.listeners.clear();
  }

  private wallNow() { return (this.options.wallNow ?? Date.now)(); }
  private changed() { for (const listener of [...this.listeners]) { try { listener(); } catch { /* a screen never stops the queue */ } } }
  /** Whoever still waits for an answer hears that the edit is kept and will be sent later. */
  private tellWaiting() {
    for (const entry of this.entries) { const caller = this.callers.get(entry.id); this.callers.delete(entry.id); caller?.reject(new OutboxWaiting()); }
  }
  private retryIn(ms: number) { this.retryAt = this.clock.now() + ms; }

  /** Sends this profile's entries in the order they were made, one at a time, until none is left or all that are left have to wait. */
  private async pump(): Promise<void> {
    if (this.running || this.stopped) return;
    this.running = true;
    this.clock.clearTimeout(this.timer);
    try {
      while (!this.stopped) {
        const old = this.entries.find(entry => this.wallNow() - entry.createdAt > OUTBOX_MAX_AGE_MS && !this.callers.has(entry.id));
        if (old) { await this.finish(old); continue; }
        const scope = this.options.scope();
        const mine = scope ? this.entries.filter(candidate => sameOutboxScope(candidate.scope, scope)) : [];
        if (!mine.length) break; // idle: nothing to send for the profile signed in
        const now = this.clock.now(), held = new Set<string>();
        let entry: OutboxEntry | undefined, wait = this.retryAt - now;
        if (wait <= 0) {
          // The first entry that is not waiting after a 5xx, nor an edit to the same thing as an earlier one that is.
          wait = Infinity;
          for (const candidate of mine) {
            const target = targetOf(candidate), until = this.failed.get(candidate.id) ?? 0;
            if (held.has(target)) continue;
            if (until <= now) { entry = candidate; break; }
            held.add(target);
            wait = Math.min(wait, until - now);
          }
        }
        if (!entry) { this.timer = this.clock.setTimeout(() => { void this.pump(); }, wait); break; }
        const answer = await this.send(entry);
        if (this.stopped) break;
        if (answer && settles(answer.status)) { this.serverAskedToWait = false; await this.finish(entry, answer); continue; }
        const again = {...entry, attempts: entry.attempts + 1};
        this.serverAskedToWait = !!answer?.retryAfterMs;
        const delay = nextDelay(BACKOFF, again.attempts, {retryAfterMs: answer?.retryAfterMs}, this.options.random);
        // A 5xx is the server failing on this write, so only this entry waits. Anything else (no answer, a wait
        // the server asked for, an ended sign-in) would meet every entry, so they all wait.
        if (answer && answer.status >= 500 && !answer.retryAfterMs) this.failed.set(entry.id, this.clock.now() + delay);
        else this.retryIn(delay);
        this.entries = this.entries.map(each => each === entry ? again : each);
        await this.options.store.put(again);
      }
    } catch {
      // The device's storage failed. Nothing was lost: what is kept is tried again shortly.
      this.retryIn(STORE_RETRY_MS);
      this.timer = this.clock.setTimeout(() => { void this.pump(); }, STORE_RETRY_MS);
    } finally { this.running = false; }
    this.tellWaiting();
  }

  private async send(entry: OutboxEntry): Promise<OutboxAnswer | undefined> {
    const abort = this.inFlight = new AbortController();
    try { return await this.options.send(entry, abort.signal); } catch { return undefined; } finally { this.inFlight = undefined; }
  }

  /** An entry's end: saved, refused, or too old (no answer). It leaves the store first, so it is never sent or reported again. */
  private async finish(entry: OutboxEntry, answer?: OutboxAnswer): Promise<void> {
    await this.options.store.delete(entry.id);
    this.entries = this.entries.filter(each => each.id !== entry.id);
    this.failed.delete(entry.id);
    const caller = this.callers.get(entry.id);
    this.callers.delete(entry.id);
    if (caller && answer) caller.resolve(answer);
    else if (!answer || answer.status >= 400) { try { this.options.onDropped(entry, answer ? 'refused' : 'expired', answer); } catch { /* the shell's notice never stops the queue */ } }
    this.changed();
  }
}
