import {routePath} from '../../contracts/src/route-paths.ts';
import {ApiError, type SystemInfo} from './index.ts';
import {RouteError, routeOrigin, routeJSON} from './route-identity.ts';
import {API_LEVEL} from '../../contracts/src/api-level.ts';
import type {SignInKind} from './app-sessions.ts';

export type ServerSignIn = Readonly<SystemInfo & {signIn: SignInKind}>;
const object = (v: unknown): v is Record<string, unknown> => !!v && typeof v === 'object' && !Array.isArray(v);
const text = (v: unknown, max: number): v is string => typeof v === 'string' && v.length > 0 && v.length <= max && !/[\p{Cc}\p{Cf}]/u.test(v);
/** A missing or unknown kind never grants a password form. No claim/Hosted inference. */
export function parseServerSignIn(raw: unknown): ServerSignIn {
  if (!object(raw) || !text(raw.id, 256) || !text(raw.name, 120) || !text(raw.version, 256) ||
      !['portico', 'local'].includes(String(raw.signIn)) || raw.move!==null&&!['to_portico','to_local'].includes(String(raw.move)) || typeof raw.setupRequired !== 'boolean' ||
      typeof raw.hostedConfigured !== 'boolean' || typeof raw.hostedAttached !== 'boolean' || !Number.isSafeInteger(raw.apiLevel) || (raw.apiLevel as number) < 1)
    throw new RouteError('invalid_system', 'This server did not report how to sign in.');
  if(raw.move==='to_portico'&&raw.signIn!=='portico'||raw.move==='to_local'&&raw.signIn!=='local')throw new RouteError('invalid_system','The server reported a different sign-in move.');
  const moveEndsAt=raw.moveEndsAt===undefined||raw.moveEndsAt===null?null:typeof raw.moveEndsAt==='string'&&raw.moveEndsAt.length<=64&&raw.move==='to_portico'&&Number.isFinite(Date.parse(raw.moveEndsAt))?raw.moveEndsAt:undefined;
  if(moveEndsAt===undefined)throw new RouteError('invalid_system','The server reported an unreadable move window.');
  return Object.freeze({id: raw.id, name: raw.name, version: raw.version, signIn: raw.signIn as SignInKind,move:raw.move as ServerSignIn['move'],
    setupRequired: raw.setupRequired, hostedConfigured: raw.hostedConfigured, hostedAttached: raw.hostedAttached, apiLevel: raw.apiLevel as number, moveEndsAt,
    ...serverAPI(raw)});
}
/** O-159: the negotiation facts. A server without them, or with a shape this client can't read,
 * is pre-release: it carries none, and negotiation refuses it. Nothing is guessed from `apiLevel`. */
function serverAPI(raw: Record<string, unknown>) {
  const version = typeof raw.apiVersion === 'string' && /^\d{1,6}\.\d{1,6}$/.test(raw.apiVersion) ? raw.apiVersion : undefined;
  const majors = Array.isArray(raw.apiMajors) && raw.apiMajors.length > 0 && raw.apiMajors.length <= 64 && raw.apiMajors.every(value => Number.isSafeInteger(value) && value >= 1)
    ? [...new Set(raw.apiMajors as number[])].sort((a, b) => a - b) : undefined;
  const capabilities = Array.isArray(raw.capabilities) && raw.capabilities.length <= 1024 && raw.capabilities.every(value => text(value, 128))
    ? [...new Set(raw.capabilities as string[])].sort() : undefined;
  return version && majors && capabilities ? {apiVersion: version, apiMajors: Object.freeze(majors), capabilities: Object.freeze(capabilities)} : {};
}
/** Whole calendar days left to use the old sign-in during a move, counted on this device's
 * calendar: 0 when it ends today, 1 when it ends tomorrow. Undefined when nothing is offered. */
export function moveOfferDays(endsAt: string | null | undefined, now: Date = new Date()): number | undefined {
  const end = endsAt ? new Date(endsAt) : undefined;
  if (!end || !Number.isFinite(end.getTime()) || end.getTime() <= now.getTime()) return undefined;
  const day = (value: Date) => Date.UTC(value.getFullYear(), value.getMonth(), value.getDate());
  return Math.round((day(end) - day(now)) / 86_400_000);
}
/** Thrown before any sign-in when the server serves no API major this app speaks and is older. */
export const API_LEVEL_MISMATCH = 'api_level_mismatch';
/** Thrown before any sign-in when the server serves only majors newer than this app speaks. */
export const APP_UPDATE_REQUIRED = 'app_update_required';
/** The API majors this client speaks (O-159). A server is opened when the ranges overlap. */
export const CLIENT_API = Object.freeze({min: 1, max: API_LEVEL});
export type ServerAPI = Readonly<{major: number; version: string; capabilities: readonly string[]; can: (capability: string) => boolean}>;
/** O-159, Justin's rule: if a server and a client can talk, allow the connection. The refusals,
 * decided before credentials are sent or a session is stored, are a pre-release server (no
 * negotiation facts: it needs an update) and no shared API major. Features added after 1.0
 * are gated on `can(id)`, never on version numbers; the 1.0 baseline is never gated. */
