import {linkAbortSignals, throwIfAborted} from './abort-lifetime.ts';
import {resendableRefusal, retryWriterCapacity} from './writer-capacity.ts';
export {linkAbortSignals, throwIfAborted} from './abort-lifetime.ts';
export * from './read-preparation.ts';
export * from './server-links.ts';
export * from './file-info-windows.ts';
export * from './file-info-pages.ts';
export * from './file-info-session.ts';
export {DATABASE_BUSY, WRITER_CAPACITY, WRITER_RETRY_BUDGET_MS, resendableRefusal, retryWriterCapacity, writerRetryDelayMs} from './writer-capacity.ts';
export * from './reading-mark-all.ts';
export {contentEntrySubject} from './presentation/content-entry-subject.ts';
export * from './reading-reread.ts';
export * from './reading-indexed-search.ts';
export * from './reading-offline-refresh.ts';
import type {MusicTechnicalFacts} from './music-facts.ts';
import {routeBody, routes} from "../../contracts/src/routes.ts";
import {ReadingClient, type ReadingScope} from './reading.ts';
export * from './reading.ts';
import type {RouteResponse} from "../../contracts/src/route-paths.ts";
import {routePath as hostedRoutePath} from "../../contracts/src/hosted/routes.ts";
import {routePrefix, apiRoot} from "../../contracts/src/routes.ts";
import {routePath, encodedRoutePath} from "../../contracts/src/route-paths.ts";
import {parseMediaItem} from './media-item.ts';
import {parseConnectionDocument,type ConnectionDocument} from './server-administration.ts';
import {trackName} from './presentation/language.ts';
import {randomId} from './random-id.ts';
import {routeOrigin} from './route-identity.ts';
import {parseMoveCollisionDetails,type MoveCollisionDetails} from './account-move.ts';
import {sessionRoute,bindSessionRoute,type RouteConnection} from './route-connection.ts';
export * from './route-identity.ts';
export * from './clone-portable-dto.ts';
export * from './owner-recovery-contact.ts';
export * from './identity-restrictions.ts';
export * from './identity-devices.ts';
export * from './account-notifications.ts';
export * from './account-capacity.ts';
export * from './owner-claim-client.ts';
export * from './route-connection.ts';
export * from './known-servers.ts';
export * from './portico-servers.ts';
export * from './remote-source-choice.ts';
import type {PreparedChoice} from './prepared-media.ts';
import type {PlaybackChoices} from './playback-v1/choices.ts';
import {ListeningDeadlineClock,noListeningDeadlines,type NativeListeningStatus,type NativeListeningV1Control,type NativeListeningV1Event,type ListeningDeadlines,type ListeningPolicy} from './playback/native-listening.ts';
import {audioEffectsEnabled,defaultAudioEffects,parseAudioEffects,type AudioEffectsSnapshot,type AudioEffectsSettings,type PreparedAudio,type ListeningNoticeCode,type AudioProcessingBinding,type AudioProcessingApplied} from './audio-effects.ts';
import {playableAudio,type AudioRenderV2} from './playback-v1/audio-render.ts';
export * from './audio-effects.ts';
export * from './music-preferences.ts';
export * from './music-signal.ts';
export * from './music-facts.ts';
export * from './music-mixes.ts';
export * from './music-mixes-controller.ts';
import {sessionEndMessage} from './session-end.ts';
import {serviceText} from './presentation/service-text.ts';
import {TUNER_RESERVED, tunerHandedOver} from './live-handover.ts';
import {readCertificateResponse, type CertificateAction} from './certificates.ts';
import type {PlayerFailureCode} from './client-profile.ts';
import {normalizeTransportClass,transportClassHeader,deviceClassHeader,deliveryContextHeaders,setDeliveryContext,type TransportClass} from './delivery.ts';
export * from './certificates.ts';
export * from './delivery.ts';
export * from './client-profile.ts';
import {readConsoleJson,consoleUTF8Size} from './console-transport.ts';
import {channelSeconds,channelMessage,type ChannelProjection,type ChannelReference} from './playback/linear-protocol.ts';
import type {ChannelPlayback,ChannelView,ChannelStartOptions} from './playback-v1/channel-control.ts';
import type {StartPreparation} from './playback-v1/sessions.ts';
export type {ChannelStartOptions} from './playback-v1/channel-control.ts';
export type {ChannelReference,ChannelProjection} from './playback/linear-protocol.ts';
import type {QueueController} from './queue-controller.ts';
import {readLyricsResponse} from "./lyrics-http.ts";
import {readBoundedJson} from './bounded-json.ts';
import {parseLocalSession} from './local-session.ts';
import {setupResultBody} from './setup-result.ts';
import {unreadableServerResponse} from './server-messages.ts';
import {parseLibraryList} from './library-list.ts';
import {installationHeaders,issuesCredentials,provesIdentity} from './installation.ts';
import {AudioSelection,emptyAudio,type AudioPlan,type AudioBinding,type AudioSelectionSnapshot} from './audio-selection.ts';
export type {AudioPlan,AudioBinding,AudioSelectionSnapshot} from './audio-selection.ts';
import {UserMutationCoordinator, UserMutationError, userMutationFingerprint, userMutationSecretField, newUserMutationKey, isUserMutationKey, sameUserMutationScope, type DurableUserMutationStore, type UserMutationScope, type UserMutationEntry, type UserMutationReceipt, type UserMutationResponse} from './user-mutations.ts';
import {userMutationRoute, type userMutationRouteIds} from './user-mutation-routes.ts';
export * from './user-mutations.ts';
export * from './user-mutation-recovery.ts';
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
function localStartCurrent(context?:PlaybackLocalStartContext):boolean {
  try{return !context?.signal?.aborted&&context?.isCurrent?.()!==false;}catch{return false;}
}
function localStartCancelled():Error{return Object.assign(new Error('cancelled'),{name:'AbortError'});}

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
/** RFC 9110 Retry-After: delay-seconds or an HTTP-date. Returns whole seconds from now, or undefined. */
export function retryAfterSecondsFrom(value: string | null, now = Date.now()): number | undefined {
  if (!value) return undefined;
  const trimmed = value.trim();
  if (/^\d+$/.test(trimmed)) { const n = Number(trimmed); return n > 0 ? n : undefined; }
  const at = Date.parse(trimmed);
  if (!Number.isFinite(at)) return undefined;
  const seconds = Math.ceil((at - now) / 1000);
  return seconds > 0 ? seconds : undefined;
}

/** A refusal to wait out and resend: the server said nothing ran. */
function writerRefusal(error: unknown): {retryAfterMs?: number} | undefined {
  if (!(error instanceof ApiError) || !resendableRefusal(error.code) || !error.retryable) return undefined;
  return {retryAfterMs: error.retryAfterSeconds === undefined ? undefined : error.retryAfterSeconds * 1000};
}

/** The HTTP status an API failure carries, whichever API raised it (the v1 playback client has its own error class). */
function statusOf(error: unknown): number {
  const status = (error as { status?: unknown } | null)?.status;
  return typeof status === "number" ? status : 0;
}
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
  /** A retained protocol intent must be reviewed, never silently rebased. */
  mutationKey?: string;
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
export function transportJSON(value:unknown):string {return JSON.stringify(value).replace(/[<>&\u2028\u2029]/g,c=>'\\u'+c.charCodeAt(0).toString(16).padStart(4,'0'));}
/** The selected server's HTTP API. Playback sessions are not started here: they are Playback
 * Protocol v1 sessions (`playback-v1/sessions.ts`), spoken through `requestRaw`. */
