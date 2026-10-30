import {routePath} from "../../../contracts/src/route-paths.ts";
import type {ChannelApi} from '../channel-guide.ts';
import {ApiError} from '../index.ts';
import {guideReadCapacityExceeded} from '../server-messages.ts';

/** Guide reads are scoped to the authenticated API object, never to a URL or viewer name. */
export const GUIDE_READ_LIMIT = 64;
type Subscriber = {resolve: (value: unknown) => void; reject: (error: unknown) => void; removeAbort: () => void};
type Read = {
  at: number; controller: AbortController; subscribers: Set<Subscriber>;
  settled: boolean; invalidated: boolean; value?: unknown;
  remove: () => void;
};
let guideReads = new WeakMap<ChannelApi, Map<string, Read>>();
const activeReads = new Set<Read>();
function abortError(): Error { const error = new Error('Guide read cancelled.'); error.name = 'AbortError'; return error; }
function reason(signal: AbortSignal): unknown { return signal.reason ?? abortError(); }
function cancel(read: Read, error: unknown) {
  read.invalidated = true;
  read.remove();
  activeReads.delete(read);
  for (const subscriber of read.subscribers) { subscriber.removeAbort(); subscriber.reject(error); }
  read.subscribers.clear();
  read.controller.abort(error);
}

/** Invalidate cached credentials/results and cancel outstanding reads at a mutation or sign-in boundary. */
export function forgetGuideReads() {
  guideReads = new WeakMap();
  for (const read of [...activeReads]) cancel(read, abortError());
}

/**
 * Concurrent callers share one transport. Each caller owns its subscription: leaving the last
 * subscription aborts server work, while leaving one of several does not interrupt the others.
 * Completed results are removed immediately: the next refresh reads the server revision.
 */
export function dedupedGuideApi(api: ChannelApi): ChannelApi {
  return {
    request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> {
      if (signal?.aborted) return Promise.reject(reason(signal));
      if ((method ?? 'GET') !== 'GET' || body !== undefined || !path.startsWith(routePath("get_guide"))) return api.request<T>(path, method, body, signal);
      let reads = guideReads.get(api);
      if (!reads) guideReads.set(api, (reads = new Map()));

      let read = reads.get(path);
      if (read) { reads.delete(path); reads.set(path, read); }
      if (read?.settled) return Promise.resolve(read.value as T);
      if (!read) {
        // Never evict an active subscription: that would either cancel another viewport or let
        // a duplicate escape the cap. Excess distinct reads fail recoverably instead.
        if (reads.size >= GUIDE_READ_LIMIT) {
          for (const [key, candidate] of reads) if (candidate.settled) { reads.delete(key); break; }
          if (reads.size >= GUIDE_READ_LIMIT) return Promise.reject(new ApiError(503, 'server_busy', guideReadCapacityExceeded, true, 1));
        }
        const map = reads;
        read = {at:0, controller: new AbortController(), subscribers: new Set(), settled: false, invalidated: false,
          remove: () => { if (map.get(path) === read) map.delete(path); }};
        map.set(path, read);
        const flight = read;
        activeReads.add(flight);
        // Defer the transport so an immediately cancelled subscriber does not start server work.
        Promise.resolve().then(() => {
          if (flight.invalidated) throw reason(flight.controller.signal);
          return api.request<unknown>(path, method, body, flight.controller.signal);
        }).then(value => {
          if (flight.invalidated) return;
          flight.settled = true; flight.remove();
          activeReads.delete(flight);
          for (const subscriber of flight.subscribers) { subscriber.removeAbort(); subscriber.resolve(value); }
          flight.subscribers.clear();
        }, error => {
          if (flight.invalidated) return;
          flight.remove(); activeReads.delete(flight);
          for (const subscriber of flight.subscribers) { subscriber.removeAbort(); subscriber.reject(error); }
          flight.subscribers.clear();
        });
      }
      const flight = read;
      return new Promise<T>((resolve, reject) => {
        const abort = () => {
          flight.subscribers.delete(subscriber); subscriber.removeAbort(); reject(reason(signal!));
          if (!flight.settled && !flight.subscribers.size) cancel(flight, reason(signal!));
        };
        const subscriber: Subscriber = {resolve: value => resolve(value as T), reject,
          removeAbort: () => signal?.removeEventListener('abort', abort)};
        flight.subscribers.add(subscriber);
        signal?.addEventListener('abort', abort, {once: true});
      });
    },
  };
}
