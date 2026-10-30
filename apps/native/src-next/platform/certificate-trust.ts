import {
  RouteError, probeRoute, routeOrigin, observedRouteTLSLeaf, identityCode, normaliseIdentityCode,
  type RouteCrypto, type RouteProof,
} from '../../../../packages/client-core/src/route-identity.ts';

export type PinnedCertificate = Readonly<{fingerprint: string; serverId: string}>;
export interface CertificatePlatform {
  refused(origin: string): Promise<string | undefined>;
  pinned(origin: string): Promise<PinnedCertificate | undefined>;
  pin(origin: string, pin: PinnedCertificate): Promise<void>;
  /** Noncredential POST proof, under exactly this observed TLS leaf. */
  proofFetcher(origin: string, fingerprint: string): typeof fetch;
  observedTLSLeaf?: (response: Response) => string | undefined;
}
export type CertificateIdentityContext = Readonly<{expected?: string; home: boolean; knownServerIds: readonly string[]}>;
export type CertificateContext = CertificateIdentityContext | ((proof: RouteProof, certificateFingerprint: string) => Promise<CertificateIdentityContext>);
export type PendingCertificate = Readonly<{origin: string; fingerprint: string; serverId: string; proof: RouteProof}>;
const FINGERPRINT = /^[A-Za-z0-9_-]{43}$/;
const UNPROVEN = new Set(['identity_mismatch', 'invalid_identity', 'invalid_response', 'invalid_signature']);
const pending = new Map<string, PendingCertificate>();
const tails = new Map<string, Promise<void>>();
const cancelled = () => new RouteError('cancelled', 'certificate_decision_cancelled');
const unverified = () => new RouteError('certificate_unverified', 'certificate_unverified');

export function certificateOrigin(url: string): string | undefined {
  try {const value = new URL(url); return value.protocol === 'https:' ? routeOrigin(value.origin) : undefined;} catch {return undefined;}
}

/** Each caller keeps its captured authority. Calls at an origin serialize; an unrelated
 * expected server never inherits another caller's pending/certificate decision. */
export function settleCertificate(origin: string, platform: CertificatePlatform, crypto: RouteCrypto, context: CertificateContext, signal?: AbortSignal): Promise<void> {
  origin = routeOrigin(origin);
  const work = (tails.get(origin) ?? Promise.resolve()).catch(() => {}).then(() => decide(origin, platform, crypto, context, signal));
  tails.set(origin, work);
  void work.finally(() => {if (tails.get(origin) === work) tails.delete(origin);}).catch(() => {});
  return work;
}

/** Identity precedes certificate admission. A home decision comes only from the
 * canonical same-proof Bonjour/TLS conjunction. Away requires a known signed
 * identity or the separately supplied code, always bound to this observed leaf. */
export async function decide(origin: string, platform: CertificatePlatform, crypto: RouteCrypto, context: CertificateContext, signal?: AbortSignal): Promise<void> {
  const current = () => {if (signal?.aborted) throw cancelled();};
  current();
  const fingerprint = await platform.refused(origin); current();
  if (!fingerprint || !FINGERPRINT.test(fingerprint)) throw new RouteError('certificate_untrusted', 'certificate_untrusted');
  const previous = await platform.pinned(origin); current();
  let proof: RouteProof;
  try {proof = await probeRoute(origin, undefined, {fetcher: platform.proofFetcher(origin, fingerprint), observedTLSLeaf: platform.observedTLSLeaf, crypto, signal});}
  catch (error) {if (error instanceof RouteError && UNPROVEN.has(error.code)) throw unverified(); throw error;}
  current();
  if (!proof.serverId) throw unverified();
  const decision = typeof context === 'function' ? await context(proof, fingerprint) : context; current();
  if ((previous && previous.serverId !== proof.serverId) || (decision.expected && decision.expected !== proof.serverId))
    throw new RouteError('identity_mismatch', 'identity_mismatch');
  const bound = observedRouteTLSLeaf(origin, proof) === fingerprint && proof.tlsCertificateFingerprints?.includes(fingerprint) === true;
  if (bound && (decision.home || previous?.serverId === proof.serverId || decision.expected === proof.serverId || decision.knownServerIds.includes(proof.serverId))) {
    current(); await platform.pin(origin, {fingerprint, serverId: proof.serverId}); current(); pending.delete(origin); return;
  }
  if (!bound) throw unverified();
  pending.set(origin, Object.freeze({origin, fingerprint, serverId: proof.serverId, proof}));
  throw new RouteError('identity_code_needed', 'identity_code_needed');
}

export function pendingCertificate(origin: string): PendingCertificate | undefined {
  try {return pending.get(routeOrigin(origin));} catch {return undefined;}
}
export function clearPendingCertificate(origin: string, captured?: PendingCertificate): void {
  try {const key = routeOrigin(origin); if (!captured || pending.get(key) === captured) pending.delete(key);}
  catch { /* No valid origin means no pending decision. */ }
}
/** The expected code is computed only for comparison, never rendered, logged or sent. */
export async function confirmIdentityCode(origin: string, code: string, platform: CertificatePlatform, signal?: AbortSignal, expected?: PendingCertificate): Promise<boolean> {
  const captured = pendingCertificate(origin), normalised = normaliseIdentityCode(code);
  if (expected && captured !== expected) throw cancelled();
  if (signal?.aborted) throw cancelled();
  if (!captured || !normalised || normalised !== normaliseIdentityCode(identityCode(captured.serverId))) return false;
  if (pending.get(captured.origin) !== captured) throw cancelled();
  await platform.pin(captured.origin, {fingerprint: captured.fingerprint, serverId: captured.serverId});
  if (signal?.aborted || pending.get(captured.origin) !== captured) throw cancelled();
  pending.delete(captured.origin); return true;
}
/** Test seam. Current product never restores pending decisions from storage. */
export function resetCertificateDecisions(): void {pending.clear(); tails.clear();}

/** Pin inventory is headless cleanup data; no certificate-management UI consumes it. */
export type SavedCertificate = Readonly<{address: string; fingerprint: string; serverId: string}>;
export function savedCertificateOrigin(address: string): string {
  const at = address.lastIndexOf(':'), host = address.slice(0, at), port = address.slice(at + 1);
  return `https://${host.includes(':') ? `[${host}]` : host}${port === '443' ? '' : `:${port}`}`;
}
export function parseSavedCertificates(raw: unknown): SavedCertificate[] {
  if (!Array.isArray(raw)) return [];
  return raw.flatMap((value): SavedCertificate[] => {
    const item = value as Record<string, unknown> | null;
    if (!item || typeof item.address !== 'string' || !/^[^\s]+:\d{1,5}$/.test(item.address) || typeof item.fingerprint !== 'string' || !FINGERPRINT.test(item.fingerprint) ||
        typeof item.serverId !== 'string' || !/^srv_[A-Za-z0-9_-]{43}$/.test(item.serverId)) return [];
    return [{address: item.address, fingerprint: item.fingerprint, serverId: item.serverId}];
  });
}