export class HttpLocalApi {
  private ephemeralMutationBodies = new Map<string, unknown>();
  private mutationStore?: DurableUserMutationStore;
  private mutationSession?: LocalSession;
  private mutationCoordinator?: UserMutationCoordinator;
  getUserMutationCoordinator(): UserMutationCoordinator | undefined { return this.mutationCoordinator; }
  async awaitUserMutationsReady(): Promise<void> { await this.mutationReady; }
  private mutationReady?: Promise<void>;
  private mutationClockOffset = 0;
  private mutationClockRead = false;
  /** Full selected authority, supplied by the mounted session owner, never inferred from a token. */
  setUserMutationSession(session: LocalSession | undefined) {
    const previous=this.mutationSession;
    if (!session || previous && (session.sessionFamilyId!==previous.sessionFamilyId || session.deviceId!==previous.deviceId || (['serverId','authority','accountId','profileId'] as const).some(key=>session.viewer[key]!==previous.viewer[key]))) this.ephemeralMutationBodies.clear();
    this.mutationSession = session ? {...session, viewer: {...session.viewer}} : undefined;
    this.setAccessToken(session?.accessToken ?? '');
  }
  getUserMutationScope = (): UserMutationScope | undefined => {
    const session = this.mutationSession;
    if (!session || session.accessToken !== this.token) return undefined;
    return {serverId: session.viewer.serverId, origin: this.baseUrl, authority: session.viewer.authority,
      accountId: session.viewer.accountId, profileId: session.viewer.profileId, familyId: session.sessionFamilyId,
      ...(session.deviceId ? {deviceId: session.deviceId} : {})};
  };
  configureUserMutations(store: DurableUserMutationStore): UserMutationCoordinator {
    this.mutationCoordinator?.dispose();
    this.mutationStore = store;
    const coordinator = new UserMutationCoordinator({store, scope: this.getUserMutationScope, transport: {
      send: (entry, signal) => {
        let body = entry.body;
        if (entry.requiresReentry) {
          const raw = this.ephemeralMutationBodies.get(JSON.stringify([entry.scope,entry.intentId]));
          if (!raw) return Promise.reject(new UserMutationError('secret_reentry_required', entry.key));
          body = JSON.parse(JSON.stringify(raw));
          if (body && typeof body === 'object') for (const name of ['operationId','idempotencyKey','requestId']) if (name in (body as object)) (body as Record<string,unknown>)[name] = entry.key;
        }
        return this.mutationExchange(entry, entry.path, entry.method, body, signal, entry.headers);
      },
      lookup: (entry, signal) => this.mutationExchange(entry, encodedRoutePath('get_user_mutation', {key: entry.key}), 'GET', undefined, signal),
      recover: (entry, receipt, signal) => this.recoverUserMutation(entry, receipt, signal),
    }});
    this.mutationCoordinator = coordinator;
    this.mutationReady = coordinator.start();
    // A read error is returned again to the first writer; never send without durable custody.
    void this.mutationReady.catch(() => {});
    return coordinator;
  }
  async acknowledgeUserMutation(intentId: string): Promise<void> {
    const scope = this.getUserMutationScope(), coordinator = this.mutationCoordinator;
    const entry = coordinator?.snapshot().find(entry => scope && sameUserMutationScope(scope, entry.scope) && (entry.intentId === intentId || entry.key === intentId) && entry.state === 'confirmed');
    if (entry) {if(entry.nativeStopOwner){await coordinator!.consumeConfirmedStop(entry.key);}else await coordinator!.acknowledge(entry.key);this.ephemeralMutationBodies.delete(JSON.stringify([entry.scope,entry.intentId]));}
  }
  async consumeUserMutation(key: string): Promise<boolean> {
    return this.mutationCoordinator?.consumeConfirmedStop(key) ?? false;
  }
  async dismissReviewedUserMutation(intentId: string): Promise<void> {
    const scope=this.getUserMutationScope(),coordinator=this.mutationCoordinator;
    const entry=coordinator?.snapshot().find(entry=>scope&&sameUserMutationScope(scope,entry.scope)&&(entry.intentId===intentId||entry.key===intentId));
    if(entry){await coordinator!.dismissReviewed(entry.key);this.ephemeralMutationBodies.delete(JSON.stringify([entry.scope,entry.intentId]));}
  }
  async createUserMutationKey(): Promise<string> {
    const scope=this.getUserMutationScope();if(!scope)throw new UserMutationError('mutation_scope_changed');
    const response=await this.mutationExchange({key:'',scope} as UserMutationEntry,routePath('get_user_mutation_policy'),'GET',undefined,new AbortController().signal);
    const policy=response.body as {serverTimeMs?:unknown}|undefined;
    if(response.status!==200||!Number.isSafeInteger(policy?.serverTimeMs))throw new UserMutationError('mutation_not_queued');
    this.mutationClockOffset=Number(policy!.serverTimeMs)-Date.now();this.mutationClockRead=true;
    return newUserMutationKey(Number(policy!.serverTimeMs));
  }
  /** Called only after the person explicitly re-enters a pending secret draft. */
  async reenterUserMutationSecrets(key: string, rawBody: unknown, signal?: AbortSignal): Promise<UserMutationResponse> {
    const store=this.mutationStore, coordinator=this.mutationCoordinator, scope=this.getUserMutationScope();
    if(!store||!coordinator||!scope)throw new UserMutationError('mutation_scope_changed',key);
    const raw=JSON.parse(JSON.stringify(rawBody));
    for(let attempt=0;attempt<32;attempt++){
      const state=await store.read(), entry=state.entries.find(entry=>entry.key===key);
      if(!entry||!sameUserMutationScope(entry.scope,scope)||entry.state!=='review'||entry.errorCode!=='secret_reentry_required'||!entry.requiresReentry)throw new UserMutationError('mutation_needs_review',key);
      const original=JSON.parse(entry.fingerprint) as {operation:string;method:string;path:string;target:string;body?:unknown;headers?:Record<string,string>};
      const names:string[]=[];
      const strip=(value:unknown,path=''):unknown=>Array.isArray(value)?value.map((v,i)=>strip(v,path+'.'+i)):value&&typeof value==='object'?Object.fromEntries(Object.entries(value).flatMap(([name,v])=>{const field=path?path+'.'+name:name;if((userMutationSecretField(name)||original.operation==='set_live_source_configuration'&&field==='settings.remote.guideUrl')&&v!==undefined&&v!==null&&v!==''){names.push(field);return [];}return [[name,strip(v,field)]];})):value;
      const safe=strip(raw);
      if(userMutationFingerprint({...original,body:safe,requiresReentry:true,secretFields:names})!==entry.fingerprint)throw new UserMutationError('idempotency_conflict',key);
      const custodyKey=JSON.stringify([entry.scope,entry.intentId]),prior=this.ephemeralMutationBodies.get(custodyKey);
      if(prior&&JSON.stringify(prior)!==JSON.stringify(raw))throw new UserMutationError('idempotency_conflict',key);
      this.ephemeralMutationBodies.set(custodyKey,raw);
      const revision=state.revision;state.revision++;entry.state='pending';entry.dueMs=Date.now();delete entry.errorCode;delete entry.leaseOwner;delete entry.leaseUntilMs;
      if(await store.compareAndSwap(revision,state)) return coordinator.run({...original,intentId:entry.intentId,key,requiresReentry:true,secretFields:names},signal);
    }
    throw new UserMutationError('mutation_store_busy',key);
  }
  private async mutationExchange(entry: Readonly<UserMutationEntry>, path: string, method: string, body: unknown, signal: AbortSignal, headers?: Readonly<Record<string,string>>): Promise<UserMutationResponse> {
    return withinDeadline(requestDeadlineMs(path, method), signal, async bounded => {
    signal = bounded;
    const check = () => {const current = this.getUserMutationScope(); if (!current || !sameUserMutationScope(current, entry.scope)) throw new UserMutationError('mutation_scope_changed', entry.key);};
    check();
    let token = this.token;
    if (this.auth) {token = await waitForAuth(this.auth.token(token), signal); check(); if (token !== this.token) throw new UserMutationError('mutation_scope_changed', entry.key);}
    check();
    let response = await this.send(path, method, body, signal, token, headers);
    if (response.status === 401 && this.auth) {
      const next = await waitForAuth(this.auth.recover(token), signal); check();
      if (next && next !== token && next === this.token) {await response.body?.cancel(); response = await this.send(path, method, body, signal, next, headers);}
    }
    check();
    return this.mutationResponse(response, signal);
    });
  }
  private async mutationResponse(response: Response, signal: AbortSignal): Promise<UserMutationResponse> {
    const headers: Record<string,string> = {}; response.headers.forEach((v,k) => {headers[k] = v;});
    const retry = headers['retry-after'];
    if (retry && !/^[0-9]+$/.test(retry.trim())) {
      const retryAt = Date.parse(retry), serverNow = Date.now() + this.mutationClockOffset;
      if (Number.isFinite(retryAt)) headers['retry-after'] = String(Math.max(0, Math.ceil((retryAt-serverNow)/1000)));
    }
    let body: unknown;
    try {
    const maximum=1048576,declared=response.headers.get('Content-Length');
    if(declared!==null&&(!/^[0-9]+$/.test(declared)||Number(declared)>maximum)){void response.body?.cancel().catch(()=>{});throw new UserMutationError('mutation_response_invalid');}
    let text:string;
    if(response.body&&typeof response.body.getReader==='function'){
      const reader=response.body.getReader(),chunks:Uint8Array[]=[];let total=0;
      const cancel=()=>{void reader.cancel().catch(()=>{});};signal.addEventListener('abort',cancel,{once:true});
      try{throwIfAborted(signal);for(;;){const part=await reader.read();throwIfAborted(signal);if(part.done)break;total+=part.value.byteLength;if(total>maximum)throw new UserMutationError('mutation_response_invalid');chunks.push(part.value);}const bytes=new Uint8Array(total);let at=0;for(const chunk of chunks){bytes.set(chunk,at);at+=chunk.byteLength;}text=new TextDecoder('utf-8',{fatal:true}).decode(bytes);}
      finally{signal.removeEventListener('abort',cancel);void reader.cancel().catch(()=>{});try{reader.releaseLock();}catch{}}
    }else{text=await response.text();if(consoleUTF8Size(text)>maximum)throw new UserMutationError('mutation_response_invalid');}
    throwIfAborted(signal);
    if (text) {try {body = JSON.parse(text);} catch {throw new UserMutationError('mutation_response_invalid');}}
    } catch (error) {
      throwIfAborted(signal);
      if (!(response.status >= 200 && response.status < 300) && response.status < 500 && response.status !== 429) throw error;
      // An unreadable accepted response or proxy error page is no proof of refusal.
      // Keep status/headers so the coordinator looks up the same key with the retry floor.
    }
    return {status: response.status, headers, ...(body === undefined ? {} : {body})};
  }
  private async recoverUserMutation(entry: Readonly<UserMutationEntry>, receipt: UserMutationReceipt, signal: AbortSignal): Promise<UserMutationResponse> {
    if (entry.operation === 'stop_playback_session' || entry.operation === 'stop_account_playback') return {status: 204, headers: {}};
    // Each operation explicitly chooses a compatible current-state document or
    // the tiny receipt marker. A current read never recreates historical arrays.
    const adapters: Record<typeof userMutationRouteIds[number], string | null> = {
      put_admin_backup_settings: 'get_admin_backup_settings',
      change_console_settings: 'get_console_settings',
      put_admin_dvr_settings: 'get_admin_dvr_settings',
      put_admin_libraries_order: null,
      patch_admin_libraries_id: 'get_admin_libraries_id',
      put_admin_libraries_id_scan_policy: null,
      put_library_settings: 'get_library_settings',
      put_admin_libraries_id_shows_show_settings: null,
      put_admin_libraries_id_sonic_analysis: 'get_admin_libraries_id_sonic_analysis',
      set_live_source_configuration: 'get_live_source_configuration',
      set_live_settings: 'get_live_settings',
      patch_admin_logs_settings: 'get_admin_logs_settings',
      put_admin_maintenance_settings: 'get_admin_maintenance_settings',
      put_metadata_settings: 'get_metadata_settings',
      patch_admin_notifications_settings: 'get_admin_notifications_settings',
      post_channels_preferences: null,
      set_container_personal_state: null,
      undo_container_personal_state: null,
      put_download_settings: 'get_download_settings',
      put_items_personal_state_batch: null,
      put_items_id_personal_state: null,
      put_items_itemId_reading_favorite: 'get_items_itemId_reading_favorite',
      put_items_itemId_reading_mark: 'get_items_itemId_reading_mark',
      put_items_itemId_reading_my_list: 'get_items_itemId_reading_my_list',
      put_libraries_id_metadata_agent: 'get_libraries_id_metadata_agent',
      put_libraries_id_metadata_screen: 'get_libraries_id_metadata_screen',
      put_libraries_id_strm_analysis_policy: 'get_libraries_id_strm_analysis_policy',
      put_reading_group_my_list: 'get_reading_group_my_list',
      post_libraries_libraryId_reading_mark_all: null,
      post_library_channels_preferences: null,
      put_listening_preferences: 'get_listening_preferences',
      post_networking_remote_config: 'get_networking_remote',
      stop_playback_session: null,
      stop_account_playback: null,
      post_playlists: null,
      delete_playlists_id: null,
      patch_playlists_id: null,
      post_playlists_id_entries: null,
      delete_playlists_id_entries_entryId: null,
      reorder_playlist_window: null,
      delete_playlists_id_shares: null,
      put_playlists_id_shares: null,
      patch_preferences: 'get_preferences',
      delete_reading_saved_group_reference: null,
      put_saved_pins_order: null,
      put_saved_pins_kind_id: null,
      post_saved_resources: null,
      delete_saved_resources_id: null,
      patch_saved_resources_id: null,
      put_saved_resources_id_entries: null,
      reorder_saved_collection: null,
      delete_saved_resources_id_shares: null,
      put_saved_resources_id_shares: null,
      put_storage_source_advanced: 'get_storage_source_advanced',
      put_storage_source_program: null,
      put_storage_source_read_profile: 'get_storage_source_read_profile',
    };
    const operation = entry.operation as keyof typeof adapters;
    if (!(operation in adapters)) throw new UserMutationError('mutation_needs_review', entry.key, receipt);
    const marker = (): UserMutationResponse => ({status: 200, headers: {'portico-mutation-recovered':'true'}, body: {mutationReceipt: receipt}});
    const readerId = adapters[operation];
    if (readerId === null) return marker();
    const writer = userMutationRoute(entry.path, entry.method), reader = routes.find(route => route.id === readerId && route.method === 'GET');
    if (!writer || writer.id !== operation || !reader) throw new UserMutationError('mutation_needs_review', entry.key, receipt);
    const segments = entry.path.split('?')[0].split('/');
    const params = Object.fromEntries(writer.path.split('/').flatMap((part,i) => part.startsWith('{') ? [[part.slice(1,-1),segments[i]]] : []));
    let path = reader.path.replace(/\{([^}]+)\}/g, (_,name: string) => params[name]);
    if (operation === 'patch_preferences') {
      // Retained even after the confirmed command body is erased.
      if (!entry.recoveryDeviceClass) return marker();
      path += '?deviceClass=' + encodeURIComponent(entry.recoveryDeviceClass);
    }
    const current = await this.mutationExchange(entry, path, 'GET', undefined, signal);
    if (current.status >= 200 && current.status < 300) return {...current, headers: {...current.headers, 'portico-mutation-recovered':'true'}};
    throw new UserMutationError('mutation_readback_unavailable', entry.key, receipt);
  }

  private async coordinatedMutation(path: string, method: string, body: unknown, headers?: Readonly<Record<string,string>>, signal?: AbortSignal): Promise<UserMutationResponse> {
    const capturedScope = this.getUserMutationScope();
    body = body === undefined ? undefined : JSON.parse(JSON.stringify(body));
    headers = headers ? {...headers} : undefined;
    const route = userMutationRoute(path, method), coordinator = this.mutationCoordinator;
    if (!route || !coordinator) throw new UserMutationError('mutation_not_queued');
    await this.mutationReady;
    const scope = this.getUserMutationScope(); if (!scope || !capturedScope || !sameUserMutationScope(scope,capturedScope)) throw new UserMutationError('mutation_scope_changed');
    const obj = body && typeof body === 'object' && !Array.isArray(body) ? body as Record<string,unknown> : undefined;
    const headerKey = Object.entries(headers ?? {}).find(([name]) => name.toLowerCase() === 'idempotency-key')?.[1];
    const originalId = ['operationId','idempotencyKey','requestId'].map(name => obj?.[name]).find(value => typeof value === 'string') as string | undefined;
    const sessionId = route.id === 'stop_playback_session' ? decodeURIComponent(path.split('?')[0].split('/').pop()!) : route.id === 'stop_account_playback' ? decodeURIComponent(path.split('?')[0].split('/').pop()!.replace(/:stop$/, '')) : undefined;
    const generation = obj?.generation ?? obj?.expectedGeneration;
    if (sessionId && (!Number.isSafeInteger(generation) || Number(generation) < 1 || Number(generation) > 2147483647)) throw new UserMutationError('mutation_not_queued');
    const target = sessionId ? `stop:${sessionId}:${generation}` : path.split('?')[0];
    // Calibrate once before minting; a failed policy read keeps custody with the caller.
    if (!this.mutationClockRead) {
      const seed = {key:'', scope} as UserMutationEntry;
      const policy = await this.mutationExchange(seed, routePath('get_user_mutation_policy'), 'GET', undefined, new AbortController().signal);
      const value = policy.body as {serverTimeMs?: unknown; retiredBeforeMs?: unknown} | undefined;
      if (policy.status !== 200 || typeof value?.serverTimeMs !== 'number' || !Number.isSafeInteger(value.serverTimeMs)) throw new UserMutationError('mutation_not_queued');
      this.mutationClockOffset = value.serverTimeMs - Date.now(); this.mutationClockRead = true;
    }
    const suppliedKey = originalId && isUserMutationKey(originalId) ? originalId : headerKey && isUserMutationKey(headerKey) ? headerKey : undefined;
    const mutationKey = suppliedKey ?? newUserMutationKey(Date.now() + this.mutationClockOffset);
    const currentScope=this.getUserMutationScope();if(!currentScope||!sameUserMutationScope(currentScope,scope))throw new UserMutationError('mutation_scope_changed');
    const secretFields: string[] = [];
    const redact = (value: unknown, path = ''): unknown => Array.isArray(value) ? value.map((v,i) => redact(v, path+'.'+i)) : value && typeof value === 'object' ? Object.fromEntries(Object.entries(value).flatMap(([name,v]) => {
      const field = path ? path+'.'+name : name;
      if ((userMutationSecretField(name) || route.id === 'set_live_source_configuration' && field === 'settings.remote.guideUrl') && v !== undefined && v !== null && v !== '') {secretFields.push(field);return [];}
      return [[name,redact(v,field)]];
    })) : value;
    const safeBody = redact(body);
    const semanticHeaders = Object.fromEntries(Object.entries(headers ?? {}).filter(([name]) => ['content-type','if-match','if-unmodified-since'].includes(name.toLowerCase())));
    // Older declarative settings callers have no operation ID. Their exact
    // immutable draft is the retry identity until a consumer acknowledges it.
    const intentId = sessionId ? target : originalId ?? headerKey ?? 'request:'+userMutationFingerprint({operation:route.id,method,path,target,body:safeBody,headers:semanticHeaders,...(secretFields.length?{requiresReentry:true,secretFields}: {})});
    if (secretFields.length) {
      const custodyKey = JSON.stringify([scope,intentId]), prior = this.ephemeralMutationBodies.get(custodyKey);
      const raw = JSON.parse(JSON.stringify(body));
      if (prior && JSON.stringify(prior) !== JSON.stringify(raw)) throw new UserMutationError('idempotency_conflict');
      if (!prior) this.ephemeralMutationBodies.set(custodyKey, raw);
    }
    let response: UserMutationResponse;
    try {response = await coordinator.run({operation: route.id, method, path, target,
      intentId, key: mutationKey, body: safeBody, headers: semanticHeaders, ...(route.id === 'patch_preferences' && typeof obj?.deviceClass === 'string' ? {recoveryDeviceClass:obj.deviceClass} : {}), ...(secretFields.length ? {requiresReentry:true,secretFields} : {})}, signal);}
    catch (error) {if (error instanceof UserMutationError && error.response) response = error.response; else throw error;}
    // Existing domain parsers correlate their original caller intent ID. Only the wire and
    // durable command use the protocol ID; translate the actual acknowledgement at this seam.
    const key = coordinator.snapshot().find(entry => sameUserMutationScope(entry.scope, scope) && entry.intentId === intentId)?.key ?? Object.entries(response.headers).find(([name]) => name.toLowerCase() === 'portico-mutation-key')?.[1];
    const restore = (value: unknown): unknown => Array.isArray(value) ? value.map(restore) : value && typeof value === 'object' ? Object.fromEntries(Object.entries(value).map(([name,v]) => [name, ['operationId','idempotencyKey','requestId'].includes(name) && v === key && originalId ? originalId : restore(v)])) : value;
    return {...response, headers: {...response.headers, ...(key ? {'portico-mutation-key':key} : {})}, body: restore(response.body)};
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
      acknowledge: key => this.acknowledgeUserMutation(key),
      isCurrent,
      subscribe: retire => this.subscribeCertificateSession(retire),
      send: (path, init) => {
        if (!isCurrent() || !path.startsWith(apiRoot) || path.includes('#') || path.includes('\\')) return Promise.reject(new Error('cancelled'));
        const method = init.method ?? 'GET';
        if (this.mutationCoordinator && userMutationRoute(path, method)) {
          const body = typeof init.body === 'string' ? JSON.parse(init.body) : undefined;
          return this.coordinatedMutation(path, method, body, (() => {const headers: Record<string,string> = {}; new Headers(init.headers).forEach((v,k) => {headers[k] = v;}); return headers;})(), init.signal ?? undefined).then(response => new Response(response.body === undefined ? null : JSON.stringify(response.body), {status: response.status, headers: {'Content-Type':'application/json', ...response.headers}}));
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
    if (this.mutationCoordinator && userMutationRoute(path, method)) {
      const response = await withinDeadline(requestDeadlineMs(path,method), signal, bounded => this.coordinatedMutation(path, method, body, undefined, bounded));
      let value: T;
      try {value = await readConsoleJson<T>(new Response(response.body === undefined ? null : JSON.stringify(response.body), {status: response.status, headers: response.headers}), signal);}
      catch (error) {const key=response.headers['portico-mutation-key'];if(error instanceof Error&&key&&isUserMutationKey(key))Object.assign(error,{mutationKey:key});throw error;}
      // Settings have consumed the bounded JSON envelope here. Domain commands
      // with caller-owned IDs acknowledge only after their specific parser settles.
      const object = body && typeof body === 'object' ? body as Record<string,unknown> : undefined;
      if (!object || !['operationId','idempotencyKey','requestId'].some(name => typeof object[name] === 'string')) {
        const key = response.headers['portico-mutation-key']; if (key) await this.acknowledgeUserMutation(key);
      }
      return value;
    }
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
  private auth?: AuthRecovery;
  /** Keep this transport's access token alive: refresh before expiry and once on a 401. */
  setAuthRecovery(auth: AuthRecovery | undefined) { this.auth = auth; }
  private send(path: string, method: string, body: unknown, signal: AbortSignal | undefined, token: string, extra?: Readonly<Record<string, string>>, keepalive = false) {
    const ownerStart = method === 'POST' && path === routePath('post_networking_claim_web_approval');
    return this.fetcher(this.baseUrl + path, {
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
  }
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
    if (this.mutationCoordinator && userMutationRoute(path, method)) { void this.coordinatedMutation(path, method, body).then(async response => {if(response.status>=200&&response.status<300){const key=response.headers['portico-mutation-key'];if(key)await this.acknowledgeUserMutation(key);}}).catch(() => { /* The coordinator keeps unresolved Stop custody and publishes its state. */ }); return; }
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
    if (this.mutationCoordinator && userMutationRoute(path, method)) return withinDeadline(options.deadlineMs ?? requestDeadlineMs(path,method), options.signal, bounded => this.coordinatedMutation(path, method, options.body, options.headers, bounded));
    return withinDeadline(options.deadlineMs ?? requestDeadlineMs(path, method), options.signal, async signal => {
      const response = await this.exchange(path, method, options.body, signal, options.headers, options.singleAttempt);
      const headers: Record<string, string> = {};
      response.headers.forEach((value, key) => { headers[key] = value; });
      let body: unknown;
      if (response.status !== 204 && response.status !== 205 && response.status !== 304) {
        const text = await response.text();
        if (text) { try { body = JSON.parse(text); } catch { body = undefined; } }
      }
      return {status: response.status, headers, body};
    });
  }
  async request<T>(
    path: string,
    method = "GET",
    body?: unknown,
    signal?: AbortSignal
  ): Promise<T> {
    if (this.mutationCoordinator && userMutationRoute(path, method)) {
      const response = await withinDeadline(requestDeadlineMs(path,method), signal, bounded => this.coordinatedMutation(path, method, body, undefined, bounded));
      const value = await this.mutationValue<T>(response);
      // Settings have consumed the bounded JSON envelope here. Domain commands
      // with caller-owned IDs acknowledge only after their specific parser settles.
      const object = body && typeof body === 'object' ? body as Record<string,unknown> : undefined;
      if (!object || !['operationId','idempotencyKey','requestId'].some(name => typeof object[name] === 'string')) {
        const key = response.headers['portico-mutation-key']; if (key) await this.acknowledgeUserMutation(key);
      }
      return value;
    }
    // Ordinary requests time out; the paged owner preview belongs to caller cancellation (see `requestDeadlineMs`).
    // A change the writer refused before it began is sent again, not dropped (`writer-capacity.ts`).
    return retryWriterCapacity(
      () => withinDeadline(requestDeadlineMs(path, method), signal, bounded => this.answered<T>(path, method, body, bounded)),
      writerRefusal,
      signal,
    );
  }
  private async mutationValue<T>(response: UserMutationResponse): Promise<T> {
    try {return await this.readAnswered<T>(new Response(response.body === undefined ? null : JSON.stringify(response.body), {status: response.status, headers: response.headers}));}
    catch (error) {const key=response.headers['portico-mutation-key'];if(error instanceof Error&&key&&isUserMutationKey(key))Object.assign(error,{mutationKey:key});throw error;}
  }
  private async answered<T>(path: string, method: string, body: unknown, signal: AbortSignal): Promise<T> {
    // A 401 means the server did not act on the request, so one retry with a renewed token is safe for any method.
    const response = await this.exchange(path, method, body, signal);
    return this.readAnswered<T>(response, signal);
  }
  private async readAnswered<T>(response: Response, signal?: AbortSignal): Promise<T> {
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
      throw error;
    }
    return response.status === 204 ? (undefined as T) : response.json();
  }
  connection(signal?:AbortSignal):Promise<ConnectionDocument> {
    return this.request<RouteResponse<"get_connection">>(routePath("get_connection"),"GET",undefined,signal).then(parseConnectionDocument);
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
export type ViewerSnapshot = {
  generation: number;
  phase: "signedOut" | "ready";
  serverUrl?: string;
  session?: LocalSession;
  internetReachable: boolean | null;
  hostedReachable: boolean | null;
};
export class ViewerService {
  private snapshot: ViewerSnapshot = {
    generation: 0,
    phase: "signedOut",
    internetReachable: null,
    hostedReachable: null,
  };
  private listeners = new Set<() => void>();
  getSnapshot = () => this.snapshot;
  subscribe = (listener: () => void) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  };
  private publish(value: ViewerSnapshot) {
    this.snapshot = Object.freeze(value);
    for (const l of this.listeners) l();
  }
  /** `stored`: a sign-in this device saved earlier, shown before it is verified. Its access token
   * may have expired since, which the restore renews; it is never a reason to refuse it here. */
  select(serverUrl: string, session: LocalSession, options: {stored?: boolean} = {}) {
    const {refreshToken: _refresh, ...checked}=parseLocalSession(session,{},{stored:options.stored});
    this.publish({
      ...this.snapshot,
      generation: this.snapshot.generation + 1,
      phase: "ready",
      serverUrl,
      session:checked,
    });
  }
  /** The same viewer with a renewed access token: no new generation, so nothing viewer-scoped restarts. */
  rotate(session: LocalSession): boolean {
    const current = this.snapshot.session;
    if (!current || current.sessionFamilyId !== session.sessionFamilyId || (['accountId','profileId','serverId','authority'] as const).some(k => current.viewer[k] !== session.viewer[k])) return false;
    if (BigInt(session.tokenGeneration) < BigInt(current.tokenGeneration)) return false;
    const {refreshToken: _refresh, ...checked} = parseLocalSession(session);
    this.publish({ ...this.snapshot, session: checked });
    return true;
  }
  clear() {
    this.publish({
      generation: this.snapshot.generation + 1,
      phase: "signedOut",
      internetReachable: this.snapshot.internetReachable,
      hostedReachable: this.snapshot.hostedReachable,
    });
  }
  setConnectivity(
    internetReachable: boolean | null,
    hostedReachable: boolean | null
  ) {
    this.publish({ ...this.snapshot, internetReachable, hostedReachable });
  }
  isCurrent(generation: number) {
    return this.snapshot.generation === generation;
  }
}
export type ProgressCheckpoint = {
  generation: number;
  sequence: number;
  positionSeconds: number;
  state: "playing" | "paused" | "ended";
};
export type PlaybackJournalEntry = {
  id?: string;
  request?: { itemId: string; requestId: string };
  checkpoint?: ProgressCheckpoint;
};
/** Adapter must scope the protected journal to the selected server/account/profile. */
export interface PlaybackJournal {
  load(): Promise<PlaybackJournalEntry[]>;
  save(entries: PlaybackJournalEntry[]): Promise<void>;
}
export type PlaybackOptions = {
  /** Diagnostic codes and bounded server reasons never enter a UI snapshot. */
  audioDiagnostic?: (code:string,detail?:string)=>void;
  /** Live TV and Library Channels on v1 sessions (spec §18.6): V1ChannelControl. */
  channels?: ChannelPlayback;
  /** A new playback (`playbackRun`) is about to start: put back to the Settings whatever the
   * player changed for the last one that the server plans from (a player engine chosen in the
   * player), and resolve when the server has it. The start waits for it. */
  beforeFreshPlayback?: () => Promise<void>;
  journal?: PlaybackJournal;
  deferRecovery?: boolean;
  startupTimeoutMs?: number;
  operationTimeoutMs?: number;
  seekTimeoutMs?: number;
  audioTimeoutMs?: number;
  maxPendingCleanup?: number;
  queueRequired?: boolean;
  /** Development builds: log why a start timed out (see setStartupProbe). */
  diagnostics?: boolean;
  /**
   * Whether the page/app is hidden. Startup and seek deadlines pause while media loading
   * is suspended; platforms with onChange preserve the remaining foreground window.
   */
  pageVisibility?: PageVisibility;
  /** The wall clock (tests), for tuner deadlines and foreground media windows. */
  now?: () => number;
};
/** Page/app visibility. onChange reports both hide and show before suspended timers resume. */
export type PageVisibility = {hidden(): boolean; onVisible(listener: () => void): () => void; onChange?(listener: () => void): () => void};
/** One selectable audio track of the playing source. */
export type AudioTrackOffer = Readonly<{streamIndex:number;label:string;language?:string;channels?:number;codec:string}>;
export type AudioTracksSnapshot = Readonly<{tracks:readonly AudioTrackOffer[];selected:number|null;pending?:number}>;
/** A readable name for a track the file did not name. */
export function audioTrackLabel(t:{title?:string;language?:string;channels?:number;codec:string},ordinal:number):string{
  const layout=t.channels===undefined||t.channels<1?'':t.channels===1?'Mono':t.channels===2?'Stereo':t.channels===6?'5.1':t.channels===8?'7.1':t.channels+' ch';
  const codec=({aac:'AAC',ac3:'Dolby Digital',eac3:'Dolby Digital Plus',truehd:'Dolby TrueHD',dts:'DTS',flac:'FLAC',opus:'Opus',vorbis:'Vorbis',mp3:'MP3',alac:'ALAC',pcm:'PCM'} as Record<string,string>)[t.codec]??t.codec.toUpperCase();
  // The language leads; the track's own title follows only when it says more than the language (presentation/language.ts).
  return [trackName(t)||('Track '+ordinal),[codec,layout].filter(Boolean).join(' ')].filter(Boolean).join(' · ');
}
export type PlaybackSnapshot = {
  /** Genuine server-directed start preparation; carries no session or media authority. */
  preparation?: StartPreparation;
  audioTracks?: AudioTracksSnapshot;
  nativeListening?: NativeListeningStatus;
  listeningDeadlines?: ListeningDeadlines;
  audioEffects: AudioEffectsSnapshot;
  listeningNotice?: ListeningNoticeCode;
  /** The music engine fell back for this song: the signal path says so, never a notice. */
  musicFallback?: 'processing' | 'eq';
  channel?: ChannelReference;
  linear?: ChannelProjection;
  /** The channel's start can't proceed (occurrence status "recoverable"); `linear.media.errorCode` says why. */
  linearStalled?: boolean;
  audio: AudioSelectionSnapshot;
  revision: number;
  intentId: number;
  /** One continuous playback. A start the viewer asks for (Play on a title, a new queue, a
   * download) begins a new one; the queue moving on (autoplay, Up Next, Next and Previous, a
   * retry or restart in place) stays in it. A choice made in the player (speed, subtitle
   * position, Forced only, picture fill) lasts while this stays the same and goes back to the
   * Settings when it changes; the audio and subtitle tracks are carried by the server. */
  playbackRun: number;
  seekRevision: number;
  pendingSeek?: Readonly<{revision: number; positionSeconds: number}>;
  seekError?: string;
  failedSeek?: Readonly<{revision: number; positionSeconds: number}>;
  /** The player ran out of buffered media and is trying to get more. A status, never an error:
   * playback is only ended by `fail`, on a definite refusal or after recovery is exhausted. */
  recovering?: boolean;
  phase: "idle" | "starting" | "ready" | "ended" | "error";
  intent: "playing" | "paused";
  itemId?: string;
  session?: PlaybackSession;
  positionSeconds: number;
  duration: number;
  error?: string;
  /** The server ended this playback, and why: "terminated" by an administrator (with their
   * message), "transferred", "lease_expired", "stopped"… `phase` is "error" and `error` is the
   * viewer's text for it (`sessionEndMessage`); nothing advances. Cleared by the next start. */
  ended?: SessionEndInfo;
  cleanupPending: number;
  cleanupError?: string;
};
export interface PlaybackAdapter {
  apply(snapshot: PlaybackSnapshot): void;
}
/** Actual current-song applied leveling gain: only when the report says leveling was applied and
 * its dB amount is finite and inside the renderer's admitted range. */
function appliedLevelingGain(facts:Pick<NativeListeningStatus,'levelingApplied'|'levelingGainDb'>|undefined):number|undefined{
 return facts?.levelingApplied===true&&typeof facts.levelingGainDb==='number'&&Number.isFinite(facts.levelingGainDb)&&facts.levelingGainDb>=-60&&facts.levelingGainDb<=30?facts.levelingGainDb:undefined;
}
/** UI intent is the sole command authority. Native events report facts, never autoplay intent. */
export class PlaybackService {
  /** Device engine processing (for example device-local EQ), never an account preference. */
  private deviceAudioProcessing = false;

  private channels?: ChannelPlayback;
  private channelWantedState:'playing'|'paused'='playing';
  private channelSeekId?: string;
  private channelObservedSeek?: string;
  private channelSubmittedSeek?: string;
  private channelReference?: ChannelReference;
  private audioSelection: AudioSelection;
  private queue?: QueueController;
  private queueConnection:{error?:string;retry?:()=>void}={};
  private api: PlaybackApi;
  /** A Playback v1 audio session the native listening owner plays (Apple, plan §9.3 C3): it
   * reports the timeline and moves the queue; this service only forwards intents to it. */
  private nativeV1?: NativeListeningV1Control;
  private listeningPolicy?:ListeningPolicy;
  private nativePolicyRevision=0;private nativePolicyKey="";
  private nativeResumeRevision=0;
  private nativePolicyPending?:{key:string;native:NativeListeningV1Control;revision:number;promise:Promise<boolean>};
  private listeningClock=new ListeningDeadlineClock(()=>{this.pause();this.update({listeningDeadlines:this.listeningClock.getSnapshot(),error:'Listening timer paused playback.'});});
  private options: PlaybackOptions;
  /** `beforeFreshPlayback` for the playback being started, awaited before its first request. */
  private freshReady?: Promise<void>;
  private snapshot: PlaybackSnapshot = {
    audioEffects: {settings:defaultAudioEffects,rendering:false,rate:1,eqApplied:false,levelingApplied:false},
    audio: emptyAudio(),
    revision: 0,
    intentId: 0,
    playbackRun: 0,
    seekRevision: 0,
    phase: "idle",
    intent: "paused",
    positionSeconds: 0,
    duration: 0,
    cleanupPending: 0,
  };
  private listeners = new Set<() => void>();
  private adapter?: PlaybackAdapter;
  private sequence = 0;
  private endedSeekPosition?: number;
  private failedRecovery?: { positionSeconds?: number; intent: "playing" | "paused" };
  /** The server's music preferences have been adopted: the device journal no longer overrides them. */
  private effectsFromServer = false;
  private lastPlay?: { itemId: string; prepared?: PreparedChoice; quality?: string; audioStream?: number; choices?: PlaybackChoices; local?: () => PlaybackSession; localContext?: PlaybackLocalStartContext };
  /** The session the engine-failure count belongs to. */
  private escalationSession?: string;
  private seekRearm?: () => void;
  private startupRearm?: () => void;
  private escalations = 0;
  private seekWait?: () => void;
  private hasObserved = false;
  private bookmarkProtected = true;
  private lastProgress = 0;
  private creates = new Map<string, { itemId: string; running: boolean }>();
  private cleanup = new Map<
    string,
    { running: boolean; checkpoint?: ProgressCheckpoint; work?: Promise<void> }
  >();
  private progressPending = new Map<string, ProgressCheckpoint>();
  private localSessions = new Map<string, PlaybackApi['localPlayback']>();
  private progressRunning = new Set<string>();
  private requestId: () => string;
  private startupWait?: () => void;
  private journalTail = Promise.resolve();
  private restored?: Promise<void>;
  constructor(
    api: PlaybackApi,
    requestId: () => string = () => randomId(),
    options: PlaybackOptions = {}
  ) {
    this.api = api;
    this.requestId = requestId;
    this.options = options;
    this.channels=options.channels;
    this.audioSelection = new AudioSelection(audio => this.update({audio}), () => this.snapshot, options.audioTimeoutMs);
    if (options.seekTimeoutMs !== undefined && (!Number.isFinite(options.seekTimeoutMs) || options.seekTimeoutMs < 1 || options.seekTimeoutMs > 60000)) throw new Error("Invalid seek timeout");
    api.onServerEnded?.((id, end) => this.serverEnded(id, end));
    if (!options.deferRecovery) this.startRecovery();
  }
  /** The server ended a session: an old one is dropped from the progress backlog; the current one
   * stops, telling the viewer why (an administrator's message as written). A session the engine
   * already finished is completion, unless an administrator ended it. */
  private serverEnded(sessionId: string, end: SessionEndInfo) {
    this.progressPending.delete(sessionId);
    const s = this.snapshot;
    if (s.session?.id !== sessionId || s.phase === "idle" || (s.phase === "ended" && end.reason !== "terminated")) return;
    this.fail(s.intentId, sessionEndMessage(end), { ended: Object.freeze({ reason: end.reason, message: end.message }) });
  }
  /** Explicit committed startup for React owners; safe to call repeatedly. */
  startRecovery(): Promise<void> {
    if (this.restored) return this.restored;
    this.restored = this.options.journal
      ? this.options.journal
          .load()
          .then((entries) => {
            for (const entry of entries) {
              if (entry.id && !this.cleanup.has(entry.id))
                this.cleanup.set(entry.id, {
                  running: false,
                  checkpoint: entry.checkpoint,
                });
              if (entry.request)
                this.creates.set(entry.request.requestId, {
                  itemId: entry.request.itemId,
                  running: false,
                });
            }
            this.update({});
            this.retryCleanup();
          })
          .catch(() => {
            this.update({
              cleanupError:
                "Playback recovery could not be read. Retry before starting another session.",
            });
          })
      : Promise.resolve();
    return this.restored;
  }

  getSnapshot = () => this.snapshot;
  subscribe = (listener: () => void) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  };
  attachAdapter(adapter: PlaybackAdapter) {
    this.adapter = adapter;
    adapter.apply(this.snapshot);
    return () => {
      if (this.adapter === adapter) this.adapter = undefined;
    };
  }
  private update(patch: Partial<PlaybackSnapshot>) {
    if(patch.phase==='idle'||patch.phase==='error'||patch.phase==='ended'||'intentId' in patch&&patch.intentId!==this.snapshot.intentId)patch={...patch,preparation:undefined};
    if(patch.intent==='playing'||patch.phase==='idle'||patch.phase==='error'||patch.error||'intentId' in patch&&patch.intentId!==this.snapshot.intentId)patch={...patch,listeningNotice:undefined};
    // A fallback lasts for this song only: its own terminal and new-run resets, separate
    // from the notice, including a real ended fact that keeps the same item and intent.
    if(patch.phase==='ended'||patch.phase==='idle'||patch.phase==='error'||patch.error||'intentId' in patch&&patch.intentId!==this.snapshot.intentId||patch.itemId!==undefined&&patch.itemId!==this.snapshot.itemId)patch={...patch,musicFallback:undefined};
    const changed='session' in patch&&(patch.session?.id!==this.snapshot.session?.id||patch.session?.generation!==this.snapshot.session?.generation);
    if(changed||patch.phase==='idle'||patch.phase==='error'||patch.phase==='ended'){
      const actual=patch.phase==='idle'||patch.phase==='error'||patch.phase==='ended'?undefined:patch.nativeListening;
      patch={...patch,audioEffects:{...(patch.audioEffects??this.snapshot.audioEffects),eqApplied:actual?.eqApplied===true,levelingApplied:actual?.levelingApplied===true,levelingGainDb:appliedLevelingGain(actual)}};
    }
    // `ended` belongs to one session: a new start or another session clears it.
    if (!("ended" in patch) && this.snapshot.ended && (patch.phase === "starting" || ("session" in patch && patch.session?.id !== this.snapshot.session?.id)))
      patch = { ...patch, ended: undefined };
    this.snapshot = Object.freeze({
      ...this.snapshot,
      ...patch,
      revision: this.snapshot.revision + 1,
      cleanupPending: this.cleanup.size + this.creates.size,
    });
    this.adapter?.apply(this.snapshot);
    for (const listener of this.listeners) listener();
  }
  private presentationAudioPreference?:string;
  /** Same session and occurrence; only committed transport generation changes.
   * Current intent, speed (adapter-owned), pending seek and source position win
   * over the older position used to prepare the replacement. */
  installSubtitlePresentation(p:import('./subtitles.ts').SubtitlePresentation):boolean {
    const before=this.snapshot,session=before.session;
    if(!session||p.sessionId!==session.id||p.generation<session.generation||!['starting','ready'].includes(before.phase))return false;
    if(p.generation===session.generation)return p.streamUrl===session.streamUrl&&p.mode===session.mode;
    if(!Number.isSafeInteger(p.generation)||!/^\/v1\/media\/[A-Za-z0-9_-]+(?:\/master\.m3u8|\/subtitle-video\/[A-Za-z0-9_-]+\/master\.m3u8)?$/.test(p.streamUrl))return false;
    const position=before.pendingSeek?.positionSeconds??before.positionSeconds;
    const oldPlan=before.audio.plan;
    this.presentationAudioPreference=before.audio.pending?.renditionId??before.audio.observedRenditionId??undefined;
    this.clearStartup();this.bookmarkProtected=true;this.hasObserved=false;
    const audio=this.audioSelection.reset();
    this.requestSeek(position,{session:{...session,generation:p.generation,streamUrl:p.streamUrl,mode:p.mode,resumeSeconds:position},phase:'starting',audio,error:undefined});
    if(oldPlan&&p.mode==='hls')this.audioSelection.install(before.intentId,{...oldPlan,generation:p.generation});
    return true;
  }
  /**
   * The same session re-planned on the server (a quality, version or audio change): its new
   * presentation replaces the old one in place. The queue, the intent and the position stay; the
   * engine loads the new stream where the picture was.
   */
  installPresentation(next:PlaybackSession):boolean {
    const before=this.snapshot,session=before.session;
    if(!session||next.id!==session.id||before.channel||!['starting','ready'].includes(before.phase))return false;
    if(!Number.isSafeInteger(next.generation)||next.generation<=session.generation)return next.generation===session.generation;
    const position=before.pendingSeek?.positionSeconds??(this.hasObserved?before.positionSeconds:session.resumeSeconds);
    this.clearStartup();this.bookmarkProtected=true;this.hasObserved=false;
    const intentId=before.intentId;
    this.requestSeek(position,{session:{...next,duration:next.duration||session.duration,resumeSeconds:position},phase:'starting',audio:this.audioSelection.reset(),audioTracks:before.audioTracks?Object.freeze({...before.audioTracks,selected:before.audioTracks.pending??before.audioTracks.selected,pending:undefined}):undefined,error:undefined,recovering:undefined,
      audioEffects:{...before.audioEffects,rendering:renderable(next)&&(audioEffectsEnabled(before.audioEffects.settings)||this.deviceAudioProcessing),prepared:undefined,committed:undefined}});
    this.audioPlanDiagnostic(next);
    this.armStartup(()=>{this.reportStalledStart(intentId);this.fail(intentId,'Playback did not become ready. Check the server and try again.');});
    return true;
  }
  /**
   * The engine is still receiving media for the start or seek in progress. A slow link or a
   * server converting slower than real time is not a failure: the start and seek windows begin
   * again, and only run out after a full window with nothing arriving at all.
   */
  engineProgress(intentId:number){
    if(intentId!==this.snapshot.intentId)return;
    this.startupRearm?.();this.seekRearm?.();
  }
  /** ListeningService is the sole policy producer; render preferences join the
   * same snapshot before crossing the native boundary. Identical observations
   * never reset native deadlines or native-only remote-control activity. */
  setListeningPolicy(value:ListeningPolicy){this.listeningPolicy=value;return this.syncListeningPolicy();}
  private syncListeningPolicy():Promise<boolean>{
    const native=this.nativeV1,original=Object.freeze({sessionId:this.snapshot.session?.id,generation:this.snapshot.session?.generation,run:this.snapshot.playbackRun,owner:this.snapshot.nativeListening?.owner});
    const originalSource=()=>this.snapshot.session?.id===original.sessionId&&this.snapshot.session?.generation===original.generation&&this.snapshot.playbackRun===original.run&&this.snapshot.nativeListening?.owner===original.owner;
    if(!this.listeningPolicy||!native)return Promise.resolve(true);
    if(!native.configure)return Promise.resolve(this.listeningPolicy.timer===null);
    const value={...this.listeningPolicy,effects:this.snapshot.audioEffects.settings},key=JSON.stringify(value);
    if(key===this.nativePolicyKey)return Promise.resolve(true);
    if(this.nativePolicyPending?.key===key&&this.nativePolicyPending.native===native)return this.nativePolicyPending.promise;
    this.nativePolicyKey='';
    const revision=++this.nativePolicyRevision;
    const promise=Promise.resolve().then(()=>native.configure!({...value,revision})).then(accepted=>{
      if(accepted!==true)throw new Error('Native listening policy was not accepted');
      if(revision!==this.nativePolicyRevision||this.nativeV1!==native)return false;
      this.nativePolicyKey=key;return true;
    }).catch(()=>{
      // A failed bridge policy must not leave audible playback behind a timer
      // the UI assumes is armed. A late error cannot pause a different owner.
      if(revision!==this.nativePolicyRevision||this.nativeV1!==native)return false;
      this.nativePolicyKey='';if(originalSource()){this.pause();this.listeningNotice('listening_policy_failed');}return false;
    }).finally(()=>{if(this.nativePolicyPending?.revision===revision)this.nativePolicyPending=undefined;});
    this.nativePolicyPending={key,native,revision,promise};return promise;
  }
  isNativeListening(){return !!this.snapshot.nativeListening||!!this.nativeV1;}
  async setListeningDeadline(kind:'sleep'|'passout',minutes:number|null){
    if(minutes!==null&&(!Number.isFinite(minutes)||minutes<=0||minutes>1440))throw new Error('Choose a listening timer between 1 minute and 24 hours.');
    const key=kind==='sleep'?'sleepAtMs':'passoutAtMs',value={...(this.snapshot.listeningDeadlines??noListeningDeadlines),[key]:minutes===null?null:Date.now()+Math.round(minutes*60000)};
    if(this.nativeV1){await this.nativeV1.deadlines(value);return;}
    this.listeningClock.set(value);
    this.update({listeningDeadlines:value});
  }
  checkListeningDeadlines(){if(!this.nativeV1)this.listeningClock.check();}
  systemListeningAction(action:'next'|'previous'){return this.nativeV1?.advance(action)??Promise.resolve();}
  setPlaybackRate(rate:number){
    if(!Number.isFinite(rate)||rate<.25||rate>4)return;
    // AVAudioUnitTimePitch is controlled by the native owner. A JS seek here
    // would duplicate its intent and restart a PCM window at the old epoch.
    if(this.nativeV1){void this.nativeV1.action('rate',rate).catch(()=>{});return;}
    // The speed already in use: nothing to restart (a new session re-applies the held speed).
    if(rate===this.snapshot.audioEffects.rate)return;
    // Where the listener is, or is about to be: a start or seek still on its way (a resume
    // part-way into a book) keeps its target rather than going back to the last reported position.
    const at=this.snapshot.pendingSeek?.positionSeconds??this.snapshot.positionSeconds;
    this.queue?.seekRetiringAudio?.(at);this.queue?.invalidateAudioPreparation?.();
    this.update({audioEffects:{...this.snapshot.audioEffects,rate}});
    if(this.snapshot.audioEffects.rendering&&this.snapshot.session)this.requestSeek(at);
  }
  /** The native v1 owner re-reads its session (the app returned to the foreground). */
  refreshPlaybackAuthority(){return this.nativeV1?.refresh()??Promise.resolve();}
  installAudioPlan(intentId:number,plan:AudioPlan){return this.audioSelection.install(intentId,plan);}
  invalidateAudioPlan(intentId:number,reason:string){this.audioSelection.invalidate(intentId,reason);}
  audioUnavailable(intentId:number,reason:string){this.audioSelection.unavailable(intentId,reason);}
  attachAudioEngine(binding:AudioBinding){return this.audioSelection.attach(binding);}
  detachAudioEngine(binding:AudioBinding,epoch:number){this.audioSelection.detach(binding,epoch);}
  audioMapped(binding:AudioBinding,epoch:number,ids:readonly string[]){const mapped=this.audioSelection.mapped(binding,epoch,ids);const preferred=this.presentationAudioPreference;if(mapped&&preferred&&ids.includes(preferred)){this.presentationAudioPreference=undefined;this.audioSelection.select(preferred);}return mapped;}
  selectAudio(id:string){return this.audioSelection.select(id);}
  /** The session's audio tracks, for a stream that carries one track at a time (an original, a
   * repackaged or converted stream without alternate renditions). Choosing one asks for the title
   * again from the same position with that track named; the server picks the delivery that reaches
   * it, which may differ from the current one. */
  installAudioTracks(intentId:number,tracks:readonly AudioTrackOffer[],selected:number|null){
    if(intentId!==this.snapshot.intentId||!this.snapshot.session)return;
    const clean=tracks.filter(t=>Number.isInteger(t.streamIndex)&&t.streamIndex>=0&&t.streamIndex<=65535).slice(0,64);
    // The track this play asked for by name is the one in use; otherwise the caller's (the file's default).
    const choice=this.lastPlay?.itemId===this.snapshot.itemId?this.lastPlay?.choices?.audioTrackId:undefined;
    const asked=this.lastPlay?.itemId===this.snapshot.itemId?(choice&&/^a\d{1,4}$/.test(choice)?Number(choice.slice(1)):this.lastPlay?.audioStream):undefined;
    if(asked!==undefined&&clean.some(t=>t.streamIndex===asked))selected=asked;
    this.update({audioTracks:clean.length>1?Object.freeze({tracks:Object.freeze(clean.map(t=>Object.freeze({...t}))),selected}):undefined});
  }
  selectAudioTrack(streamIndex:number):Promise<void>{
    const tracks=this.snapshot.audioTracks,itemId=this.snapshot.itemId,last=this.lastPlay;
    if(!tracks||!itemId||last?.local||tracks.pending!==undefined||tracks.selected===streamIndex||!tracks.tracks.some(t=>t.streamIndex===streamIndex))return Promise.resolve();
    const position=this.snapshot.pendingSeek?.positionSeconds??this.snapshot.positionSeconds;
    this.update({audioTracks:Object.freeze({...tracks,pending:streamIndex})});
    // The live session is re-planned with that track (same session, same queue entry).
    if(this.queue?.changePlayback&&this.snapshot.session&&['starting','ready'].includes(this.snapshot.phase)){
      const intentId=this.snapshot.intentId;
      return this.changePlayback({audioTrackId:'a'+streamIndex}).then(()=>{
        const now=this.snapshot.audioTracks;
        if(this.snapshot.intentId===intentId&&now?.pending===streamIndex)this.update({audioTracks:Object.freeze({...now,selected:streamIndex,pending:undefined})});
      },error=>{
        const now=this.snapshot.audioTracks;
        if(this.snapshot.intentId===intentId&&now?.pending===streamIndex)this.update({audioTracks:Object.freeze({...now,pending:undefined})});
        throw error;
      });
    }
    return this.play(itemId,position,undefined,last?.itemId===itemId?last.quality:undefined,streamIndex,last?.itemId===itemId?last.choices:undefined);
  }
  /**
   * Change the quality, version or audio track of what is playing. The session is re-planned on
   * the server and stays the same session on the same queue entry, so Up next is untouched. The
   * choices are remembered for a replay or an automatic retry of this title.
   */
  /** Whether a change of quality, version or track can be made on the playing session itself
   * (`changePlayback` does nothing otherwise: a channel, a downloaded file, nothing playing). */
  changesInPlace():boolean{
    return !!this.snapshot.itemId&&!!this.snapshot.session&&!this.snapshot.channel&&!this.lastPlay?.local&&!!this.queue?.changePlayback;
  }
  changePlayback(choices:PlaybackChoices):Promise<void>{
    const itemId=this.snapshot.itemId,session=this.snapshot.session;
    if(!itemId||!session||this.snapshot.channel||this.lastPlay?.local||!this.queue?.changePlayback)return Promise.resolve();
    const {replan: _replan, ...remembered}=choices;
    if (choices.subtitleTrackId !== undefined && choices.subtitleDelivery === undefined && this.lastPlay?.choices) {
      const {subtitleDelivery: _oldDelivery, subtitleOffsetUs: _oldExactOffset, ...previous} = this.lastPlay.choices;
      this.lastPlay = {...this.lastPlay, choices: previous};
    }
    this.lastPlay={...(this.lastPlay?.itemId===itemId?this.lastPlay:{itemId}),choices:{...(this.lastPlay?.itemId===itemId?this.lastPlay.choices:undefined),...remembered}};
    return this.queue.changePlayback(choices);
  }
  /** The choices the current title was started or last changed with. */
  playbackChoices():PlaybackChoices{return this.lastPlay?.itemId===this.snapshot.itemId?this.lastPlay?.choices??{}:{};}
  audioObserved(binding:AudioBinding,epoch:number,sequence:number,id:string|null){return this.audioSelection.observed(binding,epoch,sequence,id);}
  audioApplied(binding:AudioBinding,epoch:number,revision:number,id:string){return this.audioSelection.applied(binding,epoch,revision,id);}
  audioRenditionUnavailable(binding:AudioBinding,epoch:number,id:string){return this.audioSelection.renditionUnavailable(binding,epoch,id);}
  audioFailed(binding:AudioBinding,epoch:number,revision:number,reason:string,unavailable=false){return this.audioSelection.failed(binding,epoch,revision,reason,unavailable);}
  /** The device's cached effects (the queue journal): used only until the server's music
   * preferences have been adopted, and offline (X-02). */
  restoreAudioEffects(settings:AudioEffectsSettings){if(this.effectsFromServer)return;this.update({audioEffects:{...this.snapshot.audioEffects,settings:parseAudioEffects(settings)}});this.syncListeningPolicy();}
  /** X-02: the viewer's music preferences from the server (`musicAudioEffects`), applied on
   * sign-in, profile selection and every preference change. They win over the device's cached
   * copy, which they also refresh. The same settings again change nothing. */
  adoptAudioEffectsPreference(settings:AudioEffectsSettings){
    settings=parseAudioEffects(settings);this.effectsFromServer=true;
    const current=this.snapshot.audioEffects.settings;
    // This setting belongs to one app/device, never the profile registry.
    settings={...settings,...(current.sampleRateMatching?{sampleRateMatching:current.sampleRateMatching}:{})};
    if(current.gapless===settings.gapless&&current.crossfadeSeconds===settings.crossfadeSeconds&&current.normalization===settings.normalization&&current.neverBoost===settings.neverBoost&&current.smoothTransitions===settings.smoothTransitions)return;
    this.setAudioEffects(settings);
  }
  /** Local settings owner supplies the same device value on every output. No persistence here. */
  setDeviceSampleRateMatching(value:'smart'|'strict'|'off'){
    if(!['smart','strict','off'].includes(value))throw new Error('Invalid sample-rate matching setting.');
    const effects=this.snapshot.audioEffects;
    if(effects.settings.sampleRateMatching===value)return;
    this.update({audioEffects:{...effects,settings:{...effects.settings,sampleRateMatching:value}}});
    this.syncListeningPolicy();
  }
  setAudioEffects(settings:AudioEffectsSettings){
    settings=parseAudioEffects(settings);if(!this.nativeV1){this.queue?.seekRetiringAudio?.(this.snapshot.positionSeconds);this.queue?.invalidateAudioPreparation?.();}
    const current=this.snapshot,effects=current.audioEffects;
    const enable=renderable(current.session)&&(audioEffectsEnabled(settings)||this.deviceAudioProcessing);
    const sameNormalization=effects.settings.normalization===settings.normalization;
    this.update({audioEffects:{...effects,settings,rendering:effects.rendering||enable,levelingApplied:sameNormalization&&effects.levelingApplied,levelingGainDb:sameNormalization&&effects.levelingApplied?effects.levelingGainDb:undefined}});
    this.syncListeningPolicy();
    if(enable&&!effects.rendering&&current.session&&!this.nativeV1)this.requestSeek(current.positionSeconds);
    void this.queue?.saveAudioEffects(settings);
  }
  /** The platform engine selects local processing without rewriting the viewer's music settings. */
  setDeviceAudioProcessing(enabled:boolean){
    if(this.deviceAudioProcessing===enabled)return;
    this.deviceAudioProcessing=enabled;
    const current=this.snapshot,effects=current.audioEffects;
    const rendering=renderable(current.session)&&(enabled||audioEffectsEnabled(effects.settings));
    if(rendering===effects.rendering)return;
    this.queue?.invalidateAudioPreparation?.();
    const audioEffects={...effects,rendering,prepared:undefined,committed:undefined};
    if(current.session)this.requestSeek(current.pendingSeek?.positionSeconds??current.positionSeconds,{audioEffects});
    else this.update({audioEffects});
  }
  audioRenderStatus(effectiveRate:number,waiting:boolean){
    if(!Number.isFinite(effectiveRate)||effectiveRate<0||effectiveRate>4)return;
    const effects=this.snapshot.audioEffects;
    if(!effects.rendering||effects.effectiveRate===effectiveRate&&effects.waiting===waiting)return;
    this.update({audioEffects:{...effects,effectiveRate,waiting}});
  }
  /** A lasting engine fallback for this song, read by the signal path; never a notice.
   * The caller passes the deck it observed, and the write is refused unless that exact
   * session, generation, item and intent is still the current, live one. */
  listeningFallback(kind:'processing'|'eq',captured:Readonly<{sessionId?:string;generation?:number;itemId?:string;intentId:number}>){
    const live=this.snapshot;
    if(!captured.sessionId||live.phase!=='ready'||live.error)return;
    if(!live.session||live.session.id!==captured.sessionId||live.session.generation!==captured.generation)return;
    if(live.itemId!==captured.itemId||live.intentId!==captured.intentId)return;
    if(live.musicFallback!==kind)this.update({musicFallback:kind});
  }
  listeningNotice(code:ListeningNoticeCode){
    if(code!=='audio_autoplay_blocked'&&code!=='listening_policy_failed')return;
    if(this.snapshot.intent==='paused'&&this.snapshot.phase!=='error'&&this.snapshot.phase!=='idle'&&this.snapshot.listeningNotice!==code)this.update({listeningNotice:code});
  }
  /** Called only after filters/gain have actually been installed or removed for this song. */
  audioProcessingApplied(binding:AudioProcessingBinding,facts:AudioProcessingApplied):boolean{
    const s=this.snapshot,amount=facts.levelingGainDb;
    if(!s.session||this.nativeV1||s.phase==='idle'||s.phase==='error'||s.phase==='ended'||s.intentId!==binding.intentId||s.session.id!==binding.sessionId||s.session.generation!==binding.generation||typeof facts.eqApplied!=='boolean'||typeof facts.levelingApplied!=='boolean'||amount!==undefined&&(typeof amount!=='number'||!Number.isFinite(amount)||amount<-60||amount>30))return false;
    const levelingGainDb=facts.levelingApplied&&amount!==undefined?amount:undefined;
    if(s.audioEffects.eqApplied!==facts.eqApplied||s.audioEffects.levelingApplied!==facts.levelingApplied||s.audioEffects.levelingGainDb!==levelingGainDb)this.update({audioEffects:{...s.audioEffects,eqApplied:facts.eqApplied,levelingApplied:facts.levelingApplied,levelingGainDb}});
    return true;
  }
  audioDiagnostic(code:string,detail?:string){
    if(!/^[a-z][a-z0-9_]{0,95}$/.test(code))return;
    const safe=detail?.replace(/[\x00-\x1f\x7f]/g,' ').slice(0,1024);
    this.options.audioDiagnostic?.(code,safe);
    if(this.options.diagnostics)console.debug('[audio]',code,safe??'');
  }
  private audioPlanDiagnostic(session:PlaybackSession){if(session.audio?.reason)this.audioDiagnostic('audio_plan',session.audio.reason);}
  prepareAudio(prepared:PreparedAudio){this.update({audioEffects:{...this.snapshot.audioEffects,prepared}});}
  clearPreparedAudio(){const e=this.snapshot.audioEffects;if(e.prepared||e.committed)this.update({audioEffects:{...e,prepared:undefined,committed:undefined}});}
  audioPrepared(token:string){this.queue?.audioPrepared(token);}
  audioCommit(token:string){void this.queue?.commitAudio(token);}
  audioBoundary(token:string,position:number){void this.queue?.audioBoundary(token,position);}
  audioPreparationFailed(){this.queue?.discardAudio();}
  audioRenderFailed(message:string){this.queue?.audioRenderFailed(message);}
  /** A failed optional decoder hands the same session to the platform player. */
  fallbackAudioRender():boolean {
    const current=this.snapshot;
    if(!current.session||!current.audioEffects.rendering||!current.session.streamUrl||this.nativeV1)return false;
    this.queue?.invalidateAudioPreparation?.();this.bookmarkProtected=true;
    this.requestSeek(current.pendingSeek?.positionSeconds??current.positionSeconds,{audioEffects:{...current.audioEffects,rendering:false,eqApplied:false,levelingApplied:false,levelingGainDb:undefined,prepared:undefined,committed:undefined}});
    return true;
  }
  async commitAudioAuthority(token:string,itemId:string,session:PlaybackSession,expectedIntent:number):Promise<boolean>{
    if(this.snapshot.intentId!==expectedIntent)return false;
    // The server has committed the next session (§18.3); the bridge reports its timeline from the
    // boundary on. Buffered outgoing samples may still drain, but they write no progress.
    this.bookmarkProtected=true;
    this.update({audioEffects:{...this.snapshot.audioEffects,committed:{token,itemId,session}}});return true;
  }
  adoptAudioBoundary(token:string,position:number):boolean{
    const next=this.snapshot.audioEffects.committed;if(!next||next.token!==token||!Number.isFinite(position)||position<0)return false;
    this.clearStartup();this.clearSeek();this.hasObserved=true;this.bookmarkProtected=false;this.sequence=0;this.lastProgress=position;
    this.update({intentId:this.snapshot.intentId+1,session:next.session,itemId:next.itemId,phase:'ready',positionSeconds:position,duration:next.session.duration,pendingSeek:undefined,failedSeek:undefined,seekError:undefined,audio:this.audioSelection.reset(),audioEffects:{...this.snapshot.audioEffects,prepared:undefined,committed:undefined,rendering:true}});
    this.checkpoint(this.snapshot.intent);void this.persist();return true;
  }
  private clearStartup() {
    this.startupRearm = undefined;
    this.startupWait?.();
    this.startupWait = undefined;
  }
  /** Both media deadlines count foreground time, with the same progress-based rearm. */
  private visibleDeadline(ms: number, expire: () => void) {
    const visibility = this.options.pageVisibility, now = this.options.now ?? Date.now;
    let timer: ReturnType<typeof setTimeout> | undefined, off: (() => void) | undefined;
    let remaining = ms, started = 0, closed = false;
    const pause = () => {
      if (timer === undefined) return;
      clearTimeout(timer); timer = undefined;
      remaining = Math.max(0, remaining - Math.max(0, now() - started));
    };
    const clear = () => { closed = true; pause(); off?.(); off = undefined; };
    const arm = () => {
      if (closed || timer !== undefined || visibility?.hidden()) return;
      started = now();
      timer = setTimeout(() => {
        timer = undefined;
        // Older adapters expose only onVisible. A timer firing while hidden waits for
        // a fresh visible window; adapters with onChange paused it at backgrounding.
        if (visibility?.hidden()) { remaining = ms; return; }
        clear(); expire();
      }, remaining);
      (timer as any)?.unref?.();
    };
    const rearm = () => { if (!closed) { pause(); remaining = ms; arm(); } };
    off = visibility?.onChange
      ? visibility.onChange(() => { if (visibility.hidden()) pause(); else arm(); })
      : visibility?.onVisible(rearm);
    arm();
    return {clear, rearm};
  }
  /** The start and its foreground deadline retire together on readiness or replacement. */
  private armStartup(expire: () => void) {
    this.clearStartup();
    const deadline = this.visibleDeadline(this.options.startupTimeoutMs ?? 20000, expire);
    this.startupWait = deadline.clear;
    this.startupRearm = deadline.rearm;
  }
  /** Channel selection uses this same service/adapter owner, but never a VOD
   * create, persisted catalog placeholder, resume checkpoint or chapter query. */
  async playChannel(reference:ChannelReference,options:ChannelStartOptions={}):Promise<void> {
    const state=options.state??'playing';
    if(state!=='playing'&&state!=='paused')throw new Error('invalid_channel_start_state');
    if(options.signal?.aborted||options.isCurrent?.()===false)throw Object.assign(new Error('cancelled'),{name:'AbortError'});
    const oldVOD=this.snapshot.channel?undefined:this.snapshot.session?.id;
    this.leave();this.channelReference=reference;this.channelWantedState=state;
    const intentId=this.snapshot.intentId;
    this.channelSeekId=undefined;this.channelObservedSeek=undefined;this.channelSubmittedSeek=undefined;
    this.update({channel:reference,phase:'starting',intent:state,itemId:reference.channelId,playbackRun:this.snapshot.playbackRun+1});
    if(!this.channels){this.update({phase:'error',error:'Channel playback is not configured on this client.'});return;}
    // Release the exact prior VOD before live admission. This is resource
    // cleanup, not a predicted capacity check or a profile-wide singleton.
    if(oldVOD){try{await this.persist();await this.tryCleanup(oldVOD);}catch{
      if(this.snapshot.intentId===intentId)this.update({phase:'error',error:'The previous playback could not be stopped. Retry this channel when the server reconnects.'});return;
    }}
    if(this.snapshot.intentId!==intentId||!this.snapshot.channel||options.signal?.aborted||options.isCurrent?.()===false)return;
    await this.channels.open(reference,{
      state:value=>{if(this.snapshot.intentId===intentId&&this.snapshot.channel)this.applyChannel(value);},
      preparation:value=>{if(this.snapshot.intentId===intentId&&this.snapshot.channel)this.reportStartPreparation(intentId,value);},
      error:(message,terminal)=>{if(this.snapshot.intentId!==intentId||!this.snapshot.channel)return;this.update({error:message,...(terminal?{phase:'error' as const,session:undefined,pendingSeek:undefined}:{} )});}
    },{...options,state});
  }
  /** Channels on v1 sessions (spec §18.6): the apps switch to this when the server offers them. */
  useChannelPlayback(control:ChannelPlayback){if(this.snapshot.channel)this.leave();this.channels=control;}
  private applyChannel(state:ChannelView){
    const linear=state.linear,m=linear.media,seek=linear.desired.seek;
    // B-11.27: the owner lets recordings take a watched tuner and this one's moment has come (or
    // the server already gave it away). Stop here, once, and say why; nothing retries.
    if(tunerHandedOver(m,this.options.now?.()??Date.now())){this.handTunerOver(linear);return;}
    const changed=this.snapshot.session?.generation!==Number(m.bufferGeneration);
    let patch:Partial<PlaybackSnapshot>={linear,channel:linear.channel,linearStalled:state.status==='recoverable',intent:this.channelWantedState==='paused'?'paused':linear.desired.state,error:m.errorCode?channelMessage(m.errorCode):undefined};
    // No engine attachment until an ordered, server-resolved join target exists.
    if(m.streamUrl&&seek&&seek.bufferGeneration===m.bufferGeneration&&seek.positionUs!==null){
      const target=channelSeconds(seek.positionUs);
      if(!this.snapshot.session||changed){
        const generation=Number(m.bufferGeneration);if(!Number.isSafeInteger(generation)){this.fail(this.snapshot.intentId,'Channel generation is not supported.');return;}
        patch.session={id:state.playbackId,generation,streamUrl:m.streamUrl,mode:'hls',duration:0,resumeSeconds:target};
        patch.phase='starting';patch.duration=0;this.channelSeekId=undefined;this.channelObservedSeek=undefined;this.channelSubmittedSeek=undefined;
      }else if(this.snapshot.phase==='error')patch.phase='error';
      if(seek.seekCommandId!==this.channelSeekId){
        this.clearSeek();this.channelSeekId=seek.seekCommandId;this.channelSubmittedSeek=undefined;
        const revision=this.snapshot.seekRevision+1,intent=this.snapshot.intentId;
        patch.seekRevision=revision;patch.pendingSeek=Object.freeze({revision,positionSeconds:target});patch.failedSeek=undefined;patch.seekError=undefined;
        const deadline=this.visibleDeadline(this.options.seekTimeoutMs??15000,()=>this.seekFailed(intent,revision,'That position is no longer available. Choose Go Live or another position.'));
        this.seekWait=deadline.clear;
      }
      if(linear.acknowledgedSeekCommandId===seek.seekCommandId&&this.channelSubmittedSeek===seek.seekCommandId&&this.channelObservedSeek!==seek.seekCommandId&&!linear.seekError){
        this.channelObservedSeek=seek.seekCommandId;this.clearSeek();patch.pendingSeek=undefined;patch.seekError=undefined;patch.failedSeek=undefined;
        if(linear.confirmedPositionUs!==null)patch.positionSeconds=channelSeconds(linear.confirmedPositionUs);
      }
    }else if(changed&&this.snapshot.session){
      this.clearSeek();patch.session=undefined;patch.pendingSeek=undefined;patch.phase='starting';patch.seekError='The retained window changed. Choose Go Live to rejoin.';
    }
    if(linear.seekError){this.clearSeek();patch.failedSeek=patch.pendingSeek??this.snapshot.pendingSeek??this.snapshot.failedSeek;patch.pendingSeek=undefined;patch.seekError=channelMessage(linear.seekError);}
    // Expiry is recovery, not a successful clamped seek. Freeze the engine until
    // an explicit new target is accepted; the rolling producer remains active.
    const observedPosition=patch.positionSeconds??this.snapshot.positionSeconds;
    if(m.streamUrl&&this.channelObservedSeek&&!patch.pendingSeek&&!this.snapshot.pendingSeek&&
       (observedPosition<channelSeconds(m.windowStartUs)||observedPosition>channelSeconds(m.windowEndUs)+1)){
      patch.seekError='That position has left the retained window. Choose Go Live.';
      patch.failedSeek={revision:this.snapshot.seekRevision,positionSeconds:observedPosition};
    }
    this.update(patch);
  }
  private handTunerOver(linear:ChannelView['linear']){
    const intentId=this.snapshot.intentId;
    this.channels?.close();this.channelSeekId=undefined;this.channelObservedSeek=undefined;this.channelSubmittedSeek=undefined;
    this.fail(intentId,sessionEndMessage({reason:TUNER_RESERVED}),{ended:Object.freeze({reason:TUNER_RESERVED}),linear,linearStalled:false,session:undefined});
  }
  goLive(){if(this.snapshot.channel){this.channelWantedState='playing';this.channels?.goLive();}}
  getQueueConnection(){return this.queueConnection;}
  setQueueConnection(connection:{error?:string;retry?:()=>void}){this.queueConnection=connection;this.update({});}
  getQueueController(){return this.queue;}
  installQueue(queue:QueueController){this.queue=queue;this.update({});return()=>{if(this.queue===queue){this.queue=undefined;this.update({});}};}
  /** `fresh`: the viewer started a new playback (a new queue), not the queue moving on. */
  beginQueueIntent(itemId?:string,fresh=false,state:'playing'|'paused'='playing'):number{
    // Live uses another lane and never becomes a queue replacement predecessor.
    if(this.snapshot.channel)this.leave(false);
    const run=fresh?{playbackRun:this.snapshot.playbackRun+1}:{};
    // A parked restore or server-selected start has no new per-title choices to retain.
    if(fresh&&(state==='paused'||itemId===undefined)){this.lastPlay=undefined;this.escalations=0;}
    if(fresh&&this.options.beforeFreshPlayback)this.freshReady=this.options.beforeFreshPlayback().catch(()=>{});
    if(!this.snapshot.session){this.clearStartup();this.update({phase:'starting',intent:state,itemId,error:undefined,preparation:undefined,...run});}
    else this.update({intent:state,error:undefined,preparation:undefined,...run});
    return this.snapshot.intentId;
  }
  /** Presentation only: the queue/channel start retains the actual original
   * intent and clears this status on settlement. It never installs a session. */
  reportStartPreparation(intentId:number,preparation:StartPreparation|undefined):void{
    if(this.snapshot.intentId===intentId)this.update({preparation});
  }
  async prepareQueueIntent(intentId:number):Promise<boolean>{
    if(this.snapshot.intentId!==intentId||this.snapshot.channel)return false;
    const fresh=this.freshReady;this.freshReady=undefined;
    await fresh;
    await this.channels?.recover();
    return this.snapshot.intentId===intentId&&!this.snapshot.channel;
  }
  queueEnded(expectedIntentId:number){if(this.snapshot.intentId===expectedIntentId&&this.snapshot.phase==='ended')this.update({intent:'paused'});}
  queueFailed(itemId:string,message:string){if(!this.snapshot.channel&&!this.snapshot.session)this.update({phase:'error',itemId,intent:'paused',error:message});}
  async adoptQueueSession(itemId:string,session:PlaybackSession,expectedIntentId:number):Promise<boolean>{
    if(this.snapshot.intentId!==expectedIntentId)return false;
    // A title the queue started (Next, a container, the next episode) is the one an engine
    // failure asks for again; one started through `play` keeps the choices it was started with.
    if(this.lastPlay?.itemId!==itemId||this.lastPlay.local){this.lastPlay={itemId};this.escalations=0;}
    const intent=this.snapshot.intent;
    this.leave(false,true);this.sequence=0;this.lastProgress=0;
    const intentId=this.snapshot.intentId;
    this.update({phase:'starting',itemId,intent,error:undefined});
    this.requestSeek(session.resumeSeconds,{session,duration:session.duration,audioEffects:{...this.snapshot.audioEffects,rendering:renderable(session)&&(audioEffectsEnabled(this.snapshot.audioEffects.settings)||this.deviceAudioProcessing),prepared:undefined,committed:undefined}});
    this.audioPlanDiagnostic(session);
    if(this.snapshot.intentId===intentId&&this.snapshot.phase!=='ready'){
      this.armStartup(()=>{this.reportStalledStart(intentId);this.fail(intentId,'The selected queue item did not become ready. Retry playback or choose another item.');});
    }
    await this.persist();return this.snapshot.intentId===intentId&&!this.snapshot.cleanupError;
  }
  /**
   * A v1 audio session the native owner plays (plan §9.3 C3). `start` hands the session over (the
   * owner's first snapshot arrives through `applyNativeQueueEvent`); from then on pause, resume,
   * seek, rate, retry, timers and queue moves go to `native`, and this service never reports the
   * session's timeline or stops it itself.
   */
  async adoptNativeQueueSession(itemId:string,expectedIntentId:number,native:NativeListeningV1Control,start:()=>Promise<void>):Promise<boolean>{
    if(this.snapshot.intentId!==expectedIntentId)return false;
    const intent=this.snapshot.intent;
    const deadlines=this.snapshot.listeningDeadlines??noListeningDeadlines;
    this.leave(false,true);this.sequence=0;this.lastProgress=0;
    this.update({phase:'starting',itemId,intent,error:undefined});
    this.nativeV1=native;
    try{await start();}catch(error){if(this.nativeV1===native){this.nativeV1=undefined;void native.detach(true).catch(()=>{});}throw error;}
    if(this.nativeV1!==native)return false;
    if(this.listeningPolicy){this.nativePolicyKey='';this.syncListeningPolicy();}
    else await native.deadlines(deadlines).catch(()=>{});
    if(this.snapshot.phase!=='ready'&&this.snapshot.phase!=='error'&&this.nativeV1===native){
      this.armStartup(()=>{if(this.nativeV1===native&&this.snapshot.phase==='starting')this.fail(this.snapshot.intentId,'The selected queue item did not become ready. Retry playback or choose another item.');});
    }
    return true;
  }
  /** The native v1 owner's snapshot, with its session in this service's shape. */
  applyNativeQueueEvent(event:NativeListeningV1Event,session:PlaybackSession){
    if(!this.nativeV1)return;
    const before=this.snapshot;
    if(before.nativeListening?.owner===event.owner&&event.sequence<=before.nativeListening.sequence)return;
    const changed=before.session?.id!==session.id||before.session?.generation!==session.generation;
    if(event.ready)this.clearStartup();
    if(event.observed&&!event.seeking){this.clearSeek();this.bookmarkProtected=false;this.hasObserved=true;this.lastProgress=event.positionSeconds;}
    this.update({nativeListening:event,session,itemId:event.session.itemId??before.itemId,
      intentId:event.binding?.intentId??before.intentId,intent:event.intent,
      duration:session.duration,positionSeconds:event.observed?event.positionSeconds:(changed?session.resumeSeconds:before.positionSeconds),
      phase:event.terminal?'error':event.ended?'ended':event.ready?'ready':'starting',
      ...(changed?{audio:this.audioSelection.reset()}:{}),
      ...(event.observed&&!event.seeking?{pendingSeek:undefined,failedSeek:undefined,seekError:undefined}:{}),
      audioEffects:{...this.snapshot.audioEffects,rendering:event.rendering??false,eqApplied:event.eqApplied===true,levelingApplied:event.levelingApplied===true,levelingGainDb:appliedLevelingGain(event),rate:event.rate,effectiveRate:event.effectiveRate,waiting:event.rateWaiting,prepared:undefined,committed:undefined},
      error:event.error??undefined,listeningDeadlines:event.deadlines});
    // The server ended it (an administrator, a transfer, the lease): its reason, never an advance.
    if(event.terminal&&event.end)this.serverEnded(session.id,event.end);
    // A terminal owner acts on nothing more: let go of it, so Retry starts afresh (a new session,
    // handed to a new owner) instead of asking the finished one.
    if(event.terminal){const native=this.nativeV1;this.nativeV1=undefined;void native?.detach(true).catch(()=>{});}
  }
  /** Whether a native v1 owner holds the current session (queue moves then go through it). */
  hasNativeOwner(){return !!this.nativeV1;}
  async confirmQueueEnd(intentId:number):Promise<void>{
    const snapshot=this.snapshot,session=snapshot.session;
    if(snapshot.channel||intentId!==snapshot.intentId||snapshot.phase!=='ended'||!session||this.localSessions.has(session.id)||!this.hasObserved||this.bookmarkProtected)throw new Error('Playback completion is not confirmed.');
    const payload:ProgressCheckpoint={generation:session.generation,sequence:++this.sequence,positionSeconds:snapshot.positionSeconds,state:'ended'};
    this.progressPending.set(session.id,payload);await this.persist();
    await this.bounded(()=>this.api.progressPlayback(session.id,payload));
    if(this.progressPending.get(session.id)===payload)this.progressPending.delete(session.id);await this.persist();
  }
  /** `audioStream` is the absolute stream index of an audio track from the
   * offer. The server may change the delivery mode to reach it (an original whose
   * second track the engine cannot select is repackaged instead). */
  play(itemId: string, startPosition?: number, prepared?: PreparedChoice, quality?: string, audioStream?: number, choices?: PlaybackChoices): Promise<void> {
    if(this.snapshot.listeningNotice)this.update({listeningNotice:undefined});
    if (this.lastPlay?.itemId !== itemId) this.escalations = 0;
    this.lastPlay = { itemId, prepared, quality, audioStream, choices };
    if(this.queue)return this.queue.playItem(itemId,startPosition,prepared,quality,audioStream,choices);
    if(prepared){this.queueFailed(itemId,"Prepared playback requires the device queue. Reconnect it before choosing a version.");return Promise.resolve();}
    if(this.options.queueRequired){if(this.snapshot.channel)this.leave();this.queueFailed(itemId,this.queueConnection.error??'The device queue is still connecting. Retry once it is ready.');return Promise.resolve();}
    this.update({playbackRun:this.snapshot.playbackRun+1});
    return this.startPlayback(itemId, startPosition, "playing");
  }
  /**
   * OS-07: a title stored on this device (a download). It never goes through the device queue and
   * never asks the server for a session: `session` makes the local one (a fresh id each time, also
   * for a retry), and its progress and stop go to this service's PlaybackApi like any other, which
   * keeps them on the device (see the Apple app's offline playback).
   */
  async playLocal(itemId: string, session: () => PlaybackSession, startPosition?: number, context?: PlaybackLocalStartContext): Promise<void> {
    if(!localStartCurrent(context))throw localStartCancelled();
    const requested=context?.initialIntent,intent=requested===undefined?"playing":requested;
    if(intent!=="playing"&&intent!=="paused")throw new Error('invalid_local_start_intent');
    if(this.snapshot.listeningNotice)this.update({listeningNotice:undefined});
    this.escalations = 0;
    const captured=context?Object.freeze({signal:context.signal,isCurrent:context.isCurrent,initialIntent:intent}):undefined;
    this.lastPlay = { itemId, local: session, localContext:captured };
    this.update({ playbackRun: this.snapshot.playbackRun + 1 });
    return this.startLocal(itemId, session, startPosition, intent, captured);
  }
  private async startLocal(itemId: string, make: () => PlaybackSession, startPosition: number | undefined, intent: "playing" | "paused", context?:PlaybackLocalStartContext): Promise<void> {
    if(!localStartCurrent(context))throw localStartCancelled();
    this.endedSeekPosition = undefined;
    this.leave();
    this.failedRecovery = { positionSeconds: startPosition, intent };
    const intentId = this.snapshot.intentId;
    const cancel=()=>{if(this.snapshot.intentId===intentId)this.leave();};
    const check=()=>{
      if(this.snapshot.intentId!==intentId||!localStartCurrent(context)){
        cancel();throw localStartCancelled();
      }
    };
    this.update({ phase: "starting", intent, itemId, error: undefined });
    let detach:()=>void=()=>{},base:PlaybackSession|undefined;
    try {
      check();
      // Recovery belongs to the service, so a canceled row stops waiting without canceling
      // the shared journal read or losing cleanup for an earlier accepted session.
      const recovery=this.startRecovery();
      if(context?.signal){
        const signal=context.signal;
        await new Promise<void>((resolve,reject)=>{
          const abort=()=>{cancel();reject(localStartCancelled());};
          detach=()=>signal.removeEventListener('abort',abort);
          signal.addEventListener('abort',abort,{once:true});
          if(signal.aborted)abort();
          recovery.then(resolve,reject);
        });
      }else await recovery;
      check();
      this.sequence = 0;
      this.lastProgress = 0;
      let session:PlaybackSession;
      try {
        const localOwner=this.api.localPlayback;
        base=make();
        if(base.mode==='local'&&base.protocol===undefined){
          // Capture custody before checking cancellation: even a late maker's actual local
          // resource must retire through its local owner, never a server session route.
          if(this.cleanup.has(base.id)||this.progressPending.has(base.id)||this.localSessions.has(base.id))throw new Error('local_session_identity_reused');
          this.localSessions.set(base.id,localOwner);
        }
        check();
        session={...base,resumeSeconds:startPosition??base.resumeSeconds};
        check();
      } catch {
        // A maker can synchronously retire its caller or start a replacement. Neither a
        // late return nor an exception from that old maker may become the new UI's error.
        check();
        if(base&&this.localSessions.has(base.id)&&this.snapshot.session?.id!==base.id)this.enqueueCleanup(base.id);
        this.clearStartup();
        this.update({phase:"error",intent:"paused",error:serviceText('playback.message.internal_error')});
        return;
      }
      this.armStartup(() => {
        if (this.snapshot.intentId !== intentId) return;
        const session = this.snapshot.session;
        this.clearSeek();
        this.update({ audio: this.audioSelection.reset(), pendingSeek: undefined, failedSeek: undefined, phase: "error", intent: "paused", session: undefined, error: serviceText('playback.message.timeout') });
        if (session) this.enqueueCleanup(session.id);
      });
      check();
      this.requestSeek(session.resumeSeconds, { session, duration: session.duration });
      check();
      this.persist();
    } catch(error) {
      if(base&&this.snapshot.session?.id!==base.id)this.enqueueCleanup(base.id);
      // Existing three-argument callers quietly finish when another Play replaces them.
      if(!context&&this.snapshot.intentId!==intentId&&error instanceof Error&&error.name==='AbortError')return;
      throw error;
    } finally {detach();}
  }
  private startupProbe?: () => Readonly<Record<string, unknown>>;
  /** Development aid (demo, 23 Sep: a start after a Metro reload never produced a frame): the
   * engine adapter says what a start is waiting on (route ready, load command sent, native ready),
   * logged in development builds when the startup timer fails a start. */
  setStartupProbe(probe: (() => Readonly<Record<string, unknown>>) | undefined) { this.startupProbe = probe; }
  private reportStalledStart(intentId: number) {
    if (!this.options.diagnostics || intentId !== this.snapshot.intentId) return;
    const s = this.snapshot;
    let engine: unknown;
    try { engine = this.startupProbe?.(); } catch (e) { engine = String(e); }
    console.warn('[playback] start timed out', JSON.stringify({session: s.session?.id, protocol: s.session?.protocol, phase: s.phase, intent: s.intent, pendingSeek: s.pendingSeek?.positionSeconds, engine}));
  }
  /** Records a marker skip (spec §4.2) against the playing session; the caller seeks. Evidence
   * only: a failure is dropped. `automatic` is for the viewer's `auto` preference on a marker the
   * server marks safe. */
  markerSkipped(markerId: string, mode: 'automatic' | 'manual', positionSeconds: number = this.snapshot.positionSeconds): void {
    const session = this.snapshot.session;
    if (!session || this.localSessions.has(session.id) || this.snapshot.channel || !markerId) return;
    void this.api.markerSkipped?.(session.id, {markerId, mode, positionMs: Math.max(0, Math.round(positionSeconds * 1000))}).catch(() => {});
  }
  /** Where a retry of the current title starts: the target of a start or seek that failed or
   * never settled (a resume point included), else the last observed position. A title that never
   * played retries at its requested start, never at 0:00. */
  recoveryTarget(): number|undefined {
    const s=this.snapshot;
    return this.failedRecovery?.positionSeconds??s.pendingSeek?.positionSeconds??s.failedSeek?.positionSeconds??(this.hasObserved?s.positionSeconds:s.session?.resumeSeconds);
  }
  /** The intent before the current failure paused its error surface. */
  recoveryIntent(): "playing" | "paused" { return this.failedRecovery?.intent ?? this.snapshot.intent; }
  /** Error retry preserves the requested target and intent; ordinary Play starts playing. */
  retry(): Promise<void> {
    if(this.snapshot.channel){
      if(this.snapshot.phase==='error'&&this.channelReference)return this.playChannel(this.channelReference);
      this.channels?.retry();this.update({error:undefined});return Promise.resolve();
    }
    if (this.snapshot.phase !== "error" || !this.snapshot.itemId) return Promise.resolve();
    const recovery = this.failedRecovery ?? { intent: this.snapshot.intent };
    if(this.nativeV1)return this.nativeV1.action('retry',recovery.positionSeconds??this.snapshot.positionSeconds);
    // A local title retries locally; it never belonged to the device queue or the server.
    if(this.lastPlay?.local&&this.lastPlay.itemId===this.snapshot.itemId)return this.startLocal(this.snapshot.itemId, this.lastPlay.local, recovery.positionSeconds??this.snapshot.positionSeconds, recovery.intent, this.lastPlay.localContext);
    if(this.queue||this.options.queueRequired){return this.queue?.check(true)??Promise.resolve();}
    return this.startPlayback(this.snapshot.itemId, recovery.positionSeconds, recovery.intent);
  }
  private async startPlayback(itemId: string, startPosition: number | undefined, intent: "playing" | "paused"): Promise<void> {
    this.endedSeekPosition = undefined;
    this.leave();
    // Preserve the command before any journal, cleanup, create or readiness failure.
    this.failedRecovery = { positionSeconds: startPosition, intent };
    const intentId = this.snapshot.intentId;
    this.update({
      phase: "starting",
      intent,
      itemId,
      error: undefined,
    });
    await this.startRecovery();
    if (this.snapshot.intentId !== intentId) return;
    if (
      this.cleanup.size + this.creates.size >=
        (this.options.maxPendingCleanup ?? 32) ||
      this.snapshot.cleanupError
    ) {
      this.update({
        phase: "error",
        intent: "paused",
        error:
          "Playback cleanup needs to reconnect before another session can start.",
      });
      return;
    }
    this.sequence = 0;
    this.lastProgress = 0;
    let expired = false;
    this.armStartup(() => {
      expired = true;
      if (this.snapshot.intentId === intentId) {
        const session = this.snapshot.session;
        this.clearSeek();
        this.update({
          audio: this.audioSelection.reset(),
          pendingSeek: undefined,
          failedSeek: undefined,
          phase: "error",
          intent: "paused",
          session: undefined,
          error:
            "Playback did not become ready. Check the server and try again.",
        });
        if (session) this.enqueueCleanup(session.id);
      }
    });
    // Persist the request receipt before sending so ambiguous creates can be replayed.
    const requestId = this.requestId();
    this.creates.set(requestId, { itemId, running: true });
    await this.persist();
    if (this.snapshot.cleanupError) {
      this.clearStartup();
      this.update({
        phase: "error",
        intent: "paused",
        error: this.snapshot.cleanupError,
      });
      return;
    }
    if (this.snapshot.intentId !== intentId || expired) {
      this.creates.delete(requestId);
      void this.persist();
      return;
    }
    try {
      const receivedSession = await this.bounded((signal) =>
        this.api.createPlayback(itemId, requestId, signal).then((value) => {
          if (this.snapshot.intentId !== intentId || expired) {
            this.creates.delete(requestId);
            this.enqueueCleanup(value.id);
          }
          return value;
        })
      );
      const session = {
        ...receivedSession,
        // The server owns completion/resume policy, including near-end audiobook
        // bookmarks. Only an explicit caller position (including zero) overrides it.
        resumeSeconds: startPosition ?? receivedSession.resumeSeconds,
      };
      this.creates.delete(requestId);
      if (this.snapshot.intentId !== intentId || expired) {
        this.enqueueCleanup(session.id);
        return;
      }
      this.requestSeek(session.resumeSeconds, {session, duration: session.duration});
      this.persist();
    } catch (error) {
      const pending = this.creates.get(requestId);
      if (pending) pending.running = false;
      if (
        error instanceof ApiError &&
        [400, 401, 403, 404, 409, 422].includes(error.status)
      )
        this.creates.delete(requestId);
      this.persist();
      if (this.snapshot.intentId === intentId && !expired) {
        this.clearStartup();
        this.update({
          phase: "error",
          intent: "paused",
          error:
            error instanceof Error
              ? error.message
              : "Playback could not start.",
        });
      }
    }
  }
  pause() {
    this.nativeResumeRevision++;
    this.queue?.invalidateAudioPreparation?.();
    if(this.nativeV1){void this.nativeV1.action('pause').catch(()=>{});if(this.snapshot.phase!=='idle')this.update({intent:'paused'});return;}
    if(this.snapshot.channel){this.channelWantedState="paused";this.update({intent:"paused"});this.channels?.pause();return;}
    if (this.snapshot.phase !== "idle") {
      if (this.failedRecovery) this.failedRecovery = { ...this.failedRecovery, intent: "paused" };
      if (this.snapshot.phase === "ready") this.clearStartup();
      this.update({ intent: "paused" });
      this.checkpoint("paused");
    }
  }
  resume() {
    this.update({listeningNotice:undefined});
    this.checkListeningDeadlines();
    if(this.nativeV1){
      const native=this.nativeV1,revision=++this.nativeResumeRevision;
      void (this.nativePolicyKey?Promise.resolve(true):this.syncListeningPolicy()).then(ok=>{
        if(ok&&this.nativeV1===native&&revision===this.nativeResumeRevision)return native.action('resume');
      }).catch(()=>{});return;
    }
    if(this.snapshot.channel){
      if(this.snapshot.seekError||this.snapshot.failedSeek){this.update({error:'Choose Go Live or a position in the retained window before resuming.'});return;}
      this.channelWantedState="playing";this.channels?.resume();return;
    }

    if (this.snapshot.phase === "ended" && this.snapshot.itemId) {
      // Replay: the same title again with the choices it was playing with.
      const last = this.lastPlay?.itemId === this.snapshot.itemId && !this.lastPlay.local ? this.lastPlay : undefined;
      void this.play(this.snapshot.itemId, this.endedSeekPosition ?? 0, last?.prepared, last?.quality, last?.audioStream, last?.choices);
      return;
    }
    if (this.snapshot.phase === "starting" || this.snapshot.phase === "ready") {
      if (this.failedRecovery) this.failedRecovery = { ...this.failedRecovery, intent: "playing" };
      if ((this.bookmarkProtected || this.snapshot.failedSeek) && this.snapshot.session && !this.snapshot.pendingSeek) {
        this.requestSeek(this.snapshot.failedSeek?.positionSeconds ?? this.snapshot.session.resumeSeconds, {intent: "playing"});
        return;
      }
      this.update({ intent: "playing" });
    }
  }
  private clearSeek() {
    this.seekWait?.();
    this.seekWait = undefined;
    this.seekRearm = undefined;
  }
  private requestSeek(positionSeconds: number, patch: Partial<PlaybackSnapshot> = {}) {
    this.clearSeek();
    const duration = patch.duration ?? this.snapshot.duration;
    const target = Math.max(0, Math.min(positionSeconds, duration || positionSeconds));
    this.failedRecovery = { positionSeconds: target, intent: patch.intent ?? this.snapshot.intent };
    const revision = this.snapshot.seekRevision + 1, intentId = this.snapshot.intentId;
    const deadline = this.visibleDeadline(this.options.seekTimeoutMs ?? 15000, () => this.seekFailed(intentId, revision, "That position is not available to seek yet. Try again."));
    this.seekWait = deadline.clear;
    this.seekRearm = deadline.rearm;
    this.update({...patch, seekRevision: revision, pendingSeek: Object.freeze({revision, positionSeconds: target}), seekError: undefined, failedSeek: undefined});
  }
  seek(positionSeconds: number) {
    if(this.queue?.seekRetiringAudio?.(positionSeconds))return;
    this.queue?.invalidateAudioPreparation?.();
    if(this.snapshot.channel){this.channels?.seek(positionSeconds);return;}
    if (!this.snapshot.session || !Number.isFinite(positionSeconds) || !["starting", "ready", "ended"].includes(this.snapshot.phase)) return;
    if (this.snapshot.phase === "ended") {
      this.endedSeekPosition = Math.max(0, Math.min(positionSeconds, this.snapshot.duration || positionSeconds));
      return;
    }
    if(this.nativeV1){void this.nativeV1.action('seek',positionSeconds).catch(()=>{});return;}
    this.requestSeek(positionSeconds);
  }
  /** Only a correlated adapter completion acknowledges a seek; ordinary time facts do not. */
  seekApplied(intentId: number, revision: number, observedSeconds: number, state: "playing" | "paused" | "ended"): boolean {
    const pending = this.snapshot.pendingSeek;
    if(this.snapshot.channel){
      if(intentId!==this.snapshot.intentId||!pending||pending.revision!==revision||!Number.isFinite(observedSeconds)||Math.abs(observedSeconds-pending.positionSeconds)>.5||!this.channelSeekId)return false;
      if(this.channelSubmittedSeek!==this.channelSeekId){const seekId=this.channelSeekId;this.channelSubmittedSeek=seekId;void this.channels?.observe(observedSeconds,seekId).then(ok=>{if(!ok&&this.snapshot.intentId===intentId&&this.channelSubmittedSeek===seekId)this.channelSubmittedSeek=undefined;});}
      return true;
    }
    if (intentId !== this.snapshot.intentId || !this.snapshot.session || !pending || pending.revision !== revision || !["starting", "ready"].includes(this.snapshot.phase) || !Number.isFinite(observedSeconds) || observedSeconds < 0 || this.snapshot.duration > 0 && observedSeconds > this.snapshot.duration || Math.abs(observedSeconds-pending.positionSeconds) > 0.5 || !["playing", "paused", "ended"].includes(state)) return false;
    this.clearSeek();
    this.hasObserved = true;
    this.bookmarkProtected = false;
    this.update({positionSeconds: observedSeconds, pendingSeek: undefined, seekError: undefined, failedSeek: undefined});
    this.lastProgress = observedSeconds;
    this.checkpoint(state);
    return true;
  }
  /** Reports that media has stopped arriving and the buffer is empty (or that it is flowing
   * again). Anything short of that is the adapter's business and is never shown. */
  recovering(intentId: number, active: boolean) {
    if (intentId !== this.snapshot.intentId || this.snapshot.phase === "error" || this.snapshot.phase === "idle") return;
    if (!!this.snapshot.recovering !== active) this.update({ recovering: active || undefined });
  }
  seekFailed(intentId: number, revision: number, message: string) {
    if (intentId !== this.snapshot.intentId || !this.snapshot.session || this.snapshot.pendingSeek?.revision !== revision) return;
    this.clearSeek();
    if(this.snapshot.channel)void this.channels?.observe(this.snapshot.positionSeconds,null,'engine_seek_failed');
    const safe = typeof message === "string" && message.trim() && message.length <= 512 && !/[\x00-\x1f\x7f]/.test(message) ? message : "That position could not be reached. Try again.";
    this.update({failedSeek: this.snapshot.pendingSeek, pendingSeek: undefined, seekError: safe, intent: "paused" as const});
  }
  /** A lost engine restores an outstanding seek first, then the last settled observation. */
  engineLost(intentId: number) {
    if(this.snapshot.channel){
      if(intentId===this.snapshot.intentId&&this.snapshot.session&&this.channelObservedSeek)this.channels?.seek(this.snapshot.positionSeconds);
      return;
    }
    if (intentId !== this.snapshot.intentId || !this.snapshot.session || !["starting", "ready"].includes(this.snapshot.phase)) return;
    const target = this.snapshot.pendingSeek?.positionSeconds ?? (this.hasObserved ? this.snapshot.positionSeconds : this.snapshot.session.resumeSeconds);
    this.bookmarkProtected = true;
    this.requestSeek(target);
  }
  ready(intentId: number) {
    if (
      intentId === this.snapshot.intentId &&
      this.snapshot.session &&
      this.snapshot.phase === "starting"
    ) {
      this.clearStartup();
      this.update({ phase: "ready" });
    }
  }
  fact(
    intentId: number,
    positionSeconds: number,
    state: "playing" | "paused" | "ended"
  ) {
    if(this.snapshot.channel){
      if(intentId!==this.snapshot.intentId||!this.snapshot.session||this.snapshot.pendingSeek||this.snapshot.failedSeek||!Number.isFinite(positionSeconds))return;
      if(state==='ended'){this.update({error:'The channel stream ended. Retry or choose Go Live.'});return;}
      this.update({positionSeconds});void this.channels?.observe(positionSeconds);return;
    }
    if (
      intentId !== this.snapshot.intentId ||
      !this.snapshot.session ||
      !!this.snapshot.pendingSeek ||
      !!this.snapshot.failedSeek ||
      !["starting", "ready"].includes(this.snapshot.phase) ||
      !Number.isFinite(positionSeconds) ||
      positionSeconds < 0
    )
      return;
    if (
      state === "playing" &&
      positionSeconds > this.snapshot.session.resumeSeconds + 0.01
    )
      this.clearStartup();
    this.hasObserved = true;
    // An element that has already reported its end keeps reporting it on every
    // later event. Only the first report is a transition; later ones must not
    // pause an intent the queue has since set to playing for the next item.
    if (state === "ended" && this.snapshot.phase === "ended") return;
    if (state === "ended") this.clearSeek();
    this.update({
      positionSeconds,
      ...(state === "ended"
        ? { phase: "ended" as const, intent: "paused" as const, pendingSeek: undefined, audio: this.audioSelection.reset() }
        : {}),
    });
    if (
      state !== "playing" ||
      Math.abs(positionSeconds - this.lastProgress) >= 3
    ) {
      this.lastProgress = positionSeconds;
      this.checkpoint(state);
    }
  }
  /** The engine rejected the stream it was given: a decode error, an unsupported
   * source, a manifest it cannot read. Not the network, and not the server saying
   * no. A capability document can be wrong, so this is not yet a failure the
   * viewer should see: the server is told which route failed, and when there is a
   * route above it the title is asked for again from the same position and plays
   * through that one instead. The viewer sees the recovery state, never an error,
   * unless the last resort has failed too. */
  engineFailed(intentId: number, code: PlayerFailureCode, detail: string, message: string) {
    const session = this.snapshot.session, itemId = this.snapshot.itemId;
    if (intentId !== this.snapshot.intentId || this.snapshot.phase === "error" || this.snapshot.phase === "idle") return;
    // The recovery belongs to the session being played, however it was started (a direct play, a
    // queue's first entry, or a queue's successor): the server re-plans that session in place.
    // Only a server session can be re-planned (a download or a prepared file is what it is). The
    // bound is per session: a re-plan keeps the session, a new title starts a new count.
    if (session && this.escalationSession !== session.id) { this.escalationSession = session.id; this.escalations = 0; }
    if (!session || !itemId || this.snapshot.channel || !this.api.reportPlayerFailure || session.protocol !== 'v1' || this.escalations >= 3) {
      this.fail(intentId, message);
      return;
    }
    const position = this.snapshot.pendingSeek?.positionSeconds ?? (this.hasObserved ? this.snapshot.positionSeconds : session.resumeSeconds ?? 0);
    this.escalations++;
    this.recovering(intentId, true);
    void this.api.reportPlayerFailure(session.id, code, detail, position).then(async result => {
      if (intentId !== this.snapshot.intentId) return;
      if (!result?.escalates || !result.session) { this.fail(intentId, message); return; }
      // The same session, another way: its new presentation replaces the old one in place, so
      // the queue and the position stay.
      const next = {...result.session, duration: result.session.duration || session.duration, resumeSeconds: position};
      if (!this.installPresentation(next)) await this.adoptQueueSession(itemId, next, intentId);
    }).catch(() => { if (intentId === this.snapshot.intentId) this.fail(intentId, message); });
  }
  fail(intentId: number, message: string, extra: Partial<PlaybackSnapshot> = {}) {
    if (intentId === this.snapshot.intentId && this.snapshot.phase !== "idle") {
      if (this.snapshot.phase !== "error") {
        this.failedRecovery = {
          positionSeconds: this.snapshot.pendingSeek?.positionSeconds ?? this.snapshot.failedSeek?.positionSeconds ?? (this.hasObserved ? this.snapshot.positionSeconds : this.snapshot.session?.resumeSeconds ?? this.failedRecovery?.positionSeconds),
          intent: this.snapshot.intent,
        };
      }
      this.clearStartup();
      this.clearSeek();
      this.update({ audio: this.audioSelection.reset(), phase: "error", intent: "paused", error: message, pendingSeek: undefined, seekError: undefined, failedSeek: undefined, recovering: undefined, ...extra });
    }
  }
  leave(notifyQueue=true,preserveListeningDeadlines=false) {
    this.nativeResumeRevision++;this.nativePolicyRevision++;this.nativePolicyKey='';this.nativePolicyPending=undefined;
    if(!preserveListeningDeadlines)this.listeningClock.clear();
    if(notifyQueue)this.queue?.leave();
    const wasChannel=!!this.snapshot.channel;
    // A native v1 owner reports and stops its own session; nothing here writes for it.
    const native=this.nativeV1;this.nativeV1=undefined;
    if(native)void native.detach(true).catch(()=>{});
    this.channels?.close();this.channelReference=undefined;this.channelSeekId=undefined;this.channelObservedSeek=undefined;this.channelSubmittedSeek=undefined;
    this.failedRecovery = undefined;
    this.clearStartup();
    this.clearSeek();
    if(!native)this.checkpoint("paused");
    this.hasObserved = false;
    this.bookmarkProtected = true;
    const old = this.snapshot.session;
    this.update({
      audioEffects:{...this.snapshot.audioEffects,rendering:false,eqApplied:false,levelingApplied:false,levelingGainDb:undefined,effectiveRate:0,waiting:false,prepared:undefined,committed:undefined},
      audio: this.audioSelection.reset(),
      audioTracks: undefined,
      intentId: this.snapshot.intentId + 1,
      phase: "idle",
      intent: "paused",
      itemId: preserveListeningDeadlines?this.snapshot.itemId:undefined,
      nativeListening:undefined,
      listeningDeadlines:preserveListeningDeadlines?this.snapshot.listeningDeadlines:noListeningDeadlines,
      channel: undefined,
      linear: undefined,
      session: undefined,
      pendingSeek: undefined,
      seekError: undefined,
      failedSeek: undefined,
      recovering: undefined,
      positionSeconds: 0,
      duration: 0,
      error: undefined,
    });
    if (old&&!wasChannel&&!native) this.enqueueCleanup(old.id);
  }
  private checkpoint(state: "playing" | "paused" | "ended") {
    if(this.snapshot.channel||this.nativeV1)return;
    const session = this.snapshot.session;
    if (!session || !this.hasObserved || this.bookmarkProtected) return;
    if(this.localSessions.has(session.id)&&!this.localSessions.get(session.id))return;
    this.progressPending.set(session.id, {
      generation: session.generation,
      sequence: ++this.sequence,
      positionSeconds: this.snapshot.positionSeconds,
      state,
    });
    this.persist();
    this.flushProgress();
  }
  private async bounded<T>(
    work: (signal: AbortSignal) => Promise<T>
  ): Promise<T> {
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout> | undefined;
    try {
      return await Promise.race([
        work(controller.signal),
        new Promise<T>((_, reject) => {
          timer = setTimeout(() => {
            controller.abort();
            reject(new Error("Operation timed out"));
          }, this.options.operationTimeoutMs ?? 5000);
        }),
      ]);
    } finally {
      if (timer) clearTimeout(timer);
    }
  }
  private flushProgress() {
    for (const [id, payload] of this.progressPending) {
      if (this.progressRunning.has(id) || this.cleanup.has(id)) continue;
      const api=this.localSessions.has(id)?this.localSessions.get(id):this.api;
      if(!api)continue;
      this.progressRunning.add(id);
      void this.bounded(() => api.progressPlayback(id, payload)).then(
        () => {
          if (this.progressPending.get(id) === payload)
            this.progressPending.delete(id);
          this.progressRunning.delete(id);
          this.persist();
          if (
            this.progressPending.get(id) &&
            this.progressPending.get(id) !== payload
          )
            this.flushProgress();
        },
        () => {
          this.progressRunning.delete(id);
          this.persist();
        }
      );
    }
  }
  private enqueueCleanup(id: string) {
    if (!this.cleanup.has(id))
      this.cleanup.set(id, {
        running: false,
        checkpoint: this.progressPending.get(id),
      });
    this.update({});
    void this.persist().then(() => this.tryCleanup(id)).catch(() => {});
  }
  private tryCleanup(id: string): Promise<void> {
    const entry = this.cleanup.get(id);
    if (!entry) return Promise.resolve();
    if (entry.work) return entry.work;
    entry.running = true;
    entry.work = (async () => {
      try {
        const api=this.localSessions.has(id)?this.localSessions.get(id):this.api;
        const checkpoint = this.progressPending.get(id) ?? entry.checkpoint;
        if (checkpoint&&api) {
          try {
            await this.bounded(() => api.progressPlayback(id, checkpoint));
          } catch (error) {
            if (![404, 409, 410].includes(statusOf(error))) throw error;
          }
        }
        try {
          if(api)await this.bounded((signal) => api.stopPlayback(id, signal));
        } catch (error) {
          // Already ended (it played to its end) or already gone: there is nothing left to stop.
          if (![404, 410].includes(statusOf(error))) throw error;
        }
        this.cleanup.delete(id);
        this.progressPending.delete(id);
        this.localSessions.delete(id);
        this.update({});
        await this.persist();
      } catch (error) {
        entry.running = false;
        entry.work = undefined;
        this.update({});
        throw error;
      }
    })();
    // Background leave/retry callers do not await, but an explicit cross-kind
    // handoff awaits the same work and surfaces failure without a second player.
    void entry.work.catch(() => {});
    return entry.work;
  }
  retryCleanup() {
    for (const id of this.cleanup.keys()) this.tryCleanup(id);
    for (const [requestId, entry] of this.creates) {
      if (entry.running) continue;
      entry.running = true;
      void this.bounded((signal) =>
        this.api.createPlayback(entry.itemId, requestId, signal)
      ).then(
        (session) => {
          this.creates.delete(requestId);
          this.enqueueCleanup(session.id);
        },
        () => {
          entry.running = false;
        }
      );
    }
    this.flushProgress();
  }
  private persist(): Promise<void> {
    if (!this.options.journal) return Promise.resolve();
    const records: PlaybackJournalEntry[] = [...this.cleanup].filter(([id])=>!this.localSessions.has(id)).map(
      ([id, value]) => ({
        id,
        checkpoint: this.progressPending.get(id) ?? value.checkpoint,
      })
    );
    for (const [requestId, entry] of this.creates)
      records.push({ request: { requestId, itemId: entry.itemId } });
    // A native v1 owner's session is its own to stop (it holds the timeline and the lease).
    const active = this.nativeV1 ? undefined : this.snapshot.session;
    if (active && !this.localSessions.has(active.id) && !records.some((v) => v.id === active.id))
      records.push({
        id: active.id,
        checkpoint: this.progressPending.get(active.id),
      });
    this.journalTail = this.journalTail
      .then(() => this.options.journal!.save(records))
      .catch(() => {
        this.update({
          cleanupError:
            "Playback recovery could not be saved. Check secure storage.",
        });
      });
    return this.journalTail;
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
export {
  CatalogService,
  type CatalogSnapshot,
  type CatalogApi,
} from "./catalog.ts";

export { DeviceAuthorizationService, HttpDeviceAuthorizationApi, type DeviceAuthorization, type DeviceAuthorizationApi, type DeviceAuthorizationSnapshot } from "./device-authorization.ts";

export { HostedGate, hostedGate, type HostedGateSnapshot, type HostedRequestKind } from "./hosted-gate.ts";
export { CredentialSessionService,HttpCredentialApi,type CredentialContext,type CredentialRecord,type CredentialStorage,type CredentialApi,type CredentialSnapshot,type CredentialCoordinator } from "./credentials.ts";
export {LibraryNavigationService,navigationStorageKey,type NavigationScope,type LibraryRoute,type LibraryEntityView,type EntityViewState,type NavigationFocus,type ScrollAnchor,type LibraryNavigationMetadata,type LibraryViewState,type LibraryViewUpdate,type NavigationStorage,type LibraryNavigationSnapshot} from './library-navigation.ts';
export {readListeningSimilar,LibraryContentService,type ContentView,type ContentSurface,type ContentRoute,type ContentScope,type ContentReadContext,type LibraryContentApi,type ContentEntryKind,type ContentHeading,type ContentEntry,type ContentSection,type ContentProjection,type LibraryContentSnapshot} from './library-content.ts';

export * from './hosted-security.ts';
export {DetailService,type DetailScope,type DetailTarget,type DetailApi,type PersonalState,type PersonalOfflineMutation,type PersonalConflict,type DetailAction,type DetailMetadata,type DetailEpisodeInfo,type DetailSongInfo,type DetailBookFileInfo,type DetailItem,type DetailProjection,type PersonalIntent,type DetailError,type DetailSnapshot} from './detail.ts';
export * from "./personal-saved.ts";
export {SearchService,searchPath,parseSearchHistory,type SearchGroup,type SearchSort,type SearchDirection,type SearchGroupStatus,type SearchGroupErrorCode,type SearchQuery,type SearchEntry,type SearchGroupResult,type SearchGroupCapability,type SearchCapabilities,type SearchData,type SearchSnapshot,type SearchHistoryEntry,type SearchHistoryPage} from './search.ts';
export {readSearchGenres, releaseSearchGenres, searchGenresReadMetadata, searchGenresPath, parseSearchGenres, parseSearchGenrePreparing, type SearchGenreArt, type SearchGenre, type SearchGenrePage, type SearchGenreRequest, type SearchGenreReadOptions} from './search-genres.ts';
export * from './facet-windows.ts';
export * from './people.ts';
export {savedListPath,personalHistoryPath,savedListFilters,savedListSorts,historyPeriods,type SavedListFilter,type SavedListSort,type HistoryPeriod,type SavedApi,type SavedScope,type SavedRoute,type SavedActor} from './saved.ts';
export * from './browse.ts';
export * from './browse-buckets.ts';
export * from './browse-whole-selection.ts';
export * from './social-playback.ts';
export * from './group-session.ts';
export * from './remote-playback.ts';
export * from './library-pins.ts';
export * from './subtitles.ts';
export * from './subtitle-originals.ts';
export * from "./lyrics.ts";

export {type MusicPolicy,type MusicPolicyInput,type MusicMatchObservation,type MusicEvidenceDetails,type LocalBookMetadata,musicEvidenceLabel,fingerprintStatusLabel} from "./music-metadata.ts";

export {saveLocalBookPolicy,parseLocalAudioPolicy,type LocalAudioPolicy} from './music-metadata.ts';
export {MetadataRepairService,validateRepair} from "./metadata-repair.ts";
export type {RepairTarget,RepairScope,RepairData,RepairCommand,RepairView,RepairPreview,RepairField,RepairFieldSpec} from "./metadata-repair.ts";
export {validBulkEdit,validBulkListEntry,validateBulkReceipt,bulkConflicts,bulkTargetLimit} from './metadata-bulk.ts';
export type {BulkEdit,BulkTarget,BulkListEdit,BulkFieldEdit,BulkResult,BulkReceipt} from './metadata-bulk.ts';
export * from './prepared-media.ts';

export * from './trickplay.ts';
export {PlayerChaptersService} from './player-chapters.ts';
export type {PlayerChapter,PlayerChaptersData,PlayerChaptersTarget,PlayerChaptersSnapshot} from './player-chapters.ts';
export {PlayerSessionChoices,playerSessionChoices} from './player-session-choices.ts';
export * from './listening.ts';
export * from './preferences.ts';
export * from './preference-effects.ts';
export {parseSegmentMarkers,parseSegmentMarkerSet,canSkipAutomatically,type SegmentMarker,type SegmentMarkerKind,type SegmentMarkerSet} from './segment-markers.ts';
export {activeSegment,skipsAutomatically,skipTarget,followedByScene,closingKind,SKIP_LEAD_SECONDS,UNDO_SECONDS,type SkipMode} from './segment-skip.ts';
export {parseQueuePostPlay,passoutCheckRequired,type QueuePostPlay,type QueueAdvanceMode} from './post-play.ts';
export * from './home.ts';
export * from './play-history.ts';
export * from './recommendations.ts';
export * from './relation-rows.ts';
export * from './notifications.ts';
export * from './notification-scope.ts';
export {detailExtraTypes,type DetailExtra,type DetailExtraType,parseCreditPage,type CreditPage,type DetailCredit} from './detail.ts';

export * from './downloads.ts';
export * from './bulk-jobs.ts';
export * from './container-state.ts';
export * from './download-requests.ts';
export * from './inbox.ts';
export * from './identity-client.ts';
export * from './sse.ts';
export * from './downloads-service.ts';

export * from './client-reports.ts';
export * from './live-handover.ts';

export * from './metadata-attention.ts';
export * from './metadata-settings.ts';

export {parseFreshnessChange, subscribeFreshness, type FreshnessCounters, type FreshnessChange, type CatalogueFailure} from './freshness.ts';
export * from './freshness-hub.ts';
export * from './freshness-pool.ts';
export * from './freshness-actions.ts';
export * from './event-feed.ts';
export * from './home-service.ts';
export * from './home-layout-editor.ts';
export * from './scan-issues.ts';
export * from './playback-controls.ts';
export {sizeClassForWidth, type SizeClass} from './presentation/size-class.ts';

export {readLibrarySummaries,parseLibrarySummary} from './library-summary.ts';
export type {LibrarySummary} from './library-summary.ts';

export {browseServerFilesystem} from './library-management.ts';
export {libraryNavigationIcon} from './presentation/library-navigation.ts';


export {PeopleInvitationsClient,parsePublicInvitation,privatePersonNotes,personIdentity} from './direct-account.ts';
export type {ServerInvitation,ServerShortInvitation,PeopleMember,PublicInvitation} from './direct-account.ts';
export {highestRatingChoices} from './profile-limits.ts';

export * from './account-reset.ts';
export * from './same-tab-return.ts';
export {buildHostedSignInStartURL, parseHostedSignInStartURL} from './hosted-sign-in-start.ts';

export * from './report-context.ts';
export {AppSessionStore, parseAppSessionRecord, type AppSessionRecord, type AppSessionStorage, type AppServerSession, type NewServerConnection, type PorticoAccountScope, type SignInKind} from './app-sessions.ts';
export {ServerSignInProbe, parseServerSignIn, negotiateServer, CLIENT_API, APP_UPDATE_REQUIRED, type ServerAPI, API_LEVEL_MISMATCH, moveOfferDays, signInLabelKey, serverSignInSettingsURL, accountSignInKeys, type ServerSignIn, type ServerSignInSnapshot} from './sign-in.ts';
export * from './account-move.ts';
export {TVConsentApprover, type TVConsentAuthority, type TVConsentTransport} from './tv-consent.ts';

export {videoPictureName, videoAudioFormatName, videoTrackName, videoTrackWords, formatVideoBytes} from './video-track-labels.ts';
export type {VideoTrackWords, VideoTrackNameParts} from './video-track-labels.ts';

export {createReadingHost, connectReadingHost} from './reading-host.ts';
export type {ReadingHostSession, ReadingHostTransport, ReadingGuest, ReadingHostContentsTarget, ReadingHostCrease, ReadingHostSearchAll} from './reading-host.ts';
export {savedViewSummary, type SavedViewSummary, type SavedViewSummaryText} from './presentation/saved-view-summary.ts';
export {readLibrarySettings,saveLibrarySettings,reorderLibraries,saveLibraryFolder,removeLibraryFolder} from './server-admin/libraries.ts';
export {readLibraryInventoryConfig} from './library-inventory.ts';
export {setLibraryAnalysis,setLibraryScanDepth,libraryScanDepth,setLibrarySeekPreviews,setLibraryPreviewHours} from './server-admin/library-policy.ts';
export type {LibraryAdminDocument,LibrarySettings} from './administration.ts';
export {readLibrarySonic,saveLibrarySonic,retryLibrarySonic,librarySonicAvailable,type LibrarySonicDocument,type LibrarySonicState} from './server-admin/library-sonic.ts';
export {librarySonicStatus,type LibrarySonicStatusLine} from './server-admin/library-sonic-words.ts';

export {parsePlaybackInfo} from './playback-v1/playback-info.ts';
export type {PlaybackInfo, PlaybackRoute, PlaybackPictureFacts, PlaybackSoundFacts, PlaybackSubtitleFacts, PlaybackMediaFacts, PlaybackReplacement} from './playback-v1/playback-info.ts';

export {playbackInfoProjection, reconciledPlaybackLosses} from './playback-info-projection.ts';
export type {PlaybackInfoWords, PlaybackComparisonProjection, PlaybackInfoRow, PlaybackInfoField} from './playback-info-projection.ts';
export {videoAudioChannelName} from './video-track-labels.ts';

export type {ReadingSavedGroupTarget, ReadingSavedGroupKind, ReadingSavedGroupMembership, ReadingSavedGroupCommand, SavedReadingGroupCard} from './reading-saved-group.ts';
