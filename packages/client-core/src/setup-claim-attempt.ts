import {routeBytes} from './route-identity.ts';

/** A setup with a Portico Account has 30 minutes end to end: signing up and verifying an email takes longer than five. */
export const SETUP_CLAIM_LIFETIME_MS = 1_800_000;

/** The approval code in a Portico Account claim URL bound to this attempt's hosted
 * origin, operation and server name, or undefined. A malformed code throws. */
export function setupApprovalCode(url: string, expected: Readonly<{hostedOrigin: string; operationId: string; serverName: string}>): string | undefined {
  const parsed = new URL(url), fragment = new URLSearchParams(parsed.hash.slice(1)), code = fragment.get('code') ?? '';
  if (parsed.origin !== expected.hostedOrigin || parsed.pathname !== '/claim' || parsed.search || parsed.username || parsed.password
    || [...fragment.keys()].sort().join(',') !== 'code,operation,server' || fragment.get('operation') !== expected.operationId
    || fragment.get('server') !== expected.serverName || !code.startsWith('claim_')) return undefined;
  routeBytes(code.slice(6), 32);
  return code;
}
