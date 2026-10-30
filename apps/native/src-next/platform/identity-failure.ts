import type {AppServerSession} from '../../../../packages/client-core/src/app-sessions';
import type {NativeServerBinding} from './forget-server';

export type NativeIdentityServer = NativeServerBinding & Readonly<{name: string; signIn: AppServerSession['signIn']}>;
const identityCodes = new Set(['identity_mismatch', 'certificate_unverified', 'certificate_untrusted']);

/** Captured public display and exact family only. No bearer, address or raw
 * transport diagnostic can cross into the blocking identity view. */
export class NativeIdentityFailure extends Error {
  readonly code: string;
  readonly server: NativeIdentityServer;
  readonly changed: boolean;
  constructor(code: string, server: NativeIdentityServer) {
    super('native_server_identity_unconfirmed'); this.name = 'NativeIdentityFailure';
    this.code = code; this.server = Object.freeze({...server});
    this.changed = code === 'identity_mismatch' && server.signIn === 'local';
  }
}
export function nativeIdentityFailure(error: unknown, server?: NativeIdentityServer): NativeIdentityFailure | undefined {
  const code = error && typeof error === 'object' && 'code' in error ? error.code : undefined;
  return server && typeof code === 'string' && identityCodes.has(code) ? new NativeIdentityFailure(code, server) : undefined;
}

export function nativeRouteIdentityFailure(route: Readonly<{phase: string; errorCode?: unknown}>, server: NativeIdentityServer) {
  if (route.phase === 'identity_mismatch') return new NativeIdentityFailure('identity_mismatch', server);
  if (route.phase === 'unconfirmed') return new NativeIdentityFailure(
    typeof route.errorCode === 'string' && identityCodes.has(route.errorCode) ? route.errorCode : 'certificate_unverified', server);
  return undefined;
}
