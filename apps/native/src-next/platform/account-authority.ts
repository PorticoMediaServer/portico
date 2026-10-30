import {NativeModules} from 'react-native';

/** Hosted (Portico Account) origin. Production unless the build injects one. */
export const hostedUrl: string = (NativeModules.PorticoRuntime?.hostedUrl as string | undefined) ?? 'https://web.getportico.tv';

const accountOrigin = (() => { try { return new URL(hostedUrl).origin; } catch { return undefined; } })();

/**
 * Whether a request goes to the Portico Account service. That service is reached over ordinary,
 * publicly trusted TLS only: an owner's own certificate is for the owner's server, never for the
 * account authority, so certificate recovery never runs for it (NS-02). The native trust code
 * refuses to pin or accept one there as well.
 */
export function isAccountAuthority(url: string): boolean {
  if (!accountOrigin) return false;
  try { return new URL(url).origin === accountOrigin; } catch { return false; }
}
