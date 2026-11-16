/**
 * `@amazon-devices/keplercrypto` in a TV browser. The bridge asks it for randomness only when the
 * runtime has no `crypto` of its own, which a browser always has. The SDK's native key derivation
 * does not exist here: `Crypto` refuses, and the PIN key derivation that uses it is replaced as a
 * whole by one on the browser's Web Crypto (`overrides/vega/bridge/kdf.ts`). (Were it not, the
 * Fire TV derivation falls back to its exact JavaScript one when `Crypto` refuses.)
 */
export class WebCrypto {
  readonly crypto = globalThis.crypto;
}

export const DigestAlgorithm = {SHA256: 'SHA-256'} as const;
export const KeyPurpose = {DERIVE: 'derive'} as const;

export class Crypto {
  constructor() {
    throw new Error('The SDK key derivation is not available in a TV browser.');
  }
  // The shapes the Fire TV derivation calls; never reached, since construction refuses.
  makeSymmetricKeyBuilder(): any {return undefined;}
  makePbkdf2ContextBuilder(): any {return undefined;}
  getDigestAlgorithmByName(_name: string): any {return undefined;}
}
