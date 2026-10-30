import type {ChannelStartReceipt, OriginalChannelStart} from '@core/playback-v1/channel-control.ts';
import type {PreparingSessionView, StartContext, StartOptions, StartTarget} from '@core/playback-v1/sessions.ts';
import {PreparingStartCustody, type PreparingStop} from '@core/playback-v1/preparing-custody.ts';

const retired = () => new DOMException('Original playback start retired', 'AbortError');
const valid = (predicate: () => boolean) => {try {return predicate() === true;} catch {return false;}};
const freeze = <T,>(value: T): T => {
  if (value && typeof value === 'object') {for (const child of Object.values(value)) freeze(child);Object.freeze(value);}
  return value;
};
export type OriginalWebStart = Readonly<{
  /** The exact JSON body is passed to the canonical SDK, which owns its wire validation. */
  body: StartTarget & StartOptions; key: string; context: StartContext; finish(): void;
}>;
export type ChannelStartResource = Readonly<{id: string; generation: number}>;
export type WebChannelStart = ChannelStartReceipt;
export type WebStartRegistration = Readonly<{
  remember(id: string): boolean; forget(id: string): void; dispose(): void;
  beginChannel(original: OriginalChannelStart, isCurrent: () => boolean): WebChannelStart;
  begin(target: StartTarget, options: StartOptions, key: string, signal: AbortSignal | undefined, isCurrent: () => boolean): OriginalWebStart;
}>;

/** Shell-private original-start custody, separate from queue-selection registrations.
 * The shared client-core registry owns flights, install history and the one-shot
 * cleanup; this adapter owns the captured body, offer matching and channel receipts.
 * Retiring a view/stack ends continuation, but does not erase its uninstalled 202
 * receipt while the original authenticated principal can still retire it. */
export class WebPreparingStarts {
  private readonly custody: PreparingStartCustody;
  constructor(private readonly owner: Readonly<{isCurrent(): boolean;
    stop(session: PreparingSessionView, signal: AbortSignal, uninstalled: () => boolean, claimStop: () => boolean): Promise<void>;
    stopChannel?(body: OriginalChannelStart['body'], session: ChannelStartResource, signal: AbortSignal, uninstalled: () => boolean, claimStop: () => boolean): Promise<void>}>) {
    this.custody = new PreparingStartCustody(() => owner.isCurrent());
  }
  attach(installed: (id: string) => boolean = () => false): WebStartRegistration {
    const registration = this.custody.attach(installed);
    const begin = (target: StartTarget, options: StartOptions, key: string, signal: AbortSignal | undefined, isCurrent: () => boolean,
      channel?: OriginalChannelStart['body']): OriginalWebStart & {retireChannel(session: ChannelStartResource): void | Promise<void>; claimChannel(session: ChannelStartResource): boolean} => {
        if (typeof key !== 'string' || !key) throw retired();
        const flight = registration.reserve(key, () => !signal?.aborted && valid(isCurrent));
        let captured = false;
        let receipt: (() => Promise<void>) | undefined;
        const finish = flight.finish;
        try {
          if (!flight.current()) throw retired();
          flight.seed();
          // Capture once, before dispatch. Do not later reconstruct a body from a
          // mutable source, queue, quality choice or a successor caller.
          const body: StartTarget & StartOptions = freeze(JSON.parse(JSON.stringify({...target, ...options})));
          const channelBody = channel && 'channelId' in body ? Object.freeze({channelId: body.channelId, channelGeneration: body.channelGeneration!, state: body.state!}) : undefined;
          const continuation = flight.current;
          if (!continuation()) throw retired();
          const acquire = (actual: ChannelStartResource, stop: PreparingStop): (() => Promise<void>) | undefined => {
            if (captured || !flight.held()) return undefined;
            captured = true;
            return receipt = flight.acquire(actual, () => true, stop);
          };
          const channelStop = (actual: ChannelStartResource) => acquire(actual, (signal, uninstalled, claimStop) =>
            this.owner.stopChannel ? this.owner.stopChannel(channelBody!, actual, signal, uninstalled, claimStop) : Promise.resolve());
          const capturePreparing: NonNullable<StartContext['capturePreparing']> = offered => {
            // Only the actual declared response to this original request can
            // acquire custody; channel facts never become a playable Session.
            if (captured || !flight.held()) return undefined;
            if ('transferId' in body || offered.role !== 'local' || offered.state === 'ended' ||
              ('queue' in body ? offered.queue?.queueId !== body.queue.queueId || offered.queue?.entryId !== body.queue.entryId
                : 'itemId' in body ? offered.itemId !== body.itemId : 'channelId' in body ? offered.channelId !== body.channelId : true) ||
              body.versionId !== undefined && offered.versionId !== body.versionId ||
              body.discTitle !== undefined && (offered.discTitle?.id !== body.discTitle.id || offered.discTitle.revision !== body.discTitle.revision)) {captured = true;return undefined;}
            if (channel && (!['live', 'channel'].includes(offered.kind) || offered.queue || offered.itemId || offered.versionId || offered.discTitle)) {captured = true;return undefined;}
            const actual = Object.freeze({id: offered.id, generation: offered.presentation.generation});
            return channel ? channelStop(actual) : acquire(actual, (signal, uninstalled, claimStop) => this.owner.stop(offered, signal, uninstalled, claimStop));
          };
          const retireChannel = (actual: ChannelStartResource) => {
            if (!channel || !flight.held()) return;
            // A final late response can spend only this request's first exact
            // resource. Never replace a real 202 receipt with another id/gen.
            if (captured) {
              const bound = flight.bound();
              return bound?.id === actual.id && bound.generation === actual.generation ? receipt?.() : undefined;
            }
            const original = Object.freeze({id: actual.id, generation: actual.generation});
            return channelStop(original)?.();
          };
          const claimChannel = (actual: ChannelStartResource) => {
            const bound = flight.bound();
            if (!channel || !continuation() || typeof actual.id !== 'string' || !actual.id ||
              !Number.isSafeInteger(actual.generation) || actual.generation < 0 ||
              captured && (!bound || bound.id !== actual.id || bound.generation !== actual.generation)) return false;
            // Canonical SDK819 calls this before a.id/accept/go-live. The same
            // registration synchronously excludes every older cleanup flight.
            return registration.remember(actual.id) && continuation();
          };
          return Object.freeze({body, key, context: Object.freeze({signal, isCurrent: continuation, capturePreparing}), finish, retireChannel, claimChannel});
        } catch (error) {finish();throw error;}
      };
    return Object.freeze({
      // An actual current install denies older cleanup, even after forget.
      // A stop already synchronously claimed for that id refuses installation.
      remember: registration.remember,
      forget: registration.forget,
      dispose: registration.dispose,
      begin,
      beginChannel: (original: OriginalChannelStart, isCurrent: () => boolean) => {
        const {body, key, signal, isCurrent: channelCurrent} = original;
        const current = () => valid(channelCurrent) && valid(isCurrent);
        const start = begin({channelId: body.channelId, channelGeneration: body.channelGeneration}, {state: body.state}, key, signal, current, body);
        return Object.freeze({isCurrent: start.context.isCurrent!, capturePreparing: start.context.capturePreparing!, claimInstall: start.claimChannel, retireUninstalled: start.retireChannel, finish: start.finish});
      },
    });
  }
  /** The canonical owner includes late authenticated cleanup after a view retires. */
  pending = (): boolean => this.custody.pending();
  subscribePending = (listener: () => void): (() => void) => this.custody.subscribePending(listener);
  dispose = () => {this.custody.dispose();};
}
