import {serverPath} from './query/server-api.ts';
import {ApiError, type SystemInfo} from './index.ts';
import {RouteError, routeOrigin, routeJSON} from './route-identity.ts';
import {API_LEVEL} from '../../contracts/src/api-level.ts';
import type {SignInKind} from './app-sessions.ts';
import {serverNow} from './server-clock.ts';

export type ServerSignIn = Readonly<SystemInfo & {signIn: SignInKind}>;
const object = (v: unknown): v is Record<string, unknown> => !!v && typeof v === 'object' && !Array.isArray(v);
const text = (v: unknown, max: number): v is string => typeof v === 'string' && v.length > 0 && v.length <= max && !/[\p{Cc}\p{Cf}]/u.test(v);
/** Reads `GET /v1/system` (`System` in the generated contract). What this app must know is who
 * the server is, how to sign in and whether setup is pending; anything else is ignored. A server
 * that names no way to sign in this app knows never gets a password form. The version is told only
 * to a signed-in caller, so it is carried when present. */
export function parseServerSignIn(raw: unknown): ServerSignIn {
  const methods = object(raw) && Array.isArray(raw.signInMethods) ? raw.signInMethods : [];
  const signIn: SignInKind | undefined = methods.includes('portico') ? 'portico' : methods.includes('password') ? 'local' : undefined;
  if (!object(raw) || !text(raw.id, 256) || !text(raw.name, 120) || !signIn || typeof raw.setupRequired !== 'boolean' || !Number.isSafeInteger(raw.apiMajor) || (raw.apiMajor as number) < 1 ||
      !Array.isArray(raw.capabilities) || raw.capabilities.length > 1024)
    throw new RouteError('invalid_system', 'This server did not report how to sign in.');
  const major = raw.apiMajor as number;
  const capabilities = Object.freeze([...new Set(raw.capabilities.filter((value): value is string => text(value, 128)))].sort());
  return Object.freeze({id: raw.id, name: raw.name, signIn, setupRequired: raw.setupRequired, move: null, moveEndsAt: null, hostedConfigured: false, hostedAttached: false, apiLevel: major, apiVersion: major + '.0', apiMajors: Object.freeze([major]), capabilities,
    ...(text(raw.version, 256) ? {version: raw.version} : {})});
}
/** Whole calendar days left to use the old sign-in during a move, counted on this device's
 * calendar: 0 when it ends today, 1 when it ends tomorrow. Undefined when nothing is offered. */
export function moveOfferDays(endsAt: string | null | undefined, now: Date = new Date(serverNow())): number | undefined {
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
        const response = await this.fetcher(origin + serverPath('GET /v1/system'), {method: 'GET', redirect: 'error', credentials: 'omit', headers: {Accept: 'application/json'}, signal: controller.signal});
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
