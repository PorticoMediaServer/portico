// The SDK's own random source, for a runtime that has no `crypto` of its own.
const system = globalThis.crypto;
export class WebCrypto { crypto = system; }
// Runtime transport tests do not use native derivation; that boundary is unavailable here.
export class Crypto { constructor() { throw new Error('Native derivation is unavailable in this test.'); } }
export const DigestAlgorithm = {SHA256: 'SHA256'};
export const KeyPurpose = {DERIVE: 'DERIVE'};
