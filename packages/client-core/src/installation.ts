import {routePrefix} from "../../contracts/src/routes.ts";
/**
 * The installation claim a first-party client sends when it asks a server for credentials
 * (lane C, 939a746). The server binds the new session family to the device record for this
 * installation, and `/v1/auth/refresh` later requires the same installation id, so an app must
 * set this once at startup, before any sign-in, and never change it for the life of the install.
 *
 * The id is a random per-installation secret (web: a UUID in local storage; Apple: the
 * installation id the native bridge keeps in the Keychain), never a hardware identifier.
 */
export type InstallationIdentity = Readonly<{installationId: string; name: string; platform: string; app: string; appVersion: string}>;

let current: InstallationIdentity | undefined;

/** Server rule: 32–128 characters of `[A-Za-z0-9_-]`. */
export function validInstallationId(value: unknown): value is string {
  return typeof value === 'string' && value.length >= 32 && value.length <= 128 && /^[A-Za-z0-9_-]+$/.test(value);
}

/** Header values must be ASCII for fetch ("Justin’s iPhone" would throw), so names are folded. */
const clean = (value: string, max: number) => value.normalize('NFKD').replace(/[̀-ͯ]/g, '').replace(/[‘’]/g, "'").replace(/[“”]/g, '"').replace(/[^\x20-\x7e]/g, '').replace(/\s+/g, ' ').trim().slice(0, max);

/** Set the claim (apps call this at startup). An invalid id is refused, so sign-in never sends a claim the server would reject. */
export function setInstallationIdentity(identity: InstallationIdentity | undefined): void {
  if (identity === undefined) { current = undefined; return; }
  if (!validInstallationId(identity.installationId)) throw new Error('The installation id must be 32 to 128 letters, digits, dashes or underscores.');
  current = Object.freeze({
    installationId: identity.installationId,
    name: clean(identity.name, 120),
    platform: clean(identity.platform, 64),
    app: clean(identity.app, 64),
    appVersion: clean(identity.appVersion, 64),
  });
}

export function installationIdentity(): InstallationIdentity | undefined {
  return current;
}

/** Paths that issue credentials: the claim travels only with these. */
export function issuesCredentials(path: string, method: string): boolean {
  return method.toUpperCase() === 'POST' && /^\/v1\/(?:direct\/|auth\/|dev\/e2e\/sign-in(?:\?|$)|quick-connect(?:\/|\?|$)|device-authorizations(?:\/|\?|$)|sessions(?:\?|$)|setup(?:\?|$)|access\/invitations\/accept(?:\?|$)|hosted\/(?:attach|profiles\/offline-select)(?:\?|$))/.test(path);
}

/** The request headers for the claim (empty until an app sets it). */
export function installationHeaders(): Record<string, string> {
  if (!current) return {};
  return {
    'X-Portico-Installation-Id': current.installationId,
    ...(current.name ? {'X-Portico-Device-Name': current.name} : {}),
    ...(current.platform ? {'X-Portico-Device-Platform': current.platform} : {}),
    ...(current.app ? {'X-Portico-App': current.app} : {}),
    ...(current.appVersion ? {'X-Portico-App-Version': current.appVersion} : {}),
  };
}

/**
 * Serializes credential rotation across every context sharing one credential store (browser tabs
 * share IndexedDB; apps set `navigator.locks` here). The default serializes within this runtime.
 */
export type CredentialLock = <T>(name: string, work: () => Promise<T>) => Promise<T>;
const tails = new Map<string, Promise<unknown>>();
const inProcess: CredentialLock = (name, work) => {
  const run = (tails.get(name) ?? Promise.resolve()).catch(() => {}).then(work);
  tails.set(name, run);
  void run.finally(() => { if (tails.get(name) === run) tails.delete(name); }).catch(() => {});
  return run;
};
let lock: CredentialLock = inProcess;
export function setCredentialLock(next: CredentialLock | undefined): void { lock = next ?? inProcess; }
export function credentialLock(): CredentialLock { return lock; }

/**
 * Requests that prove identity themselves (sign-in, refresh, attach): a 401 there is an answer,
 * never a reason to refresh and resend (resending a password would count twice toward lockout).
 */
export function provesIdentity(path: string, method: string): boolean {
  return path.startsWith(routePrefix("get_auth_capabilities", 2)) || (method.toUpperCase() === 'POST' && (/^\/v1\/(?:direct\/(?:sign-in|two-factor)|sessions(?:\?|$)|setup(?:\?|$)|hosted\/(?:attach|profiles\/offline-select)(?:\?|$))/.test(path)
    // Binding this sign-in to this device record: a 401 means the family belongs to another device
    // (a Quick Connect grant), which is an answer, never a reason to renew the sign-in.
    || /^\/v1\/devices\/[A-Za-z0-9_-]{1,128}\/sessions\/bind$/.test(path)));
}
