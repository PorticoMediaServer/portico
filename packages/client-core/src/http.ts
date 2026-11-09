/** The selected server's HTTP client, its wire types and errors, and the Portico account client. */
import {clockOrigin, observeResponseClock, serverNow} from './server-clock.ts';
import {linkAbortSignals, throwIfAborted} from './abort-lifetime.ts';
import {resendableRefusal, retryWriterCapacity} from './writer-capacity.ts';
import {parseRetryAfter, retryAfterFrom} from './timer.ts';
import type {MusicTechnicalFacts} from './music-facts.ts';
import {routeBody} from "../../contracts/src/routes.ts";
import {ReadingClient, type ReadingScope} from './reading.ts';
import type {RouteResponse} from "../../contracts/src/route-paths.ts";
import {routePath as hostedRoutePath} from "../../contracts/src/hosted/routes.ts";
import {routePrefix, apiRoot} from "../../contracts/src/routes.ts";
import {routePath, encodedRoutePath} from "../../contracts/src/route-paths.ts";
import {parseMediaItem} from './media-item.ts';
import {parseConnectionFacts, type ConnectionFacts} from './server-administration.ts';
import {randomId} from './random-id.ts';
import {routeOrigin} from './route-identity.ts';
import {parseMoveCollisionDetails, type MoveCollisionDetails} from './account-move.ts';
import {sessionRoute, bindSessionRoute, type RouteConnection} from './route-connection.ts';
import {playableAudio, type AudioRenderV2} from './playback-v1/audio-render.ts';
import {readCertificateResponse, type CertificateAction} from './certificates.ts';
import type {PlayerFailureCode} from './client-profile.ts';
import {normalizeTransportClass, transportClassHeader, deviceClassHeader, deliveryContextHeaders, setDeliveryContext, type TransportClass} from './delivery.ts';
import {readConsoleJson, consoleUTF8Size} from './console-transport.ts';
import {readLyricsResponse} from "./lyrics-http.ts";
import {readBoundedJson} from './bounded-json.ts';
import {parseLocalSession} from './local-session.ts';
import {setupResultBody} from './setup-result.ts';
import {unreadableServerResponse} from './server-messages.ts';
import {parseLibraryList} from './library-list.ts';
import {installationHeaders, issuesCredentials, provesIdentity} from './installation.ts';
import {Outbox, sameOutboxScope, type OutboxAnswer, type OutboxEntry, type OutboxScope, type OutboxStore} from './outbox.ts';
import {isOutboxRoute, outboxKind, type OutboxKind} from './outbox-routes.ts';
import {castStopKey} from './cast/sender.ts';
import {readWriteAnswer, writeRoute, type ConfirmedWrite, type WriteAnswer} from './query/write-dependencies.ts';
/** Portable wire types and services. No renderer, native module or process globals. */
export type Viewer = {
  accountId: string;
  profileId: string;
  serverId: string;
  authority: string;
  role: string;
};
export type NativeServerIdentity = {publicKey:string;fingerprint:string};
export type LocalSession = {
  serverIdentity?: NativeServerIdentity;
  accessToken: string;
  expiresAt: string;
  viewer: Viewer;
  sessionFamilyId: string;
  tokenGeneration: string;
  authorizationHorizon: string;
  /** The device record this family is bound to (device-bound sessions). */
  deviceId?: string;
  /** The installation the family is bound to (C45: sent with every issued session; refresh names it). */
  installationId?: string;
  /** Rotating refresh credential (HTTPS only). Lives in protected storage; never in UI snapshots or the viewer record. */
  refreshToken?: string;
};
/** How a transport keeps its access token alive (`LocalSessionRefresher` implements it). */
export type AuthRecovery = Readonly<{
  /** A usable access token, refreshing first when `current` is about to expire. */
  token(current: string): Promise<string>;
  /** The server refused `rejected` (401): its replacement, or undefined when the sign-in has ended. */
  recover(rejected: string): Promise<string | undefined>;
 permissionDenied?():Promise<boolean>;
}>;
export type Library = { id: string; name: string; kind: string; defaultView?: import('./library-content.ts').ContentView; /** Pinned by this viewer: pinned libraries come first, in the viewer's order. */ pinned?: boolean };
export type MediaItem = {
  audioFacts?: MusicTechnicalFacts;
  id: string;
  libraryId: string;
  title: string;
  kind: string;
  year?: number;
  /** Seconds. Absent when not known: in particular the server could not represent it
   * (`durationUnrepresentable`). Never read an absent length as zero. */
  duration?: number;
  durationUnrepresentable?: true;
  overview?: string;
  posterUrl?: string;
  backdropUrl?: string;
  progressSeconds: number;
  available: boolean;
};
/** Whether a session's audio can go through the effects engine: a version 2 plan
 * the device decodes (`direct`/`converted`). */
export const renderable=(s?:{audio?:AudioRenderV2}):boolean=>playableAudio(s?.audio);
/** Caller lifetime for a local start, including Retry. It must describe the host/authority,
 * not the output generation this start replaces. Cancellation ends admission, not accepted playback. */
export type PlaybackLocalStartContext = Readonly<{signal?:AbortSignal;isCurrent?:()=>boolean;initialIntent?:"playing"|"paused"}>;

export type PlaybackSession = {
  preparedVersionId?: string;
  /** §18.1 version 2: the client decodes the original file (Plexamp's model); `mode` says how. */
  audio?: AudioRenderV2;
  /** 'v1': a Playback Protocol v1 session, whose authority is the session resource itself (its
   * lease is renewed by the timeline, spec §5–§6). A local (downloaded) session has none. */
  protocol?: 'v1';
  id: string;
  generation: number;
  streamUrl: string;
  mode: string;
  duration: number;
  resumeSeconds: number;
};
/** O-159: `apiVersion`, `apiMajors` and `capabilities` are absent on servers older than 1.0's
 * negotiation; `parseServerSignIn` fills them from `apiLevel`. */
export type SystemInfo = Omit<RouteResponse<'get_system'>, 'signIn'|'move'|'apiVersion'|'apiMajors'|'capabilities'> & {signIn: 'portico' | 'local';move:'to_portico'|'to_local'|null;
  apiVersion?: string; apiMajors?: readonly number[]; capabilities?: readonly string[]};
/** A closed numeric rclone version (three numeric parts plus an optional suffix); never prose. */
function errorProgramVersion(value:unknown):string|undefined{
  return typeof value==='string'&&/^\d{1,4}(?:\.\d{1,4}){2}(?:[-+][0-9A-Za-z.+-]{1,32})?$/.test(value)?value:undefined;
}

