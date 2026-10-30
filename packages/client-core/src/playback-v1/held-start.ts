import {call, PlaybackApiError, type V1Http, type V1Request, type V1Response} from './http.ts';
import {playbackFailurePermanent} from '../playback-messages.ts';
import {playbackControlsService, type AccountStreamLimit, type RemotePlaybackControls} from '../playback-controls.ts';
import {capacityChangedEventType, parseCapacityChangedEvent} from '../account-capacity.ts';
import type {HttpLocalApi, PageVisibility} from '../index.ts';
import type {EventsClient} from './events.ts';
import {routePath} from '../../../contracts/src/routes.ts';

/** A refused first start, held with its original request. Web and native draw it. */
export type HeldPlaybackStartFailure = Readonly<{
  error: unknown; code: string; scope: 'account' | 'server' | 'unknown'; retryable: boolean;
  streamLimit?: AccountStreamLimit;
  /** Exhaustion stops automation, not this original refusal or explicit action. */
  readonly automaticRecoveryExhausted: boolean;
  isCurrent(): boolean;
  retry(): Promise<void>;
  cancel(): void;
  remote(): Promise<Readonly<{items: readonly RemotePlaybackControls[]; nextCursor: string}>>;
  subscribeSessions(listener: (items: readonly RemotePlaybackControls[]) => void): () => void;
  stop(row: RemotePlaybackControls): Promise<void>;
}>;
const retired = () => Object.assign(new Error('Playback start retired'), {name: 'AbortError'});
/** Join only this original operation's signals, without AbortSignal.any (absent on
 * native). Every registration is released with its held HTTP continuation. */
function joinSignals(controller: AbortController, parents: readonly (AbortSignal | undefined)[]) {
  const abort = () => controller.abort();
  for (const parent of parents) {parent?.addEventListener('abort', abort, {once: true});if (parent?.aborted) abort();}
  return {signal: controller.signal, close() {for (const parent of parents) parent?.removeEventListener('abort', abort);}};
}
const starts: ReadonlySet<string> = new Set([routePath('start_playback_session'), routePath('create_queue')]);

/** A first start stays with its original core caller. The HTTP response is held at
 * this seam while its real failure is drawn; retry resumes that same request, body,
 * key and revision, rather than calling Play against a newer runtime or selection.
 * This sits outside capacityRetryHttp, whose directed conversion retries stay intact.
 *
 * Automatic recovery is bounded (five retries, twenty rereads, thirty seconds from
 * the first recovery). Exhaustion stops automation only: the original refusal stays
 * drawn and an explicit retry or a confirmed Stop still resumes the same request. */
