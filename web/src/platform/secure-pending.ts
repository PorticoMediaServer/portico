import {RouteError} from '@core/route-identity.ts';
import {BrowserProtectedStore} from './server-connections';

type PendingStorage<T> = Pick<BrowserProtectedStore<T>, 'read' | 'change'>;
export type SecurePendingEnvironment = Readonly<{protocol: string; hostname: string; secureContext: boolean; encryption: boolean; indexedDB: boolean; locks: boolean}>;
const browserEnvironment = (): SecurePendingEnvironment => ({protocol: location.protocol, hostname: location.hostname, secureContext: globalThis.isSecureContext === true,
  encryption: Boolean(globalThis.crypto?.subtle), indexedDB: typeof indexedDB !== 'undefined',
  // The LAN compatibility lock serializes only this tab. It cannot protect a
  // continuation shared by two tabs and must never satisfy this precondition.
  locks: typeof LockManager !== 'undefined' && navigator.locks instanceof LockManager});

/** Ruling 17.5: network HTTP refuses before preview/pending/handoff. The
 * explicit localhost exception still requires a real secure browser context. */
export function ownerSwitchContextAllowed(env: Pick<SecurePendingEnvironment, 'protocol' | 'hostname' | 'secureContext'> = browserEnvironment()) {
  return env.secureContext && (env.protocol === 'https:' || env.protocol === 'http:' && env.hostname === 'localhost');
}

/** Storage precondition only, not an owner protocol or authority validator.
 * HTTPS/CSP supply page integrity; AES protects disk dumps, never same-origin JS.
 * Constructing this object is inert, including on an HTTP server. */
export class SecurePendingStorage<T> {
  private storage?: PendingStorage<T>;
  constructor(private readonly create: () => PendingStorage<T>, private readonly environment = browserEnvironment) {}
  private requireSecure() {
    const env = this.environment();
    if (!ownerSwitchContextAllowed(env) || !env.encryption || !env.indexedDB || !env.locks)
      throw new RouteError('secure_context_required', 'This continuation requires a secure browser context.');
  }
  private store() {this.requireSecure(); return this.storage ??= this.create();}
  /** Check before creating even a tab locator or issuing a preparation request. */
  assertAvailable() {this.requireSecure();}
  async read() {const value = await this.store().read(); this.requireSecure(); return value;}
  async change(update: (current: T | undefined) => T | undefined, beforeCommit?: () => void) {
    await this.store().change(current => {this.requireSecure(); return update(current);}, () => {this.requireSecure(); beforeCommit?.();});
    this.requireSecure();
  }
}
/** No plaintext fallback or old session-store migration is allowed for pending secrets. */
export function secureBrowserPending<T>(databaseName: string) {
  return new SecurePendingStorage<T>(() => new BrowserProtectedStore<T>(databaseName, 128 * 1024, true));
}