/** CD-06: `error.confirmRoots`, kept only as a short list of plain http(s) URLs. */
export function errorConfirmRoots(value: unknown): readonly string[] | undefined {
  if (!Array.isArray(value) || value.length === 0 || value.length > 16) return undefined;
  const out: string[] = [];
  for (const v of value) {
    if (typeof v !== 'string' || v.length > 2048 || /[\x00-\x1f\x7f\s]/.test(v)) return undefined;
    try { const u = new URL(v); if ((u.protocol !== 'http:' && u.protocol !== 'https:') || u.username || u.password) return undefined; } catch { return undefined; }
    out.push(v);
  }
  return Object.freeze(out);
}
/** A server-named request field (`name`, `rules[0].query.libraryIds`), or undefined for anything else. */
export function errorFieldPath(value: unknown): string | undefined {
  return typeof value === "string" && /^[A-Za-z][A-Za-z0-9_]*(?:\[\d{1,4}\]|\.[A-Za-z][A-Za-z0-9_]*){0,12}$/.test(value) && value.length <= 200 ? value : undefined;
}
/** RFC 9110 Retry-After as whole seconds from now (rounded up), or undefined; parsed by timer.ts. */
export function retryAfterSecondsFrom(value: string | null, now = Date.now()): number | undefined {
  const ms = parseRetryAfter(value, now);
  // A positive number too large for the helper stays an (enormous) floor, so callers never retry early.
  return ms !== undefined ? ms / 1000 : value && /^\s*\d*[1-9]\d*\s*$/.test(value) ? Number(value) : undefined;
}

/** The same whole-second retry floor for transports holding response headers. */
export function retryAfterSecondsFromHeaders(headers: Readonly<{get(name: string): string | null}>, now?: number): number | undefined {
  return retryAfterSecondsFrom(headers.get("Retry-After"), now);
}

/** A refusal to wait out and resend: the server said nothing ran. */
function writerRefusal(error: unknown): {retryAfterMs?: number} | undefined {
  if (!(error instanceof ApiError) || !resendableRefusal(error.code) || !error.retryable) return undefined;
  return {retryAfterMs: error.retryAfterSeconds === undefined ? undefined : error.retryAfterSeconds * 1000};
}

/** The HTTP status an API failure carries, whichever API raised it (the v1 playback client has its own error class). */
/**
 * A request the server accepted and then never answered. It is a failure the person can retry,
 * never a cancellation: the screen that asked stops waiting and says so.
 */
export class RequestTimeoutError extends Error {
  readonly code = 'request_timeout';
  readonly retryable = true;
  constructor() { super('The server did not answer in time.'); this.name = 'TimeoutError'; }
}
/**
 * How long a request may go unanswered. A long poll waits as long as it asked the server to
 * hold it, plus a margin; a read gets half a minute; a change gets a minute, and an owner's
 * console change two (some do real work before they answer). Media, downloads and event streams
 * do not come through here and have no such limit.
 */
export function requestDeadlineMs(path: string, method: string): number {
  // This owner read advances capped server snapshots; selection size sets its
  // duration. The screen owns cancellation, rather than a whole-scan timer.
  if (method === 'POST' && path === routePath('post_metadata_bulk_preview')) return Infinity;
  // The server's claim watch lasts ten minutes and streams whitespace until its
  // final JSON answer. Give the body the whole approval window plus a margin.
  if (method === 'POST' && path.split('?')[0] === routePath('post_networking_claim_await_approval')) return 615_000;
  const held = /[?&]waitMs=(\d{1,7})(?:&|$)/.exec(path);
  if (held) return Math.min(Number(held[1]), 600_000) + 15_000;
  if (method === 'GET' || method === 'HEAD') return 30_000;
  return path.startsWith(routePrefix("change_connectivity_policy", 2)) ? 120_000 : 60_000;
}
/** Runs `work` under the request's deadline and the caller's own cancellation. A caller's cancel stays a cancel; the deadline is a timeout. */
async function withinDeadline<T>(ms: number, signal: AbortSignal | undefined, work: (signal: AbortSignal) => Promise<T>): Promise<T> {
  const controller = new AbortController();
  let timedOut = false;
  const cancel = () => controller.abort();
  if (signal?.aborted) cancel(); else signal?.addEventListener('abort', cancel, {once: true});
  const timer = ms === Infinity ? undefined : setTimeout(() => { timedOut = true; controller.abort(); }, ms);
  try {
    return await work(controller.signal);
  } catch (error) {
    if (timedOut && !signal?.aborted) throw new RequestTimeoutError();
    throw error;
  } finally {
    if (timer !== undefined) clearTimeout(timer);
    signal?.removeEventListener('abort', cancel);
  }
}
/** Detach only this request from shared credential work; rotation keeps its own lifetime. */
function waitForAuth<T>(work: Promise<T>, signal: AbortSignal | undefined): Promise<T> {
  if (!signal) return work;
  return new Promise<T>((resolve, reject) => {
    const detach = () => signal.removeEventListener('abort', abort);
    const abort = () => { detach(); try { throwIfAborted(signal); } catch (error) { reject(error); } };
    signal.addEventListener('abort', abort, {once: true});
    void work.then(value => { detach(); resolve(value); }, error => { detach(); reject(error); });
    if (signal.aborted) abort();
  });
}
export class ApiError extends Error {
  code: string;
  retryable: boolean;
  retryAfterSeconds?: number;
  /** Validated RFC3339 clock hint only; never retains the server error body. */
  serverTime?: string;
  status: number;
  /**
   * The request field the server rejected (`rules[0].query.kinds`), when it names one. A machine
   * field for highlighting, never shown as text; validated to a plain field path.
   */
  path?: string;
  /**
   * CD-06: the local-network roots a Live TV source preview needs the owner to confirm
   * (`source_lan_confirmation_required`), sent back in `confirmedLanRoots`. Validated plain URLs.
   */
  confirmRoots?: readonly string[];
  /** Only a checked move_collision document is retained; no generic response-body storage. */
  current?: MoveCollisionDetails;
  /** Bounded closed version facts for storage_executable_too_old; never raw server text. */
  version?: string;
  minimumVersion?: string;
  constructor(
    status: number,
    code: string,
    message: string,
    retryable = false,
    retryAfterSeconds?: number,
    serverTime?: string
  ) {
    super(message);
    this.name = "ApiError";
    this.code = code;
    this.retryable = retryable;
    this.retryAfterSeconds = retryAfterSeconds;
    this.status = status;
    if (typeof serverTime === "string" && /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?(?:Z|[+-]\d{2}:\d{2})$/.test(serverTime) && Number.isFinite(Date.parse(serverTime))) this.serverTime = serverTime;
  }
}
/** What a reported player failure did: `session` is the same session re-planned another way,
 * to be played in place; `escalates` false means there is no other way. */
export type PlayerFailureResult = Readonly<{escalates: boolean; session?: PlaybackSession}>;
export interface PlaybackApi {
  /** Explicit app-local journal/cleanup custody. A local maker never grants the server API
   * authority over its session; omission leaves this work with the app's local owner. */
  localPlayback?: Pick<PlaybackApi,'stopPlayback'|'progressPlayback'>;
  /** Optional: tells the server the engine could not play one session's presentation. The server
   * remembers that for this file on this device and re-plans the same session the next way up, at
   * `positionSeconds`. The session keeps its id, its queue and its place in it. */
  reportPlayerFailure?(sessionId: string, code: PlayerFailureCode, detail: string, positionSeconds: number): Promise<PlayerFailureResult | null>;
  createPlayback(
    itemId: string,
    requestId: string,
    signal?: AbortSignal
  ): Promise<PlaybackSession>;
  stopPlayback(id: string, signal?: AbortSignal): Promise<void>;
  progressPlayback(
    id: string,
    payload: {
      generation: number;
      sequence: number;
      positionSeconds: number;
      state: "playing" | "paused" | "ended";
    }
  ): Promise<void>;
  /** Optional: tells the service when the server ended a session it is playing (an administrator
   * terminated it, it moved to another device, its lease lapsed), with the reason. */
  onServerEnded?(listener: (sessionId: string, end: SessionEndInfo) => void): () => void;
  /** Optional: a marker the viewer skipped (spec §4.2), reported as evidence with the timeline. */
  markerSkipped?(sessionId: string, skipped: Readonly<{markerId: string; mode: 'automatic' | 'manual'; positionMs: number}>): Promise<void>;
}
/** Why the server ended a playback, and the administrator's message when there is one. */
export type SessionEndInfo = Readonly<{ reason: string; message?: string }>;
/** Match Go's canonical JSON escaping for signed-claim wrapper endpoints. */
/** How long one outbox attempt waits for an answer. Shorter than any screen's own wait, so the person hears
 * "saved on this device" from the outbox and never a timeout from the screen for a write the outbox still holds. */