export function startFailureHttp(input: Readonly<{
  http: V1Http; api: HttpLocalApi; events: Pick<EventsClient, 'on'> & Partial<Pick<EventsClient, 'onResync' | 'getStatus' | 'subscribeStatus' | 'resyncReads'>>; life: AbortSignal;
  /** Rechecks currentness when the platform's original caller changes. */
  subscribeCurrent?(listener: () => void): () => void;
  visibility?: PageVisibility;
  isCurrent(): boolean; isInitial(): boolean; captureCurrent(request: V1Request): (() => boolean) | undefined;
  now?: () => number; publish(failure: HeldPlaybackStartFailure | undefined): void;
  cancelStart(): void;
}>): V1Http {
  let generation = 0, active: HeldPlaybackStartFailure | undefined;
  const now = input.now ?? Date.now;
  return {async send(request) {
    if (request.method !== 'POST' || !starts.has(request.path) || request.path === routePath('create_queue') && (!request.body || typeof request.body !== 'object' || !(request.body as {startPlayback?: unknown}).startPlayback) || !input.isInitial()
      || !Object.entries(request.headers ?? {}).some(([key, value]) => key.toLowerCase() === 'idempotency-key' && !!value)) return input.http.send(request);
    const originalCurrent = input.captureCurrent(request);
    if (!originalCurrent) return input.http.send(request);
    const serial = ++generation, controller = new AbortController();
    const joined = joinSignals(controller, [input.life, request.signal]), signal = joined.signal;
    const captured = {...request, headers: {...request.headers}, body: request.body === undefined ? undefined : JSON.parse(JSON.stringify(request.body)), signal};
    const current = () => {
      if (signal.aborted) return false;
      let valid = false;
      try {valid = serial === generation && input.isCurrent() && originalCurrent();} catch { /* Retired original caller. */ }
      // End the held HTTP continuation as well as refusing its callbacks. Merely
      // hiding a stale card must not leave the original screen's Play pending.
      if (!valid) controller.abort();
      return valid;
    };
    const assert = () => {if (!current()) throw retired();};
    const uncurrent = input.subscribeCurrent?.(() => {current();}) ?? (() => {});
    let mine: HeldPlaybackStartFailure | undefined, automaticRetries = 0, firstRecovery: number | undefined, automaticPending = false;
    let retainedRefusal: V1Response | undefined;
    const consumed = new Set<string>();let refreshes = 0;
    const exhausted = () => automaticRetries >= 5 || refreshes >= 20 || firstRecovery !== undefined && now() - firstRecovery >= 30_000;
    const foreground = () => input.visibility?.hidden() === false;
    const waitForForeground = async () => {
      while (!foreground()) {
        assert();
        await new Promise<void>((resolve, reject) => {
          let off: (() => void) | undefined;
          const abort = () => {off?.();signal.removeEventListener('abort', abort);reject(retired());};
          off = input.visibility?.onVisible(() => {off?.();signal.removeEventListener('abort', abort);resolve();});
          signal.addEventListener('abort', abort, {once: true});
          if (signal.aborted || !off) abort();
        });
      }
      assert();
    };
    try {
      for (;;) {
        assert();
        let response: V1Response | undefined, error: PlaybackApiError, originalError: unknown;
        if (automaticPending) {
          await waitForForeground();
          // Keep the real refusal visible if the automatic continuation expired
          // while suspended. No new send, key or retry budget is manufactured.
          if (firstRecovery === undefined || now() - firstRecovery >= 30_000) response = retainedRefusal;
          automaticPending = false;
        }
        try {
          response ??= await input.http.send(captured);
          // A late success still belongs to the original core, which can clean up
          // its admitted session. Never turn it into another start or a failure.
          if (response.status < 400 || !current() || response.status === 401) return response;
          try {await call({send: async () => response!}, captured);} catch (failure) {
            if (!(failure instanceof PlaybackApiError)) throw failure;
            error = failure;originalError = failure;
          }
        } catch (failure) {
          if (!current() || signal.aborted) throw retired();
          if (failure instanceof Error && failure.name === 'AbortError') throw failure;
          // Only an actual transport failure is presented as a lost connection.
          // Identity/guard/decoder errors retain their original typed error path.
          if (!(failure instanceof TypeError || failure instanceof Error && failure.name === 'TimeoutError')) throw failure;
          originalError = failure;error = new PlaybackApiError(0, 'server_unreachable', 'same_request');
        }
        assert();
        if (response) retainedRefusal = response;
        if (response?.status === 403 && !['transcode_not_allowed', 'transcoding_disabled'].includes(error!.code)) return response;
        // A revision, identity or validation refusal must reach its existing owner;
        // retaining it would disguise a required refresh as a replayable start.
        if (response && (response.status === 412 || ['idempotency_key_reused', 'invalid_request', 'revision_mismatch', 'revision_required', 'factor_setup_required', 'access_revoked'].includes(error!.code))) return response;
        if (response && error!.retry !== 'same_request' && error!.code !== 'stream_limit_reached' && !playbackFailurePermanent(error!)) return response;
        const scope = error!.code !== 'stream_limit_reached' ? 'unknown'
          : error!.reason === 'owner_server_cap' ? 'server' : error!.reason === 'owner_account_cap' || error!.streamLimit ? 'account' : 'unknown';
        const refusedLimit = scope === 'account' ? error!.streamLimit : undefined;
        let limit = refusedLimit && refusedLimit.playing >= refusedLimit.limit ? refusedLimit : undefined;
        let wake: (() => void) | undefined, requested = false, busy = false, readSerial = 0;
        let dirty = false, scheduled = false, refreshing = false, visibilityEpoch = 0;
        let offered: readonly RemotePlaybackControls[] = Object.freeze([]);
        const sessionListeners = new Set<(items: readonly RemotePlaybackControls[]) => void>();
        const service = playbackControlsService(<T,>(path: string, method?: string, body?: unknown) => {
          const request = () => input.api.request<T>(path, method, body, signal);
          return input.events.resyncReads && (!method || method === 'GET') ? input.events.resyncReads.run(signal, request) : request();
        });
        const shown = () => current() && active === failure;
        const check = () => {if (!shown()) throw retired();};
        const publishSessions = (rows: readonly RemotePlaybackControls[], latestLimit: typeof limit) => {
          check();offered = Object.freeze(rows.filter(row => row.actions.includes('stop') && row.state !== 'ended'));
          // Once capacity is available this is a recovery transition, not an
          // "Already playing on 0 screens" failure. Keep numeric failure facts
          // only while the actual read still reports a full account.
          const blocked = latestLimit && latestLimit.playing >= latestLimit.limit ? latestLimit : undefined;
          const changed = limit?.limit !== blocked?.limit || limit?.playing !== blocked?.playing;
          limit = blocked;if (changed) input.publish(failure);
          for (const listener of [...sessionListeners]) listener(offered);
        };
        const completeRead = async () => {
          check();const rows: RemotePlaybackControls[] = [], cursors = new Set<string>();let cursor = '';
          let firstLimit: AccountStreamLimit | undefined, coherent = true;
          // An incomplete page can never prove a stream ended. Bound the read,
          // refuse a looping cursor, and never infer capacity from a partial list.
          for (let page = 0; page < 20; page++) {
            const value = await service.remote(cursor, 100);check();rows.push(...value.items);
            if (page === 0) firstLimit = value.streamLimit;
            else if (firstLimit?.limit !== value.streamLimit?.limit || firstLimit?.playing !== value.streamLimit?.playing) coherent = false;
            if (!value.nextCursor) {
              if (new Set(rows.map(row => row.sessionId)).size !== rows.length) throw retired();
              if (!coherent) {dirty = true;queueMicrotask(invalidate);throw retired();}
              // Only the final page's actual durable-slot fact accompanies a
              // complete read. The number of controls rows is never a slot count.
              return Object.freeze({items: Object.freeze(rows), streamLimit: value.streamLimit});
            }
            if (cursors.has(value.nextCursor)) throw retired();
            cursors.add(value.nextCursor);cursor = value.nextCursor;
          }
          throw retired();
        };
        const supportsRecovery = (read: Awaited<ReturnType<typeof completeRead>>, before: readonly RemotePlaybackControls[]) => read.streamLimit
          ? read.streamLimit.scope === 'account' && read.streamLimit.playing < read.streamLimit.limit
          : before.some(row => !read.items.some(value => value.sessionId === row.sessionId));
        const resume = (automatic: boolean) => {
          check();if (requested) throw retired();
          if (automatic) {
            if (!foreground()) throw retired();
            const at = now();firstRecovery ??= at;
            if (automaticRetries >= 5 || at - firstRecovery >= 30_000) {input.publish(failure);return;}
            automaticRetries++;
            automaticPending = true;
          }
          requested = true;active = undefined;input.publish(undefined);wake?.();
        };
        const confirmFreed = async (row: RemotePlaybackControls) => {
          if (!foreground()) {dirty = true;return;}
          const epoch = visibilityEpoch, remaining = await completeRead();check();
          if (!foreground() || epoch !== visibilityEpoch) {invalidate();return;}
          // A complete fresh account read after the original offered row's Stop
          // acknowledgement (or its newer ended event) is the capacity evidence.
          if (supportsRecovery(remaining, [row])) resume(false);
          else publishSessions(remaining.items, remaining.streamLimit);
        };
        // Hints, reconnect, foreground and the existing ended-event path all use
        // one read/admission gate. Never issue a POST from an event callback.
        const invalidate = () => {
          if (scope !== 'account' || !shown() || exhausted()) return;
          dirty = true;
          if (!foreground() || busy || refreshing || scheduled || refreshes >= 20) return;
          scheduled = true;
          queueMicrotask(() => {
            scheduled = false;
            if (!dirty || !shown() || !foreground() || busy || refreshing || refreshes >= 20) return;
            dirty = false;refreshing = true;refreshes++;const ticket = ++readSerial, epoch = visibilityEpoch, before = offered;
            void completeRead().then(rows => {
              check();if (ticket !== readSerial) return;
              if (!foreground() || epoch !== visibilityEpoch) {dirty = true;return;}
              if (supportsRecovery(rows, before)) resume(true);
              else publishSessions(rows.items, rows.streamLimit);
            }).catch(() => {}).finally(() => {refreshing = false;if (dirty) invalidate();});
          });
        };
        const consume = (id: string) => {
          if (!id || id.length > 128 || consumed.has(id) || consumed.size >= 128) return false;
          consumed.add(id);return true;
        };
        const failure: HeldPlaybackStartFailure = Object.freeze({error: originalError, code: error!.code, scope,
          retryable: !playbackFailurePermanent(error!), get streamLimit() {return limit;}, get automaticRecoveryExhausted() {return exhausted();}, isCurrent: shown,
          async retry() {check();if (scope === 'account' && !exhausted() || playbackFailurePermanent(error!)) throw retired();resume(false);},
          cancel() {if (shown()) {controller.abort();input.cancelStart();}},
          async remote() {
            check();if (scope !== 'account' || busy || refreshing) throw retired();const ticket = ++readSerial, epoch = visibilityEpoch;
            const rows = await completeRead();check();if (ticket !== readSerial || busy) throw retired();
            // One initial guarded reconciliation covers a release whose hint
            // preceded attachment/baseline. Subsequent attempts need a new real
            // trigger, rather than a failure -> mount -> POST recursion.
            if (!exhausted() && automaticRetries === 0 && rows.streamLimit && foreground() && epoch === visibilityEpoch && supportsRecovery(rows, [])) {
              resume(true);return Object.freeze({items: rows.items, nextCursor: ''});
            }
            publishSessions(rows.items, rows.streamLimit);
            return Object.freeze({items: offered, nextCursor: ''});
          },
          subscribeSessions(listener) {check();sessionListeners.add(listener);return () => {sessionListeners.delete(listener);};},
          async stop(row) {
            check();if (scope !== 'account' || busy || refreshing || !offered.includes(row) || !row.actions.includes('stop')) throw retired();
            busy = true;++readSerial;
            try {await service.stopAccount(row);check();await confirmFreed(row);} finally {busy = false;if (dirty) invalidate();}
          },
        });
        mine = active = failure;
        // A revisioned ended event is only a hint to reread. It is never permission
        // to retry a session not present in this failure's own scoped offered rows.
        const offEnded = scope === 'account' ? input.events.on('session.updated', event => {
          if (scope !== 'account' || !shown() || !foreground() || event.resource?.kind !== 'session') return;
          const row = offered.find(value => value.sessionId === event.resource?.id);
          const data = event.data as {state?: unknown} | undefined;
          if (!row || data?.state !== 'ended' || !/^\d{1,32}$/.test(event.revision ?? '') || BigInt(event.revision!) <= BigInt(row.revision)) return;
          if (consume(event.id)) invalidate();
        }) : () => {};
        const offCapacity = scope === 'account' ? input.events.on(capacityChangedEventType, event => {
          if (!shown() || !foreground()) return;
          const hint = parseCapacityChangedEvent(event);
          if (hint && consume(hint.id)) invalidate();
        }) : () => {};
        const offVisible = scope === 'account' ? input.visibility?.onVisible(() => {visibilityEpoch++;invalidate();}) : undefined;
        const offResync = scope === 'account' ? input.events.onResync?.(() => {if (foreground()) invalidate();}) : undefined;
        let connected = input.events.getStatus?.().connected;
        const offStatus = scope === 'account' && input.events.getStatus ? input.events.subscribeStatus?.(() => {
          const next = input.events.getStatus!().connected, reconnected = connected === false && next;
          connected = next;if (reconnected && foreground()) invalidate();
        }) : undefined;
        try {
          input.publish(failure);
          assert();
          await new Promise<void>((resolve, reject) => {
            const abort = () => {signal.removeEventListener('abort', abort);reject(retired());};
            wake = () => {signal.removeEventListener('abort', abort);resolve();};
            signal.addEventListener('abort', abort, {once: true});
            if (signal.aborted) abort();else if (requested) wake();
          });
        } finally {offEnded();offCapacity();offVisible?.();offResync?.();offStatus?.();sessionListeners.clear();}
      }
    } finally {joined.close();uncurrent();if (active === mine && mine) {active = undefined;input.publish(undefined);}}
  }};
}
