import {readCanonicalClaimProof, canonicalClaimTimestamp, canonicalClaimCounter} from './claim-proof.ts';
import {validInstallationId} from './installation.ts';
import {routeBase64, routeBytes, routeOrigin, sameServerPin, serverPin, webRouteCrypto, type RouteCrypto, type ServerPin} from './route-identity.ts';
import {CodedError} from './server-messages.ts';

export type OwnerSignedProof = Readonly<{payload: string; signature: string}>;
const claimFields = ['kind', 'audience', 'operationId', 'serverId', 'accountId', 'publicKey', 'localGeneration', 'name', 'address', 'purpose', 'currentOwnerAccountId', 'previousOwnerRole', 'returnRegistrationId', 'issuedAt', 'expiresAt'] as const;
const startFields = ['kind', 'audience', 'claimHash', 'operationId', 'serverId', 'publicKey', 'localGeneration', 'address', 'purpose', 'state', 'codeChallenge', 'installationId', 'serverChallenge', 'issuedAt', 'expiresAt'] as const;
type ClaimFacts = Readonly<Record<typeof claimFields[number], string>>;
export type OwnerStartFacts = Readonly<Record<typeof startFields[number], string>>;
export type OwnerStartExpectation = Readonly<{
  pin: ServerPin; origin: string; hostedOrigin: string; operationId: string; localGeneration: string;
  state: string; codeChallenge: string; installationId: string;
  /** Absent means the original Local -> Portico switch. A change-owner handoff
   * binds the current owner's Hosted account and the role they keep. */
  purpose?: 'switch' | 'change-owner';
  currentOwnerAccountId?: string;
  previousOwnerRole?: 'admin' | 'member' | 'remove';
}>;
function invalid(): never {throw new CodedError('invalid_return', 'This owner continuation no longer matches its captured operation.');}
const readProof = <T extends readonly string[]>(value: unknown, fields: T) => readCanonicalClaimProof(value, fields, invalid);
const timestamp = (value: string) => canonicalClaimTimestamp(value, invalid);
const counter = canonicalClaimCounter;

/** Verify the actual canonical MAIN OwnerStart protocol before saving a
 * browser handoff. Both original signed envelopes remain unchanged. This
 * proves immutable binding only; it grants no claim, account or admission.
 * The browser separately enforces its secure context/protected store and the
 * Server owns current family, reservation, install and one-spend authority. */
export async function verifyOwnerClaimStart(original: unknown, signedStart: unknown, expected: OwnerStartExpectation, now = Date.now(), crypto: RouteCrypto = webRouteCrypto): Promise<Readonly<{claim: ClaimFacts; start: OwnerStartFacts}>> {
  // Capture scalar expectations before the first cryptographic await. A caller
  // reusing its selection object cannot retarget this verification in flight.
  expected = Object.freeze({...expected, pin: Object.freeze({...expected.pin})});
  if (!Number.isSafeInteger(now) || !counter(expected.localGeneration) || !/^[A-Za-z0-9_-]{1,128}$/.test(expected.operationId) || !validInstallationId(expected.installationId)) invalid();
  if (routeOrigin(expected.origin) !== expected.origin || routeOrigin(expected.hostedOrigin) !== expected.hostedOrigin || !expected.hostedOrigin.startsWith('https://')) invalid();
  const address = new URL(expected.origin);
  if (address.protocol !== 'https:' && !(address.protocol === 'http:' && address.hostname === 'localhost')) invalid();
  routeBytes(expected.state, 32); routeBytes(expected.codeChallenge, 32);
  // Persisted expectations are runtime data, not the TypeScript union. Reject
  // any purpose other than the two the protocol signs, then validate the facts
  // that purpose is allowed to carry.
  const purpose = expected.purpose ?? 'switch';
  if (purpose !== 'switch' && purpose !== 'change-owner') invalid();
  if (purpose === 'switch'
    ? expected.currentOwnerAccountId !== undefined || expected.previousOwnerRole !== undefined
    : typeof expected.currentOwnerAccountId !== 'string' || !/^[A-Za-z0-9_-]{1,128}$/.test(expected.currentOwnerAccountId) ||
      typeof expected.previousOwnerRole !== 'string' || !['admin', 'member', 'remove'].includes(expected.previousOwnerRole)) invalid();
  const c = readProof(original, claimFields), s = readProof(signedStart, startFields), claim = c.facts, start = s.facts;
  const purposeBound = purpose === 'switch'
    ? claim.purpose === 'switch' && start.purpose === 'switch' && claim.currentOwnerAccountId === '' && claim.previousOwnerRole === ''
    : claim.purpose === 'change-owner' && start.purpose === 'change-owner' &&
      claim.currentOwnerAccountId === expected.currentOwnerAccountId && claim.previousOwnerRole === expected.previousOwnerRole;
  if (claim.kind !== 'portico.claim.web.v2' || start.kind !== 'portico.owner-start.v1' || !purposeBound ||
    claim.accountId !== '' || claim.returnRegistrationId !== '' ||
    claim.audience !== expected.hostedOrigin || start.audience !== claim.audience || claim.address !== expected.origin || start.address !== claim.address ||
    claim.operationId !== expected.operationId || start.operationId !== claim.operationId || claim.localGeneration !== expected.localGeneration || start.localGeneration !== claim.localGeneration ||
    claim.serverId !== expected.pin.serverId || start.serverId !== claim.serverId || claim.publicKey !== expected.pin.publicKey || start.publicKey !== claim.publicKey ||
    !claim.name || new TextEncoder().encode(claim.name).length > 120 || start.state !== expected.state || start.codeChallenge !== expected.codeChallenge || start.installationId !== expected.installationId ||
    !/^[A-Za-z0-9_-]{32,128}$/.test(start.serverChallenge)) invalid();
  const issued = timestamp(claim.issuedAt), expires = timestamp(claim.expiresAt), startIssued = timestamp(start.issuedAt), startExpires = timestamp(start.expiresAt), nowNS = BigInt(now) * 1000000n;
  if (issued > nowNS + 60000000000n || expires <= nowNS || expires <= issued || expires - issued > 600000000000n || startIssued < issued || startIssued > nowNS + 60000000000n ||
    startExpires <= nowNS || startExpires <= startIssued || startExpires - startIssued > 300000000000n || startExpires > expires) invalid();
  if (routeBase64(await crypto.sha256(c.raw)) !== start.claimHash) invalid();
  routeBytes(start.claimHash, 32);
  const pin = await serverPin(claim.serverId, claim.publicKey, expected.pin.fingerprint, crypto);
  if (!sameServerPin(pin, expected.pin) || !await crypto.verify(routeBytes(pin.publicKey, 32), c.signature, c.raw) || !await crypto.verify(routeBytes(pin.publicKey, 32), s.signature, s.raw)) invalid();
  return Object.freeze({claim, start});
}