const OUTBOX_ATTEMPT_MS = 10_000;
export function transportJSON(value:unknown):string {return JSON.stringify(value).replace(/[<>&\u2028\u2029]/g,c=>'\\u'+c.charCodeAt(0).toString(16).padStart(4,'0'));}
/** The selected server's HTTP API. Playback sessions are not started here: they are Playback
 * Protocol v1 sessions (`playback-v1/sessions.ts`), spoken through `requestRaw`. */
/** A request refused with profile_pin_reentry, and the access token it was sent with (absent for a
 * reply that wasn't sent from this API, such as a replayed durable change). */
export type ProfileGateRefusal = Readonly<{error: ApiError; accessToken: string | undefined}>;

export class HttpLocalApi {
  private outbox?: Outbox;
  private outboxSession?: LocalSession;
  getOutbox(): Outbox | undefined { return this.outbox; }
  /** The signed-in session, supplied by its owner and never inferred from a token. The outbox sends a
   * profile's edits only while that profile's token is the one in use. */
  setOutboxSession(session: LocalSession | undefined) {
    this.outboxSession = session ? {...session, viewer: {...session.viewer}} : undefined;
    this.setAccessToken(session?.accessToken ?? '');
    this.outbox?.wake();
  }
  /** The session the outbox sends for, while its token is the one in use. */
  getOutboxSession(): LocalSession | undefined {
    const session = this.outboxSession;
    return session && session.accessToken === this.token ? session : undefined;
  }
  private outboxScope = (): OutboxScope | undefined => {
    const session = this.getOutboxSession();
    if (!session) return undefined;
    const {serverId, authority, accountId, profileId} = session.viewer;
    return {serverId, authority, accountId, profileId};
  };
  private outboxDropped: OutboxKind[] = [];
  private outboxDroppedListener?: (kind: OutboxKind) => void;
  /** The shell's one notice for an edit that will never be saved. A drop that happens before the
   * shell is listening is kept for it. */
  onOutboxDropped(listener: (kind: OutboxKind) => void): () => void {
    this.outboxDroppedListener = listener;
    for (const kind of this.outboxDropped.splice(0)) listener(kind);
    return () => { if (this.outboxDroppedListener === listener) this.outboxDroppedListener = undefined; };
  }
  /** Keeps the personal edits made out of reach of the server (`outbox.ts`). */
  configureOutbox(store: OutboxStore): Outbox {
    this.outbox?.dispose();
    const outbox = this.outbox = new Outbox({store, scope: this.outboxScope, send: (entry, signal) => this.outboxSend(entry, signal),
      // A stop that was never delivered has nothing to tell anyone: the session ends by itself on the server.
      // An edit of a profile that is not the one signed in is not described to whoever is.
      onDropped: entry => {
        const scope = this.outboxScope(), kind = outboxKind(entry.path, entry.method);
        if (!scope || !sameOutboxScope(scope, entry.scope) || !kind || kind === 'stop') return;
        if (this.outboxDroppedListener) this.outboxDroppedListener(kind); else if (this.outboxDropped.length < 20) this.outboxDropped.push(kind);
      }});
    void outbox.start().catch(() => { /* a store that cannot be read is reported to the first write */ });
    return outbox;
  }
  private outboxSend(entry: OutboxEntry, signal: AbortSignal): Promise<OutboxAnswer> {
    return withinDeadline(OUTBOX_ATTEMPT_MS, signal, async bounded => {
      const mine = () => {
        const scope = this.outboxScope();
        if (!scope || !sameOutboxScope(scope, entry.scope)) throw new Error('The signed-in profile changed.');
      };
      mine();
      let token = this.token;
      if (this.auth) { token = await waitForAuth(this.auth.token(token), bounded); mine(); if (token !== this.token) throw new Error('The signed-in profile changed.'); }
      const headers = {...entry.headers, 'Idempotency-Key': entry.id};
      let response = await this.send(entry.path, entry.method, entry.body, bounded, token, headers);
      if (response.status === 401 && this.auth) {
        const next = await waitForAuth(this.auth.recover(token), bounded);
        mine();
        if (next && next !== token && next === this.token) {
          void response.body?.cancel().catch(() => {});
          response = await this.send(entry.path, entry.method, entry.body, bounded, next, headers);
        }
      }
      const answered: Record<string, string> = {};
      response.headers.forEach((value, key) => { answered[key] = value; });
      const text = await response.text();
      let body: unknown;
      try { body = text ? JSON.parse(text) : undefined; }
      catch {
        // A success that is not JSON is not this server's answer (a hotel sign-in page, a proxy's page): the write is kept.
        if (response.ok) throw new Error('The answer did not come from the server.');
      }
      const retryAfterMs = retryAfterFrom(response.headers);
      return {status: response.status, headers: answered, ...(body === undefined ? {} : {body}), ...(retryAfterMs ? {retryAfterMs} : {})};
    });
  }
  /** A key for a Cast stop, stamped with the server's time: the form the present receiver requires. */
  async createCastStopKey(): Promise<string> {
    const policy = await this.request<{serverTimeMs?: unknown}>(routePath('get_user_mutation_policy'));
    if (!Number.isSafeInteger(policy?.serverTimeMs)) throw new Error('The server did not report its time.');
    return castStopKey(policy.serverTimeMs as number);
  }
  /** A write the outbox carries. Resolves with the server's answer; when nothing answered it throws
   * `OutboxWaiting`, and the edit is kept and sent later. */
  private async outboxWrite(path: string, method: string, body: unknown, headers?: Readonly<Record<string, string>>, signal?: AbortSignal): Promise<OutboxAnswer> {
    const header = (name: string) => Object.entries(headers ?? {}).find(([key]) => key.toLowerCase() === name)?.[1];
    const fields = body && typeof body === 'object' && !Array.isArray(body) ? body as Record<string, unknown> : {};
    const named = ['operationId', 'idempotencyKey', 'requestId'].map(name => fields[name]).find(value => typeof value === 'string') as string | undefined;
    // The caller's own operation id is the entry's id, so a second tap is the same entry. The present
    // server reads that id from the body; a stop has none, so its session and generation name it.
    const stop = method === 'DELETE' && typeof fields.generation === 'number' ? `stop-${path.split('?')[0]!.split('/').pop()}-${fields.generation}` : undefined;
    const id = header('idempotency-key') ?? named ?? stop ?? randomId();
    const again = Object.fromEntries(Object.entries(headers ?? {}).filter(([key]) => ['content-type', 'if-match'].includes(key.toLowerCase())));
    const write = {id, method, path, ...(body === undefined ? {} : {body: JSON.parse(JSON.stringify(body))}), ...(Object.keys(again).length ? {headers: again} : {})};
    return this.outbox!.add(write, signal).catch(error => {
      // A device whose storage cannot keep the write still sends it once: the person hears the answer, or the failure, directly.
      const scope = this.outboxScope();
      if ((error as {code?: unknown} | null)?.code !== 'outbox_store_failed' || !scope) throw error;
      return this.outboxSend({...write, scope, createdAt: Date.now(), attempts: 0}, signal ?? new AbortController().signal);
    });
  }
  readonly baseUrl: string;
  private token: string;
  private certificateSessionEpoch = 0;
  private certificateSessionListeners = new Set<() => void>();
  readonly getCertificateSessionEpoch = () => this.certificateSessionEpoch;
  readonly subscribeCertificateSession = (listener: () => void) => {
    this.certificateSessionListeners.add(listener);
    return () => { this.certificateSessionListeners.delete(listener); };
  };
  // A request this session made was refused with profile_pin_reentry (the profile's PIN changed
  // elsewhere): whoever holds the session hears it, whichever screen asked. The refusal names the
  // access token its request went out with, so a reply to an earlier sign-in is never taken for
  // the current one's.
  private profileGateListeners = new Set<(refusal: ProfileGateRefusal) => void>();
  private sentWith = new WeakMap<Response, string>();
  readonly subscribeProfileGate = (listener: (refusal: ProfileGateRefusal) => void) => {
    this.profileGateListeners.add(listener);
    return () => { this.profileGateListeners.delete(listener); };
  };
  // PR-1: a request this session made was refused with profile_pin_manage (a remembered main
  // profile hasn't entered its PIN to manage the account in the server's window). The session's
  // holder asks for the PIN, whichever screen asked; same token rule as the profile gate.
  private manageGateListeners = new Set<(refusal: ProfileGateRefusal) => void>();
  readonly subscribeManageGate = (listener: (refusal: ProfileGateRefusal) => void) => {
    this.manageGateListeners.add(listener);
    return () => { this.manageGateListeners.delete(listener); };
  };
  private announceGate(error: ApiError, response: Response) {
    const gate = error.code === 'profile_pin_reentry' ? this.profileGateListeners : error.code === 'profile_pin_manage' ? this.manageGateListeners : undefined;
    if (!gate) return;
    const refusal: ProfileGateRefusal = Object.freeze({error, accessToken: this.sentWith.get(response)});
    for (const listener of [...gate]) try { listener(refusal); } catch { /* the caller still gets its error */ }
  }
  private fetcher: typeof fetch;
  private rawFetcher: typeof fetch;
  private routes?: RouteConnection;
  private routeListeners = new Set<() => void>();
  private routeUnsubscribe?: () => void;
  readonly subscribeRoute = (listener: () => void) => {
    this.routeListeners.add(listener); this.observeRoutes();
    return () => {
      this.routeListeners.delete(listener);
      if (!this.routeListeners.size) { this.routeUnsubscribe?.(); this.routeUnsubscribe = undefined; }
    };
  };
  private observeRoutes() {
    if (this.routes && this.routeListeners.size && !this.routeUnsubscribe)
      this.routeUnsubscribe = this.routes.subscribe(() => { for (const listener of this.routeListeners) listener(); });
  }
  readonly getRouteSnapshot = () => this.routes?.getSnapshot() ?? null;
  readonly routeFetch: typeof fetch = (input,init) => this.fetcher(input,init);
  getRouteConnection() { return this.routes; }
  useRoutes(routes: RouteConnection) {
    if (routes.logicalOrigin !== this.baseUrl) throw new Error('This route belongs to another logical server origin.');
    if (this.routes === routes) return;
    this.routeUnsubscribe?.(); this.routeUnsubscribe = undefined; this.routes = routes;
    this.fetcher = routes.fetch;
    this.observeRoutes();
    if (this.token) bindSessionRoute(this.token,routes);
  }
  recoverRoute() { return this.routes?.recover() ?? Promise.reject(new Error('No remembered server routes are available.')); }

