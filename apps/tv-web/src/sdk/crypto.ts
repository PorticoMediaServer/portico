/**
 * `@amazon-devices/keplercrypto` in a TV browser. The bridge asks it for randomness only when the
 * runtime has no `crypto` of its own, which a browser always has; the PIN key derivation is
 * replaced as a whole (`overrides/vega/bridge/kdf.ts`).
 */
export class WebCrypto {
  readonly crypto = globalThis.crypto;
}
