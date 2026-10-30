/** Original-start custody for a genuine preparing (202) resource, shared by web and
 * native. Each platform keeps its own request capture, wire observation, channel
 * receipts and authenticated stop; this registry owns only who may retire what.
 *
 * - A flight is reserved under its idempotency key before any caller getter runs.
 * - Every installation seen while a flight lives permanently denies its cleanup,
 *   even after forget. The history is bounded (256 ids) and fails closed.
 * - A flight binds at most one exact resource (id and generation). Its cleanup is
 *   spent at most once, rechecks currentness around every await, and a claimed
 *   stop refuses a later installation of the same id. */

export type PreparingResource = Readonly<{id: string; generation: number}>;
/** The original owner's bounded, authenticated stop of one resource. */
export type PreparingStop = (signal: AbortSignal, uninstalled: () => boolean, claimStop: () => boolean) => Promise<void>;

export type PreparingFlight = Readonly<{
  key: string;
  /** Live registration, flight not finished, registry and caller still current. */
  current(): boolean;
  /** Record every id installed now; later installs are recorded by remember(). */
  seed(): void;
  /** Bind this flight's one resource and return its cleanup. A second call with the
   * same id and generation returns the same cleanup; anything else returns undefined. */
  acquire(resource: PreparingResource, ownerCurrent: () => boolean, stop: PreparingStop): (() => Promise<void>) | undefined;
  bound(): PreparingResource | undefined;
  /** Still held by this registry: neither finished nor released. */
  held(): boolean;
  finish(): void;
}>;

export type PreparingRegistration = Readonly<{
  remember(id: string): boolean;
  forget(id: string): void;
  dispose(): void;
  reserve(key: string, continuation: () => boolean): PreparingFlight;
  /** This registration's live flight for a key, if any. */
  flight(key: string): PreparingFlight | undefined;
}>;

const valid = (read: () => boolean) => {try {return read() === true;} catch {return false;}};
const retired = () => Object.assign(new Error('Original playback start retired'), {name: 'AbortError'});
const HISTORY = 256;

type Registered = {ids: Set<string>; installed(id: string): boolean};
type Flight = {
  registration: Registered; seen: Set<string>; denied: boolean; stopping: boolean; cleaning: boolean; done: boolean;
  resource?: PreparingResource; cleanup?: () => Promise<void>; abort?: AbortController; view: PreparingFlight;
};

export class PreparingStartCustody {
  private readonly registrations = new Set<Registered>();
  private readonly flights = new Set<Flight>();
  private readonly requests = new Map<string, Flight>();
  private disposed = false;
  private readonly pendingListeners = new Set<() => void>();
  private publishedPending = false;
  /** Real flights (including authenticated cleanup) or installed IDs; empty
   * registrations carry no media. This observes custody without transferring it. */
  pending = (): boolean => this.current() && (this.flights.size > 0 || [...this.registrations].some(value => value.ids.size > 0));
  subscribePending = (listener: () => void): (() => void) => {
    if (this.disposed) return () => {};
    this.pendingListeners.add(listener);
    return () => {this.pendingListeners.delete(listener);};
  };
  private publishPending(): void {
    const pending = this.pending();
    if (pending === this.publishedPending) return;
    this.publishedPending = pending;
    for (const listener of [...this.pendingListeners]) {
      // Observation cannot interrupt reservation, claim or release of custody.
      try {listener();} catch { /* The custody transition is already committed. */ }
    }
  }
  /** `current` is the registry owner's own authority, when it has one. */
  private readonly owner: () => boolean;
  constructor(owner: () => boolean = () => true) {this.owner = owner;}
  private current = () => !this.disposed && valid(this.owner);

  attach(installed: (id: string) => boolean = () => false): PreparingRegistration {
    const registration: Registered = {ids: new Set(), installed};
    this.registrations.add(registration);
    let live = true;
    const remember = (id: string) => {
      if ([...this.flights].some(flight => flight.stopping && flight.resource?.id === id)) return false;
      for (const flight of this.flights) {
        if (flight.seen.size >= HISTORY) flight.denied = true;
        else flight.seen.add(id);
        if (flight.resource?.id === id) {flight.denied = true; flight.abort?.abort();}
      }
      if (live) registration.ids.add(id);
      this.publishPending();
      return true;
    };
    const reserve = (key: string, continuation: () => boolean): PreparingFlight => {
      if (!live || typeof key !== 'string' || !key || this.requests.has(key)) throw retired();
      const release = () => {this.flights.delete(flight); if (this.requests.get(key) === flight) this.requests.delete(key); this.publishPending();};
      const flight: Flight = {registration, seen: new Set(), denied: false, stopping: false, cleaning: false, done: false, view: undefined!};
      const current = () => live && !flight.done && this.current() && valid(continuation);
      const acquire = (resource: PreparingResource, ownerCurrent: () => boolean, stop: PreparingStop) => {
        if (flight.resource) return flight.resource.id === resource.id && flight.resource.generation === resource.generation ? flight.cleanup : undefined;
        if (flight.done || typeof resource.id !== 'string' || !resource.id || !Number.isSafeInteger(resource.generation) || resource.generation < 0) return undefined;
        const original = flight.resource = Object.freeze({id: resource.id, generation: resource.generation});
        let spent = false;
        const owned = () => !flight.denied && this.flights.has(flight) && flight.resource === original && !flight.seen.has(original.id);
        const uninstalled = () => owned() && this.current() && valid(ownerCurrent)
          && ![...this.flights].some(other => other !== flight && other.stopping && other.resource?.id === original.id)
          && [...this.registrations].every(value => valid(() => !value.installed(original.id)))
          && this.current() && valid(ownerCurrent) && owned();
        return flight.cleanup = async () => {
          if (spent) return; spent = true;
          if (!uninstalled()) return;
          flight.cleaning = true;
          const abort = flight.abort = new AbortController();
          try {
            if (!uninstalled()) return;
            // The original owner supplies its authenticated, bounded stop. No
            // installation, forget, navigation or successor publication.
            await stop(abort.signal, uninstalled, () => {
              if (flight.stopping || !uninstalled()) return false;
              flight.stopping = true; return true;
            });
          } finally {flight.cleaning = false; flight.abort = undefined; release();}
        };
      };
      flight.view = Object.freeze<PreparingFlight>({
        key, current, acquire,
        seed: () => {
          for (const registered of this.registrations) for (const id of registered.ids) {
            if (flight.seen.size >= HISTORY) flight.denied = true; else flight.seen.add(id);
          }
        },
        bound: () => flight.resource,
        held: () => !flight.done && this.flights.has(flight),
        finish: () => {flight.done = true; if (!flight.cleaning) release();},
      });
      this.flights.add(flight); this.requests.set(key, flight); this.publishPending();
      return flight.view;
    };
    return Object.freeze<PreparingRegistration>({
      remember,
      forget: (id: string) => {registration.ids.delete(id); this.publishPending();},
      dispose: () => {live = false; registration.ids.clear(); this.registrations.delete(registration); this.publishPending();},
      reserve,
      flight: (key: string) => {const flight = this.requests.get(key); return flight?.registration === registration ? flight.view : undefined;},
    });
  }

  dispose = () => {
    if (this.disposed) return;
    this.disposed = true;
    for (const flight of this.flights) {flight.denied = true; flight.abort?.abort();}
    this.flights.clear(); this.requests.clear(); this.registrations.clear();
    this.publishPending(); this.pendingListeners.clear();
  };
}
