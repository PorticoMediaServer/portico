import {readCanonicalClaimProof, canonicalClaimTimestamp, canonicalClaimCounter} from './claim-proof.ts';
import {validInstallationId} from './installation.ts';
import {routeBase64, routeBytes, routeOrigin, sameServerPin, serverPin, webRouteCrypto, type RouteCrypto, type ServerPin} from './route-identity.ts';
import {setupAddressAllowed} from './setup-address.ts';
import {CodedError} from './server-messages.ts';

const claimFields = ['kind', 'audience', 'operationId', 'serverId', 'accountId', 'publicKey', 'localGeneration', 'name', 'address', 'purpose', 'currentOwnerAccountId', 'previousOwnerRole', 'returnRegistrationId', 'issuedAt', 'expiresAt'] as const;
const setupFields = ['kind', 'audience', 'claimHash', 'operationId', 'serverId', 'publicKey', 'localGeneration', 'address', 'purpose', 'setupRequestId', 'setupRevision', 'state', 'codeChallenge', 'installationId', 'serverChallenge', 'issuedAt', 'expiresAt'] as const;
export type SetupStartFacts = Readonly<Record<typeof setupFields[number], string>>;
export type SetupStartExpectation = Readonly<{
  pin: ServerPin; origin: string; hostedOrigin: string; operationId: string; localGeneration: string;
  setupRequestId: string; setupRevision: string; state: string; codeChallenge: string; installationId: string;
}>;
function invalid(): never {throw new CodedError('invalid_return', 'This setup continuation no longer matches its captured request.');}
const timestamp = (value: string) => canonicalClaimTimestamp(value, invalid);

/** Verify only immutable setup proof binding. The setup token is deliberately
 * absent: current request/revision authority, challenge consumption and final
 * owner admission remain Server operations, separate from switch authority. */
export async function verifySetupClaimStart(original: unknown, signedStart: unknown, expected: SetupStartExpectation, now = Date.now(), crypto: RouteCrypto = webRouteCrypto) {
  expected = Object.freeze({...expected, pin: Object.freeze({...expected.pin})});
  if (!Number.isSafeInteger(now) || !canonicalClaimCounter(expected.localGeneration) || !canonicalClaimCounter(expected.setupRevision) || expected.setupRevision === '0' ||
    !/^[A-Za-z0-9_-]{1,128}$/.test(expected.operationId) || !/^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/.test(expected.setupRequestId) || !validInstallationId(expected.installationId)) invalid();
  if (routeOrigin(expected.origin) !== expected.origin || routeOrigin(expected.hostedOrigin) !== expected.hostedOrigin || !expected.hostedOrigin.startsWith('https://')) invalid();
  if (!setupAddressAllowed(expected.origin)) invalid();
  routeBytes(expected.state, 32); routeBytes(expected.codeChallenge, 32);
  const c = readCanonicalClaimProof(original, claimFields, invalid), s = readCanonicalClaimProof(signedStart, setupFields, invalid), claim = c.facts, start = s.facts;
  if (claim.kind !== 'portico.claim.web.v2' || start.kind !== 'portico.setup-start.v1' || claim.purpose !== 'setup' || start.purpose !== 'setup' ||
    claim.accountId !== '' || claim.currentOwnerAccountId !== '' || claim.previousOwnerRole !== '' || claim.returnRegistrationId !== '' ||
    claim.audience !== expected.hostedOrigin || start.audience !== claim.audience || claim.address !== expected.origin || start.address !== claim.address ||
    claim.operationId !== expected.operationId || start.operationId !== claim.operationId || claim.localGeneration !== expected.localGeneration || start.localGeneration !== claim.localGeneration ||
    claim.serverId !== expected.pin.serverId || start.serverId !== claim.serverId || claim.publicKey !== expected.pin.publicKey || start.publicKey !== claim.publicKey ||
    !claim.name || new TextEncoder().encode(claim.name).length > 120 || start.setupRequestId !== expected.setupRequestId || start.setupRevision !== expected.setupRevision ||
    start.state !== expected.state || start.codeChallenge !== expected.codeChallenge || start.installationId !== expected.installationId || !/^[A-Za-z0-9_-]{32,128}$/.test(start.serverChallenge)) invalid();
  const issued = timestamp(claim.issuedAt), expires = timestamp(claim.expiresAt), startIssued = timestamp(start.issuedAt), startExpires = timestamp(start.expiresAt), nowNS = BigInt(now) * 1000000n;
  if (issued > nowNS + 60000000000n || expires <= nowNS || expires <= issued || expires - issued > 1800000000000n || startIssued < issued || startIssued > nowNS + 60000000000n ||
    startExpires <= nowNS || startExpires <= startIssued || startExpires - startIssued > 1800000000000n || startExpires > expires) invalid();
  if (routeBase64(await crypto.sha256(c.raw)) !== start.claimHash) invalid();
  routeBytes(start.claimHash, 32);
  const pin = await serverPin(claim.serverId, claim.publicKey, expected.pin.fingerprint, crypto);
  if (!sameServerPin(pin, expected.pin) || !await crypto.verify(routeBytes(pin.publicKey, 32), c.signature, c.raw) || !await crypto.verify(routeBytes(pin.publicKey, 32), s.signature, s.raw)) invalid();
  return Object.freeze({claim, start});
}
