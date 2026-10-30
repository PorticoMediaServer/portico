import {RouteError} from '@core/route-identity.ts';
import {BrowserProtectedStore} from './server-connections';

type PendingStorage<T> = Pick<BrowserProtectedStore<T>, 'read' | 'change'>;
export type SetupPendingEnvironment = Readonly<{indexedDB: boolean; locks: boolean}>;
const browserEnvironment = (): SetupPendingEnvironment => ({indexedDB: typeof indexedDB !== 'undefined',
  // The tab-lock polyfill counts: setup is one tab's continuation, and plain HTTP has no Web Locks.
  locks: typeof navigator !== 'undefined' && typeof navigator.locks?.request === 'function'});

/** Setup's pending storage (PLEX-SETUP-NOTES S2): encrypted where WebCrypto exists, a short-lived
 * plain record where it does not (plain-HTTP LAN setup). It needs IndexedDB and locks, never a
 * secure context. Owner-switch, account-security and sign-in pending state keep `secureBrowserPending`. */
export class SetupPendingStorage<T> {
  private storage?: PendingStorage<T>;
  constructor(private readonly create: () => PendingStorage<T>, private readonly environment = browserEnvironment) {}
  private require() {
    const env = this.environment();
    if (!env.indexedDB || !env.locks) throw new RouteError('storage_unavailable', 'This browser cannot keep setup progress.');
  }
  private store() {this.require(); return this.storage ??= this.create();}
  assertAvailable() {this.require();}
  async read() {return this.store().read();}
  async change(update: (current: T | undefined) => T | undefined, beforeCommit?: () => void) {await this.store().change(update, beforeCommit);}
}
export function setupBrowserPending<T>(databaseName: string) {
  return new SetupPendingStorage<T>(() => new BrowserProtectedStore<T>(databaseName, 128 * 1024, false));
}