export function negotiateServer(system: Pick<ServerSignIn, 'apiLevel' | 'apiVersion' | 'apiMajors' | 'capabilities'>, client: Readonly<{min: number; max: number}> = CLIENT_API): ServerAPI {
  if (!system.apiVersion || !system.apiMajors?.length || !system.capabilities) throw new RouteError(API_LEVEL_MISMATCH, 'This server needs an update to work with this app.');
  const majors = system.apiMajors;
  const shared = majors.filter(major => major >= client.min && major <= client.max);
  if (!shared.length) {
    if (Math.max(...majors) < client.min) throw new RouteError(API_LEVEL_MISMATCH, 'This server needs an update to work with this app.');
    throw new RouteError(APP_UPDATE_REQUIRED, 'This app needs an update to work with this server.');
  }
  const capabilities = Object.freeze([...system.capabilities]), known = new Set(capabilities);
  return Object.freeze({major: Math.max(...shared), version: system.apiVersion, capabilities, can: (capability: string) => known.has(capability)});
}
export const accountSignInKeys = Object.freeze({
  portico: 'accounts.signIn.portico', local: 'accounts.signIn.local', usesPortico: 'accounts.signIn.usesPortico',
  signedInPortico: 'accounts.session.portico', signedInLocal: 'accounts.session.local',
  signOutServer: 'accounts.signOut.server', signOutPortico: 'accounts.signOut.portico',
  profilePicker: 'accounts.profiles.onServer', consentPortico: 'accounts.consent.portico', consentLocal: 'accounts.consent.local',
} as const);
export function signInLabelKey(kind: SignInKind) {
  if (kind !== 'local' && kind !== 'portico') throw new RouteError('invalid_sign_in_kind', 'Choose a server sign-in kind.');
  return kind === 'portico' ? accountSignInKeys.signedInPortico : accountSignInKeys.signedInLocal;
}
/** Opens this server's actual Sign-in settings. No member secret or Hosted origin is carried. */
export function serverSignInSettingsURL(serverOrigin:string):string {
  return new URL('/settings?scope=server&section=signin',routeOrigin(serverOrigin)).href;
}
export type ServerSignInSnapshot = Readonly<{
  phase: 'idle' | 'probing' | 'ready' | 'error'; origin?: string; server?: ServerSignIn;
  destination?: 'portico-account' | 'local-account' | 'setup'; error?: unknown;
}>;
/** One anonymous bounded request to the explicitly chosen server. Construction, cancel and
 * local-only navigation never contact Portico Services. The platform supplies its HTTP policy. */
export class ServerSignInProbe {
  private state: ServerSignInSnapshot = Object.freeze({phase: 'idle'});
  private listeners = new Set<() => void>();
  private generation = 0;
  private controller?: AbortController;
  private disposed = false;
  private readonly fetcher: typeof fetch; private readonly timeoutMs: number;
  constructor(fetcher: typeof fetch = globalThis.fetch, timeoutMs = 15000) {
    // Invoke the captured transport as a value, never as a method of this
    // probe. Native Window.fetch rejects a ServerSignInProbe receiver; a
    // caller's explicitly bound transport keeps its own receiver unchanged.
    this.fetcher = (input, init) => fetcher(input, init); this.timeoutMs = timeoutMs;
    if (!Number.isFinite(timeoutMs) || timeoutMs <= 0 || timeoutMs > 120000) throw new Error('Invalid probe timeout.');
  }
  getSnapshot = () => this.state;
  subscribe = (listener: () => void) => { if (this.disposed) return () => {}; this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };
  private publish(value: ServerSignInSnapshot) { if (this.disposed) return; this.state = Object.freeze(value); for (const listener of this.listeners) listener(); }
  cancel() { ++this.generation; this.controller?.abort(); this.controller = undefined; this.publish({phase: 'idle'}); }
  dispose() { this.cancel(); this.disposed = true; this.listeners.clear(); }
  async inspect(address: string): Promise<ServerSignIn | undefined> {
    if (this.disposed) throw new Error('The sign-in probe is disposed.');
    this.cancel();
    const generation = this.generation, controller = this.controller = new AbortController();
    let timer: ReturnType<typeof setTimeout> | undefined;
    const current = () => !this.disposed && generation === this.generation && !controller.signal.aborted;
    try {
      const origin = routeOrigin(address); this.publish({phase: 'probing', origin});
      const pending = (async () => {
        const response = await this.fetcher(origin + routePath('get_system'), {method: 'GET', redirect: 'error', credentials: 'omit', headers: {Accept: 'application/json'}, signal: controller.signal});
        if (!current()) { void response.body?.cancel().catch(() => {}); return; }
        if (!response.ok) throw new ApiError(response.status, 'system_unavailable', 'The server could not report how to sign in.', response.status >= 500);
        return parseServerSignIn(await routeJSON(response, controller.signal, 8192));
      })();
      const server = await Promise.race([pending, new Promise<never>((_, reject) => {
        const abort = () => reject(new RouteError('cancelled', 'Server selection was cancelled.'));
        controller.signal.addEventListener('abort', abort, {once: true});
        timer = setTimeout(() => { reject(new RouteError('probe_timeout', 'The server did not answer. Try again.')); controller.abort(); }, this.timeoutMs);
      })]);
      if (!current() || !server) return;
      this.publish({phase: 'ready', origin, server, destination: server.setupRequired ? 'setup' : server.signIn === 'portico' ? 'portico-account' : 'local-account'});
      return server;
    } catch (error) {
      if (!this.disposed && generation === this.generation) this.publish({phase: 'error', error});
      return undefined;
    } finally { clearTimeout(timer); if (this.controller === controller) this.controller = undefined; }
  }
}
