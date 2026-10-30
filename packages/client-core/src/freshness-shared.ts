import {SHARED_RESULT_BYTES, sharedResultBytes} from './freshness-memory.ts';
import {positiveDeadline, serverDeadline} from './freshness-deadline.ts';

export type SharedReadKey = 'guide' | 'preferences:web';
const keys: readonly SharedReadKey[] = ['guide', 'preferences:web'];
class SharedReadSuperseded extends Error {
  readonly retryError?: unknown;
  readonly nextAllowedAt?: number;
  constructor(retryError?: unknown, nextAllowedAt?: number) { super('The shared read was superseded.'); this.retryError = retryError; this.nextAllowedAt = nextAllowedAt; }
}
/** Adapters must propagate this core-owned control outcome, preserving independent inputs. */
export function isSharedReadSuperseded(error: unknown): boolean { return error instanceof SharedReadSuperseded; }
const sharedReadSuperseded = (error?: unknown, nextAllowedAt?: number): Error => new SharedReadSuperseded(error, nextAllowedAt);
/** @internal A superseded confirmation can still carry a binding server wait. */
export function supersededRetryError(error: unknown): unknown { return error instanceof SharedReadSuperseded ? error.retryError : undefined; }
/** @internal Absolute response-time floor, never reinterpreted as a fresh relative wait. */
export function supersededNextAllowedAt(error: unknown): number { return error instanceof SharedReadSuperseded ? positiveDeadline(error.nextAllowedAt) : 0; }
export const observationError = (key: SharedReadKey, value: unknown): unknown => key === 'preferences:web' &&
  value !== null && typeof value === 'object' && (value as {kind?: unknown}).kind === 'unreadable'
  ? (value as {error?: unknown}).error : undefined;

type Flight = {
  version: number; controller: AbortController; members: Set<object>;
  promise: Promise<unknown>; complete: boolean; bytes: number;
};
export type SharedWitness = Readonly<Record<SharedReadKey, number>>;

/** Exactly two fixed slots. Planned gather borrowers count even when an earlier
 * callback resolves completely before the next timer callback gets its turn. */
