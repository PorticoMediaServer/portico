import {routePath} from '../../../../../packages/contracts/src/routes';
import {parseStartPreparation, type PreparingSessionView, type StartContext, type StartOptions, type StartTarget} from '../../../../../packages/client-core/src/playback-v1/sessions';
import type {V1Request, V1Response} from '../../../../../packages/client-core/src/playback-v1/http';
import {PreparingStartCustody, type PreparingFlight} from '../../../../../packages/client-core/src/playback-v1/preparing-custody';

const valid = (read: () => boolean) => {try {return read() === true;} catch {return false;}};
const retired = () => Object.assign(new Error('Original playback start retired'), {name: 'AbortError'});
const freeze = <T,>(value: T): T => {
  if (value && typeof value === 'object') {for (const child of Object.values(value)) freeze(child); Object.freeze(value);}
  return value;
};
export type NativePreparingOwner = Readonly<{
  current(): boolean;
  stop(session: PreparingSessionView, signal: AbortSignal, uninstalled: () => boolean, claim: () => boolean): Promise<void>;
}>;
export type OriginalNativeStart = Readonly<{body: StartTarget & StartOptions; key: string; context: StartContext; finish(): void}>;
export type NativeStartRegistration = Readonly<{
  remember(id: string): boolean; forget(id: string): void; dispose(): void;
  begin(target: StartTarget, options: StartOptions, key: string, signal: AbortSignal | undefined, current: () => boolean): OriginalNativeStart;
  observe(request: V1Request): void;
  retired(request: V1Request, response: V1Response): void;
}>;

/** Original-start receipts are independent of queue-selection receipts. This
 * owner is shared by the actual native paths borrowing one selected-server API.
 * The shared client-core registry owns flights, install history and the one-shot
 * cleanup; this adapter owns the captured body, offer matching and the native
 * wire observation that lets a late genuine 202 reach its original owner. */
export class NativePreparingStarts {
  private readonly custody = new PreparingStartCustody();
  private readonly dispatched = new WeakMap<V1Request, Readonly<{flight: PreparingFlight; capture(session: PreparingSessionView): (() => Promise<void>) | undefined}>>();
  private readonly bodies = new WeakMap<PreparingFlight, string>();
  private readonly captures = new WeakMap<PreparingFlight, (session: PreparingSessionView) => (() => Promise<void>) | undefined>();

  attach(captureOwner: () => NativePreparingOwner | undefined, installed: (id: string) => boolean): NativeStartRegistration {
    const registration = this.custody.attach(installed);
    return Object.freeze<NativeStartRegistration>({
      remember: registration.remember,
      forget: registration.forget,
      dispose: registration.dispose,
      begin: (target: StartTarget, options: StartOptions, key: string, signal: AbortSignal | undefined, current: () => boolean) => {
        // Reserve before any caller/target/options getter can reenter this path.
        const flight = registration.reserve(key, () => !signal?.aborted && valid(current));
        try {
          if (!flight.current()) throw retired();
          const owner = captureOwner();
          const body: StartTarget & StartOptions = freeze(JSON.parse(JSON.stringify({...target, ...options})));
          this.bodies.set(flight, JSON.stringify(body));
          flight.seed();
          if (!flight.current()) throw retired();
          const capture = (offered: PreparingSessionView) => {
            // No generic/latest-principal cleanup fallback. Older/unbound hosts
            // may complete ordinary 201s, but cannot continue a real-resource202
            // without this original owner proof (its finite server lease remains).
            if (!owner) throw retired();
            const resource = {id: offered.id, generation: offered.presentation.generation};
            if (flight.bound()) return flight.acquire(resource, owner.current, () => Promise.resolve());
            if (!flight.held() || !valid(owner.current) || 'transferId' in body || offered.role !== 'local' || offered.state === 'ended'
              || !Number.isSafeInteger(offered.presentation.generation) || offered.presentation.generation < 1
              || ('queue' in body ? offered.queue?.queueId !== body.queue.queueId || offered.queue?.entryId !== body.queue.entryId
                : 'itemId' in body ? offered.itemId !== body.itemId : true)
              || body.versionId !== undefined && offered.versionId !== body.versionId
              || body.discTitle !== undefined && (offered.discTitle?.id !== body.discTitle.id || offered.discTitle.revision !== body.discTitle.revision)) return undefined;
            return flight.acquire(resource, owner.current, (abort, uninstalled, claim) => owner.stop(offered, abort, uninstalled, claim));
          };
          this.captures.set(flight, capture);
          return Object.freeze({body, key, context: Object.freeze({signal, isCurrent: flight.current, capturePreparing: capture}), finish: flight.finish});
        } catch (error) {flight.finish(); throw error;}
      },
      observe: request => {
        if (request.method !== 'POST' || request.path !== routePath('start_playback_session')) return;
        const key = Object.entries(request.headers ?? {}).find(([name]) => name.toLowerCase() === 'idempotency-key')?.[1];
        const flight = key ? registration.flight(key) : undefined, capture = flight && this.captures.get(flight);
        if (!flight || !capture || !flight.current()) return;
        try {
          if (JSON.stringify(request.body) === this.bodies.get(flight) && flight.current() && flight.held()) this.dispatched.set(request, {flight, capture});
        } catch { /* No original-wire proof. */ }
      },
      retired: (request, response) => {
        const dispatched = this.dispatched.get(request);
        if (!dispatched || !dispatched.flight.held() || response.status !== 202) return;
        try {
          // Scope HTTP still rejects the caller. Only the strict actual real202
          // variant can hand its original receipt a resource; retry-only cannot.
          const offered = parseStartPreparation(response.body).session;
          if (offered) void dispatched.capture(offered)?.().catch(() => {});
        } catch { /* Malformed/changed/uncustodied replies never authorize DELETE. */ }
      },
    });
  }
}