  constructor(
    baseUrl: string,
    token = "",
    fetcher: typeof fetch = (input, init) => globalThis.fetch(input, init)
  ) {
    this.baseUrl = routeOrigin(baseUrl);
    this.token = token;
    // Native Window.fetch accepts a value call, not this API as its receiver.
    // An explicitly bound injected transport retains its own receiver.
    this.rawFetcher = (input, init) => fetcher(input, init);
    this.fetcher = (input,init) => this.rawFetcher(input,{...init,redirect:'error',credentials:'omit',referrerPolicy:'no-referrer'});
    const routes = sessionRoute(token); if (routes) this.useRoutes(routes);
  }
  /** What this device is connected over, for the server to resolve a network
   * class from. The client only declares a transport: the lane, its ceilings and
   * the quality rungs remain the server's decision, and an unset or unknown
   * value is a valid answer rather than a missing one. */
  private transportClass: TransportClass = 'unknown';
  private deviceClass = '';
  setTransportClass(value: string | null | undefined) {
    this.transportClass = normalizeTransportClass(value);
    // The playback control and queue transports read the shared copy.
    setDeliveryContext({ transportClass: this.transportClass });
  }
  getTransportClass(): TransportClass { return this.transportClass; }
  /** The surface this client renders on, which scopes its device-class
   * preferences. Unset means the server applies no device-class document. */
  setDeviceClass(value: 'web' | 'mobile' | 'tablet' | 'television' | '') { this.deviceClass = value; setDeliveryContext({ deviceClass: value }); }
  getDeviceClass() { return this.deviceClass; }
  /** The catalogue actually displayed by this app, after locale fallback. */
  setAppLanguage(value: string) { setDeliveryContext({appLanguage: value}); }
  private deliveryHeaders(): Record<string, string> {
    return {
      // This client reports playback with observations (claim, then the latest once a second).
      // The server refuses to start playback for a client that does not say so.
      'Portico-Playback': '2',
      ...deliveryContextHeaders(),
      ...(this.transportClass === 'unknown' ? {} : { [transportClassHeader]: this.transportClass }),
      ...(this.deviceClass ? { [deviceClassHeader]: this.deviceClass } : {}),
    };
  }
  setAccessToken(token: string) {
    if (this.token === token) return;
    this.token = token;
    if (this.routes && token) bindSessionRoute(token,this.routes);
    this.certificateSessionEpoch++;
    for (const listener of this.certificateSessionListeners) listener();
  }
  /** Captures this exact selected-server authority; reader callbacks never
   * refresh, retry or substitute another viewer's token behind an open book. */
  reading(scope: ReadingScope): ReadingClient {
    const token = this.token, epoch = this.certificateSessionEpoch, fetcher = this.fetcher;
    const isCurrent = () => this.token === token && this.certificateSessionEpoch === epoch;
    return new ReadingClient({
      isCurrent,
      subscribe: retire => this.subscribeCertificateSession(retire),
      send: (path, init) => {
        if (!isCurrent() || !path.startsWith(apiRoot) || path.includes('#') || path.includes('\\')) return Promise.reject(new Error('cancelled'));
        const method = init.method ?? 'GET';
        if (this.outbox && isOutboxRoute(path, method)) {
          const sent: Record<string, string> = {};
          new Headers(init.headers).forEach((value, key) => { sent[key] = value; });
          return this.outboxWrite(path, method, typeof init.body === 'string' ? JSON.parse(init.body) : undefined, sent, init.signal ?? undefined)
            .then(answer => new Response(answer.body === undefined ? null : JSON.stringify(answer.body), {status: answer.status, headers: {...answer.headers, 'Content-Type': 'application/json'}}));
        }
        const headers = new Headers(init.headers);
        headers.delete('Authorization'); if (token) headers.set('Authorization', 'Bearer ' + token);
        return fetcher(this.baseUrl + path, {...init, headers, redirect: 'error', credentials: 'omit', referrerPolicy: 'no-referrer', cache: 'no-store'});
      },
    }, scope);
  }
  /** Fixed owner-certificate operations, selected-server credentials and bounded responses. */
  async requestCertificate<T>(action:CertificateAction,body:unknown,signal:AbortSignal):Promise<T>{
    const suffix={status:'',config:'/config',retry:'/retry'}[action];
    if(suffix===undefined)throw new Error('Invalid certificate operation.');
    const raw=action==='status'?undefined:JSON.stringify(body);
    if(raw!==undefined&&raw.length>2048)throw new Error('Certificate configuration is too large.');
    const token=this.token,epoch=this.certificateSessionEpoch;
    const lifetime=linkAbortSignals([signal],10000);
    const bounded=lifetime.signal;
    try {
    throwIfAborted(bounded);
    const response=await this.fetcher(this.baseUrl + routePath(action==='status'?'get_networking_certificate':action==='config'?'post_networking_certificate_config':'post_networking_certificate_retry'),{method:action==='status'?'GET':'POST',signal:bounded,redirect:'error',credentials:'omit',headers:{Accept:'application/json',...(raw===undefined?{}:{'Content-Type':'application/json'}),...(token?{Authorization:'Bearer '+token}:{})},...(raw===undefined?{}:{body:raw})});
    if(bounded.aborted||this.certificateSessionEpoch!==epoch){void response.body?.cancel().catch(()=>{});throw new Error('The server session changed.');}
    const value=await readCertificateResponse<T>(response,bounded);
    if(bounded.aborted||this.certificateSessionEpoch!==epoch)throw new Error('The server session changed.');
    return value;
    } finally {lifetime.dispose();}
  }
  /** GET-only bounded diagnostics transport; existing request behavior is unchanged. */
  async requestBounded<T>(path:string,maxBytes:number,signal:AbortSignal):Promise<T>{
    if(!Number.isSafeInteger(maxBytes)||maxBytes<1||maxBytes>1048576)throw new Error('Invalid response byte limit.');
    if(!path.startsWith(apiRoot)||path.includes('?')||path.includes('#'))throw new Error('Use a fixed selected-server API path.');
    const token=this.token,fetcher=this.fetcher;
    const lifetime=linkAbortSignals([signal],10000);
    const bounded=lifetime.signal;
    try {
    throwIfAborted(bounded);
    const response=await fetcher(this.baseUrl+path,{method:'GET',signal:bounded,headers:{Accept:'application/json',...(token?{Authorization:'Bearer '+token}:{})}});
    if(bounded.aborted||this.token!==token){void response.body?.cancel().catch(()=>{});throw new Error('Your server session changed. Reopen support.');}
    const value=await readBoundedJson<T>(response,maxBytes,bounded);
    if(bounded.aborted||this.token!==token)throw new Error('Your server session changed. Reopen support.');
    return value;
    } finally {lifetime.dispose();}
  }
  /** Bounded selected-server console transport, including paged reads and writes. */
  async requestConsole<T>(path:string, method:string, body:unknown, signal:AbortSignal):Promise<T>{
    if(!path.startsWith(apiRoot)||path.includes('#')||path.includes('\\')||!['GET','POST','PATCH','PUT'].includes(method))throw new Error('Invalid console request.');
    const payload=body===undefined?undefined:JSON.stringify(body);
    if(payload!==undefined&&consoleUTF8Size(payload)>65536)throw new Error('Console request is too large.');
    const token=this.token,c=new AbortController();const abort=()=>c.abort();signal.addEventListener('abort',abort,{once:true});
    if(signal.aborted)c.abort();const timer=setTimeout(abort,10000);
    try{
      const response=await this.fetcher(this.baseUrl+path,{method,signal:c.signal,redirect:'error',credentials:'omit',headers:{Accept:'application/json',...(token?{Authorization:'Bearer '+token}:{}),...(payload===undefined?{}:{'Content-Type':'application/json'})},...(payload===undefined?{}:{body:payload})});
      if(c.signal.aborted||this.token!==token){void response.body?.cancel().catch(()=>{});throw new Error('The selected server session changed.');}
      const value=await readConsoleJson<T>(response,c.signal);
      if(c.signal.aborted||this.token!==token)throw new Error('The selected server session changed.');return value;
    }finally{clearTimeout(timer);signal.removeEventListener('abort',abort);}
  }
  /** Lyric resources never inherit authority from their digest or a cached URL. */
  async requestLyrics<T>(path:string, method='GET', body?:unknown, signal?:AbortSignal):Promise<T> {
    if(!/^\/v1\/(?:items\/[A-Za-z0-9_-]+\/lyrics(?:\/[A-Za-z0-9_/-]+)?|lyrics\/provider)(?:\?[^#]*)?$/.test(path)) throw new Error('Invalid lyric API path.');
    const token=this.token, controller=new AbortController();
    const abort=()=>controller.abort(); signal?.addEventListener('abort',abort,{once:true}); if(signal?.aborted)abort();
    const timer=setTimeout(abort,22000);
    try {
      const response=await this.fetcher(this.baseUrl+path,{method,signal:controller.signal,headers:{Accept:'application/json',...(body===undefined?{}:{'Content-Type':'application/json'}),...(token?{Authorization:'Bearer '+token}:{})},...(body===undefined?{}:{body:transportJSON(body)})});
      const value=await readLyricsResponse(response,controller.signal);
      if(this.token!==token||controller.signal.aborted)throw new Error('The lyric request belongs to an old session.');
      return value as T;
    } finally {clearTimeout(timer);signal?.removeEventListener('abort',abort);}
  }
  private writeListeners = new Set<(write: ConfirmedWrite) => void>();
  /**
   * Hear every write the server confirmed (a 2xx answer to anything but a GET), however it was sent
   * (an ordinary request or the outbox) and whoever sent it, for the routes the
   * cache's table names (query/write-dependencies.ts). The viewer's cache subscribes, so a write
   * made anywhere reads again every page built from what it moved. A listener hears the route, its
   * parameters and the few fields the table reads from the answer; never the request body.
   */
  readonly onConfirmedWrite = (listener: (write: ConfirmedWrite) => void) => {
    this.writeListeners.add(listener);
    return () => { this.writeListeners.delete(listener); };
  };
  private announceWrite(path: string, method: string, response: Response) {
    const write = writeRoute(path, method);
    if (!write) return;
    // Listeners hear the route, its parameters and what the answer names: never the request body.
    const tell = (answer: WriteAnswer | undefined) => {
      const confirmed: ConfirmedWrite = Object.freeze({route: write.route, params: write.params, ...(answer ? {answer} : {})});
      for (const listener of [...this.writeListeners]) try { listener(confirmed); } catch { /* a listener's failure is never the writer's */ }
    };
    // The answer is read from a copy, so the writer reads its own untouched; an unreadable one still tells.
    let copy: Response | undefined;
    try { copy = write.readsAnswer && response.status !== 204 && typeof response.clone === 'function' ? response.clone() : undefined; } catch { copy = undefined; }
    // Told after the writer has its answer in hand, never in the middle of its request.
    if (!copy) { void Promise.resolve().then(() => tell(undefined)); return; }
    copy.json().then(value => { let answer: WriteAnswer | undefined; try { answer = readWriteAnswer(write.route, value); } catch { answer = undefined; } tell(answer); }, () => tell(undefined));
  }
  private auth?: AuthRecovery;
  /** Keep this transport's access token alive: refresh before expiry and once on a 401. */
  setAuthRecovery(auth: AuthRecovery | undefined) { this.auth = auth; }
  private async send(path: string, method: string, body: unknown, signal: AbortSignal | undefined, token: string, extra?: Readonly<Record<string, string>>, keepalive = false) {
    const ownerStart = method === 'POST' && (path === routePath('post_networking_claim_web_approval') || path === routePath('post_setup_approval'));
    const sentAt = Date.now();
    const response = await this.fetcher(this.baseUrl + path, {
      method,
      signal,
      ...(keepalive ? {keepalive: true} : {}),
      // Credential/profile proof exchanges must never follow an HTTP redirect.
      ...(ownerStart || /^\/v1\/(?:direct(?:\/|$)|auth\/|hosted\/(?:attach|profiles\/offline-select))/.test(path)?{redirect:'error' as const,credentials:'omit' as const}:{}),
      headers: {
        Accept: "application/json",
        ...(body === undefined ? {} : { "Content-Type": "application/json" }),
        ...(token ? { Authorization: `Bearer ${token}` } : {}),
        ...this.deliveryHeaders(),
        ...(ownerStart || issuesCredentials(path, method) ? installationHeaders() : {}),
        // Per-request protocol headers (If-Match, Idempotency-Key, merge-patch Content-Type); never Authorization.
        ...Object.fromEntries(Object.entries(extra ?? {}).filter(([k]) => k.toLowerCase() !== 'authorization')),
      },
      ...(body === undefined ? {} : { body: transportJSON(body) }),
    });
    if (token) this.sentWith.set(response, token);
    if (this.writeListeners.size && response.ok) this.announceWrite(path, method, response);
    // No client-clock requirements: every answer teaches this server's clock (server-clock.ts).
    observeResponseClock(clockOrigin(this.baseUrl) || clockOrigin(response.url), response, sentAt);
    return response;
  }
  /** This server's current time as best this device knows it: compare server-issued instants with this, never `Date.now()`. */
  serverNow(): number { return serverNow(clockOrigin(this.baseUrl)); }
  /** Send with a live token; on a 401 renew once and resend (the server did not act on it). */
  private async exchange(path: string, method: string, body: unknown, signal: AbortSignal | undefined, extra?: Readonly<Record<string, string>>, singleAttempt = false): Promise<Response> {
    if (!path.startsWith(apiRoot))
      throw new Error("API path must remain on the selected server.");
    // Sign-in and refresh never refresh or retry themselves.
    const exchange = provesIdentity(path, method);
    let token = this.token;
    throwIfAborted(signal);
    if (this.auth && token && !exchange) { try { token = await waitForAuth(this.auth.token(token), signal); } catch { throwIfAborted(signal); /* send what we have; a 401 recovers below */ } }
    throwIfAborted(signal);
    let response = await this.send(path, method, body, signal, token, extra);
    // A single attempt is never resent: its 401 starts the renewal, and the next one goes out with it.
    if (response.status === 401 && this.auth && token && !exchange && singleAttempt) { const auth = this.auth; try { void Promise.resolve(auth.recover(token)).catch(() => {}); } catch { /* the next request recovers */ } }
    if (response.status === 401 && this.auth && token && !exchange && !singleAttempt && !signal?.aborted) {
      let next: string | undefined;
      try { next = await waitForAuth(this.auth.recover(token), signal); } catch { throwIfAborted(signal); next = undefined; }
      throwIfAborted(signal);
      if (next && next !== token) { void response.body?.cancel().catch(() => {}); response = await this.send(path, method, body, signal, next, extra); }
    }
    if(response.status===403&&this.auth?.permissionDenied&&!exchange&&!singleAttempt&&!path.startsWith(routePath("get_me"))&&!signal?.aborted){
      let changed=false;try{changed=await waitForAuth(this.auth.permissionDenied(),signal);}catch{throwIfAborted(signal);}
      throwIfAborted(signal);
      if(changed){void response.body?.cancel().catch(()=>{});response=await this.send(path,method,body,signal,this.token,extra);}
    }
    return response;
  }
  /**
   * A last word as the page goes away (a stop with the final position): sent at once with the
   * token in hand and `keepalive`, so the browser delivers it after the page has gone. Nothing is
   * awaited, renewed or retried; the server's own time limits cover a message that never arrives.
   */
  farewell(path: string, method: string, body?: unknown): void {
    if (!path.startsWith(apiRoot) || provesIdentity(path, method)) return;
    try { void this.send(path, method, body, undefined, this.token, undefined, true).then(r => { void r.body?.cancel().catch(() => {}); }, () => {}); } catch { /* the page is going away */ }
  }
  /** One artwork upload under the captured selected-server session. Never refresh/replay a lost write. */
  async requestMetadataUpload<T>(path: string, body: FormData, input?: AbortSignal): Promise<T> {
    if (!/^\/v1\/metadata\/(?:item|show|season|album|artist|book)\/[A-Za-z0-9_-]+\/art\/(?:poster|backdrop|cover|square|thumbnail|banner|logo|still|portrait)\/upload$/.test(path)) throw new Error('Invalid metadata upload path.');
    const token = this.token, epoch = this.certificateSessionEpoch;
    const current = () => {if (this.token !== token || this.certificateSessionEpoch !== epoch) throw new Error('The metadata upload belongs to an old session.');};
    return withinDeadline(30000, input, async signal => {
      current(); throwIfAborted(signal);
      const response = await this.fetcher(this.baseUrl + path, {method: 'POST', body, signal, redirect: 'error', credentials: 'omit', referrerPolicy: 'no-referrer',
        headers: {Accept: 'application/json', ...(token ? {Authorization: `Bearer ${token}`} : {}), ...this.deliveryHeaders()}});
      current(); throwIfAborted(signal);
      const value = await response.json();
      current(); throwIfAborted(signal);
      if (!response.ok) throw new ApiError(response.status, value?.error?.code ?? value?.code ?? 'request_failed', value?.error?.message ?? 'Artwork upload failed.', false);
      return value as T;
    });
  }
  /** One multipart subtitle save (IM04) under the captured selected-server session. Never refresh/replay a lost write. */
  async requestSubtitleUpload<T>(path: string, method: 'POST' | 'PUT', body: FormData, input?: AbortSignal): Promise<T> {
    if (!/^\/v1\/items\/[A-Za-z0-9_-]+\/subtitles(?:\/[A-Za-z0-9_-]+)?$/.test(path) || (method === 'PUT') !== /\/subtitles\/[A-Za-z0-9_-]+$/.test(path)) throw new Error('Invalid subtitle upload path.');
    const token = this.token, epoch = this.certificateSessionEpoch;
    const current = () => {if (this.token !== token || this.certificateSessionEpoch !== epoch) throw new Error('The subtitle upload belongs to an old session.');};
    return withinDeadline(110000, input, async signal => {
      current(); throwIfAborted(signal);
      const response = await this.fetcher(this.baseUrl + path, {method, body, signal, redirect: 'error', credentials: 'omit', referrerPolicy: 'no-referrer',
        headers: {Accept: 'application/json', ...(token ? {Authorization: `Bearer ${token}`} : {}), ...this.deliveryHeaders()}});
      current(); throwIfAborted(signal);
      const value = await response.json().catch(() => undefined);
      current(); throwIfAborted(signal);
      if (!response.ok) throw new ApiError(response.status, value?.error?.code ?? value?.code ?? 'request_failed', value?.error?.message ?? 'Subtitle upload failed.', value?.error?.retryable === true);
      return value as T;
    });
  }
  /**
   * Any status, with headers and the parsed body (playback v1's `V1Http` seam: 202, 412 `current`,
   * `ETag`, `Report-Every-Ms`). Rejects only when there is no response (offline, aborted).
   */
  async requestRaw(path: string, method: string, options: Readonly<{headers?: Readonly<Record<string, string>>; body?: unknown; signal?: AbortSignal; deadlineMs?: number; singleAttempt?: boolean}> = {}): Promise<{status: number; headers: Record<string, string>; body?: unknown}> {
    if (this.outbox && isOutboxRoute(path, method)) {
      const answer = await this.outboxWrite(path, method, options.body, options.headers, options.signal);
      return {status: answer.status, headers: {...answer.headers}, body: answer.body};
    }
    return withinDeadline(options.deadlineMs ?? requestDeadlineMs(path, method), options.signal, async signal => {
      const response = await this.exchange(path, method, options.body, signal, options.headers, options.singleAttempt);
      const headers: Record<string, string> = {};
      response.headers.forEach((value, key) => { headers[key] = value; });
      let body: unknown;
      if (response.status !== 204 && response.status !== 205 && response.status !== 304) {
        const text = await response.text();
        if (text) { try { body = JSON.parse(text); } catch { body = undefined; } }
      }
      // Playback v1 callers read their own errors; a management refusal still reaches the session's holder.
      const code = response.status === 403 ? (body as {error?: {code?: unknown}} | undefined)?.error?.code : undefined;
      if (code === 'profile_pin_manage') this.announceGate(new ApiError(403, code, 'Enter this profile\'s PIN to manage this account.', false), response);
      return {status: response.status, headers, body};
    });
  }
  async request<T>(
    path: string,
    method = "GET",
    body?: unknown,
    signal?: AbortSignal,
    /** `manageGate: false`: an optional read whose refusal its caller simply leaves out; a
     * profile_pin_manage answer to it doesn't ask for the PIN (#574's away-limit row). */
    options: Readonly<{manageGate?: false}> = {},
  ): Promise<T> {
    if (this.outbox && isOutboxRoute(path, method)) {
      const answer = await this.outboxWrite(path, method, body, undefined, signal);
      return this.readAnswered<T>(new Response(answer.body === undefined ? null : JSON.stringify(answer.body), {status: answer.status, headers: {...answer.headers}}));
    }
    // Ordinary requests time out; the paged owner preview belongs to caller cancellation (see `requestDeadlineMs`).
    // A change the writer refused before it began is sent again, not dropped (`writer-capacity.ts`).
    return retryWriterCapacity(
      () => withinDeadline(requestDeadlineMs(path, method), signal, bounded => this.answered<T>(path, method, body, bounded, options.manageGate !== false)),
      writerRefusal,
      signal,
    );
  }
  private async answered<T>(path: string, method: string, body: unknown, signal: AbortSignal, announce = true): Promise<T> {
    // A 401 means the server did not act on the request, so one retry with a renewed token is safe for any method.
    const response = await this.exchange(path, method, body, signal);
    return this.readAnswered<T>(response, signal, announce);
  }
  private async readAnswered<T>(response: Response, signal?: AbortSignal, announce = true): Promise<T> {
    if (!response.ok) {
      let data: any;
      try {
        data = await response.json();
      } catch {}
      throwIfAborted(signal);
      const error = new ApiError(
        response.status,
        data?.error?.code ?? data?.code ?? "request_failed",
        data?.error?.message ?? `Request failed (${response.status}).`,
        // Two envelopes: the older `retryable` boolean, and the registry's `retry` class, where
        // only "send the same request again" is a retry of this request.
        data?.error?.retryable !== undefined ? data.error.retryable === true : (data?.error?.retry !== undefined ? data.error.retry === "same_request" : response.status >= 500),
        retryAfterSecondsFrom(response.headers.get("Retry-After")),
        data?.error?.serverTime
      );
      const path = errorFieldPath(data?.error?.path);
      if (path) error.path = path;
      const roots = errorConfirmRoots(data?.error?.current?.confirmRoots);
      if (roots) error.confirmRoots = roots;
      if(error.status===409&&error.code==='move_collision')error.current=parseMoveCollisionDetails(data?.error?.current);
      if(error.code==='storage_executable_too_old'){
        const current=data?.error?.current, version=errorProgramVersion(current?.version), minimum=errorProgramVersion(current?.minimumVersion);
        if(version)error.version=version;
        if(minimum)error.minimumVersion=minimum;
      }
      if (announce || error.code !== 'profile_pin_manage') this.announceGate(error, response);
      throw error;
    }
    return response.status === 204 ? (undefined as T) : response.json();
  }
  connection(signal?:AbortSignal):Promise<ConnectionFacts> {
    return this.request<RouteResponse<"get_connection">>(routePath("get_connection"),"GET",undefined,signal).then(parseConnectionFacts);
  }
  system() {
    return this.request<SystemInfo>(routePath("get_system"));
  }
  setup(setupToken: string, username: string, password: string, name: string) {
    return this.request<RouteResponse<"setup_server">>(routePath("setup_server"), "POST", routeBody("setup_server", {
      setupToken,
      authMode: "local",
      username,
      password,
      name,
    })).then(value=>{const {tag,body}=setupResultBody(value);if(tag!=='session')throw new Error(unreadableServerResponse);return parseLocalSession(body,{authority:'local'});});
  }
  me(signal?: AbortSignal) {
    return this.request<{ viewer: Viewer }>(routePath("get_me"), "GET", undefined, signal);
  }
  logout() {
    return this.request<void>(routePath("delete_sessions_current"), "DELETE");
  }
  libraries() {
    return this.request<RouteResponse<"get_libraries">>(routePath("get_libraries")).then(parseLibraryList);
  }
  items(libraryId = "", cursor = "", limit = 40) {
    const q = new URLSearchParams({ limit: String(limit) });
    if (libraryId) q.set("libraryId", libraryId);
    if (cursor) q.set("cursor", cursor);
    return this.request<{ items: MediaItem[]; nextCursor?: string }>(
      routePath("get_items") + "?" + (q)
    ).then(page=>({...page,items:page.items.map(parseMediaItem)}));
  }
  item(id: string) {
    return this.request<RouteResponse<"get_items_id">>(encodedRoutePath("get_items_id", {"id": encodeURIComponent(id)})).then(parseMediaItem);
  }
  mediaUrl(path: string) {
    if (this.routes) return this.routes.mediaURL(path);
    const u = new URL(path, this.baseUrl);
    if (u.origin !== this.baseUrl)
      throw new Error("Cross-origin media capability rejected.");
    return u.href;
  }
}
export type HostedAccount = {
  id: string;
  username: string;
  displayName: string;
};
export type HostedSession = {
  refreshToken?: string;
  familyId: string;
  refreshExpiresAt: string;
  accessToken: string;
  expiresAt: string;
  account: HostedAccount;
};
/**
 * Whether Hosted has heard from a server recently (its presence lease). Absent
 * means unknown (an older Hosted); `lastSeenAt` is null when it never checked in.
 */
export type ServerPresence = Readonly<{ online: boolean; lastSeenAt: string | null }>;
export type HostedServer = {
  routes?: import('./route-identity.ts').SignedRoutes;
  id: string;
  name: string;
  baseUrl: string;
  publicKey: string;
  policyRevision: number;
  presence?: ServerPresence;
  /** Display facts from the authenticated directory, never server admission authority. */
  role?: 'owner' | 'admin' | 'member';
  ownedByMe?: boolean;
};
/** Reads a server's presence defensively: anything malformed is "unknown" (undefined). */
export function parseServerPresence(value: unknown): ServerPresence | undefined {
  if (!value || typeof value !== "object" || Array.isArray(value)) return undefined;
  const v = value as Record<string, unknown>;
  if (typeof v.online !== "boolean") return undefined;
  const last = typeof v.lastSeenAt === "string" && v.lastSeenAt.length <= 64 && Number.isFinite(Date.parse(v.lastSeenAt)) ? v.lastSeenAt : null;
  return Object.freeze({ online: v.online, lastSeenAt: last });
}
/**
 * X-13 (client): chooser ordering and the offline-dialog rule. Pure, no
 * formatting: apps render dates through the i18n catalogue (`i18n.date`).
 * Unknown presence (absent or malformed) sorts last and renders normally.
 */
/** Within the last hour a server still gets the "keep trying" copy; older than that it is stale. */
export const SERVER_RECENTLY_SEEN_MS = 3_600_000;
/** Chooser order: online first, then offline, then unknown. */
export function presenceRank(presence: ServerPresence | undefined): 0 | 1 | 2 {
  if (!presence) return 2;
  return presence.online ? 0 : 1;
}
function lastSeenMs(presence: ServerPresence | undefined): number | undefined {
  if (!presence || !presence.lastSeenAt) return undefined;
  const ms = Date.parse(presence.lastSeenAt);
  return Number.isFinite(ms) ? ms : undefined;
}
/** Online servers first, then the rest by `lastSeenAt` descending (unknown last). Stable. */
export function sortServersByPresence<T extends { presence?: ServerPresence }>(servers: readonly T[]): T[] {
  return [...servers].sort((a, b) => {
    const rank = presenceRank(a.presence) - presenceRank(b.presence);
    if (rank !== 0) return rank;
    const aSeen = lastSeenMs(a.presence), bSeen = lastSeenMs(b.presence);
    if (aSeen === undefined && bSeen === undefined) return 0;
    if (aSeen === undefined) return 1;
    if (bSeen === undefined) return -1;
    return bSeen - aSeen;
  });
}
/** Whether Hosted saw the server within `windowMs` (default one hour). Never-seen is never recent. */
export function wasRecentlySeen(presence: ServerPresence | undefined, nowMs: number = Date.now(), windowMs: number = SERVER_RECENTLY_SEEN_MS): boolean {
  const seen = lastSeenMs(presence);
  return seen !== undefined && seen <= nowMs && nowMs - seen < windowMs;
}
export type PresenceLabel = Readonly<
  | { kind: "unknown" }
  | { kind: "online" }
  | { kind: "lastSeen"; lastSeenAt: string; recent: boolean }
  | { kind: "neverSeen" }
>;
/** What a chooser row says about presence: "Last seen {date}" (`lastSeen`), "Never seen" (`neverSeen`), or nothing. */
export function presenceLabel(presence: ServerPresence | undefined, nowMs: number = Date.now()): PresenceLabel {
  if (!presence) return Object.freeze({ kind: "unknown" }) as PresenceLabel;
  if (presence.online) return Object.freeze({ kind: "online" }) as PresenceLabel;
  if (!presence.lastSeenAt || !Number.isFinite(Date.parse(presence.lastSeenAt))) return Object.freeze({ kind: "neverSeen" }) as PresenceLabel;
  return Object.freeze({ kind: "lastSeen", lastSeenAt: presence.lastSeenAt, recent: wasRecentlySeen(presence, nowMs) });
}
/**
 * A failed connect gets the specific "can't reach" dialog (not the generic
 * waiting notice) only for an offline, stale server: unknown presence renders
 * normally, and a server seen within the hour keeps the keep-trying copy.
 */
export function needsOfflineServerDialog(presence: ServerPresence | undefined, unreachable: boolean, nowMs: number = Date.now()): boolean {
  return unreachable && !!presence && !presence.online && !wasRecentlySeen(presence, nowMs);
}
export class HttpHostedApi {
  private http: HttpLocalApi;
  constructor(
    baseUrl: string,
    token = "",
    fetcher: typeof fetch = (input, init) => globalThis.fetch(input, init)
  ) {
    this.http = new HttpLocalApi(baseUrl, token, fetcher);
  }
  setAccessToken(token: string) {
    this.http.setAccessToken(token);
  }
  /** `GET /v1/servers`. `version` and `watch` feed `ServerListWatcher.adopt`. */
  async servers(signal?: AbortSignal): Promise<{ items: HostedServer[]; version?: string; watch?: string }> {
    const out = await this.http.request<{ items: HostedServer[]; version?: string; watch?: string }>(hostedRoutePath("servers_list"), "GET", undefined, signal);
    if (!out || !Array.isArray(out.items)) return out;
    return { ...out, items: out.items.map(server => {
      if (!server || typeof server !== "object") return server;
      const { presence: raw, role, ownedByMe, ...rest } = server as HostedServer & { presence?: unknown };
      const presence = parseServerPresence(raw);
      const ownership = ['owner', 'admin', 'member'].includes(String(role)) && typeof ownedByMe === 'boolean' && (role === 'owner') === ownedByMe
        ? {role, ownedByMe} : {};
      return {...rest, ...ownership, ...(presence ? {presence} : {})};
    }) };
  }
  /** The account's transport, for `porticoSignIn` and `acceptPorticoInvitation`. */
  request<T>(path: string, method = "GET", body?: unknown, signal?: AbortSignal): Promise<T> {
    return this.http.request<T>(path, method, body, signal);
  }
  logout() {
    return this.http.request<void>(routePath("delete_sessions_current"), "DELETE");
  }
}