export class FreshnessSharedReads {
  private versions: Record<SharedReadKey, number> = {guide: 0, 'preferences:web': 0};
  // Exactly two scalar input floors. Flights, cohort results and dependency
  // history may all disappear without forgetting a server's absolute deadline.
  private serverNotBefore: Record<SharedReadKey, number> = {guide: 0, 'preferences:web': 0};
  private planned = new Map<object, number>();
  private flights = new Map<SharedReadKey, Flight>();
  private now: () => number;
  private gatherMs: () => number;
  constructor(now: () => number, gatherMs: () => number) {this.now = now;this.gatherMs = gatherMs;}
  reserve(token: object, dueAt: number): void { this.planned.set(token, dueAt); }
  witness(): SharedWitness { return {...this.versions}; }
  notBefore(key: SharedReadKey): number { return this.serverNotBefore[key]; }
  private observeFailure(key: SharedReadKey, error: unknown): void {
    const deadline = isSharedReadSuperseded(error) ? supersededNextAllowedAt(error) : serverDeadline(error, this.now());
    this.serverNotBefore[key] = Math.max(this.serverNotBefore[key], deadline);
  }
  move(dependencies?: readonly string[]): void {
    if (!dependencies || dependencies.some(d => d === 'settings:server' || d === 'event:list.changed:liveSources' || d === 'event:list.changed:libraryChannels')) this.versions.guide++;
    if (!dependencies || dependencies.some(d => d === 'settings:server' || d === 'settings:preferences')) this.versions['preferences:web']++;
    // A completed old projection must not block newer network demand while its
    // former display borrower is still settling or awaiting an independent input.
    for (const [key, flight] of this.flights) if (flight.complete && flight.version !== this.versions[key]) this.flights.delete(key);
  }
  release(token: object): void {
    this.planned.delete(token);
    for (const [key, flight] of this.flights) {
      flight.members.delete(token);
      if (!flight.members.size) { flight.controller.abort(); this.flights.delete(key); }
    }
  }
  async borrow<T>(token: object, witness: SharedWitness, signal: AbortSignal, key: SharedReadKey, read: (signal: AbortSignal) => Promise<T>): Promise<T> {
    if (!keys.includes(key)) throw new Error('Unknown shared read key.');
    if (signal.aborted || witness[key] !== this.versions[key]) throw sharedReadSuperseded(undefined, this.notBefore(key));
    let flight = this.flights.get(key);
    // Already captured borrowers can still receive their identical original
    // error/observation. A later group cannot use another group's absent floor.
    const captured = flight?.version === witness[key] && flight.members.has(token);
    if (!captured && this.notBefore(key) > this.now()) throw sharedReadSuperseded(undefined, this.notBefore(key));
    if (flight && flight.version !== witness[key]) {
      // This borrower cannot use the older answer. Wait for network ownership to
      // release before owing one gathered follow-up, rather than spinning timers.
      const error = await this.released(flight, signal, key);
      throw sharedReadSuperseded(error, this.notBefore(key));
    }
    if (flight?.complete && !flight.members.has(token)) {
      // Completed answers belong only to the gathered cohort captured at dispatch.
      this.flights.delete(key); flight = undefined;
    }
    if (!flight) {
      const controller = new AbortController();
      const through = this.now() + Math.max(0, this.gatherMs()) * 1.25;
      const members = new Set<object>();
      for (const [member, dueAt] of this.planned) if (dueAt <= through) members.add(member);
      flight = {version: witness[key], controller, members, complete: false, bytes: 0, promise: Promise.resolve()};
      const owned = flight;
      this.flights.set(key, owned);
      owned.promise = Promise.resolve().then(() => {
        if (controller.signal.aborted) throw sharedReadSuperseded();
        return read(controller.signal);
      }).then(value => {
        this.observeFailure(key, observationError(key, value));
        owned.complete = true;
        owned.bytes = 96 + sharedResultBytes(value);
        this.trimResults();
        return value;
      }, error => {
        this.observeFailure(key, error);
        // Error identity is shared. Do not retain arbitrary error graphs/metadata.
        owned.complete = true; owned.bytes = 96 + sharedResultBytes(error);
        this.trimResults();
        throw error;
      });
    }
    flight.members.add(token);
    const owned = flight;
    let stop!: () => void;
    const cancelled = new Promise<never>((_, reject) => {
      stop = () => reject(sharedReadSuperseded());
      signal.addEventListener('abort', stop, {once: true});
    });
    try {
      const value = await Promise.race([owned.promise, cancelled]);
      if (signal.aborted || owned.controller.signal.aborted || witness[key] !== this.versions[key]) throw sharedReadSuperseded(observationError(key, value), this.notBefore(key));
      return value as T;
    } catch (error) {
      if (signal.aborted || owned.controller.signal.aborted || witness[key] !== this.versions[key]) throw sharedReadSuperseded(isSharedReadSuperseded(error) ? supersededRetryError(error) : error, this.notBefore(key));
      throw error;
    } finally { signal.removeEventListener('abort', stop); }
  }
  private released(flight: Flight, signal: AbortSignal, key: SharedReadKey): Promise<unknown> {
    return new Promise(resolve => {
      const done = (error?: unknown) => {
        signal.removeEventListener('abort', done);
        flight.controller.signal.removeEventListener('abort', done);
        resolve(error);
      };
      signal.addEventListener('abort', done, {once: true});
      flight.controller.signal.addEventListener('abort', done, {once: true});
      void flight.promise.then(value => done(observationError(key, value)), done);
      if (signal.aborted || flight.controller.signal.aborted) done();
    });
  }
  private trimResults(): void {
    let bytes = 0;
    for (const flight of this.flights.values()) if (flight.complete) bytes += flight.bytes;
    for (const [key, flight] of this.flights) {
      if (!flight.complete) continue;
      if (flight.bytes > SHARED_RESULT_BYTES || bytes > SHARED_RESULT_BYTES) {
        // Current borrowers still receive the promise; future borrowers refetch.
        bytes -= flight.bytes; this.flights.delete(key);
      }
    }
  }
  dispose(): void {
    for (const flight of this.flights.values()) flight.controller.abort();
    this.flights.clear(); this.planned.clear(); this.move();
  }
}
